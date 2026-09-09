// edge-syncd 是 edge-sync 的常驻内核：任务调度、快照 diff、原子应用、
// 版本保留与 Unix socket IPC 服务（CLI / 面板接入点）。
//
// 生命周期：加载配置 → 启动 runner + IPC → SIGHUP / IPC 热重载（任务集）
// → SIGTERM/SIGINT 优雅退出。storage 路径变更需重启（热重载仅刷新任务集）。
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"edge-sync/internal/config"
	"edge-sync/internal/ipc"
	"edge-sync/internal/logring"
	"edge-sync/internal/runner"
	"edge-sync/internal/state"
)

const version = "0.1.0"

func main() {
	configPath := flag.String("config", "etc/config.yaml", "path to config.yaml")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	// 日志：stderr + 环形缓冲（IPC log.tail 查询）。
	ring := logring.NewRing(1000)
	logger := slog.New(logring.NewHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}), ring))

	// 配置快照（reload 后原子替换）。
	var cfgMu sync.Mutex
	current := cfg
	getCfg := func() *config.Config {
		cfgMu.Lock()
		defer cfgMu.Unlock()
		return current
	}
	setCfg := func(newCfg *config.Config) {
		cfgMu.Lock()
		current = newCfg
		cfgMu.Unlock()
	}

	store := state.NewStore(current.Storage.StateDir)
	r := runner.New(current, store, logger)
	r.Start(context.Background())

	// applyCfg：SIGHUP 与 IPC config.reload 共用的配置应用路径。
	applyCfg := func(newCfg *config.Config) error {
		if newCfg.Storage.DataDir != current.Storage.DataDir ||
			newCfg.Storage.StateDir != current.Storage.StateDir {
			logger.Warn("storage path change requires restart; keeping old paths",
				"old_data", current.Storage.DataDir, "new_data", newCfg.Storage.DataDir)
			newCfg.Storage = current.Storage
		}
		setCfg(newCfg)
		r.Reload(newCfg)
		return nil
	}

	ipcSrv, err := ipc.Listen(current.Server.Socket, ipc.Deps{
		CfgPath: *configPath,
		GetCfg:  getCfg,
		SetCfg:  applyCfg,
		Runner:  r,
		Store:   store,
		Ring:    ring,
		Log:     logger,
	})
	if err != nil {
		logger.Error("start ipc", "err", err)
		os.Exit(1)
	}
	go ipcSrv.Serve()
	logger.Info("edge-syncd started", "version", version, "config", *configPath,
		"socket", current.Server.Socket, "tasks", len(current.Tasks))

	// SIGHUP：重载配置（与 IPC reload 同一应用路径）。
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	go func() {
		for range sighup {
			newCfg, err := config.Load(*configPath)
			if err != nil {
				logger.Error("reload config failed, keeping old config", "err", err)
				continue
			}
			if err := applyCfg(newCfg); err != nil {
				logger.Error("apply config failed", "err", err)
				continue
			}
			logger.Info("config reloaded", "tasks", len(newCfg.Tasks))
		}
	}()

	// 优雅退出。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	logger.Info("shutting down")
	ipcSrv.Close()
	r.Stop()
	logger.Info("bye")
}
