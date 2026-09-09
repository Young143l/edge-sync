import { useMemo, useState } from 'react'
import { Link as RouterLink, useParams, useSearchParams } from 'react-router'
import {
  Alert, Breadcrumbs, Box, Button, Chip, Dialog, DialogActions, DialogContent,
  DialogContentText, DialogTitle, Divider, Drawer, IconButton, Link, Paper, Snackbar,
  Table, TableBody, TableCell, TableContainer, TableHead, TableRow, Tabs, Tab,
  Typography,
} from '@mui/material'
import {
  ArrowBack as ArrowBackIcon,
  Download as DownloadIcon,
  History as HistoryIcon,
  InsertDriveFile as FileIcon,
  Folder as FolderIcon,
  Close as CloseIcon,
} from '@mui/icons-material'
import { humanBytes, EmptyState } from '../components/Shared'
import { StatusChip } from '../components/StatusChip'
import {
  useHistory,
  useFiles,
  useFileVersions,
  useLogs,
  useSync,
  useTask,
  type FileListEntry,
  type VersionInfo,
} from '../query'

const API_TOKEN_KEY = 'edge-sync-token'

export function TaskDetailPage(): React.JSX.Element {
  const { name = '' } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const { data: d, error, refetch } = useTask(name)
  const sync = useSync(name)
  const overviewLogs = useLogs(200, name, true)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [snack, setSnack] = useState<{ ok: boolean; msg: string } | null>(null)

  const version = searchParams.get('version') ?? 'current'
  const path = searchParams.get('path') ?? ''
  const tab = (searchParams.get('tab') as 'overview' | 'files' | null) ?? 'overview'

  const setTab = (v: 'overview' | 'files'): void => {
    const next = new URLSearchParams(searchParams)
    next.set('tab', v)
    setSearchParams(next, { replace: true })
  }
  const pickVersion = (v: string): void => {
    const next = new URLSearchParams(searchParams)
    next.set('tab', 'files')
    next.set('version', v)
    setSearchParams(next)
  }
  const navigatePath = (p: string): void => {
    const next = new URLSearchParams(searchParams)
    if (p === '') next.delete('path')
    else next.set('path', p)
    setSearchParams(next, { replace: true })
  }

  if (error !== undefined && error !== null) {
    return (
      <Alert
        severity={error instanceof Error && error.message.includes('not found') ? 'warning' : 'error'}
        action={<Button onClick={() => void refetch()}>重试</Button>}
      >
        {error instanceof Error ? error.message : String(error)}
      </Alert>
    )
  }
  if (d === undefined) return <EmptyState text="加载中…" />

  const startSync = (): void => {
    setConfirmOpen(false)
    sync.mutate(undefined, {
      onSuccess: () => {
        setSnack({ ok: true, msg: '同步完成' })
        const next = new URLSearchParams(searchParams)
        next.set('tab', 'files')
        next.set('version', 'current')
        setSearchParams(next)
      },
      onError: (e) => setSnack({ ok: false, msg: `同步失败：${(e as Error).message}` }),
    })
  }

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: { xs: 1, sm: 1.5 }, mb: 1, flexWrap: 'wrap' }}>
        <IconButton component={RouterLink} to="/" size="small" aria-label="返回">
          <ArrowBackIcon />
        </IconButton>
        <Typography sx={{ fontWeight: 600, typography: { xs: 'h6', md: 'h5' } }}>
          {name}
        </Typography>
        <StatusChip task={d} />
        <Box sx={{ flexGrow: 1 }} />
        <Button
          size="small"
          variant="contained"
          disabled={d.status === 'syncing' || !d.enabled}
          onClick={() => setConfirmOpen(true)}
        >
          立即同步
        </Button>
      </Box>
      <Typography
        variant="body2"
        color="text.secondary"
        sx={{ mb: 2, typography: { xs: 'caption', sm: 'body2' } }}
      >
        {d.plugin} · 每 {d.interval} · 下次运行{' '}
        {d.nextRunAt ? shortTime(d.nextRunAt) : '-'} · 统计：{d.stats.totalSyncs} 次同步 /
        {d.stats.totalFiles} 文件 / {humanBytes(d.stats.totalBytes)} · 保留 keepLast=
        {d.retention.keepLast}
        {d.retention.keepDays > 0 ? ` / keepDays=${d.retention.keepDays}` : ''}
      </Typography>

      <Tabs
        value={tab}
        onChange={(_, v) => {
          const next = new URLSearchParams(searchParams)
          next.set('tab', v)
          setSearchParams(next, { replace: true })
        }}
        sx={{ mb: 2 }}
      >
        <Tab label="概览" value="overview" />
        <Tab label="版本与文件" value="files" />
      </Tabs>

      {tab === 'overview' && (
        <OverviewTab name={name} onPickVersion={pickVersion} logs={overviewLogs} />
      )}
      {tab === 'files' && (
        <FilesTab name={name} version={version} path={path} onNavigate={navigatePath} onPickVersion={pickVersion} />
      )}

      <Dialog open={confirmOpen} onClose={() => setConfirmOpen(false)} maxWidth="xs">
        <DialogTitle>立即同步</DialogTitle>
        <DialogContent>
          <DialogContentText>
            将立即对任务「{name}」执行一次同步（等待完成）。
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
    </Box>
  )
}

// ---------- 概览 Tab ----------

function OverviewTab(props: {
  name: string
  onPickVersion: (v: string) => void
  logs: ReturnType<typeof useLogs> | undefined
}): React.JSX.Element {
  const { data: versions, isLoading } = useHistory(props.name)
  const logEntries = props.logs?.data ?? []
  const syncRecords = useMemo(
    () => logEntriesFiltered(logEntries),
    [logEntries],
  )

  return (
    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '3fr 2fr' }, gap: 2 }}>
      <Paper variant="outlined" sx={{ p: 2 }}>
        <Typography variant="h6" sx={{ mb: 1.5, fontSize: 16 }}>
          版本时间线
        </Typography>
        {isLoading && <EmptyState text="加载中…" />}
        {!isLoading && versions !== undefined && versions.length === 0 && (
          <EmptyState text="首次同步尚未完成" />
        )}
        {versions?.map((v, i) => (
          <TimelineItem
            key={v.version}
            version={v.version}
            isCurrent={v.isCurrent}
            files={v.files}
            bytes={v.bytes}
            onPick={() => props.onPickVersion(v.version)}
          />
        ))}
      </Paper>
      <Paper variant="outlined" sx={{ p: 2 }}>
        <Typography variant="h6" sx={{ mb: 1.5, fontSize: 16 }}>
          最近同步记录
        </Typography>
        {syncRecords.length === 0 ? (
          <Typography color="text.secondary" variant="body2">
            暂无记录
          </Typography>
        ) : (
          syncRecords.map((l, i) => (
            <Typography
              key={i}
              variant="body2"
              sx={{ fontFamily: 'monospace', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}
            >
              {shortTime(l.time)} {l.msg} {l.attrs ?? ''}
            </Typography>
          ))
        )}
      </Paper>
    </Box>
  )
}

function logEntriesFiltered(
  logs: { msg: string; attrs?: string; time: string }[],
): { msg: string; attrs?: string; time: string }[] {
  return logs.filter(
    (l) => l.msg === 'synced' || l.msg === 'no change' || l.msg === 'sync failed',
  )
}

function TimelineItem(props: {
  version: string
  isCurrent: boolean
  files: number
  bytes: number
  onPick: () => void
}): React.JSX.Element {
  return (
    <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 1.5, pb: 1.5 }}>
      <Box
        sx={{
          width: 10,
          height: 10,
          borderRadius: '50%',
          mt: '6px',
          bgcolor: props.isCurrent ? 'primary.main' : 'divider',
          flexShrink: 0,
        }}
      />
      <Box>
        <Link
          component="button"
          variant="body2"
          sx={{ fontFamily: 'monospace', fontWeight: props.isCurrent ? 700 : 400, cursor: 'pointer' }}
          onClick={props.onPick}
        >
          {props.version}
        </Link>
        {props.isCurrent && (
          <Chip size="small" label="当前" color="primary" sx={{ ml: 1, height: 20 }} />
        )}
        <Typography variant="caption" color="text.secondary" sx={{ display: 'block' }}>
          {props.files} 文件 · {humanBytes(props.bytes)}
        </Typography>
      </Box>
    </Box>
  )
}

// ---------- 版本与文件 Tab ----------

function FilesTab(props: {
  name: string
  version: string
  path: string
  onNavigate: (path: string) => void
  onPickVersion: (version: string) => void
}): React.JSX.Element {
  const { data: versions } = useHistory(props.name)
  const { data: files, error } = useFiles(props.name, props.version, props.path)
  const [fileVersionsOpen, setFileVersionsOpen] = useState<string | null>(null)
  const [zipConfirm, setZipConfirm] = useState(false)
  const [zipMeta, setZipMeta] = useState<{ size: number; suggest: boolean }>({ size: 0, suggest: false })

  const crumbs = useMemoPath(props.path)

  const openZip = async (): Promise<void> => {
    try {
      const resp = await fetch(`/api/tasks/${props.name}/archive?version=${encodeURIComponent(props.version)}`, {
        headers: { Authorization: `Bearer ${localStorage.getItem('edge-sync-token') ?? ''}` },
      })
      if (!resp.ok) {
        setZipMeta({ size: 0, suggest: false })
        setZipConfirm(true)
        return
      }
      // 只取响应头做确认，不真正消费 body。
      const bytes = Number.parseInt(resp.headers.get('X-Edge-Sync-Bytes') ?? '0', 10)
      const suggest = resp.headers.get('X-Edge-Sync-Suggest') === 'rsync'
      setZipMeta({ size: bytes, suggest })
      setZipConfirm(true)
    } catch {
      setZipConfirm(false)
    }
  }

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', mb: 1, gap: 1, flexWrap: 'wrap' }}>
        <Box sx={{ display: 'flex', gap: 0.5, alignItems: 'center', flexWrap: 'wrap' }}>
          {versions?.slice(0, 8).map((v) => (
            <Chip
              key={v.version}
              size="small"
              label={v.isCurrent ? `${v.version} (当前)` : v.version}
              color={props.version === v.version ? 'primary' : 'default'}
              variant={props.version === v.version ? 'filled' : 'outlined'}
              onClick={() => props.onPickVersion(v.version)}
            />
          ))}
        </Box>
        <Box sx={{ flexGrow: 1 }} />
        <Button size="small" variant="outlined" startIcon={<DownloadIcon />} onClick={() => void openZip()}>
          下载整版本 zip
        </Button>
      </Box>

      <Breadcrumbs sx={{ mb: 1 }}>
        <Link component="button" onClick={() => props.onNavigate('')} sx={{ cursor: 'pointer' }}>
          全部文件
        </Link>
        {crumbs.map((c, i) => (
          <Link
            key={c.path}
            component="button"
            sx={{ cursor: 'pointer' }}
            color={i === crumbs.length - 1 ? 'text.primary' : 'inherit'}
            onClick={() => props.onNavigate(c.path)}
          >
            {c.name}
          </Link>
        ))}
      </Breadcrumbs>

      {error !== undefined && error !== null && error instanceof Error && (
        <Alert severity="warning" sx={{ mb: 1 }}>
          {error.message}
        </Alert>
      )}

      <TableContainer component={Paper} variant="outlined">
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>名称</TableCell>
              <TableCell align="right">大小</TableCell>
              <TableCell>修改时间</TableCell>
              <TableCell align="right">操作</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {(files ?? []).map((f) => (
              <FileRow
                key={f.name}
                f={f}
                name={props.name}
                version={props.version}
                base={props.path}
                onNavigate={props.onNavigate}
                onHistory={() => setFileVersionsOpen(f.name)}
              />
            ))}
          </TableBody>
        </Table>
      </TableContainer>

      <Drawer
        anchor="right"
        open={fileVersionsOpen !== null}
        onClose={() => setFileVersionsOpen(null)}
        slotProps={{ paper: { sx: { width: { xs: '100vw', sm: 380 }, p: 2 } } }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center' }}>
          <Typography variant="h6" sx={{ flexGrow: 1, fontSize: 16, wordBreak: 'break-all' }}>
            文件历史：{fileVersionsOpen}
          </Typography>
          <IconButton onClick={() => setFileVersionsOpen(null)}>
            <CloseIcon />
          </IconButton>
        </Box>
        <Divider sx={{ my: 1 }} />
        {fileVersionsOpen !== null && (
          <FileVersionsPanel
            name={props.name}
            path={joinPath(props.path, fileVersionsOpen)}
          />
        )}
      </Drawer>

      <Dialog open={zipConfirm} onClose={() => setZipConfirm(false)} maxWidth="xs">
        <DialogTitle>下载整版本 zip</DialogTitle>
        <DialogContent>
          <DialogContentText component="div">
            {zipMeta.size > 0 && (
              <>
                预估体积：{humanBytes(zipMeta.size)}
                {zipMeta.suggest && (
                  <Alert severity="info" sx={{ mt: 1 }}>
                    体积较大（&gt;500MB），建议改用 rsync/scp 直接拷贝。
                  </Alert>
                )}
              </>
            )}
            浏览器将开始下载 zip 文件。
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setZipConfirm(false)} color="inherit">
            取消
          </Button>
          <Button
            onClick={() => {
              setZipConfirm(false)
              window.location.href = `/api/tasks/${props.name}/archive?version=${encodeURIComponent(props.version)}`
            }}
            variant="contained"
          >
            开始下载
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}

function FileRow(props: {
  f: FileListEntry
  name: string
  version: string
  base: string
  onNavigate: (path: string) => void
  onHistory: () => void
}): React.JSX.Element {
  const full = joinPath(props.base, props.f.name)
  return (
    <TableRow hover>
      <TableCell>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {props.f.isDir ? (
            <FolderIcon color="primary" fontSize="small" />
          ) : (
            <FileIcon fontSize="small" />
          )}
          {props.f.isDir ? (
            <Link component="button" onClick={() => props.onNavigate(full)} sx={{ cursor: 'pointer' }}>
              {props.f.name}
            </Link>
          ) : (
            props.f.name
          )}
        </Box>
      </TableCell>
      <TableCell align="right">{props.f.isDir ? '—' : humanBytes(props.f.size)}</TableCell>
      <TableCell>{props.f.mtime ? props.f.mtime.replace('T', ' ').slice(0, 19) : '—'}</TableCell>
      <TableCell align="right">
        {!props.f.isDir && (
          <>
            <IconButton
              size="small"
              title="下载"
              href={`/api/tasks/${props.name}/download?version=${encodeURIComponent(props.version)}&path=${encodeURIComponent(full)}`}
            >
              <DownloadIcon fontSize="small" />
            </IconButton>
            <IconButton size="small" title="历史" onClick={props.onHistory}>
              <HistoryIcon fontSize="small" />
            </IconButton>
          </>
        )}
      </TableCell>
    </TableRow>
  )
}

function FileVersionsPanel(props: { name: string; path: string }): React.JSX.Element {
  const { data } = useFileVersions(props.name, props.path)
  if (data === undefined) return <Typography color="text.secondary">加载中…</Typography>
  if (data.length === 0) {
    return <Typography color="text.secondary">该文件自首次同步以来未发生变更</Typography>
  }
  return (
    <>
      {data.map((v) => (
        <Box key={v.version} sx={{ mb: 1.5 }}>
          <Typography variant="body2" sx={{ fontFamily: 'monospace' }}>
            {v.version}
          </Typography>
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block' }}>
            {v.size !== undefined ? humanBytes(v.size) : ''} · {v.fingerprint.slice(0, 12)}
          </Typography>
          <Button
            size="small"
            variant="outlined"
            startIcon={<DownloadIcon />}
            href={`/api/tasks/${props.name}/download?version=${encodeURIComponent(v.version)}&path=${encodeURIComponent(props.path)}`}
          >
            下载此版本
          </Button>
          <Divider sx={{ mt: 1 }} />
        </Box>
      ))}
    </>
  )
}

function useMemoPath(path: string): { name: string; path: string }[] {
  const out: { name: string; path: string }[] = []
  let acc = ''
  for (const seg of path.split('/').filter(Boolean)) {
    acc = acc === '' ? seg : `${acc}/${seg}`
    out.push({ name: seg, path: acc })
  }
  return out
}

function joinPath(base: string, name: string): string {
  return base === '' ? name : `${base}/${name}`
}

function shortTime(iso: string): string {
  return iso.replace('T', ' ').slice(11, 19)
}
