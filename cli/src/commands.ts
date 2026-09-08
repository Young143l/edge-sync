import { createInterface } from 'node:readline/promises'
import { cp, mkdir, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { basename, dirname, join } from 'node:path'
import { realpath } from 'node:fs/promises'
import { ConfigFile, parseYaml, stringifyYaml } from './config-io.js'
import { ipcCall } from './ipc.js'
import { hr, humanBytes, pad, row, statusBadge, timeAgo } from './format.js'
import type {
  FileListEntry,
  FileVersionEntry,
  LogEntry,
  PluginInfo,
  TaskDetail,
  TaskSummary,
  VersionInfo,
} from '@edge-sync/protocol'

export interface Ctx {
  cfg: ConfigFile
}

const W = [16, 10, 12, 12, 14] as const

export async function cmdList(ctx: Ctx): Promise<void> {
  const tasks = await ipcCall<TaskSummary[]>(ctx.cfg.socketPath(), 'task.list')
  console.log(row([
    ['NAME', W[0]], ['STATUS', W[1]], ['PLUGIN', W[2]], ['INTERVAL', W[3]], ['LAST SYNC', W[4]],
  ]))
  console.log(hr(70))
  for (const t of tasks) {
    const failed = t.consecutiveFailures > 0 ? ` (${t.consecutiveFailures} fails)` : ''
    console.log(row([
      [t.name, W[0]],
      [statusBadge(t.status + failed), W[1] + 14],
      [t.plugin, W[2]],
      [t.interval, W[3]],
      [timeAgo(t.lastSuccessAt), W[4]],
    ]))
  }
  console.log(`\n${tasks.length} task(s)`)
}

export async function cmdStatus(ctx: Ctx, name?: string): Promise<void> {
  if (!name) {
    await cmdList(ctx)
    return
  }
  const d = await ipcCall<TaskDetail>(ctx.cfg.socketPath(), 'task.status', { name })
  console.log(`task:      ${d.name}`)
  console.log(`plugin:    ${d.plugin}`)
  console.log(`status:    ${statusBadge(d.status)}`)
  console.log(`interval:  ${d.interval}`)
  console.log(`next run:  ${d.nextRunAt ?? '-'}`)
  console.log(`last sync: ${d.lastSyncAt ?? 'never'} (success: ${d.lastSuccessAt ?? 'never'})`)
  console.log(`failures:  ${d.consecutiveFailures}`)
  console.log(`retention: keepLast=${d.retention.keepLast} keepDays=${d.retention.keepDays}`)
  console.log(`stats:     ${d.stats.totalSyncs} syncs, ${d.stats.totalFiles} files, ${humanBytes(d.stats.totalBytes)}`)
}

export async function cmdSync(ctx: Ctx, name?: string): Promise<void> {
  if (!name) {
    console.error('usage: edge-sync sync <task>')
    process.exitCode = 1
    return
  }
  console.log(`syncing ${name} ...`)
  const res = await ipcCall<{ triggered: boolean }>(ctx.cfg.socketPath(), 'task.trigger', { name })
  if (res.triggered) {
    console.log(`${name}: sync completed`)
  }
}

export async function cmdHistory(ctx: Ctx, name?: string): Promise<void> {
  if (!name) {
    console.error('usage: edge-sync history <task>')
    process.exitCode = 1
    return
  }
  const versions = await ipcCall<VersionInfo[]>(ctx.cfg.socketPath(), 'history.list', { name })
  console.log(row([
    ['VERSION', 20], ['FILES', 8], ['SIZE', 12], ['NOTE', 10],
  ]))
  console.log(hr(56))
  for (const v of versions) {
    console.log(row([
      [v.version, 20],
      [String(v.files), 8],
      [humanBytes(v.bytes), 12],
      [v.isCurrent ? '<- current' : '', 10],
    ]))
  }
  console.log(`\n${versions.length} version(s)`)
}

export async function cmdLogs(ctx: Ctx, n = 30, task?: string): Promise<void> {
  const entries = await ipcCall<LogEntry[]>(ctx.cfg.socketPath(), 'log.tail', { n, task: task ?? '' })
  for (const e of entries) {
    const attrs = e.attrs ? `  ${e.attrs}` : ''
    console.log(`${e.time} ${pad(e.level.toUpperCase(), 6)} ${e.msg}${attrs}`)
  }
}

export async function cmdPlugins(ctx: Ctx): Promise<void> {
  const plugins = await ipcCall<PluginInfo[]>(ctx.cfg.socketPath(), 'plugin.list')
  for (const p of plugins) {
    console.log(`${pad(p.name, 12)} v${p.version}`)
    if (p.configSchema) {
      const schema = p.configSchema as { properties?: Record<string, { type?: string; description?: string }> }
      for (const [k, v] of Object.entries(schema.properties ?? {})) {
        console.log(`  - ${k} (${v.type ?? 'any'})${v.description ? `: ${v.description}` : ''}`)
      }
    }
  }
}

// ---------- add 向导 ----------

async function ask(rl: ReturnType<typeof createInterface>, question: string, def?: string): Promise<string> {
  const suffix = def !== undefined && def !== '' ? ` [${def}]` : ''
  const a = (await rl.question(`${question}${suffix}: `)).trim()
  return a === '' ? (def ?? '') : a
}

export async function cmdAdd(ctx: Ctx): Promise<void> {
  const rl = createInterface({ input: process.stdin, output: process.stdout })
  try {
    const plugins = await ipcCall<PluginInfo[]>(ctx.cfg.socketPath(), 'plugin.list')
    if (plugins.length === 0) {
      console.error('no plugins discovered in pluginBinDir')
      return
    }
    console.log('available plugins:')
    plugins.forEach((p, i) => console.log(`  ${i + 1}. ${p.name} (v${p.version})`))

    let plugin = ''
    for (;;) {
      const pick = await ask(rl, 'plugin (number or name)')
      const idx = Number.parseInt(pick, 10)
      const chosen = Number.isInteger(idx) && idx >= 1 && idx <= plugins.length
        ? plugins[idx - 1]
        : plugins.find((p) => p.name === pick)
      if (chosen) {
        plugin = chosen.name
        break
      }
      console.log('  invalid choice')
    }

    const chosen = plugins.find((p) => p.name === plugin)!
    const name = await ask(rl, 'task name (letters/digits/._-)')
    if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(name)) {
      console.error('invalid task name')
      process.exitCode = 1
      return
    }
    const intervalSec = Number.parseInt(await ask(rl, 'poll interval (seconds)', '600'), 10)
    const keepLast = Number.parseInt(await ask(rl, 'retention keepLast (versions to keep)', '10'), 10)
    const keepDays = Number.parseInt(await ask(rl, 'retention keepDays (0 = unlimited)', '0'), 10)

    const options: Record<string, unknown> = {}
    const props = (chosen.configSchema as { properties?: Record<string, { type?: string; description?: string }> })
      ?.properties
    if (props && Object.keys(props).length > 0) {
      console.log(`plugin ${plugin} options:`)
      for (const [key, prop] of Object.entries(props)) {
        const desc = prop.description ? ` (${prop.description})` : ''
        const raw = await ask(rl, `  ${key} [${prop.type ?? 'any'}]${desc}`)
        if (raw === '') continue
        options[key] = prop.type === 'number' || prop.type === 'integer'
          ? Number(raw)
          : prop.type === 'boolean'
            ? raw === 'true' || raw === 'yes'
            : raw
      }
    } else {
      const raw = await ask(rl, 'options (JSON, empty = {})', '{}')
      Object.assign(options, parseYaml(raw) as Record<string, unknown>)
    }

    const task = {
      name,
      plugin,
      interval: `${intervalSec}s`,
      options,
      retention: { keepLast, keepDays },
    }
    console.log('\n' + stringifyYaml({ tasks: [task] }))
    const confirm = await ask(rl, 'write this task to config? (y/N)', 'n')
    if (confirm.toLowerCase() !== 'y') {
      console.log('aborted')
      return
    }
    const tasks = ctx.cfg.tasks().filter((t) => t['name'] !== name)
    tasks.push(task)
    ctx.cfg.data.tasks = tasks
    ctx.cfg.save()
    await reload(ctx)
    console.log(`task "${name}" added`)
  } finally {
    rl.close()
  }
}

export async function cmdEdit(ctx: Ctx, name?: string): Promise<void> {
  if (!name) {
    console.error('usage: edge-sync edit <task>')
    process.exitCode = 1
    return
  }
  const tasks = ctx.cfg.tasks()
  const idx = tasks.findIndex((t) => t['name'] === name)
  if (idx < 0) {
    console.error(`task not found: ${name}`)
    process.exitCode = 1
    return
  }
  const editor = process.env['EDITOR'] || 'vi'
  const tmp = `${ctx.cfg.path}.edit-${name}.yml`
  await writeFile(tmp, `# edit task "${name}"; name must stay unchanged\n${stringifyYaml(tasks[idx])}`)
  const { spawnSync } = await import('node:child_process')
  const res = spawnSync(editor, [tmp], { stdio: 'inherit' })
  if (res.status !== 0) {
    console.error('editor exited nonzero, aborting')
    return
  }
  let edited: Record<string, unknown>
  try {
    edited = parseYaml(await readFile(tmp, 'utf8')) as Record<string, unknown>
  } catch (e) {
    console.error(`parse edited file failed: ${(e as Error).message}`)
    await rm(tmp, { force: true })
    process.exitCode = 1
    return
  }
  await rm(tmp, { force: true })
  if (edited['name'] !== name) {
    console.error('task name changed in editor, aborting')
    process.exitCode = 1
    return
  }
  tasks[idx] = edited
  ctx.cfg.data.tasks = tasks
  ctx.cfg.save()
  await reload(ctx)
  console.log(`task "${name}" updated`)
}

export async function cmdRemove(ctx: Ctx, name?: string, flags: Map<string, string> = new Map()): Promise<void> {
  if (!name) {
    console.error('usage: edge-sync remove <task> [--purge]')
    process.exitCode = 1
    return
  }
  const tasks = ctx.cfg.tasks()
  const remaining = tasks.filter((t) => t['name'] !== name)
  if (remaining.length === tasks.length) {
    console.error(`task not found: ${name}`)
    process.exitCode = 1
    return
  }
  if (flags.get('purge') === 'true') {
    const dir = join(ctx.cfg.storagePath('dataDir'), name)
    console.log(`purging data directory: ${dir}`)
    await rm(dir, { recursive: true, force: true })
  }
  ctx.cfg.data.tasks = remaining
  ctx.cfg.save()
  await reload(ctx)
  console.log(`task "${name}" removed`)
}

export async function cmdExport(ctx: Ctx, name?: string, flags: Map<string, string> = new Map()): Promise<void> {
  if (!name || !flags.get('out')) {
    console.error('usage: edge-sync export <task> [--version <ts>] --out <dir>')
    process.exitCode = 1
    return
  }
  const dataDir = join(ctx.cfg.storagePath('dataDir'), name)
  const version = flags.get('version') || 'current'
  const src = version === 'current'
    ? join(dataDir, 'current')
    : join(dataDir, 'versions', version)

  await stat(src) // 不存在直接抛错
  const out = flags.get('out')!
  await mkdir(out, { recursive: true })
  // current 是 symlink，解引用为真实版本目录再拷贝。
  const realSrc = await realpath(src)
  await cp(realSrc, out, {
    recursive: true,
    filter: (s) => basename(s) !== '.edge-sync-manifest.json',
  })
  console.log(`exported ${name}@${version} -> ${out}`)
}

export async function cmdRestoreFile(
  ctx: Ctx,
  name?: string,
  path?: string,
  flags: Map<string, string> = new Map(),
): Promise<void> {
  if (!name || !path || !flags.get('out')) {
    console.error('usage: edge-sync restore-file <task> <path> [--version <ts>] --out <file>')
    process.exitCode = 1
    return
  }
  const dataDir = join(ctx.cfg.storagePath('dataDir'), name)
  const version = flags.get('version') || 'current'
  const src = version === 'current'
    ? join(dataDir, 'current', path)
    : join(dataDir, 'versions', version, path)
  const content = await readFile(src)
  const out = flags.get('out')!
  await mkdir(dirname(out), { recursive: true })
  await writeFile(out, content)
  console.log(`restored ${name}@${version}:${path} -> ${out} (${content.length} bytes)`)
}

export async function cmdFileVersions(ctx: Ctx, name?: string, path?: string): Promise<void> {
  if (!name || !path) {
    console.error('usage: edge-sync file-versions <task> <path>')
    process.exitCode = 1
    return
  }
  const versions = await ipcCall<FileVersionEntry[]>(
    ctx.cfg.socketPath(), 'history.fileVersions', { name, path })
  console.log(row([['VERSION', 20], ['SIZE', 12], ['FINGERPRINT', 16]]))
  console.log(hr(52))
  for (const v of versions) {
    console.log(row([
      [v.version, 20],
      [humanBytes(v.size ?? 0), 12],
      [v.fingerprint.slice(0, 14), 16],
    ]))
  }
}

export async function cmdReload(ctx: Ctx): Promise<void> {
  await reload(ctx)
}

async function reload(ctx: Ctx): Promise<void> {
  const res = await ipcCall<{ reloaded: boolean; tasks: number }>(
    ctx.cfg.socketPath(), 'config.reload')
  console.log(`config reloaded (${res.tasks} task(s))`)
}
