package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"edge-sync/internal/config"
	"edge-sync/internal/state"
)

// buildPluginLocal 编译 plugins/plugin-local 到临时目录（module 根在 ../..）。
func buildPluginLocal(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "plugin-local")
	cmd := exec.Command("go", "build", "-o", bin, "edge-sync/plugins/plugin-local")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build plugin-local: %v\n%s", err, out)
	}
	return bin
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("module root not found from %s: %v", dir, err)
	}
	return dir
}

// testEnv 搭一套完整端到端环境：mock 源目录 + 配置 + runner。
type testEnv struct {
	root     string // mock 源目录
	binDir   string
	dataDir  string
	stateDir string
	cfgPath  string
	runner   *Runner
	log      *bytesBufferLogger
}

type bytesBufferLogger struct{ lines []string }

func (b *bytesBufferLogger) Info(msg string, args ...any)  {}
func (b *bytesBufferLogger) Warn(msg string, args ...any)  {}
func (b *bytesBufferLogger) Error(msg string, args ...any) {}

func newTestEnv(t *testing.T, keepLast int) *testEnv {
	t.Helper()
	bin := buildPluginLocal(t)
	base := t.TempDir()
	e := &testEnv{
		root:     filepath.Join(base, "mock-src"),
		binDir:   filepath.Dir(bin),
		dataDir:  filepath.Join(base, "data"),
		stateDir: filepath.Join(base, "state"),
		cfgPath:  filepath.Join(base, "config.yaml"),
	}
	if err := os.MkdirAll(e.root, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Storage: config.StorageConfig{
			DataDir: e.dataDir, StateDir: e.stateDir, PluginBinDir: e.binDir,
		},
		Tasks: []*config.Task{{
			Name:     "t1",
			Plugin:   "local",
			Interval: config.Duration{Duration: time.Minute},
			Options:  map[string]any{"root": e.root},
			Retention: config.Retention{
				KeepLast: keepLast,
			},
		}},
	}
	e.log = &bytesBufferLogger{}
	e.runner = New(cfg, state.NewStore(e.stateDir), slog.New(discardHandler{}))
	return e
}

type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (discardHandler) WithAttrs([]slog.Attr) slog.Handler        { return discardHandler{} }
func (discardHandler) WithGroup(string) slog.Handler             { return discardHandler{} }

func (e *testEnv) writeSrc(t *testing.T, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(e.root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *testEnv) task() *config.Task { return e.runner.cfg.Tasks[0] }

func (e *testEnv) versionsDir() string {
	return filepath.Join(e.dataDir, "t1", "versions")
}

func (e *testEnv) stateOf(t *testing.T) *state.TaskState {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.stateDir, "t1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st state.TaskState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	return &st
}

func TestEndToEndTwoRounds(t *testing.T) {
	e := newTestEnv(t, 10)

	// 第一轮：两个文件全量。
	e.writeSrc(t, map[string]string{"a.txt": "A", "sub/b.txt": "B"})
	if err := e.runner.Sync(context.Background(), e.task()); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	versions1, _ := os.ReadDir(e.versionsDir())
	if len(versions1) != 1 {
		t.Fatalf("after round 1, want 1 version, got %d", len(versions1))
	}
	st := e.stateOf(t)
	if st.Stats.TotalSyncs != 1 || st.Stats.TotalFiles != 2 {
		t.Fatalf("state after round 1: %+v", st.Stats)
	}

	// 第二轮：改一个文件，加一个文件。
	e.writeSrc(t, map[string]string{"a.txt": "A2", "new.txt": "N"})
	if err := e.runner.Sync(context.Background(), e.task()); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	versions2, _ := os.ReadDir(e.versionsDir())
	if len(versions2) != 2 {
		t.Fatalf("after round 2, want 2 versions, got %d", len(versions2))
	}

	// current 指向最新版本。
	current, err := os.Readlink(filepath.Join(e.dataDir, "t1", "current"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(current) != versions2[1].Name() && filepath.Base(current) != versions2[0].Name() {
		// versions2 按目录序，current 应等于其中较新者（最后一个创建的）
		t.Fatalf("current -> %s, versions = %s/%s", current, versions2[0].Name(), versions2[1].Name())
	}

	// 硬链接：sub/b.txt 两轮未变，跨版本同 inode。
	v1 := filepath.Join(e.versionsDir(), versions1[0].Name())
	var v2 string
	for _, d := range versions2 {
		if d.Name() != versions1[0].Name() {
			v2 = filepath.Join(e.versionsDir(), d.Name())
		}
	}
	fi1, _ := os.Stat(filepath.Join(v1, "sub", "b.txt"))
	fi2, _ := os.Stat(filepath.Join(v2, "sub", "b.txt"))
	if !os.SameFile(fi1, fi2) {
		t.Error("unchanged file should be hardlinked across versions")
	}
	// a.txt 变更后内容正确。
	af, _ := os.ReadFile(filepath.Join(v2, "a.txt"))
	if string(af) != "A2" {
		t.Errorf("a.txt in v2 = %q", af)
	}

	st = e.stateOf(t)
	if st.Stats.TotalSyncs != 2 || st.Stats.TotalFiles != 4 || st.ConsecutiveFailures != 0 {
		t.Fatalf("state after round 2: %+v", st.Stats)
	}
}

func TestEndToEndNoChangeShortCircuit(t *testing.T) {
	e := newTestEnv(t, 10)
	e.writeSrc(t, map[string]string{"a.txt": "A"})

	if err := e.runner.Sync(context.Background(), e.task()); err != nil {
		t.Fatal(err)
	}
	// 源无变化：第二轮应零下载（无新版本目录）。
	if err := e.runner.Sync(context.Background(), e.task()); err != nil {
		t.Fatal(err)
	}
	versions, _ := os.ReadDir(e.versionsDir())
	if len(versions) != 1 {
		t.Fatalf("no-change round must not create version, got %d", len(versions))
	}
	st := e.stateOf(t)
	if st.Stats.TotalSyncs != 1 {
		t.Fatalf("totalSyncs = %d, want 1 (no-change round not counted)", st.Stats.TotalSyncs)
	}
}

func TestEndToEndRetentionKeepLast(t *testing.T) {
	e := newTestEnv(t, 2) // keepLast=2

	// 三轮变更，每轮改同文件内容。
	contents := []string{"1", "2", "3"}
	for i, c := range contents {
		e.writeSrc(t, map[string]string{"a.txt": c})
		if err := e.runner.Sync(context.Background(), e.task()); err != nil {
			t.Fatalf("sync %d: %v", i+1, err)
		}
		time.Sleep(1100 * time.Millisecond) // 版本名秒级精度，确保目录名唯一
	}
	versions, _ := os.ReadDir(e.versionsDir())
	if len(versions) != 2 {
		t.Fatalf("keepLast=2 should leave 2 versions, got %d", len(versions))
	}
	// current 内容是最新。
	currentTarget, _ := os.Readlink(filepath.Join(e.dataDir, "t1", "current"))
	latest, _ := os.ReadFile(filepath.Join(e.dataDir, "t1", "current", "a.txt"))
	if string(latest) != "3" {
		t.Errorf("current a.txt = %q, want 3", latest)
	}
	if filepath.Base(currentTarget) != versions[1].Name() {
		t.Errorf("current -> %s, newest version = %s", currentTarget, versions[1].Name())
	}
}

func TestEndToEndFailureIncrementsState(t *testing.T) {
	e := newTestEnv(t, 10)
	// 指向不存在的 root：snapshot 会失败（SourceUnreachable）。
	e.runner.cfg.Tasks[0].Options = map[string]any{"root": filepath.Join(e.root, "missing")}
	if err := os.MkdirAll(filepath.Join(e.root), 0o755); err != nil {
		t.Fatal(err)
	}
	// plugin-local 在 root 不存在时返回错误 → runSync 失败。
	err := e.runner.Sync(context.Background(), e.task())
	if err == nil {
		t.Fatal("expected sync failure for missing root")
	}
	st := e.stateOf(t)
	if st.ConsecutiveFailures != 1 {
		t.Fatalf("consecutiveFailures = %d, want 1", st.ConsecutiveFailures)
	}
}
