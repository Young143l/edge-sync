package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"edge-sync/pkg/pluginkit"
	"edge-sync/pkg/protocol"
)

// initRepo 建本地 git 仓库并提交 files，返回仓库路径与 commit hash。
func initRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	repo := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	for p, c := range files {
		full := filepath.Join(repo, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	out := run("rev-parse", "HEAD")
	return repo, strings.TrimSpace(out)
}

func gitOpts(repo, cacheDir string) map[string]any {
	return map[string]any{
		"url":      repo,
		"cacheDir": cacheDir,
	}
}

func TestGitSnapshotAndFetch(t *testing.T) {
	repo, head := initRepo(t, map[string]string{
		"a.txt":     "hello",
		"sub/b.txt": "bee",
	})
	cache := t.TempDir()
	cfg := gitOpts(repo, cache)

	p := gitPlugin{}
	m, err := p.Snapshot(cfg)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if m.ManifestFingerprint != head {
		t.Fatalf("manifestFingerprint = %s, want commit %s", m.ManifestFingerprint, head)
	}
	if len(m.Entries) != 2 {
		t.Fatalf("want 2 entries, got %+v", m.Entries)
	}
	for _, e := range m.Entries {
		if !strings.HasPrefix(e.Fingerprint, "") || len(e.Fingerprint) != 40 {
			t.Errorf("blob hash format unexpected: %s (%s)", e.Fingerprint, e.Path)
		}
	}

	// fetchFile 内容一致。
	dest := filepath.Join(t.TempDir(), "a.txt")
	res, err := p.FetchFile(cfg, "a.txt", dest)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "hello" {
		t.Fatalf("content = %q", data)
	}
	if res.Size != 5 {
		t.Fatalf("size = %d", res.Size)
	}
}

func TestGitDetectsNewCommit(t *testing.T) {
	repo, head1 := initRepo(t, map[string]string{"a.txt": "v1", "keep.txt": "K"})
	cache := t.TempDir()
	cfg := gitOpts(repo, cache)
	p := gitPlugin{}

	m1, err := p.Snapshot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m1.ManifestFingerprint != head1 {
		t.Fatalf("fp = %s want %s", m1.ManifestFingerprint, head1)
	}

	// 第二次 commit：改一个文件，加一个文件。
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v2-longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("N"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "second")
	out := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	head2, err := out.Output()
	if err != nil {
		t.Fatal(err)
	}

	m2, err := p.Snapshot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m2.ManifestFingerprint != strings.TrimSpace(string(head2)) {
		t.Fatalf("fp = %s want %s", m2.ManifestFingerprint, head2)
	}
	if len(m2.Entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(m2.Entries))
	}
	// keep.txt blob hash 跨 commit 不变。
	fpKeep1 := findEntry(m1, "keep.txt").Fingerprint
	fpKeep2 := findEntry(m2, "keep.txt").Fingerprint
	if fpKeep1 != fpKeep2 {
		t.Error("keep.txt blob hash should be identical across commits")
	}
	if findEntry(m1, "a.txt").Fingerprint == findEntry(m2, "a.txt").Fingerprint {
		t.Error("a.txt blob hash should change")
	}
}

func findEntry(m *protocol.Manifest, path string) *protocol.FileEntry {
	for i := range m.Entries {
		if m.Entries[i].Path == path {
			return &m.Entries[i]
		}
	}
	return nil
}

func TestGitMissingBranch(t *testing.T) {
	repo, _ := initRepo(t, map[string]string{"a.txt": "x"})
	cfg := gitOpts(repo, t.TempDir())
	cfg["branch"] = "no-such-branch"
	if _, err := (gitPlugin{}).Snapshot(cfg); err == nil {
		t.Fatal("missing branch should fail")
	}
}

func TestGitMissingCacheDirFails(t *testing.T) {
	repo, _ := initRepo(t, map[string]string{"a.txt": "x"})
	cfg := map[string]any{"url": repo}
	if _, err := (gitPlugin{}).Snapshot(cfg); err == nil {
		t.Fatal("missing cacheDir should fail")
	}
}

func TestBuildAuthURL(t *testing.T) {
	cases := []struct{ url, user, token, want string }{
		{"https://github.com/a/b.git", "", "tok", "https://x-access-token:tok@github.com/a/b.git"},
		{"https://github.com/a/b.git", "oauth2", "tok", "https://oauth2:tok@github.com/a/b.git"},
		{"https://github.com/a/b.git", "", "", "https://github.com/a/b.git"},
		{"git@github.com:a/b.git", "", "tok", "git@github.com:a/b.git"},
		{"http://x/a.git", "", "tok", "http://x/a.git"},
		{"/local/path", "", "tok", "/local/path"},
	}
	for _, c := range cases {
		got := buildAuthURL(c.url, c.user, c.token)
		if got != c.want {
			t.Errorf("buildAuthURL(%q,%q,%q) = %q, want %q", c.url, c.user, c.token, got, c.want)
		}
	}
}

func TestGitLocalPathTokenRejected(t *testing.T) {
	repo, _ := initRepo(t, map[string]string{"a.txt": "x"})
	cfg := gitOpts(repo, t.TempDir())
	cfg["token"] = "tok"
	if _, err := (gitPlugin{}).Snapshot(cfg); err == nil {
		t.Fatal("token with non-https url should fail validation")
	}
}

func TestGitSshCommandConstruction(t *testing.T) {
	opts := &gitOptions{
		URL:      "git@github.com:me/private.git",
		CacheDir: "/tmp/cache",
		KeyFile:  "/opt/keys/gh_deploy",
	}
	env := gitEnv(opts)
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			found = true
			want := "GIT_SSH_COMMAND=ssh -i /opt/keys/gh_deploy -o BatchMode=yes -o StrictHostKeyChecking=accept-new"
			if e != want {
				t.Fatalf("GIT_SSH_COMMAND = %q, want %q", e, want)
			}
		}
	}
	if !found {
		t.Fatal("GIT_SSH_COMMAND not injected")
	}
	// keyFile 转为绝对路径断言：相对路径也要正确拼接。
	opts2 := &gitOptions{KeyFile: "rel/key", CacheDir: "/tmp/cache"}
	env2 := gitEnv(opts2)
	for _, e := range env2 {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") && !strings.Contains(e, "/rel/key") {
			t.Fatalf("relative keyFile should be absolutized: %q", e)
		}
	}
}

func TestGitKeyFileMissingFailsFast(t *testing.T) {
	repo, _ := initRepo(t, map[string]string{"a.txt": "x"})
	cfg := gitOpts(repo, t.TempDir())
	cfg["keyFile"] = filepath.Join(t.TempDir(), "no-such-key")
	_, err := (gitPlugin{}).Snapshot(cfg)
	if err == nil {
		t.Fatal("missing keyFile should fail before spawning git")
	}
	var pe *pluginkit.Error
	if !errors.As(err, &pe) || pe.Code != protocol.CodeAuthFailed {
		t.Fatalf("want AuthFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), "no-such-key") {
		t.Fatalf("error should mention the key path: %v", err)
	}
}

func TestBuildAuthURLSshPortForm(t *testing.T) {
	// ssh:// 自定义端口 URL：token 不适用，原样返回。
	got := buildAuthURL("ssh://git@github.com:2222/me/private.git", "", "tok")
	if got != "ssh://git@github.com:2222/me/private.git" {
		t.Fatalf("got %q", got)
	}
}

func TestGitConfigSchemaExposed(t *testing.T) {
	// initialize 应返回含 url/keyFile 字段的 configSchema（CLI 向导依赖）。
	name, version, raw := (gitPlugin{}).Initialize()
	if name == "" || version == "" {
		t.Fatal("initialize must return name/version")
	}
	if raw == nil {
		t.Fatal("initialize should return configSchema")
	}
	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	for _, field := range []string{"url", "cacheDir", "keyFile", "branch", "token"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("schema missing field %q", field)
		}
	}
	if len(schema.Required) == 0 {
		t.Error("schema should have required fields")
	}
}
