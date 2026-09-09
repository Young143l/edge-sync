import { randomBytes } from 'node:crypto'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'

/** 面板自身配置（etc/panel.json）；部署助手负责 socket/dataDir 字段。 */
export interface PanelConfig {
  port: number
  bind: string
  token: string
  socket: string
  dataDir: string
  /** 前端静态目录（panel/web/dist）；开发时可缺省。 */
  webDir?: string
}

export function loadOrCreatePanelConfig(path: string): PanelConfig {
  let data: Partial<PanelConfig> = {}
  if (existsSync(path)) {
    data = JSON.parse(readFileSync(path, 'utf8')) as Partial<PanelConfig>
  }
  const cfg: PanelConfig = {
    port: data.port ?? 8080,
    bind: data.bind ?? '0.0.0.0',
    token: data.token ?? randomBytes(24).toString('hex'),
    socket: data.socket ?? '',
    dataDir: data.dataDir ?? '',
    webDir: data.webDir,
  }
  if (!data.token || !existsSync(path)) {
    writeFileSync(path, JSON.stringify(cfg, null, 2) + '\n', 'utf8')
    console.log(`[panel] config written to ${path}`)
  }
  return cfg
}
