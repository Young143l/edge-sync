import { QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'
import type {
  FileListEntry,
  FileVersionEntry,
  LogEntry,
  PluginInfo,
  SettingsInfo,
  TaskDetail,
  TaskSummary,
  VersionInfo,
} from '@edge-sync/protocol'

export type {
  FileListEntry,
  FileVersionEntry,
  LogEntry,
  PluginInfo,
  SettingsInfo,
  TaskDetail,
  TaskSummary,
  VersionInfo,
} from '@edge-sync/protocol'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false },
  },
})

export function useOverview() {
  return useQuery({
    queryKey: ['overview'],
    queryFn: () => api<TaskSummary[]>('/api/overview'),
    refetchInterval: (q) =>
      q.state.data?.some((t) => t.status === 'syncing') ? 2000 : 5000,
  })
}

export function useTask(name: string | undefined) {
  return useQuery({
    queryKey: ['task', name],
    enabled: !!name,
    queryFn: () => api<TaskDetail>(`/api/tasks/${name}`),
    refetchInterval: (q) => (q.state.data?.status === 'syncing' ? 2000 : 5000),
  })
}

export function useHistory(name: string | undefined) {
  return useQuery({
    queryKey: ['history', name],
    enabled: !!name,
    queryFn: () => api<VersionInfo[]>(`/api/tasks/${name}/history`),
  })
}

export function useFiles(name: string | undefined, version: string, path: string) {
  return useQuery({
    queryKey: ['files', name, version, path],
    enabled: !!name,
    queryFn: () =>
      api<FileListEntry[]>(
        `/api/tasks/${name}/files?version=${encodeURIComponent(version)}&path=${encodeURIComponent(path)}`,
      ),
  })
}

export function useFileVersions(name: string | undefined, path: string) {
  return useQuery({
    queryKey: ['fileVersions', name, path],
    enabled: !!name && !!path,
    queryFn: () =>
      api<FileVersionEntry[]>(
        `/api/tasks/${name}/file-versions?path=${encodeURIComponent(path)}`,
      ),
  })
}

export function useLogs(n: number, task: string, enabled: boolean) {
  return useQuery({
    queryKey: ['logs', n, task],
    enabled,
    queryFn: () => api<LogEntry[]>(`/api/logs?n=${n}&task=${encodeURIComponent(task)}`),
    refetchInterval: enabled ? 5000 : false,
  })
}

export function usePlugins() {
  return useQuery({
    queryKey: ['plugins'],
    queryFn: () => api<PluginInfo[]>('/api/plugins'),
    staleTime: Infinity,
  })
}

/** 手动触发同步（同步等待完成）。 */
export function useSync(name: string | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      await api<{ triggered: boolean }>(`/api/tasks/${name}/sync`, { method: 'POST' })
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['overview'] })
      void qc.invalidateQueries({ queryKey: ['task', name] })
      void qc.invalidateQueries({ queryKey: ['history', name] })
    },
  })
}

export { useQueryClient }

export function useSettingsInfo() {
  return useQuery({
    queryKey: ['settings'],
    queryFn: () => api<SettingsInfo>('/api/settings/info'),
    staleTime: 10_000,
  })
}

export function useClearCache() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (task: string) =>
      api<{ ok: boolean }>('/api/settings/cache/clear', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task }),
      }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['settings'] }),
  })
}

export function usePanelRestart() {
  return useMutation({ mutationFn: () => api<{ ok: boolean }>('/api/settings/restart-panel', { method: 'POST' }) })
}

export function useSyncdRestart() {
  return useMutation({ mutationFn: () => api<{ ok: boolean }>('/api/settings/restart-syncd', { method: 'POST' }) })
}
