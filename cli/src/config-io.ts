import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute, join, resolve } from 'node:path'
import { parse, stringify } from 'yaml'

/**
 * CLI 侧配置文件句柄。
 * 注意：CLI 只负责编辑 YAML（唯一真源）并触发内核 config.reload；
 * 路径字段相对路径按「相对配置文件所在目录」解析（建议配置内写绝对路径）。
 */
export class ConfigFile {
  readonly path: string
  data: Record<string, unknown>

  constructor(path: string) {
    this.path = resolve(path)
    this.data = parse(readFileSync(this.path, 'utf8')) as Record<string, unknown>
  }

  /** 存储类路径解析。 */
  storagePath(key: 'dataDir' | 'stateDir' | 'pluginBinDir'): string {
    const storage = (this.data.storage ?? {}) as Record<string, string>
    const v = storage[key] ?? ''
    if (!v) return ''
    return isAbsolute(v) ? v : join(dirname(this.path), v)
  }

  /** IPC socket 路径解析。 */
  socketPath(): string {
    const server = (this.data.server ?? {}) as Record<string, string>
    const v = server.socket ?? 'var/edge-syncd.sock'
    return isAbsolute(v) ? v : join(dirname(this.path), v)
  }

  tasks(): Record<string, unknown>[] {
    return (this.data.tasks ?? []) as Record<string, unknown>[]
  }

  save(): void {
    writeFileSync(this.path, stringify(this.data), 'utf8')
  }
}

export { parse as parseYaml, stringify as stringifyYaml, resolve as resolvePath }
