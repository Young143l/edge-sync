// plugin-git：Git 仓库源插件（外部进程协议实现）。
// snapshot：git ls-remote 轮询分支 head；hash 未变且缓存仓库已就绪时零 fetch 短路；
// 变更时对本地缓存仓库 fetch --depth 1 + checkout，用 git ls-files 的 blob hash 生成 Manifest。
// fetchFile：从缓存 worktree 拷贝（symlink 记录链接目标字符串）。
//
// options:
//
//	url       string  必填，仓库地址（https:// 或 git@host:path 或本地路径）
//	branch    string  可选，默认 HEAD
//	cacheDir  string  必填，缓存仓库目录（建议 var/cache/<task>，跨进程持久）
//	keyFile   string  可选，SSH 私钥路径（经 GIT_SSH_COMMAND 注入）
//	token     string  可选，HTTPS token（配置侧用 ${ENV} 展开注入，避免明文）
//	username  string  可选，HTTPS token 用户名（默认 x-access-token）
package main

import (
	"os"
	"path/filepath"
	"strings"

	"edge-sync/pkg/pluginkit"
	"edge-sync/pkg/protocol"
	"encoding/json"
)

const (
	pluginName    = "git"
	pluginVersion = "0.1.0"
)

type gitPlugin struct{}

// configSchema 任务 options 的 JSON Schema：供 CLI 向导逐字段引导、
// 面板设置页展示。面向 GitHub 私有仓库场景的字段说明写全。
var gitConfigSchema = json.RawMessage(`{
  "type": "object",
  "required": ["url", "cacheDir"],
  "properties": {
    "url": {
      "type": "string",
      "description": "仓库地址；GitHub 私有仓库用 SSH 形式 git@github.com:<user>/<repo>.git"
    },
    "branch": {
      "type": "string",
      "description": "分支名，留空 = 默认分支 (HEAD)"
    },
    "cacheDir": {
      "type": "string",
      "description": "缓存仓库目录（必填），建议 var/cache/<task>，跨同步持久"
    },
    "keyFile": {
      "type": "string",
      "description": "SSH 私钥路径（deploy key）；需无口令；首次连接自动接受 known_hosts"
    },
    "username": {
      "type": "string",
      "description": "仅 HTTPS+token 时使用，默认 x-access-token"
    },
    "token": {
      "type": "string",
      "description": "仅 HTTPS 仓库使用，建议配置里用 ${ENV} 展开避免明文"
    }
  }
}`)

func (gitPlugin) Initialize() (string, string, json.RawMessage) {
	return pluginName, pluginVersion, gitConfigSchema
}

func (gitPlugin) Snapshot(cfg map[string]any) (*protocol.Manifest, error) {
	opts, err := parseOptions(cfg)
	if err != nil {
		return nil, err
	}
	manifest, err := syncRepo(opts)
	if err != nil {
		return nil, err
	}
	pluginkit.Logf("snapshot: %d files, commit=%s, url=%s",
		len(manifest.Entries), shortHash(manifest.ManifestFingerprint), opts.URL)
	return manifest, nil
}

func (gitPlugin) FetchFile(cfg map[string]any, path, destPath string) (*protocol.FetchFileResult, error) {
	opts, err := parseOptions(cfg)
	if err != nil {
		return nil, err
	}
	src, err := pluginkit.SafeJoin(opts.cacheRepoDir(), path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return nil, pluginkit.NotFound("file %q not in cache worktree: %v", path, err)
	}
	var size int64
	var sum string
	if fi.Mode()&os.ModeSymlink != 0 {
		// symlink：记录链接目标字符串（与 git blob 语义一致）。
		target, err := os.Readlink(src)
		if err != nil {
			return nil, err
		}
		size, sum, err = pluginkit.WriteContentWithHash([]byte(target), destPath)
	} else {
		size, sum, err = pluginkit.CopyWithHash(src, destPath)
	}
	if err != nil {
		return nil, pluginkit.SourceUnreachable("copy %q: %v", path, err)
	}
	pluginkit.Logf("fetched %s (%d bytes)", path, size)
	return &protocol.FetchFileResult{Size: size, Checksum: sum}, nil
}

// ---------- options ----------

type gitOptions struct {
	URL      string
	Branch   string // 空 = HEAD
	CacheDir string
	KeyFile  string
	Token    string
	Username string
}

func (o *gitOptions) cacheRepoDir() string {
	return filepath.Join(o.CacheDir, "repo")
}

func parseOptions(cfg map[string]any) (*gitOptions, error) {
	o := &gitOptions{Username: "x-access-token"}

	get := func(key string) (string, bool) {
		v, ok := cfg[key]
		if !ok {
			return "", false
		}
		s, ok := v.(string)
		return s, ok && s != ""
	}

	var ok bool
	if o.URL, ok = get("url"); !ok {
		return nil, pluginkit.InvalidParams(`options.url is required`)
	}
	o.Branch, _ = get("branch")
	if o.CacheDir, ok = get("cacheDir"); !ok {
		return nil, pluginkit.InvalidParams(`options.cacheDir is required (e.g. var/cache/<task>)`)
	}
	o.KeyFile, _ = get("keyFile")
	o.Token, _ = get("token")
	o.Username, _ = get("username")
	if o.Token != "" && !strings.HasPrefix(o.URL, "https://") {
		return nil, pluginkit.InvalidParams(`options.token only applies to https:// URLs`)
	}
	return o, nil
}

func shortHash(h string) string {
	if len(h) > 10 {
		return h[:10]
	}
	return h
}

func main() { pluginkit.Run(gitPlugin{}) }
