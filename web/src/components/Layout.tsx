// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import React, { useEffect, useRef, useState } from 'react'
import { Link, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api, filesRoute, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { uploadTarget, type Here } from '../lib/here'
import { plural } from '../lib/format'
import { filesFromInput, useUploads } from '../lib/uploads'
import { RequestDialog } from '../pages/Requests'
import { Avatar } from './people'
import { ErrorBoundary } from './ErrorBoundary'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { type Anchor, Menu, type MenuItem, MenuButton, modalOpen, Prompt, rectOf, useIsMobile, useMediaQuery, useToast } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'pf-sidebar'
/** Where Settings' back button returns to. */
export const LAST_KEY = 'pf-last'

const section = (path: string) => path.split('/')[1] || 'f'

export type NewKind = 'files' | 'folder' | 'mkdir' | 'send' | 'request'

/** The New menu: the same everywhere (sidebar button, phone button). */
export function newItems(run: (k: NewKind) => void): MenuItem[] {
  const t = uploadTarget()
  return [
    { heading: `Into ${t.name}` },
    { label: 'Upload files…', icon: 'upload', hint: 'U', onSelect: () => run('files') },
    { label: 'Upload a folder…', icon: 'folderUp', onSelect: () => run('folder') },
    { label: 'New folder', icon: 'folderPlus', hint: 'N', onSelect: () => run('mkdir') },
    'sep',
    { label: 'Send files with a link', icon: 'send', hint: 'L', onSelect: () => run('send') },
    { label: 'Ask someone for files…', icon: 'inbox', onSelect: () => run('request') },
  ]
}

export default function Layout() {
  const { bump } = useApp()
  const { add } = useUploads()
  const { toast } = useToast()
  const navigate = useNavigate()
  const loc = useLocation()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { closeDrawer(); sessionStorage.setItem(LAST_KEY, loc.pathname + loc.search) }, [loc.pathname, loc.search]) // eslint-disable-line react-hooks/exhaustive-deps

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [dialog, setDialog] = useState<{ kind: 'mkdir' | 'request'; at: Here } | null>(null)
  const [fabMenu, setFabMenu] = useState<Anchor | null>(null)

  // uploads from the New menu go to the open folder (or My files)
  const fileInput = useRef<HTMLInputElement>(null)
  const dirInput = useRef<HTMLInputElement>(null)
  const target = useRef<Here>(uploadTarget())
  const picked = (files: FileList | null) => {
    const list = filesFromInput(files)
    if (!list.length) return
    const t = target.current
    add(t.space, t.path, list)
    toast(`Uploading ${plural(list.length, 'file')} to ${t.name}`)
  }

  const run = (k: NewKind) => {
    closeDrawer()
    target.current = uploadTarget()
    if (k === 'files') fileInput.current?.click()
    else if (k === 'folder') dirInput.current?.click()
    else if (k === 'send') navigate('/send')
    else setDialog({ kind: k, at: target.current })
  }
  const runRef = useRef(run)
  runRef.current = run

  useEffect(() => {
    const open = () => setShortcuts(true)
    window.addEventListener('pf:shortcuts', open)
    return () => window.removeEventListener('pf:shortcuts', open)
  }, [])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t?.closest?.('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .select-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'u': e.preventDefault(); runRef.current('files'); break
        case 'n': e.preventDefault(); runRef.current('mkdir'); break
        case 'l': go('/send'); break
        case 'h': go(filesRoute('home')); break
        case 's': go('/starred'); break
        case 'p': go('/shared'); break
        case 'i': go('/requests'); break
        case 't': go('/trash'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'upload', label: `Upload files to ${uploadTarget().name}`, icon: 'upload', hint: 'U', keywords: 'add put', run: () => run('files') },
    { id: 'upload-dir', label: `Upload a folder to ${uploadTarget().name}`, icon: 'folderUp', keywords: 'add put directory', run: () => run('folder') },
    { id: 'mkdir', label: 'New folder', icon: 'folderPlus', hint: 'N', keywords: 'create directory mkdir', run: () => run('mkdir') },
    { id: 'send', label: 'Send files with a link', icon: 'send', hint: 'L', keywords: 'share link skysend encrypted note', run: () => navigate('/send') },
    { id: 'request', label: 'Ask someone for files', icon: 'inbox', keywords: 'upload link request drop', run: () => run('request') },
    { id: 'home', label: 'My files', icon: 'home', hint: 'H', run: () => navigate(filesRoute('home')) },
    { id: 'starred', label: 'Starred', icon: 'star', hint: 'S', run: () => navigate('/starred') },
    { id: 'shared', label: 'Shared', icon: 'users', hint: 'P', keywords: 'people', run: () => navigate('/shared') },
    { id: 'links', label: 'Links', icon: 'link', hint: 'L', keywords: 'sends', run: () => navigate('/send') },
    { id: 'requests', label: 'Upload links', icon: 'inbox', hint: 'I', keywords: 'requests', run: () => navigate('/requests') },
    { id: 'trash', label: 'Trash', icon: 'trash', hint: 'T', keywords: 'deleted restore', run: () => navigate('/trash') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'webdav', label: 'WebDAV & app passwords', icon: 'key', keywords: 'finder mount dav token', run: () => navigate('/settings/webdav') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  const at = dialog?.at
  return (
    <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
      {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
      <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
        <Sidebar onNew={run} onNavigate={closeDrawer} />
      </div>
      <div className="main">
        <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} />
        <main className="content">
          <div className={`content-inner ${loc.pathname.startsWith('/f/') ? 'wide' : ''}`}>
            <ErrorBoundary key={section(loc.pathname)} where={loc.pathname}><Outlet /></ErrorBoundary>
          </div>
        </main>
      </div>
      {mobile && !loc.pathname.startsWith('/send') && <button type="button" className="fab" aria-label="New" title="New" aria-haspopup="menu" aria-expanded={!!fabMenu} onClick={e => setFabMenu(fabMenu ? null : rectOf(e.currentTarget))}><Icon name="plus" size={26} stroke={2.2} /></button>}
      {fabMenu && <Menu anchor={fabMenu} align="right" width={250} items={newItems(run)} onClose={() => setFabMenu(null)} />}
      <input ref={fileInput} type="file" multiple hidden onChange={e => { picked(e.target.files); e.target.value = '' }} />
      <input ref={dirInput} type="file" hidden {...{ webkitdirectory: '', directory: '' } as Record<string, string>} onChange={e => { picked(e.target.files); e.target.value = '' }} />
      {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} />}
      {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
      {dialog?.kind === 'mkdir' && at && <Prompt title={`New folder in ${at.name}`} placeholder="Folder name" button="Create" onClose={() => setDialog(null)} onDone={async name => {
        const p = (at.path === '/' ? '' : at.path) + '/' + name
        try { await api.post(`${V1}/fs/mkdir`, { space: at.space, path: p }); setDialog(null); bump(); navigate(filesRoute(at.space, p)) } catch (e) { toast((e as Error).message, 'err') }
      }} />}
      {dialog?.kind === 'request' && at && <RequestDialog space={at.space} path={at.path} folderName={at.path === '/' ? undefined : at.name} onClose={() => setDialog(null)} />}
    </div>
  )
}

const SECTION: Record<string, string> = { starred: 'Starred', shared: 'Shared', send: 'Links', requests: 'Upload links', trash: 'Trash' }

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pf:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'logout' as const, onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title"><Title mobile={mobile} /></h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
      {mobile && <AskButton />}
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        {user ? <Avatar user={user} size={30} /> : <Icon name="user" size={18} />}
      </MenuButton>
    </header>
  )
}

/** The page title; in a folder, its breadcrumb (on phones: back + the folder's name). */
function Title({ mobile }: { mobile: boolean }) {
  const { spaces } = useApp()
  const loc = useLocation()
  const navigate = useNavigate()
  const parts = loc.pathname.split('/').filter(Boolean)
  if (parts[0] !== 'f') return <>{SECTION[parts[0]] || ''}</>
  const space = decodeURIComponent(parts[1] || 'home')
  const crumbs = parts.slice(2).map(p => { try { return decodeURIComponent(p) } catch { return p } })
  const root = spaces.find(s => s.id === space)?.name || (space === 'home' ? 'My files' : space.startsWith('mount:') ? 'Mount' : 'Shared')
  const to = (i: number) => filesRoute(space, '/' + crumbs.slice(0, i).join('/'))
  if (mobile && crumbs.length) {
    return <>
      <button type="button" className="btn ghost icon-only sm crumb-back" aria-label="Up one folder" title="Up one folder" onClick={() => navigate(to(crumbs.length - 1))}><Icon name="chevronLeft" size={18} /></button>
      <span className="ellipsis">{crumbs[crumbs.length - 1]}</span>
    </>
  }
  return <>
    {crumbs.length ? <Link to={to(0)} className="crumb">{root}</Link> : <span className="ellipsis">{root}</span>}
    {crumbs.map((c, i) => (
      <React.Fragment key={i}>
        <Icon name="chevronRight" size={16} className="crumb-sep" />
        {i === crumbs.length - 1 ? <span className="ellipsis crumb-cur">{c}</span> : <Link to={to(i + 1)} className="crumb ellipsis">{c}</Link>}
      </React.Fragment>
    ))}
  </>
}
