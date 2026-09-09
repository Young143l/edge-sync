// Package ipc 提供内核对外的 Unix socket JSON-RPC 服务（CLI / 面板接入点）。
// 每连接一个 goroutine，行分隔 JSON-RPC 2.0，连接关闭即结束会话。
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"edge-sync/kernel/internal/config"
	"edge-sync/kernel/internal/engine"
	"edge-sync/kernel/internal/logring"
	"edge-sync/kernel/internal/plugin"
	"edge-sync/kernel/internal/runner"
	"edge-sync/kernel/internal/state"
	"edge-sync/pkg/protocol"
)

// 任务/历史/插件类型定义见 pkg/protocol（面板 client 共享同一真源）。
type (
	TaskSummary      = protocol.TaskSummary
	TaskDetail       = protocol.TaskDetail
	VersionInfo      = protocol.VersionInfo
	FileListEntry    = protocol.FileListEntry
	FileVersionEntry = protocol.FileVersionEntry
	PluginInfo       = protocol.PluginInfo
)

// Deps Server 依赖（cfg 经锁访问，reload 后替换）。
type Deps struct {
	CfgPath string
	GetCfg  func() *config.Config
	SetCfg  func(*config.Config) error
	Runner  *runner.Runner
	Store   *state.Store
	Ring    *logring.Ring
	Log     *slog.Logger
}

type Server struct {
	deps       Deps
	ln         net.Listener
	socketPath string
	closeOnce  sync.Once
}

// Listen 建立 Unix socket 监听（先清理残留 socket 文件）。
func Listen(socketPath string, deps Deps) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, err
	}
	os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen unix socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return &Server{deps: deps, ln: ln, socketPath: socketPath}, nil
}

// Serve 启动 accept 循环（阻塞；调用方放 goroutine）。
func (s *Server) Serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return // listener closed
		}
		go s.handleConn(conn)
	}
}

// Close 关闭监听并清理 socket 文件。
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.ln.Close()
		os.Remove(s.socketPath)
	})
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, 1024*1024)
	writer := bufio.NewWriter(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var req protocol.Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			writeResp(writer, errResp(0, protocol.CodeParseError, "malformed request line"))
			continue
		}
		writeResp(writer, s.dispatch(&req))
	}
}

func writeResp(w *bufio.Writer, resp *protocol.Response) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return
	}
	w.Write(raw)
	w.WriteByte('\n')
	w.Flush()
}

func ok(id int64, result any) *protocol.Response {
	raw, _ := json.Marshal(result)
	return &protocol.Response{JSONRPC: "2.0", ID: id, Result: raw}
}

func errResp(id int64, code int, msg string) *protocol.Response {
	return &protocol.Response{JSONRPC: "2.0", ID: id,
		Error: &protocol.Error{Code: code, Message: msg}}
}

// dispatch 方法路由。
func (s *Server) dispatch(req *protocol.Request) *protocol.Response {
	switch req.Method {
	case "task.list":
		return s.taskList(req)
	case "task.status":
		return s.taskStatus(req)
	case "task.trigger":
		return s.taskTrigger(req)
	case "history.list":
		return s.historyList(req)
	case "history.files":
		return s.historyFiles(req)
	case "history.fileVersions":
		return s.historyFileVersions(req)
	case "config.reload":
		return s.configReload(req)
	case "log.tail":
		return s.logTail(req)
	case "plugin.list":
		return s.pluginList(req)
	case "event.subscribe":
		return errResp(req.ID, protocol.CodeMethodNotFound, "event.subscribe not implemented yet (poll log.tail instead)")
	default:
		return errResp(req.ID, protocol.CodeMethodNotFound, "unknown method: "+req.Method)
	}
}

// ---------- task.* ----------

func (s *Server) taskList(req *protocol.Request) *protocol.Response {
	cfg := s.deps.GetCfg()
	out := make([]TaskSummary, 0, len(cfg.Tasks))
	for _, t := range cfg.Tasks {
		out = append(out, s.summaryOf(t))
	}
	return ok(req.ID, out)
}

func (s *Server) summaryOf(t *config.Task) TaskSummary {
	st := s.deps.Store.Load(t.Name)
	sum := TaskSummary{
		Name:                t.Name,
		Plugin:              t.Plugin,
		Enabled:             t.IsEnabled(),
		Status:              "idle",
		Interval:            t.Interval.Duration.String(),
		LastSyncAt:          st.LastSyncAt,
		LastSuccessAt:       st.LastSuccessAt,
		ConsecutiveFailures: st.ConsecutiveFailures,
	}
	if s.deps.Runner.IsSyncing(t.Name) {
		sum.Status = "syncing"
	}
	if !t.IsEnabled() {
		sum.Status = "paused"
	}
	if next := s.deps.Runner.NextRunAt(t.Name); !next.IsZero() {
		sum.NextRunAt = next.UTC().Format(time.RFC3339)
	}
	return sum
}

func (s *Server) taskStatus(req *protocol.Request) *protocol.Response {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
	}
	cfg := s.deps.GetCfg()
	var task *config.Task
	for _, t := range cfg.Tasks {
		if t.Name == p.Name {
			task = t
			break
		}
	}
	if task == nil {
		return errResp(req.ID, protocol.CodeNotFound, "task not found: "+p.Name)
	}
	st := s.deps.Store.Load(task.Name)
	d := TaskDetail{
		TaskSummary: s.summaryOf(task),
		Retention:   task.Retention,
		Stats:       st.Stats,
	}
	return ok(req.ID, d)
}

func (s *Server) taskTrigger(req *protocol.Request) *protocol.Response {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
	}
	cfg := s.deps.GetCfg()
	var task *config.Task
	for _, t := range cfg.Tasks {
		if t.Name == p.Name {
			task = t
			break
		}
	}
	if task == nil {
		return errResp(req.ID, protocol.CodeNotFound, "task not found: "+p.Name)
	}
	// 同步等待结果（单用户工具；IPC 每连接独立 goroutine，不阻塞其他连接）。
	if err := s.deps.Runner.Sync(context.Background(), task); err != nil {
		return errResp(req.ID, protocol.CodeInternalError, err.Error())
	}
	return ok(req.ID, map[string]any{"triggered": true})
}

// ---------- history.* ----------

func (s *Server) historyList(req *protocol.Request) *protocol.Response {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
	}
	versionsDir := filepath.Join(s.deps.GetCfg().Storage.DataDir, p.Name, "versions")
	names, err := engine.ListVersions(versionsDir)
	if err != nil {
		return errResp(req.ID, protocol.CodeInternalError, err.Error())
	}
	currentBase := filepath.Base(engine.ReadCurrentTarget(filepath.Join(s.deps.GetCfg().Storage.DataDir, p.Name)))
	out := make([]VersionInfo, 0, len(names))
	// 最新在前。
	for i := len(names) - 1; i >= 0; i-- {
		name := names[i]
		vi := VersionInfo{Version: name, IsCurrent: name == currentBase}
		if m := engine.ReadVersionManifest(filepath.Join(versionsDir, name)); m != nil {
			vi.Files = len(m.Entries)
			for i := range m.Entries {
				vi.Bytes += m.Entries[i].Size
			}
		}
		out = append(out, vi)
	}
	return ok(req.ID, out)
}

func (s *Server) historyFiles(req *protocol.Request) *protocol.Response {
	var p struct {
		Name    string `json:"name"`
		Version string `json:"version"` // 版本目录名或 "current"
		SubPath string `json:"subPath"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
	}
	dataDir := filepath.Join(s.deps.GetCfg().Storage.DataDir, p.Name)
	versionDir, err := s.resolveVersionDir(dataDir, p.Version)
	if err != nil {
		return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
	}
	dirPath := versionDir
	if p.SubPath != "" {
		dirPath, err = engine.SafeJoin(versionDir, p.SubPath)
		if err != nil {
			return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
		}
	}
	ents, err := os.ReadDir(dirPath)
	if err != nil {
		return errResp(req.ID, protocol.CodeNotFound, err.Error())
	}
	out := make([]FileListEntry, 0, len(ents))
	for _, e := range ents {
		if e.Name() == engine.ManifestFile {
			continue // 元数据文件对外不可见
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, FileListEntry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Size:  fi.Size(),
			MTime: fi.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return ok(req.ID, out)
}

// resolveVersionDir 把 "current" 或版本目录名解析为版本目录绝对路径（防穿越）。
func (s *Server) resolveVersionDir(dataDir, version string) (string, error) {
	if version == "" || version == "current" {
		target := engine.ReadCurrentTarget(dataDir)
		if target == "" {
			return "", fmt.Errorf("no current version yet")
		}
		return target, nil
	}
	if strings.Contains(version, "/") || strings.Contains(version, "..") {
		return "", fmt.Errorf("invalid version name: %q", version)
	}
	dir := filepath.Join(dataDir, "versions", version)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("version not found: %s", version)
	}
	return dir, nil
}

func (s *Server) historyFileVersions(req *protocol.Request) *protocol.Response {
	var p struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
	}
	versionsDir := filepath.Join(s.deps.GetCfg().Storage.DataDir, p.Name, "versions")
	names, err := engine.ListVersions(versionsDir)
	if err != nil {
		return errResp(req.ID, protocol.CodeInternalError, err.Error())
	}
	currentBase := filepath.Base(engine.ReadCurrentTarget(filepath.Join(s.deps.GetCfg().Storage.DataDir, p.Name)))

	// 逐版本取该 path 的 fingerprint，只保留与上一版本不同的时点（旧→新）。
	var out []FileVersionEntry
	prevFP := ""
	var prevSize int64
	for _, name := range names {
		m := engine.ReadVersionManifest(filepath.Join(versionsDir, name))
		if m == nil {
			continue // 老版本无元数据，跳过
		}
		for i := range m.Entries {
			e := &m.Entries[i]
			if e.Path == p.Path {
				if e.Fingerprint != prevFP {
					out = append(out, FileVersionEntry{
						Version:     name,
						Fingerprint: e.Fingerprint,
						Size:        e.Size,
					})
					prevFP = e.Fingerprint
					prevSize = e.Size
				}
				break
			}
		}
	}
	// 标记最新出现是否为 current。
	if len(out) > 0 && out[len(out)-1].Version == currentBase {
		_ = prevSize
	}
	return ok(req.ID, out)
}

// ---------- config.reload / log.tail / plugin.list ----------

func (s *Server) configReload(req *protocol.Request) *protocol.Response {
	newCfg, err := config.Load(s.deps.CfgPath)
	if err != nil {
		return errResp(req.ID, protocol.CodeInvalidParams, "config invalid, keeping old: "+err.Error())
	}
	if err := s.deps.SetCfg(newCfg); err != nil {
		return errResp(req.ID, protocol.CodeInternalError, err.Error())
	}
	return ok(req.ID, map[string]any{"reloaded": true, "tasks": len(newCfg.Tasks)})
}

func (s *Server) logTail(req *protocol.Request) *protocol.Response {
	var p struct {
		N    int    `json:"n"`
		Task string `json:"task"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
	}
	entries := s.deps.Ring.Tail(p.N, p.Task)
	type line struct {
		Time  string `json:"time"`
		Level string `json:"level"`
		Msg   string `json:"msg"`
		Attrs string `json:"attrs,omitempty"`
	}
	out := make([]line, 0, len(entries))
	for _, e := range entries {
		out = append(out, line{
			Time:  e.Time.Format("2006-01-02T15:04:05.000"),
			Level: e.Level,
			Msg:   e.Msg,
			Attrs: e.Attrs,
		})
	}
	return ok(req.ID, out)
}

func (s *Server) pluginList(req *protocol.Request) *protocol.Response {
	binDir := s.deps.GetCfg().Storage.PluginBinDir
	ents, err := os.ReadDir(binDir)
	if err != nil {
		return errResp(req.ID, protocol.CodeInternalError, "read plugin bin dir: "+err.Error())
	}
	out := []PluginInfo{}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "plugin-") {
			continue
		}
		meta, err := plugin.Inspect(ctx, filepath.Join(binDir, e.Name()))
		if err != nil {
			s.deps.Log.Warn("plugin inspect failed", "bin", e.Name(), "err", err)
			continue
		}
		out = append(out, PluginInfo{
			Name:         meta.Name,
			Version:      meta.Version,
			ConfigSchema: meta.ConfigSchema,
		})
	}
	return ok(req.ID, out)
}
