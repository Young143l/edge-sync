// Package pluginkit 是 edge-sync 插件的公共骨架：
// 行分隔 JSON-RPC 主循环、带协议码的错误、安全路径拼接、
// 流式拷贝（附 sha256）与源级指纹合成。
// 插件 main 只需实现 Handler 并调用 Run。
package pluginkit

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"edge-sync/pkg/protocol"
)

// Handler 插件业务接口（协议方法到业务的一对一映射）。
type Handler interface {
	// Initialize 返回插件名、版本与任务 options 的 JSON Schema 片段
	// （协议握手用；schema 供 CLI 向导与面板设置页展示/校验，可为 nil）。
	Initialize() (name, version string, configSchema json.RawMessage)
	// Snapshot 返回远端文件清单。cfg 为任务 options（已展开的 map）。
	Snapshot(cfg map[string]any) (*protocol.Manifest, error)
	// FetchFile 把远端单个文件写到 destPath（先写临时文件再 rename）。
	FetchFile(cfg map[string]any, path, destPath string) (*protocol.FetchFileResult, error)
}

// Error 携带协议错误码的错误类型，插件用构造函数返回特定码。
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func coded(code int, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func SourceUnreachable(format string, args ...any) error {
	return coded(protocol.CodeSourceUnreachable, format, args...)
}

func AuthFailed(format string, args ...any) error {
	return coded(protocol.CodeAuthFailed, format, args...)
}

func NotFound(format string, args ...any) error {
	return coded(protocol.CodeNotFound, format, args...)
}

func InvalidParams(format string, args ...any) error {
	return coded(protocol.CodeInvalidParams, format, args...)
}

var logPrefix = "[plugin]"

// Logf 输出到 stderr，由内核采集为插件日志。插件名在 Run 时自动成为前缀。
func Logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, logPrefix+" "+format+"\n", args...)
}

// Run 启动行协议主循环；stdin 关闭（内核结束会话）时返回。
func Run(h Handler) {
	name, version, _ := h.Initialize()
	logPrefix = "[" + name + "]"
	Logf("starting, version %s, protocol v%d", version, protocol.ProtocolVersion)

	in := bufio.NewReaderSize(os.Stdin, 1024*1024)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return
		}
		var req protocol.Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			Logf("ignoring malformed request line: %v", err)
			continue
		}
		writeResp(out, dispatch(h, &req))
	}
}

func dispatch(h Handler, req *protocol.Request) *protocol.Response {
	switch req.Method {
	case protocol.MethodInitialize:
		name, version, schema := h.Initialize()
		return ok(req.ID, protocol.InitializeResult{
			Name: name, Version: version, ConfigSchema: schema,
		})

	case protocol.MethodSnapshot:
		var p protocol.SnapshotParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		opts, err := optionsOf(p.Config)
		if err != nil {
			return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		result, err := h.Snapshot(opts)
		if err != nil {
			return errResp(req.ID, codeOf(err), err.Error())
		}
		return ok(req.ID, result)

	case protocol.MethodFetchFile:
		var p protocol.FetchFileParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		opts, err := optionsOf(p.Config)
		if err != nil {
			return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		result, err := h.FetchFile(opts, p.Path, p.DestPath)
		if err != nil {
			return errResp(req.ID, codeOf(err), err.Error())
		}
		return ok(req.ID, result)

	default:
		return errResp(req.ID, protocol.CodeMethodNotFound, "unknown method: "+req.Method)
	}
}

func optionsOf(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var opts map[string]any
	if err := json.Unmarshal(raw, &opts); err != nil {
		return nil, fmt.Errorf("invalid options: %w", err)
	}
	return opts, nil
}

func codeOf(err error) int {
	var e *Error
	if errorsAs(err, &e) {
		return e.Code
	}
	return protocol.CodeInternalError
}

// errorsAs 局部实现，避免直接依赖 errors 包名冲突。
func errorsAs(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func ok(id int64, result any) *protocol.Response {
	raw, _ := json.Marshal(result)
	return &protocol.Response{JSONRPC: "2.0", ID: id, Result: raw}
}

func errResp(id int64, code int, msg string) *protocol.Response {
	return &protocol.Response{JSONRPC: "2.0", ID: id,
		Error: &protocol.Error{Code: code, Message: msg}}
}

func writeResp(w *bufio.Writer, resp *protocol.Response) {
	raw, err := json.Marshal(resp)
	if err != nil {
		Logf("marshal response: %v", err)
		return
	}
	w.Write(raw)
	w.WriteByte('\n')
	w.Flush()
}

// SafeJoin 把 '/' 分隔的相对路径安全接在 base 下。
// 拒绝绝对路径与含 ".." 段的路径（防清单路径注入越界）。
func SafeJoin(base, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path not allowed: %q", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", fmt.Errorf("path escapes base: %q", rel)
		}
	}
	clean := path.Clean("/" + rel)
	trimmed := strings.TrimPrefix(clean, "/")
	if trimmed == "" || trimmed == "." {
		return "", fmt.Errorf("path resolves to base itself: %q", rel)
	}
	return filepath.Join(base, filepath.FromSlash(trimmed)), nil
}

// CopyWithHash 把 src 流式拷贝到 destPath（先写 .part 再 rename），
// 返回最终字节数与整文件 sha256 hex。
func CopyWithHash(src, destPath string) (int64, string, error) {
	f, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return 0, "", err
	}
	tmp := destPath + ".part"
	dst, err := os.Create(tmp)
	if err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(dst, hash), f)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return 0, "", err
	}
	if err := os.Rename(tmp, destPath); err != nil {
		os.Remove(tmp)
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

// WriteContentWithHash 把内存内容写到 destPath（.part + rename），用于 symlink 等特殊条目。
func WriteContentWithHash(content []byte, destPath string) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return 0, "", err
	}
	tmp := destPath + ".part"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return 0, "", err
	}
	if err := os.Rename(tmp, destPath); err != nil {
		os.Remove(tmp)
		return 0, "", err
	}
	sum := sha256.Sum256(content)
	return int64(len(content)), hex.EncodeToString(sum[:]), nil
}

// FileSHA256 计算文件整内容 sha256 hex（流式）。
func FileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CombineFingerprint 由排序后的 (path, fingerprint) 序列合成源级整体指纹。
func CombineFingerprint(entries []protocol.FileEntry) string {
	if len(entries) == 0 {
		return ""
	}
	sorted := make([]protocol.FileEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	for _, e := range sorted {
		h.Write([]byte(e.Path))
		h.Write([]byte{0})
		h.Write([]byte(e.Fingerprint))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
