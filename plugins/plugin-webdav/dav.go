// WebDAV 客户端实现：PROPFIND 遍历（infinity → 逐层降级）、GET + Range 断点续传。
package main

import (
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"edge-sync/pkg/pluginkit"
	"edge-sync/pkg/protocol"
)

var errDepthUnsupported = errors.New("depth infinity not supported by server")

type davEntry struct {
	RelPath string
	IsDir   bool
	ETag    string
	Size    int64
	ModTime time.Time
}

// fingerprint 按策略生成：etag 优先，条目无 ETag 或策略为 mtime_size 时用 mtime+size。
func (de davEntry) fingerprint(policy string) string {
	if policy == FingerprintEtag && de.ETag != "" {
		return de.ETag
	}
	return fmt.Sprintf("%d:%d", de.ModTime.UnixNano(), de.Size)
}

func (de davEntry) ModTimeUTC() string {
	if de.ModTime.IsZero() {
		return ""
	}
	return de.ModTime.UTC().Format(time.RFC3339)
}

type davClient struct {
	base *url.URL
	opts *davOptions
	hc   *http.Client
}

func newDavClient(opts *davOptions) (*davClient, error) {
	u, err := url.Parse(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second, // 大文件 GET 头部等待；总时长由内核超时控制
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: opts.InsecureTLS},
	}
	return &davClient{
		base: u,
		opts: opts,
		hc:   &http.Client{Transport: transport},
	}, nil
}

const propfindBody = `<?xml version="1.0"?>
<d:propfind xmlns:d="DAV:">
  <d:prop>
    <d:resourcetype/>
    <d:getetag/>
    <d:getcontentlength/>
    <d:getlastmodified/>
  </d:prop>
</d:propfind>`

// listAll 列出 base 下全部条目（含目录，由调用方过滤）。优先 Depth:infinity，被拒时降级逐层 BFS。
func (c *davClient) listAll() ([]davEntry, error) {
	entries, err := c.propfind(c.base, "infinity")
	if err == nil {
		return entries, nil
	}
	if !errors.Is(err, errDepthUnsupported) {
		return nil, err
	}
	pluginkit.Logf("server rejected Depth:infinity, falling back to level-by-level traversal")

	seen := map[string]bool{c.base.Path: true}
	var all []davEntry
	queue := []*url.URL{c.base}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		level, err := c.propfind(cur, "1")
		if err != nil {
			return nil, err
		}
		for _, de := range level {
			all = append(all, de)
			if de.IsDir {
				child := *c.base
				child.Path = path.Join(c.base.Path, de.RelPath) + "/"
				if !seen[child.Path] {
					seen[child.Path] = true
					queue = append(queue, &child)
				}
			}
		}
	}
	return all, nil
}

// propfind 解析一次 PROPFIND 响应；把 href 换算为相对 base 的路径。
func (c *davClient) propfind(target *url.URL, depth string) ([]davEntry, error) {
	req, err := http.NewRequest("PROPFIND", target.String(), strings.NewReader(propfindBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", depth)
	req.Header.Set("Content-Type", "application/xml")
	if c.opts.Username != "" || c.opts.Password != "" {
		req.SetBasicAuth(c.opts.Username, c.opts.Password)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, pluginkit.SourceUnreachable("PROPFIND %s: %v", target, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusMultiStatus: // 207
	case http.StatusBadRequest, http.StatusNotImplemented:
		if depth == "infinity" {
			return nil, fmt.Errorf("%w (status %d)", errDepthUnsupported, resp.StatusCode)
		}
		return nil, pluginkit.SourceUnreachable("PROPFIND %s: status %d", target, resp.StatusCode)
	case http.StatusUnauthorized:
		return nil, pluginkit.AuthFailed("PROPFIND %s: status 401 (check credentials)", target)
	case http.StatusForbidden:
		// 部分服务器对 infinity 返回 403；带认证的逐层请求 403 才是权限问题。
		if depth == "infinity" {
			return nil, fmt.Errorf("%w (status %d)", errDepthUnsupported, resp.StatusCode)
		}
		return nil, pluginkit.AuthFailed("PROPFIND %s: status 403 (check credentials)", target)
	case http.StatusNotFound:
		return nil, pluginkit.NotFound("PROPFIND %s: not found", target)
	default:
		return nil, pluginkit.SourceUnreachable("PROPFIND %s: unexpected status %d", target, resp.StatusCode)
	}

	var ms multistatus
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, pluginkit.SourceUnreachable("read PROPFIND body: %v", err)
	}
	if err := xml.Unmarshal(body, &ms); err != nil {
		return nil, pluginkit.SourceUnreachable("parse PROPFIND response: %v", err)
	}

	var out []davEntry
	for _, rs := range ms.Responses {
		rel, isDirHref, ok := c.relOf(target, rs.HRef)
		if !ok {
			continue // base 自身或 base 子树之外的响应
		}
		entry := davEntry{RelPath: rel, IsDir: isDirHref}
		for _, ps := range rs.Propstats {
			if statusInt(ps.Status) != http.StatusOK {
				continue // 个别 prop 404（如服务器不支持 etag）不致命
			}
			entry.IsDir = entry.IsDir || ps.Prop.ResourceType.Collection != nil
			entry.ETag = ps.Prop.ETag
			entry.Size = ps.Prop.ContentLength
			if t, err := http.ParseTime(ps.Prop.LastModified); err == nil {
				entry.ModTime = t
			}
		}
		if entry.IsDir {
			out = append(out, entry) // 目录条目保留：BFS 需要下钻，snapshot 层过滤
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// statusInt 解析 "HTTP/1.1 200 OK" 形式的状态行，取第二段的数字部分。
func statusInt(s string) int {
	parts := strings.Fields(s)
	if len(parts) < 2 {
		return 0
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	return n
}

// relOf 把响应 href 换算为相对 base 的路径。候选消歧顺序：
// A. 绝对 URL / 以 / 开头的绝对路径：标准解析（含挂载点相对的拼回重试）；
// B. 相对引用：优先按「相对 base（WebDAV 根 / handler 根）」解释（x/net/webdav 等
//
//	实现的 href 形态），再按「相对当前请求目录」解释（RFC 3986 标准语义）。
//
// 返回 (relPath, isDirByTrailingSlash, belongsToBase)。
func (c *davClient) relOf(target *url.URL, href string) (string, bool, bool) {
	if href == "" {
		return "", false, false
	}
	ref, err := url.Parse(href)
	if err != nil {
		return "", false, false
	}

	if ref.IsAbs() || strings.HasPrefix(href, "/") {
		// A1: 标准解析。
		resolved := target.ResolveReference(ref)
		if rel, isDir, ok := c.relFromAbs(resolved.Path); ok {
			return rel, isDir, true
		}
		// A2: 绝对路径形式但相对于 handler 挂载点（缺真实前缀）。
		if !ref.IsAbs() {
			if decoded, err := url.PathUnescape(ref.Path); err == nil {
				if rel, isDir, ok := c.relFromAbs(path.Join(c.base.Path, decoded)); ok {
					return rel, isDir, true
				}
			}
		}
		return "", false, false
	}

	// B: 相对引用 —— 先按相对 base 根解释，再按相对当前目录解释。
	if rel, isDir, ok := c.relFromAbs(path.Join(c.base.Path, href)); ok {
		return rel, isDir, true
	}
	resolved := target.ResolveReference(ref)
	if rel, isDir, ok := c.relFromAbs(resolved.Path); ok {
		return rel, isDir, true
	}
	return "", false, false
}

func (c *davClient) relFromAbs(decoded string) (string, bool, bool) {
	norm := strings.TrimSuffix(decoded, "/")
	baseNorm := strings.TrimSuffix(c.base.Path, "/")
	if norm == baseNorm {
		return "", false, false // base 自身
	}
	if !strings.HasPrefix(decoded, c.base.Path) {
		return "", false, false
	}
	rel := strings.TrimPrefix(decoded, c.base.Path)
	isDir := strings.HasSuffix(rel, "/") || strings.HasSuffix(decoded, "/")
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		return "", false, false
	}
	return rel, isDir, true
}

// fetch GET 下载单文件到 destPath，支持 Range 断点续传。
// 策略：.part 存在则尝试续传；响应 206 → 追加；200 → 全量重写；
// 最终 size 与预期不符 → 删除 .part 全量重试一次；仍不符 → 报错。
func (c *davClient) fetch(relPath, destPath string) (*protocol.FetchFileResult, error) {
	expected, err := c.fetchOnce(relPath, destPath, true)
	if err == nil {
		return expected, nil
	}
	if !errors.Is(err, errSizeMismatch) {
		return nil, err
	}
	pluginkit.Logf("resumed download size mismatch for %s, retrying full download", relPath)
	os.Remove(destPath + ".part")
	res, err := c.fetchOnce(relPath, destPath, false)
	if err != nil {
		return nil, err
	}
	return res, nil
}

var errSizeMismatch = errors.New("size mismatch after download")

func (c *davClient) fetchOnce(relPath, destPath string, allowResume bool) (*protocol.FetchFileResult, error) {
	u := *c.base
	u.Path = path.Join(c.base.Path, relPath)

	var offset int64
	partFile := destPath + ".part"
	if allowResume {
		if fi, err := os.Stat(partFile); err == nil {
			offset = fi.Size()
		}
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.opts.Username != "" || c.opts.Password != "" {
		req.SetBasicAuth(c.opts.Username, c.opts.Password)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, pluginkit.SourceUnreachable("GET %s: %v", u.String(), err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK: // 全量（或服务器不支持 Range）
		offset = 0
	case http.StatusPartialContent: // 续传
	case http.StatusNotFound:
		return nil, pluginkit.NotFound("file %q not found", relPath)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, pluginkit.AuthFailed("GET %s: status %d (check credentials)", relPath, resp.StatusCode)
	default:
		return nil, pluginkit.SourceUnreachable("GET %s: unexpected status %d", relPath, resp.StatusCode)
	}
	if offset > 0 && resp.StatusCode == http.StatusOK && !strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes") {
		offset = 0 // 服务器忽略 Range 返回 200，从头写
	}

	if err := os.MkdirAll(path.Dir(destPath), 0o755); err != nil {
		return nil, err
	}
	flag := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(partFile, flag, 0o644)
	if err != nil {
		return nil, err
	}
	n, copyErr := io.Copy(f, resp.Body)
	if cerr := f.Close(); copyErr == nil {
		copyErr = cerr
	}
	if copyErr != nil {
		return nil, pluginkit.SourceUnreachable("download %s: %v", relPath, copyErr)
	}
	size := offset + n

	// 与 manifest 元数据校验（若有）。
	if cl := resp.Header.Get("Content-Length"); cl != "" && resp.StatusCode == http.StatusOK {
		if want, err := strconv.ParseInt(cl, 10, 64); err == nil && want != size {
			return nil, fmt.Errorf("%w: content-length=%d got=%d", errSizeMismatch, want, size)
		}
	}

	sum, err := pluginkit.FileSHA256(partFile)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(partFile, destPath); err != nil {
		return nil, err
	}
	return &protocol.FetchFileResult{Size: size, Checksum: sum}, nil
}

// ---------- XML 解析（宽松匹配 local name，兼容任意命名空间前缀） ----------

type multistatus struct {
	Responses []struct {
		HRef      string `xml:"href"`
		Propstats []struct {
			Status string `xml:"status"`
			Prop   struct {
				ResourceType struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
				ETag          string `xml:"getetag"`
				ContentLength int64  `xml:"getcontentlength"`
				LastModified  string `xml:"getlastmodified"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}
