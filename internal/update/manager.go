package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Phase 表示更新流程所处的阶段。
type Phase string

const (
	// PhaseIdle 空闲，或刚完成一次检查。
	PhaseIdle Phase = "idle"
	// PhaseChecking 正在查询最新版本。
	PhaseChecking Phase = "checking"
	// PhaseDownloading 正在下载产物。
	PhaseDownloading Phase = "downloading"
	// PhaseVerifying 正在校验下载内容。
	PhaseVerifying Phase = "verifying"
	// PhaseInstalling 正在替换二进制。
	PhaseInstalling Phase = "installing"
	// PhaseRestarting 服务即将重启。
	PhaseRestarting Phase = "restarting"
	// PhaseFailed 更新失败。
	PhaseFailed Phase = "failed"
)

// defaultRestartDelay 是返回「正在重启」状态后，到真正重启之间的等待。
// 留出这一小段是为了让浏览器先拿到响应。
const defaultRestartDelay = 1500 * time.Millisecond

// State 是更新流程对外暴露的状态快照，直接序列化成接口响应。
//
// Progress 取值约定：0..1 表示下载进度，-1 表示进度未知（渲染成不确定进度条）。
type State struct {
	Enabled   bool    `json:"enabled"`
	Platform  string  `json:"platform"`
	Phase     Phase   `json:"phase"`
	Progress  float64 `json:"progress"`
	Message   string  `json:"message"`
	Current   string  `json:"current"`
	Latest    string  `json:"latest"`
	HasUpdate bool    `json:"hasUpdate"`
	Asset     string  `json:"asset"`
	AssetSize int64   `json:"assetSize"`
	Target    string  `json:"target"`
	Notes     string  `json:"notes"`
	HTMLURL   string  `json:"htmlUrl"`
	CheckedAt string  `json:"checkedAt,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// Updater 是自助更新能力的对外接口，Web 层只依赖这个抽象。
type Updater interface {
	// State 返回当前状态快照，供前端轮询。
	State() State
	// Check 查询最新版本并刷新状态。
	Check(ctx context.Context) (State, error)
	// Start 启动异步更新流程，立即返回。
	Start() error
}

// Config 是 Manager 的构造参数。
type Config struct {
	// Repo 是 GitHub 仓库（owner/name），留空则用 DefaultRepo。
	Repo string
	// Current 是当前运行的版本号。
	Current string
	// APIBase 覆盖 GitHub API 基地址，测试用。
	APIBase string
	// ExecPath 指定要替换的可执行文件，留空则用 os.Executable()。
	ExecPath string
	// Logger 记录更新过程。
	Logger *slog.Logger
	// RestartDelay 覆盖重启前的等待时间，测试用。
	RestartDelay time.Duration

	// Client、TrustedHosts、Restart 仅供测试注入。
	Client       *http.Client
	TrustedHosts []string
	Restart      func() error
}

// Manager 串起「检查 → 下载 → 校验 → 替换 → 重启」的完整流程。
type Manager struct {
	checker      *Checker
	applier      *Applier
	logger       *slog.Logger
	restartDelay time.Duration
	// target 是最终会被替换的可执行文件路径（构造时解析一次）。
	target string

	mu      sync.Mutex
	state   State
	running bool
}

// New 构造 Manager。
func New(cfg Config) (*Manager, error) {
	repo := strings.TrimSpace(cfg.Repo)
	if repo == "" {
		repo = DefaultRepo
	}
	if !ValidRepo(repo) {
		return nil, fmt.Errorf("更新仓库名 %q 无效，应形如 owner/name", repo)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	delay := cfg.RestartDelay
	if delay <= 0 {
		delay = defaultRestartDelay
	}

	applier := &Applier{ExecPath: cfg.ExecPath, Logger: logger, RestartFunc: cfg.Restart}
	target, err := applier.TargetPath()
	if err != nil {
		return nil, err
	}

	return &Manager{
		checker: &Checker{
			Repo:         repo,
			Current:      strings.TrimSpace(cfg.Current),
			APIBase:      cfg.APIBase,
			Client:       cfg.Client,
			trustedHosts: cfg.TrustedHosts,
		},
		applier:      applier,
		logger:       logger,
		restartDelay: delay,
		target:       target,
		state: State{
			Enabled:  true,
			Platform: Platform(),
			Phase:    PhaseIdle,
			Current:  strings.TrimSpace(cfg.Current),
			Progress: -1,
			Target:   target,
			Message:  "尚未检查更新",
		},
	}, nil
}

// Platform 返回 "linux/amd64" 形式的平台标识。
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// State 返回当前状态快照。
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Check 查询最新版本并刷新状态。
func (m *Manager) Check(ctx context.Context) (State, error) {
	m.mu.Lock()
	if m.running {
		st := m.state
		m.mu.Unlock()
		return st, errors.New("更新正在进行中，请稍候")
	}
	m.state.Phase = PhaseChecking
	m.state.Progress = -1
	m.state.Message = "正在查询最新版本…"
	m.state.Error = ""
	m.mu.Unlock()

	rel, err := m.checker.Latest(ctx)
	if err != nil {
		m.fail(err)
		return m.State(), err
	}

	st := m.evaluate(rel)
	m.mu.Lock()
	m.state = st
	m.mu.Unlock()
	return st, nil
}

// Start 启动异步更新流程。整个过程可能持续数分钟，因此与调用方（HTTP 请求）解耦。
func (m *Manager) Start() error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return errors.New("已有更新任务正在进行")
	}
	if !m.state.HasUpdate {
		m.mu.Unlock()
		return errors.New("当前没有可用的新版本，请先检查更新")
	}
	m.running = true
	m.state.Phase = PhaseDownloading
	m.state.Progress = 0
	m.state.Message = "正在准备下载…"
	m.state.Error = ""
	m.mu.Unlock()

	go m.run()
	return nil
}

// evaluate 把 Release 信息折算成新的状态快照。
func (m *Manager) evaluate(rel *Release) State {
	st := State{
		Enabled:   true,
		Platform:  Platform(),
		Phase:     PhaseIdle,
		Progress:  -1,
		Current:   m.checker.Current,
		Latest:    rel.Tag,
		Notes:     strings.TrimSpace(rel.Notes),
		HTMLURL:   rel.HTMLURL,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
		Target:    m.target,
	}

	name, ok := AssetName(runtime.GOARCH)
	if !ok {
		st.Message = fmt.Sprintf("当前平台 %s 没有对应的发布产物，无法自助更新", st.Platform)
		return st
	}
	asset, found := rel.FindAsset(name)
	if !found {
		st.Message = fmt.Sprintf("最新版本 %s 里没有找到本机产物 %s", rel.Tag, name)
		return st
	}

	st.Asset = asset.Name
	st.AssetSize = asset.Size
	if CompareVersions(rel.Tag, m.checker.Current) > 0 {
		st.HasUpdate = true
		st.Message = "发现新版本 " + rel.Tag
	} else {
		st.Message = "已是最新版本"
	}
	return st
}

// run 执行完整的更新流程，由 Start 在独立 goroutine 中调起。
func (m *Manager) run() {
	defer func() {
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()

	ctx := context.Background()

	// 真实检查一次，避免依赖可能已经过期的缓存状态
	rel, err := m.checker.Latest(ctx)
	if err != nil {
		m.fail(err)
		return
	}
	st := m.evaluate(rel)
	if !st.HasUpdate {
		m.mu.Lock()
		m.state = st
		m.mu.Unlock()
		return
	}
	asset, ok := rel.FindAsset(st.Asset)
	if !ok {
		m.fail(fmt.Errorf("最新版本 %s 里没有找到本机产物 %s", rel.Tag, st.Asset))
		return
	}

	m.update(PhaseDownloading, 0, "正在下载 "+asset.Name+"…")

	stage, err := m.applier.NewStagingFile()
	if err != nil {
		m.fail(err)
		return
	}
	stagePath := stage.Name()
	// 替换成功后文件已被 rename 走，这里的清理只会处理失败残留
	defer os.Remove(stagePath)

	if _, err := m.checker.fetch(ctx, asset.URL, stage, func(done, total int64) {
		m.update(PhaseDownloading, ratio(done, total),
			"正在下载 "+asset.Name+"… "+humanSize(done))
	}); err != nil {
		stage.Close()
		m.fail(err)
		return
	}
	if err := stage.Sync(); err != nil {
		stage.Close()
		m.fail(fmt.Errorf("写入下载文件失败：%w", err))
		return
	}
	if err := stage.Close(); err != nil {
		m.fail(fmt.Errorf("关闭下载文件失败：%w", err))
		return
	}

	m.update(PhaseVerifying, -1, "正在校验文件完整性…")
	if err := m.verify(ctx, rel, asset.Name, stagePath); err != nil {
		m.fail(err)
		return
	}

	m.update(PhaseInstalling, -1, "正在替换程序文件…")
	backup, err := m.applier.Install(stagePath)
	if err != nil {
		m.fail(err)
		return
	}
	if backup != "" {
		m.logger.Info("旧版本已备份，需要回滚时可手工恢复", "path", backup)
	}

	m.mu.Lock()
	m.state.Phase = PhaseRestarting
	m.state.Progress = 1
	m.state.HasUpdate = false
	m.state.Message = "更新完成，服务正在重启…"
	m.mu.Unlock()
	m.logger.Info("更新完成，准备重启服务", "from", m.checker.Current, "to", rel.Tag)

	time.Sleep(m.restartDelay)
	if err := m.applier.Restart(); err != nil {
		m.fail(err)
	}
}

// verify 用 Release 里的 checksums.txt 校验下载内容。
// 这是更新链路上唯一能发现「下载内容被篡改或损坏」的环节，因此缺失即中止。
func (m *Manager) verify(ctx context.Context, rel *Release, assetName, path string) error {
	sums, err := m.checker.Checksums(ctx, rel)
	if err != nil {
		return fmt.Errorf("获取校验和失败：%w", err)
	}
	if len(sums) == 0 {
		return errors.New("该版本未提供 checksums.txt，出于安全考虑已中止更新")
	}
	want, ok := sums[assetName]
	if !ok {
		return fmt.Errorf("该版本的 checksums.txt 中没有 %s 的记录，已中止更新", assetName)
	}
	got, err := sha256File(path)
	if err != nil {
		return fmt.Errorf("计算校验和失败：%w", err)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("校验和不匹配（期望 %s…，实际 %s…），已放弃更新",
			want[:12], got[:12])
	}
	return nil
}

// update 推进状态。
func (m *Manager) update(phase Phase, progress float64, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Phase = phase
	m.state.Progress = progress
	m.state.Message = message
}

// fail 记录失败状态。
func (m *Manager) fail(err error) {
	m.logger.Error("自助更新失败", "err", err)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Phase = PhaseFailed
	m.state.Progress = -1
	m.state.Message = ""
	m.state.Error = err.Error()
}

// ratio 把下载进度折算成 0..1；总长度未知时返回 -1。
func ratio(done, total int64) float64 {
	if total <= 0 {
		return -1
	}
	r := float64(done) / float64(total)
	if r > 1 {
		return 1
	}
	return r
}

// humanSize 把字节数格式化成便于阅读的形式。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB"}
	v := float64(n)
	i := -1
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
