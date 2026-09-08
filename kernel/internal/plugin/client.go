// Package plugin 实现内核侧的外部进程插件客户端：
// spawn 插件二进制 → initialize 握手 → snapshot / fetchFile 调用 → 退出。
// 通信为 stdin/stdout 行分隔 JSON-RPC 2.0；stdout 只走协议，stderr 采集为日志。
package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"edge-sync/pkg/protocol"
)

// HandshakeTimeout initialize 握手固定超时。
const HandshakeTimeout = 10 * time.Second

// Logger 接收插件 stderr 行。
type Logger func(line string)

// RPCError 插件返回的 JSON-RPC 错误。
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("plugin rpc error %d: %s", e.Code, e.Message)
}

// Client 单次插件会话。用完必须 Close。
type Client struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	nextID  atomic.Int64
	initRes *protocol.InitializeResult

	closeOnce sync.Once
}

// Start spawn 插件并完成 initialize 握手。
func Start(ctx context.Context, binPath string, stderrLogger Logger) (*Client, error) {
	cmd := exec.Command(binPath)
	// 进程退出后若子进程仍持有 stdout 写端（如被遗弃的 sleep），
	// 最多再等 WaitDelay 就强制关闭管道，避免读端永久阻塞。
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn plugin %s: %w", binPath, err)
	}

	name := "plugin"
	if stderrLogger != nil {
		go func() {
			sc := bufio.NewScanner(stderr)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				stderrLogger(sc.Text())
			}
		}()
	}

	c := &Client{
		name:   name,
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReaderSize(stdout, 1024*1024),
	}

	// 握手（固定超时，不随调用超时配置走）。
	hsCtx, cancel := context.WithTimeout(ctx, HandshakeTimeout)
	defer cancel()
	var res protocol.InitializeResult
	if err := c.call(hsCtx, protocol.MethodInitialize,
		protocol.InitializeParams{ProtocolVersion: protocol.ProtocolVersion}, &res); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize handshake: %w", err)
	}
	if res.Name != "" {
		c.name = res.Name
	}
	c.initRes = &res
	return c, nil
}

// InitResult 返回握手结果（插件名 / 版本 / configSchema）。
func (c *Client) InitResult() *protocol.InitializeResult { return c.initRes }

// Snapshot 调用 snapshot 获取远端清单。
func (c *Client) Snapshot(ctx context.Context, options map[string]any) (*protocol.Manifest, error) {
	cfg, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	var res protocol.Manifest
	err = c.call(ctx, protocol.MethodSnapshot, protocol.SnapshotParams{Config: cfg}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// FetchFile 调用 fetchFile，插件把文件写入 destPath。
func (c *Client) FetchFile(ctx context.Context, options map[string]any, path, destPath string) (*protocol.FetchFileResult, error) {
	cfg, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	var res protocol.FetchFileResult
	err = c.call(ctx, protocol.MethodFetchFile,
		protocol.FetchFileParams{Config: cfg, Path: path, DestPath: destPath}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// Close 结束会话：关 stdin 让插件自然退出，5s 不退则 kill。
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.stdin.Close()
		done := make(chan struct{})
		go func() { _ = c.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = c.cmd.Process.Kill()
			<-done
		}
	})
}

// call 发送请求并等待响应。ctx 超时/取消会 kill 插件进程，
// 使卡在 Read 上的读取 goroutine 退出。
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	id := c.nextID.Add(1)
	rawParams, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}
	line, err := json.Marshal(protocol.Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	})
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	type readRes struct {
		line string
		err  error
	}
	ch := make(chan readRes, 1)
	go func() {
		l, rerr := c.stdout.ReadString('\n')
		ch <- readRes{line: l, err: rerr}
	}()

	var respLine string
	select {
	case <-ctx.Done():
		// 直接 kill 并返回；不等待读 goroutine —— 被杀进程的子进程可能
		// 仍持有 stdout 写端，EOF 要到它们退出才出现（如 sleep）。
		// 读 goroutine 会在 EOF / Close 强关管道后自行退出，channel 有缓冲不泄漏。
		_ = c.cmd.Process.Kill()
		return fmt.Errorf("call %s: %w", method, ctx.Err())
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("call %s: read response: %w", method, r.err)
		}
		respLine = r.line
	}

	var resp protocol.Response
	if err := json.Unmarshal([]byte(respLine), &resp); err != nil {
		_ = c.cmd.Process.Kill()
		return fmt.Errorf("call %s: decode response %q: %w", method, respLine, err)
	}
	if resp.ID != id {
		_ = c.cmd.Process.Kill()
		return fmt.Errorf("call %s: response id %d != request id %d", method, resp.ID, id)
	}
	if resp.Error != nil {
		return &RPCError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	if result == nil || len(resp.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Result, result); err != nil {
		return fmt.Errorf("call %s: decode result: %w", method, err)
	}
	return nil
}
