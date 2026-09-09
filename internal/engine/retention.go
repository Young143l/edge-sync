// retention：版本保留清理。
// 语义（与 DESIGN 4.4 一致）：删除条件 = 超过 keepDays OR 不在最新 keepLast 内；
// current 指向的版本始终保留且计入 keepLast 预算。
// keepLast=0 表示不限数量（仅 keepDays 生效）；keepDays=0 表示不限时间（仅 keepLast 生效）。
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

var versionNameRe = regexp.MustCompile(`^\d{8}-\d{6}(-\d+)?$`)

// ParseVersionTime 从版本目录名解析 UTC 时间（"20060102-150405(-N)"）。
func ParseVersionTime(name string) (time.Time, bool) {
	if len(name) < len(versionTimeLayout) {
		return time.Time{}, false
	}
	t, err := time.Parse(versionTimeLayout, name[:len(versionTimeLayout)])
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// ListVersions 返回 versionsDir 下合法版本目录名，按时间升序（字典序即时间序）。
func ListVersions(versionsDir string) ([]string, error) {
	ents, err := os.ReadDir(versionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() && versionNameRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Cleanup 执行保留清理，返回被删除的版本名（按从新到旧的删除顺序）。
// currentBase 为 current 指向的版本目录名（basename），始终保留：
// current 占时间排名（计入 keepLast 预算），但不参与删除判定；
// 即 current 保护优先于保留策略（current 异常老旧时不删它，可能多保留一个版本）。
func Cleanup(versionsDir, currentBase string, keepLast, keepDays int, now time.Time) ([]string, error) {
	names, err := ListVersions(versionsDir)
	if err != nil {
		return nil, err
	}
	var removed []string
	rank := 0 // 时间排名，1 = 最新（current 也计入）
	for i := len(names) - 1; i >= 0; i-- {
		name := names[i]
		rank++
		if name == currentBase {
			continue
		}
		if keepDays > 0 {
			if t, ok := ParseVersionTime(name); ok && now.Sub(t) > time.Duration(keepDays)*24*time.Hour {
				if err := os.RemoveAll(filepath.Join(versionsDir, name)); err != nil {
					return removed, fmt.Errorf("remove expired version %s: %w", name, err)
				}
				removed = append(removed, name)
				continue
			}
		}
		if keepLast > 0 && rank > keepLast {
			if err := os.RemoveAll(filepath.Join(versionsDir, name)); err != nil {
				return removed, fmt.Errorf("remove excess version %s: %w", name, err)
			}
			removed = append(removed, name)
			continue
		}
	}
	return removed, nil
}

// VersionExists 返回版本目录是否存在（诊断用）。
func VersionExists(versionsDir, name string) bool {
	_, err := os.Stat(filepath.Join(versionsDir, name))
	return err == nil
}
