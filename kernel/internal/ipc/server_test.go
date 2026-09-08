package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os/exec"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"edge-sync/kernel/internal/config"
	"edge-sync/kernel/internal/logring"
	"edge-sync/kernel/internal/runner"
	"edge-sync/kernel/internal/state"
	"edge-sync/pkg/protocol"
)

// testStack 搭 runner + ipc server（含可编译的 plugin-local 任务）。
type testStack struct {
	cfg      *config.Config
	cfgPath  string
	cfgMu    sync.Mutex
	runner   *runner.Runner
	server   *Server
	socket   string
	dataDir  string
	stateDir string
}



func (ts *testStack) setCfg(c *config.Config) error {
	ts.cfgMu.Lock()
	ts.cfg = c
	ts.cfgMu.Unlock()
	ts.runner.Reload(c)
	return nil
}

func newTestStack(t *testing.T, tasks []*config.Task) *testStack {
	t.Helper()
	bin := buildPluginLocal(t)
	base := t.TempDir()
	ts := &testStack{
		dataDir:  filepath.Join(base, "data"),
		stateDir: filepath.Join(base, "state"),
		socket:   filepath.Join(base, "s.sock"),
	}
	ts.cfgPath = filepath.Join(base, "config.yaml")
	ts.cfg = &config.Config{
		Storage: config.StorageConfig{
			DataDir: ts.dataDir, StateDir: ts.stateDir, PluginBinDir: filepath.Dir(bin),
		},
		Server: config.ServerConfig{Socket: ts.socket},
		Tasks:  tasks,
	}
	ring := logring.NewRing(100)
	logger := slog.New(logring.NewHandler(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}), ring))
	store := state.NewStore(ts.stateDir)
	ts.runner = runner.New(ts.cfg, store, logger)
	ts.runner.Start(context.Background())
	t.Cleanup(ts.runner.Stop)

	var err error
	ts.server, err = Listen(ts.socket, Deps{
		CfgPath: ts.cfgPath,
		GetCfg:  ts.current,
		SetCfg:  ts.setCfg,
		Runner:  ts.runner,
		Store:   store,
		Ring:    ring,
		Log:     logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	go ts.server.Serve()
	t.Cleanup(ts.server.Close)
	return ts
}

// current 返回当前配置快照。
func (ts *testStack) current() *config.Config {
	ts.cfgMu.Lock()
	defer ts.cfgMu.Unlock()
	return ts.cfg
}

func buildPluginLocal(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "plugin-local")
	cmd := exec.Command("go", "build", "-o", bin, "edge-sync/plugins/plugin-local")
	cmd.Dir = "../../../"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build plugin-local: %v\n%s", err, out)
	}
	return bin
}

// call 行协议客户端单次调用。
func call(t *testing.T, socket, method string, params any) (json.RawMessage, *protocol.Error) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	rawParams, _ := json.Marshal(params)
	line, _ := json.Marshal(protocol.Request{JSONRPC: "2.0", ID: 1, Method: method, Params: rawParams})
	conn.Write(append(line, '\n'))

	reader := bufio.NewReader(conn)
	respLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal([]byte(respLine), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp.Result, resp.Error
}

func mustDecode(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}

func localTask(name, root string) *config.Task {
	return &config.Task{
		Name: name, Plugin: "local",
		Interval: config.Duration{Duration: time.Minute},
		Options:  map[string]any{"root": root},
	}
}

func TestIpcTaskListAndStatus(t *testing.T) {
	src := t.TempDir()
	ts := newTestStack(t, []*config.Task{localTask("t1", src), localTask("t2", src)})

	var list []TaskSummary
	raw, rpcErr := call(t, ts.socket, "task.list", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("task.list: %v", rpcErr)
	}
	mustDecode(t, raw, &list)
	if len(list) != 2 || list[0].Name != "t1" || list[0].Status != "idle" {
		t.Fatalf("task.list = %+v", list)
	}

	raw, rpcErr = call(t, ts.socket, "task.status", map[string]any{"name": "t1"})
	if rpcErr != nil {
		t.Fatalf("task.status: %v", rpcErr)
	}
	var detail TaskDetail
	mustDecode(t, raw, &detail)
	if detail.Name != "t1" || detail.Plugin != "local" {
		t.Fatalf("detail = %+v", detail)
	}

	_, rpcErr = call(t, ts.socket, "task.status", map[string]any{"name": "nope"})
	if rpcErr == nil || rpcErr.Code != protocol.CodeNotFound {
		t.Fatalf("want NotFound, got %v", rpcErr)
	}
}

func TestIpcTriggerAndHistory(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newTestStack(t, []*config.Task{localTask("t1", src)})

	_, rpcErr := call(t, ts.socket, "task.trigger", map[string]any{"name": "t1"})
	if rpcErr != nil {
		t.Fatalf("trigger: %v", rpcErr)
	}

	// 历史里应有一个版本。
	var versions []VersionInfo
	raw, rpcErr := call(t, ts.socket, "history.list", map[string]any{"name": "t1"})
	if rpcErr != nil {
		t.Fatalf("history.list: %v", rpcErr)
	}
	mustDecode(t, raw, &versions)
	if len(versions) != 1 || !versions[0].IsCurrent || versions[0].Files != 1 {
		t.Fatalf("versions = %+v", versions)
	}

	// history.files 列根目录。
	var files []FileListEntry
	raw, rpcErr = call(t, ts.socket, "history.files", map[string]any{"name": "t1", "version": "current"})
	if rpcErr != nil {
		t.Fatalf("history.files: %v", rpcErr)
	}
	mustDecode(t, raw, &files)
	if len(files) != 1 || files[0].Name != "a.txt" || files[0].IsDir {
		t.Fatalf("files = %+v", files)
	}

	// history.files 带 subPath 穿越被拒。
	_, rpcErr = call(t, ts.socket, "history.files", map[string]any{"name": "t1", "version": "current", "subPath": "../../"})
	if rpcErr == nil {
		t.Fatal("path traversal should be rejected")
	}

	// history.fileVersions：改文件再触发一轮，应出现两个版本记录。
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = call(t, ts.socket, "task.trigger", map[string]any{"name": "t1"})
	var fv []FileVersionEntry
	raw, rpcErr = call(t, ts.socket, "history.fileVersions", map[string]any{"name": "t1", "path": "a.txt"})
	if rpcErr != nil {
		t.Fatalf("history.fileVersions: %v", rpcErr)
	}
	mustDecode(t, raw, &fv)
	if len(fv) != 2 {
		t.Fatalf("fileVersions = %+v, want 2 (both syncs changed the file)", fv)
	}
}

func TestIpcConfigReload(t *testing.T) {
	src := t.TempDir()
	binDir := t.TempDir()
	_ = binDir
	ts := newTestStack(t, []*config.Task{localTask("t1", src)})

	// 写一份新配置（加任务）。
	newCfg := `storage:
  dataDir: ` + ts.dataDir + `
  stateDir: ` + ts.stateDir + `
  pluginBinDir: ` + filepath.Dir(binDir) + `
tasks:
  - name: t1
    plugin: local
    options:
      root: ` + src + `
  - name: t2
    plugin: local
    interval: 2m
    options:
      root: ` + src + `
`
	if err := os.WriteFile(ts.cfgPath, []byte(newCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	raw, rpcErr := call(t, ts.socket, "config.reload", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("config.reload: %v", rpcErr)
	}
	mustDecode(t, raw, &res)
	if res["reloaded"] != true {
		t.Fatalf("reload result = %v", res)
	}
	time.Sleep(100 * time.Millisecond) // 等 reload 收敛
	var list []TaskSummary
	raw, _ = call(t, ts.socket, "task.list", map[string]any{})
	mustDecode(t, raw, &list)
	if len(list) != 2 {
		t.Fatalf("after reload, task.list = %+v", list)
	}

	// 坏配置 → 错误响应，任务集保持。
	if err := os.WriteFile(ts.cfgPath, []byte("tasks:\n  - name: '../bad'\n    plugin: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, rpcErr = call(t, ts.socket, "config.reload", map[string]any{})
	if rpcErr == nil {
		t.Fatal("invalid config should fail reload")
	}
	raw, _ = call(t, ts.socket, "task.list", map[string]any{})
	mustDecode(t, raw, &list)
	if len(list) != 2 {
		t.Fatalf("task set should be unchanged after failed reload, got %d", len(list))
	}
}

func TestIpcLogTailAndPluginList(t *testing.T) {
	src := t.TempDir()
	ts := newTestStack(t, []*config.Task{localTask("t1", src)})

	// 触发一次产生日志。
	call(t, ts.socket, "task.trigger", map[string]any{"name": "t1"})

	var logs []map[string]any
	raw, rpcErr := call(t, ts.socket, "log.tail", map[string]any{"n": 10})
	if rpcErr != nil {
		t.Fatalf("log.tail: %v", rpcErr)
	}
	mustDecode(t, raw, &logs)
	if len(logs) == 0 {
		t.Fatal("log.tail should return entries after a sync")
	}

	var plugins []PluginInfo
	raw, rpcErr = call(t, ts.socket, "plugin.list", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("plugin.list: %v", rpcErr)
	}
	mustDecode(t, raw, &plugins)
	found := false
	for _, p := range plugins {
		if p.Name == "local" {
			found = true
		}
	}
	if !found {
		t.Fatalf("plugin-local not discovered: %+v", plugins)
	}

	// 未知方法。
	_, rpcErr = call(t, ts.socket, "bogus.method", map[string]any{})
	if rpcErr == nil || rpcErr.Code != protocol.CodeMethodNotFound {
		t.Fatalf("want MethodNotFound, got %v", rpcErr)
	}
}
