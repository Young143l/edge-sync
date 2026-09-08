// Package protocol 定义内核与外部进程插件之间的通信类型。
// 本文件与 protocol/schema/*.schema.json 及 protocol/src/index.ts（TS 侧）
// 保持对齐，schema 为单一真源，修改须三处同步。
package protocol

import "encoding/json"

// ProtocolVersion 插件协议版本，initialize 握手时校验。
const ProtocolVersion = 1

// 方法名。
const (
	MethodInitialize = "initialize"
	MethodSnapshot   = "snapshot"
	MethodFetchFile  = "fetchFile"
)

// JSON-RPC 2.0 与插件自定义错误码（见 schema/envelope.schema.json errorCodes）。
const (
	CodeParseError        = -32700
	CodeInvalidRequest    = -32600
	CodeMethodNotFound    = -32601
	CodeInvalidParams     = -32602
	CodeInternalError     = -32603
	CodeSourceUnreachable = -32000
	CodeAuthFailed        = -32001
	CodeNotFound          = -32002
)

// Request 是内核发往插件的单行 JSON-RPC 请求。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Error 是 JSON-RPC 错误对象。
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Response 是插件返回的单行 JSON-RPC 响应，result 与 error 二选一。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// FileEntry 清单中的单个文件条目。
// Path 约定：相对路径、'/' 分隔、不以 '/' 开头，仅文件不含目录。
// Fingerprint 由插件自定义（blob hash / ETag / mtime+size），
// 内核只做字符串相等比较，不解释内容。
type FileEntry struct {
	Path        string `json:"path"`
	Fingerprint string `json:"fingerprint"`
	Size        int64  `json:"size,omitempty"`
	MTime       string `json:"mtime,omitempty"` // RFC3339 / ISO8601，仅展示用
}

// Manifest 远端文件清单快照。
// ManifestFingerprint 为源级整体指纹（如 git commit hash），
// 内核可借此在未变更时跳过 diff。
type Manifest struct {
	Entries             []FileEntry `json:"entries"`
	ManifestFingerprint string      `json:"manifestFingerprint,omitempty"`
}

// InitializeParams / InitializeResult：握手。
type InitializeParams struct {
	ProtocolVersion int `json:"protocolVersion"`
}

type InitializeResult struct {
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	ConfigSchema json.RawMessage `json:"configSchema,omitempty"` // 任务 options 的 JSON Schema
}

// SnapshotParams / SnapshotResult：取远端清单。Config 为任务 options 原样透传。
type SnapshotParams struct {
	Config json.RawMessage `json:"config"`
}

type SnapshotResult = Manifest

// FetchFileParams / FetchFileResult：下载单文件。
// 插件把文件写入 DestPath（先写临时文件再 rename），二进制不过协议管道。
type FetchFileParams struct {
	Config   json.RawMessage `json:"config"`
	Path     string          `json:"path"`
	DestPath string          `json:"destPath"`
}

type FetchFileResult struct {
	Size     int64  `json:"size"`
	Checksum string `json:"checksum,omitempty"` // 建议 sha256 hex
}
