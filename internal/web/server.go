package web

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"crontabui/internal/auth"
	"crontabui/internal/crontab"
	"crontabui/internal/logview"
	"crontabui/internal/update"
)

const (
	// sessionCookieName 是会话 Cookie 名称。
	sessionCookieName = "crontabui_session"
	// maxBodyBytes 限制请求体大小，避免内存被单个请求撑爆。
	maxBodyBytes = 64 << 10
	// defaultMaxLogLines 是单次日志尾读的最大行数。
	defaultMaxLogLines = 2000
	// defaultLogLines 是日志接口的默认行数。
	defaultLogLines = 200
	// defaultVersion 是未注入版本号时展示的兜底值。
	defaultVersion = "dev"
)

// ExecFunc 立即执行一个任务。由调用方决定输出如何重定向。
type ExecFunc func(job crontab.Job) error

// Options 是 Server 的构造参数。
type Options struct {
	Token       string
	Auth        *auth.Manager
	Store       *crontab.Store
	LogDir      string
	FindLog     func() *logview.Source
	Exec        ExecFunc
	Logger      *slog.Logger
	MaxLogLines int
	// Version 是构建时通过 -ldflags 注入的版本号，展示在页面上。
	// 留空时回退为 "dev"。
	Version string
	// Updater 提供 Web 界面内的自助更新能力；为 nil 表示该功能未启用。
	Updater update.Updater
}

// Server 是 HTTP 服务。
type Server struct {
	opts    Options
	logger  *slog.Logger
	handler http.Handler
}

// New 构造 Server 并完成路由与中间件装配。
func New(opts Options) (*Server, error) {
	if opts.Auth == nil {
		return nil, errors.New("Options.Auth 不能为空")
	}
	if opts.Store == nil {
		return nil, errors.New("Options.Store 不能为空")
	}
	if opts.LogDir == "" {
		opts.LogDir = opts.Store.LogDir()
	}
	if opts.LogDir == "" {
		return nil, errors.New("Options.LogDir 不能为空")
	}
	if opts.MaxLogLines <= 0 {
		opts.MaxLogLines = defaultMaxLogLines
	}
	if strings.TrimSpace(opts.Version) == "" {
		opts.Version = defaultVersion
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	s := &Server{opts: opts, logger: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("GET /favicon.ico", s.handleFavicon)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	mux.HandleFunc("GET /{$}", s.handleIndex)

	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	mux.HandleFunc("POST /api/jobs", s.handleCreateJob)
	mux.HandleFunc("PUT /api/jobs/{id}", s.handleUpdateJob)
	mux.HandleFunc("DELETE /api/jobs/{id}", s.handleDeleteJob)
	mux.HandleFunc("POST /api/jobs/{id}/toggle", s.handleToggleJob)
	mux.HandleFunc("POST /api/jobs/{id}/run", s.handleRunJob)
	mux.HandleFunc("GET /api/jobs/{id}/log", s.handleJobLog)
	mux.HandleFunc("GET /api/logs/system", s.handleSystemLog)
	mux.HandleFunc("GET /api/logs/files", s.handleLogFiles)
	mux.HandleFunc("GET /api/validate", s.handleValidate)
	mux.HandleFunc("GET /api/update", s.handleUpdateState)
	mux.HandleFunc("POST /api/update/check", s.handleUpdateCheck)
	mux.HandleFunc("POST /api/update/apply", s.handleUpdateApply)
	mux.HandleFunc("/api/", s.handleAPINotFound)

	s.handler = s.recoverPanic(securityHeaders(s.authGate(mux)))
	return s, nil
}

// Handler 返回可直接挂载到 http.Server 的处理器。
func (s *Server) Handler() http.Handler { return s.handler }

// LogDir 返回任务日志目录。
func (s *Server) LogDir() string { return s.opts.LogDir }

// Version 返回用于页面展示的版本号。
func (s *Server) Version() string { return s.opts.Version }

func (s *Server) authGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 纵深防御：写入类请求要求同源，避免旧浏览器不遵守 SameSite=Strict 时被 CSRF。
		if isStateChanging(r.Method) && !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "已拒绝跨站请求")
			return
		}
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if id := sessionID(r); id != "" && s.opts.Auth.Validate(id) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusUnauthorized, "未登录或会话已过期，请重新登录")
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// sameOrigin 校验 Origin（缺失时退回 Referer）与请求 Host 是否一致。
// 两者都缺失时放行，以兼容 curl、脚本等非浏览器客户端。
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func isPublicPath(path string) bool {
	switch path {
	case "/healthz", "/login", "/favicon.ico", "/api/login", "/api/session":
		return true
	}
	return strings.HasPrefix(path, "/static/")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
				"connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("请求处理发生 panic", "path", r.URL.Path, "err", rec)
				writeError(w, http.StatusInternalServerError, "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func sessionID(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
