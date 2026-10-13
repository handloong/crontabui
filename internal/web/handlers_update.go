package web

import (
	"net/http"

	"crontabui/internal/update"
)

// errUpdateDisabled 是未启用自助更新时统一的提示。
const errUpdateDisabled = "当前服务未启用自助更新（启动时可用 --self-update=false 关闭）"

// ---------- 自助更新 ----------

// handleUpdateState 返回更新状态，前端在弹窗里轮询它。
// 未启用时也返回 200，只是 enabled=false，便于前端决定是否显示入口。
func (s *Server) handleUpdateState(w http.ResponseWriter, r *http.Request) {
	if s.opts.Updater == nil {
		writeData(w, update.State{
			Enabled:  false,
			Platform: update.Platform(),
			Phase:    update.PhaseIdle,
			Progress: -1,
			Current:  s.opts.Version,
			Message:  "当前服务未启用自助更新",
		})
		return
	}
	writeData(w, s.opts.Updater.State())
}

// handleUpdateCheck 到 GitHub 查询最新版本，同步返回结果。
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.opts.Updater == nil {
		writeError(w, http.StatusNotImplemented, errUpdateDisabled)
		return
	}
	st, err := s.opts.Updater.Check(r.Context())
	if err != nil {
		// 上游（GitHub / 网络）的问题，不属于本服务的错误
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeData(w, st)
}

// handleUpdateApply 启动更新流程。下载可能要几分钟，所以此处只负责「发车」，
// 真正的进度由前端轮询 handleUpdateState 获取。
func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if s.opts.Updater == nil {
		writeError(w, http.StatusNotImplemented, errUpdateDisabled)
		return
	}
	if err := s.opts.Updater.Start(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeData(w, map[string]any{"started": true})
}
