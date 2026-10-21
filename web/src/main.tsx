import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import './styles/base.css'
import './styles/app.css'
import './styles/settings.css'
import './styles/files.css'
import { AppProvider, applyTheme, storedThemeMode, useApp } from './lib/app-context'
import { UploadProvider } from './lib/uploads'
import { Loading, ToastProvider } from './components/ui'
import Layout from './components/Layout'
import Login from './pages/Login'
import Browser from './pages/Browser'
import Shared from './pages/Shared'
import Starred from './pages/Starred'
import SendPage from './pages/Send'
import Requests from './pages/Requests'
import Trash from './pages/Trash'
import Settings from './pages/Settings'
import PublicSend from './pages/PublicSend'
import PublicRequest from './pages/PublicRequest'
import { ErrorBoundary } from './components/ErrorBoundary'
import { UploadTray } from './components/UploadTray'

function Gate({ children }: { children: React.ReactNode }) {
  const { status, user } = useApp()
  const loc = useLocation()
  if (!status) return <div className="boot"><img src="/favicon.svg" alt="" /><Loading /></div>
  if (status.needs_setup) return <Navigate to="/setup" replace />
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />
  return <>{children}</>
}

function App() {
  return (
    <AppProvider>
      <UploadProvider>
        <Routes>
          <Route path="/login" element={<Login mode="login" />} />
          <Route path="/setup" element={<Login mode="setup" />} />
          <Route path="/settings/:tab?" element={<Gate><ErrorBoundary where="settings"><Settings /></ErrorBoundary></Gate>} />
          <Route element={<Gate><Layout /></Gate>}>
            <Route path="/" element={<Navigate to="/f/home" replace />} />
            <Route path="/f/:space/*" element={<Browser />} />
            <Route path="/shared" element={<Shared />} />
            <Route path="/starred" element={<Starred />} />
            <Route path="/send" element={<SendPage />} />
            <Route path="/requests" element={<Requests />} />
            <Route path="/trash" element={<Trash />} />
            <Route path="*" element={<Navigate to="/f/home" replace />} />
          </Route>
        </Routes>
        <UploadTray />
      </UploadProvider>
    </AppProvider>
  )
}

// Share and upload pages are for people without an account: they never
// touch the signed-in parts of the app.
function Root() {
  const loc = useLocation()
  if (loc.pathname.startsWith('/s/') || loc.pathname.startsWith('/r/')) {
    return (
      <Routes>
        <Route path="/s/:id" element={<PublicSend />} />
        <Route path="/r/:id" element={<PublicRequest />} />
      </Routes>
    )
  }
  return <App />
}

// Theme before the first paint (share and upload pages have no app context):
// the stored choice, else the system's, following it when it changes.
{
  const mq = window.matchMedia('(prefers-color-scheme: dark)')
  const boot = () => { const m = storedThemeMode(); applyTheme(m === 'system' ? (mq.matches ? 'dark' : 'light') : m) }
  boot()
  if (location.pathname.startsWith('/s/') || location.pathname.startsWith('/r/')) mq.addEventListener('change', boot)
  localStorage.removeItem('pf-theme')
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary where="app"><ToastProvider><Root /></ToastProvider></ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
)
