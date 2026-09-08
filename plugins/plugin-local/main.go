// plugin-local：本地目录源插件（外部进程协议实现）。
// 把一个本地目录当作同步源：snapshot 遍历目录生成 Manifest（fingerprint = 内容 sha256），
// fetchFile 从目录拷贝文件到 destPath。
// 既充当阶段 1 的 mock 插件（测试直接往 root 写文件即可制造变更），
// 也是真实可用的源类型（如同步 U 盘 / 挂载目录）。
//
// options: {"root": "<绝对路径>"}
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"edge-sync/pkg/protocol"
)

const pluginName = "local"
const pluginVersion = "0.1.0"

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[plugin-local] "+format+"\n", args...)
}

func main() {
	in := bufio.NewReaderSize(os.Stdin, 1024*1024)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return // stdin 关闭，正常退出
		}
		var req protocol.Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			logf("ignoring malformed request line: %v", err)
			continue
		}
		writeResp(out, handle(&req))
	}
}

func handle(req *protocol.Request) *protocol.Response {
	switch req.Method {
	case protocol.MethodInitialize:
		return ok(req.ID, protocol.InitializeResult{Name: pluginName, Version: pluginVersion})

	case protocol.MethodSnapshot:
		var p protocol.SnapshotParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		var opts map[string]any
		if err := json.Unmarshal(p.Config, &opts); err != nil {
			return errResp(req.ID, protocol.CodeInvalidParams, "invalid options: "+err.Error())
		}
		manifest, err := buildManifest(opts)
		if err != nil {
			return errResp(req.ID, protocol.CodeSourceUnreachable, err.Error())
		}
		return ok(req.ID, manifest)

	case protocol.MethodFetchFile:
		var p protocol.FetchFileParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		var opts map[string]any
		if err := json.Unmarshal(p.Config, &opts); err != nil {
			return errResp(req.ID, protocol.CodeInvalidParams, "invalid options: "+err.Error())
		}
		result, err := fetchFile(p, opts)
		if err != nil {
			return errResp(req.ID, protocol.CodeInternalError, err.Error())
		}
		return ok(req.ID, result)

	default:
		return errResp(req.ID, protocol.CodeMethodNotFound, "unknown method: "+req.Method)
	}
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
		logf("marshal response: %v", err)
		return
	}
	w.Write(raw)
	w.WriteByte('\n')
	w.Flush()
}

// ---------- snapshot ----------

func buildManifest(cfg map[string]any) (*protocol.Manifest, error) {
	root, err := rootOf(cfg)
	if err != nil {
		return nil, err
	}
	manifest := &protocol.Manifest{}
	type kv struct{ path, fp string }
	var parts []kv

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			logf("skipping non-regular file: %s", p)
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		sum, size, err := fileSHA256(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		manifest.Entries = append(manifest.Entries, protocol.FileEntry{
			Path:        rel,
			Fingerprint: sum,
			Size:        size,
			MTime:       info.ModTime().UTC().Format(time.RFC3339),
		})
		parts = append(parts, kv{rel, sum})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(manifest.Entries, func(i, j int) bool {
		return manifest.Entries[i].Path < manifest.Entries[j].Path
	})
	// 源级指纹：排序后 path+hash 拼接的整体 sha256。
	sort.Slice(parts, func(i, j int) bool { return parts[i].path < parts[j].path })
	h := sha256.New()
	for _, x := range parts {
		h.Write([]byte(x.path))
		h.Write([]byte{0})
		h.Write([]byte(x.fp))
		h.Write([]byte{'\n'})
	}
	if len(parts) > 0 {
		manifest.ManifestFingerprint = hex.EncodeToString(h.Sum(nil))
	}
	logf("snapshot: %d files, root=%s", len(manifest.Entries), root)
	return manifest, nil
}

// ---------- fetchFile ----------

func fetchFile(p protocol.FetchFileParams, cfg map[string]any) (*protocol.FetchFileResult, error) {
	root, err := rootOf(cfg)
	if err != nil {
		return nil, err
	}
	src, err := safeJoin(root, p.Path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p.DestPath), 0o755); err != nil {
		return nil, err
	}

	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	tmp := p.DestPath + ".part"
	dst, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(dst, hash), f)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, p.DestPath); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	logf("fetched %s (%d bytes)", p.Path, size)
	return &protocol.FetchFileResult{
		Size:     size,
		Checksum: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

// ---------- helpers ----------

func rootOf(cfg map[string]any) (string, error) {
	v, ok := cfg["root"]
	if !ok {
		return "", errors.New(`options.root is required (absolute path)`)
	}
	root, ok := v.(string)
	if !ok || root == "" {
		return "", errors.New(`options.root must be a non-empty string`)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("root %q: %w", abs, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("root %q is not a directory", abs)
	}
	return abs, nil
}

// safeJoin 与内核 engine.SafeJoin 同语义：拒绝绝对路径与 ".." 段。
func safeJoin(base, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty path")
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

func fileSHA256(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}
