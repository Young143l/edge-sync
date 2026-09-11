// 内核 IPC 客户端：unix socket 行分隔 JSON-RPC（每次调用一条连接）。
// 与 kernel/internal/ipc 服务端及 TS 侧 ipc.ts 的语义一致。
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"edge-sync/pkg/protocol"
)

const (
	cookieName = "edge-sync-token"
)

// IpcError 内核返回的 JSON-RPC 错误。
type IpcError struct {
	Code int
	Msg  string
}

func (e *IpcError) Error() string { return e.Msg }

// IpcConnectError 连接类错误（socket 不存在/不可达）→ HTTP 503。
type IpcConnectError struct{ Msg string }

func (e *IpcConnectError) Error() string { return e.Msg }

// IPCClient 对内核 unix socket 的调用客户端。
type IPCClient struct {
	SocketPath string
}

func (c *IPCClient) call(ctx context.Context, method string, params any, result any) error {
	conn, err := net.Dial("unix", c.SocketPath)
	if err != nil {
		return &IpcConnectError{Msg: fmt.Sprintf("IPC %s: %v", method, err)}
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		// 默认 30 分钟（覆盖大文件同步的 task.trigger 等待）。
		_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
	}

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
		return &IpcConnectError{Msg: fmt.Sprintf("IPC %s: write: %v", method, err)}
	}

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return &IpcConnectError{Msg: fmt.Sprintf("IPC %s: read response: %v", method, err)}
	}
	var resp protocol.Response
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return &IpcError{Code: protocol.CodeInternalError, Msg: fmt.Sprintf("decode response: %v", err)}
	}
	if resp.Error != nil {
		return &IpcError{Code: resp.Error.Code, Msg: resp.Error.Message}
	}
	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return &IpcError{Code: protocol.CodeInternalError, Msg: fmt.Sprintf("decode result: %v", err)}
		}
	}
	return nil
}

// 便捷方法：params 为 map 直接调用（错误统一从 call 返回）。
func (c *IPCClient) list(ctx context.Context) ([]protocol.TaskSummary, error) {
	var out []protocol.TaskSummary
	err := c.call(ctx, "task.list", map[string]any{}, &out)
	return out, err
}

func (c *IPCClient) status(ctx context.Context, name string) (protocol.TaskDetail, error) {
	var out protocol.TaskDetail
	err := c.call(ctx, "task.status", map[string]any{"name": name}, &out)
	return out, err
}

func (c *IPCClient) shutdown(ctx context.Context) error {
	var out struct {
		OK bool `json:"ok"`
	}
	return c.call(ctx, "sys.shutdown", map[string]any{}, &out)
}

func (c *IPCClient) trigger(ctx context.Context, name string) error {
	return c.call(ctx, "task.trigger", map[string]any{"name": name}, nil)
}
