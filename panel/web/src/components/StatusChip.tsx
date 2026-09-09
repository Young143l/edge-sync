import { Chip, CircularProgress } from '@mui/material'
import { Sync as SyncIcon, CheckCircle as CheckCircleIcon, Error as ErrorIcon, Pause as PauseIcon } from '@mui/icons-material'
import type { TaskSummary } from '../query'

/**
 * 任务状态 Chip（panel-design：正常 / 同步中 / 失败(重试N次) / 暂停）。
 * 失败判定：status==='idle' 且 consecutiveFailures>0。
 */
export function StatusChip({ task }: { task: TaskSummary }): React.JSX.Element {
  const status = task.enabled
    ? task.status === 'syncing'
      ? 'syncing'
      : task.consecutiveFailures > 0
        ? 'failed'
        : 'ok'
    : 'paused'

  if (status === 'syncing') {
    return (
      <Chip
        size="small"
        color="warning"
        icon={<CircularProgress size={14} color="inherit" />}
        label="同步中"
      />
    )
  }
  if (status === 'failed') {
    return <Chip size="small" color="error" icon={<ErrorIcon />} label={`失败（重试 ${task.consecutiveFailures} 次）`} />
  }
  if (status === 'paused') {
    return <Chip size="small" color="default" icon={<PauseIcon />} label="已暂停" />
  }
  return <Chip size="small" color="success" icon={<CheckCircleIcon />} label="正常" />
}
