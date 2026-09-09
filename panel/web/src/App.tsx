import { useState } from 'react'
import { Route, Routes } from 'react-router'
import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  TextField,
} from '@mui/material'
import { Layout } from './components/Layout'
import { DashboardPage } from './pages/Dashboard'
import { TaskDetailPage } from './pages/TaskDetail'
import { LogsPage } from './pages/Logs'
import { SettingsPage } from './pages/Settings'
import { getToken, setToken, onUnauthorized } from './api'

export function App(): React.JSX.Element {
  const [authed, setAuthed] = useState<boolean>(!!getToken())
  const [loginOpen, setLoginOpen] = useState(false)

  onUnauthorized(() => {
    setAuthed(false)
    setLoginOpen(true)
  })

  const submitToken = (t: string): void => {
    setToken(t)
    setAuthed(true)
    setLoginOpen(false)
    window.location.reload() // 以新 token 重取全部 query
  }

  return (
    <>
      <Layout>
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/task/:name" element={<TaskDetailPage />} />
          <Route path="/logs" element={<LogsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Routes>
      </Layout>
      <LoginDialog
        open={loginOpen || !authed}
        canClose={authed}
        onSubmit={submitToken}
        onClose={() => setLoginOpen(false)}
      />
    </>
  )
}

function LoginDialog(props: {
  open: boolean
  canClose: boolean
  onSubmit: (token: string) => void
  onClose: () => void
}): React.JSX.Element {
  const [token, setToken] = useState('')
  return (
    <Dialog open={props.open} maxWidth="xs" fullWidth>
      <DialogTitle>访问令牌</DialogTitle>
      <DialogContent sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
        <DialogContentText>输入面板访问令牌（见 etc/panel.json）</DialogContentText>
        <TextField
          autoFocus
          label="token"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') props.onSubmit(token)
          }}
        />
      </DialogContent>
      <DialogActions>
        <Button variant="contained" onClick={() => props.onSubmit(token)} disabled={!token}>
          确定
        </Button>
        {props.canClose && (
          <Button onClick={props.onClose} color="inherit">
            取消
          </Button>
        )}
      </DialogActions>
    </Dialog>
  )
}
