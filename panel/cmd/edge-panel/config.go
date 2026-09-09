// panel.json 配置的加载与自动生成。
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

type PanelConfig struct {
	Port    int    `json:"port"`
	Bind    string `json:"bind"`
	Token   string `json:"token"`
	Socket  string `json:"socket"`
	DataDir string `json:"dataDir"`
	WebDir  string `json:"webDir,omitempty"`
}

// loadOrCreate 读取 panel.json；文件不存在或缺 token 时生成随机 token 并回写。
func loadOrCreatePanelConfig(path string) (PanelConfig, error) {
	cfg := PanelConfig{Port: 8080, Bind: "0.0.0.0"}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	changed := false
	if cfg.Token == "" {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return cfg, err
		}
		cfg.Token = hex.EncodeToString(b)
		changed = true
	}
	if cfg.Bind == "" {
		cfg.Bind = "0.0.0.0"
		changed = true
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
		changed = true
	}
	if changed || !fileExists(path) {
		raw, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
			return cfg, err
		}
		fmt.Printf("[panel] config written to %s\n", path)
	}
	return cfg, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
