package logview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLines(t *testing.T, path string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line-%06d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func TestTailSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	writeLines(t, path, 10)

	got, err := Tail(path, 3, 1<<20)
	if err != nil {
		t.Fatalf("Tail error: %v", err)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), got)
	}
	if lines[0] != "line-000008" || lines[2] != "line-000010" {
		t.Errorf("wrong window: %q", lines)
	}
}

func TestTailMoreLinesThanPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	writeLines(t, path, 4)
	got, err := Tail(path, 100, 1<<20)
	if err != nil {
		t.Fatalf("Tail error: %v", err)
	}
	if n := strings.Count(got, "\n"); n != 4 {
		t.Errorf("got %d lines, want 4: %q", n, got)
	}
}

func TestTailLargeFileStaysBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.log")
	writeLines(t, path, 200000)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	got, err := Tail(path, 100, 64<<10) // maxBytes caps the read
	if err != nil {
		t.Fatalf("Tail error: %v", err)
	}
	if int64(len(got)) > 64<<10 {
		t.Errorf("result %d bytes exceeds maxBytes 65536", len(got))
	}
	if int64(len(got)) >= fi.Size() {
		t.Errorf("result %d bytes should be far smaller than file %d bytes", len(got), fi.Size())
	}
	if !strings.HasSuffix(got, "line-200000\n") {
		t.Errorf("tail must end at the file end, got ...%q", got[len(got)-40:])
	}
}

func TestTailNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Tail(path, 2, 1<<20)
	if err != nil {
		t.Fatalf("Tail error: %v", err)
	}
	if got != "beta\ngamma" {
		t.Errorf("Tail = %q, want %q", got, "beta\ngamma")
	}
}

func TestTailEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Tail(path, 10, 1<<20)
	if err != nil {
		t.Fatalf("Tail error: %v", err)
	}
	if got != "" {
		t.Errorf("Tail = %q, want empty", got)
	}
}

func TestTailMissingFile(t *testing.T) {
	if _, err := Tail(filepath.Join(t.TempDir(), "nope.log"), 10, 1<<20); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Tail on missing file = %v, want os.ErrNotExist", err)
	}
}

func TestTailDirectory(t *testing.T) {
	if _, err := Tail(t.TempDir(), 10, 1<<20); err == nil {
		t.Error("Tail on a directory should fail")
	}
}

func TestTailZeroLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	writeLines(t, path, 5)
	got, err := Tail(path, 0, 1<<20)
	if err != nil {
		t.Fatalf("Tail error: %v", err)
	}
	if got != "" {
		t.Errorf("Tail with 0 lines = %q, want empty", got)
	}
}

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.log")
	newer := filepath.Join(dir, "newer.log")
	writeLines(t, old, 1)
	writeLines(t, newer, 1)
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	files, err := ListFiles(dir)
	if err != nil {
		t.Fatalf("ListFiles error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2 (directories excluded)", len(files))
	}
	if files[0].Name != "newer.log" {
		t.Errorf("expected newest first, got %q", files[0].Name)
	}
	if files[0].Size == 0 {
		t.Error("size should be populated")
	}
	if files[0].ModTime.IsZero() {
		t.Error("mod time should be populated")
	}
}

func TestListFilesMissingDir(t *testing.T) {
	if _, err := ListFiles(filepath.Join(t.TempDir(), "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ListFiles on missing dir = %v, want os.ErrNotExist", err)
	}
}

func TestDetectSystemSource(t *testing.T) {
	exact := func(target string) func(string) bool {
		return func(p string) bool { return p == target }
	}
	never := func(string) bool { return false }
	all := func(string) bool { return true }
	noLookPath := func(string) (string, error) { return "", errors.New("not found") }
	hasJournalctl := func(name string) (string, error) {
		if name == "journalctl" {
			return "/usr/bin/journalctl", nil
		}
		return "", errors.New("not found")
	}

	t.Run("explicit flag wins", func(t *testing.T) {
		s := DetectSystemSource("/custom/cron.log", exact("/custom/cron.log"), noLookPath)
		if s == nil || s.Path != "/custom/cron.log" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("explicit missing falls through", func(t *testing.T) {
		s := DetectSystemSource("/custom/cron.log", exact("/var/log/cron"), noLookPath)
		if s == nil || s.Path != "/var/log/cron" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("syslog before messages", func(t *testing.T) {
		s := DetectSystemSource("", exact("/var/log/syslog"), noLookPath)
		if s == nil || s.Path != "/var/log/syslog" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("fallback order prefers cron", func(t *testing.T) {
		s := DetectSystemSource("", all, noLookPath)
		if s == nil || s.Path != "/var/log/cron" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("journalctl fallback", func(t *testing.T) {
		s := DetectSystemSource("", never, hasJournalctl)
		if s == nil {
			t.Fatal("expected journalctl source")
		}
		if s.Path != "" {
			t.Errorf("journalctl source should have empty Path, got %q", s.Path)
		}
		if len(s.Args) == 0 || s.Args[0] != "/usr/bin/journalctl" {
			t.Errorf("args = %v", s.Args)
		}
		if !strings.Contains(strings.Join(s.Args, " "), "cron") {
			t.Errorf("journalctl args should target cron: %v", s.Args)
		}
	})
	t.Run("nothing available", func(t *testing.T) {
		if s := DetectSystemSource("", never, noLookPath); s != nil {
			t.Errorf("got %+v, want nil", s)
		}
	})
}

func TestFilterCronLines(t *testing.T) {
	in := strings.Join([]string{
		"Jan  1 00:00:01 host CRON[1]: (root) CMD (/opt/a.sh)",
		"Jan  1 00:00:01 host systemd[1]: Started something",
		"Jan  1 00:05:01 host crond[2]: (alice) CMD (/opt/b.sh)",
	}, "\n")
	out := FilterCronLines(in)
	if strings.Contains(out, "systemd") {
		t.Errorf("non-cron line survived: %q", out)
	}
	if !strings.Contains(out, "/opt/a.sh") || !strings.Contains(out, "/opt/b.sh") {
		t.Errorf("cron lines dropped: %q", out)
	}
}
