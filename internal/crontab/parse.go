package crontab

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"time"
)

// MarkerPrefix 是托管任务标记注释的前缀。标记独占一行，位于任务命令行上一行。
// 之所以不用行尾内联注释：Vixie cron 不支持行尾注释，会把 " #xxx" 当作命令的一部分。
const MarkerPrefix = "#CRONTABUI:"

// EntryKind 区分托管任务与需要原样保留的其它行。
type EntryKind int

const (
	// KindRaw 表示不由本程序管理的行（注释、空行、环境变量、手写任务等），必须逐字节保留。
	KindRaw EntryKind = iota
	// KindJob 表示由本程序管理的任务。
	KindJob
)

// Entry 是 crontab 中的一个有序条目。
type Entry struct {
	Kind EntryKind `json:"kind"`
	Raw  []string  `json:"raw,omitempty"`
	Job  *Job      `json:"job,omitempty"`
}

// Job 是一个被托管的定时任务。字段全部为可比较类型，便于整体比较。
type Job struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
	Command    string `json:"command"`
	Enabled    bool   `json:"enabled"`
	CaptureLog bool   `json:"captureLog"`
}

// LogFile 返回该任务在指定日志目录下的输出文件路径。
func (j Job) LogFile(logDir string) string {
	return logPathFor(logDir, j.ID)
}

// logPathFor 使用 Linux 风格路径拼接：crontab 中的路径必须是正斜杠，
// 因此在 Windows 上构建/测试时也不能依赖 filepath.Join 的反斜杠分隔符。
func logPathFor(logDir, id string) string {
	return strings.TrimRight(logDir, "/\\") + "/" + id + ".log"
}

// NewID 生成 8 位十六进制任务 ID。
func NewID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		binary.BigEndian.PutUint32(b[:], uint32(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b[:])
}

// Parse 把 crontab 内容解析为有序条目。任何无法确定归属的行都会降级为 KindRaw，
// 保证 Render(Parse(x)) == x（除托管任务的空白归一化外）。
func Parse(content, logDir string) ([]Entry, error) {
	lines := splitLines(content)
	entries := make([]Entry, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		id, name, isMarker := parseMarker(line)
		if !isMarker {
			entries = append(entries, rawEntry(line))
			continue
		}
		// 标记行必须紧跟一条有效命令行，否则视为普通注释，避免误判导致内容丢失。
		if i+1 >= len(lines) {
			entries = append(entries, rawEntry(line))
			continue
		}
		body := strings.TrimSpace(lines[i+1])
		if body == "" {
			entries = append(entries, rawEntry(line))
			continue
		}
		if _, _, next := parseMarker(lines[i+1]); next {
			entries = append(entries, rawEntry(line))
			continue
		}

		enabled := true
		if strings.HasPrefix(body, "#") {
			enabled = false
			body = strings.TrimSpace(strings.TrimPrefix(body, "#"))
		}
		schedule, command := splitScheduleCommand(body)
		if schedule == "" {
			entries = append(entries, rawEntry(line))
			continue
		}
		command, capture := stripCapture(command, logDir, id)
		entries = append(entries, Entry{Kind: KindJob, Job: &Job{
			ID:         id,
			Name:       name,
			Schedule:   schedule,
			Command:    command,
			Enabled:    enabled,
			CaptureLog: capture,
		}})
		i++ // 该行已被消费
	}
	return entries, nil
}

// Render 把条目渲染回 crontab 文本，未托管条目原样输出。
func Render(entries []Entry, logDir string) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.Kind {
		case KindJob:
			if e.Job == nil {
				continue
			}
			b.WriteString(markerLine(e.Job))
			b.WriteByte('\n')
			b.WriteString(commandLine(e.Job, logDir))
			b.WriteByte('\n')
		default:
			for _, line := range e.Raw {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func rawEntry(line string) Entry {
	return Entry{Kind: KindRaw, Raw: []string{line}}
}

func markerLine(j *Job) string {
	if j.Name == "" {
		return MarkerPrefix + " " + j.ID
	}
	return MarkerPrefix + " " + j.ID + " " + j.Name
}

func commandLine(j *Job, logDir string) string {
	cmd := j.Command
	if j.CaptureLog {
		cmd += " >> " + logPathFor(logDir, j.ID) + " 2>&1"
	}
	line := j.Schedule + " " + cmd
	if !j.Enabled {
		line = "#" + line
	}
	return line
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func parseMarker(line string) (id, name string, ok bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, MarkerPrefix) {
		return "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(t, MarkerPrefix))
	if rest == "" {
		return "", "", false
	}
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		id = rest[:i]
		name = strings.TrimSpace(rest[i+1:])
	} else {
		id = rest
	}
	if !validID(id) {
		return "", "", false
	}
	return id, name, true
}

func validID(id string) bool {
	if len(id) != 8 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return false
	}
	return true
}

// splitScheduleCommand 把命令行拆成 5 字段表达式与命令原文（保留命令内的原始空格）。
func splitScheduleCommand(line string) (schedule, command string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, "@") {
		i := strings.IndexAny(trimmed, " \t")
		if i < 0 {
			return trimmed, ""
		}
		return trimmed[:i], strings.TrimSpace(trimmed[i+1:])
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 6 {
		return "", ""
	}
	pos := 0
	for n := 0; n < 5; n++ {
		for pos < len(trimmed) && (trimmed[pos] == ' ' || trimmed[pos] == '\t') {
			pos++
		}
		for pos < len(trimmed) && trimmed[pos] != ' ' && trimmed[pos] != '\t' {
			pos++
		}
	}
	for pos < len(trimmed) && (trimmed[pos] == ' ' || trimmed[pos] == '\t') {
		pos++
	}
	if pos >= len(trimmed) {
		return "", ""
	}
	return strings.Join(fields[:5], " "), trimmed[pos:]
}

// stripCapture 剥离本程序自动追加的日志重定向后缀；不匹配时保持命令原样。
func stripCapture(command, logDir, id string) (string, bool) {
	if logDir == "" || id == "" {
		return command, false
	}
	suffix := " >> " + logPathFor(logDir, id) + " 2>&1"
	if strings.HasSuffix(command, suffix) {
		return strings.TrimSpace(strings.TrimSuffix(command, suffix)), true
	}
	return command, false
}
