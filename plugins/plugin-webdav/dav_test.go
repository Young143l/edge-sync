package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/webdav"

	"edge-sync/pkg/pluginkit"
	"edge-sync/pkg/protocol"
)

// newDavFixture 起一个基于目录的本地 WebDAV 服务器，返回 (插件 options map, 根目录)。
func newDavFixture(t *testing.T, middleware func(http.Handler) http.Handler) (map[string]any, string) {
	t.Helper()
	root := t.TempDir()
	handler := &webdav.Handler{
		FileSystem: webdav.Dir(root),
		LockSystem: webdav.NewMemLS(),
	}
	var h http.Handler = handler
	if middleware != nil {
		h = middleware(handler)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	opts := map[string]any{"url": srv.URL + "/dav/"}
	// webdav.Handler 挂在 /dav/ 下，用 http.NewServeMux 前缀承载。
	mux := http.NewServeMux()
	mux.Handle("/dav/", http.StripPrefix("/dav/", h))
	srv2 := httptest.NewServer(mux)
	t.Cleanup(srv2.Close)
	opts["url"] = srv2.URL + "/dav/"
	return opts, root
}

func davWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func snapshotViaPlugin(t *testing.T, cfg map[string]any) *protocol.Manifest {
	t.Helper()
	p := webdavPlugin{}
	m, err := p.Snapshot(cfg)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return m
}

func entryOf(m *protocol.Manifest, path string) *protocol.FileEntry {
	for i := range m.Entries {
		if m.Entries[i].Path == path {
			return &m.Entries[i]
		}
	}
	return nil
}

func TestWebdavSnapshotAndFetch(t *testing.T) {
	cfg, root := newDavFixture(t, nil)
	davWrite(t, root, "a.txt", "hello")
	davWrite(t, root, "sub/deep/b.txt", "bee")
	davWrite(t, root, "empty.txt", "")

	m := snapshotViaPlugin(t, cfg)
	if len(m.Entries) != 3 {
		t.Fatalf("want 3 files, got %d: %+v", len(m.Entries), m.Entries)
	}
	if entryOf(m, "a.txt") == nil || entryOf(m, "sub/deep/b.txt") == nil || entryOf(m, "empty.txt") == nil {
		t.Fatalf("missing entries: %+v", m.Entries)
	}
	if m.ManifestFingerprint == "" {
		t.Fatal("manifestFingerprint empty")
	}

	// fetchFile 内容与 checksum 一致。
	p := webdavPlugin{}
	dest := filepath.Join(t.TempDir(), "out", "a.txt")
	res, err := p.FetchFile(cfg, "a.txt", dest)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "hello" {
		t.Fatalf("content = %q", data)
	}
	sum := sha256.Sum256([]byte("hello"))
	if res.Checksum != hex.EncodeToString(sum[:]) {
		t.Fatalf("checksum = %s", res.Checksum)
	}
}

func TestWebdavFingerprintChangesOnModify(t *testing.T) {
	cfg, root := newDavFixture(t, nil)
	davWrite(t, root, "a.txt", "v1")
	davWrite(t, root, "b.txt", "stable")

	m1 := snapshotViaPlugin(t, cfg)
	fpA1 := entryOf(m1, "a.txt").Fingerprint
	fpB1 := entryOf(m1, "b.txt").Fingerprint

	// x/net/webdav 的 etag 基于秒级 modtime+size，用不同长度内容保证变化。
	davWrite(t, root, "a.txt", "v2-with-longer-content")
	m2 := snapshotViaPlugin(t, cfg)

	if entryOf(m2, "a.txt").Fingerprint == fpA1 {
		t.Error("a.txt fingerprint should change after modify")
	}
	if entryOf(m2, "b.txt").Fingerprint != fpB1 {
		t.Error("b.txt fingerprint should stay")
	}
}

func TestWebdavDepthFallback(t *testing.T) {
	// 拦截 Depth: infinity 请求返回 403，验证插件降级逐层遍历仍能列全。
	var calls []string
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PROPFIND" {
				calls = append(calls, r.Header.Get("Depth"))
				if r.Header.Get("Depth") == "infinity" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
	cfg, root := newDavFixture(t, mw)
	davWrite(t, root, "a.txt", "1")
	davWrite(t, root, "sub/b.txt", "2")

	m := snapshotViaPlugin(t, cfg)
	if len(m.Entries) != 2 {
		t.Fatalf("fallback traversal incomplete: %+v", m.Entries)
	}
	if entryOf(m, "sub/b.txt") == nil {
		t.Fatalf("nested file missing: %+v", m.Entries)
	}
	if len(calls) == 0 || calls[0] != "infinity" {
		t.Fatalf("expected first call to be infinity, calls=%v", calls)
	}
}

func TestWebdavAuthRequired(t *testing.T) {
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "u" || pass != "p" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	cfg, root := newDavFixture(t, mw)
	davWrite(t, root, "a.txt", "secret")

	// 无凭证 → AuthFailed。
	if _, err := snapshotViaPluginErr(t, cfg); err == nil {
		t.Fatal("expected auth failure without credentials")
	} else {
		var pe *pluginkit.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeAuthFailed {
			t.Fatalf("want AuthFailed, got %v", err)
		}
	}

	// 带凭证 → 成功。
	cfg["username"] = "u"
	cfg["password"] = "p"
	m := snapshotViaPlugin(t, cfg)
	if len(m.Entries) != 1 {
		t.Fatalf("authenticated snapshot failed: %+v", m.Entries)
	}
}

func TestWebdavFingerprintPolicyMtimeSize(t *testing.T) {
	cfg, root := newDavFixture(t, nil)
	davWrite(t, root, "a.txt", "x")
	cfg["fingerprint"] = FingerprintMtimeSize

	m := snapshotViaPlugin(t, cfg)
	fp := entryOf(m, "a.txt").Fingerprint
	// 格式 "<nanos>:<size>"
	if !strings.Contains(fp, ":") {
		t.Fatalf("mtime_size fingerprint format unexpected: %q", fp)
	}
	if strings.HasPrefix(fp, `"`) {
		t.Fatalf("etag leaked into mtime_size policy: %q", fp)
	}
}

func TestWebdavOptionsValidation(t *testing.T) {
	p := webdavPlugin{}
	if _, err := p.Snapshot(map[string]any{}); err == nil {
		t.Fatal("missing url should fail")
	}
	bad := map[string]any{"url": "ftp://x/"}
	if _, err := p.Snapshot(bad); err == nil {
		t.Fatal("non-http url should fail")
	}
	badFp := map[string]any{"url": "http://x/", "fingerprint": "bogus"}
	if _, err := p.Snapshot(badFp); err == nil {
		t.Fatal("bad fingerprint policy should fail")
	}
	// passwordEnv 空变量报错。
	t.Setenv("ES_EMPTY", "")
	emptyEnv := map[string]any{"url": "http://x/", "passwordEnv": "ES_EMPTY"}
	if _, err := p.Snapshot(emptyEnv); err == nil {
		t.Fatal("empty passwordEnv should fail")
	}
}

func snapshotViaPluginErr(t *testing.T, cfg map[string]any) (*protocol.Manifest, error) {
	t.Helper()
	return webdavPlugin{}.Snapshot(cfg)
}

func TestWebdavRangeResume(t *testing.T) {
	cfg, root := newDavFixture(t, nil)
	content := strings.Repeat("0123456789", 1000) // 10KB
	davWrite(t, root, "big.txt", content)

	// 预置半截 .part（前 4KB），模拟上次中断。
	dest := filepath.Join(t.TempDir(), "big.txt")
	if err := os.WriteFile(dest+".part", []byte(content[:4000]), 0o644); err != nil {
		t.Fatal(err)
	}

	p := webdavPlugin{}
	res, err := p.FetchFile(cfg, "big.txt", dest)
	if err != nil {
		t.Fatalf("fetch with resume: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != content {
		t.Fatalf("resumed content mismatch: len=%d", len(data))
	}
	sum := sha256.Sum256([]byte(content))
	if res.Checksum != hex.EncodeToString(sum[:]) {
		t.Fatal("checksum mismatch after resume")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part should be renamed away")
	}
	_ = fmt.Sprint // 保持 fmt 引用（测试断言消息使用）
}
