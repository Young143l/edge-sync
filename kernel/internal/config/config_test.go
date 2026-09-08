package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const validYAML = `
storage:
  dataDir: /tmp/data
tasks:
  - name: t1
    plugin: local
    interval: 5m
    options:
      root: /tmp/src
    retention:
      keepLast: 3
  - name: t2
    plugin: git
`

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(writeCfg(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.StateDir != "var/state" {
		t.Errorf("stateDir default = %q", cfg.Storage.StateDir)
	}
	if cfg.Storage.PluginBinDir != "bin" {
		t.Errorf("pluginBinDir default = %q", cfg.Storage.PluginBinDir)
	}
	t2 := cfg.Tasks[1]
	if t2.Interval.Duration != DefaultInterval {
		t.Errorf("interval default = %s", t2.Interval)
	}
	if t2.Retention.KeepLast != DefaultKeepLast {
		t.Errorf("keepLast default = %d, want %d", t2.Retention.KeepLast, DefaultKeepLast)
	}
	if !t2.IsEnabled() {
		t.Error("task should default to enabled")
	}
	if cfg.Tasks[0].Retention.KeepLast != 3 {
		t.Errorf("explicit keepLast = %d", cfg.Tasks[0].Retention.KeepLast)
	}
}

func TestLoadEnvExpansion(t *testing.T) {
	t.Setenv("ES_TEST_TOKEN", "secret123")
	p := writeCfg(t, `
tasks:
  - name: t1
    plugin: webdav
    options:
      passwordEnv: ES_TEST_TOKEN
      note: "tok=${ES_TEST_TOKEN}"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tasks[0].Options["note"] != "tok=secret123" {
		t.Errorf("env expansion failed: %v", cfg.Tasks[0].Options["note"])
	}
}

func TestLoadEnvMissingFails(t *testing.T) {
	p := writeCfg(t, `
tasks:
  - name: t1
    plugin: webdav
    options:
      password: ${ES_TEST_MISSING_VAR}
`)
	if _, err := Load(p); err == nil {
		t.Fatal("missing env var should fail the load")
	}
}

func TestLoadValidation(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"empty name", "tasks:\n  - plugin: git\n", "name is required"},
		{"bad name", "tasks:\n  - name: '../evil'\n    plugin: git\n", "invalid name"},
		{"duplicate", "tasks:\n  - name: a\n    plugin: git\n  - name: a\n    plugin: git\n", "duplicate"},
		{"no plugin", "tasks:\n  - name: a\n", "plugin is required"},
		{"interval too small", "tasks:\n  - name: a\n    plugin: git\n    interval: 5s\n", "below minimum"},
		{"negative keepLast", "tasks:\n  - name: a\n    plugin: git\n    retention:\n      keepLast: -5\n      keepDays: 1\n", "must be >= 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(writeCfg(t, c.yaml))
			if err == nil {
				t.Fatalf("want error containing %q", c.wantErr)
			}
			if !contains(err.Error(), c.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), c.wantErr)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestDurationParsing(t *testing.T) {
	p := writeCfg(t, `
tasks:
  - name: a
    plugin: git
    interval: 90m
    snapshotTimeout: 45s
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tasks[0].Interval.Duration != 90*time.Minute {
		t.Errorf("interval = %s", cfg.Tasks[0].Interval)
	}
	if cfg.Tasks[0].SnapshotTimeout.Duration != 45*time.Second {
		t.Errorf("snapshotTimeout = %s", cfg.Tasks[0].SnapshotTimeout)
	}
}

func TestDisabledTask(t *testing.T) {
	p := writeCfg(t, `
tasks:
  - name: a
    plugin: git
    enabled: false
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tasks[0].IsEnabled() {
		t.Error("task should be disabled")
	}
}
