// 下载流与整版本 zip：fs 直读（不过内核），路径白名单 + 并发限流。
package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const manifestFile = ".edge-sync-manifest.json" // 与内核 engine.ManifestFile 对齐

var versionNameRe = regexp.MustCompile(`^\d{8}-\d{6}(-\d+)?$`)

const MB = 1024 * 1024

// downloadLimiter 全局并发上限 + 排队（超出 maxQueue 拒绝）。
type downloadLimiter struct {
	sem chan struct{}
	mu  struct {
		waiting int
	}
	maxQueue int
}

func newLimiter(max int) *downloadLimiter {
	return &downloadLimiter{sem: make(chan struct{}, max), maxQueue: 8}
}

func (l *downloadLimiter) acquire(w http.ResponseWriter) bool {
	if len(l.sem) >= cap(l.sem) && l.mu.waiting >= l.maxQueue {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many concurrent downloads"})
		return false
	}
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		l.mu.waiting++
		l.sem <- struct{}{}
		l.mu.waiting--
		return true
	}
}

func (l *downloadLimiter) release() {
	<-l.sem
}

// resolveVersionDir 把 "current" 或版本目录名解析为真实版本目录（防穿越）。
func resolveVersionDir(dataDir, task, version string) (string, error) {
	if task == "" || strings.ContainsAny(task, "/\\") || strings.Contains(task, "..") {
		return "", &httpError{status: http.StatusBadRequest, msg: "invalid task name"}
	}
	dataRoot := filepath.Join(dataDir, task)
	if version == "" || version == "current" {
		link := filepath.Join(dataRoot, "current")
		real, err := filepath.EvalSymlinks(link)
		if err != nil {
			return "", &httpError{status: http.StatusNotFound, msg: "no current version yet"}
		}
		return real, nil
	}
	if !versionNameRe.MatchString(version) {
		return "", &httpError{status: http.StatusBadRequest, msg: fmt.Sprintf("invalid version name: %s", version)}
	}
	dir := filepath.Join(dataRoot, "versions", version)
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return "", &httpError{status: http.StatusNotFound, msg: fmt.Sprintf("version not found: %s", version)}
	}
	return filepath.EvalSymlinks(dir)
}

// resolveFileUnder 版本目录内的文件解析：拒绝越界（SafeJoin 语义）、必须常规文件。
func resolveFileUnder(versionDir, subPath string) (string, error) {
	if subPath == "" || strings.Contains(subPath, "\x00") {
		return "", &httpError{status: http.StatusBadRequest, msg: "invalid path"}
	}
	clean := filepath.Clean(filepath.Join(versionDir, subPath))
	if !strings.HasPrefix(clean, versionDir+string(os.PathSeparator)) {
		return "", &httpError{status: http.StatusBadRequest, msg: "path escapes version directory"}
	}
	fi, err := os.Lstat(clean)
	if err != nil {
		return "", &httpError{status: http.StatusNotFound, msg: fmt.Sprintf("file not found: %s", subPath)}
	}
	if !fi.Mode().IsRegular() {
		return "", &httpError{status: http.StatusBadRequest, msg: "not a regular file"}
	}
	return clean, nil
}

// httpError 带 HTTP 状态的错误。
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func (s *panelServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	if !s.dl.acquire(w) {
		return
	}
	defer s.dl.release()

	versionDir, err := resolveVersionDir(s.cfg.DataDir, r.PathValue("name"), r.URL.Query().Get("version"))
	if err != nil {
		writeHErr(w, err)
		return
	}
	file, err := resolveFileUnder(versionDir, r.URL.Query().Get("path"))
	if err != nil {
		writeHErr(w, err)
		return
	}
	f, err := os.Open(file)
	if err != nil {
		writeHErr(w, err)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()

	name := filepath.Base(r.URL.Query().Get("path"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", urlPathEscape(name)))
	// ServeContent：自带 Range 断点续传与 Last-Modified。
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// estimateVersionBytes 读版本 manifest 估算体积；无元数据返回 0。
func estimateVersionBytes(versionDir string) int64 {
	raw, err := os.ReadFile(filepath.Join(versionDir, manifestFile))
	if err != nil {
		return 0
	}
	var mf struct {
		Entries []struct {
			Size int64 `json:"size"`
		} `json:"entries"`
	}
	if json.Unmarshal(raw, &mf) != nil {
		return 0
	}
	var sum int64
	for _, e := range mf.Entries {
		sum += e.Size
	}
	return sum
}

func (s *panelServer) handleArchive(w http.ResponseWriter, r *http.Request) {
	if !s.dl.acquire(w) {
		return
	}
	defer s.dl.release()

	version := r.URL.Query().Get("version")
	if version == "" {
		version = "current"
	}
	store := r.URL.Query().Get("store") == "1"

	versionDir, err := resolveVersionDir(s.cfg.DataDir, r.PathValue("name"), version)
	if err != nil {
		writeHErr(w, err)
		return
	}

	task := r.PathValue("name")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename*=UTF-8''%s", urlPathEscape(task+"-"+version+".zip")))
	w.Header().Set("Cache-Control", "no-store")
	if est := estimateVersionBytes(versionDir); est > 0 {
		w.Header().Set("X-Edge-Sync-Bytes", strconv.FormatInt(est, 10))
		if est > 500*MB {
			w.Header().Set("X-Edge-Sync-Suggest", "rsync")
		}
	}

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	// 递归写入版本目录（元数据文件排除）。
	err = filepath.WalkDir(versionDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == manifestFile {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(versionDir, p)
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{
			Name:     filepath.ToSlash(rel),
			Modified: fi.ModTime(),
		}
		if store {
			hdr.Method = zip.Store
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := zipWriter.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, src)
		return err
	})
	if err != nil && !errors.Is(err, fs.SkipDir) {
		// 头部已发送，无法改状态码；记录并中断。
		log.Printf("[panel] archive error: %v", err)
	}
}

// ---------- shared ----------

func urlPathEscape(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '/' || r == '-' || r == '.' || r == '_' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		} else {
			for _, by := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", by)
			}
		}
	}
	return b.String()
}

func writeHErr(w http.ResponseWriter, err error) {
	var he *httpError
	if errors.As(err, &he) {
		writeJSON(w, he.status, map[string]string{"error": he.msg})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
