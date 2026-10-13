package crontab

import (
	"strings"
	"testing"
)

const testLogDir = "/var/log/crontabui"

func jobsOf(t *testing.T, entries []Entry) []*Job {
	t.Helper()
	var out []*Job
	for _, e := range entries {
		if e.Kind == KindJob {
			out = append(out, e.Job)
		}
	}
	return out
}

func TestParseEmpty(t *testing.T) {
	for _, c := range []string{"", "\n"} {
		entries, err := Parse(c, testLogDir)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", c, err)
		}
		if len(entries) != 0 {
			t.Errorf("Parse(%q) = %d entries, want 0", c, len(entries))
		}
	}
}

func TestParseKeepsForeignLinesVerbatim(t *testing.T) {
	content := "MAILTO=root\n\n# 我手写的任务\n0 1 * * * /usr/bin/foo.sh\nSHELL=/bin/bash\n"
	entries, err := Parse(content, testLogDir)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	for _, e := range entries {
		if e.Kind != KindRaw {
			t.Fatalf("expected every entry to be raw, got kind %v for %v", e.Kind, e.Raw)
		}
	}
	if got := Render(entries, testLogDir); got != content {
		t.Errorf("round trip mismatch:\n got %q\nwant %q", got, content)
	}
}

func TestParseManagedJobs(t *testing.T) {
	content := strings.Join([]string{
		"MAILTO=root",
		"",
		"# 手写任务",
		"0 1 * * * /usr/bin/foo.sh",
		"",
		"#CRONTABUI: a1b2c3d4 每日备份",
		"0 3 * * * /opt/backup.sh >> " + testLogDir + "/a1b2c3d4.log 2>&1",
		"#CRONTABUI: e5f6a7b8 已停用任务",
		"#*/10 * * * * /opt/ping.sh >> " + testLogDir + "/e5f6a7b8.log 2>&1",
		"",
	}, "\n")

	entries, err := Parse(content, testLogDir)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	jobs := jobsOf(t, entries)
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(jobs))
	}

	a := jobs[0]
	if a.ID != "a1b2c3d4" || a.Name != "每日备份" {
		t.Errorf("job[0] identity = %q/%q", a.ID, a.Name)
	}
	if a.Schedule != "0 3 * * *" || a.Command != "/opt/backup.sh" {
		t.Errorf("job[0] = %q / %q", a.Schedule, a.Command)
	}
	if !a.Enabled || !a.CaptureLog {
		t.Errorf("job[0] flags enabled=%v capture=%v, want true/true", a.Enabled, a.CaptureLog)
	}

	b := jobs[1]
	if b.ID != "e5f6a7b8" || b.Name != "已停用任务" {
		t.Errorf("job[1] identity = %q/%q", b.ID, b.Name)
	}
	if b.Enabled {
		t.Error("job[1] should be disabled")
	}
	if !b.CaptureLog || b.Command != "/opt/ping.sh" {
		t.Errorf("job[1] capture=%v command=%q", b.CaptureLog, b.Command)
	}

	if got := Render(entries, testLogDir); got != content {
		t.Errorf("round trip mismatch:\n got %q\nwant %q", got, content)
	}
}

func TestParseCaptureLogDetection(t *testing.T) {
	cases := []struct {
		name     string
		cmdLine  string
		wantCmd  string
		wantCapt bool
	}{
		{
			name:     "managed redirect",
			cmdLine:  "0 3 * * * /opt/a.sh >> " + testLogDir + "/deadbeef.log 2>&1",
			wantCmd:  "/opt/a.sh",
			wantCapt: true,
		},
		{
			name:     "foreign redirect is kept",
			cmdLine:  "0 3 * * * /opt/a.sh >> /tmp/other.log 2>&1",
			wantCmd:  "/opt/a.sh >> /tmp/other.log 2>&1",
			wantCapt: false,
		},
		{
			name:     "other job id is kept",
			cmdLine:  "0 3 * * * /opt/a.sh >> " + testLogDir + "/11112222.log 2>&1",
			wantCmd:  "/opt/a.sh >> " + testLogDir + "/11112222.log 2>&1",
			wantCapt: false,
		},
		{
			name:     "no redirect",
			cmdLine:  "0 3 * * * /opt/a.sh",
			wantCmd:  "/opt/a.sh",
			wantCapt: false,
		},
		{
			name:     "stderr only",
			cmdLine:  "0 3 * * * /opt/a.sh 2>&1",
			wantCmd:  "/opt/a.sh 2>&1",
			wantCapt: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			content := "#CRONTABUI: deadbeef job\n" + c.cmdLine + "\n"
			entries, err := Parse(content, testLogDir)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			jobs := jobsOf(t, entries)
			if len(jobs) != 1 {
				t.Fatalf("got %d jobs, want 1", len(jobs))
			}
			if jobs[0].Command != c.wantCmd {
				t.Errorf("Command = %q, want %q", jobs[0].Command, c.wantCmd)
			}
			if jobs[0].CaptureLog != c.wantCapt {
				t.Errorf("CaptureLog = %v, want %v", jobs[0].CaptureLog, c.wantCapt)
			}
			if got := Render(entries, testLogDir); got != content {
				t.Errorf("round trip mismatch:\n got %q\nwant %q", got, content)
			}
		})
	}
}

func TestParseMarkerWithoutBodyDegradesToRaw(t *testing.T) {
	cases := []string{
		"#CRONTABUI: a1b2c3d4 orphan\n",
		"#CRONTABUI: a1b2c3d4 orphan\n\n",
		"#CRONTABUI: a1b2c3d4 orphan\n#CRONTABUI: b2c3d4e5 second\n",
	}
	for _, content := range cases {
		entries, err := Parse(content, testLogDir)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", content, err)
		}
		if len(jobsOf(t, entries)) != 0 {
			t.Errorf("Parse(%q) produced a job, want none", content)
		}
		if got := Render(entries, testLogDir); got != content {
			t.Errorf("Parse(%q) round trip = %q", content, got)
		}
	}
}

func TestParseMarkerWithoutName(t *testing.T) {
	content := "#CRONTABUI: a1b2c3d4\n0 3 * * * /opt/a.sh\n"
	entries, err := Parse(content, testLogDir)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	jobs := jobsOf(t, entries)
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	if jobs[0].Name != "" {
		t.Errorf("Name = %q, want empty", jobs[0].Name)
	}
	if got := Render(entries, testLogDir); got != content {
		t.Errorf("round trip = %q, want %q", got, content)
	}
}

func TestRenderAddsCaptureRedirect(t *testing.T) {
	entries := []Entry{{Kind: KindJob, Job: &Job{
		ID: "cafebabe", Name: "抓日志", Schedule: "0 3 * * *",
		Command: "/opt/a.sh", Enabled: true, CaptureLog: true,
	}}}
	want := "#CRONTABUI: cafebabe 抓日志\n0 3 * * * /opt/a.sh >> " + testLogDir + "/cafebabe.log 2>&1\n"
	if got := Render(entries, testLogDir); got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRenderDisabledJobCommentsOutCommand(t *testing.T) {
	entries := []Entry{{Kind: KindJob, Job: &Job{
		ID: "cafebabe", Name: "停用", Schedule: "0 3 * * *",
		Command: "/opt/a.sh", Enabled: false, CaptureLog: false,
	}}}
	want := "#CRONTABUI: cafebabe 停用\n#0 3 * * * /opt/a.sh\n"
	if got := Render(entries, testLogDir); got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRenderNormalizesTrailingNewline(t *testing.T) {
	entries, err := Parse("MAILTO=root", testLogDir)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if got := Render(entries, testLogDir); !strings.HasSuffix(got, "\n") {
		t.Errorf("Render must end with newline, got %q", got)
	}
}

func TestNewID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := NewID()
		if len(id) != 8 {
			t.Fatalf("NewID() = %q, want 8 chars", id)
		}
		if seen[id] {
			t.Fatalf("NewID() produced duplicate %q", id)
		}
		seen[id] = true
	}
}
