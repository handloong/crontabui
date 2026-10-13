// Command crontabui 是一个极简的 Linux crontab 可视化管理器。
//
// 设计目标：单文件静态二进制、零第三方依赖、常驻内存不超过 50MB。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"crontabui/internal/auth"
	"crontabui/internal/config"
	"crontabui/internal/crontab"
	"crontabui/internal/logview"
	"crontabui/internal/web"
)

// version 由构建脚本通过 -ldflags "-X main.version=..." 注入。
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败：%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Parse(os.Args[1:], os.Getenv)
	if err != nil {
		return err
	}
	if cfg.ShowVer {
		fmt.Printf("crontabui %s\n", version)
		return nil
	}

	applyMemoryLimit(cfg.MemLimit)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	logDir, err := config.ResolveLogDir(cfg.LogDir, os.UserHomeDir)
	if err != nil {
		return err
	}
	if logDir != cfg.LogDir {
		logger.Warn("日志目录不可写，已自动回退", "请求目录", cfg.LogDir, "实际目录", logDir)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		logger.Warn("数据目录创建失败，将跳过 crontab 备份", "目录", cfg.DataDir, "err", err)
	}

	if _, err := exec.LookPath(cfg.CrontabBin); err != nil {
		return fmt.Errorf("找不到 crontab 可执行文件 %q，请用 --crontab-bin 指定路径：%w", cfg.CrontabBin, err)
	}

	store := crontab.NewStore(crontab.NewExecRunner(cfg.CrontabBin, cfg.User), logDir, cfg.DataDir)
	authManager := auth.New(cfg.Token, cfg.SessionTTL)

	server, err := web.New(web.Options{
		Token:   cfg.Token,
		Version: version,
		Auth:    authManager,
		Store:   store,
		LogDir:  logDir,
		FindLog: func() *logview.Source { return detectSystemLog(cfg.CronLog) },
		Exec:    makeExec(logDir),
		Logger:  logger,
	})
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go janitor(ctx, authManager)

	printBanner(cfg, logDir)

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("收到退出信号，正在优雅关闭…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("优雅关闭超时", "err", err)
		}
		return nil
	}
}

// applyMemoryLimit 用运行时软内存上限兜底，保证内存占用符合要求。
func applyMemoryLimit(mib int) {
	if mib <= 0 {
		return
	}
	debug.SetMemoryLimit(int64(mib) << 20)
	// 更积极的 GC 让常驻内存稳定在较低水位（工作负载极小，代价可忽略）。
	debug.SetGCPercent(50)
}

// janitor 周期性清理过期会话与陈旧的登录失败记录，避免内存无界增长。
func janitor(ctx context.Context, manager *auth.Manager) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			manager.Cleanup()
		}
	}
}

// makeExec 返回「立即执行」的实现：以后台方式拉起命令，输出可写入托管日志。
func makeExec(logDir string) web.ExecFunc {
	return func(job crontab.Job) error {
		cmd := exec.Command("/bin/sh", "-c", job.Command)
		cmd.Dir = "/"

		var logFile *os.File
		if job.CaptureLog {
			if f, err := os.OpenFile(job.LogFile(logDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				fmt.Fprintf(f, "\n===== 手动执行 %s =====\n", time.Now().Format("2006-01-02 15:04:05"))
				cmd.Stdout = f
				cmd.Stderr = f
				logFile = f
			}
		}
		if cmd.Stdout == nil {
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
		}

		if err := cmd.Start(); err != nil {
			if logFile != nil {
				logFile.Close()
			}
			return err
		}
		// 回收子进程避免僵尸进程，同时关闭父进程侧的文件句柄；不阻塞 HTTP 请求。
		go func() {
			_ = cmd.Wait()
			if logFile != nil {
				logFile.Close()
			}
		}()
		return nil
	}
}

func detectSystemLog(explicit string) *logview.Source {
	return logview.DetectSystemSource(explicit, fileExists, exec.LookPath)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func printBanner(cfg *config.Config, logDir string) {
	fmt.Printf(`
  CrontabUI %s  ·  Linux crontab 管理器
  ─────────────────────────────────────────────
  访问地址   http://%s/
  日志目录   %s
  备份目录   %s/backups
  crontab    %s
  内存上限   %s
`, version, cfg.Addr(), logDir, cfg.DataDir, cfg.CrontabBin, memLimitLabel(cfg.MemLimit))

	if cfg.Bind != "127.0.0.1" && cfg.Bind != "::1" && cfg.Bind != "localhost" {
		fmt.Printf("\n  ⚠  当前监听 %s，服务将暴露到网络。请务必通过反向代理启用 HTTPS 后再对外提供服务。\n", cfg.Bind)
	}
	fmt.Println()
}

func memLimitLabel(mib int) string {
	if mib <= 0 {
		return "不限制"
	}
	return fmt.Sprintf("%d MiB", mib)
}
