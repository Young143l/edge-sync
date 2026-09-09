import { useState } from 'react'
import { Outlet, NavLink, useLocation } from 'react-router'
import {
  AppBar,
  Box,
  Chip,
  Drawer,
  IconButton,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Toolbar,
  Typography,
  useMediaQuery,
  useTheme,
} from '@mui/material'
import {
  Menu as MenuIcon,
  Dashboard as DashboardIcon,
  Description as LogsIcon,
  Settings as SettingsIcon,
  Refresh as RefreshIcon,
  DarkMode as DarkModeIcon,
  LightMode as LightModeIcon,
  Sync as SyncIcon,
} from '@mui/icons-material'
import { useColorScheme } from '@mui/material/styles'
import { useOverview, useQueryClient } from '../query'

const DRAWER_W = 240

interface NavItem {
  to: string
  label: string
  icon: React.JSX.Element
  match: (p: string) => boolean
}

const navItems: NavItem[] = [
  {
    to: '/',
    label: '仪表盘',
    icon: <DashboardIcon />,
    match: (p: string) => p === '/' || p.startsWith('/task'),
  },
  { to: '/logs', label: '日志', icon: <LogsIcon />, match: (p) => p.startsWith('/logs') },
  { to: '/settings', label: '设置', icon: <SettingsIcon />, match: (p) => p.startsWith('/settings') },
]

export function Layout({ children }: { children: React.ReactNode }): React.JSX.Element {
  const theme = useTheme()
  const isSmall = useMediaQuery(theme.breakpoints.down('md'))
  const [drawerOpen, setDrawerOpen] = useState(false)
  const { mode, setMode } = useColorScheme()
  const { data: tasks } = useOverview()
  const qc = useQueryClient()
  const location = useLocation()

  const syncing = tasks?.filter((t) => t.status === 'syncing').length ?? 0
  const failed = tasks?.filter((t) => t.consecutiveFailures > 0).length ?? 0
  const serviceChip = (): React.JSX.Element | null => {
    if (tasks === undefined || tasks.length === 0) return null
    if (failed > 0) return <Chip size="small" color="error" label={`${failed} 任务异常`} />
    if (syncing > 0)
      return <Chip size="small" color="warning" icon={<SyncIcon />} label={`${syncing} 同步中`} />
    return <Chip size="small" color="success" label="运行中" />
  }

  const drawerContent = (
    <>
      <Toolbar />
      <List>
        {navItems.map((n) => (
          <ListItemButton
            key={n.to}
            component={NavLink}
            to={n.to}
            selected={n.match(location.pathname)}
            onClick={() => setDrawerOpen(false)}
          >
            <ListItemIcon>{n.icon}</ListItemIcon>
            <ListItemText primary={n.label} />
          </ListItemButton>
        ))}
      </List>
      <Box sx={{ mt: 'auto', p: 2 }}>
        <Typography variant="caption" color="text.secondary">
          edge-sync v0.1.0
        </Typography>
      </Box>
    </>
  )

  return (
    <Box sx={{ display: 'flex', minHeight: '100vh' }}>
      <AppBar
        position="fixed"
        sx={{ zIndex: (t) => t.zIndex.drawer + 1 }}
        color="inherit"
        elevation={0}
      >
        <Toolbar sx={{ borderBottom: 1, borderColor: 'divider' }}>
          {isSmall && (
            <IconButton edge="start" onClick={() => setDrawerOpen(true)} sx={{ mr: 1 }}>
              <MenuIcon />
            </IconButton>
          )}
          <Typography variant="h6" sx={{ fontWeight: 600, color: 'primary.main' }}>
            edge-sync
          </Typography>
          <Box sx={{ flexGrow: 1, ml: 2 }}>{serviceChip()}</Box>
          <IconButton title="刷新全部" onClick={() => {
              void qc.invalidateQueries()
            }}>
            <RefreshIcon />
          </IconButton>
          <IconButton title="切换明暗" onClick={() => setMode(mode === 'dark' ? 'light' : 'dark')}>
            {mode === 'dark' ? <LightModeIcon /> : <DarkModeIcon />}
          </IconButton>
        </Toolbar>
      </AppBar>

      {!isSmall ? (
        <Drawer
          variant="permanent"
          sx={{
            width: DRAWER_W,
            flexShrink: 0,
            '& .MuiDrawer-paper': { width: DRAWER_W, boxSizing: 'border-box' },
          }}
        >
          {drawerContent}
        </Drawer>
      ) : (
        <Drawer
          open={drawerOpen}
          onClose={() => setDrawerOpen(false)}
          slotProps={{
            paper: {
              sx: {
                width: `min(78vw, ${DRAWER_W}px)`,
                boxSizing: 'border-box',
              },
            },
          }}
        >
          {drawerContent}
        </Drawer>
      )}

      <Box component="main" sx={{
        flexGrow: 1,
        p: { xs: 1.5, sm: 2, md: 3 },
        width: { md: `calc(100% - ${DRAWER_W}px)` },
        maxWidth: 1400,
      }}>
        <Toolbar />
        {children}
      </Box>
      <Outlet />
    </Box>
  )
}
