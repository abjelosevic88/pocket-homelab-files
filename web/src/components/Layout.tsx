import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api, filesRoute, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { bytes } from '../lib/format'
import type { Space, SystemInfo } from '../lib/types'
import { Avatar } from './people'
import { ErrorBoundary } from './ErrorBoundary'
import { Icon, type IconName } from './icons'
import { UploadTray } from './UploadTray'

const section = (path: string) => path.split('/')[1] || 'f'

export default function Layout() {
  const { user, settings, status, theme, toggleTheme, setUser, spaces, version } = useApp()
  const [open, setOpen] = useState(false)
  const [info, setInfo] = useState<SystemInfo | null>(null)
  const navigate = useNavigate()
  const loc = useLocation()
  const title = settings?.title || 'Pocket Files'

  useEffect(() => { setOpen(false) }, [loc.pathname])
  useEffect(() => { api.get<SystemInfo>(`${V1}/settings/system`).then(setInfo).catch(() => {}) }, [version])

  const logout = async () => { await api.post(`${V1}/auth/logout`); setUser(null); navigate('/login') }
  const mounts = spaces.filter(s => s.kind === 'mount')
  const shared = spaces.filter(s => s.kind === 'share')
  const homeActive = loc.pathname === '/' || loc.pathname.startsWith('/f/home')

  return (
    <div className="app">
      <aside className={`sidebar ${open ? 'open' : ''}`}>
        <div className="brand">
          <span className="logo"><img src="/favicon.svg" alt="" /></span><span className="brand-name">{title}</span>
        </div>
        <div className="side-actions">
          <button className="new-task" onClick={() => navigate('/send')} title="Send files with a link"><Icon name="send" size={15} stroke={2.2} /><span>Send files</span></button>
        </div>
        <nav className="side-nav">
          <NavLink to={filesRoute('home')} className={() => `nav-item ${homeActive ? 'active' : ''}`}><span className="nav-ico"><Icon name="home" size={17} style={{ color: '#0891b2' }} /></span><span className="grow">My files</span></NavLink>
          <Nav to="/starred" icon="star" color="#f5b400" fill label="Starred" />
          <Nav to="/shared" icon="users" color="#8b5cf6" label="Shared" />
          <Nav to="/send" icon="link" color="#0ea5e9" label="Links" />
          <Nav to="/requests" icon="inbox" color="#16a34a" label="Upload links" />
        </nav>
        <div className="side-scroll">
          {mounts.length > 0 && <>
            <div className="side-head"><span>Mounts</span></div>
            <ul className="tree">{mounts.map(m => <SpaceLeaf key={m.id} space={m} icon="disk" />)}</ul>
          </>}
          {shared.length > 0 && <>
            <div className="side-head"><span>Shared with me</span></div>
            <ul className="tree">{shared.slice(0, 12).map(s => <SpaceLeaf key={s.id} space={s} icon={s.is_dir ? 'folderShared' : 'file'} />)}</ul>
            {shared.length > 12 && <NavLink to="/shared" className="side-item-add">All {shared.length} shared…</NavLink>}
          </>}
        </div>
        <div className="side-bottom">
          {info && <div className="usage" title={`${bytes(info.home.size)} in ${info.home.files} files · ${bytes(info.disk.free)} free on the server`}>
            <div className="row small"><span className="grow muted">{bytes(info.home.size)} used</span><span className="faint">{bytes(info.disk.free)} free</span></div>
            <div className="bar"><span className="fill" style={{ width: info.disk.total ? Math.max(2, Math.round(((info.disk.total - info.disk.free) / info.disk.total) * 100)) + '%' : '0' }} /></div>
          </div>}
          <nav className="side-nav">
            <Nav to="/trash" icon="trash" label="Trash" />
            <Nav to="/settings" icon="settings" label="Settings" />
          </nav>
          <div className="foot">
            <span className="row foot-who" title={user?.email}>{user && <Avatar user={user} />}<span className="foot-id"><span>{user?.name || user?.email}</span>{status?.version && <a className="ver" href="https://github.com/abjelosevic88/pocket-homelab-files/releases" target="_blank" rel="noreferrer">v{status.version.replace(/^v/, '')}</a>}</span></span>
            <span className="row" style={{ gap: 2 }}>
              <button className="icon-btn" onClick={toggleTheme} title={theme === 'dark' ? 'Light theme' : 'Dark theme'}><Icon name={theme === 'dark' ? 'sun' : 'moon'} size={16} /></button>
              {status?.auth_mode === 'local' && <button className="icon-btn" onClick={logout} title="Sign out"><Icon name="logout" size={16} /></button>}
            </span>
          </div>
        </div>
      </aside>
      {open && <div className="scrim" onClick={() => setOpen(false)} />}
      <div className="main">
        <header className="topbar">
          <button className="icon-btn" onClick={() => setOpen(o => !o)} aria-label="Menu"><Icon name="menu" size={20} /></button>
          <span className="topbar-title">{title}</span>
          <span className="spacer" />
          <button className="icon-btn accent" onClick={() => navigate('/send')} aria-label="Send files"><Icon name="send" size={19} /></button>
        </header>
        <div className="content-row">
          <main className="content"><ErrorBoundary key={section(loc.pathname)} where={loc.pathname}><Outlet /></ErrorBoundary></main>
        </div>
        <nav className="tabbar">
          <NavLink to={filesRoute('home')} className={() => homeActive ? 'active' : ''}><Icon name="home" size={21} />Files</NavLink>
          <NavLink to="/shared"><Icon name="users" size={21} />Shared</NavLink>
          <NavLink to="/send"><Icon name="link" size={21} />Links</NavLink>
          <NavLink to="/starred"><Icon name="star" size={21} />Starred</NavLink>
          <button onClick={() => setOpen(true)}><Icon name="menu" size={21} />More</button>
        </nav>
      </div>
      <UploadTray />
    </div>
  )
}

function SpaceLeaf({ space, icon }: { space: Space; icon: IconName }) {
  const loc = useLocation()
  const to = filesRoute(space.id)
  const active = decodeURIComponent(loc.pathname).startsWith(decodeURIComponent(to))
  return (
    <li>
      <div className="tree-row">
        <NavLink to={to} className={() => `tree-link ${active ? 'active' : ''}`} title={space.owner ? `${space.name} — from ${space.owner}` : space.name}>
          <span className="tree-ico" style={{ color: space.color || (space.kind === 'mount' ? '#64748b' : '#8b5cf6') }}><Icon name={icon} size={15} /></span>
          <span className="grow">{space.name}</span>
          {space.perm === 'view' && <Icon name="lock" size={12} className="faint" />}
        </NavLink>
      </div>
    </li>
  )
}

function Nav({ to, icon, label, color, fill }: { to: string; icon: IconName; label: string; color?: string; fill?: boolean }) {
  return (
    <NavLink to={to} className="nav-item">
      <span className="nav-ico"><Icon name={icon} size={17} fill={fill} stroke={fill ? 1.4 : 1.9} style={{ color: color || 'var(--muted)' }} /></span>
      <span className="grow">{label}</span>
    </NavLink>
  )
}
