// plugin-local：本地目录源插件（外部进程协议实现）。
// 把一个本地目录当作同步源：snapshot 遍历目录生成 Manifest（fingerprint = 内容 sha256），
// fetchFile 从目录拷贝文件到 destPath。
// 既充当测试用的 mock 插件（直接往 root 写文件即可制造变更），
// 也是真实可用的源类型（如同步 U 盘 / 挂载目录）。
//
// options: {"root": "<绝对路径>"}
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"edge-sync/pkg/pluginkit"
	"edge-sync/pkg/protocol"
)

const (
	pluginName    = "local"
	pluginVersion = "0.1.0"
)

type localPlugin struct{}

func (localPlugin) Initialize() (string, string) { return pluginName, pluginVersion }

func (localPlugin) Snapshot(cfg map[string]any) (*protocol.Manifest, error) {
	root, err := rootOf(cfg)
	if err != nil {
		return nil, pluginkit.InvalidParams("%v", err)
	}
	manifest := &protocol.Manifest{}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			if !d.IsDir() {
				pluginkit.Logf("skipping non-regular file: %s", p)
			}
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
		return nil
	})
	if err != nil {
		return nil, pluginkit.SourceUnreachable("walk %q: %v", root, err)
	}
	manifest.ManifestFingerprint = pluginkit.CombineFingerprint(manifest.Entries)
	pluginkit.Logf("snapshot: %d files, root=%s", len(manifest.Entries), root)
	return manifest, nil
}

func (localPlugin) FetchFile(cfg map[string]any, path, destPath string) (*protocol.FetchFileResult, error) {
	root, err := rootOf(cfg)
	if err != nil {
		return nil, pluginkit.InvalidParams("%v", err)
	}
	src, err := pluginkit.SafeJoin(root, path)
	if err != nil {
		return nil, err
	}
	size, sum, err := pluginkit.CopyWithHash(src, destPath)
	if err != nil {
		return nil, pluginkit.SourceUnreachable("copy %q: %v", path, err)
	}
	pluginkit.Logf("fetched %s (%d bytes)", path, size)
	return &protocol.FetchFileResult{Size: size, Checksum: sum}, nil
}

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

func fileSHA256(p string) (string, int64, error) {
	sum, err := pluginkit.FileSHA256(p)
	if err != nil {
		return "", 0, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", 0, err
	}
	return sum, fi.Size(), nil
}

func main() { pluginkit.Run(localPlugin{}) }
