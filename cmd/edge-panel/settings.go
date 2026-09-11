// 设置页后端：系统概览/存储统计、缓存清理、面板与内核重启。
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"syscall"
	"time"
)

// syscallStat 命名别名：硬链接去重需要 dev+ino（Sys() 返回的 Stat_t）。
type syscallStat = syscall.Stat_t

// taskStorage 单任务的存储统计。
type taskStorage struct {
	Name       string `json:"name"`
	Versions   int    `json:"versions"`
	DataBytes  int64  `json:"dataBytes"`
	CacheBytes int64  `json:"cacheBytes"`
}

// settingsInfo 设置页信息响应。
type settingsInfo struct {
	PanelVersion   string        `json:"panelVersion"`
	GoVersion      string        `json:"goVersion"`
	SyncdStartedAt string        `json:"syncdStartedAt,omitempty"` // 由 socket 文件 mtime 近似
	TotalDataBytes int64         `json:"totalDataBytes"`
	Tasks          []taskStorage `json:"tasks"`
}

// dirSize 遍历目录统计：within=去重 key（硬链接共享 inode 只计一次；nil 则不去重）。
// 排除符号链接本体（0 字节，current 是 symlink）。
func dirSize(root string, inodeSeen map[string]struct{}) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 无权限/已删除的条目跳过，不中断统计
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil // 符号链接不占目标数据空间
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if inodeSeen != nil {
			key := fmtKey(info)
			if _, dup := inodeSeen[key]; dup {
				return nil
			}
			inodeSeen[key] = struct{}{}
		}
		total += info.Size()
		return nil
	})
	return total
}

func fmtKey(info os.FileInfo) string {
	st, ok := info.Sys().(*syscallStat)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}

// storageOf 统计单个任务的 versions 目录（硬链接去重）与插件缓存。
func (s *panelServer) storageOf(name string) taskStorage {
	t := taskStorage{Name: name}
	seen := map[string]struct{}{}
	versionsDir := filepath.Join(s.cfg.DataDir, name, "versions")
	_ = filepath.WalkDir(versionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		key := fmtKey(info)
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			t.DataBytes += info.Size()
		}
		return nil
	})
	if entries, err := os.ReadDir(versionsDir); err == nil {
		t.Versions = len(entries)
	}
	t.CacheBytes = dirSize(filepath.Join(filepath.Dir(s.cfg.Socket), "cache", name), nil)
	return t
}

// settingsNames 列出 dataDir 下的任务目录（= 已有数据块的任务）。
func (s *panelServer) settingsNames() []string {
	entries, err := os.ReadDir(s.cfg.DataDir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && e.Name() != "." && e.Name() != ".." {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func (s *panelServer) handleSettingsInfo(w http.ResponseWriter, r *http.Request) {
	info := settingsInfo{
		PanelVersion: version,
		GoVersion:    runtime.Version(),
		Tasks:        []taskStorage{},
	}
	// 内核启动时间 ≈ socket 文件 mtime（syncd 启动时创建）。
	if fi, err := os.Stat(s.cfg.Socket); err == nil {
		info.SyncdStartedAt = fi.ModTime().UTC().Format(time.RFC3339)
	}
	names := s.settingsNames()
	for _, n := range names {
		st := s.storageOf(n)
		info.TotalDataBytes += st.DataBytes
		info.Tasks = append(info.Tasks, st)
	}
	writeJSON(w, http.StatusOK, info)
}

// handleCacheClear 清空某任务的插件缓存（git depth-1 仓库），下次轮询自动重建。
func (s *panelServer) handleCacheClear(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Task string `json:"task"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Task == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "需要 task 字段"})
		return
	}
	dir := filepath.Join(filepath.Dir(s.cfg.Socket), "cache")
	target := filepath.Join(dir, filepath.Clean("/"+req.Task)) // 防穿越：Clean 后必须仍位于 dir 内
	if filepath.Dir(target) != dir || req.Task == "." || req.Task == "/" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "非法任务名"})
		return
	}
	if err := os.RemoveAll(target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "清理失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePanelRestart 面板进程退出，systemd Restart=always 拉起（软重启）。
func (s *panelServer) handlePanelRestart(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "面板将在 1 秒后重启"})
	slog.Info("restart requested via API")
	go func() {
		time.Sleep(time.Second)
		os.Exit(0)
	}()
}

// handleSyncdRestart 经 IPC 通知内核优雅退出（systemd 拉起）。
func (s *panelServer) handleSyncdRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.ipc.shutdown(r.Context()); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "通知内核失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "内核正在重启（约 5 秒后恢复）"})
}
