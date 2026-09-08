/** 终端表格与格式化 helper（无第三方依赖）。 */

export function pad(s: string, n: number): string {
  return s.length >= n ? s : s + ' '.repeat(n - s.length)
}

export function padStart(s: string, n: number): string {
  return s.length >= n ? s : ' '.repeat(n - s.length) + s
}

export function row(cells: [string, number][]): string {
  return cells.map(([v, w]) => pad(v, w)).join('  ').trimEnd()
}

export function hr(width: number): string {
  return '-'.repeat(width)
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

/** 相对时间（粗粒度）。 */
export function timeAgo(iso: string | undefined): string {
  if (!iso) return 'never'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return iso
  const diff = Date.now() - t
  if (diff < 60_000) return `${Math.max(1, Math.floor(diff / 1000))}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  return `${Math.floor(diff / 86_400_000)}d ago`
}

/** 状态着色（无 ANSI 依赖时退化为纯文本）。 */
export function statusBadge(s: string): string {
  const useColor = process.stdout.isTTY
  const wrap = (code: string) => (useColor ? `\x1b[${code}m${s}\x1b[0m` : s)
  switch (s) {
    case 'syncing':
      return wrap('33') // yellow
    case 'failed':
      return wrap('31') // red
    case 'paused':
      return wrap('90') // gray
    default:
      return wrap('32') // green
  }
}
