// 输出格式化 helper（对齐 TS 版 format.ts）。
package main

import (
	"fmt"
	"strings"
	"time"
)

func pad(s string, n int) string {
	w := utf8Len(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// utf8Len 按可见宽度近似（中日韩字符按 2 计）。
func utf8Len(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r >= 0x1100 && (r <= 0x115F || r == 0x2329 || r == 0x232A ||
			(r >= 0x2E80 && r <= 0xA4CF) || (r >= 0xAC00 && r <= 0xD7A3) ||
			(r >= 0xF900 && r <= 0xFAFF) || (r >= 0xFE30 && r <= 0xFE4F) ||
			(r >= 0xFF00 && r <= 0xFF60) || (r >= 0xFFE0 && r <= 0xFFE6) ||
			(r >= 0x20000 && r <= 0x3FFFD)):
			w += 2
		default:
			w++
		}
	}
	return w
}

func hr(width int) string {
	return strings.Repeat("-", width)
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	v := float64(n)
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v >= 100 || i == 0 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func timeAgo(iso string) string {
	if iso == "" {
		return "从未"
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d 秒前", max(1, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}

func shortTime(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Format("01-02 15:04:05")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
