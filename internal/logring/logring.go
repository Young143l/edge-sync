// Package logring 提供内存环形日志缓冲：供 IPC log.tail 查询最近日志，
// 同时把日志转发给底层 handler（stderr / journald）。
package logring

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Entry 单条日志。
type Entry struct {
	Time  time.Time
	Level string
	Msg   string
	Attrs string // key=value 空格连接（含 task 等）
}

// Ring 固定容量环形缓冲。
type Ring struct {
	mu    sync.Mutex
	buf   []Entry
	head  int
	count int
}

func NewRing(capacity int) *Ring {
	return &Ring{buf: make([]Entry, capacity)}
}

func (r *Ring) Add(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := (r.head + r.count) % len(r.buf)
	if r.count == len(r.buf) {
		r.buf[r.head] = e // 覆盖最旧
		r.head = (r.head + 1) % len(r.buf)
	} else {
		r.buf[idx] = e
		r.count++
	}
}

// Tail 返回最近 n 条（旧→新），task 非空时按其 attrs 过滤。
func (r *Ring) Tail(n int, task string) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > r.count {
		n = r.count
	}
	out := make([]Entry, 0, n)
	for i := r.count - n; i < r.count; i++ {
		e := r.buf[(r.head+i)%len(r.buf)]
		if task != "" && !hasTask(e.Attrs, task) {
			continue // 精确匹配：task=demo 不应误中 task=demo2
		}
		out = append(out, e)
	}
	return out
}

// hasTask 按 token 精确匹配 attrs 中的 task 键（避免 demo 误中 demo2）。
func hasTask(attrs, task string) bool {
	for _, tok := range strings.Fields(attrs) {
		if tok == "task="+task {
			return true
		}
	}
	return false
}

// Handler slog.Handler：写入环形缓冲并转发底层。
type Handler struct {
	next  slog.Handler
	ring  *Ring
	attrs []slog.Attr
	group string
}

func NewHandler(next slog.Handler, ring *Ring) *Handler {
	return &Handler{next: next, ring: ring}
}

func (h *Handler) Enabled(context.Context, slog.Level) bool {
	// 环形缓冲收全部级别（log.tail 需要看 Info/Debug），底层 handler 自行过滤。
	return true
}

func (h *Handler) Handle(ctx context.Context, rec slog.Record) error {
	var b strings.Builder
	for _, a := range h.attrs {
		writeAttr(&b, a)
	}
	rec.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, a)
		return true
	})
	h.ring.Add(Entry{
		Time:  rec.Time,
		Level: rec.Level.String(),
		Msg:   rec.Message,
		Attrs: strings.TrimSpace(b.String()),
	})
	return h.next.Handle(ctx, rec)
}

func writeAttr(b *strings.Builder, a slog.Attr) {
	v := a.Value.Resolve()
	s := v.String()
	if v.Kind() == slog.KindString {
		s = v.String()
	}
	fmt.Fprintf(b, "%s=%s ", a.Key, s)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{next: h.next.WithAttrs(attrs), ring: h.ring, attrs: append(append([]slog.Attr{}, h.attrs...), attrs...), group: h.group}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name), ring: h.ring, attrs: h.attrs, group: name}
}
