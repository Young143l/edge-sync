package engine

import (
	"os"
	"path/filepath"
	"testing"

	"edge-sync/pkg/protocol"
)

// 构造目录树：files[path] = content。
func makeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func manifestOf(files map[string]string) protocol.Manifest {
	m := protocol.Manifest{}
	for p, c := range files {
		m.Entries = append(m.Entries, protocol.FileEntry{
			Path:        p,
			Fingerprint: "fp-" + c,
			Size:        int64(len(c)),
		})
	}
	return m
}

func mustApply(t *testing.T, in ApplyInput) string {
	t.Helper()
	dir, err := Apply(in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return dir
}

func TestApplyFirstSync(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	staging := filepath.Join(dataDir, "staging")
	makeTree(t, staging, map[string]string{"a.txt": "A", "sub/b.txt": "B"})

	files := map[string]string{"a.txt": "A", "sub/b.txt": "B"}
	v1 := mustApply(t, ApplyInput{
		DataDir: dataDir, StagingDir: staging,
		NewManifest: manifestOf(files),
		Changed:     map[string]bool{"a.txt": true, "sub/b.txt": true},
	})

	// current 相对 symlink 指向新版本。
	target := ReadCurrentTarget(dataDir)
	if target != v1 {
		t.Fatalf("current target = %s, want %s", target, v1)
	}
	// staging 已被清空。
	ents, _ := os.ReadDir(staging)
	if len(ents) != 0 {
		t.Fatalf("staging not cleared: %d entries", len(ents))
	}
}

func TestApplySecondRoundHardlink(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	staging := filepath.Join(dataDir, "staging")

	v1Files := map[string]string{"a.txt": "A", "keep.txt": "KEEP"}
	makeTree(t, staging, v1Files)
	v1 := mustApply(t, ApplyInput{
		DataDir: dataDir, StagingDir: staging,
		NewManifest: manifestOf(v1Files),
		Changed:     map[string]bool{"a.txt": true, "keep.txt": true},
	})

	// 第二轮：只改 a.txt，keep.txt 不变（从上一版本硬链接）。
	v2Files := map[string]string{"a.txt": "A2", "keep.txt": "KEEP"}
	makeTree(t, staging, map[string]string{"a.txt": "A2"})
	v2 := mustApply(t, ApplyInput{
		DataDir: dataDir, StagingDir: staging,
		NewManifest:    manifestOf(v2Files),
		Changed:        map[string]bool{"a.txt": true},
		PrevVersionDir: v1,
	})

	fi1, err := os.Stat(filepath.Join(v1, "keep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	fi2, err := os.Stat(filepath.Join(v2, "keep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(fi1, fi2) {
		t.Error("keep.txt should be hardlinked across versions (same inode)")
	}
	// a.txt 在两版本中内容不同，必须不是同一 inode。
	ai1, _ := os.Stat(filepath.Join(v1, "a.txt"))
	ai2, _ := os.Stat(filepath.Join(v2, "a.txt"))
	if os.SameFile(ai1, ai2) {
		t.Error("a.txt changed but shares inode across versions")
	}
	// current 指向 v2。
	if got := ReadCurrentTarget(dataDir); got != v2 {
		t.Fatalf("current = %s, want %s", got, v2)
	}
}

func TestApplyRemovedFileKeptInOldVersion(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	staging := filepath.Join(dataDir, "staging")

	v1Files := map[string]string{"gone.txt": "G", "stay.txt": "S"}
	makeTree(t, staging, v1Files)
	v1 := mustApply(t, ApplyInput{
		DataDir: dataDir, StagingDir: staging,
		NewManifest: manifestOf(v1Files),
		Changed:     map[string]bool{"gone.txt": true, "stay.txt": true},
	})

	// 第二轮：gone.txt 被删除。
	v2Files := map[string]string{"stay.txt": "S"}
	v2 := mustApply(t, ApplyInput{
		DataDir: dataDir, StagingDir: staging,
		NewManifest:    manifestOf(v2Files),
		Changed:        map[string]bool{},
		PrevVersionDir: v1,
	})

	if _, err := os.Stat(filepath.Join(v2, "gone.txt")); !os.IsNotExist(err) {
		t.Error("gone.txt should not exist in v2")
	}
	if _, err := os.Stat(filepath.Join(v1, "gone.txt")); err != nil {
		t.Error("gone.txt must remain in v1 (hardlink snapshot semantics)")
	}
}

func TestApplyFailureRollsBack(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	staging := filepath.Join(dataDir, "staging")

	v1Files := map[string]string{"a.txt": "A"}
	makeTree(t, staging, v1Files)
	v1 := mustApply(t, ApplyInput{
		DataDir: dataDir, StagingDir: staging,
		NewManifest: manifestOf(v1Files),
		Changed:     map[string]bool{"a.txt": true},
	})

	// 第二轮：manifest 声称 b.txt 变更，但 staging 里没有 b.txt → 失败。
	bad := map[string]string{"a.txt": "A", "b.txt": "B"}
	_, err := Apply(ApplyInput{
		DataDir: dataDir, StagingDir: staging,
		NewManifest:    manifestOf(bad),
		Changed:        map[string]bool{"b.txt": true},
		PrevVersionDir: v1,
	})
	if err == nil {
		t.Fatal("expected error for missing staged file")
	}
	// current 未受影响。
	if got := ReadCurrentTarget(dataDir); got != v1 {
		t.Fatalf("current changed on failed apply: %s", got)
	}
	// 失败的版本目录已回滚。
	ents, _ := os.ReadDir(filepath.Join(dataDir, "versions"))
	if len(ents) != 1 {
		t.Fatalf("rollback failed, versions dir has %d entries", len(ents))
	}
}

func TestApplySameSecondVersionSuffix(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	staging := filepath.Join(dataDir, "staging")
	files := map[string]string{"a.txt": "A"}
	for i := 0; i < 2; i++ {
		makeTree(t, staging, files)
		mustApply(t, ApplyInput{
			DataDir: dataDir, StagingDir: staging,
			NewManifest: manifestOf(files),
			Changed:     map[string]bool{"a.txt": true},
		})
	}
	ents, _ := os.ReadDir(filepath.Join(dataDir, "versions"))
	if len(ents) != 2 {
		t.Fatalf("expected 2 versions (same-second suffix), got %d", len(ents))
	}
}

func TestSafeJoin(t *testing.T) {
	cases := []struct {
		rel    string
		ok     bool
		want   string // 相对 base 的落点（slash 形式），ok=true 时有效
	}{
		{"a.txt", true, "a.txt"},
		{"sub/dir/b.txt", true, "sub/dir/b.txt"},
		{"./a.txt", true, "a.txt"},
		{"a//b.txt", true, "a/b.txt"},
		{"", false, ""},
		{"/abs/path", false, ""},
		{"../escape", false, ""},
		{"sub/../../escape", false, ""},
		{".", false, ""},
	}
	for _, c := range cases {
		got, err := SafeJoin("/base", c.rel)
		if c.ok {
			if err != nil {
				t.Errorf("SafeJoin(%q): unexpected error %v", c.rel, err)
				continue
			}
			rel, _ := filepath.Rel("/base", got)
			if filepath.ToSlash(rel) != c.want {
				t.Errorf("SafeJoin(%q) = %q, want under %q", c.rel, got, c.want)
			}
		} else if err == nil {
			t.Errorf("SafeJoin(%q) = %q, want error", c.rel, got)
		}
	}
}

func TestValidateFetched(t *testing.T) {
	base := t.TempDir()
	staging := filepath.Join(base, "staging")
	makeTree(t, staging, map[string]string{"a.txt": "hello"}) // 5 bytes

	e := protocol.FileEntry{Path: "a.txt", Size: 5}
	if err := ValidateFetched(staging, e, 5, ""); err != nil {
		t.Errorf("valid case: %v", err)
	}
	if err := ValidateFetched(staging, e, 4, ""); err == nil {
		t.Error("size mismatch should fail")
	}
	// sha256("hello")
	good := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if err := ValidateFetched(staging, e, 5, good); err != nil {
		t.Errorf("checksum valid case: %v", err)
	}
	if err := ValidateFetched(staging, e, 5, "bad"); err == nil {
		t.Error("bad checksum should fail")
	}
	e2 := protocol.FileEntry{Path: "../esc.txt", Size: 1}
	if err := ValidateFetched(staging, e2, 1, ""); err == nil {
		t.Error("escaping path should fail")
	}
}
