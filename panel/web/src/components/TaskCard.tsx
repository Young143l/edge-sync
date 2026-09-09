import { useState } from 'react'
import { useNavigate } from 'react-router'
import {
  Alert, Button, Card, CardActions, CardContent, Dialog, DialogActions,
  DialogContent, DialogContentText, DialogTitle, LinearProgress, Snackbar,
  Typography,
} from '@mui/material'
import { StatusChip } from './StatusChip'
import { useSync, type TaskSummary } from '../query'

/** 仪表盘任务卡片（panel-design 4.3）。 */
export function TaskCard({ task }: { task: TaskSummary }): React.JSX.Element {
  const navigate = useNavigate()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [snack, setSnack] = useState<{ ok: boolean; msg: string } | null>(null)
  const sync = useSync(task.name)

  const failed = task.enabled && task.status === 'idle' && task.consecutiveFailures > 0
  const syncing = task.status === 'syncing'

  const startSync = (): void => {
    setConfirmOpen(false)
    sync.mutate(undefined, {
      onSuccess: () => setSnack({ ok: true, msg: `${task.name} 同步完成` }),
      onError: (e) => setSnack({ ok: false, msg: `同步失败：${(e as Error).message}` }),
    })
  }

  return (
    <Card
      variant="outlined"
      sx={{
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        borderColor: failed ? 'error.main' : 'divider',
      }}
    >
      <CardContent sx={{ flexGrow: 1 }}>
        <Typography variant="h6" component="div" sx={{ mb: 1, fontWeight: 600 }}>
          {task.name}
        </Typography>
        <StatusChip task={task} />
        <Typography variant="body2" color="text.secondary" sx={{ mt: 1.5 }}>
          {task.plugin} · 每 {task.interval}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          上次成功：{timeAgo(task.lastSuccessAt)}
        </Typography>
        <Typography variant="caption" color="text.secondary">
          下次运行：{task.nextRunAt ? shortTime(task.nextRunAt) : '-'}
        </Typography>
        {syncing && (
          <Typography sx={{ mt: 1 }}>
            <LinearProgress />
          </Typography>
        )}
      </CardContent>
      <CardActions sx={{ px: 2, pb: 2 }}>
        <Button
          size="small"
          variant="contained"
          disabled={syncing || !task.enabled}
          onClick={() => setConfirmOpen(true)}
        >
          立即同步
        </Button>
        <Button size="small" onClick={() => navigate(`/task/${task.name}`)}>
          详情
        </Button>
      </CardActions>

      <Dialog open={confirmOpen} onClose={() => setConfirmOpen(false)} maxWidth="xs">
        <DialogTitle>立即同步</DialogTitle>
        <DialogContent>
          <DialogContentText>
            将立即对任务「{task.name}」执行一次同步（等待完成）。
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirmOpen(false)} color="inherit">
            取消
          </Button>
          <Button onClick={startSync} variant="contained" disabled={sync.isPending}>
            {sync.isPending ? '同步中…' : '确认同步'}
          </Button>
        </DialogActions>
      </Dialog>

      <Snackbar
        open={snack !== null}
        autoHideDuration={4000}
        onClose={() => setSnack(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
      >
        <Alert severity={snack?.ok ? 'success' : 'error'} variant="filled">
          {snack?.msg}
        </Alert>
      </Snackbar>
    </Card>
  )
}

function timeAgo(iso: string | undefined): string {
  if (!iso) return '从未'
  const diff = Date.now() - Date.parse(iso)
  if (Number.isNaN(diff)) return iso
  if (diff < 60_000) return `${Math.max(1, Math.floor(diff / 1000))} 秒前`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`
  return `${Math.floor(diff / 86_400_000)} 天前`
}

function shortTime(iso: string): string {
  return iso.replace('T', ' ').slice(11, 19)
}
