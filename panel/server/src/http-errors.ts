import type { Context } from 'hono'
import type { ContentfulStatusCode } from 'hono/utils/http-status'
import { IpcConnectError, IpcError } from '@edge-sync/protocol'

/** IPC 错误 → HTTP 状态码（panel-design 3.3 错误映射表）。 */
export function mapIpcError(c: Context, e: unknown): Response {
  if (e instanceof IpcConnectError) {
    return c.json({ error: e.message }, 503 satisfies ContentfulStatusCode)
  }
  if (e instanceof IpcError) {
    if (e.notFound) return c.json({ error: e.message }, 404 satisfies ContentfulStatusCode)
    if (e.invalidParams) return c.json({ error: e.message }, 400 satisfies ContentfulStatusCode)
    return c.json({ error: e.message }, 502 satisfies ContentfulStatusCode)
  }
  return c.json({ error: e instanceof Error ? e.message : String(e) }, 500 satisfies ContentfulStatusCode)
}

/** 统一 handler 包装：异常落错误映射。 */
export async function handle<T>(
  c: Context,
  fn: (c: Context) => Promise<Response> | Response,
): Promise<Response> {
  try {
    return await fn(c)
  } catch (e) {
    return mapIpcError(c, e)
  }
}
