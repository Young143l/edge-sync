// IPC 返回结构（内核 ⇄ 面板/CLI 共享）。
// 本文件为这些类型的单一真源：kernel/internal/ipc（服务端）、
// panel/goserver（客户端）与 TS 侧 protocol/src/index.ts 均与此对齐。
package protocol

// Retention 版本保留策略（与 config.yaml 的 retention 字段一致）。
// KeepLast=0 表示不按数量限制（仅 keepDays 生效）；
// KeepDays=0 表示不按时间限制（仅 keepLast 生效）。
type Retention struct {
	KeepLast int `json:"keepLast" yaml:"keepLast"`
	KeepDays int `json:"keepDays" yaml:"keepDays"`
}

// Stats 任务累计统计。
type Stats struct {
	TotalSyncs int   `json:"totalSyncs" yaml:"totalSyncs"` // 成功同步次数
	TotalFiles int   `json:"totalFiles" yaml:"totalFiles"` // 累计拉取文件数
	TotalBytes int64 `json:"totalBytes" yaml:"totalBytes"` // 累计拉取字节数
}

// TaskSummary 任务摘要（task.list / task.status 基础字段）。
type TaskSummary struct {
	Name                string `json:"name"`
	Plugin              string `json:"plugin"`
	Enabled             bool   `json:"enabled"`
	Status              string `json:"status"` // idle | syncing
	Interval            string `json:"interval"`
	LastSyncAt          string `json:"lastSyncAt,omitempty"`
	LastSuccessAt       string `json:"lastSuccessAt,omitempty"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	NextRunAt           string `json:"nextRunAt,omitempty"`
}

// TaskDetail 单任务详情。
type TaskDetail struct {
	TaskSummary
	Retention Retention `json:"retention"`
	Stats     Stats     `json:"stats"`
}

// VersionInfo 版本条目。
type VersionInfo struct {
	Version   string `json:"version"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
	IsCurrent bool   `json:"isCurrent"`
}

// FileListEntry 版本目录内的条目。
type FileListEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
	MTime string `json:"mtime,omitempty"`
}

// FileVersionEntry 单文件在历史版本中的出现记录（仅列出与上一版本不同的时点）。
type FileVersionEntry struct {
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
	Size        int64  `json:"size,omitempty"`
}

// PluginInfo 插件元信息。
type PluginInfo struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	ConfigSchema []byte `json:"configSchema,omitempty"`
}
