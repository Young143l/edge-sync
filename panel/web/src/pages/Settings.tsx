import { Alert, Box, Divider, Paper, Typography } from '@mui/material'
import { usePlugins } from '../query'
import { EmptyState } from '../components/Shared'

/** 设置页：只读摘要（panel-design 4.3；修改配置请用 CLI）。 */
export function SettingsPage(): React.JSX.Element {
  const { data: plugins } = usePlugins()

  return (
    <Box>
      <Alert severity="info" variant="outlined" sx={{ mb: 2 }}>
        面板只读展示配置摘要；修改配置请使用 CLI（edge-sync add/edit/remove）或直接编辑
        etc/config.yaml 后执行 edge-sync reload。
      </Alert>

      <Paper variant="outlined" sx={{ p: 2, mb: 2 }}>
        <Typography variant="h6" sx={{ fontSize: 16, mb: 1 }}>
          服务
        </Typography>
        <SettingRow k="版本" v="edge-sync v0.1.0" />
        <SettingRow k="内核 socket" v="见 etc/panel.json: socket" />
        <SettingRow k="存储目录" v="见 etc/panel.json: dataDir" />
        <SettingRow k="访问令牌" v="已配置（脱敏）" />
      </Paper>

      <Paper variant="outlined" sx={{ p: 2 }}>
        <Typography variant="h6" sx={{ fontSize: 16, mb: 1 }}>
          插件
        </Typography>
        {plugins === undefined && <EmptyState text="加载中…" />}
        {plugins !== undefined && plugins.length === 0 && (
          <EmptyState text="pluginBinDir 中未发现插件" />
        )}
        {plugins?.map((p) => (
          <Box key={p.name} sx={{ mb: 1.5 }}>
            <Typography variant="body1" sx={{ fontWeight: 600 }}>
              {p.name} <Typography component="span" variant="caption">v{p.version}</Typography>
            </Typography>
            {p.configSchema !== undefined &&
              Object.keys(p.configSchema).length > 0 &&
              Object.entries(
                (p.configSchema as { properties?: Record<string, { type?: string; description?: string }> })
                  .properties ?? {},
              ).map(([k, v]) => (
                <Typography key={k} variant="body2" color="text.secondary" sx={{ pl: 2 }}>
                  - {k} ({v.type ?? 'any'})
                  {v.description ? `: ${v.description}` : ''}
                </Typography>
              ))}
            <Divider sx={{ mt: 1 }} />
          </Box>
        ))}
        {plugins !== undefined && plugins.length === 0 && <EmptyState text="未发现插件" />}
      </Paper>
    </Box>
  )
}

function SettingRow(props: { k: string; v: string }): React.JSX.Element {
  return (
    <Box sx={{ display: 'flex', gap: 2, py: 0.5 }}>
      <Typography variant="body2" sx={{ width: 120, color: 'text.secondary' }}>
        {props.k}
      </Typography>
      <Typography variant="body2">{props.v}</Typography>
    </Box>
  )
}
