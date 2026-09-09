// 全部 CLI 命令实现（对齐 TS 版 commands.ts）。
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"edge-sync/pkg/protocol"
)

type ctx = *context

func needTask(name, usage string) (string, error) {
	if name == "" {
		return "", errors.New(usage)
	}
	return name, nil
}

func cmdList(ctx ctx) error {
	var tasks []protocol.TaskSummary
	if err := ctx.ipc().call("task.list", map[string]any{}, &tasks); err != nil {
		return err
	}
	fmt.Println(pad("NAME", 16), pad("STATUS", 24), pad("PLUGIN", 10), pad("INTERVAL", 10), "LAST SYNC")
	fmt.Println(hr(72))
	for _, t := range tasks {
		status := t.Status
		if !t.Enabled {
			status = "paused"
		} else if t.ConsecutiveFailures > 0 {
			status = fmt.Sprintf("%s (%d fails)", status, t.ConsecutiveFailures)
		}
		fmt.Println(pad(t.Name, 16), pad(status, 24), pad(t.Plugin, 10), pad(t.Interval, 10), timeAgo(t.LastSuccessAt))
	}
	fmt.Printf("\n%d task(s)\n", len(tasks))
	return nil
}

func cmdStatus(ctx ctx, name string) error {
	if name == "" {
		return cmdList(ctx)
	}
	var d protocol.TaskDetail
	if err := ctx.ipc().call("task.status", map[string]any{"name": name}, &d); err != nil {
		return err
	}
	fmt.Printf("task:      %s\n", d.Name)
	fmt.Printf("plugin:    %s\n", d.Plugin)
	fmt.Printf("status:    %s\n", statusText(&d.TaskSummary))
	fmt.Printf("interval:  %s\n", d.Interval)
	fmt.Printf("next run:  %s\n", orDash(d.NextRunAt))
	fmt.Printf("last sync: %s (success: %s)\n", orDash(d.LastSyncAt), orDash(d.LastSuccessAt))
	fmt.Printf("failures:  %d\n", d.ConsecutiveFailures)
	fmt.Printf("retention: keepLast=%d keepDays=%d\n", d.Retention.KeepLast, d.Retention.KeepDays)
	fmt.Printf("stats:     %d syncs, %d files, %s\n", d.Stats.TotalSyncs, d.Stats.TotalFiles, humanBytes(d.Stats.TotalBytes))
	return nil
}

func statusText(t *protocol.TaskSummary) string {
	if !t.Enabled {
		return "paused"
	}
	if t.Status == "syncing" {
		return "syncing"
	}
	if t.ConsecutiveFailures > 0 {
		return fmt.Sprintf("idle (%d fails)", t.ConsecutiveFailures)
	}
	return "idle"
}

func cmdSync(ctx ctx, name string) error {
	if name == "" {
		return fmt.Errorf("usage: edge-sync sync <task>")
	}
	fmt.Printf("syncing %s ...\n", name)
	var res struct {
		Triggered bool `json:"triggered"`
	}
	if err := ctx.ipc().call("task.trigger", map[string]any{"name": name}, &res); err != nil {
		return err
	}
	fmt.Printf("%s: sync completed\n", name)
	return nil
}

func cmdHistory(ctx ctx, name string) error {
	if name == "" {
		return fmt.Errorf("usage: edge-sync history <task>")
	}
	var versions []protocol.VersionInfo
	if err := ctx.ipc().call("history.list", map[string]any{"name": name}, &versions); err != nil {
		return err
	}
	fmt.Println(pad("VERSION", 20), pad("FILES", 8), pad("SIZE", 12), "NOTE")
	fmt.Println(hr(52))
	for _, v := range versions {
		note := ""
		if v.IsCurrent {
			note = "<- current"
		}
		fmt.Println(pad(v.Version, 20), pad(fmt.Sprint(v.Files), 8), pad(humanBytes(v.Bytes), 12), note)
	}
	fmt.Printf("\n%d version(s)\n", len(versions))
	return nil
}

func cmdFileVersions(ctx ctx, name, path string) error {
	if name == "" || path == "" {
		return fmt.Errorf("usage: edge-sync file-versions <task> <path>")
	}
	var versions []protocol.FileVersionEntry
	if err := ctx.ipc().call("history.fileVersions", map[string]any{"name": name, "path": path}, &versions); err != nil {
		return err
	}
	fmt.Println(pad("VERSION", 20), pad("SIZE", 12), "FINGERPRINT")
	fmt.Println(hr(52))
	for _, v := range versions {
		fp := v.Fingerprint
		if len(fp) > 14 {
			fp = fp[:14]
		}
		fmt.Println(pad(v.Version, 20), pad(humanBytes(v.Size), 12), fp)
	}
	return nil
}

func cmdLogs(ctx ctx, n int, task string) error {
	var entries []logLine
	if err := ctx.ipc().call("log.tail", map[string]any{"n": n, "task": task}, &entries); err != nil {
		return err
	}
	for _, e := range entries {
		fmt.Printf("%s %-5s %s %s\n", e.Time, strings.ToUpper(e.Level), e.Msg, e.Attrs)
	}
	return nil
}

type logLine struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Attrs string `json:"attrs,omitempty"`
}

func cmdPlugins(ctx ctx) error {
	var plugins []protocol.PluginInfo
	if err := ctx.ipc().call("plugin.list", map[string]any{}, &plugins); err != nil {
		return err
	}
	for _, p := range plugins {
		fmt.Printf("%-12s v%s\n", p.Name, p.Version)
		var schema struct {
			Properties map[string]struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"properties"`
		}
		if len(p.ConfigSchema) > 0 && json.Unmarshal(p.ConfigSchema, &schema) == nil {
			for k, v := range schema.Properties {
				fmt.Printf("  - %s (%s)%s\n", k, orAny(v.Type), orEmpty(v.Description))
			}
		}
	}
	return nil
}

func cmdReload(ctx ctx) error {
	var res struct {
		Reloaded bool `json:"reloaded"`
		Tasks    int  `json:"tasks"`
	}
	if err := ctx.ipc().call("config.reload", map[string]any{}, &res); err != nil {
		return err
	}
	fmt.Printf("config reloaded (%d task(s))\n", res.Tasks)
	return nil
}

// ---------- add 向导 ----------

func cmdAdd(ctx ctx) error {
	var plugins []protocol.PluginInfo
	if err := ctx.ipc().call("plugin.list", map[string]any{}, &plugins); err != nil {
		return err
	}
	if len(plugins) == 0 {
		return fmt.Errorf("pluginBinDir 中未发现插件")
	}
	fmt.Println("available plugins:")
	for i, p := range plugins {
		fmt.Printf("  %d. %s (v%s)\n", i+1, p.Name, p.Version)
	}

	pick := ask("plugin (number or name): ")
	var chosen *protocol.PluginInfo
	for i, p := range plugins {
		if fmt.Sprint(i+1) == pick || p.Name == pick {
			chosen = &plugins[i]
			break
		}
	}
	if chosen == nil {
		return fmt.Errorf("invalid plugin choice")
	}

	name := ask("task name (letters/digits/._-): ")
	if !validTaskName(name) {
		return fmt.Errorf("invalid task name")
	}
	intervalSec := askDefault("poll interval (seconds): ", "600")
	keepLastStr := askDefault("retention keepLast (versions to keep): ", "10")
	keepDaysStr := askDefault("retention keepDays (0 = unlimited): ", "0")
	keepLast, keepErr := strconv.Atoi(keepLastStr)
	keepDays, daysErr := strconv.Atoi(keepDaysStr)
	if keepErr != nil || daysErr != nil || keepLast < 0 || keepDays < 0 {
		return fmt.Errorf("retention 值必须是非负整数（got keepLast=%q keepDays=%q）", keepLastStr, keepDaysStr)
	}

	options := map[string]any{}
	props := schemaProps(chosen.ConfigSchema)
	if len(props) > 0 {
		fmt.Printf("plugin %s options:\n", chosen.Name)
		for k, p := range props {
			raw := ask(fmt.Sprintf("  %s [%s]%s: ", k, p.Type, maybeDesc(p.Description)))
			if raw == "" {
				continue
			}
			options[k] = coerce(raw, p.Type)
		}
	} else {
		raw := ask("options (YAML/JSON, empty = {}): ")
		if raw != "" {
			if err := unmarshalLoose(raw, &options); err != nil {
				return err
			}
		}
	}

	task := map[string]any{
		"name":      name,
		"plugin":    chosen.Name,
		"interval":  intervalSec + "s",
		"options":   options,
		"retention": map[string]any{"keepLast": keepLast, "keepDays": keepDays},
	}
	fmt.Println()
	printYAMLTask(task)
	if !strings.EqualFold(ask("write this task to config? (y/N): "), "y") {
		fmt.Println("aborted")
		return nil
	}
	tasks := ctx.cfg.tasks()
	var kept []map[string]any
	for _, t := range tasks {
		if t["name"] != name {
			kept = append(kept, t)
		}
	}
	ctx.cfg.data["tasks"] = append(kept, task)
	if err := ctx.cfg.save(); err != nil {
		return err
	}
	if err := cmdReload(ctx); err != nil {
		return err
	}
	fmt.Printf("task \"%s\" added\n", name)
	return nil
}

func cmdEdit(ctx ctx, name string) error {
	if name == "" {
		return fmt.Errorf("usage: edge-sync edit <task>")
	}
	tasks := ctx.cfg.tasks()
	idx := -1
	for i, t := range tasks {
		if t["name"] == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("task not found: %s", name)
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	tmp := ctx.cfg.path + ".edit.yml"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("# edit task %q; name must stay unchanged\n", name)+marshalTask(tasks[idx])), 0o644); err != nil {
		return err
	}
	cmd := exec.Command(editor, tmp)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("editor exited nonzero, aborting")
	}
	edited, err := readYAML(tmp)
	os.Remove(tmp)
	if err != nil {
		return err
	}
	if edited["name"] != name {
		return fmt.Errorf("task name changed in editor, aborting")
	}
	tasks[idx] = edited
	ctx.cfg.data["tasks"] = tasks
	if err := ctx.cfg.save(); err != nil {
		return err
	}
	if err := cmdReload(ctx); err != nil {
		return err
	}
	fmt.Printf("task \"%s\" updated\n", name)
	return nil
}

func cmdRemove(ctx ctx, name string, purge bool) error {
	if name == "" {
		return fmt.Errorf("usage: edge-sync remove <task> [--purge]")
	}
	tasks := ctx.cfg.tasks()
	var kept []map[string]any
	found := false
	for _, t := range tasks {
		if t["name"] == name {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		return fmt.Errorf("task not found: %s", name)
	}
	if purge {
		dir := filepath.Join(ctx.cfg.storagePath("dataDir"), name)
		fmt.Println("purging data directory:", dir)
		if err := os.RemoveAll(dir); err != nil {
			// 目录可能含 root 属主的历史残留；警告后继续移除任务配置。
			fmt.Printf("warning: purge incomplete (%v)；请用 root 手动清理残留\n", err)
		}
	}
	ctx.cfg.data["tasks"] = kept
	if err := ctx.cfg.save(); err != nil {
		return err
	}
	if err := cmdReload(ctx); err != nil {
		return err
	}
	fmt.Printf("task \"%s\" removed\n", name)
	return nil
}

// ---------- 取回 ----------

func cmdExport(ctx ctx, name, out string) error {
	if name == "" || out == "" {
		return fmt.Errorf("usage: edge-sync export <task> [--version <ts>] --out <dir>")
	}
	version := ctx.flags.Get("version")
	if version == "" {
		version = "current"
	}
	src := filepath.Join(ctx.cfg.storagePath("dataDir"), name)
	if version == "current" {
		src = filepath.Join(src, "current")
	} else {
		src = filepath.Join(src, "versions", version)
	}
	if _, err := os.Stat(src); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := copyTree(src, out); err != nil {
		return err
	}
	fmt.Printf("exported %s@%s -> %s\n", name, version, out)
	return nil
}

func cmdRestoreFile(ctx ctx, name, path, out string) error {
	if name == "" || path == "" || out == "" {
		return fmt.Errorf("usage: edge-sync restore-file <task> <path> [--version <ts>] --out <file>")
	}
	version := ctx.flags.Get("version")
	if version == "" {
		version = "current"
	}
	src := filepath.Join(ctx.cfg.storagePath("dataDir"), name)
	if version == "current" {
		src = filepath.Join(src, "current", filepath.FromSlash(path))
	} else {
		src = filepath.Join(src, "versions", version, filepath.FromSlash(path))
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("restored %s@%s:%s -> %s (%d bytes)\n", name, version, path, out, len(data))
	return nil
}

// copyTree 递归复制目录（跳过版本元数据文件）。
func copyTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == manifestFileExport {
			continue
		}
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		in, err := os.Open(s)
		if err != nil {
			return err
		}
		outF, err := os.Create(d)
		if err != nil {
			in.Close()
			return err
		}
		if _, err := io.Copy(outF, in); err != nil {
			in.Close()
			outF.Close()
			return err
		}
		in.Close()
		if err := outF.Close(); err != nil {
			return err
		}
	}
	return nil
}

const manifestFileExport = ".edge-sync-manifest.json"

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}

func orEmpty(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}

func validTaskName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func marshalTask(task map[string]any) string {
	raw, _ := json.MarshalIndent(task, "", "  ")
	return string(raw)
}

func printYAMLTask(task map[string]any) {
	fmt.Println(marshalTask(map[string]any{"tasks": []map[string]any{task}}))
}
