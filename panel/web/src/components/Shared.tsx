import { useMemo } from 'react'
import { Box, Alert, Button } from '@mui/material'
import { isOfflineError } from '../api'

/** 内核离线横幅（503 / 网络错误时显示，含重试按钮）。 */
export function OfflineBanner({ error, onRetry }: { error: unknown; onRetry: () => void }): React.JSX.Element | null {
  if (error === undefined || error === null || !isOfflineError(error)) return null
  return (
    <Alert
      severity="error"
      sx={{ mb: 2 }}
      action={
        <Button color="inherit" size="small" onClick={onRetry}>
          重试
        </Button>
      }
    >
      edge-syncd 内核未连接（{errorText(error)}）
    </Alert>
  )
}

export function EmptyState({ text }: { text: string }): React.JSX.Element {
  return (
    <Box sx={{ textAlign: 'center', py: 10, color: 'text.secondary' }}>
      <Box sx={{ fontSize: 48, mb: 1 }}>—</Box>
      {text}
    </Box>
  )
}

/** 骨架加载态（卡片网格占位）。 */
export function LoadingCards({ count = 3 }: { count?: number }): React.JSX.Element {
  const items = useMemo(() => Array.from({ length: count }, () => null), [count])
  return (
    <Box sx={{ display: 'grid', gap: 2, gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))' }}>
      {items.map((_, i) => (
        <Box key={i} sx={{ height: 180, bgcolor: 'action.hover', borderRadius: 3 }} />
      ))}
    </Box>
  )
}

function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/** 字节人性化。 */
export function humanBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}
