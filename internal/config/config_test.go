package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func noEnv(string) string { return "" }

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse([]string{"--token", "12345678"}, noEnv)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cfg.Bind != "127.0.0.1" {
		t.Errorf("Bind = %q, want 127.0.0.1", cfg.Bind)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.LogDir != defaultLogDir {
		t.Errorf("LogDir = %q, want %q", cfg.LogDir, defaultLogDir)
	}
	if cfg.MemLimit != defaultMemLimit {
		t.Errorf("MemLimit = %d, want %d", cfg.MemLimit, defaultMemLimit)
	}
	if cfg.SessionTTL != 12*time.Hour {
		t.Errorf("SessionTTL = %v, want 12h", cfg.SessionTTL)
	}
	if cfg.CrontabBin != "crontab" {
		t.Errorf("CrontabBin = %q, want crontab", cfg.CrontabBin)
	}
	if cfg.Addr() != "127.0.0.1:8080" {
		t.Errorf("Addr() = %q", cfg.Addr())
	}
}

func TestParseToken(t *testing.T) {
	env := func(k string) string {
		if k == "CRONTABUI_TOKEN" {
			return "env-token-value"
		}
		return ""
	}
	cfg, err := Parse(nil, env)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cfg.Token != "env-token-value" {
		t.Errorf("Token from env = %q", cfg.Token)
	}

	cfg, err = Parse([]string{"--token", "flag-token-value"}, env)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cfg.Token != "flag-token-value" {
		t.Errorf("flag should win over env, got %q", cfg.Token)
	}

	if _, err := Parse(nil, noEnv); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("missing token error = %v", err)
	}
}

func TestParseAllFlags(t *testing.T) {
	args := []string{
		"--token", "abcdefgh",
		"--bind", "0.0.0.0",
		"--port", "9090",
		"--user", "alice",
		"--log-dir", "/tmp/logs",
		"--data-dir", "/tmp/data",
		"--cron-log", "/tmp/cron.log",
		"--mem-limit", "24",
		"--session-ttl", "30m",
		"--crontab-bin", "/usr/bin/crontab",
	}
	cfg, err := Parse(args, noEnv)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cfg.Bind != "0.0.0.0" || cfg.Port != 9090 || cfg.User != "alice" {
		t.Errorf("addr/user parsed wrong: %+v", cfg)
	}
	if cfg.LogDir != "/tmp/logs" || cfg.DataDir != "/tmp/data" || cfg.CronLog != "/tmp/cron.log" {
		t.Errorf("paths parsed wrong: %+v", cfg)
	}
	if cfg.MemLimit != 24 || cfg.SessionTTL != 30*time.Minute {
		t.Errorf("limits parsed wrong: %+v", cfg)
	}
	if cfg.CrontabBin != "/usr/bin/crontab" {
		t.Errorf("CrontabBin = %q", cfg.CrontabBin)
	}
	if cfg.Addr() != "0.0.0.0:9090" {
		t.Errorf("Addr() = %q", cfg.Addr())
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{"short token", []string{"--token", "abc"}, "8"},
		{"port zero", []string{"--token", "12345678", "--port", "0"}, "端口"},
		{"port high", []string{"--token", "12345678", "--port", "70000"}, "端口"},
		{"port negative", []string{"--token", "12345678", "--port", "-1"}, "端口"},
		{"mem negative", []string{"--token", "12345678", "--mem-limit", "-1"}, "内存"},
		{"mem huge", []string{"--token", "12345678", "--mem-limit", "4096"}, "内存"},
		{"ttl zero", []string{"--token", "12345678", "--session-ttl", "0s"}, "会话"},
		{"bad bind", []string{"--token", "12345678", "--bind", "127.0.0.1:80"}, "bind"},
		{"positional", []string{"--token", "12345678", "extra"}, "参数"},
		{"unknown flag", []string{"--token", "12345678", "--nope"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.args, noEnv)
			if err == nil {
				t.Fatalf("Parse(%v) = nil error, want failure", c.args)
			}
			if c.wantSub != "" && !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), c.wantSub)
			}
		})
	}
}

func TestParseMemLimitZeroDisables(t *testing.T) {
	cfg, err := Parse([]string{"--token", "12345678", "--mem-limit", "0"}, noEnv)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cfg.MemLimit != 0 {
		t.Errorf("MemLimit = %d, want 0", cfg.MemLimit)
	}
}

func TestResolveLogDirFallsBackWhenUnwritable(t *testing.T) {
	home := t.TempDir()
	// 用一个普通文件作为路径前缀，保证 MkdirAll 在任何平台上都失败。
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	got, err := ResolveLogDir(filepath.Join(blocker, "logs"), func() (string, error) { return home, nil })
	if err != nil {
		t.Fatalf("ResolveLogDir error: %v", err)
	}
	want := filepath.Join(home, ".crontabui", "logs")
	if got != want {
		t.Errorf("fallback = %q, want %q", got, want)
	}
	if !dirExists(got) {
		t.Errorf("fallback dir %q was not created", got)
	}
}

func TestResolveLogDirKeepsWritable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	got, err := ResolveLogDir(dir, func() (string, error) { return "", nil })
	if err != nil {
		t.Fatalf("ResolveLogDir error: %v", err)
	}
	if got != dir {
		t.Errorf("got %q, want %q", got, dir)
	}
	if !dirExists(got) {
		t.Errorf("dir %q was not created", got)
	}
}
