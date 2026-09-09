import { useState } from 'react'
import {
  Box, Checkbox, FormControlLabel, MenuItem, Paper, TextField, Typography,
} from '@mui/material'
import { useLogs, useOverview, type LogEntry } from '../query'
import { EmptyState, OfflineBanner } from '../components/Shared'

export function LogsPage(): React.JSX.Element {
  const [n, setN] = useState(100)
  const [task, setTask] = useState('')
  const [auto, setAuto] = useState(true)
  const [levelFilter, setLevelFilter] = useState('auto')

  const { data: tasks } = useOverview()
  const { data: logs, error, refetch } = useLogs(n, task, auto)

  const filtered = (logs ?? []).filter(
    (l) => levelFilter === 'auto' || l.level.toLowerCase() === levelFilter,
  )

  return (
    <Box>
      <OfflineBanner error={error} onRetry={() => void refetch()} />
      <Box sx={{ display: 'flex', gap: 2, mb: 2, flexWrap: 'wrap', alignItems: 'center' }}>
        <TextField
          select
          size="small"
          label="任务"
          value={task}
          onChange={(e) => setTask(e.target.value)}
          sx={{ minWidth: 160 }}
        >
          <MenuItem value="">全部</MenuItem>
          {(tasks ?? []).map((t) => (
            <MenuItem key={t.name} value={t.name}>
              {t.name}
            </MenuItem>
          ))}
        </TextField>
        <TextField
          select
          size="small"
          label="级别"
          value={levelFilter}
          onChange={(e) => setLevelFilter(e.target.value)}
          sx={{ minWidth: 120 }}
        >
          <MenuItem value="auto">全部</MenuItem>
          <MenuItem value="info">INFO</MenuItem>
          <MenuItem value="warn">WARN</MenuItem>
          <MenuItem value="error">ERROR</MenuItem>
        </TextField>
        <FormControlLabel
          control={<Checkbox checked={auto} onChange={(e) => setAuto(e.target.checked)} />}
          label="自动刷新（5s）"
        />
      </Box>

      <Paper variant="outlined" sx={{ p: 2, maxHeight: '70vh', overflow: 'auto' }}>
        {filtered.length === 0 ? (
          <EmptyState text="暂无日志" />
        ) : (
          filtered.map((l, i) => <LogLine key={i} l={l} />)
        )}
      </Paper>
    </Box>
  )
}

function LogLine({ l }: { l: LogEntry }): React.JSX.Element {
  const color =
    l.level === 'warn' ? 'warning.main' : l.level === 'error' ? 'error.main' : 'text.primary'
  return (
    <Typography
      variant="body2"
      sx={{ fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-all', color }}
    >
      <span style={{ opacity: 0.6 }}>
        {l.time} {l.level.toUpperCase().padEnd(5)}
      </span>{' '}
      {l.msg}
      {l.attrs ? `  ${l.attrs}` : ''}
    </Typography>
  )
}
