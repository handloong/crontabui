package crontab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxBackups 是保留的 crontab 备份份数。
const maxBackups = 10

// ErrJobNotFound 表示按 ID 未找到任务，调用方可用 errors.Is 判定并返回 404。
var ErrJobNotFound = errors.New("任务不存在")

// Runner 抽象 crontab 的读写，便于测试时注入假实现。
type Runner interface {
	List(ctx context.Context) (string, error)
	Replace(ctx context.Context, content string) error
}

// Store 串行化地管理 crontab：每次修改都重新读取当前内容再合并，
// 因此不会覆盖用户在我们之外做的编辑。
type Store struct {
	runner  Runner
	logDir  string
	dataDir string
	mu      sync.Mutex
}

// NewStore 创建 Store。dataDir 为空时不写备份。
func NewStore(runner Runner, logDir, dataDir string) *Store {
	return &Store{runner: runner, logDir: logDir, dataDir: dataDir}
}

// LogDir 返回任务日志目录。
func (s *Store) LogDir() string { return s.logDir }

// List 返回当前 crontab 的全部有序条目。
func (s *Store) List(ctx context.Context) ([]Entry, error) {
	_, entries, err := s.Load(ctx)
	return entries, err
}

// Load 一次性返回 crontab 原文与解析后的条目，避免重复调用 crontab -l。
func (s *Store) Load(ctx context.Context) (string, []Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(ctx)
}

// Get 按 ID 返回任务。
func (s *Store) Get(ctx context.Context, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, entries, err := s.readLocked(ctx)
	if err != nil {
		return Job{}, err
	}
	for _, e := range entries {
		if e.Kind == KindJob && e.Job.ID == id {
			return *e.Job, nil
		}
	}
	return Job{}, fmt.Errorf("任务 %s：%w", id, ErrJobNotFound)
}

// Add 新增任务，追加到 crontab 末尾。ID 为空时自动生成。
func (s *Store) Add(ctx context.Context, job Job) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job = normalizeJob(job)
	if err := ValidateJob(job); err != nil {
		return Job{}, err
	}
	if job.ID == "" {
		job.ID = NewID()
	}
	prev, entries, err := s.readLocked(ctx)
	if err != nil {
		return Job{}, err
	}
	for _, e := range entries {
		if e.Kind == KindJob && e.Job.ID == job.ID {
			return Job{}, fmt.Errorf("任务 ID %s 已存在", job.ID)
		}
	}
	entries = append(entries, Entry{Kind: KindJob, Job: &job})
	if err := s.commitLocked(ctx, prev, entries); err != nil {
		return Job{}, err
	}
	return job, nil
}

// Update 覆盖式更新任务，保持任务在 crontab 中的原有位置。
func (s *Store) Update(ctx context.Context, id string, job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job = normalizeJob(job)
	job.ID = id
	if err := ValidateJob(job); err != nil {
		return err
	}
	prev, entries, err := s.readLocked(ctx)
	if err != nil {
		return err
	}
	idx := indexOf(entries, id)
	if idx < 0 {
		return fmt.Errorf("任务 %s：%w", id, ErrJobNotFound)
	}
	j := job
	entries[idx].Job = &j
	return s.commitLocked(ctx, prev, entries)
}

// Delete 删除任务，其它条目保持原样。
func (s *Store) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, entries, err := s.readLocked(ctx)
	if err != nil {
		return err
	}
	idx := indexOf(entries, id)
	if idx < 0 {
		return fmt.Errorf("任务 %s：%w", id, ErrJobNotFound)
	}
	entries = append(entries[:idx], entries[idx+1:]...)
	return s.commitLocked(ctx, prev, entries)
}

// Toggle 启用或停用任务。停用通过给命令行加 # 前缀实现，兼容所有 cron 实现。
func (s *Store) Toggle(ctx context.Context, id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, entries, err := s.readLocked(ctx)
	if err != nil {
		return err
	}
	idx := indexOf(entries, id)
	if idx < 0 {
		return fmt.Errorf("任务 %s：%w", id, ErrJobNotFound)
	}
	j := *entries[idx].Job
	j.Enabled = enabled
	entries[idx].Job = &j
	return s.commitLocked(ctx, prev, entries)
}

// 名称与命令的长度上限，防止超长内容撑大 crontab 与内存。
const (
	maxNameLen    = 128
	maxCommandLen = 4096
)

// ValidateJob 校验任务定义。
//
// 这里必须拒绝换行与其它控制字符：crontab 以行为单位，任何换行都会
// 让后续内容变成一条新的 crontab 条目（即通过接口注入任意定时任务）。
func ValidateJob(j Job) error {
	if strings.TrimSpace(j.Command) == "" {
		return errors.New("命令不能为空")
	}
	if j.ID != "" && !validID(j.ID) {
		return fmt.Errorf("任务 ID %q 无效：必须是 8 位小写十六进制字符", j.ID)
	}
	if err := checkSingleLine("任务名称", j.Name, maxNameLen); err != nil {
		return err
	}
	if err := checkSingleLine("执行周期", j.Schedule, 128); err != nil {
		return err
	}
	if err := checkSingleLine("命令", j.Command, maxCommandLen); err != nil {
		return err
	}
	return ValidateSchedule(j.Schedule)
}

func checkSingleLine(field, value string, maxLen int) error {
	if len([]rune(value)) > maxLen {
		return fmt.Errorf("%s过长：最多 %d 个字符", field, maxLen)
	}
	for _, r := range value {
		switch {
		case r == '\n' || r == '\r':
			return fmt.Errorf("%s不能包含换行符", field)
		case r < 0x20 && r != '\t':
			return fmt.Errorf("%s不能包含控制字符", field)
		case r == 0x7f:
			return fmt.Errorf("%s不能包含控制字符", field)
		}
	}
	return nil
}

func normalizeJob(j Job) Job {
	j.Name = strings.TrimSpace(j.Name)
	j.Schedule = strings.TrimSpace(j.Schedule)
	j.Command = strings.TrimSpace(j.Command)
	return j
}

func indexOf(entries []Entry, id string) int {
	for i, e := range entries {
		if e.Kind == KindJob && e.Job != nil && e.Job.ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) readLocked(ctx context.Context) (string, []Entry, error) {
	content, err := s.runner.List(ctx)
	if err != nil {
		return "", nil, err
	}
	entries, err := Parse(content, s.logDir)
	if err != nil {
		return "", nil, err
	}
	return content, entries, nil
}

func (s *Store) commitLocked(ctx context.Context, prev string, entries []Entry) error {
	if strings.TrimSpace(prev) != "" {
		s.backup(prev)
	}
	return s.runner.Replace(ctx, Render(entries, s.logDir))
}

// backup 在每次写入前保存旧内容，最多保留 maxBackups 份。
func (s *Store) backup(content string) {
	if s.dataDir == "" {
		return
	}
	dir := filepath.Join(s.dataDir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	name := fmt.Sprintf("crontab-%s.bak", time.Now().Format("20060102-150405.000000000"))
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		return
	}
	pruneBackups(dir)
}

func pruneBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) <= maxBackups {
		return
	}
	// 文件名带定长时间戳，字典序即时间序，删除最旧的若干份。
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names[maxBackups:] {
		os.Remove(filepath.Join(dir, name))
	}
}

// ExecRunner 通过调用系统的 crontab 命令读写 crontab。
type ExecRunner struct {
	Bin  string
	User string
}

// NewExecRunner 创建 ExecRunner；bin 为空时使用 PATH 中的 crontab。
func NewExecRunner(bin, user string) *ExecRunner {
	if bin == "" {
		bin = "crontab"
	}
	return &ExecRunner{Bin: bin, User: user}
}

func (r *ExecRunner) commandList() []string {
	args := []string{"-l"}
	if r.User != "" {
		args = append(args, "-u", r.User)
	}
	return append([]string{r.Bin}, args...)
}

func (r *ExecRunner) commandReplace() []string {
	args := []string{"-"}
	if r.User != "" {
		args = append(args, "-u", r.User)
	}
	return append([]string{r.Bin}, args...)
}

// List 读取当前 crontab；"no crontab for ..." 视为空内容。
func (r *ExecRunner) List(ctx context.Context) (string, error) {
	argv := r.commandList()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			msg := strings.TrimSpace(stderr.String())
			if strings.Contains(msg, "no crontab for") {
				return "", nil
			}
			return "", fmt.Errorf("执行 %s 失败：%s", r.Bin, msg)
		}
		return "", fmt.Errorf("无法执行 %s：%w", r.Bin, err)
	}
	return stdout.String(), nil
}

// Replace 用 content 整体覆盖 crontab。
func (r *ExecRunner) Replace(ctx context.Context, content string) error {
	argv := r.commandReplace()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(content)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("写入 crontab 失败：%s", msg)
	}
	return nil
}
