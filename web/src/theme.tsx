import { createTheme, ThemeProvider } from '@mui/material/styles'
import { CssBaseline } from '@mui/material'
import type { ReactNode } from 'react'

/**
 * MD 风格主题（teal 系，panel-design 4.2 视觉 token）。
 * 明暗双 scheme：跟随系统 + App Bar 手动切换。
 */

const statusColors = {
  success: { light: '#2E7D32', dark: '#66BB6A' },
  warning: { light: '#ED6C02', dark: '#FFB74D' },
  error: { light: '#D32F2F', dark: '#EF5350' },
} as const

const light = {
  palette: {
    primary: {
      main: '#00695C',
      contrastText: '#FFFFFF',
      container: '#CCE8E4',
    },
    background: { default: '#F4F9F9', paper: '#FFFFFF' },
    text: { primary: '#1A2B29' },
    divider: '#DDE6E4',
    success: { main: statusColors.success.light },
    warning: { main: statusColors.warning.light },
    error: { main: statusColors.error.light },
  },
  shape: {
    borderRadius: 12,
  },
} as const

const dark = {
  palette: {
    primary: {
      main: '#4DB6AC',
      contrastText: '#0B2B27',
      container: '#0F3A35',
    },
    background: { default: '#10201E', paper: '#182B28' },
    text: { primary: '#E0ECEA' },
    divider: '#2A3D3A',
    success: { main: statusColors.success.dark },
    warning: { main: statusColors.warning.dark },
    error: { main: statusColors.error.dark },
  },
  shape: {
    borderRadius: 12,
  },
} as const

const typography = {
  h6: { fontSize: '1.25rem', fontWeight: 600 },
  body2: { fontSize: '0.875rem' },
  caption: { fontSize: '0.75rem' },
  fontFamily: 'Roboto, system-ui, "PingFang SC", "Microsoft YaHei", sans-serif',
} as const

export const theme = createTheme({
  // data 属性选择器模式：支持手动切换（media 模式只随系统，setMode 无效）。
  cssVariables: { colorSchemeSelector: 'data' },
  colorSchemes: { light, dark },
  typography,
})

export function AppTheme({ children }: { children: ReactNode }): ReactNode {
  return (
    <ThemeProvider theme={theme} defaultMode="system">
      <CssBaseline />
      {children}
    </ThemeProvider>
  )
}
