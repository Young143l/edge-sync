package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 建一个假版本目录（带时间名），返回名字。
func fakeVersion(t *testing.T, versionsDir string, ts time.Time, suffix string) string {
	t.Helper()
	name := ts.Format(versionTimeLayout) + suffix
	if err := os.MkdirAll(filepath.Join(versionsDir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestCleanupKeepLastRolling(t *testing.T) {
	base := t.TempDir()
	versions := filepath.Join(base, "versions")
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	names := []string{
		fakeVersion(t, versions, now.Add(-40*time.Minute), ""),
		fakeVersion(t, versions, now.Add(-30*time.Minute), ""),
		fakeVersion(t, versions, now.Add(-20*time.Minute), ""),
		fakeVersion(t, versions, now.Add(-10*time.Minute), ""),
	}
	current := names[3]

	removed, err := Cleanup(versions, current, 2, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	// keepLast=2：保留 current + names[2]；删除最旧两个（顺序不假设）。
	removedSet := map[string]bool{}
	for _, n := range removed {
		removedSet[n] = true
	}
	if len(removed) != 2 || !removedSet[names[0]] || !removedSet[names[1]] {
		t.Fatalf("removed = %v", removed)
	}
	for _, keep := range []string{names[2], current} {
		if !VersionExists(versions, keep) {
			t.Errorf("version %s should be kept", keep)
		}
	}
	if VersionExists(versions, names[0]) || VersionExists(versions, names[1]) {
		t.Error("oldest versions should be removed")
	}
}

func TestCleanupKeepDays(t *testing.T) {
	base := t.TempDir()
	versions := filepath.Join(base, "versions")
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	old := fakeVersion(t, versions, now.Add(-5*24*time.Hour), "")  // 超期
	recent := fakeVersion(t, versions, now.Add(-1*time.Hour), "")  // 未超期
	current := fakeVersion(t, versions, now.Add(-30*time.Minute), "")

	// keepLast=0：不限数量，仅按 keepDays=3 清理。
	removed, err := Cleanup(versions, current, 0, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != old {
		t.Fatalf("removed = %v, want only %s", removed, old)
	}
	if !VersionExists(versions, recent) || !VersionExists(versions, current) {
		t.Error("recent/current should survive keepDays cleanup")
	}
}

func TestCleanupCurrentAlwaysKept(t *testing.T) {
	base := t.TempDir()
	versions := filepath.Join(base, "versions")
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	// 场景 A：超期且非 current → 删；current 虽也超期但受保护。
	current := fakeVersion(t, versions, now.Add(-30*time.Minute), "")
	old := fakeVersion(t, versions, now.Add(-48*time.Hour), "")

	removed, err := Cleanup(versions, current, 10, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != old {
		t.Fatalf("removed = %v, want only %s", removed, old)
	}
	if !VersionExists(versions, current) {
		t.Fatal("current must always be kept even when expired")
	}

	// 场景 B：current 异常老旧且不在最新 keepLast 集合内 →
	// current 保护优先，可能多保留一个版本，但不删 current。
	base2 := t.TempDir()
	versions2 := filepath.Join(base2, "versions")
	oldCurrent := fakeVersion(t, versions2, now.Add(-48*time.Hour), "")
	fresh := fakeVersion(t, versions2, now.Add(-1*time.Hour), "")

	removed2, err := Cleanup(versions2, oldCurrent, 1, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed2) != 0 {
		t.Fatalf("both versions must survive: removed = %v", removed2)
	}
	if !VersionExists(versions2, oldCurrent) || !VersionExists(versions2, fresh) {
		t.Fatal("current-protection must keep both versions here")
	}
}

func TestCleanupIgnoresForeignDirs(t *testing.T) {
	base := t.TempDir()
	versions := filepath.Join(base, "versions")
	now := time.Now()
	current := fakeVersion(t, versions, now, "")
	// 非版本命名规则的目录不应被动。
	foreign := filepath.Join(versions, "not-a-version")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Cleanup(versions, current, 1, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("foreign dir should not be touched")
	}
}
