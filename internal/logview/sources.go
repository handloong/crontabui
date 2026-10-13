package logview

import (
	"path/filepath"
	"strings"
)

// Source 描述一个系统 cron 日志来源：要么是文件，要么是要执行的命令。
type Source struct {
	Name string   `json:"name"`
	Path string   `json:"path"`
	Args []string `json:"-"`
}

// systemLogCandidates 是系统 cron 日志的探测顺序。
var systemLogCandidates = []string{
	"/var/log/cron",
	"/var/log/syslog",
	"/var/log/messages",
}

// DetectSystemSource 依次探测：显式指定路径 → 常见日志文件 → journalctl。
// exists 与 lookPath 由调用方注入，便于测试。
func DetectSystemSource(explicit string, exists func(string) bool, lookPath func(string) (string, error)) *Source {
	if explicit != "" && exists(explicit) {
		return &Source{Name: explicit, Path: explicit}
	}
	for _, candidate := range systemLogCandidates {
		if exists(candidate) {
			return &Source{Name: filepath.Base(candidate), Path: candidate}
		}
	}
	if lookPath != nil {
		if bin, err := lookPath("journalctl"); err == nil && bin != "" {
			return &Source{
				Name: "journalctl",
				Args: []string{bin, "-u", "cron", "--no-pager", "-n", "500", "-o", "short-iso"},
			}
		}
	}
	return nil
}

// FilterCronLines 从 syslog 一类的混合日志中筛出 cron 相关行。
func FilterCronLines(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if !isCronLine(line) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func isCronLine(line string) bool {
	if line == "" {
		return false
	}
	lower := strings.ToLower(line)
	for _, marker := range []string{"cron[", "crond[", "cron:", "cron(", "crontab", "cron.service"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
