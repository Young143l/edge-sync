// CLI 纯函数测试（不依赖内核）。
package main

import (
	"os"
	"path/filepath"
	"testing"

	"edge-sync/pkg/protocol"
)

func TestParseFlags(t *testing.T) {
	f, pos := parseFlags([]string{"--out", "/tmp/x", "--purge", "task1", "extra"})
	if f.m["out"] != "/tmp/x" {
		t.Errorf("out = %q", f.m["out"])
	}
	if f.m["purge"] != "true" {
		t.Errorf("purge = %q", f.m["purge"])
	}
	if len(pos) != 2 || pos[0] != "task1" || pos[1] != "extra" {
		t.Fatalf("positional = %v", pos)
	}
	// 尾部 bool flag。
	f2, pos2 := parseFlags([]string{"a", "--wait"})
	if f2.m["wait"] != "true" || len(pos2) != 1 {
		t.Fatalf("trailing flag: %v %v", f2.m, pos2)
	}
}

func TestValidTaskName(t *testing.T) {
	valid := []string{"a", "notes-repo", "my.task_1"}
	invalid := []string{"", "../evil", "-lead", "has space", "中文"}
	for _, v := range valid {
		if !validTaskName(v) {
			t.Errorf("%q should be valid", v)
		}
	}
	for _, v := range invalid {
		if validTaskName(v) {
			t.Errorf("%q should be invalid", v)
		}
	}
}

func TestCoerce(t *testing.T) {
	if v := coerce("42", "integer"); v != 42 {
		t.Errorf("integer coerce = %v", v)
	}
	if v := coerce("true", "boolean"); v != true {
		t.Errorf("boolean coerce = %v", v)
	}
	if v := coerce("hello", "string"); v != "hello" {
		t.Errorf("string coerce = %v", v)
	}
	if v := coerce("abc", "integer"); v != "abc" {
		t.Errorf("invalid int should fall back to raw: %v", v)
	}
}

func TestCopyTreeSkipsManifest(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.txt", "sub/b.txt", manifestFileExport} {
		if err := os.WriteFile(filepath.Join(src, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a.txt")); err != nil {
		t.Error("a.txt should be copied")
	}
	if _, err := os.Stat(filepath.Join(dst, "sub", "b.txt")); err != nil {
		t.Error("nested b.txt should be copied")
	}
	if _, err := os.Stat(filepath.Join(dst, manifestFileExport)); !os.IsNotExist(err) {
		t.Error("manifest file should be skipped")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:       "0 B",
		5:       "5 B",
		1024:    "1.0 KB",
		1536:    "1.5 KB",
		1048576: "1.0 MB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestUtf8LenCJK(t *testing.T) {
	if w := utf8Len("abc"); w != 3 {
		t.Errorf("ascii = %d", w)
	}
	if w := utf8Len("中文"); w != 4 {
		t.Errorf("CJK width = %d, want 4", w)
	}
}

func TestStatusText(t *testing.T) {
	tk := &TaskSummaryAlias{Enabled: true, Status: "syncing"}
	if got := statusText(tk); got != "syncing" {
		t.Errorf("syncing = %q", got)
	}
	tk2 := &TaskSummaryAlias{Enabled: true, Status: "idle", ConsecutiveFailures: 3}
	if got := statusText(tk2); got != "idle (3 fails)" {
		t.Errorf("failed = %q", got)
	}
	tk3 := &TaskSummaryAlias{Enabled: false, Status: "idle"}
	if got := statusText(tk3); got != "paused" {
		t.Errorf("paused = %q", got)
	}
}

// TaskSummaryAlias 复用 protocol 类型（与 TS 面板同结构）。
type TaskSummaryAlias = protocol.TaskSummary
