import {
  Alert,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  Divider,
  LinearProgress,
  Paper,
  Snackbar,
  Typography,
} from '@mui/material'
import { useState } from 'react'
import { useClearCache, usePanelRestart, usePlugins, useSettingsInfo, useSyncdRestart } from '../query'
import { EmptyState } from '../components/Shared'

function humanBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

function relativeTime(iso: string | undefined): string {
  if (!iso) return '未知'
  const d = Date.now() - new Date(iso).getTime()
  if (d < 0) return '刚刚'
  const s = Math.floor(d / 1000)
  if (s < 60) return `${s} 秒`
  if (s < 3600) return `${Math.floor(s / 60)} 分钟`
  if (s < 86400) return `${Math.floor(s / 3600)} 小时`
  return `${Math.floor(s / 86400)} 天`
}

function SectionTitle({ children }: { children: React.ReactNode }): React.JSX.Element {
  return (
    <Typography variant="h6" sx={{ fontSize: 16, mb: 1.5, fontWeight: 600 }}>
      {children}
    </Typography>
  )
}

/** 存储占用条（dataBytes 主条 + cacheBytes 副条） */
function StorageBar({ label, data, cache, max }: { label: string; data: number; cache: number; max: number }): React.JSX.Element {
  const dPct = max > 0 ? (data / max) * 100 : 0
  const cPct = max > 0 ? (cache / max) * 100 : 0
  return (
    <Box sx={{ mb: 1.5 }}>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: 0.5 }}>
        <Typography variant="body2" sx={{ fontWeight: 600 }}>
          {label}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {humanBytes(data)}{cache > 0 ? ` + 缓存 ${humanBytes(cache)}` : ''}
        </Typography>
      </Box>
      <Box sx={{ position: 'relative', height: 8, borderRadius: 4, bgcolor: 'action.hover', overflow: 'hidden' }}>
        <Box sx={{ position: 'absolute', inset: 0, width: `${dPct}%`, bgcolor: 'primary.main' }} />
        <Box sx={{ position: 'absolute', top: 0, bottom: 0, left: `${dPct}%`, width: `${cPct}%`, bgcolor: 'secondary.main', opacity: 0.55 }} />
      </Box>
    </Box>
  )
}

/** 确认对话框 */
function ConfirmDialog({ open, title, text, onClose, onConfirm, loading }: {
  open: boolean
  title: string
  text: string
  onClose: () => void
  onConfirm: () => void
  loading?: boolean
}): React.JSX.Element {
  return (
    <Dialog open={open} onClose={onClose}>
      <DialogTitle>{title}</DialogTitle>
      <DialogContent>
        <DialogContentText>{text}</DialogContentText>
        {loading && <LinearProgress sx={{ mt: 2 }} />}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>取消</Button>
        <Button color="warning" variant="contained" onClick={onConfirm} disabled={loading}>
          确认执行
        </Button>
      </DialogActions>
    </Dialog>
  )
}

/** 设置页：系统概览 / 存储占用 / 同步活动 / 插件 / 维护 / 会话。 */
export function SettingsPage(): React.JSX.Element {
  const { data: info } = useSettingsInfo()
  const { data: plugins } = usePlugins()
  const clearCache = useClearCache()
  const restartPanel = usePanelRestart()
  const restartSyncd = useSyncdRestart()

  const [confirm, setConfirm] = useState<{ title: string; text: string; action: () => void } | null>(null)
  const [snack, setSnack] = useState('')

  const maxBytes = Math.max(1, ...(info?.tasks ?? []).map((t) => t.dataBytes + t.cacheBytes))
  const totalVersions = info?.tasks.reduce((a, t) => a + t.versions, 0) ?? 0
  const totalCache = info?.tasks.reduce((a, t) => a + t.cacheBytes, 0) ?? 0

  return (
    <Box>
      {!info && <LinearProgress sx={{ mb: 2 }} />}
      {info === undefined && <EmptyState text="加载中…" />}

      {/* 系统概览 */}
      {info && (
        <Paper variant="outlined" sx={{ p: 2, mb: 2 }}>
          <SectionTitle>系统概览</SectionTitle>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 3 }}>
            {[
              { k: '版本', v: `edge-sync v${info.panelVersion}` },
              { k: '内核运行', v: relativeTime(info.syncdStartedAt) },
              { k: '任务数', v: String(info.tasks.length) },
              { k: '版本总数', v: String(totalVersions) },
              { k: '数据总量', v: humanBytes(info.totalDataBytes) },
              { k: '缓存总量', v: humanBytes(totalCache) },
            ].map((x) => (
              <Box key={x.k}>
                <Typography variant="caption" color="text.secondary">
                  {x.k}
                </Typography>
                <Typography variant="body1" sx={{ fontWeight: 600 }}>
                  {x.v}
                </Typography>
              </Box>
            ))}
          </Box>
          <Typography variant="caption" color="text.secondary" sx={{ mt: 1.5, display: 'block' }}>
            面板 {info.panelVersion} · Go {info.goVersion.replace(/^go/, '')} · 数据目录占用为硬链接去重后的真实磁盘占用
          </Typography>
        </Paper>
      )}

      {/* 存储占用 */}
      {info && info.tasks.length > 0 && (
        <Paper variant="outlined" sx={{ p: 2, mb: 2 }}>
          <SectionTitle>存储占用</SectionTitle>
          {info.tasks.map((t) => (
            <StorageBar key={t.name} label={t.name} data={t.dataBytes} cache={t.cacheBytes} max={maxBytes} />
          ))}
          <Typography variant="caption" color="text.secondary">
            深色为版本数据（硬链接去重），浅色为插件缓存（可在下方维护区清理）
          </Typography>
        </Paper>
      )}

      {/* 同步活动（每任务统计行） */}
      {info && info.tasks.length > 0 && (
        <Paper variant="outlined" sx={{ p: 2, mb: 2 }}>
          <SectionTitle>插件</SectionTitle>
          {plugins === undefined && <EmptyState text="加载中…" />}
          {plugins !== undefined && plugins.length === 0 && <EmptyState text="pluginBinDir 中未发现插件" />}
          {plugins?.map((p, i) => (
            <Box key={p.name}>
              {i > 0 && <Divider sx={{ my: 1 }} />}
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
            </Box>
          ))}
        </Paper>
      )}

      {/* 维护 */}
      <Paper variant="outlined" sx={{ p: 2, mb: 2 }}>
        <SectionTitle>维护</SectionTitle>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>
          重启均为软重启：进程退出后由 systemd 在约 5 秒内自动拉起，期间面板短暂 503。
        </Typography>
        {info?.tasks
          .filter((t) => t.cacheBytes > 0)
          .map((t) => (
            <Box key={t.name} sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
              <Typography variant="body2">
                清理 <b>{t.name}</b> 插件缓存（{humanBytes(t.cacheBytes)}）
              </Typography>
              <Button
                size="small"
                variant="outlined"
                onClick={() =>
                  setConfirm({
                    title: '清理插件缓存',
                    text: `删除 ${t.name} 的插件缓存（git 浅克隆仓库，${humanBytes(t.cacheBytes)}）。下次轮询将自动重建，期间可能消耗较多网络流量。`,
                    action: () =>
                      clearCache.mutate(t.name, {
                        onSuccess: () => {
                          setSnack(`${t.name} 缓存已清理`)
                          setConfirm(null)
                        },
                        onError: () => setConfirm(null),
                      }),
                  })
                }
              >
                清理
              </Button>
            </Box>
          ))}
        <Divider sx={{ my: 1.5 }} />
        <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
          <Button
            variant="outlined"
            color="warning"
            onClick={() =>
              setConfirm({
                title: '重启面板',
                text: '面板进程将退出并由 systemd 拉起（约 5 秒），期间页面会短暂失联。',
                action: () =>
                  restartPanel.mutate(undefined, {
                    onSuccess: () => {
                      setSnack('面板正在重启，5 秒后刷新页面')
                      setConfirm(null)
                    },
                  }),
              })
            }
          >
            重启面板
          </Button>
          <Button
            variant="outlined"
            color="warning"
            onClick={() =>
              setConfirm({
                title: '重启内核',
                text: '内核将优雅停止并由 systemd 拉起，所有任务轮询短暂中断。',
                action: () =>
                  restartSyncd.mutate(undefined, {
                    onSuccess: () => {
                      setSnack('内核正在重启')
                      setConfirm(null)
                    },
                    onError: () => setConfirm(null),
                  }),
              })
            }
          >
            重启内核
          </Button>
        </Box>
      </Paper>

      {/* 关于 */}
      <Paper variant="outlined" sx={{ p: 2, mb: 2 }}>
        <SectionTitle>关于</SectionTitle>
        <Typography variant="body2" color="text.secondary" component="div">
          edge-sync — 边缘设备插件化同步备份服务（MIT License）。
          <br />
          文档与源码：
          <a href="https://github.com/Young143l/edge-sync" target="_blank" rel="noreferrer" style={{ color: 'inherit' }}>
            github.com/Young143l/edge-sync
          </a>
        </Typography>
      </Paper>

      {/* 会话 */}
      <Paper variant="outlined" sx={{ p: 2 }}>
        <SectionTitle>会话</SectionTitle>
        <Button
          variant="outlined"
          color="error"
          onClick={() => {
            localStorage.removeItem('edge-sync-token')
            document.cookie = 'edge-sync-token=; Max-Age=0; path=/'
            location.reload()
          }}
        >
          退出登录
        </Button>
      </Paper>

      {/* 配置修改提示 */}
      <Alert severity="info" variant="outlined" sx={{ mt: 2 }}>
        同步任务与保留策略的修改请使用 CLI（edge-sync add/edit/remove）或直接编辑 etc/config.yaml 后执行
        edge-sync reload。
      </Alert>

      <ConfirmDialog
        open={confirm !== null}
        title={confirm?.title ?? ''}
        text={confirm?.text ?? ''}
        onClose={() => setConfirm(null)}
        onConfirm={() => confirm?.action()}
        loading={clearCache.isPending || restartPanel.isPending || restartSyncd.isPending}
      />
      <Snackbar open={snack !== ''} autoHideDuration={4000} onClose={() => setSnack('')} message={snack} />
    </Box>
  )
}
