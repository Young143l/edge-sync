/**
 * 面板 API 客户端：token 管理 + 统一错误。
 * 401 → 清 token 并通知全局（登录 Dialog）；
 * 503 / 网络错误 → offline 语义（离线横幅）。
 */

const TOKEN_KEY = 'edge-sync-token'

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? ''
}

export function setToken(t: string): void {
  localStorage.setItem(TOKEN_KEY, t)
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly offline = false,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export function isOfflineError(e: unknown): boolean {
  return e instanceof ApiError && e.offline
}

type UnauthorizedCb = () => void
let cb: UnauthorizedCb | null = null

/** App 注册未授权回调（触发登录 Dialog）。 */
export function onUnauthorized(fn: UnauthorizedCb): void {
  cb = fn
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  const token = getToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)

  let resp: Response
  try {
    resp = await fetch(path, { ...init, headers })
  } catch {
    throw new ApiError(0, '网络错误，无法连接面板服务', true)
  }

  if (resp.status === 401) {
    cb?.()
    throw new ApiError(401, '需要访问令牌')
  }

  if (!resp.ok) {
    let msg = `HTTP ${resp.status}`
    try {
      const body = (await resp.json()) as { error?: string }
      if (body.error) msg = body.error
    } catch {
      // 非 JSON 错误体
    }
    throw new ApiError(resp.status, msg, resp.status === 503)
  }
  return (await resp.json()) as T
}
