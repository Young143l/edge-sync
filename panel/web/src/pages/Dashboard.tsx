import { Alert, Box, Typography } from '@mui/material'
import { useOverview } from '../query'
import { TaskCard } from '../components/TaskCard'
import { EmptyState, LoadingCards, OfflineBanner } from '../components/Shared'

export function DashboardPage(): React.JSX.Element {
  const { data: tasks, error, refetch } = useOverview()

  const syncing = tasks?.filter((t) => t.status === 'syncing').length ?? 0
  const failed = tasks?.filter((t) => t.consecutiveFailures > 0).length ?? 0

  return (
    <Box>
      <OfflineBanner error={error} onRetry={() => void refetch()} />

      {tasks && tasks.length > 0 && (
        <Alert
          severity={failed > 0 ? 'error' : syncing > 0 ? 'warning' : 'success'}
          variant="outlined"
          sx={{ mb: 2, minHeight: 44 }}
        >
          {tasks.length} 个任务 · {syncing} 同步中
          {failed > 0 ? ` · ${failed} 个失败` : ''} · 服务正常轮询中
        </Alert>
      )}

      {tasks === undefined && !error && <LoadingCards />}

      {tasks !== undefined && tasks.length === 0 && (
        <EmptyState text="还没有任务，用 edge-sync add 添加" />
      )}

      {tasks !== undefined && tasks.length > 0 && (
        <Box
          sx={{
            display: 'grid',
            gap: 2,
            alignItems: 'start',
            gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))',
          }}
        >
          {tasks.map((t) => (
            <TaskCard key={t.name} task={t} />
          ))}
        </Box>
      )}

      {error && tasks === undefined && (
        <Typography color="text.secondary">{String(error)}</Typography>
      )}
    </Box>
  )
}
