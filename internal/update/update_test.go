package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testRepo = "owner/repo"

// ---------- 纯函数 ----------

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"1.0.0", "v1.0.0", 0},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.10.0", "v1.9.9", 1},
		{"v2.0.0", "v1.99.99", 1},
		{"v0.9.0", "v1.0.0", -1},
		{"v1.0.0-rc1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc1", 1},
		{"v1.0", "v1.0.0", 0},
		{"dev", "v1.0.1", -1},
		{"", "v1.0.1", -1},
		{"v1.2.3", "1.2.3", 0},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	cases := map[string]string{
		"amd64": "crontabui-linux-amd64",
		"386":   "crontabui-linux-386",
		"arm64": "crontabui-linux-arm64",
		"arm":   "crontabui-linux-armv7", // 32 位 ARM 带 v7 后缀，避免与 arm64 混淆
	}
	for arch, want := range cases {
		got, ok := AssetName(arch)
		if !ok || got != want {
			t.Errorf("AssetName(%q) = %q, %v；want %q, true", arch, got, ok, want)
		}
	}
	if _, ok := AssetName("mips"); ok {
		t.Errorf("AssetName(mips) 应当返回不支持")
	}
}

func TestValidRepo(t *testing.T) {
	valid := []string{"handloong/crontabui", "a/b", "org-name/repo.name", "user_1/repo_2"}
	for _, r := range valid {
		if !ValidRepo(r) {
			t.Errorf("ValidRepo(%q) = false，应当为 true", r)
		}
	}
	invalid := []string{"", "onlyowner", "a/b/c", "a b/c", "owner/", "/name", "owner/na me", "owner/na?me"}
	for _, r := range invalid {
		if ValidRepo(r) {
			t.Errorf("ValidRepo(%q) = true，应当为 false", r)
		}
	}
}

// ---------- 替换与归档 ----------

func TestInstallReplacesBinaryAndKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "crontabui")
	oldBin := append(fakeELF(runtime.GOARCH), []byte("old-binary")...)
	if err := os.WriteFile(target, oldBin, 0o755); err != nil {
		t.Fatalf("写入旧二进制：%v", err)
	}

	newBin := append(fakeELF(runtime.GOARCH), []byte("new-binary")...)
	src := filepath.Join(dir, ".crontabui-update-test")
	if err := os.WriteFile(src, newBin, 0o600); err != nil {
		t.Fatalf("写入新二进制：%v", err)
	}

	a := &Applier{ExecPath: target}
	backup, err := a.Install(src)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if backup != target+".bak" {
		t.Errorf("backup = %q，want %q", backup, target+".bak")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("读取目标文件：%v", err)
	}
	if !bytes.Equal(got, newBin) {
		t.Errorf("目标文件未被替换为新二进制")
	}
	kept, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("读取备份：%v", err)
	}
	if !bytes.Equal(kept, oldBin) {
		t.Errorf("备份内容不是旧二进制")
	}
	// 临时文件应当已被 rename 走
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("临时文件仍然存在：%v", err)
	}
}

func TestInstallRejectsWrongArchitecture(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "crontabui")
	original := fakeELF(runtime.GOARCH)
	if err := os.WriteFile(target, original, 0o755); err != nil {
		t.Fatalf("写入目标文件：%v", err)
	}

	other := "amd64"
	if runtime.GOARCH == "amd64" {
		other = "arm64"
	}
	src := filepath.Join(dir, "wrong-arch")
	if err := os.WriteFile(src, fakeELF(other), 0o755); err != nil {
		t.Fatalf("写入源文件：%v", err)
	}

	a := &Applier{ExecPath: target}
	if _, err := a.Install(src); err == nil {
		t.Fatal("架构不匹配时 Install 应当报错")
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, original) {
		t.Errorf("校验失败时不应改动目标文件")
	}
}

func TestInstallRejectsNonELF(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "crontabui")
	if err := os.WriteFile(target, fakeELF(runtime.GOARCH), 0o755); err != nil {
		t.Fatalf("写入目标文件：%v", err)
	}
	src := filepath.Join(dir, "not-elf")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
		t.Fatalf("写入源文件：%v", err)
	}

	a := &Applier{ExecPath: target}
	if _, err := a.Install(src); err == nil {
		t.Fatal("非 ELF 文件应当被拒绝")
	}
}

func TestNewStagingFileLandsNextToTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "crontabui")
	if err := os.WriteFile(target, fakeELF(runtime.GOARCH), 0o755); err != nil {
		t.Fatalf("写入目标文件：%v", err)
	}

	a := &Applier{ExecPath: target}
	f, err := a.NewStagingFile()
	if err != nil {
		t.Fatalf("NewStagingFile: %v", err)
	}
	defer os.Remove(f.Name())
	f.Close()

	if got := filepath.Dir(f.Name()); got != dir {
		t.Errorf("临时文件目录 = %q，want %q（必须同目录才能原子替换）", got, dir)
	}
}

// ---------- 完整流程 ----------

func TestManagerCheckReportsUpdate(t *testing.T) {
	target := writeTarget(t, fakeELF(runtime.GOARCH))
	newBin := append(fakeELF(runtime.GOARCH), []byte("new")...)
	srv, _ := newFakeRelease(t, "v1.2.0", releaseBlobs(t, newBin))

	m := newTestManager(t, srv, "v1.1.0", target, nil)
	st, err := m.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !st.HasUpdate {
		t.Errorf("HasUpdate = false，want true（%+v）", st)
	}
	if st.Latest != "v1.2.0" {
		t.Errorf("Latest = %q，want v1.2.0", st.Latest)
	}
	if st.Asset == "" {
		t.Errorf("未选出本机产物（platform=%s）", st.Platform)
	}

	// 已是最新时不能报可更新
	srv2, _ := newFakeRelease(t, "v1.1.0", releaseBlobs(t, newBin))
	m2 := newTestManager(t, srv2, "v1.1.0", target, nil)
	st2, err := m2.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if st2.HasUpdate {
		t.Errorf("同版本时 HasUpdate = true，want false")
	}
}

func TestManagerApplyEndToEnd(t *testing.T) {
	oldBin := append(fakeELF(runtime.GOARCH), []byte("old")...)
	target := writeTarget(t, oldBin)
	newBin := append(fakeELF(runtime.GOARCH), []byte("new")...)

	srv, _ := newFakeRelease(t, "v1.2.0", releaseBlobs(t, newBin))

	restarts := 0
	m := newTestManager(t, srv, "v1.1.0", target, func() error {
		restarts++
		return nil
	})

	if _, err := m.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitState(t, m, func(s State) bool { return s.Phase == PhaseRestarting })

	if st.HasUpdate {
		t.Errorf("重启阶段 HasUpdate 应当为 false")
	}
	if restarts != 1 {
		t.Errorf("重启被触发 %d 次，want 1", restarts)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("读取目标文件：%v", err)
	}
	if !bytes.Equal(got, newBin) {
		t.Errorf("目标文件没有被替换为新版本")
	}
	backup, err := os.ReadFile(target + ".bak")
	if err != nil {
		t.Fatalf("备份文件缺失：%v", err)
	}
	if !bytes.Equal(backup, oldBin) {
		t.Errorf("备份内容不是旧版本")
	}
}

func TestManagerApplyRejectsChecksumMismatch(t *testing.T) {
	target := writeTarget(t, fakeELF(runtime.GOARCH))
	newBin := append(fakeELF(runtime.GOARCH), []byte("new")...)

	name, _ := AssetName(runtime.GOARCH)
	blobs := releaseBlobs(t, newBin)
	blobs[checksumAssetName] = []byte(strings.Repeat("0", 64) + "  " + name + "\n")

	srv, _ := newFakeRelease(t, "v1.2.0", blobs)
	restarts := 0
	m := newTestManager(t, srv, "v1.1.0", target, func() error {
		restarts++
		return nil
	})

	if _, err := m.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitState(t, m, func(s State) bool { return s.Phase == PhaseFailed })

	if !strings.Contains(st.Error, "校验和") {
		t.Errorf("错误信息未提到校验和：%q", st.Error)
	}
	if restarts != 0 {
		t.Errorf("校验失败却触发了重启 %d 次", restarts)
	}
}

func TestManagerApplyRejectsMissingChecksums(t *testing.T) {
	target := writeTarget(t, fakeELF(runtime.GOARCH))
	newBin := append(fakeELF(runtime.GOARCH), []byte("new")...)

	blobs := releaseBlobs(t, newBin)
	delete(blobs, checksumAssetName)

	srv, _ := newFakeRelease(t, "v1.2.0", blobs)
	m := newTestManager(t, srv, "v1.1.0", target, nil)

	if _, err := m.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitState(t, m, func(s State) bool { return s.Phase == PhaseFailed })

	if !strings.Contains(st.Error, "checksums.txt") {
		t.Errorf("错误信息未提到缺少校验文件：%q", st.Error)
	}
}

func TestStartRequiresAFreshCheck(t *testing.T) {
	target := writeTarget(t, fakeELF(runtime.GOARCH))
	srv, _ := newFakeRelease(t, "v1.2.0", releaseBlobs(t, append(fakeELF(runtime.GOARCH), []byte("new")...)))
	m := newTestManager(t, srv, "v1.1.0", target, nil)

	if err := m.Start(); err == nil {
		t.Fatal("未检查更新就直接 Start 应当报错")
	}
}

func TestCheckSurfacesGitHubErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	target := writeTarget(t, fakeELF(runtime.GOARCH))
	m := newTestManager(t, srv, "v1.1.0", target, nil)

	_, err := m.Check(context.Background())
	if err == nil {
		t.Fatal("404 时应当返回错误")
	}
	if st := m.State(); st.Phase != PhaseFailed {
		t.Errorf("阶段 = %q，want %q", st.Phase, PhaseFailed)
	}
}

// ---------- 测试辅助 ----------

// fakeELF 造一个头部合法的 ELF 文件，用来喂给 checkELF。
// 只需要前 20 字节正确，后面填什么都行。
func fakeELF(goarch string) []byte {
	head := make([]byte, 64)
	copy(head, []byte{0x7f, 'E', 'L', 'F'})
	binary.LittleEndian.PutUint16(head[16:], 3) // e_type = ET_DYN
	switch goarch {
	case "amd64":
		head[4] = 2
		binary.LittleEndian.PutUint16(head[18:], 0x3e)
	case "386":
		head[4] = 1
		binary.LittleEndian.PutUint16(head[18:], 0x03)
	case "arm64":
		head[4] = 2
		binary.LittleEndian.PutUint16(head[18:], 0xb7)
	case "arm":
		head[4] = 1
		binary.LittleEndian.PutUint16(head[18:], 0x28)
	}
	return head
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// releaseBlobs 造出本机架构对应的产物改名 + 配套的 checksums.txt。
func releaseBlobs(t *testing.T, binary []byte) map[string][]byte {
	t.Helper()
	name, ok := AssetName(runtime.GOARCH)
	if !ok {
		t.Skipf("当前平台 %s 没有对应的发布产物", runtime.GOARCH)
	}
	return map[string][]byte{
		name:              binary,
		checksumAssetName: []byte(sha256Hex(binary) + "  " + name + "\n"),
	}
}

// newFakeRelease 起一个假的 GitHub API：/repos/* 返回 Release，/dl/<name> 返回产物内容。
func newFakeRelease(t *testing.T, tag string, blobs map[string][]byte) (*httptest.Server, *Release) {
	t.Helper()

	var rel Release
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name, ok := strings.CutPrefix(r.URL.Path, "/dl/"); ok {
			blob, found := blobs[name]
			if !found {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(blob)))
			_, _ = w.Write(blob)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			_ = json.NewEncoder(w).Encode(rel)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	rel = Release{Tag: tag, Notes: "测试更新说明", HTMLURL: srv.URL + "/release"}
	for name, blob := range blobs {
		rel.Assets = append(rel.Assets, Asset{
			Name: name,
			URL:  srv.URL + "/dl/" + name,
			Size: int64(len(blob)),
		})
	}
	return srv, &rel
}

func newTestManager(t *testing.T, srv *httptest.Server, current, execPath string, restart func() error) *Manager {
	t.Helper()
	m, err := New(Config{
		Repo:         testRepo,
		Current:      current,
		APIBase:      srv.URL,
		ExecPath:     execPath,
		Client:       srv.Client(),
		TrustedHosts: []string{strings.TrimPrefix(srv.URL, "http://")},
		Restart:      restart,
		RestartDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func writeTarget(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crontabui")
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("写入目标文件：%v", err)
	}
	return path
}

func waitState(t *testing.T, m *Manager, pred func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := m.State(); pred(st) {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待状态超时，当前：%+v", m.State())
	return State{}
}
