// git 操作：ls-remote 轮询、缓存仓库 init/fetch（--depth 1，token 不落盘）、
// ls-files 生成 Manifest、错误分类（认证失败 vs 源不可达）。
package main

import (
	"bytes"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"edge-sync/pkg/pluginkit"
	"edge-sync/pkg/protocol"
)

// buildAuthURL 把 HTTPS token 插入 URL userinfo；其余 URL 原样返回。
// token 只出现在进程命令行（瞬时），不写入 .git/config。
func buildAuthURL(rawURL, username, token string) string {
	if token == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return rawURL
	}
	user := username
	if user == "" {
		user = "x-access-token"
	}
	u.User = url.UserPassword(user, token)
	return u.String()
}

// validateKeyFile 前置校验 SSH 私钥：不存在时给明确报错（而非 ssh 的模糊输出）。
// 注意：deploy key 需无口令（BatchMode 下无法交互输入 passphrase）。
func validateKeyFile(opts *gitOptions) error {
	if opts.KeyFile == "" {
		return nil
	}
	keyAbs, err := filepath.Abs(opts.KeyFile)
	if err != nil {
		return pluginkit.AuthFailed("keyFile %q: %v", opts.KeyFile, err)
	}
	if fi, err := os.Stat(keyAbs); err != nil || fi.IsDir() {
		return pluginkit.AuthFailed(
			"SSH key file not found: %s （请确认路径，deploy key 私钥需无口令）", keyAbs)
	}
	return nil
}

// gitEnv 构造 git 子进程环境（注入 SSH 私钥）。
func gitEnv(opts *gitOptions) []string {
	env := os.Environ()
	if opts.KeyFile != "" {
		keyAbs, err := filepath.Abs(opts.KeyFile)
		if err == nil {
			env = append(env, "GIT_SSH_COMMAND=ssh -i "+keyAbs+
				" -o BatchMode=yes -o StrictHostKeyChecking=accept-new")
		}
	}
	return env
}

// runGit 执行 git 命令，返回 stdout；stderr 保留用于错误分类。
func runGit(dir string, env []string, args ...string) (string, string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// classifyGitErr 按输出把 git 错误分为认证失败与源不可达。
func classifyGitErr(op string, stderr string, err error) error {
	msg := stderr
	if msg == "" {
		msg = err.Error()
	}
	lower := strings.ToLower(msg)
	authMarkers := []string{
		"authentication failed", "permission denied", "could not read username",
		"403", "401", "invalid credentials", "access denied", "publickey",
	}
	for _, m := range authMarkers {
		if strings.Contains(lower, m) {
			return pluginkit.AuthFailed("%s: %s", op, strings.TrimSpace(firstLines(msg, 3)))
		}
	}
	return pluginkit.SourceUnreachable("%s: %s", op, strings.TrimSpace(firstLines(msg, 3)))
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "; ")
}

// refSpec 返回 ls-remote / fetch 的引用名。
func (o *gitOptions) refSpec() string {
	if o.Branch == "" {
		return "HEAD"
	}
	return "refs/heads/" + o.Branch
}

// lsRemote 拿分支 head commit hash。
func lsRemote(opts *gitOptions) (string, error) {
	authURL := buildAuthURL(opts.URL, opts.Username, opts.Token)
	stdout, stderr, err := runGit("", gitEnv(opts), "ls-remote", authURL, opts.refSpec())
	if err != nil {
		return "", classifyGitErr("ls-remote", stderr, err)
	}
	line := strings.TrimSpace(stdout)
	if line == "" {
		return "", pluginkit.NotFound("ref %s not found in %s", opts.refSpec(), opts.URL)
	}
	hash := strings.Fields(line)[0]
	if len(hash) < 8 {
		return "", pluginkit.SourceUnreachable("ls-remote: unexpected output %q", line)
	}
	return hash, nil
}

// syncRepo 确保缓存仓库与远端 head 一致并生成 Manifest。
func syncRepo(opts *gitOptions) (*protocol.Manifest, error) {
	if err := validateKeyFile(opts); err != nil {
		return nil, err
	}
	repoDir := opts.cacheRepoDir()
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return nil, err
	}
	env := gitEnv(opts)

	head, err := lsRemote(opts)
	if err != nil {
		return nil, err
	}

	// 缓存已就绪且 head 未变 → 零 fetch 短路。
	if current := headOfCache(repoDir, env); current != "" && current == head {
		return buildManifestFromWorktree(repoDir, head)
	}

	// 缓存仓库初始化（幂等；token 只经命令行传递不落盘）。
	if _, statErr := os.Stat(filepath.Join(repoDir, ".git")); statErr != nil {
		if _, stderr, err := runGit(repoDir, env, "init"); err != nil {
			return nil, classifyGitErr("init", stderr, err)
		}
		// fetch 显式传 authURL；remote 的 URL 若设置则用裸 URL。
		bareURL := opts.URL
		if _, stderr, err := runGit(repoDir, env, "remote", "add", "origin", bareURL); err != nil {
			return nil, classifyGitErr("remote add", stderr, err)
		}
	}

	authURL := buildAuthURL(opts.URL, opts.Username, opts.Token)
	start := time.Now()
	if _, stderr, err := runGit(repoDir, env, "fetch", "--depth", "1", "--force", authURL, opts.refSpec()); err != nil {
		return nil, classifyGitErr("fetch", stderr, err)
	}
	if _, stderr, err := runGit(repoDir, env, "checkout", "--force", "FETCH_HEAD"); err != nil {
		return nil, classifyGitErr("checkout", stderr, err)
	}
	if _, stderr, err := runGit(repoDir, env, "clean", "-fd"); err != nil {
		pluginkit.Logf("clean warning: %s", strings.TrimSpace(stderr))
	}
	pluginkit.Logf("fetched %s in %s", opts.refSpec(), time.Since(start).Round(time.Millisecond))

	return buildManifestFromWorktree(repoDir, head)
}

// headOfCache 返回缓存仓库当前 HEAD；空仓库/无缓存返回空串。
func headOfCache(repoDir string, env []string) string {
	stdout, _, err := runGit(repoDir, env, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stdout)
}

// buildManifestFromWorktree 用 git ls-files 的 blob hash + worktree stat 生成 Manifest。
func buildManifestFromWorktree(repoDir, commitHash string) (*protocol.Manifest, error) {
	env := os.Environ()
	stdout, stderr, err := runGit(repoDir, env, "ls-files", "-s", "-z")
	if err != nil {
		return nil, classifyGitErr("ls-files", stderr, err)
	}
	manifest := &protocol.Manifest{ManifestFingerprint: commitHash}

	entries := strings.Split(stdout, "\x00")
	for _, line := range entries {
		if line == "" {
			continue
		}
		// 格式：<mode> <blobhash> <stage>\t<path>
		meta, path, found := strings.Cut(line, "\t")
		if !found {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) < 2 {
			continue
		}
		blobHash := fields[1]

		full := filepath.Join(repoDir, filepath.FromSlash(path))
		fi, err := os.Lstat(full)
		if err != nil {
			continue // worktree 与 index 短暂不一致，跳过
		}
		entry := protocol.FileEntry{
			Path:        filepath.ToSlash(path),
			Fingerprint: blobHash,
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			entry.Fingerprint = blobHash // symlink 用 blob hash（内容为目标路径字符串）
		} else if fi.Mode().IsRegular() {
			entry.Size = fi.Size()
			entry.MTime = fi.ModTime().UTC().Format(time.RFC3339)
		} else {
			continue // git 不管理的特殊文件（submodule 等），跳过
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	return manifest, nil
}
