// Package state 负责任务状态的持久化：temp + rename 原子写，
// 任意断电点重启后状态文件要么是完整的旧值要么是完整的新值。
package state

import (
	"encoding/json"
	"os"
	"path/filepath"

	"edge-sync/internal/fsutil"
	"edge-sync/pkg/protocol"
)

// Stats 任务累计统计（定义见 pkg/protocol）。
type Stats = protocol.Stats

// TaskState 单任务持久化状态。
type TaskState struct {
	LastManifest        protocol.Manifest `json:"lastManifest"`
	ManifestFingerprint string            `json:"manifestFingerprint,omitempty"`
	LastSyncAt          string            `json:"lastSyncAt,omitempty"`    // RFC3339，含失败
	LastSuccessAt       string            `json:"lastSuccessAt,omitempty"` // RFC3339
	ConsecutiveFailures int               `json:"consecutiveFailures"`
	Stats               Stats             `json:"stats"`
}

// Store 管理所有任务的状态文件（stateDir/<task>.json）。
type Store struct {
	dir string
}

func NewStore(dir string) *Store { return &Store{dir: dir} }

// Load 读取任务状态；不存在或损坏时返回零值状态（损坏视为无历史，重新全量）。
func (s *Store) Load(task string) *TaskState {
	st := &TaskState{}
	raw, err := os.ReadFile(s.path(task))
	if err != nil {
		return st
	}
	if err := json.Unmarshal(raw, st); err != nil {
		return &TaskState{}
	}
	return st
}

// Save 原子写任务状态。
func (s *Store) Save(task string, st *TaskState) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	// fsync + rename：掉电后不会出现「文件名已换、内容为空」的状态文件。
	return fsutil.WriteFileAtomic(s.path(task), raw, 0o644)
}

func (s *Store) path(task string) string {
	return filepath.Join(s.dir, task+".json")
}
