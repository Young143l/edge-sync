// edge-sync CLI（Go 版，替代 TS CLI）：经 unix socket 与内核通信。
// 配置 YAML 是唯一真源；CLI 编辑后触发内核 config.reload。
package main

import (
	"fmt"
	"os"
)

const version = "0.2.0"

const help = `edge-sync — edge sync backup CLI

usage: edge-sync [-c <config.yaml>] <command> [args...]

commands:
  list                       list tasks and status
  status [task]              detailed task status
  add                        interactive task wizard
  edit <task>                edit task via $EDITOR
  remove <task> [--purge]    remove task (purge deletes local data)
  sync <task>                trigger a sync now (waits for completion)
  history <task>             list versions
  file-versions <task> <p>   history of a single file
  export <task> --out <dir>  export a version (default current) to dir
  restore-file <task> <p>    restore a single file (--out required)
  logs [n] [task]            tail kernel logs
  plugins                    list discovered plugins
  reload                     reload kernel config after manual edit

options:
  -c, --config <path>        kernel config file (default: etc/config.yaml)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	cfgPath := "etc/config.yaml"
	// 全局选项 -c/--config（仅出现在首个命令前）。
	for len(argv) >= 2 && (argv[0] == "-c" || argv[0] == "--config") {
		cfgPath = argv[1]
		argv = argv[2:]
	}
	// 过滤 "--" 分隔符。
	var filtered []string
	for _, a := range argv {
		if a != "--" {
			filtered = append(filtered, a)
		}
	}
	argv = filtered

	if len(argv) == 0 || argv[0] == "help" || argv[0] == "--help" || argv[0] == "-h" {
		fmt.Print(help)
		return nil
	}
	command, args := argv[0], argv[1:]
	flags, positional := parseFlags(args)

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	ctx := &context{cfg: cfg, flags: flags}

	switch command {
	case "list":
		return cmdList(ctx)
	case "status":
		return cmdStatus(ctx, at(positional, 0))
	case "add":
		return cmdAdd(ctx)
	case "edit":
		return cmdEdit(ctx, at(positional, 0))
	case "remove":
		return cmdRemove(ctx, at(positional, 0), flags.Bool("purge"))
	case "sync":
		return cmdSync(ctx, at(positional, 0))
	case "history":
		return cmdHistory(ctx, at(positional, 0))
	case "file-versions":
		return cmdFileVersions(ctx, at(positional, 0), at(positional, 1))
	case "export":
		return cmdExport(ctx, at(positional, 0), flags.Get("out"))
	case "restore-file":
		return cmdRestoreFile(ctx, at(positional, 0), at(positional, 1), flags.Get("out"))
	case "logs":
		n := 30
		if v, ok := parseInt(at(positional, 0)); ok {
			n = v
		}
		return cmdLogs(ctx, n, at(positional, 1))
	case "plugins":
		return cmdPlugins(ctx)
	case "reload":
		return cmdReload(ctx)
	default:
		fmt.Printf("unknown command: %s\n\n", command)
		fmt.Print(help)
		os.Exit(1)
	}
	return nil
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

func parseInt(s string) (int, bool) {
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err == nil {
		return v, true
	}
	return 0, false
}
