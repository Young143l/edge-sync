// HTTP 路由：REST 映射内核 IPC + 鉴权 + 下载流 + 静态托管。
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"edge-sync/pkg/protocol"
)

type (
	VersionInfo      = protocol.VersionInfo
	FileListEntry    = protocol.FileListEntry
	FileVersionEntry = protocol.FileVersionEntry
	PluginInfo       = protocol.PluginInfo
)

type panelServer struct {
	cfg PanelConfig
	ipc *IPCClient
	dl  *downloadLimiter
}

func newPanelServer(cfg PanelConfig) *panelServer {
	return &panelServer{cfg: cfg, ipc: &IPCClient{SocketPath: cfg.Socket}, dl: newLimiter(2)}
}

// routes 装配全部路由（Go 1.22 ServeMux 模式）。
func (s *panelServer) routes() http.Handler {
	mux := http.NewServeMux()

	// 登录（鉴权之外）：种 HttpOnly cookie。
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)

	// 鉴权中间件包裹其余 /api/*。
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return s.requireAuth(next)
	}
	mux.HandleFunc("GET /api/overview", auth(s.handleOverview))
	mux.HandleFunc("GET /api/tasks/{name}", auth(s.handleTaskStatus))
	mux.HandleFunc("POST /api/tasks/{name}/sync", auth(s.handleSync))
	mux.HandleFunc("GET /api/tasks/{name}/history", auth(s.handleHistory))
	mux.HandleFunc("GET /api/tasks/{name}/files", auth(s.handleFiles))
	mux.HandleFunc("GET /api/tasks/{name}/file-versions", auth(s.handleFileVersions))
	mux.HandleFunc("GET /api/tasks/{name}/download", auth(s.handleDownload))
	mux.HandleFunc("GET /api/tasks/{name}/archive", auth(s.handleArchive))
	mux.HandleFunc("GET /api/logs", auth(s.handleLogs))
	mux.HandleFunc("GET /api/plugins", auth(s.handlePlugins))

	// 静态托管 + SPA fallback（无 /api 前缀）。
	mux.HandleFunc("/", s.handleStatic)

	return mux
}

// ---------- 鉴权 ----------

func (s *panelServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ck, err := r.Cookie(cookieName)
		cookieOK := err == nil && ck.Value == s.cfg.Token
		bearerOK := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") == s.cfg.Token &&
			r.Header.Get("Authorization") != ""
		if !cookieOK && !bearerOK {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "需要访问令牌"})
			return
		}
		next(w, r)
	}
}

func (s *panelServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Token != s.cfg.Token {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token 不正确"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.cfg.Token,
		Path:     "/",
		MaxAge:   30 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- 状态类（转发 IPC） ----------

func (s *panelServer) handleOverview(w http.ResponseWriter, r *http.Request) {
	out, err := s.ipc.list(r.Context())
	if err != nil {
		writeIpcErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *panelServer) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	out, err := s.ipc.status(r.Context(), r.PathValue("name"))
	if err != nil {
		writeIpcErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *panelServer) handleSync(w http.ResponseWriter, r *http.Request) {
	if err := s.ipc.trigger(r.Context(), r.PathValue("name")); err != nil {
		writeIpcErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"triggered": true})
}

func (s *panelServer) handleHistory(w http.ResponseWriter, r *http.Request) {
	var out []VersionInfo
	if err := s.ipc.call(r.Context(), "history.list",
		map[string]any{"name": r.PathValue("name")}, &out); err != nil {
		writeIpcErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *panelServer) handleFiles(w http.ResponseWriter, r *http.Request) {
	var out []FileListEntry
	if err := s.ipc.call(r.Context(), "history.files", map[string]any{
		"name":    r.PathValue("name"),
		"version": r.URL.Query().Get("version"),
		"subPath": r.URL.Query().Get("path"),
	}, &out); err != nil {
		writeIpcErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *panelServer) handleFileVersions(w http.ResponseWriter, r *http.Request) {
	var out []FileVersionEntry
	if err := s.ipc.call(r.Context(), "history.fileVersions", map[string]any{
		"name": r.PathValue("name"),
		"path": r.URL.Query().Get("path"),
	}, &out); err != nil {
		writeIpcErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *panelServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	n := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("n")); err == nil && v > 0 {
		n = min(v, 1000)
	}
	var out []logEntry
	if err := s.ipc.call(r.Context(), "log.tail", map[string]any{
		"n": n, "task": r.URL.Query().Get("task"),
	}, &out); err != nil {
		writeIpcErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleStatic 静态文件 + SPA fallback（webDir 未配置时给出提示）。
func (s *panelServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.cfg.WebDir == "" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("edge-sync panel: web dist not configured (build panel/web first)"))
		return
	}
	if r.URL.Path != "/" {
		p := filepath.Join(s.cfg.WebDir, filepath.Clean("/"+r.URL.Path))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			http.ServeFile(w, r, p)
			return
		}
	}
	// SPA fallback：其余路径回 index.html。
	http.ServeFile(w, r, filepath.Join(s.cfg.WebDir, "index.html"))
}

func (s *panelServer) handlePlugins(w http.ResponseWriter, r *http.Request) {
	var out []PluginInfo
	if err := s.ipc.call(r.Context(), "plugin.list", map[string]any{}, &out); err != nil {
		writeIpcErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeIpcErr IPC 错误 → HTTP 状态（panel-design 3.3 映射表）。
func writeIpcErr(w http.ResponseWriter, err error) {
	var ipcE *IpcError
	var connE *IpcConnectError
	switch {
	case errors.As(err, &connE):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": connE.Msg})
	case errors.As(err, &ipcE):
		switch ipcE.Code {
		case 404, protocolNotFoundCode:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": ipcE.Msg})
		case protocolInvalidParamsCode:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": ipcE.Msg})
		default:
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": ipcE.Msg})
		}
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// 404/400 语义码（与内核 protocol 错误码对应；本地常量避免引 internal）。
const (
	protocolNotFoundCode      = -32002
	protocolInvalidParamsCode = -32602
)

// logEntry log.tail 条目（与内核 logring 对齐）。
type logEntry struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Attrs string `json:"attrs,omitempty"`
}
