/**
 * edge-sync 插件协议（TS 侧类型）。
 *
 * 单一真源为 schema/*.schema.json（envelope / manifest / methods），
 * 本文件与其保持对齐；Go 侧对应 kernel/internal/protocol/types.go。
 * 修改须三处同步。
 */

/** 插件协议版本，initialize 握手时校验。 */
export const PROTOCOL_VERSION = 1

/** 方法名。 */
export const PluginMethod = {
  Initialize: 'initialize',
  Snapshot: 'snapshot',
  FetchFile: 'fetchFile',
} as const
export type PluginMethod = (typeof PluginMethod)[keyof typeof PluginMethod]

/** JSON-RPC 2.0 与插件自定义错误码（见 schema/envelope.schema.json errorCodes）。 */
export const ErrorCode = {
  ParseError: -32700,
  InvalidRequest: -32600,
  MethodNotFound: -32601,
  InvalidParams: -32602,
  InternalError: -32603,
  SourceUnreachable: -32000,
  AuthFailed: -32001,
  NotFound: -32002,
} as const

// ---------- JSON-RPC 2.0 信封（行分隔） ----------

export interface JsonRpcRequest<T = unknown> {
  jsonrpc: '2.0'
  id: number
  method: PluginMethod
  params: T
}

export interface JsonRpcError {
  code: number
  message: string
  data?: unknown
}

export type JsonRpcResponse<T = unknown> =
  | { jsonrpc: '2.0'; id: number; result: T; error?: never }
  | { jsonrpc: '2.0'; id: number; result?: never; error: JsonRpcError }

// ---------- Manifest ----------

/** 清单中的单个文件条目。path：相对路径、'/' 分隔、不以 '/' 开头，仅文件。 */
export interface FileEntry {
  path: string
  /** 插件自定义（blob hash / ETag / mtime+size），内核仅做字符串相等比较。 */
  fingerprint: string
  size?: number
  /** RFC3339 / ISO8601，仅展示用。 */
  mtime?: string
}

/** 远端文件清单快照。manifestFingerprint 为源级整体指纹，可借以跳过 diff。 */
export interface Manifest {
  entries: FileEntry[]
  manifestFingerprint?: string
}

// ---------- 方法报文 ----------

export interface InitializeParams {
  protocolVersion: number
}

export interface InitializeResult {
  name: string
  version: string
  /** 任务 options 的 JSON Schema 片段，供 CLI/面板做配置校验提示。 */
  configSchema?: Record<string, unknown>
}

/** Config 为任务 options 原样透传（结构由各插件 configSchema 定义）。 */
export interface SnapshotParams {
  config: Record<string, unknown>
}

export type SnapshotResult = Manifest

/**
 * 下载单文件：插件把文件写入 destPath（先写临时文件再 rename），
 * 二进制不过协议管道；父目录由插件创建。
 */
export interface FetchFileParams {
  config: Record<string, unknown>
  path: string
  destPath: string
}

export interface FetchFileResult {
  size: number
  /** 建议 sha256 hex，内核据此校验。 */
  checksum?: string
}

/** 各方法参数/结果类型映射。 */
export interface PluginProtocol {
  initialize: { params: InitializeParams; result: InitializeResult }
  snapshot: { params: SnapshotParams; result: SnapshotResult }
  fetchFile: { params: FetchFileParams; result: FetchFileResult }
}

// ---------- IPC（内核 ⇄ CLI / 面板）----------

/** task.list 条目。 */
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

/** task.status 结果。 */
export interface TaskDetail extends TaskSummary {
  retention: { keepLast: number; keepDays: number }
  stats: { totalSyncs: number; totalFiles: number; totalBytes: number }
}

/** history.list 条目。 */
export interface VersionInfo {
  version: string
  files: number
  bytes: number
  isCurrent: boolean
}

/** history.files 条目。 */
export interface FileListEntry {
  name: string
  isDir: boolean
  size: number
  mtime?: string
}

/** history.fileVersions 条目（仅与上一版本不同的时点，旧→新）。 */
export interface FileVersionEntry {
  version: string
  fingerprint: string
  size?: number
}

/** plugin.list 条目。 */
export interface PluginInfo {
  name: string
  version: string
  configSchema?: Record<string, unknown>
}

/** log.tail 条目。 */
export interface LogEntry {
  time: string
  level: string
  msg: string
  attrs?: string
}
