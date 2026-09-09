import { createReadStream } from 'node:fs'
import { lstat, realpath, stat } from 'node:fs/promises'
import { PassThrough, Readable } from 'node:stream'
import { basename, join, resolve, sep } from 'node:path'
import type { Context } from 'hono'
import archiver from 'archiver'
import type { PanelConfig } from './panel-config.js'
import { Limiter } from './limit.js'
import { MANIFEST_FILE } from '@edge-sync/protocol'

// 版本目录内元数据文件名与内核 engine.ManifestFile 对齐。

const VERSION_RE = /^\d{8}-\d{6}(-\d+)?$/
const limiter = new Limiter(2)

/** 版本目录解析：current → realpath；版本名校验防穿越。 */
export async function resolveVersionDir(cfg: PanelConfig, task: string, version: string): Promise<string> {
  if (!task || task.includes('/') || task.includes('..')) {
    throw new HttpError(400, 'invalid task name')
  }
  const dataDir = join(resolve(cfg.dataDir), task)
  if (version === '' || version === 'current') {
    const link = join(dataDir, 'current')
    try {
      return await realpath(link)
    } catch {
      throw new HttpError(404, 'no current version yet')
    }
  }
  if (!VERSION_RE.test(version)) {
    throw new HttpError(400, `invalid version name: ${version}`)
  }
  const dir = join(dataDir, 'versions', version)
  const fi = await stat(dir).catch(() => null)
  if (!fi?.isDirectory()) {
    throw new HttpError(404, `version not found: ${version}`)
  }
  return await realpath(dir)
}

/** 文件解析：SafeJoin 语义 + 必须是常规文件；解析结果必须仍在版本目录内。 */
export async function resolveFileUnder(versionDir: string, subPath: string): Promise<string> {
  if (!subPath || subPath.includes('\0')) {
    throw new HttpError(400, 'invalid path')
  }
  const cleaned = resolve(versionDir, subPath)
  const versionReal = await realpath(versionDir)
  const candidate = await realpath(cleaned).catch(() => null)
  if (!candidate || !(candidate + sep).startsWith(versionReal + sep)) {
    throw new HttpError(400, 'path escapes version directory')
  }
  const fi = await lstat(candidate)
  if (!fi.isFile()) {
    throw new HttpError(400, 'not a regular file')
  }
  return candidate
}

export class HttpError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

export function httpErrorOf(e: unknown): number {
  if (e instanceof HttpError) return e.status
  return 500
}

function contentDisposition(filename: string): string {
  return `attachment; filename*=UTF-8''${encodeURIComponent(filename)}`
}

/** GET /api/tasks/:name/download?version=&path= —— fs 流式单文件下载。 */
export async function handleDownload(
  c: Context,
  cfg: PanelConfig,
  task: string,
): Promise<Response> {
  const version = c.req.query('version') ?? 'current'
  const subPath = c.req.query('path') ?? ''
  await limiter.acquire()
  try {
    const versionDir = await resolveVersionDir(cfg, task, version)
    const file = await resolveFileUnder(versionDir, subPath)
    const fi = await stat(file)
    const stream = Readable.toWeb(createReadStream(file)) as ReadableStream
    return c.body(stream, 200, {
      'Content-Type': 'application/octet-stream',
      'Content-Length': String(fi.size),
      'Content-Disposition': contentDisposition(basename(subPath)),
    })
  } finally {
    limiter.release()
  }
}

/** 版本预估体积：读版本 manifest 元数据；无则返回 null。 */
async function estimateVersionBytes(versionDir: string): Promise<number | null> {
  try {
    const raw = await import('node:fs/promises').then((m) => m.readFile(join(versionDir, MANIFEST_FILE), 'utf8'))
    const mf = JSON.parse(raw) as { entries?: { size?: number }[] }
    let sum = 0
    for (const e of mf.entries ?? []) sum += e.size ?? 0
    return sum
  } catch {
    return null
  }
}

const MB = 1024 * 1024

/** GET /api/tasks/:name/archive?version=&store=1 —— 整版本 zip 流式打包。 */
export async function handleArchive(
  c: Context,
  cfg: PanelConfig,
  task: string,
): Promise<Response> {
  const version = c.req.query('version') ?? 'current'
  const store = c.req.query('store') === '1'
  await limiter.acquire()
  try {
    const versionDir = await resolveVersionDir(cfg, task, version)
    const est = await estimateVersionBytes(versionDir)

    const archive = archiver('zip', { store })
    const pass = new PassThrough()
    archive.pipe(pass)
    // 目录整棵打包；元数据文件排除（对外不可见）。
    archive.directory(versionDir, false, (entry) =>
      basename(entry.name) === MANIFEST_FILE ? false : entry,
    )
    void archive.finalize()

    const headers: Record<string, string> = {
      'Content-Type': 'application/zip',
      'Content-Disposition': contentDisposition(`${task}-${version === 'current' ? 'current' : version}.zip`),
      'Cache-Control': 'no-store',
    }
    if (est !== null) {
      headers['X-Edge-Sync-Bytes'] = String(est)
      if (est > 500 * MB) headers['X-Edge-Sync-Suggest'] = 'rsync'
    }

    const body = Readable.toWeb(pass) as ReadableStream
    return c.body(body, 200, headers)
  } finally {
    limiter.release()
  }
}

