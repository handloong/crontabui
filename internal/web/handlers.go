package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"crontabui/internal/crontab"
	"crontabui/internal/logview"
)

// errBodyTooLarge 用于把 http.MaxBytesError 归一化为 413。
var errBodyTooLarge = errors.New("请求体超过大小限制")

type envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// 响应已经开始写出，只能记录。
		fmt.Fprintf(os.Stderr, "crontabui: 写入响应失败: %v\n", err)
	}
}

func writeData(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, envelope{OK: true, Data: data})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, envelope{OK: false, Error: message})
}

// fail 把内部错误映射为合适的 HTTP 状态码。
func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBodyTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "请求体过大")
	case errors.Is(err, crontab.ErrJobNotFound):
		writeError(w, http.StatusNotFound, "任务不存在或已被其它方式修改，请刷新后重试")
	default:
		s.logger.Error("请求处理失败", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("请求体为空")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errBodyTooLarge
		}
		return fmt.Errorf("请求参数解析失败：%w", err)
	}
	return nil
}

func (s *Server) linesParam(r *http.Request) int {
	lines := defaultLogLines
	if raw := r.URL.Query().Get("lines"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			lines = n
		}
	}
	if lines < 1 {
		lines = 1
	}
	if lines > s.opts.MaxLogLines {
		lines = s.opts.MaxLogLines
	}
	return lines
}

// ---------- 页面 ----------

// versionPlaceholder 是内嵌 HTML 里的版本占位符。
// 页面响应头带了 CSP（script-src 'self'），内联 <script> 会被浏览器拒绝，
// 因此版本号只能在服务端渲染时替换，不能靠前端脚本注入。
const versionPlaceholder = "{{VERSION}}"

func (s *Server) serveStaticPage(w http.ResponseWriter, name string) {
	raw, err := readStatic(name)
	if err != nil {
		s.logger.Error("读取内嵌页面失败", "name", name, "err", err)
		http.Error(w, "页面资源缺失", http.StatusInternalServerError)
		return
	}
	body := bytes.ReplaceAll(raw, []byte(versionPlaceholder), []byte(s.opts.Version))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.serveStaticPage(w, "index.html")
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	s.serveStaticPage(w, "login.html")
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "healthy"})
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "接口不存在："+r.URL.Path)
}

// ---------- 会话 ----------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	var req struct {
		Token string `json:"token"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}

	// 先校验令牌，再考虑限流：限流只惩罚“猜错”，正确令牌永远放行。
	// 否则当服务位于反向代理之后（所有请求同一来源 IP）时，
	// 限流会被反向利用成对合法用户的拒绝服务。
	if !s.opts.Auth.VerifyToken(req.Token) {
		if !s.opts.Auth.AllowLogin(ip) {
			writeError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
			return
		}
		s.opts.Auth.RecordFailure(ip)
		writeError(w, http.StatusUnauthorized, "令牌不正确")
		return
	}

	s.opts.Auth.ResetFailures(ip)
	id, expires := s.opts.Auth.CreateSession()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(time.Until(expires).Seconds()),
	})
	writeData(w, map[string]any{"authenticated": true, "expiresAt": expires.UTC().Format(time.RFC3339)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if id := sessionID(r); id != "" {
		s.opts.Auth.Destroy(id)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	writeData(w, map[string]any{"authenticated": false})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	authenticated := false
	if id := sessionID(r); id != "" {
		authenticated = s.opts.Auth.Validate(id)
	}
	writeData(w, map[string]any{"authenticated": authenticated, "logDir": s.opts.LogDir})
}

// ---------- 任务 ----------

type jobRequest struct {
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
	Command    string `json:"command"`
	Enabled    *bool  `json:"enabled"`
	CaptureLog bool   `json:"captureLog"`
}

func (jr jobRequest) toJob() crontab.Job {
	enabled := true
	if jr.Enabled != nil {
		enabled = *jr.Enabled
	}
	return crontab.Job{
		Name:       jr.Name,
		Schedule:   jr.Schedule,
		Command:    jr.Command,
		Enabled:    enabled,
		CaptureLog: jr.CaptureLog,
	}
}

type jobView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Schedule    string `json:"schedule"`
	Description string `json:"description"`
	Command     string `json:"command"`
	Enabled     bool   `json:"enabled"`
	CaptureLog  bool   `json:"captureLog"`
	LogFile     string `json:"logFile"`
	LogExists   bool   `json:"logExists"`
	LogSize     int64  `json:"logSize"`
	Warning     string `json:"warning,omitempty"`
}

type foreignView struct {
	Lines []string `json:"lines"`
}

type jobsView struct {
	Jobs       []jobView     `json:"jobs"`
	Foreign    []foreignView `json:"foreign"`
	LogDir     string        `json:"logDir"`
	Raw        string        `json:"raw"`
	CrontabBin string        `json:"crontabBin"`
	Enabled    int           `json:"enabledCount"`
	Disabled   int           `json:"disabledCount"`
}

func (s *Server) viewOf(job crontab.Job) jobView {
	v := jobView{
		ID:          job.ID,
		Name:        job.Name,
		Schedule:    job.Schedule,
		Description: crontab.DescribeSchedule(job.Schedule),
		Command:     job.Command,
		Enabled:     job.Enabled,
		CaptureLog:  job.CaptureLog,
		LogFile:     job.LogFile(s.opts.LogDir),
		Warning:     crontab.CommandWarning(job.Command),
	}
	if info, err := os.Stat(v.LogFile); err == nil && info.Mode().IsRegular() {
		v.LogExists = true
		v.LogSize = info.Size()
	}
	return v
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	raw, entries, err := s.opts.Store.Load(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	view := jobsView{
		Jobs:    []jobView{},
		Foreign: []foreignView{},
		LogDir:  s.opts.LogDir,
		Raw:     raw,
	}
	var block []string
	flush := func() {
		if len(block) == 0 {
			return
		}
		lines := make([]string, len(block))
		copy(lines, block)
		view.Foreign = append(view.Foreign, foreignView{Lines: lines})
		block = block[:0]
	}
	for _, e := range entries {
		if e.Kind == crontab.KindJob && e.Job != nil {
			flush()
			view.Jobs = append(view.Jobs, s.viewOf(*e.Job))
			if e.Job.Enabled {
				view.Enabled++
			} else {
				view.Disabled++
			}
			continue
		}
		block = append(block, e.Raw...)
	}
	flush()
	writeData(w, view)
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req jobRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	job := req.toJob()
	if err := crontab.ValidateJob(job); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.opts.Store.Add(r.Context(), job)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeData(w, s.viewOf(created))
}

func (s *Server) handleUpdateJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 先确认目标存在：资源不存在时 404 优先于请求体校验错误。
	if _, err := s.opts.Store.Get(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	var req jobRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	job := req.toJob()
	if err := crontab.ValidateJob(job); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.opts.Store.Update(r.Context(), id, job); err != nil {
		s.fail(w, err)
		return
	}
	job.ID = id
	writeData(w, s.viewOf(job))
}

func (s *Server) handleToggleJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.opts.Store.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "缺少 enabled 字段")
		return
	}
	if err := s.opts.Store.Toggle(r.Context(), id, *req.Enabled); err != nil {
		s.fail(w, err)
		return
	}
	job.Enabled = *req.Enabled
	writeData(w, s.viewOf(job))
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.opts.Store.Delete(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	writeData(w, map[string]any{"deleted": id})
}

func (s *Server) handleRunJob(w http.ResponseWriter, r *http.Request) {
	if s.opts.Exec == nil {
		writeError(w, http.StatusNotImplemented, "当前服务未启用「立即执行」功能")
		return
	}
	id := r.PathValue("id")
	job, err := s.opts.Store.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.opts.Exec(job); err != nil {
		writeError(w, http.StatusInternalServerError, "执行失败："+err.Error())
		return
	}
	writeData(w, map[string]any{"started": true, "id": id, "command": job.Command})
}

// ---------- 日志 ----------

func (s *Server) handleJobLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.opts.Store.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	lines := s.linesParam(r)
	path := job.LogFile(s.opts.LogDir)

	content, err := logview.Tail(path, lines, 0)
	exists := true
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		exists = false
		content = ""
	default:
		s.fail(w, err)
		return
	}
	writeData(w, map[string]any{
		"id":      job.ID,
		"file":    path,
		"exists":  exists,
		"content": content,
		"lines":   lines,
	})
}

// handleJobLogDownload 以附件形式返回该任务日志的**完整原始文件**。
//
// 与查看接口不同，这里刻意不做行数截断：导出的语义就是「拿到整份日志」。
// 路径一律由 Job.LogFile(logDir) 推导，不接受任何外部传入的路径参数。
func (s *Server) handleJobLogDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.opts.Store.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	path := job.LogFile(s.opts.LogDir)

	f, err := openLogFile(path)
	if err != nil {
		s.failLogError(w, err, path)
		return
	}
	defer f.Close()

	name := logFileName(job)
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Disposition", contentDisposition(name))
	// 供前端直接取用的文件名（同源请求可读自定义头），避免前后端各拼一套。
	h.Set("X-Log-Filename", encodeRFC5987(name))
	h.Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, f); err != nil {
		// 响应已开始写出，无法再改状态码，只能记录。
		s.logger.Warn("写出日志文件中断", "id", id, "err", err)
	}
}

// handleJobLogClear 清空任务日志内容（保留文件，见 logview.Truncate 的说明）。
func (s *Server) handleJobLogClear(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.opts.Store.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	path := job.LogFile(s.opts.LogDir)

	fi, err := os.Stat(path)
	if err != nil {
		s.failLogError(w, err, path)
		return
	}
	if fi.IsDir() {
		s.fail(w, fmt.Errorf("%s 是目录，不是日志文件", path))
		return
	}

	freed := fi.Size()
	if err := logview.Truncate(path); err != nil {
		s.failLogError(w, err, path)
		return
	}
	s.logger.Info("已清空任务日志", "id", id, "file", path, "freed", freed)
	writeData(w, map[string]any{"id": id, "file": path, "freed": freed})
}

// openLogFile 打开日志文件并确认它不是目录。
func openLogFile(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if fi.IsDir() {
		f.Close()
		return nil, fmt.Errorf("%s 是目录，不是日志文件", path)
	}
	return f, nil
}

// failLogError 把日志文件操作的常见错误映射为可读提示。
func (s *Server) failLogError(w http.ResponseWriter, err error, path string) {
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "该任务还没有日志文件；请先在任务里勾选「捕获输出」，并等它执行过一次")
	case errors.Is(err, os.ErrPermission):
		writeError(w, http.StatusForbidden, "没有权限访问 "+path)
	default:
		s.fail(w, err)
	}
}

// logFileName 拼出建议的下载文件名：<任务名或ID>-YYYYMMDD-HHmmss.log
func logFileName(job crontab.Job) string {
	label := sanitizeFileName(job.Name)
	if label == "" {
		label = job.ID
	}
	// 按 rune 截断，避免把多字节字符切成半个。
	if r := []rune(label); len(r) > 60 {
		label = string(r[:60])
	}
	return label + "-" + time.Now().Format("20060102-150405") + ".log"
}

// sanitizeFileName 去掉路径分隔符、控制字符等不适合出现在文件名里的字符。
func sanitizeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			// 控制字符：包含 CR/LF，绝不能进 HTTP 头
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// contentDisposition 生成 RFC 6266 的附件头。
// 非 ASCII 文件名（中文任务名）放进 filename*，同时给一个 ASCII 兜底名。
func contentDisposition(filename string) string {
	ascii := make([]rune, 0, len(filename))
	for _, r := range filename {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			ascii = append(ascii, '_')
			continue
		}
		ascii = append(ascii, r)
	}
	return `attachment; filename="` + string(ascii) + `"; filename*=UTF-8''` + encodeRFC5987(filename)
}

// encodeRFC5987 按 RFC 5987 的 attr-char 集合做百分号编码（逐字节，兼容 UTF-8）。
func encodeRFC5987(s string) string {
	const attrChar = "!#$&+-.^_`|~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case strings.IndexByte(attrChar, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func (s *Server) handleSystemLog(w http.ResponseWriter, r *http.Request) {
	lines := s.linesParam(r)
	if s.opts.FindLog == nil {
		writeData(w, map[string]any{"content": "", "source": "", "lines": lines,
			"message": "当前环境未配置系统日志来源"})
		return
	}
	src := s.opts.FindLog()
	if src == nil {
		writeData(w, map[string]any{"content": "", "source": "", "lines": lines,
			"message": "无法定位系统 cron 日志；可用 --cron-log 指定路径，或以 root 身份运行"})
		return
	}

	var content string
	switch {
	case src.Path != "":
		raw, err := logview.Tail(src.Path, lines*4, 0)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeData(w, map[string]any{"content": "", "source": src.Name, "lines": lines,
					"message": "系统日志文件 " + src.Path + " 不存在"})
				return
			}
			if errors.Is(err, os.ErrPermission) {
				writeData(w, map[string]any{"content": "", "source": src.Name, "lines": lines,
					"message": "没有权限读取 " + src.Path + "，请以 root 身份运行或调整文件权限"})
				return
			}
			s.fail(w, err)
			return
		}
		content = logview.FilterCronLines(raw)
	default:
		out, err := exec.CommandContext(r.Context(), src.Args[0], src.Args[1:]...).Output()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "执行 journalctl 失败："+strings.TrimSpace(err.Error()))
			return
		}
		content = string(out)
	}
	writeData(w, map[string]any{"content": content, "source": src.Name, "lines": lines})
}

func (s *Server) handleLogFiles(w http.ResponseWriter, r *http.Request) {
	files, err := logview.ListFiles(s.opts.LogDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeData(w, map[string]any{"files": []logview.File{}, "logDir": s.opts.LogDir})
			return
		}
		s.fail(w, err)
		return
	}
	writeData(w, map[string]any{"files": files, "logDir": s.opts.LogDir})
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	schedule := r.URL.Query().Get("schedule")
	command := r.URL.Query().Get("command")
	if err := crontab.ValidateSchedule(schedule); err != nil {
		writeData(w, map[string]any{"valid": false, "error": err.Error(), "description": ""})
		return
	}
	writeData(w, map[string]any{
		"valid":       true,
		"description": crontab.DescribeSchedule(schedule),
		"warning":     crontab.CommandWarning(command),
	})
}
