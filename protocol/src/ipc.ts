/**
 * 内核 IPC 客户端（Unix socket 行分隔 JSON-RPC）。
 * CLI 与 panel 共用；一次调用一条连接。
 */
import { connect } from 'node:net'
import type { JsonRpcRequest } from './index.js'
import { ErrorCode } from './index.js'

/** 带协议错误码的 IPC 错误（内核 JSON-RPC error 响应）。 */
export class IpcError extends Error {
  constructor(
    readonly code: number,
    message: string,
  ) {
    super(message)
    this.name = 'IpcError'
  }
  get notFound(): boolean {
    return this.code === ErrorCode.NotFound
  }
  get invalidParams(): boolean {
    return this.code === ErrorCode.InvalidParams
  }
}

/** 连接类错误（socket 不存在/不可达）→ 面板映射为「内核离线」。 */
export class IpcConnectError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'IpcConnectError'
  }
}

export interface IpcCallOptions {
  /** 默认 30 分钟（覆盖大文件同步的 task.trigger 等待）。 */
  timeoutMs?: number
}

export async function ipcCall<T = unknown>(
  socketPath: string,
  method: string,
  params: Record<string, unknown> = {},
  options: IpcCallOptions = {},
): Promise<T> {
  const timeoutMs = options.timeoutMs ?? 30 * 60 * 1000
  return new Promise<T>((resolve, reject) => {
    const socket = connect(socketPath)
    let buf = ''
    let done = false

    const finish = (fn: () => void) => {
      if (done) return
      done = true
      clearTimeout(timer)
      socket.destroy()
      fn()
    }

    const timer = setTimeout(() => {
      finish(() => reject(new IpcConnectError(`IPC ${method}: timed out after ${timeoutMs}ms`)))
    }, timeoutMs)

    socket.on('connect', () => {
      const req: JsonRpcRequest = { jsonrpc: '2.0', id: 1, method: method as never, params }
      socket.write(JSON.stringify(req) + '\n')
    })

    socket.on('data', (chunk) => {
      buf += chunk.toString('utf8')
      const idx = buf.indexOf('\n')
      if (idx < 0) return
      const line = buf.slice(0, idx)
      finish(() => {
        let parsed: { result?: T; error?: { code: number; message: string } }
        try {
          parsed = JSON.parse(line)
        } catch (e) {
          reject(new IpcError(ErrorCode.InternalError, `bad response line: ${(e as Error).message}`))
          return
        }
        if (parsed.error) {
          reject(new IpcError(parsed.error.code, parsed.error.message))
          return
        }
        resolve(parsed.result as T)
      })
    })

    socket.on('error', (e: NodeJS.ErrnoException) =>
      finish(() => reject(new IpcConnectError(`IPC ${method}: ${e.message}`))),
    )
  })
}
