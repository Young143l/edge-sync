// applier：把 staging 中的变更文件与上一版本组装成新版本目录，
// 经 current 相对 symlink 的 rename 原子切换对外呈现。
// 任何一步失败都会回滚新建的版本目录，current 保持指向上一版本。
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"edge-sync/internal/fsutil"
	"edge-sync/pkg/protocol"
)

const (
	versionTimeLayout = "20060102-150405"
	currentLinkName   = "current"
	// ManifestFile 每版本目录内的元数据文件（历史查询/单文件版本追溺用）。
	ManifestFile = ".edge-sync-manifest.json"
)

// ApplyInput Apply 的输入。
type ApplyInput struct {
	DataDir        string // data/<task>/
	StagingDir     string // data/<task>/staging/
	NewManifest    protocol.Manifest
	Changed        map[string]bool // path -> added/modified（需要从 staging 链接）
	PrevVersionDir string          // current 指向的上一版本目录；首次同步为空
}

// Apply 组装新版本目录并原子切换 current，返回新版本目录路径。
func Apply(in ApplyInput) (string, error) {
	versionsDir := filepath.Join(in.DataDir, "versions")
	newDir, err := nextVersionDir(versionsDir, time.Now())
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return "", fmt.Errorf("create version dir: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(newDir) // 失败回滚，current 不受影响
		}
	}()

	for i := range in.NewManifest.Entries {
		e := &in.NewManifest.Entries[i]
		dst, err := SafeJoin(newDir, e.Path)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", fmt.Errorf("mkdir for %q: %w", e.Path, err)
		}
		var src string
		if in.Changed[e.Path] {
			src, err = SafeJoin(in.StagingDir, e.Path)
		} else if in.PrevVersionDir != "" {
			src, err = SafeJoin(in.PrevVersionDir, e.Path)
		} else {
			err = fmt.Errorf("file %q not in change set but no previous version exists", e.Path)
		}
		if err != nil {
			return "", err
		}
		if err := linkChecked(src, dst, e.Size); err != nil {
			return "", err
		}
	}

	// 相对 symlink（versions/<ts>），data 目录整体迁移不受影响。
	tmpLink := filepath.Join(in.DataDir, "."+currentLinkName+".new")
	_ = os.Remove(tmpLink)
	if err := os.Symlink(filepath.Join("versions", filepath.Base(newDir)), tmpLink); err != nil {
		return "", fmt.Errorf("create symlink: %w", err)
	}
	if err := os.Rename(tmpLink, filepath.Join(in.DataDir, currentLinkName)); err != nil {
		return "", fmt.Errorf("switch current: %w", err)
	}

	// staging 清理重建
	if err := os.RemoveAll(in.StagingDir); err != nil {
		return "", fmt.Errorf("clear staging: %w", err)
	}
	if err := os.MkdirAll(in.StagingDir, 0o755); err != nil {
		return "", fmt.Errorf("recreate staging: %w", err)
	}

	// 版本元数据：后续 history.list / history.fileVersions 直接读它。
	if err := writeManifestFile(newDir, in.NewManifest); err != nil {
		return "", fmt.Errorf("write manifest file: %w", err)
	}

	committed = true
	return newDir, nil
}

func writeManifestFile(versionDir string, m protocol.Manifest) error {
	type manifestFile struct {
		ManifestFingerprint string               `json:"manifestFingerprint,omitempty"`
		Entries             []protocol.FileEntry `json:"entries"`
	}
	mf := manifestFile{ManifestFingerprint: m.ManifestFingerprint, Entries: m.Entries}
	raw, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(versionDir, ManifestFile), raw, 0o644)
}

// ReadVersionManifest 读取版本目录内落盘的元数据；不存在返回 nil（老版本）。
func ReadVersionManifest(versionDir string) *protocol.Manifest {
	raw, err := os.ReadFile(filepath.Join(versionDir, ManifestFile))
	if err != nil {
		return nil
	}
	var m protocol.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &m
}

// ReadCurrentTarget 返回 data/current 指向的版本目录绝对路径；
// current 不存在或不是 symlink 时返回空串（首次同步）。
func ReadCurrentTarget(dataDir string) string {
	target, err := os.Readlink(filepath.Join(dataDir, currentLinkName))
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dataDir, target)
	}
	return filepath.Clean(target)
}

// ValidateFetched 对 fetchFile 结果做落盘校验：size 一致，提供 checksum 则校验 sha256。
func ValidateFetched(stagingDir string, e protocol.FileEntry, gotSize int64, gotChecksum string) error {
	p, err := SafeJoin(stagingDir, e.Path)
	if err != nil {
		return err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("staged file %q missing: %w", e.Path, err)
	}
	if gotSize > 0 && fi.Size() != gotSize {
		return fmt.Errorf("staged file %q size mismatch: plugin=%d disk=%d", e.Path, gotSize, fi.Size())
	}
	if e.Size > 0 && fi.Size() != e.Size {
		return fmt.Errorf("staged file %q size mismatch: manifest=%d disk=%d", e.Path, e.Size, fi.Size())
	}
	if gotChecksum != "" {
		sum, err := fileSHA256(p)
		if err != nil {
			return err
		}
		if sum != gotChecksum {
			return fmt.Errorf("staged file %q checksum mismatch", e.Path)
		}
	}
	return nil
}

func fileSHA256(p string) (string, error) {
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

// SafeJoin 把 '/' 分隔的相对路径安全接在 base 下。
// 拒绝绝对路径与含 ".." 段的路径（防插件 path 注入越界）。
func SafeJoin(base, rel string) (string, error) {
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
	clean := path.Clean("/" + rel) // 钳制边界情况（"a//b"、"./a"）
	trimmed := strings.TrimPrefix(clean, "/")
	if trimmed == "" || trimmed == "." {
		return "", fmt.Errorf("path resolves to base itself: %q", rel)
	}
	return filepath.Join(base, filepath.FromSlash(trimmed)), nil
}

// linkChecked 硬链接 src → dst，链接前校验 src 为常规文件且 size 匹配。
func linkChecked(src, dst string, wantSize int64) error {
	fi, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("source missing: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", src)
	}
	if wantSize > 0 && fi.Size() != wantSize {
		return fmt.Errorf("size mismatch: manifest=%d actual=%d (%s)", wantSize, fi.Size(), src)
	}
	return os.Link(src, dst)
}

// nextVersionDir 生成 versions/<ts> 目录路径，同秒冲突时追加 -N 后缀。
func nextVersionDir(versionsDir string, ts time.Time) (string, error) {
	base := ts.Format(versionTimeLayout)
	dir := filepath.Join(versionsDir, base)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return dir, nil
	}
	for i := 1; ; i++ {
		dir = filepath.Join(versionsDir, base+"-"+strconv.Itoa(i))
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return dir, nil
		}
	}
}
