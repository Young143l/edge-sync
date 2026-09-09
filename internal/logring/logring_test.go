package logring

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestTailTaskFilterExact(t *testing.T) {
	r := NewRing(10)
	logger := slog.New(NewHandler(&textWriter{}, r))
	logger.Info("a", "task", "demo")
	logger.Info("b", "task", "demo2")

	got := r.Tail(10, "demo")
	if len(got) != 1 || !strings.Contains(got[0].Attrs, "task=demo\n") &&
		!strings.HasSuffix(strings.TrimSpace(got[0].Attrs), "task=demo") {
		t.Fatalf("filter leaked demo2 into demo query: %+v", got)
	}
	for _, e := range got {
		if strings.Contains(e.Attrs, "task=demo2") {
			t.Fatalf("task=demo2 leaked: %+v", e)
		}
	}
}

type textWriter struct{}

func (textWriter) Enabled(context.Context, slog.Level) bool      { return true }
func (textWriter) Handle(_ context.Context, _ slog.Record) error { return nil }
func (textWriter) WithAttrs([]slog.Attr) slog.Handler            { return textWriter{} }
func (textWriter) WithGroup(string) slog.Handler                 { return textWriter{} }

func TestRingOverwriteOldest(t *testing.T) {
	r := NewRing(3)
	logger := slog.New(NewHandler(&textWriter{}, r))
	for i := 0; i < 5; i++ {
		logger.Info("msg", "i", i)
	}
	got := r.Tail(10, "")
	if len(got) != 3 {
		t.Fatalf("want 3 (capacity), got %d", len(got))
	}
	// 最旧的两条被覆盖，保留 i=2,3,4。
	if !strings.Contains(got[0].Attrs, "i=2") || !strings.Contains(got[2].Attrs, "i=4") {
		t.Fatalf("ring order wrong: %+v", got)
	}
}

func TestTailBoundary(t *testing.T) {
	r := NewRing(10)
	logger := slog.New(NewHandler(&textWriter{}, r))
	logger.Info("x", "task", "t1")
	logger.Info("y") // 无 task attr
	if got := r.Tail(10, "t1"); len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got := r.Tail(0, ""); len(got) != 2 {
		t.Fatalf("Tail(0) should mean all, got %d", len(got))
	}
}
