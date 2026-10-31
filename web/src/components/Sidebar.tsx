// Left column: New button, sections, mounts and what is shared with me — the Pocket Calendar sidebar.
import { useEffect, useState } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import { api, filesRoute, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { bytes } from '../lib/format'
import type { Space, SystemInfo } from '../lib/types'
import { CloudLogo } from './CloudLogo'
import { Icon, type IconName } from './icons'
import { newItems, type NewKind } from './Layout'
import { type Anchor, Menu, rectOf } from './ui'

export function Sidebar({ onNew, onNavigate }: { onNew: (k: NewKind) => void; onNavigate: () => void }) {
  const { status, settings, spaces, version, user } = useApp()
  const loc = useLocation()
  const [menu, setMenu] = useState<Anchor | null>(null)
  // The last usage seen is shown at once; the server answers from its own count and,
  // when that count is being refreshed, the sidebar asks again a moment later.
  const usageKey = `pf-usage-${user?.id ?? ''}`
  const [info, setInfo] = useState<SystemInfo | null>(() => { try { return JSON.parse(localStorage.getItem(usageKey) || 'null') } catch { return null } })
  useEffect(() => {
    let timer = 0, gone = false
    const load = (again: boolean) => api.get<SystemInfo>(`${V1}/settings/system`).then(i => {
      if (gone) return
      setInfo(i); localStorage.setItem(usageKey, JSON.stringify({ home: i.home, disk: i.disk }))
      if (i.home.stale && again) timer = window.setTimeout(() => load(false), 3000)
    }).catch(() => {})
    load(true)
    return () => { gone = true; clearTimeout(timer) }
  }, [version, usageKey])
  const sizes = useSpaceSizes(user?.id, version, spaces.length)
  const title = settings?.title || status?.title || 'Pocket Files'
  const mounts = spaces.filter(s => s.kind === 'mount')
  const shared = spaces.filter(s => s.kind === 'share')
  const drives = spaces.filter(s => s.kind === 'cloud')
  const homeActive = loc.pathname === '/' || loc.pathname.startsWith('/f/home')
  const nav: { to: string; label: string; icon: IconName; active?: boolean; count?: number }[] = [
    { to: filesRoute('home'), label: 'My files', icon: 'home', active: homeActive },
    { to: '/starred', label: 'Starred', icon: 'star' },
    { to: '/shared', label: 'Shared', icon: 'users', count: shared.length },
    { to: '/send', label: 'Links', icon: 'link' },
    { to: '/requests', label: 'Upload links', icon: 'inbox' },
    { to: '/trash', label: 'Trash', icon: 'trash' },
  ]
  const used = info?.disk.total ? Math.max(2, Math.round(((info.disk.total - info.disk.free) / info.disk.total) * 100)) : 0
  return (
    <aside className="sidebar" aria-label="Files">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Files' ? title : <>Pocket <b>Files</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
        <Icon name="plus" size={17} stroke={2.2} />New<Icon name="chevronDown" size={15} className="create-caret" />
      </button>
      {menu && <Menu anchor={menu} items={newItems(onNew)} onClose={() => setMenu(null)} width={252} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${(n.active ?? isActive) ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}{!!n.count && <span className="sb-count">{n.count}</span>}
          </NavLink>
        ))}
      </nav>

      {mounts.length > 0 && <SpaceGroup title="Mounts" list={mounts} sizes={sizes ?? undefined} onNavigate={onNavigate} />}
      {drives.length > 0 && <SpaceGroup title="Drives" list={drives} sizes={sizes ?? undefined} onNavigate={onNavigate} />}
      {shared.length > 0 && <SpaceGroup title="Shared with me" list={shared} onNavigate={onNavigate} limit={12} />}

      {info && <div className="sb-usage" title={`${bytes(info.home.size)} in ${info.home.files} files · ${bytes(info.disk.free)} free on the server`}>
        <div className="sb-usage-row"><span className="grow">{bytes(info.home.size)} used</span><span className="faint">{bytes(info.disk.free)} free</span></div>
        <div className="bar"><span className="fill" style={{ width: used + '%' }} /></div>
      </div>}
      <div className={`sb-foot ${info ? 'with-usage' : ''}`}>
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pf:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}

function SpaceGroup({ title, list, sizes, onNavigate, limit = 0 }: { title: string; list: Space[]; sizes?: SpaceSizes; onNavigate: () => void; limit?: number }) {
  const [open, setOpen] = useState(true)
  const shown = limit ? list.slice(0, limit) : list
  return (
    <section className="sb-sec">
      <h4 className="sb-h">
        <button type="button" className="sb-h-btn" aria-expanded={open} onClick={() => setOpen(o => !o)}><Icon name={open ? 'chevronDown' : 'chevronRight'} size={13} />{title}</button>
      </h4>
      {open && <ul className="cal-list">
        {shown.map(s => <SpaceRow key={s.id} space={s} size={sizes?.[s.id]?.size} onNavigate={onNavigate} />)}
        {limit > 0 && list.length > limit && <li><Link to="/shared" className="sb-more" onClick={onNavigate}>All {list.length} shared…</Link></li>}
      </ul>}
    </section>
  )
}

function SpaceRow({ space, size, onNavigate }: { space: Space; size?: number; onNavigate: () => void }) {
  const loc = useLocation()
  const to = filesRoute(space.id)
  let active = false
  try { active = decodeURIComponent(loc.pathname) === decodeURIComponent(to) || decodeURIComponent(loc.pathname).startsWith(decodeURIComponent(to) + '/') } catch { active = false }
  const icon: IconName = space.kind === 'mount' ? 'disk' : space.kind === 'cloud' ? 'cloud' : space.is_dir ? 'folderShared' : 'file'
  return (
    <li className="cal-row space-row">
      <NavLink to={to} onClick={onNavigate} className={`cal-check ${active ? 'is-active' : ''}`} title={space.owner ? `${space.name} — from ${space.owner}` : space.name}>
        <span className="space-ico" style={{ color: space.color || (space.kind === 'mount' ? 'var(--muted)' : space.kind === 'cloud' ? '#0891b2' : '#8b5cf6') }}>{space.kind === 'cloud' ? <CloudLogo kind={space.cloud} size={16} /> : <Icon name={icon} size={16} />}</span>
        <span className="cal-name ellipsis">{space.name}</span>
        {space.perm === 'view' && space.kind !== 'cloud' && <span className="space-flag" title="View only"><Icon name="lock" size={12} /></span>}
        {space.owner && <span className="space-owner ellipsis">{space.owner}</span>}
        {size !== undefined && <span className="sb-count space-size" title={`${bytes(size)} in ${space.name}`}>{bytes(size)}</span>}
      </NavLink>
    </li>
  )
}

type SpaceSizes = Record<string, { files: number; size: number }>

/** What each mount and drive holds: the last totals at once, then the server's, asking again while it counts. */
function useSpaceSizes(uid: number | undefined, version: unknown, count: number) {
  const key = `pf-space-sizes-${uid ?? ''}`
  const [sizes, setSizes] = useState<SpaceSizes | null>(() => { try { return JSON.parse(localStorage.getItem(key) || 'null') } catch { return null } })
  useEffect(() => {
    let timer = 0, gone = false, tries = 0
    const load = () => api.get<{ sizes: SpaceSizes; pending: boolean }>(`${V1}/spaces/sizes`).then(r => {
      if (gone) return
      setSizes(r.sizes); localStorage.setItem(key, JSON.stringify(r.sizes))
      if (r.pending && ++tries < 60) timer = window.setTimeout(load, 5000)
    }).catch(() => {})
    load()
    return () => { gone = true; clearTimeout(timer) }
  }, [key, version, count])
  return sizes
}
