#!/usr/bin/env -S tsx
/**
 * edge-sync CLI：经 Unix socket 与内核 edge-syncd 通信。
 * 配置 YAML 是唯一真源；CLI 编辑配置后触发内核 config.reload。
 */
import { existsSync } from 'node:fs'
import { ConfigFile } from './config-io.js'
import {
  cmdAdd,
  cmdEdit,
  cmdExport,
  cmdFileVersions,
  cmdHistory,
  cmdList,
  cmdLogs,
  cmdPlugins,
  cmdReload,
  cmdRemove,
  cmdRestoreFile,
  cmdStatus,
  cmdSync,
  type Ctx,
} from './commands.js'

const HELP = `edge-sync — edge sync backup CLI

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
  reload                     reload kernel config after manual edit
  plugins                    list discovered plugins

options:
  -c, --config <path>        kernel config file (default: etc/config.yaml)
`

interface Parsed {
  flags: Map<string, string>
  args: string[]
}

function parseArgs(argv: string[]): { global: Parsed; rest: string[] } {
  const global: Parsed = { flags: new Map(), args: [] }
  let i = 0
  while (i < argv.length) {
    const a: string = argv[i] ?? ''
    if (a === '-c' || a === '--config') {
      global.flags.set('config', argv[i + 1] ?? '')
      i += 2
      continue
    }
    break
  }
  global.args = argv.slice(i).filter((a) => a !== '--')
  return { global, rest: global.args }
}

/** 把命令行 --flag [value] 解析进 flags，返回剩余位置参数。 */
function parseRest(rest: string[], flags: Map<string, string>): string[] {
  const positional: string[] = []
  for (let i = 0; i < rest.length; i++) {
    const a: string = rest[i] ?? ''
    if (a.startsWith('--')) {
      const key = a.slice(2)
      const next: string | undefined = rest[i + 1]
      if (next !== undefined && !next.startsWith('--')) {
        flags.set(key, next)
        i++
      } else {
        flags.set(key, 'true')
      }
      continue
    }
    positional.push(a)
  }
  return positional
}

async function main(): Promise<void> {
  const argv: string[] = process.argv.slice(2)
  const { global, rest } = parseArgs(argv)
  const [command, ...args] = rest

  if (!command || command === 'help' || command === '--help') {
    process.stdout.write(HELP)
    return
  }

  const cfgPath = global.flags.get('config') ?? 'etc/config.yaml'
  if (!existsSync(cfgPath)) {
    console.error(`config file not found: ${cfgPath} (use -c to specify)`)
    process.exitCode = 1
    return
  }
  const ctx: Ctx = { cfg: new ConfigFile(cfgPath) }
  const flags = new Map<string, string>()
  const positional = parseRest(args, flags)

  switch (command) {
    case 'list':
      return cmdList(ctx)
    case 'status':
      return cmdStatus(ctx, positional[0])
    case 'add':
      return cmdAdd(ctx)
    case 'edit':
      return cmdEdit(ctx, positional[0])
    case 'remove':
      return cmdRemove(ctx, positional[0], flags)
    case 'sync':
      return cmdSync(ctx, positional[0])
    case 'history':
      return cmdHistory(ctx, positional[0])
    case 'file-versions':
      return cmdFileVersions(ctx, positional[0], positional[1])
    case 'export':
      return cmdExport(ctx, positional[0], flags)
    case 'restore-file':
      return cmdRestoreFile(ctx, positional[0], positional[1], flags)
    case 'logs': {
      const n = Number.parseInt(positional[0] ?? '30', 10)
      return cmdLogs(ctx, Number.isInteger(n) ? n : 30, positional[1])
    }
    case 'plugins':
      return cmdPlugins(ctx)
    case 'reload':
      return cmdReload(ctx)
    default:
      console.error(`unknown command: ${command}\n`)
      process.stdout.write(HELP)
      process.exitCode = 1
  }
}

main().catch((e: Error) => {
  console.error(`error: ${e.message}`)
  process.exitCode = 1
})
