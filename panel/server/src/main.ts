import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { Hono } from 'hono'
import { getCookie, setCookie } from 'hono/cookie'
import { HTTPException } from 'hono/http-exception'

const COOKIE_NAME = 'edge-sync-token'
import { serve } from '@hono/node-server'
import { serveStatic } from '@hono/node-server/serve-static'
import {
  loadOrCreatePanelConfig,
  type PanelConfig,
} from './panel-config.js'
import { handle, mapIpcError } from './http-errors.js'
import { HttpError } from './download.js'
import type { ContentfulStatusCode } from 'hono/utils/http-status'
import { handleArchive, handleDownload } from './download.js'
import {
  IpcConnectError,
  IpcError,
  ipcCall,
  type LogEntry,
  type PluginInfo,
  type TaskDetail,
  type TaskSummary,
} from '@edge-sync/protocol'

interface Ctx {
  cfg: PanelConfig
}

// ---------- state routes（转发内核 IPC） ----------

function stateRoutes(cfg: PanelConfig): Hono {
  const app = new Hono()

  const call = <T>(method: string, params: Record<string, unknown> = {}) =>
    ipcCall<T>(cfg.socket, method, params)

  app.get('/overview', async (c) =>
    handle(c, async () => {
      const tasks = await call<TaskSummary[]>('task.list')
      return c.json(tasks)
    }),
  )

  app.get('/tasks/:name', async (c) =>
    handle(c, async () => {
      const d = await call<TaskDetail>('task.status', { name: c.req.param('name') })
      return c.json(d)
    }),
  )

  app.post('/tasks/:name/sync', async (c) =>
    handle(c, async () => {
      const res = await call<{ triggered: boolean }>('task.trigger', {
        name: c.req.param('name'),
      })
      return c.json(res)
    }),
  )

  app.get('/tasks/:name/history', async (c) =>
    handle(c, async () => {
      const v = await call<unknown[]>('history.list', { name: c.req.param('name') })
      return c.json(v)
    }),
  )

  app.get('/tasks/:name/files', async (c) =>
    handle(c, async () => {
      const f = await call<unknown[]>('history.files', {
        name: c.req.param('name'),
        version: c.req.query('version') ?? 'current',
        subPath: c.req.query('path') ?? '',
      })
      return c.json(f)
    }),
  )

  app.get('/tasks/:name/file-versions', async (c) =>
    handle(c, async () => {
      const fv = await call<unknown[]>('history.fileVersions', {
        name: c.req.param('name'),
        path: c.req.query('path') ?? '',
      })
      return c.json(fv)
    }),
  )

  app.get('/logs', async (c) =>
    handle(c, async () => {
      const n = Number.parseInt(c.req.query('n') ?? '100', 10)
      const logs = await call<LogEntry[]>('log.tail', {
        n: Number.isFinite(n) && n > 0 ? Math.min(n, 1000) : 100,
        task: c.req.query('task') ?? '',
      })
      return c.json(logs)
    }),
  )

  app.get('/plugins', async (c) =>
    handle(c, async () => {
      const plugins = await call<PluginInfo[]>('plugin.list')
      return c.json(plugins)
    }),
  )

  return app
}

// ---------- app ----------

function buildApp(cfg: PanelConfig): Hono {
  const app = new Hono()

  // 登录：校验 token 并种 HttpOnly Cookie —— 浏览器原生导航（<a href> 下载 /
  // location.href zip）会自动携带 cookie，Bearer 仅保留给 API 客户端。
  app.post('/api/auth/login', async (c) => {
    const body = (await c.req.json().catch(() => ({}))) as { token?: string }
    if (body.token !== cfg.token) {
      return c.json({ error: 'token 不正确' }, 401)
    }
    setCookie(c, COOKIE_NAME, cfg.token, {
      httpOnly: true,
      path: '/',
      maxAge: 30 * 24 * 3600,
      sameSite: 'Lax',
    })
    return c.json({ ok: true })
  })

  // 鉴权：Cookie（浏览器）或 Bearer（API 客户端）二选一。
  app.use('/api/*', async (c, next) => {
    const cookie = getCookie(c, COOKIE_NAME)
    const auth = c.req.header('Authorization') ?? ''
    if (cookie !== cfg.token && auth !== `Bearer ${cfg.token}`) {
      return c.json({ error: '需要访问令牌' }, 401)
    }
    await next()
  })

  app.route('/api', stateRoutes(cfg))

  // 下载流（fs 直读，不过内核）。
  app.get('/api/tasks/:name/download', (c) => handleDownload(c, cfg, c.req.param('name')))
  app.get('/api/tasks/:name/archive', (c) => handleArchive(c, cfg, c.req.param('name')))

  // 静态托管 + SPA fallback。
  if (cfg.webDir && existsSync(cfg.webDir)) {
    app.use('*', serveStatic({ root: cfg.webDir }))
    app.get('*', (c) => {
      const index = join(cfg.webDir!, 'index.html')
      if (existsSync(index)) {
        return c.body(readFileSync(index), 200, { 'Content-Type': 'text/html; charset=utf-8' })
      }
      return c.text('panel web dist not built', 404)
    })
  } else {
    app.get('/', (c) =>
      c.text('edge-sync panel: web dist not configured (build panel/web first)'),
    )
  }

  // 全局错误兜底（401 原样透传）。
  app.onError((e, c) => {
    if (e instanceof HTTPException) return e.getResponse()
    if (e instanceof HttpError) return c.json({ error: e.message }, e.status as ContentfulStatusCode)
    if (e instanceof IpcConnectError || e instanceof IpcError) return mapIpcError(c, e)
    console.error('[panel] unhandled:', e)
    return c.json({ error: e instanceof Error ? e.message : String(e) }, 500)
  })

  return app
}

// ---------- main ----------

function parseArgs(argv: string[]): { config: string } {
  let config = 'etc/panel.json'
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '-c' || argv[i] === '--config') {
      config = argv[i + 1] ?? config
      i++
    }
  }
  return { config }
}

function main(): void {
  const { config } = parseArgs(process.argv.slice(2))
  const cfg = loadOrCreatePanelConfig(config)
  if (!cfg.socket) {
    console.error('[panel] panel.json missing "socket" (kernel unix socket path)')
    process.exit(1)
  }
  if (!cfg.dataDir) {
    console.error('[panel] panel.json missing "dataDir"')
    process.exit(1)
  }

  const app = buildApp(cfg)
  const server = serve({ fetch: app.fetch, port: cfg.port, hostname: cfg.bind })
  console.log(`[panel] serving on http://${cfg.bind}:${cfg.port} (socket: ${cfg.socket})`)

  const shutdown = (): void => {
    console.log('[panel] shutting down')
    server.close()
    process.exit(0)
  }
  process.on('SIGINT', shutdown)
  process.on('SIGTERM', shutdown)
}

main()
