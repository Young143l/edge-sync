package plugin

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"edge-sync/pkg/protocol"
)

func fakePlugin(t *testing.T) string {
	t.Helper()
	src := filepath.Join("testdata", "fake-plugin.sh")
	bin := filepath.Join(t.TempDir(), "fake-plugin")
	// 复制并加执行权限，避免直接执行仓库内文件。
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestClientHappyPath(t *testing.T) {
	c, err := Start(context.Background(), fakePlugin(t), nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer c.Close()

	if ir := c.InitResult(); ir == nil || ir.Name != "fake" {
		t.Fatalf("init result = %+v", ir)
	}

	snap, err := c.Snapshot(context.Background(), map[string]any{"root": "/x"})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.ManifestFingerprint != "m1" || len(snap.Entries) != 1 || snap.Entries[0].Path != "a.txt" {
		t.Fatalf("snapshot = %+v", snap)
	}

	dest := filepath.Join(t.TempDir(), "out", "a.txt")
	res, err := c.FetchFile(context.Background(), map[string]any{}, "a.txt", dest)
	if err != nil {
		t.Fatalf("fetchFile: %v", err)
	}
	if res.Size != 2 {
		t.Fatalf("size = %d", res.Size)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "hi" {
		t.Fatalf("file content = %q", data)
	}
}

func TestClientRPCError(t *testing.T) {
	c, err := Start(context.Background(), fakePlugin(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	err = c.call(context.Background(), "fail", struct{}{}, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want RPCError, got %v", err)
	}
	if rpcErr.Code != protocol.CodeSourceUnreachable {
		t.Fatalf("code = %d", rpcErr.Code)
	}
}

func TestClientTimeout(t *testing.T) {
	c, err := Start(context.Background(), fakePlugin(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = c.call(ctx, "timeout-slow", struct{}{}, nil)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout did not fire promptly")
	}
}

func TestClientSpawnFailure(t *testing.T) {
	if _, err := Start(context.Background(), "/nonexistent/plugin-bin", nil); err == nil {
		t.Fatal("spawn of nonexistent binary should fail")
	}
}

func TestFakePluginRunsOnSh(t *testing.T) {
	// 保证测试环境能执行 sh 脚本（CI / 本机一致性）。
	if err := exec.Command("sh", "-c", "true").Run(); err != nil {
		t.Skipf("sh unavailable: %v", err)
	}
}
