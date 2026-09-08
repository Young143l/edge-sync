import { connect } from 'node:net'
import type { JsonRpcRequest } from '@edge-sync/protocol'

/**
 * 对内核 IPC（Unix socket 行分隔 JSON-RPC）发起单次调用。
 * 一次调用一条连接；同步类方法（task.trigger）在内核侧阻塞到完成，
 * 默认超时给足大文件同步的余量。
 */
export async function ipcCall<T = unknown>(
  socketPath: string,
  method: string,
  params: Record<string, unknown> = {},
  timeoutMs = 30 * 60 * 1000,
): Promise<T> {
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
      finish(() => reject(new Error(`IPC ${method}: timed out after ${timeoutMs}ms`)))
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
        let resp: { result?: T; error?: { code: number; message: string } }
        try {
          resp = JSON.parse(line)
        } catch (e) {
          reject(new Error(`IPC ${method}: bad response line: ${(e as Error).message}`))
          return
        }
        if (resp.error) {
          reject(new Error(resp.error.message))
          return
        }
        resolve(resp.result as T)
      })
    })

    socket.on('error', (e) => finish(() => reject(e)))
  })
}
