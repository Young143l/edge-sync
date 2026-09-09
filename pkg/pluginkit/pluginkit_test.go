// pluginkit 纯函数与文件操作测试。
package pluginkit

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"edge-sync/pkg/protocol"
)

func TestSafeJoin(t *testing.T) {
	cases := []struct {
		rel string
		ok  bool
	}{
		{"a.txt", true},
		{"sub/dir/b.txt", true},
		{"./a.txt", true},
		{"", false},
		{"/abs", false},
		{"../esc", false},
		{"sub/../../esc", false},
		{".", false},
	}
	for _, c := range cases {
		_, err := SafeJoin("/base", c.rel)
		if c.ok && err != nil {
			t.Errorf("SafeJoin(%q): %v", c.rel, err)
		}
		if !c.ok && err == nil {
			t.Errorf("SafeJoin(%q) should fail", c.rel)
		}
	}
}

func TestCopyWithHash(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src.txt")
	content := strings.Repeat("edge-sync", 10)
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(base, "out", "deep", "src.txt")
	size, sum, err := CopyWithHash(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(content)) {
		t.Fatalf("size = %d", size)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != content {
		t.Fatalf("content mismatch: %q", got)
	}
	sumVerify := sha256.Sum256([]byte(content))
	if sum != hex.EncodeToString(sumVerify[:]) {
		t.Fatal("checksum mismatch")
	}
	// .part 残留检查
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part should be renamed away")
	}
}

func TestWriteContentWithHash(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "link")
	size, sum, err := WriteContentWithHash([]byte("../target"), dest)
	if err != nil {
		t.Fatal(err)
	}
	if size != 9 {
		t.Fatalf("size = %d", size)
	}
	_ = sum
	got, _ := os.ReadFile(dest)
	if string(got) != "../target" {
		t.Fatalf("content = %q", got)
	}
}

func TestCombineFingerprintStable(t *testing.T) {
	m := protocol.Manifest{Entries: []protocol.FileEntry{
		{Path: "b", Fingerprint: "2"},
		{Path: "a", Fingerprint: "1"},
	}}
	f1 := CombineFingerprint(m.Entries)
	// 顺序无关。
	m2 := protocol.Manifest{Entries: []protocol.FileEntry{
		{Path: "a", Fingerprint: "1"},
		{Path: "b", Fingerprint: "2"},
	}}
	f2 := CombineFingerprint(m2.Entries)
	if f1 == "" || f1 != f2 {
		t.Fatalf("fingerprint should be order-independent and non-empty: %q vs %q", f1, f2)
	}
	if CombineFingerprint(nil) != "" {
		t.Fatal("empty entries should yield empty fingerprint")
	}
}

func TestCombineFingerprintDetectsChange(t *testing.T) {
	e1 := []protocol.FileEntry{{Path: "a", Fingerprint: "h1"}}
	if CombineFingerprint(e1) == CombineFingerprint([]protocol.FileEntry{{Path: "a", Fingerprint: "h2"}}) {
		t.Fatal("changed fingerprint should change combined value")
	}
}
