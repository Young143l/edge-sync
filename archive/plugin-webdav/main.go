// plugin-webdav：WebDAV 源插件（外部进程协议实现）。
// snapshot：PROPFIND Depth:infinity 遍历（服务器拒绝时降级逐层 BFS）；
// fetchFile：GET 下载，支持 Range 断点续传（.part 残留续传，size 不符自动全量重试）。
//
// options:
//
//	url          string  必填，WebDAV 目录根 URL
//	username     string  可选，Basic Auth 用户名
//	password     string  可选，Basic Auth 密码（或用 passwordEnv）
//	passwordEnv  string  可选，从环境变量读密码（配置侧 ${ENV} 展开的替代）
//	fingerprint  string  etag（默认）| mtime_size
//	insecureTLS  bool    跳过 TLS 证书校验（自签场景）
package main

import (
	"os"
	"strings"

	"edge-sync/pkg/pluginkit"
	"edge-sync/pkg/protocol"
	"encoding/json"
)

const (
	pluginName    = "webdav"
	pluginVersion = "0.1.0"

	FingerprintEtag      = "etag"
	FingerprintMtimeSize = "mtime_size"
)

type webdavPlugin struct{}

var webdavConfigSchema = json.RawMessage(`{
  "type": "object",
  "required": ["url"],
  "properties": {
    "url": { "type": "string", "description": "WebDAV 目录根 URL（http/https）" },
    "username": { "type": "string", "description": "Basic Auth 用户名" },
    "password": { "type": "string", "description": "Basic Auth 密码（或用 passwordEnv）" },
    "passwordEnv": { "type": "string", "description": "从环境变量读密码" },
    "fingerprint": { "type": "string", "description": "etag（默认）| mtime_size" },
    "insecureTLS": { "type": "boolean", "description": "跳过 TLS 证书校验（自签场景）" }
  }
}`)

func (webdavPlugin) Initialize() (string, string, json.RawMessage) {
	return pluginName, pluginVersion, webdavConfigSchema
}

func (webdavPlugin) Snapshot(cfg map[string]any) (*protocol.Manifest, error) {
	opts, err := parseOptions(cfg)
	if err != nil {
		return nil, err
	}
	client, err := newDavClient(opts)
	if err != nil {
		return nil, pluginkit.SourceUnreachable("%v", err)
	}
	entries, err := client.listAll()
	if err != nil {
		return nil, err
	}

	manifest := &protocol.Manifest{}
	for _, de := range entries {
		if de.IsDir || de.RelPath == "" {
			continue
		}
		manifest.Entries = append(manifest.Entries, protocol.FileEntry{
			Path:        de.RelPath,
			Fingerprint: de.fingerprint(opts.Fingerprint),
			Size:        de.Size,
			MTime:       de.ModTimeUTC(),
		})
	}
	manifest.ManifestFingerprint = pluginkit.CombineFingerprint(manifest.Entries)
	pluginkit.Logf("snapshot: %d files, url=%s (fingerprint=%s)",
		len(manifest.Entries), opts.URL, opts.Fingerprint)
	return manifest, nil
}

func (webdavPlugin) FetchFile(cfg map[string]any, path, destPath string) (*protocol.FetchFileResult, error) {
	opts, err := parseOptions(cfg)
	if err != nil {
		return nil, err
	}
	client, err := newDavClient(opts)
	if err != nil {
		return nil, pluginkit.SourceUnreachable("%v", err)
	}
	res, err := client.fetch(path, destPath)
	if err != nil {
		return nil, err
	}
	pluginkit.Logf("fetched %s (%d bytes)", path, res.Size)
	return res, nil
}

// ---------- options ----------

type davOptions struct {
	URL         string
	Username    string
	Password    string
	Fingerprint string
	InsecureTLS bool
}

func parseOptions(cfg map[string]any) (*davOptions, error) {
	o := &davOptions{Fingerprint: FingerprintEtag}

	get := func(key string) (string, bool) {
		v, ok := cfg[key]
		if !ok {
			return "", false
		}
		s, ok := v.(string)
		return s, ok && s != ""
	}

	u, ok := get("url")
	if !ok {
		return nil, pluginkit.InvalidParams(`options.url is required`)
	}
	o.URL = u
	o.Username, _ = get("username")
	o.Password, _ = get("password")
	if envName, ok := get("passwordEnv"); ok {
		if o.Password != "" {
			return nil, pluginkit.InvalidParams(`options.password and options.passwordEnv are mutually exclusive`)
		}
		o.Password = os.Getenv(envName)
		if o.Password == "" {
			return nil, pluginkit.InvalidParams("environment variable %s is empty or unset", envName)
		}
	}
	if fp, ok := get("fingerprint"); ok {
		if fp != FingerprintEtag && fp != FingerprintMtimeSize {
			return nil, pluginkit.InvalidParams("options.fingerprint must be %q or %q, got %q",
				FingerprintEtag, FingerprintMtimeSize, fp)
		}
		o.Fingerprint = fp
	}
	if v, ok := cfg["insecureTLS"]; ok {
		if b, ok := v.(bool); ok {
			o.InsecureTLS = b
		}
	}
	if !strings.HasPrefix(o.URL, "http://") && !strings.HasPrefix(o.URL, "https://") {
		return nil, pluginkit.InvalidParams("options.url must start with http:// or https://")
	}
	return o, nil
}

func main() { pluginkit.Run(webdavPlugin{}) }
