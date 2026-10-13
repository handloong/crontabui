package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// DefaultLogDir 是任务日志的默认目录。
	DefaultLogDir = "/var/log/crontabui"
	// DefaultMemLimit 是默认内存上限（MiB），低于需求的 50MB 留出安全余量。
	DefaultMemLimit = 40
	// DefaultSessionTTL 是默认会话有效期。
	DefaultSessionTTL = 12 * time.Hour

	defaultLogDir   = DefaultLogDir
	defaultMemLimit = DefaultMemLimit

	minTokenLen = 8
)

// Config 是运行期配置。
type Config struct {
	Token      string
	Bind       string
	Port       int
	User       string
	LogDir     string
	DataDir    string
	CronLog    string
	CrontabBin string
	MemLimit   int
	SessionTTL time.Duration
	ShowVer    bool
}

// Addr 返回 net.Listen 使用的地址。
func (c *Config) Addr() string {
	return net.JoinHostPort(c.Bind, strconv.Itoa(c.Port))
}

// Parse 解析命令行参数并校验。getenv 用于读取 CRONTABUI_TOKEN 兜底。
func Parse(args []string, getenv func(string) string) (*Config, error) {
	cfg := &Config{}
	fs := flag.NewFlagSet("crontabui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&cfg.Token, "token", "", "登录令牌（必填，也可用环境变量 CRONTABUI_TOKEN）")
	fs.StringVar(&cfg.Bind, "bind", "127.0.0.1", "监听地址，仅填地址不含端口")
	fs.IntVar(&cfg.Port, "port", 8080, "监听端口")
	fs.StringVar(&cfg.User, "user", "", "目标用户的 crontab，默认当前用户")
	fs.StringVar(&cfg.LogDir, "log-dir", defaultLogDir, "任务日志目录")
	fs.StringVar(&cfg.DataDir, "data-dir", "", "数据与备份目录，默认 ~/.crontabui")
	fs.StringVar(&cfg.CronLog, "cron-log", "", "系统 cron 日志文件，默认自动探测")
	fs.StringVar(&cfg.CrontabBin, "crontab-bin", "crontab", "crontab 可执行文件")
	fs.IntVar(&cfg.MemLimit, "mem-limit", defaultMemLimit, "内存上限 MiB，0 表示不限制")
	fs.DurationVar(&cfg.SessionTTL, "session-ttl", DefaultSessionTTL, "会话滑动有效期")
	fs.BoolVar(&cfg.ShowVer, "version", false, "打印版本后退出")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("不支持的位置参数：%s", strings.Join(fs.Args(), " "))
	}
	if cfg.ShowVer {
		return cfg, nil
	}

	if cfg.Token == "" && getenv != nil {
		cfg.Token = strings.TrimSpace(getenv("CRONTABUI_TOKEN"))
	}
	if cfg.Token == "" {
		return nil, errors.New("缺少 --token：请用 --token 或环境变量 CRONTABUI_TOKEN 指定登录令牌")
	}
	if len([]rune(cfg.Token)) < minTokenLen {
		return nil, fmt.Errorf("token 长度不足：至少需要 %d 个字符", minTokenLen)
	}
	if err := validateBind(cfg.Bind); err != nil {
		return nil, err
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("端口 %d 无效：必须在 1-65535 之间", cfg.Port)
	}
	if cfg.MemLimit < 0 || cfg.MemLimit > 1024 {
		return nil, fmt.Errorf("内存上限 %d 无效：必须在 0-1024 MiB 之间，0 表示不限制", cfg.MemLimit)
	}
	if cfg.SessionTTL <= 0 {
		return nil, fmt.Errorf("会话有效期 %v 无效：必须大于 0", cfg.SessionTTL)
	}
	if cfg.CrontabBin == "" {
		cfg.CrontabBin = "crontab"
	}
	if cfg.LogDir == "" {
		cfg.LogDir = defaultLogDir
	}
	if cfg.DataDir == "" {
		cfg.DataDir = defaultDataDir()
	}
	return cfg, nil
}

func validateBind(bind string) error {
	if strings.TrimSpace(bind) == "" {
		return errors.New("--bind 不能为空")
	}
	if net.ParseIP(bind) != nil {
		return nil
	}
	if strings.Contains(bind, ":") {
		return fmt.Errorf("--bind 值 %q 无效：只填写监听地址，不要包含端口", bind)
	}
	for _, r := range bind {
		if r == '.' || r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return fmt.Errorf("--bind 值 %q 无效", bind)
	}
	return nil
}

func defaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".crontabui")
	}
	return filepath.Join(os.TempDir(), "crontabui")
}

// ResolveLogDir 确保日志目录可用；不可创建或不可写时回退到 ~/.crontabui/logs。
func ResolveLogDir(dir string, userHome func() (string, error)) (string, error) {
	if dir != "" && os.MkdirAll(dir, 0o755) == nil && writableDir(dir) {
		return dir, nil
	}
	fallback := ""
	if userHome != nil {
		if home, err := userHome(); err == nil && home != "" {
			fallback = filepath.Join(home, ".crontabui", "logs")
		}
	}
	if fallback == "" {
		fallback = filepath.Join(os.TempDir(), "crontabui-logs")
	}
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		return "", fmt.Errorf("无法创建日志目录 %s：%w", fallback, err)
	}
	return fallback, nil
}

// writableDir 通过创建临时文件真实探测目录可写性。
func writableDir(dir string) bool {
	f, err := os.CreateTemp(dir, ".write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
