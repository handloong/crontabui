package crontab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeRunner struct {
	mu       sync.Mutex
	content  string
	lists    int
	replaces int
	err      error
}

func (f *fakeRunner) List(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	if f.err != nil {
		return "", f.err
	}
	return f.content, nil
}

func (f *fakeRunner) Replace(ctx context.Context, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replaces++
	if f.err != nil {
		return f.err
	}
	f.content = content
	return nil
}

func (f *fakeRunner) body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.content
}

func newTestStore(t *testing.T, initial string) (*Store, *fakeRunner, string) {
	t.Helper()
	r := &fakeRunner{content: initial}
	dir := t.TempDir()
	return NewStore(r, testLogDir, dir), r, dir
}

func TestStoreAddAndGet(t *testing.T) {
	s, r, _ := newTestStore(t, "MAILTO=root\n")
	ctx := context.Background()

	created, err := s.Add(ctx, Job{Name: "备份", Schedule: "0 3 * * *", Command: "/opt/b.sh", Enabled: true, CaptureLog: true})
	if err != nil {
		t.Fatalf("Add error: %v", err)
	}
	if len(created.ID) != 8 {
		t.Fatalf("created ID = %q, want 8 chars", created.ID)
	}

	body := r.body()
	if !strings.Contains(body, "MAILTO=root") {
		t.Errorf("foreign line lost:\n%s", body)
	}
	if !strings.Contains(body, "#CRONTABUI: "+created.ID+" 备份") {
		t.Errorf("marker missing:\n%s", body)
	}
	if !strings.Contains(body, "0 3 * * * /opt/b.sh >> "+testLogDir+"/"+created.ID+".log 2>&1") {
		t.Errorf("command line wrong:\n%s", body)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if got != created {
		t.Errorf("Get = %+v, want %+v", got, created)
	}
}

func TestStoreListIncludesForeignEntries(t *testing.T) {
	s, _, _ := newTestStore(t, "MAILTO=root\n0 1 * * * /usr/bin/foo.sh\n")
	entries, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[1].Kind != KindRaw || entries[1].Raw[0] != "0 1 * * * /usr/bin/foo.sh" {
		t.Errorf("foreign cron line not preserved: %+v", entries[1])
	}
}

func TestStoreUpdate(t *testing.T) {
	s, r, _ := newTestStore(t, "")
	ctx := context.Background()

	created, err := s.Add(ctx, Job{Name: "旧名", Schedule: "0 3 * * *", Command: "/opt/a.sh", Enabled: true})
	if err != nil {
		t.Fatalf("Add error: %v", err)
	}
	err = s.Update(ctx, created.ID, Job{Name: "新名", Schedule: "*/5 * * * *", Command: "/opt/b.sh", Enabled: true, CaptureLog: true})
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}

	body := r.body()
	if strings.Contains(body, "旧名") || strings.Contains(body, "/opt/a.sh") {
		t.Errorf("stale content left behind:\n%s", body)
	}
	if !strings.Contains(body, "*/5 * * * * /opt/b.sh >> "+testLogDir+"/"+created.ID+".log 2>&1") {
		t.Errorf("new command line wrong:\n%s", body)
	}

	entries, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if n := len(jobsOf(t, entries)); n != 1 {
		t.Errorf("got %d jobs after update, want 1", n)
	}
}

func TestStoreToggle(t *testing.T) {
	s, r, _ := newTestStore(t, "")
	ctx := context.Background()
	created, _ := s.Add(ctx, Job{Schedule: "0 3 * * *", Command: "/opt/a.sh", Enabled: true})

	if err := s.Toggle(ctx, created.ID, false); err != nil {
		t.Fatalf("Toggle error: %v", err)
	}
	if !strings.Contains(r.body(), "#CRONTABUI: "+created.ID+"\n") {
		t.Errorf("marker should not carry a name here:\n%s", r.body())
	}
	if !strings.Contains(r.body(), "#0 3 * * * /opt/a.sh") {
		t.Errorf("command should be commented out:\n%s", r.body())
	}

	got, _ := s.Get(ctx, created.ID)
	if got.Enabled {
		t.Error("job should be disabled after toggle")
	}

	if err := s.Toggle(ctx, created.ID, true); err != nil {
		t.Fatalf("Toggle back error: %v", err)
	}
	got, _ = s.Get(ctx, created.ID)
	if !got.Enabled {
		t.Error("job should be enabled after toggling back")
	}
}

func TestStoreDeletePreservesNeighbours(t *testing.T) {
	s, r, _ := newTestStore(t, "MAILTO=root\n# 注释\n0 1 * * * /usr/bin/foo.sh\n")
	ctx := context.Background()
	keep1, _ := s.Add(ctx, Job{Name: "keep1", Schedule: "0 1 * * *", Command: "/opt/k1.sh", Enabled: true})
	doomed, _ := s.Add(ctx, Job{Name: "doomed", Schedule: "0 2 * * *", Command: "/opt/d.sh", Enabled: true})
	keep2, _ := s.Add(ctx, Job{Name: "keep2", Schedule: "0 4 * * *", Command: "/opt/k2.sh", Enabled: true})

	if err := s.Delete(ctx, doomed.ID); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	body := r.body()
	for _, want := range []string{"MAILTO=root", "# 注释", "0 1 * * * /usr/bin/foo.sh", "keep1", "keep2"} {
		if !strings.Contains(body, want) {
			t.Errorf("lost %q after delete:\n%s", want, body)
		}
	}
	if strings.Contains(body, "doomed") {
		t.Errorf("deleted job still present:\n%s", body)
	}

	entries, _ := s.List(ctx)
	jobs := jobsOf(t, entries)
	if len(jobs) != 2 || jobs[0].ID != keep1.ID || jobs[1].ID != keep2.ID {
		t.Errorf("order/content broken after delete: %+v", jobs)
	}
}

func TestStoreMissingID(t *testing.T) {
	s, _, _ := newTestStore(t, "")
	ctx := context.Background()
	if err := s.Update(ctx, "00000000", Job{Schedule: "* * * * *", Command: "/bin/true"}); err == nil {
		t.Error("Update on missing id should fail")
	}
	if err := s.Delete(ctx, "00000000"); err == nil {
		t.Error("Delete on missing id should fail")
	}
	if _, err := s.Get(ctx, "00000000"); err == nil {
		t.Error("Get on missing id should fail")
	}
	if err := s.Toggle(ctx, "00000000", false); err == nil {
		t.Error("Toggle on missing id should fail")
	}
}

func TestStoreValidatesInput(t *testing.T) {
	s, _, _ := newTestStore(t, "")
	ctx := context.Background()
	if _, err := s.Add(ctx, Job{Name: "bad", Schedule: "nope", Command: "/bin/true"}); err == nil {
		t.Error("Add with invalid schedule should fail")
	}
	if _, err := s.Add(ctx, Job{Name: "bad", Schedule: "* * * * *", Command: "  "}); err == nil {
		t.Error("Add with empty command should fail")
	}
}

// crontab 以行为单位，任何换行都会凭空产生一条新任务。
// 这个测试防止通过接口注入任意 crontab 条目。
func TestStoreRejectsInjection(t *testing.T) {
	s, r, _ := newTestStore(t, "MAILTO=root\n")
	ctx := context.Background()

	cases := []struct {
		desc  string
		field string
		job   Job
	}{
		{"换行注入到名称", "名称", Job{Name: "无害\n* * * * * /tmp/evil", Schedule: "* * * * *", Command: "/bin/true"}},
		{"换行注入到命令", "命令", Job{Schedule: "* * * * *", Command: "/bin/true\n* * * * * /tmp/evil"}},
		{"换行注入到周期", "周期", Job{Schedule: "* * * * *\n* * * * * /tmp/evil", Command: "/bin/true"}},
		{"回车注入", "名称", Job{Name: "a\rb", Schedule: "* * * * *", Command: "/bin/true"}},
		{"控制字符", "名称", Job{Name: "a\x00b", Schedule: "* * * * *", Command: "/bin/true"}},
		{"名称过长", "过长", Job{Name: strings.Repeat("a", 200), Schedule: "* * * * *", Command: "/bin/true"}},
		{"命令过长", "过长", Job{Schedule: "* * * * *", Command: strings.Repeat("/bin/true ", 500)}},
		{"非法 ID", "ID", Job{ID: "../../etc/evil", Schedule: "* * * * *", Command: "/bin/true"}},
		{"非法 ID 非十六进制", "ID", Job{ID: "ZZZZZZZZ", Schedule: "* * * * *", Command: "/bin/true"}},
	}
	for _, c := range cases {
		if _, err := s.Add(ctx, c.job); err == nil {
			t.Errorf("%s：Add 应当失败", c.desc)
		} else if !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s：错误信息 = %q，应包含 %q", c.desc, err.Error(), c.field)
		}
		if err := s.Update(ctx, "11112222", c.job); err == nil {
			t.Errorf("%s：Update 应当失败", c.desc)
		}
	}
	if body := r.body(); body != "MAILTO=root\n" {
		t.Errorf("非法输入不应写入 crontab，实际为：\n%s", body)
	}
}

func TestStoreDuplicateIDRejected(t *testing.T) {
	s, _, _ := newTestStore(t, "")
	ctx := context.Background()
	j := Job{ID: "aaaaaaaa", Name: "one", Schedule: "* * * * *", Command: "/bin/true", Enabled: true}
	if _, err := s.Add(ctx, j); err != nil {
		t.Fatalf("first Add error: %v", err)
	}
	if _, err := s.Add(ctx, j); err == nil {
		t.Error("duplicate ID should be rejected")
	}
}

func TestStoreWritesBackupsAndPrunes(t *testing.T) {
	s, _, dir := newTestStore(t, "")
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		if _, err := s.Add(ctx, Job{Name: fmt.Sprintf("j%d", i), Schedule: "* * * * *", Command: "/bin/true", Enabled: true}); err != nil {
			t.Fatalf("Add %d error: %v", i, err)
		}
	}
	backupDir := filepath.Join(dir, "backups")
	files, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("backup dir error: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no backups written")
	}
	if len(files) > maxBackups {
		t.Errorf("got %d backups, want <= %d", len(files), maxBackups)
	}
}

func TestStoreConcurrentAdds(t *testing.T) {
	s, r, _ := newTestStore(t, "MAILTO=root\n")
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Add(ctx, Job{Name: fmt.Sprintf("c%d", i), Schedule: "* * * * *", Command: "/bin/true", Enabled: true})
		}(i)
	}
	wg.Wait()

	entries, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if n := len(jobsOf(t, entries)); n != 20 {
		t.Errorf("got %d jobs, want 20", n)
	}
	if !strings.Contains(r.body(), "MAILTO=root") {
		t.Error("foreign line lost under concurrency")
	}
}

func TestExecRunnerArgs(t *testing.T) {
	r := NewExecRunner("my-crontab", "alice")
	if got := strings.Join(r.commandList(), " "); got != "my-crontab -l -u alice" {
		t.Errorf("list args = %q", got)
	}
	if got := strings.Join(r.commandReplace(), " "); got != "my-crontab - -u alice" {
		t.Errorf("replace args = %q", got)
	}
	single := NewExecRunner("crontab", "")
	if got := strings.Join(single.commandList(), " "); got != "crontab -l" {
		t.Errorf("list args = %q", got)
	}
}
