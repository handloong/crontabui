package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"crontabui/internal/auth"
	"crontabui/internal/crontab"
	"crontabui/internal/logview"
)

const testToken = "token-12345"

// testVersion 用于验证版本号是否被注入到页面里（内容刻意与 "dev" 区分开）。
const testVersion = "v9.9.9-test"

// memRunner 是内存版 Runner，避免测试真的调用 crontab。
type memRunner struct {
	mu      sync.Mutex
	content string
	fail    error
}

func (m *memRunner) List(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.content, m.fail
}

func (m *memRunner) Replace(ctx context.Context, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.content = content
	return nil
}

func (m *memRunner) body() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.content
}

type harness struct {
	srv    *Server
	runner *memRunner
	logDir string
	src    *logview.Source
	ranMu  sync.Mutex
	ran    string
}

func newHarness(t *testing.T, initial string) *harness {
	t.Helper()
	h := &harness{
		runner: &memRunner{content: initial},
		logDir: t.TempDir(),
	}
	h.src = &logview.Source{Name: "testlog", Path: filepath.Join(t.TempDir(), "cron.log")}
	if err := os.WriteFile(h.src.Path, []byte("Jan  1 00:00:01 host CRON[1]: (root) CMD (/opt/a.sh)\n"), 0o644); err != nil {
		t.Fatalf("write fake cron log: %v", err)
	}

	srv, err := New(Options{
		Token:   testToken,
		Version: testVersion,
		Auth:    auth.New(testToken, time.Hour),
		Store:   crontab.NewStore(h.runner, h.logDir, t.TempDir()),
		LogDir:  h.logDir,
		FindLog: func() *logview.Source { return h.src },
		Exec: func(job crontab.Job) error {
			h.ranMu.Lock()
			h.ran = job.Command
			h.ranMu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.srv = srv
	return h
}

func (h *harness) do(t *testing.T, method, target string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (h *harness) login(t *testing.T) *http.Cookie {
	t.Helper()
	rec := h.do(t, http.MethodPost, "/api/login", map[string]string{"token": testToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("login did not set a session cookie")
	return nil
}

func decodeData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON (%s): %v", rec.Body.String(), err)
	}
	if !payload.OK {
		t.Fatalf("ok = false, error = %q", payload.Error)
	}
	out := map[string]any{}
	if len(payload.Data) > 0 {
		if err := json.Unmarshal(payload.Data, &out); err != nil {
			t.Fatalf("data is not an object: %v", err)
		}
	}
	return out
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON (%s): %v", rec.Body.String(), err)
	}
	if payload.OK {
		t.Fatalf("expected failure, got %s", rec.Body.String())
	}
	return payload.Error
}

func TestHealthzIsPublic(t *testing.T) {
	h := newHarness(t, "")
	rec := h.do(t, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestProtectedRoutesRequireSession(t *testing.T) {
	h := newHarness(t, "")
	t.Run("api returns 401", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/api/jobs", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if msg := errorOf(t, rec); !strings.Contains(msg, "登录") {
			t.Errorf("error = %q", msg)
		}
	})
	t.Run("page redirects to login", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/", nil)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/login" {
			t.Errorf("Location = %q, want /login", loc)
		}
	})
	t.Run("login page is public", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/login", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "登录") {
			t.Errorf("login page body unexpected: %s", rec.Body.String())
		}
	})
	t.Run("static assets are public", func(t *testing.T) {
		for _, target := range []string{"/static/app.css", "/static/app.js"} {
			rec := h.do(t, http.MethodGet, target, nil)
			if rec.Code != http.StatusOK {
				t.Errorf("%s status = %d", target, rec.Code)
			}
		}
	})
	t.Run("bogus cookie is rejected", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/api/jobs", nil, &http.Cookie{Name: sessionCookieName, Value: "deadbeef"})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})
}

// TestPageVersionInjection 覆盖服务端版本号替换：登录页要显示版本号与功能简介，
// 且不能残留未替换的占位符（CSP 禁止内联脚本，所以只能靠服务端替换）。
func TestPageVersionInjection(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)

	t.Run("登录页包含版本号", func(t *testing.T) {
		body := h.do(t, http.MethodGet, "/login", nil).Body.String()
		if !strings.Contains(body, testVersion) {
			t.Errorf("登录页未包含版本号 %q", testVersion)
		}
	})

	t.Run("登录页不含未替换占位符", func(t *testing.T) {
		body := h.do(t, http.MethodGet, "/login", nil).Body.String()
		if strings.Contains(body, versionPlaceholder) {
			t.Errorf("登录页残留未替换的占位符 %q", versionPlaceholder)
		}
	})

	t.Run("管理页不含未替换占位符", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/", nil, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if strings.Contains(rec.Body.String(), versionPlaceholder) {
			t.Errorf("管理页残留未替换的占位符 %q", versionPlaceholder)
		}
	})

	t.Run("登录页版本号只出现在页脚一次", func(t *testing.T) {
		body := h.do(t, http.MethodGet, "/login", nil).Body.String()
		if !strings.Contains(body, "Linux crontab 可视化管理器") {
			t.Errorf("登录页底部缺少产品名")
		}
		// 页头不再展示版本号，整页只保留页脚这一处
		if n := strings.Count(body, testVersion); n != 1 {
			t.Errorf("登录页版本号出现 %d 次，want 1（仅页脚）", n)
		}
		if strings.Contains(body, "auth-version") {
			t.Errorf("登录页页头的版本号元素未移除")
		}
	})

	t.Run("已移除的功能描述不再出现", func(t *testing.T) {
		body := h.do(t, http.MethodGet, "/login", nil).Body.String()
		for _, gone := range []string{"一键启用停用", "系统 cron 日志", "查看 / 新增 / 编辑 / 删除"} {
			if strings.Contains(body, gone) {
				t.Errorf("登录页仍包含应移除的功能描述：%q", gone)
			}
		}
	})

	t.Run("已移除的令牌提示语不再出现", func(t *testing.T) {
		body := h.do(t, http.MethodGet, "/login", nil).Body.String()
		for _, gone := range []string{"请输入启动服务时", "令牌仅用于本地校验", "忘记令牌", "不会写入磁盘"} {
			if strings.Contains(body, gone) {
				t.Errorf("登录页仍包含应移除的提示语：%q", gone)
			}
		}
	})
}

// TestVersionDefaultsToDev 未注入版本号时（本地 go run / go build 不带 ldflags）必须回退为 dev。
func TestVersionDefaultsToDev(t *testing.T) {
	for _, given := range []string{"", "   "} {
		srv, err := New(Options{
			Token:   testToken,
			Version: given,
			Auth:    auth.New(testToken, time.Hour),
			Store:   crontab.NewStore(&memRunner{}, t.TempDir(), t.TempDir()),
			LogDir:  t.TempDir(),
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := srv.Version(); got != "dev" {
			t.Errorf("Version() = %q, want dev（Options.Version 传 %q）", got, given)
		}
	}
}

func TestLoginFlow(t *testing.T) {
	h := newHarness(t, "")
	t.Run("wrong token", func(t *testing.T) {
		rec := h.do(t, http.MethodPost, "/api/login", map[string]string{"token": "nope"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if msg := errorOf(t, rec); !strings.Contains(msg, "令牌") {
			t.Errorf("error = %q", msg)
		}
	})
	t.Run("correct token sets hardened cookie", func(t *testing.T) {
		cookie := h.login(t)
		if !cookie.HttpOnly {
			t.Error("session cookie must be HttpOnly")
		}
		if cookie.SameSite != http.SameSiteStrictMode {
			t.Errorf("SameSite = %v, want Strict", cookie.SameSite)
		}
		if cookie.Path != "/" {
			t.Errorf("Path = %q", cookie.Path)
		}
		if cookie.MaxAge <= 0 {
			t.Errorf("MaxAge = %d, want > 0", cookie.MaxAge)
		}
	})
	t.Run("session endpoint reports state", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/api/session", nil)
		if got := decodeData(t, rec)["authenticated"]; got != false {
			t.Errorf("authenticated = %v, want false", got)
		}
		cookie := h.login(t)
		rec = h.do(t, http.MethodGet, "/api/session", nil, cookie)
		if got := decodeData(t, rec)["authenticated"]; got != true {
			t.Errorf("authenticated = %v, want true", got)
		}
	})
	t.Run("logout invalidates session", func(t *testing.T) {
		cookie := h.login(t)
		rec := h.do(t, http.MethodPost, "/api/logout", nil, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("logout status = %d", rec.Code)
		}
		rec = h.do(t, http.MethodGet, "/api/jobs", nil, cookie)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status after logout = %d, want 401", rec.Code)
		}
	})
}

func TestLoginRateLimit(t *testing.T) {
	h := newHarness(t, "")
	// 连续猜错：前 10 次返回 401，之后开始限流。
	for i := 0; i < 10; i++ {
		rec := h.do(t, http.MethodPost, "/api/login", map[string]string{"token": "nope"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i+1, rec.Code)
		}
	}
	rec := h.do(t, http.MethodPost, "/api/login", map[string]string{"token": "nope"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 after repeated failures", rec.Code)
	}

	// 关键：限流只惩罚猜错，正确令牌必须始终可用。
	// 否则服务部署在反向代理之后时（所有请求同源 IP），限流会退化成拒绝服务。
	rec = h.do(t, http.MethodPost, "/api/login", map[string]string{"token": testToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("正确令牌被限流拦截：status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCrossOriginStateChangingRequestRejected(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)

	send := func(origin, method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	payload := `{"name":"x","schedule":"* * * * *","command":"/bin/true"}`
	if rec := send("https://evil.example", http.MethodPost, "/api/jobs", payload); rec.Code != http.StatusForbidden {
		t.Errorf("跨站 POST status = %d, want 403", rec.Code)
	}
	if body := h.runner.body(); strings.Contains(body, "/bin/true") {
		t.Errorf("跨站请求竟然写入了 crontab：\n%s", body)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(payload))
	sameOrigin := "http://" + req.Host
	if rec := send(sameOrigin, http.MethodPost, "/api/jobs", payload); rec.Code != http.StatusOK {
		t.Errorf("同源 POST status = %d, want 200 (body = %s)", rec.Code, rec.Body.String())
	}

	// 未携带 Origin/Referer 的非浏览器客户端（curl、脚本）不应被拒绝
	if rec := send("", http.MethodDelete, "/api/jobs/11112222", ""); rec.Code == http.StatusForbidden {
		t.Error("未携带 Origin/Referer 的客户端不应被 CSRF 检查拒绝")
	}
}

func TestJobsCRUD(t *testing.T) {
	h := newHarness(t, "MAILTO=root\n0 1 * * * /usr/bin/foo.sh\n")
	cookie := h.login(t)

	// create
	rec := h.do(t, http.MethodPost, "/api/jobs", map[string]any{
		"name": "备份", "schedule": "0 3 * * *", "command": "/opt/backup.sh", "captureLog": true, "enabled": true,
	}, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	created := decodeData(t, rec)
	id, _ := created["id"].(string)
	if len(id) != 8 {
		t.Fatalf("id = %q", id)
	}
	wantLogFile := crontab.Job{ID: id}.LogFile(h.logDir)
	if !strings.Contains(h.runner.body(), "#CRONTABUI: "+id+" 备份") {
		t.Errorf("marker not written:\n%s", h.runner.body())
	}
	if !strings.Contains(h.runner.body(), ">> "+wantLogFile+" 2>&1") {
		t.Errorf("capture redirect not written:\n%s", h.runner.body())
	}

	// list
	rec = h.do(t, http.MethodGet, "/api/jobs", nil, cookie)
	list := decodeData(t, rec)
	jobs, _ := list["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %v", list["jobs"])
	}
	first, _ := jobs[0].(map[string]any)
	if first["description"] != "每天 03:00" {
		t.Errorf("description = %v", first["description"])
	}
	if first["logFile"] != wantLogFile {
		t.Errorf("logFile = %v, want %v", first["logFile"], wantLogFile)
	}
	foreign, _ := list["foreign"].([]any)
	if len(foreign) != 1 {
		t.Errorf("foreign = %v, want the hand-written job plus MAILTO grouped", foreign)
	}

	// update
	rec = h.do(t, http.MethodPut, "/api/jobs/"+id, map[string]any{
		"name": "改名", "schedule": "*/5 * * * *", "command": "/opt/new.sh", "enabled": true,
	}, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(h.runner.body(), "/opt/backup.sh") {
		t.Errorf("stale command left behind:\n%s", h.runner.body())
	}
	if !strings.Contains(h.runner.body(), "*/5 * * * * /opt/new.sh") {
		t.Errorf("update not applied:\n%s", h.runner.body())
	}

	// toggle
	rec = h.do(t, http.MethodPost, "/api/jobs/"+id+"/toggle", map[string]any{"enabled": false}, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle status = %d", rec.Code)
	}
	if !strings.Contains(h.runner.body(), "#*/5 * * * * /opt/new.sh") {
		t.Errorf("toggle not applied:\n%s", h.runner.body())
	}

	// run now
	rec = h.do(t, http.MethodPost, "/api/jobs/"+id+"/run", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("run status = %d", rec.Code)
	}
	h.ranMu.Lock()
	ran := h.ran
	h.ranMu.Unlock()
	if ran != "/opt/new.sh" {
		t.Errorf("executed command = %q", ran)
	}

	// delete
	rec = h.do(t, http.MethodDelete, "/api/jobs/"+id, nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if strings.Contains(h.runner.body(), id) {
		t.Errorf("job not deleted:\n%s", h.runner.body())
	}
	if !strings.Contains(h.runner.body(), "MAILTO=root") || !strings.Contains(h.runner.body(), "0 1 * * * /usr/bin/foo.sh") {
		t.Errorf("foreign lines lost:\n%s", h.runner.body())
	}
}

func TestJobValidationErrors(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)

	rec := h.do(t, http.MethodPost, "/api/jobs", map[string]any{
		"name": "bad", "schedule": "70 * * * *", "command": "/bin/true",
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if msg := errorOf(t, rec); !strings.Contains(msg, "分钟") {
		t.Errorf("error = %q, want field detail", msg)
	}

	rec = h.do(t, http.MethodPost, "/api/jobs", map[string]any{"schedule": "* * * * *", "command": " "}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if msg := errorOf(t, rec); !strings.Contains(msg, "命令") {
		t.Errorf("error = %q", msg)
	}
}

func TestMissingJobReturns404(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)
	for _, tc := range []struct {
		method string
		target string
	}{
		{http.MethodDelete, "/api/jobs/00000000"},
		{http.MethodPut, "/api/jobs/00000000"},
		{http.MethodPost, "/api/jobs/00000000/toggle"},
		{http.MethodPost, "/api/jobs/00000000/run"},
		{http.MethodGet, "/api/jobs/00000000/log"},
	} {
		rec := h.do(t, tc.method, tc.target, map[string]any{}, cookie)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d body = %s, want 404", tc.method, tc.target, rec.Code, rec.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(h.logDir, "00000000.log")); err == nil {
		t.Error("log endpoint must not create files for unknown jobs")
	}
}

func TestRequestBodyTooLarge(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)
	big := strings.Repeat("a", 1<<20)
	rec := h.do(t, http.MethodPost, "/api/jobs", map[string]any{
		"name": "big", "schedule": "* * * * *", "command": big,
	}, cookie)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestJobLogEndpoint(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)

	rec := h.do(t, http.MethodPost, "/api/jobs", map[string]any{
		"name": "log", "schedule": "* * * * *", "command": "/bin/echo", "captureLog": true,
	}, cookie)
	id, _ := decodeData(t, rec)["id"].(string)

	logPath := filepath.Join(h.logDir, id+".log")
	if err := os.WriteFile(logPath, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	rec = h.do(t, http.MethodGet, "/api/jobs/"+id+"/log?lines=2", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec)
	if data["content"] != "three\nfour\n" {
		t.Errorf("content = %q", data["content"])
	}
	if data["exists"] != true {
		t.Errorf("exists = %v", data["exists"])
	}

	rec = h.do(t, http.MethodGet, "/api/jobs/"+id+"/log?lines=999999", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("huge lines status = %d", rec.Code)
	}
}

func TestJobLogDownload(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)

	rec := h.do(t, http.MethodPost, "/api/jobs", map[string]any{
		"name": "每日备份/清理", "schedule": "* * * * *", "command": "/bin/echo", "captureLog": true,
	}, cookie)
	id, _ := decodeData(t, rec)["id"].(string)

	// 内容刻意超过 200 行默认窗口，用来证明导出不受「显示末尾 N 行」影响
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("line-")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte('\n')
	}
	full := sb.String()
	if err := os.WriteFile(filepath.Join(h.logDir, id+".log"), []byte(full), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	// 带上 lines 参数也不该被截断
	rec = h.do(t, http.MethodGet, "/api/jobs/"+id+"/log/download?lines=1", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != full {
		t.Errorf("导出内容被截断：得到 %d 字节，期望 %d 字节", len(got), len(full))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	disp := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disp, "attachment;") {
		t.Errorf("Content-Disposition = %q", disp)
	}
	if !strings.Contains(disp, "filename*=UTF-8''") {
		t.Errorf("非 ASCII 文件名应走 filename*：%q", disp)
	}
	if strings.ContainsAny(disp, "\r\n") {
		t.Errorf("Content-Disposition 含换行，存在头注入风险：%q", disp)
	}

	raw := rec.Header().Get("X-Log-Filename")
	name, err := url.PathUnescape(raw)
	if err != nil {
		t.Fatalf("X-Log-Filename 解码失败: %v (%q)", err, raw)
	}
	if !strings.HasPrefix(name, "每日备份_清理-") {
		t.Errorf("文件名 = %q，期望以「每日备份_清理-」开头（路径分隔符被替换）", name)
	}
	if !strings.HasSuffix(name, ".log") {
		t.Errorf("文件名 = %q，期望 .log 结尾", name)
	}
}

func TestJobLogDownloadMissing(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)

	rec := h.do(t, http.MethodPost, "/api/jobs", map[string]any{
		"name": "no-log", "schedule": "* * * * *", "command": "/bin/echo",
	}, cookie)
	id, _ := decodeData(t, rec)["id"].(string)

	rec = h.do(t, http.MethodGet, "/api/jobs/"+id+"/log/download", nil, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
	if msg := errorOf(t, rec); !strings.Contains(msg, "日志文件") {
		t.Errorf("error = %q", msg)
	}
}

func TestJobLogDownloadUnknownJob(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)
	rec := h.do(t, http.MethodGet, "/api/jobs/deadbeef/log/download", nil, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestJobLogClear(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)

	rec := h.do(t, http.MethodPost, "/api/jobs", map[string]any{
		"name": "clear", "schedule": "* * * * *", "command": "/bin/echo", "captureLog": true,
	}, cookie)
	id, _ := decodeData(t, rec)["id"].(string)

	logPath := filepath.Join(h.logDir, id+".log")
	if err := os.WriteFile(logPath, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	rec = h.do(t, http.MethodDelete, "/api/jobs/"+id+"/log", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if freed, _ := decodeData(t, rec)["freed"].(float64); freed != 8 {
		t.Errorf("freed = %v, want 8", decodeData(t, rec)["freed"])
	}

	// 关键：文件必须还在（截断语义），而不是被 unlink
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("清空后文件应仍然存在: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("清空后 size = %d, want 0", fi.Size())
	}

	// 查看接口此时应报文件为空（必须在重新写入之前断言）
	rec = h.do(t, http.MethodGet, "/api/jobs/"+id+"/log", nil, cookie)
	if data := decodeData(t, rec); data["content"] != "" || data["exists"] != true {
		t.Errorf("清空后 log 接口返回 content=%v exists=%v", data["content"], data["exists"])
	}

	// 再写入应当照常追加，说明 inode 没被换掉
	if err := os.WriteFile(logPath, []byte("after\n"), 0o644); err != nil {
		t.Fatalf("清空后写入失败: %v", err)
	}
	if got, _ := os.ReadFile(logPath); string(got) != "after\n" {
		t.Errorf("清空后内容 = %q", got)
	}
}

func TestJobLogClearMissing(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)

	rec := h.do(t, http.MethodPost, "/api/jobs", map[string]any{
		"name": "no-log", "schedule": "* * * * *", "command": "/bin/echo",
	}, cookie)
	id, _ := decodeData(t, rec)["id"].(string)

	rec = h.do(t, http.MethodDelete, "/api/jobs/"+id+"/log", nil, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
}

func TestJobLogEndpointsRequireAuth(t *testing.T) {
	h := newHarness(t, "")
	for _, tc := range []struct {
		method string
		target string
	}{
		{http.MethodGet, "/api/jobs/deadbeef/log/download"},
		{http.MethodDelete, "/api/jobs/deadbeef/log"},
	} {
		rec := h.do(t, tc.method, tc.target, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", tc.method, tc.target, rec.Code)
		}
	}
}

func TestSanitizeFileName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"每日备份", "每日备份"},
		{"a/b\\c", "a_b_c"},
		{"a:b*c?d", "a_b_c_d"},
		{"quote\"here", "quote_here"},
		{"cr\r\nlf", "crlf"},
		{"  spaced  ", "spaced"},
	} {
		if got := sanitizeFileName(tc.in); got != tc.want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLogFileNameFallsBackToID(t *testing.T) {
	// 名字只由控制字符/空白组成时会被清空，此时应退回任务 ID
	name := logFileName(crontab.Job{ID: "a1b2c3d4", Name: "\r\n\t  "})
	if !strings.HasPrefix(name, "a1b2c3d4-") {
		t.Errorf("logFileName = %q，名字被清空后应退回任务 ID", name)
	}
	if !strings.HasSuffix(name, ".log") {
		t.Errorf("logFileName = %q", name)
	}
	// 名字里的路径分隔符应被替换，而不是触发回退
	if got := logFileName(crontab.Job{ID: "a1b2c3d4", Name: "/etc/passwd"}); !strings.HasPrefix(got, "_etc_passwd-") {
		t.Errorf("logFileName = %q", got)
	}
}

func TestEncodeRFC5987(t *testing.T) {
	// 中文按 UTF-8 逐字节编码；attr-char 之外的字符必须被转义
	got := encodeRFC5987("备份.log")
	want := "%E5%A4%87%E4%BB%BD.log"
	if got != want {
		t.Errorf("encodeRFC5987 = %q, want %q", got, want)
	}
	for _, bad := range []string{" ", "'", "(", ")", ":", ";", "@", "*"} {
		if enc := encodeRFC5987(bad); strings.Contains(enc, bad) {
			t.Errorf("encodeRFC5987(%q) = %q，该字符应被转义", bad, enc)
		}
	}
}

func TestSystemLogEndpoint(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)
	rec := h.do(t, http.MethodGet, "/api/logs/system?lines=50", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec)
	if !strings.Contains(data["content"].(string), "/opt/a.sh") {
		t.Errorf("content = %v", data["content"])
	}
	if data["source"] != "testlog" {
		t.Errorf("source = %v, want testlog", data["source"])
	}
}

func TestSystemLogUnavailable(t *testing.T) {
	h := newHarness(t, "")
	h.src = nil
	cookie := h.login(t)
	rec := h.do(t, http.MethodGet, "/api/logs/system", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	data := decodeData(t, rec)
	if !strings.Contains(data["message"].(string), "无法") {
		t.Errorf("message = %v", data["message"])
	}
}

func TestLogFilesEndpoint(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)
	if err := os.WriteFile(filepath.Join(h.logDir, "abc.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rec := h.do(t, http.MethodGet, "/api/logs/files", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	files, _ := decodeData(t, rec)["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	entry, _ := files[0].(map[string]any)
	if entry["name"] != "abc.log" {
		t.Errorf("name = %v", entry["name"])
	}
}

func TestValidateEndpoint(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)

	rec := h.do(t, http.MethodGet, "/api/validate?schedule=*/5+*+*+*+*", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	data := decodeData(t, rec)
	if data["valid"] != true {
		t.Errorf("valid = %v", data["valid"])
	}
	if data["description"] != "每 5 分钟" {
		t.Errorf("description = %v", data["description"])
	}

	rec = h.do(t, http.MethodGet, "/api/validate?schedule=99+*+*+*+*", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	data = decodeData(t, rec)
	if data["valid"] != false {
		t.Errorf("valid = %v, want false", data["valid"])
	}
	if !strings.Contains(data["error"].(string), "分钟") {
		t.Errorf("error = %v", data["error"])
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newHarness(t, "")
	rec := h.do(t, http.MethodGet, "/login", nil)
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q", got)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy missing")
	}
}

func TestCronFailureIsReported(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)
	h.runner.fail = context.Canceled
	rec := h.do(t, http.MethodGet, "/api/jobs", nil, cookie)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if msg := errorOf(t, rec); msg == "" {
		t.Error("error message should not be empty")
	}
}

func TestUnknownAPIRoute(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login(t)
	rec := h.do(t, http.MethodGet, "/api/nope", nil, cookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
