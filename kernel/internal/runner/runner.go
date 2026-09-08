// Package runner 编排同步流程：每任务独立调度循环（jitter 错峰、失败指数退避），
// 并实现单次同步的完整管线：
// staging 清理 → 插件 snapshot → fingerprint 短路 → diff → 逐文件 fetch（串行，含校验）
// → 原子应用 → 保留清理 → 状态落盘。
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"edge-sync/kernel/internal/config"
	"edge-sync/kernel/internal/engine"
	"edge-sync/kernel/internal/plugin"
	"edge-sync/kernel/internal/state"
	"edge-sync/pkg/protocol"
)

// MaxBackoff 连续失败后的最大同步间隔。
const MaxBackoff = time.Hour

// ErrInProgress 任务同步已在进行中（手动触发时幂等返回）。
var ErrInProgress = errors.New("sync already in progress")

// Runner 管理所有任务的调度与执行。
type Runner struct {
	cfg   *config.Config
	store *state.Store
	log   *slog.Logger

	rootCtx  context.Context
	cancel   context.CancelFunc
	taskCtxs map[string]context.CancelFunc
	running  map[string]*atomic.Bool
	wg       sync.WaitGroup
	mu       sync.Mutex
}

func New(cfg *config.Config, store *state.Store, log *slog.Logger) *Runner {
	return &Runner{
		cfg:      cfg,
		store:    store,
		log:      log,
		taskCtxs: map[string]context.CancelFunc{},
		running:  map[string]*atomic.Bool{},
	}
}

// Start 启动所有已启用任务的调度循环。
func (r *Runner) Start(parent context.Context) {
	r.rootCtx, r.cancel = context.WithCancel(parent)
	r.startAll()
	r.log.Info("runner started", "tasks", len(r.cfg.Tasks))
}

func (r *Runner) startAll() {
	for _, t := range r.cfg.Tasks {
		if t.IsEnabled() {
			r.startTask(t)
		}
	}
}

func (r *Runner) startTask(t *config.Task) {
	ctx, cancel := context.WithCancel(r.rootCtx)
	running := &atomic.Bool{}
	r.mu.Lock()
	r.taskCtxs[t.Name] = cancel
	r.running[t.Name] = running
	r.mu.Unlock()
	r.wg.Add(1)
	go r.loop(ctx, t)
}

// Stop 停止所有任务并等待进行中的同步收敛。
func (r *Runner) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

// Reload 停掉全部任务循环后按新配置重启（阶段 1 简单实现：全停全起）。
// 进行中的同步会因 ctx 取消而尽快收敛。
func (r *Runner) Reload(cfg *config.Config) {
	r.log.Info("reloading config", "tasks", len(cfg.Tasks))
	r.stopTasks()
	r.cfg = cfg
	r.startAll()
}

func (r *Runner) stopTasks() {
	r.mu.Lock()
	cancels := r.taskCtxs
	r.taskCtxs = map[string]context.CancelFunc{}
	r.running = map[string]*atomic.Bool{}
	r.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	r.wg.Wait()
}

// TaskNames 返回当前配置的任务名（诊断用）。
func (r *Runner) TaskNames() []string {
	var names []string
	for _, t := range r.cfg.Tasks {
		names = append(names, t.Name)
	}
	return names
}

func (r *Runner) loop(ctx context.Context, t *config.Task) {
	defer r.wg.Done()

	// 启动 jitter：0 ~ interval*10%，多任务错峰。
	jitter := time.Duration(rand.Int63n(int64(t.Interval.Duration / 10)))
	timer := time.NewTimer(jitter)
	failures := 0

	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if err := r.Sync(ctx, t); err != nil {
			if errors.Is(err, ErrInProgress) || ctx.Err() != nil {
				timer.Stop()
				return
			}
			failures++
			next := t.Interval.Duration << failures // interval * 2^failures
			if next > MaxBackoff || next <= 0 {
				next = MaxBackoff
			}
			r.log.Warn("sync failed", "task", t.Name, "err", err,
				"consecutive_failures", failures, "next_backoff", next.String())
			timer.Reset(next)
			continue
		}
		failures = 0
		timer.Reset(t.Interval.Duration)
	}
}

// Sync 同步执行一次任务；任务已在同步中时返回 ErrInProgress。
// 允许在未 Start 的 Runner 上直接调用（测试 / 手动场景）：
// running 标记按需惰性创建。
func (r *Runner) Sync(ctx context.Context, t *config.Task) error {
	r.mu.Lock()
	running := r.running[t.Name]
	if running == nil {
		running = &atomic.Bool{}
		r.running[t.Name] = running
	}
	r.mu.Unlock()
	if !running.CompareAndSwap(false, true) {
		return ErrInProgress
	}
	defer running.Store(false)

	err := r.runSync(ctx, t)
	if err != nil {
		// 失败路径：刷新状态中的失败计数（成功路径已在 runSync 内落盘）。
		st := r.store.Load(t.Name)
		st.ConsecutiveFailures++
		st.LastSyncAt = time.Now().UTC().Format(time.RFC3339)
		if serr := r.store.Save(t.Name, st); serr != nil {
			r.log.Error("save state after failure", "task", t.Name, "err", serr)
		}
	}
	return err
}

// dataDir / stagingDir / versionsDir 路径助手。
func dataDirOf(cfg *config.Config, task string) string {
	return filepath.Join(cfg.Storage.DataDir, task)
}

func (r *Runner) binPath(t *config.Task) string {
	return filepath.Join(r.cfg.Storage.PluginBinDir, "plugin-"+t.Plugin)
}

// runSync 单次同步管线。
func (r *Runner) runSync(ctx context.Context, t *config.Task) error {
	start := time.Now()
	dataDir := dataDirOf(r.cfg, t.Name)
	stagingDir := filepath.Join(dataDir, "staging")
	versionsDir := filepath.Join(dataDir, "versions")

	// 1. staging 清理重建（清掉上次中断残留）。
	if err := os.RemoveAll(stagingDir); err != nil {
		return fmt.Errorf("clear staging: %w", err)
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return err
	}

	// 2. 插件会话 + snapshot。
	st := r.store.Load(t.Name)
	cli, err := plugin.Start(ctx, r.binPath(t), func(line string) {
		r.log.Info("plugin", "task", t.Name, "stderr", line)
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	sctx, scancel := timeoutCtx(ctx, t.SnapshotTimeout.Duration)
	defer scancel()
	snap, err := cli.Snapshot(sctx, t.Options)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}

	// 3. 源级指纹短路：远端无变化，零下载。
	if snap.ManifestFingerprint != "" && snap.ManifestFingerprint == st.ManifestFingerprint {
		r.recordNoChange(t, st, snap)
		return nil
	}

	// 4. diff。
	cs := engine.Diff(st.LastManifest, *snap)
	if !cs.HasChanges {
		r.recordNoChange(t, st, snap)
		return nil
	}

	// 5. 逐文件 fetch（串行）到 staging，含校验。
	changed := make(map[string]bool, len(cs.Added)+len(cs.Modified))
	var totalBytes int64
	fetchAll := make([]protocol.FileEntry, 0, len(cs.Added)+len(cs.Modified))
	fetchAll = append(fetchAll, cs.Added...)
	fetchAll = append(fetchAll, cs.Modified...)

	fctx, fcancel := timeoutCtx(ctx, t.FetchTimeout.Duration)
	defer fcancel()
	for _, e := range fetchAll {
		dest, err := engine.SafeJoin(stagingDir, e.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		res, err := cli.FetchFile(fctx, t.Options, e.Path, dest)
		if err != nil {
			return fmt.Errorf("fetch %q: %w", e.Path, err)
		}
		if err := engine.ValidateFetched(stagingDir, e, res.Size, res.Checksum); err != nil {
			return err
		}
		changed[e.Path] = true
		totalBytes += res.Size
	}

	// 6. 原子应用。
	versionDir, err := engine.Apply(engine.ApplyInput{
		DataDir:        dataDir,
		StagingDir:     stagingDir,
		NewManifest:    *snap,
		Changed:        changed,
		PrevVersionDir: engine.ReadCurrentTarget(dataDir),
	})
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}

	// 7. 保留清理。
	removed, err := engine.Cleanup(versionsDir, filepath.Base(versionDir),
		t.Retention.KeepLast, t.Retention.KeepDays, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("retention: %w", err)
	}

	// 8. 成功收尾：状态落盘。
	now := time.Now().UTC().Format(time.RFC3339)
	st.LastManifest = *snap
	st.ManifestFingerprint = snap.ManifestFingerprint
	st.LastSyncAt = now
	st.LastSuccessAt = now
	st.ConsecutiveFailures = 0
	st.Stats.TotalSyncs++
	st.Stats.TotalFiles += len(fetchAll)
	st.Stats.TotalBytes += totalBytes
	if err := r.store.Save(t.Name, st); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	r.log.Info("synced", "task", t.Name,
		"added", len(cs.Added), "modified", len(cs.Modified), "removed", len(cs.Removed),
		"version", filepath.Base(versionDir), "bytes", totalBytes,
		"pruned", len(removed), "duration", time.Since(start).Round(time.Millisecond).String())
	return nil
}

// recordNoChange 收到未变更结果时只刷新指纹与时间（fingerprints 可能为空）。
// timeoutCtx 时长非正时退化为不设超时（防御未初始化配置）。
func timeoutCtx(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

func (r *Runner) recordNoChange(t *config.Task, st *state.TaskState, snap *protocol.Manifest) {
	st.ManifestFingerprint = snap.ManifestFingerprint
	st.LastSyncAt = time.Now().UTC().Format(time.RFC3339)
	if err := r.store.Save(t.Name, st); err != nil {
		r.log.Error("save state", "task", t.Name, "err", err)
	}
	r.log.Info("no change", "task", t.Name)
}
