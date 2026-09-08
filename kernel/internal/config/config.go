// Package config 负责内核配置的加载、${ENV} 展开、默认值填充与校验。
// YAML 文件是唯一配置真源；SIGHUP / IPC 触发重载时重新调用 Load。
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration 支持 "300s" / "1h" 形式的 YAML 字符串时长。
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"300s\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if v <= 0 {
		return fmt.Errorf("duration must be positive, got %q", s)
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.Duration.String(), nil }

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Storage StorageConfig `yaml:"storage"`
	Log     LogConfig     `yaml:"log"`
	Tasks   []*Task       `yaml:"tasks"`
}

type ServerConfig struct {
	Socket string `yaml:"socket"`
}

type StorageConfig struct {
	DataDir      string `yaml:"dataDir"`
	StateDir     string `yaml:"stateDir"`
	PluginBinDir string `yaml:"pluginBinDir"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

// Retention 版本保留策略。
// KeepLast=0 表示不按数量限制（仅 keepDays 生效）；
// KeepDays=0 表示不按时间限制（仅 keepLast 生效）。
type Retention struct {
	KeepLast int `yaml:"keepLast"`
	KeepDays int `yaml:"keepDays"`
}

type Task struct {
	Name            string         `yaml:"name"`
	Plugin          string         `yaml:"plugin"`
	Enabled         *bool          `yaml:"enabled"` // 缺省 true
	Interval        Duration       `yaml:"interval"`
	SnapshotTimeout Duration       `yaml:"snapshotTimeout"`
	FetchTimeout    Duration       `yaml:"fetchTimeout"`
	Options         map[string]any `yaml:"options"`
	Retention       Retention      `yaml:"retention"`
}

func (t *Task) IsEnabled() bool { return t.Enabled == nil || *t.Enabled }

const (
	DefaultInterval        = 10 * time.Minute
	MinInterval            = time.Minute
	DefaultSnapshotTimeout = 60 * time.Second
	DefaultFetchTimeout    = 5 * time.Minute
	DefaultKeepLast        = 10
)

// 任务名会进入文件路径（data/<task>/），必须限制字符集防路径注入。
var taskNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

var envRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv 展开配置文本中的 ${VAR}；变量未设置时直接报错，
// 避免把空凭证静默写入配置（fail-fast）。
func expandEnv(data []byte) ([]byte, error) {
	var missing []string
	out := envRe.ReplaceAllFunc(data, func(m []byte) []byte {
		name := string(envRe.FindSubmatch(m)[1])
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return m
		}
		return []byte(v)
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("environment variables not set: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// Load 读取、展开、解析、补默认值并校验配置。
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	raw, err = expandEnv(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Socket == "" {
		cfg.Server.Socket = "var/edge-syncd.sock"
	}
	if cfg.Storage.DataDir == "" {
		cfg.Storage.DataDir = "data"
	}
	if cfg.Storage.StateDir == "" {
		cfg.Storage.StateDir = "var/state"
	}
	if cfg.Storage.PluginBinDir == "" {
		cfg.Storage.PluginBinDir = "bin"
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	for _, t := range cfg.Tasks {
		if t.Interval.Duration == 0 {
			t.Interval.Duration = DefaultInterval
		}
		if t.SnapshotTimeout.Duration == 0 {
			t.SnapshotTimeout.Duration = DefaultSnapshotTimeout
		}
		if t.FetchTimeout.Duration == 0 {
			t.FetchTimeout.Duration = DefaultFetchTimeout
		}
		if t.Retention.KeepLast == 0 && t.Retention.KeepDays == 0 {
			// 用户未配置任何保留策略时给默认数量限制；显式想全保留
			// 可配 keepLast: -1（validate 会拒绝负数，故用 0/0 表示
			// "未配置"并由这里兜底；显式关闭数量限制需同时给出 keepDays）。
			t.Retention.KeepLast = DefaultKeepLast
		}
	}
}

func validate(cfg *Config) error {
	seen := map[string]bool{}
	for i, t := range cfg.Tasks {
		if t.Name == "" {
			return fmt.Errorf("tasks[%d]: name is required", i)
		}
		if !taskNameRe.MatchString(t.Name) {
			return fmt.Errorf("tasks[%d]: invalid name %q (allowed: letters, digits, '.', '_', '-')", i, t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("tasks[%d]: duplicate name %q", i, t.Name)
		}
		seen[t.Name] = true
		if t.Plugin == "" {
			return fmt.Errorf("task %q: plugin is required", t.Name)
		}
		if t.Interval.Duration < MinInterval {
			return fmt.Errorf("task %q: interval %s is below minimum %s", t.Name, t.Interval, MinInterval)
		}
		if t.Retention.KeepLast < 0 || t.Retention.KeepDays < 0 {
			return fmt.Errorf("task %q: retention values must be >= 0", t.Name)
		}
	}
	return nil
}
