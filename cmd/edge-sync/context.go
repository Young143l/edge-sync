// CLI 共享上下文：配置文件句柄、IPC 客户端、flag 解析。
package main

import (
	"path/filepath"
)

type context struct {
	cfg   *configFile
	flags *flagSet
}

// configFile 内核配置文件句柄（YAML 唯一真源）。
type configFile struct {
	path string
	data map[string]any
}

func loadConfig(path string) (*configFile, error) {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := readYAML(resolved)
	if err != nil {
		return nil, err
	}
	return &configFile{path: resolved, data: data}, nil
}

func (c *configFile) save() error {
	return writeYAML(c.path, c.data)
}

// storagePath 存储类路径解析（相对路径相对配置文件所在目录，与内核一致）。
func (c *configFile) storagePath(key string) string {
	storage, _ := c.data["storage"].(map[string]any)
	v, _ := storage[key].(string)
	if v == "" {
		return ""
	}
	if filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(filepath.Dir(c.path), v)
}

func (c *configFile) socketPath() string {
	server, _ := c.data["server"].(map[string]any)
	v, _ := server["socket"].(string)
	if v == "" {
		v = "var/edge-syncd.sock"
	}
	if filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(filepath.Dir(c.path), v)
}

func (c *configFile) tasks() []map[string]any {
	raw, _ := c.data["tasks"].([]any)
	var out []map[string]any
	for _, t := range raw {
		if m, ok := t.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// ---------- IPC ----------

type ipcClient struct {
	socketPath string
}

func (ctx *context) ipc() *ipcClient {
	return &ipcClient{socketPath: ctx.cfg.socketPath()}
}

// call 调内核 IPC（协议与 pkg/protocol 对齐）。
func (c *ipcClient) call(method string, params map[string]any, result any) error {
	conn, err := dialSocket(c.socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	return roundTrip(conn, method, params, result)
}

// ---------- flags ----------

type flagSet struct {
	m map[string]string
}

func (f *flagSet) Get(key string) string { return f.m[key] }

func (f *flagSet) Bool(key string) bool { return f.m[key] == "true" }

func parseFlags(args []string) (*flagSet, []string) {
	f := &flagSet{m: map[string]string{}}
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 2 && a[:2] == "--" {
			key := a[2:]
			if i+1 < len(args) && len(args[i+1]) > 0 && args[i+1][0] != '-' {
				f.m[key] = args[i+1]
				i++
			} else {
				f.m[key] = "true"
			}
			continue
		}
		positional = append(positional, a)
	}
	return f, positional
}
