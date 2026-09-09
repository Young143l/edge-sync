/**
 * edge-sync 协议与 IPC 返回类型（TS 侧，供面板前端使用）。
 *
 * 规范描述见 docs/DESIGN.md §3；Go 侧对应 pkg/protocol/types.go，
 * 本文件为 web 前端的类型定义（修改须双侧同步）。
 */

/** 版本目录内元数据文件名（与内核 engine.ManifestFile 对齐）。 */
export const MANIFEST_FILE = '.edge-sync-manifest.json' as const

// ---------- 任务 / 历史 / 插件 / 日志（IPC 返回结构） ----------

export interface TaskSummary {
  name: string
  plugin: string
  enabled: boolean
  /** idle | syncing | paused */
  status: 'idle' | 'syncing' | 'paused'
  interval: string
  lastSyncAt?: string
  lastSuccessAt?: string
  consecutiveFailures: number
  nextRunAt?: string
}

export interface TaskDetail extends TaskSummary {
  retention: { keepLast: number; keepDays: number }
  stats: { totalSyncs: number; totalFiles: number; totalBytes: number }
}

export interface VersionInfo {
  version: string
  files: number
  bytes: number
  isCurrent: boolean
}

export interface FileListEntry {
  name: string
  isDir: boolean
  size: number
  mtime?: string
}

export interface FileVersionEntry {
  version: string
  fingerprint: string
  size?: number
}

export interface PluginInfo {
  name: string
  version: string
  configSchema?: Record<string, unknown>
}

export interface LogEntry {
  time: string
  level: string
  msg: string
  attrs?: string
}
