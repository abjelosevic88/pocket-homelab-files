// ⌘K / "/" — jump to a place or a starred file, search a folder, or run a command.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { api, filesRoute, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { currentFolder } from '../lib/here'
import type { Star } from '../lib/types'
import { FileGlyph, Icon, type IconName } from './icons'

export interface PaletteCommand { id: string; label: string; icon: IconName; hint?: string; keywords?: string; run: () => void }

export function CommandPalette({ onClose, commands }: { onClose: () => void; commands: PaletteCommand[] }) {
  const { spaces } = useApp()
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [hi, setHi] = useState(0)
  const [stars, setStars] = useState<Star[]>([])
  const list = useRef<HTMLDivElement>(null)
  useEffect(() => { api.get<Star[]>(`${V1}/stars`).then(s => setStars((s || []).filter(x => !x.missing))).catch(() => {}) }, [])

  type Row = { key: string; group: string; icon: React.ReactNode; label: React.ReactNode; sub?: React.ReactNode; hint?: string; run: () => void }
  const rows = useMemo<Row[]>(() => {
    const out: Row[] = []
    const n = q.trim().toLowerCase()
    const words = n.split(/\s+/).filter(Boolean)
    const hit = (text: string) => words.every(w => text.toLowerCase().includes(w))
    // search the open folder (or My files) for the words typed
    if (n) {
      const here = currentFolder()
      const where = here || { space: 'home', path: '/', name: 'My files' }
      out.push({ key: 'search', group: 'Search', icon: <Icon name="search" size={16} />, label: <>Search {where.name} for “{q.trim()}”</>, sub: 'Names in this folder and every folder below it', run: () => navigate(filesRoute(where.space, where.path) + '?q=' + encodeURIComponent(q.trim())) })
      if (here && (here.space !== 'home' || here.path !== '/')) out.push({ key: 'search-home', group: 'Search', icon: <Icon name="home" size={16} />, label: <>Search all of My files for “{q.trim()}”</>, run: () => navigate(filesRoute('home') + '?q=' + encodeURIComponent(q.trim())) })
    }
    for (const s of spaces) {
      if (n && !hit(`${s.name} ${s.owner || ''} ${s.kind === 'mount' ? 'mount server' : s.kind === 'share' ? 'shared' : 'home my files'}`)) continue
      const icon: IconName = s.kind === 'home' ? 'home' : s.kind === 'mount' ? 'disk' : s.is_dir ? 'folderShared' : 'file'
      out.push({ key: 's' + s.id, group: 'Places', icon: <Icon name={icon} size={16} />, label: s.name, sub: s.kind === 'share' ? `from ${s.owner || 'someone'}${s.perm === 'view' ? ' · view only' : ''}` : s.kind === 'mount' ? `server folder${s.perm === 'view' ? ' · view only' : ''}` : undefined, run: () => navigate(filesRoute(s.id)) })
    }
    for (const s of stars) {
      const name = s.entry?.name || s.path.split('/').pop() || s.path
      if (n && !hit(`${name} ${s.space_name} ${s.path}`)) continue
      if (!n && out.filter(r => r.group === 'Starred').length >= 6) continue
      const dir = s.path.split('/').slice(0, -1).join('/') || '/'
      out.push({ key: 'star' + s.space + s.path, group: 'Starred', icon: <FileGlyph name={name} mime={s.entry?.mime || ''} isDir={s.is_dir} size={18} />, label: name, sub: `${s.space_name}${dir === '/' ? '' : dir}`, run: () => navigate(s.is_dir ? filesRoute(s.space, s.path) : filesRoute(s.space, dir) + '?open=' + encodeURIComponent(name)) })
    }
    for (const c of commands) {
      if (n && !hit(`${c.label} ${c.keywords || ''}`)) continue
      out.push({ key: c.id, group: 'Commands', icon: <Icon name={c.icon} size={16} />, label: c.label, hint: c.hint, run: c.run })
    }
    return out
  }, [q, spaces, stars, commands, navigate])
  useEffect(() => setHi(0), [q])
  useEffect(() => { list.current?.querySelector(`[data-i="${hi}"]`)?.scrollIntoView({ block: 'nearest' }) }, [hi])

  const run = (r: Row | undefined) => { if (!r) return; onClose(); r.run() }
  let last = ''
  return createPortal(
    <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="palette-box" role="dialog" aria-label="Search and commands">
        <div className="pal-input">
          <Icon name="search" size={18} />
          <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Search files, jump to a place, or run a command…" aria-label="Search"
            onKeyDown={e => {
              if (e.key === 'ArrowDown') { e.preventDefault(); setHi(h => Math.min(rows.length - 1, h + 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setHi(h => Math.max(0, h - 1)) }
              else if (e.key === 'Enter') { e.preventDefault(); run(rows[hi]) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
            }} />
          <kbd>esc</kbd>
        </div>
        <div className="pal-list" ref={list} role="listbox">
          {rows.length === 0 && <div className="pal-empty">Nothing matches.</div>}
          {rows.map((r, i) => {
            const head = r.group !== last ? <div className="pal-group" key={'g' + r.group}>{r.group}</div> : null
            last = r.group
            return (
              <React.Fragment key={r.key}>
                {head}
                <div data-i={i} role="option" aria-selected={i === hi} className={`pal-row ${i === hi ? 'hi' : ''}`} onMouseEnter={() => setHi(i)} onClick={() => run(r)}>
                  <span className="pal-ico">{r.icon}</span>
                  <span className="grow"><span className="pal-label">{r.label}</span>{r.sub && <span className="pal-sub">{r.sub}</span>}</span>
                  {r.hint && <kbd>{r.hint}</kbd>}
                  {i === hi && <Icon name="chevronRight" size={14} className="muted" />}
                </div>
              </React.Fragment>
            )
          })}
        </div>
      </div>
    </div>, document.body)
}
