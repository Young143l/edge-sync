// IPC 行协议与 YAML 读写（CLI 侧）。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"edge-sync/pkg/protocol"
)

// ---------- IPC（unix socket 行分隔 JSON-RPC） ----------

type ipcError struct {
	Code int
	Msg  string
}

func (e *ipcError) Error() string { return e.Msg }

func dialSocket(path string) (net.Conn, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("无法连接内核（edge-syncd 未运行？）: %w", err)
	}
	// 同步触发最长等待 30 分钟（大文件同步）。
	_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
	return conn, nil
}

func roundTrip(conn net.Conn, method string, params map[string]any, result any) error {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := json.Marshal(protocol.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  rawParams,
	})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return fmt.Errorf("响应解析失败: %w", err)
	}
	if resp.Error != nil {
		return &ipcError{Code: resp.Error.Code, Msg: resp.Error.Message}
	}
	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("结果解析失败: %w", err)
		}
	}
	return nil
}

// ---------- YAML ----------

func readYAML(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败（-c 指定路径）: %w", err)
	}
	var data map[string]any
	if err := yaml.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	return data, nil
}

func writeYAML(path string, data map[string]any) error {
	raw, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
