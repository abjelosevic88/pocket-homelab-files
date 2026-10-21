import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { api, V1 } from './api'
import type { AuthStatus, Settings, Space, User } from './types'

interface AppState {
  status: AuthStatus | null
  user: User | null
  settings: Settings | null
  spaces: Space[]
  /** Changes whenever files changed anywhere: pages reload on it. */
  version: number
  bump: () => void
  theme: 'light' | 'dark'
  toggleTheme: () => void
  refresh: () => Promise<void>
  reloadSpaces: () => Promise<void>
  setUser: (u: User | null) => void
  setSettings: (s: Settings) => void
  saveSettings: (patch: Partial<Settings>) => Promise<void>
}

const Ctx = createContext<AppState>(null!)
export function useApp() { return useContext(Ctx) }

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const [user, setUser] = useState<User | null>(null)
  const [settings, setSettings] = useState<Settings | null>(null)
  const [spaces, setSpaces] = useState<Space[]>([])
  const [version, setVersion] = useState(0)
  const [theme, setTheme] = useState<'light' | 'dark'>(() => (localStorage.getItem('pf-theme') as 'light' | 'dark') || (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'))

  useEffect(() => { document.documentElement.dataset.theme = theme; localStorage.setItem('pf-theme', theme) }, [theme])
  useEffect(() => { if (settings?.title) document.title = settings.title }, [settings?.title])

  const reloadSpaces = useCallback(async () => {
    setSpaces(await api.get<Space[]>(`${V1}/spaces`).catch(() => [] as Space[]))
  }, [])

  const refresh = useCallback(async () => {
    const st = await api.get<AuthStatus>(`${V1}/auth/status`)
    setStatus(st)
    setUser(st.user)
    if (st.authenticated) {
      setSettings(await api.get<Settings>(`${V1}/settings`))
      await reloadSpaces()
    }
  }, [reloadSpaces])

  useEffect(() => { refresh().catch(() => setStatus({ needs_setup: false, auth_mode: 'local', authenticated: false, user: null, version: '', title: 'Pocket Files' })) }, [refresh])
  useEffect(() => {
    const h = () => { setUser(null); setStatus(s => s ? { ...s, authenticated: false, user: null } : s) }
    window.addEventListener('pf:unauthorized', h)
    return () => window.removeEventListener('pf:unauthorized', h)
  }, [])
  useEffect(() => { if (user && version > 0) reloadSpaces().catch(() => {}) }, [version, user, reloadSpaces])

  const bump = useCallback(() => setVersion(v => v + 1), [])
  const saveSettings = useCallback(async (patch: Partial<Settings>) => {
    setSettings(s => s ? { ...s, ...patch } : s)
    const next = await api.put<Settings>(`${V1}/settings`, patch)
    setSettings(next)
  }, [])

  // Changes made elsewhere (WebDAV, another person, an upload link) reach the
  // open page within seconds: a stamp is polled while the tab is visible.
  const stamp = useRef<number | null>(null)
  useEffect(() => {
    if (!user) return
    const check = async () => {
      if (document.hidden) return
      try {
        const r = await api.get<{ stamp: number }>(`${V1}/changes`)
        if (stamp.current !== null && r.stamp !== stamp.current) bump()
        stamp.current = r.stamp
      } catch {}
    }
    check()
    const t = setInterval(check, 8000)
    const onShow = () => { if (!document.hidden) check() }
    document.addEventListener('visibilitychange', onShow)
    window.addEventListener('focus', onShow)
    return () => { clearInterval(t); document.removeEventListener('visibilitychange', onShow); window.removeEventListener('focus', onShow) }
  }, [user, bump])

  return <Ctx.Provider value={{ status, user, settings, spaces, version, bump, theme, toggleTheme: () => setTheme(t => t === 'dark' ? 'light' : 'dark'), refresh, reloadSpaces, setUser, setSettings, saveSettings }}>{children}</Ctx.Provider>
}
