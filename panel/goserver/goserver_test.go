package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"edge-sync/pkg/protocol"
)

// fakeIPC 起一个假内核 IPC server（unix socket，返回预设响应）。
type fakeIPC struct {
	ln       net.Listener
	socket   string
	requests []string // 收到的 method 记录
}

func newFakeIPC(t *testing.T) *fakeIPC {
	t.Helper()
	f := &fakeIPC{}
	// macOS unix socket 路径限 104 字节，t.TempDir 太深；用根级短临时目录。
	tmp, err := os.MkdirTemp("", "ipc")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(tmp, "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	f.socket = ln.Addr().String()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		os.RemoveAll(tmp)
	})
	return f
}

func (f *fakeIPC) serve(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	var req protocol.Request
	if json.Unmarshal([]byte(line), &req) != nil {
		return
	}
	f.requests = append(f.requests, req.Method)

	var result any
	switch req.Method {
	case "task.list":
		result = []protocol.TaskSummary{
			{Name: "t1", Plugin: "local", Enabled: true, Status: "idle", Interval: "1m0s"},
		}
	case "task.status":
		result = protocol.TaskDetail{
			TaskSummary: protocol.TaskSummary{Name: "t1", Plugin: "local", Enabled: true, Status: "idle", Interval: "1m0s"},
			Retention:   protocol.Retention{KeepLast: 10},
		}
	case "history.list":
		result = []protocol.VersionInfo{{Version: "20260909-000001", Files: 1, Bytes: 5, IsCurrent: true}}
	case "history.files":
		result = []protocol.FileListEntry{{Name: "a.txt", Size: 5}}
	case "history.fileVersions":
		result = []protocol.FileVersionEntry{{Version: "20260909-000001", Fingerprint: "h1", Size: 5}}
	case "log.tail":
		result = []map[string]any{{"time": "t", "level": "info", "msg": "synced"}}
	case "plugin.list":
		result = []protocol.PluginInfo{{Name: "local", Version: "0.1.0"}}
	default:
		result = map[string]any{}
	}
	raw, _ := json.Marshal(result)
	resp, _ := json.Marshal(protocol.Response{JSONRPC: "2.0", ID: req.ID, Result: raw})
	conn.Write(append(resp, '\n'))
}

// newTestServer 建面板 fixture：临时 dataDir（含一个版本与文件）+ fake IPC + 面板 server。
func newTestServer(t *testing.T, token string) (ts *httptest.Server, cfg PanelConfig, dataDir string) {
	t.Helper()
	ipc := newFakeIPC(t)
	base := t.TempDir()
	dataDir = filepath.Join(base, "data")

	versionDir := filepath.Join(dataDir, "t1", "versions", "20260909-000001")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := `{"entries":[{"path":"a.txt","size":5}]}`
	if err := os.WriteFile(filepath.Join(versionDir, manifestFile), []byte(mf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("versions", "20260909-000001"), filepath.Join(dataDir, "t1", "current")); err != nil {
		t.Fatal(err)
	}

	cfg = PanelConfig{
		Port: 0, Bind: "127.0.0.1", Token: token,
		Socket: ipc.socket, DataDir: dataDir,
	}
	srv := &panelServer{cfg: cfg, ipc: &IPCClient{SocketPath: ipc.socket}, dl: newLimiter(2)}
	ts = httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts, cfg, dataDir
}

func get(t *testing.T, url, bearer string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

const testToken = "tok-123"

func TestAuthRequired(t *testing.T) {
	ts, _, _ := newTestServer(t, testToken)
	resp := get(t, ts.URL+"/api/overview", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	resp = get(t, ts.URL+"/api/overview", testToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 with bearer, got %d", resp.StatusCode)
	}
}

func TestLoginSetsCookieAndDownloadWorks(t *testing.T) {
	ts, _, _ := newTestServer(t, testToken)

	// 错误 token → 401。
	resp, err := http.Post(ts.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"token":"wrong"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}

	// 正确 token → 200 + Set-Cookie。
	resp, err = http.Post(ts.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"token":"`+testToken+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var cookies []*http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			cookies = append(cookies, c)
		}
	}
	if len(cookies) == 0 {
		t.Fatal("login should set cookie")
	}

	// 仅凭 cookie 的下载（浏览器 <a href> 等效）→ 200 + 内容。
	resp = get(t, ts.URL+"/api/tasks/t1/download?version=current&path=a.txt", "", cookies[0])
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	buf := make([]byte, 16)
	n, _ := resp.Body.Read(buf)
	if string(buf[:n]) != "hello" {
		t.Fatalf("body = %q", string(buf[:n]))
	}

	// 路径穿越 → 400。
	resp = get(t, ts.URL+"/api/tasks/t1/download?version=current&path=../../etc", "", cookies[0])
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("traversal should be 400, got %d", resp.StatusCode)
	}
}

func TestZipArchive(t *testing.T) {
	ts, _, _ := newTestServer(t, testToken)
	resp := get(t, ts.URL+"/api/tasks/t1/archive?version=current&store=1", testToken, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Edge-Sync-Bytes") != "5" {
		t.Fatalf("X-Edge-Sync-Bytes = %q", resp.Header.Get("X-Edge-Sync-Bytes"))
	}
	// zip 魔数 PK。
	magic := make([]byte, 2)
	if _, err := resp.Body.Read(magic); err != nil || string(magic) != "PK" {
		t.Fatalf("not a zip stream: %v %q", err, magic)
	}
}

func TestIpcOfflineIs503(t *testing.T) {
	base := t.TempDir()
	cfg := PanelConfig{Token: testToken, Socket: filepath.Join(base, "missing.sock"), DataDir: base}
	srv := &panelServer{cfg: cfg, ipc: &IPCClient{SocketPath: cfg.Socket}, dl: newLimiter(2)}
	ts := httptest.NewServer(srv.routes())
	defer ts.Close()

	resp := get(t, ts.URL+"/api/overview", testToken, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 offline, got %d", resp.StatusCode)
	}
}

func TestVersionTraversalRejected(t *testing.T) {
	ts, _, _ := newTestServer(t, testToken)
	for _, v := range []string{"../../etc", "%2e%2e%2fx"} {
		resp := get(t, ts.URL+"/api/tasks/t1/download?version="+v+"&path=a.txt", testToken, nil)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("version %q should not pass", v)
		}
	}
}

func TestTaskRoutesForwardIPC(t *testing.T) {
	ipc := newFakeIPC(t)
	base := t.TempDir()
	cfg := PanelConfig{Token: testToken, Socket: ipc.socket, DataDir: base}
	srv := &panelServer{cfg: cfg, ipc: &IPCClient{SocketPath: ipc.socket}, dl: newLimiter(2)}
	ts := httptest.NewServer(srv.routes())
	defer ts.Close()

	for _, path := range []string{
		"/api/overview", "/api/tasks/t1", "/api/tasks/t1/history",
		"/api/tasks/t1/files", "/api/tasks/t1/file-versions?path=a.txt",
		"/api/logs", "/api/plugins",
	} {
		resp := get(t, ts.URL+path, testToken, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s -> %d", path, resp.StatusCode)
		}
	}
	if len(ipc.requests) != 7 {
		t.Fatalf("ipc methods = %v", ipc.requests)
	}
}

var _ = fmt.Sprintf
