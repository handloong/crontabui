package web

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"crontabui/internal/auth"
	"crontabui/internal/crontab"
	"crontabui/internal/update"
)

// fakeUpdater 用来在不碰网络的前提下驱动更新相关的接口。
type fakeUpdater struct {
	state    update.State
	checkErr error
	startErr error
	starts   int
}

func (f *fakeUpdater) State() update.State { return f.state }

func (f *fakeUpdater) Check(context.Context) (update.State, error) {
	if f.checkErr != nil {
		return f.state, f.checkErr
	}
	f.state.Phase = update.PhaseIdle
	f.state.Latest = "v9.9.9"
	f.state.HasUpdate = true
	f.state.Asset = "crontabui-linux-test"
	return f.state, nil
}

func (f *fakeUpdater) Start() error {
	if f.startErr != nil {
		return f.startErr
	}
	f.starts++
	f.state.Phase = update.PhaseDownloading
	return nil
}

func newUpdateHarness(t *testing.T, up update.Updater) *harness {
	t.Helper()
	h := &harness{runner: &memRunner{}, logDir: t.TempDir()}
	srv, err := New(Options{
		Token:   testToken,
		Version: testVersion,
		Auth:    auth.New(testToken, time.Hour),
		Store:   crontab.NewStore(h.runner, h.logDir, t.TempDir()),
		LogDir:  h.logDir,
		Updater: up,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.srv = srv
	return h
}

// 未启用自助更新时，接口要如实回报 enabled=false，而不是报错——
// 前端正是靠这个字段决定顶栏要不要显示更新入口。
func TestUpdateDisabledReportsUnavailable(t *testing.T) {
	h := newUpdateHarness(t, nil)
	cookie := h.login(t)

	rec := h.do(t, http.MethodGet, "/api/update", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	data := decodeData(t, rec)
	if enabled, _ := data["enabled"].(bool); enabled {
		t.Errorf("enabled = true, want false")
	}
	if data["current"] != testVersion {
		t.Errorf("current = %v, want %q", data["current"], testVersion)
	}

	if rec := h.do(t, http.MethodPost, "/api/update/check", nil, cookie); rec.Code != http.StatusNotImplemented {
		t.Errorf("check status = %d, want 501", rec.Code)
	}
	if rec := h.do(t, http.MethodPost, "/api/update/apply", nil, cookie); rec.Code != http.StatusNotImplemented {
		t.Errorf("apply status = %d, want 501", rec.Code)
	}
}

func TestUpdateCheckAndApply(t *testing.T) {
	up := &fakeUpdater{state: update.State{Enabled: true, Current: testVersion, Progress: -1}}
	h := newUpdateHarness(t, up)
	cookie := h.login(t)

	rec := h.do(t, http.MethodGet, "/api/update", nil, cookie)
	data := decodeData(t, rec)
	if enabled, _ := data["enabled"].(bool); !enabled {
		t.Errorf("enabled = false, want true")
	}

	rec = h.do(t, http.MethodPost, "/api/update/check", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("check status = %d, body = %s", rec.Code, rec.Body.String())
	}
	data = decodeData(t, rec)
	if has, _ := data["hasUpdate"].(bool); !has {
		t.Errorf("hasUpdate = false, want true")
	}
	if data["latest"] != "v9.9.9" {
		t.Errorf("latest = %v, want v9.9.9", data["latest"])
	}

	rec = h.do(t, http.MethodPost, "/api/update/apply", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if data = decodeData(t, rec); data["started"] != true {
		t.Errorf("started = %v, want true", data["started"])
	}
	if up.starts != 1 {
		t.Errorf("Start 被调用 %d 次, want 1", up.starts)
	}

	// 轮询接口应当反映后台推进后的阶段
	rec = h.do(t, http.MethodGet, "/api/update", nil, cookie)
	if got := decodeData(t, rec)["phase"]; got != string(update.PhaseDownloading) {
		t.Errorf("phase = %v, want %q", got, update.PhaseDownloading)
	}
}

func TestUpdateSurfacesUpstreamAndConflictErrors(t *testing.T) {
	up := &fakeUpdater{state: update.State{Enabled: true, Progress: -1}}
	up.checkErr = errors.New("访问 GitHub 失败：连接超时")
	up.startErr = errors.New("当前没有可用的新版本，请先检查更新")
	h := newUpdateHarness(t, up)
	cookie := h.login(t)

	rec := h.do(t, http.MethodPost, "/api/update/check", nil, cookie)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("check status = %d, want 502", rec.Code)
	}
	if msg := errorOf(t, rec); msg != up.checkErr.Error() {
		t.Errorf("check error = %q, want %q", msg, up.checkErr.Error())
	}

	rec = h.do(t, http.MethodPost, "/api/update/apply", nil, cookie)
	if rec.Code != http.StatusConflict {
		t.Errorf("apply status = %d, want 409", rec.Code)
	}
	if msg := errorOf(t, rec); msg != up.startErr.Error() {
		t.Errorf("apply error = %q, want %q", msg, up.startErr.Error())
	}
}

func TestUpdateEndpointsRequireSession(t *testing.T) {
	up := &fakeUpdater{state: update.State{Enabled: true, Progress: -1}}
	h := newUpdateHarness(t, up)

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/update"},
		{http.MethodPost, "/api/update/check"},
		{http.MethodPost, "/api/update/apply"},
	} {
		rec := h.do(t, tc.method, tc.path, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
	if up.starts != 0 {
		t.Errorf("未登录却触发了 %d 次更新", up.starts)
	}
}
