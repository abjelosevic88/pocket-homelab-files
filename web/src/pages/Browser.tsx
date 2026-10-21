import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, filesRoute, qs, rawURL, thumbURL, V1, zipURL } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, bytes, plural, stamp } from '../lib/format'
import { filesFromDrop, filesFromInput, useUploads } from '../lib/uploads'
import type { Entry, Listing } from '../lib/types'
import { FileGlyph, Icon, fileKind } from '../components/icons'
import { Confirm, ContextMenu, Dropdown, Empty, Loading, Prompt, useToast } from '../components/ui'
import { Preview, previewKind } from '../components/Preview'
import { ShareDialog } from '../components/ShareDialog'
import { FolderPicker } from '../components/FolderPicker'
import { RequestDialog } from './Requests'

type Dialog =
  | { kind: 'mkdir' } | { kind: 'rename'; entry: Entry } | { kind: 'delete'; entries: Entry[] }
  | { kind: 'move' | 'copy'; entries: Entry[] } | { kind: 'share'; entries: Entry[]; tab?: 'people' | 'link' } | { kind: 'request' }

const THUMB_TYPES = /^image\/(jpeg|png|gif|webp)$/

/** Splits /f/<space>/<path…> into a space id and a path. */
function useLocationSpace() {
  const params = useParams()
  const space = decodeURIComponent(params.space || 'home')
  const rest = (params['*'] || '').split('/').filter(Boolean).map(decodeURIComponent)
  return { space, path: '/' + rest.join('/') }
}

export default function Browser() {
  const { space, path } = useLocationSpace()
  const { settings, saveSettings, version, bump, spaces } = useApp()
  const { add, onDone } = useUploads()
  const { toast } = useToast()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const [data, setData] = useState<Listing | null>(null)
  const [err, setErr] = useState('')
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [anchor, setAnchor] = useState<string | null>(null)
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const [menu, setMenu] = useState<{ x: number; y: number; entry: Entry } | null>(null)
  const [preview, setPreview] = useState<number | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [dropFolder, setDropFolder] = useState<string | null>(null)
  const [filter, setFilter] = useState('')
  const [results, setResults] = useState<Entry[] | null>(null)
  const [searching, setSearching] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const dirInput = useRef<HTMLInputElement>(null)
  const dragDepth = useRef(0)
  const view = settings?.view || 'list'
  const sort = settings?.sort || 'name'
  const desc = settings?.sort_desc || false
  const q = params.get('q') || ''

  const load = useCallback(async () => {
    try {
      const d = await api.get<Listing>(`${V1}/fs/list${qs({ space, path })}`)
      if (!d.entry.is_dir) { navigate(filesRoute(space, d.path.split('/').slice(0, -1).join('/') || '/') + `?open=${encodeURIComponent(d.entry.name)}`, { replace: true }); return }
      setData(d); setErr('')
    } catch (e) { setErr((e as Error).message); setData(null) }
  }, [space, path, navigate])

  useEffect(() => { setData(null); setSel(new Set()); setFilter(''); setPreview(null) }, [space, path])
  useEffect(() => { load() }, [load, version])
  useEffect(() => onDone(() => load()), [onDone, load])

  // deep search (?q=)
  useEffect(() => {
    if (!q) { setResults(null); return }
    setSearching(true)
    api.get<{ entries: Entry[]; partial: boolean }>(`${V1}/fs/search${qs({ space, path, q })}`).then(r => setResults(r.entries)).catch(e => toast((e as Error).message, 'err')).finally(() => setSearching(false))
  }, [q, space, path, version, toast])

  const entries = useMemo(() => {
    const list = (results ?? data?.entries ?? []).filter(e => !filter || e.name.toLowerCase().includes(filter.toLowerCase()))
    const dir = desc ? -1 : 1
    const cmp = (a: Entry, b: Entry) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
      switch (sort) {
        case 'size': return (a.size - b.size) * dir || a.name.localeCompare(b.name)
        case 'modified': return (new Date(a.modified).getTime() - new Date(b.modified).getTime()) * dir
        case 'type': return fileKind(a.name, a.mime, a.is_dir).label.localeCompare(fileKind(b.name, b.mime, b.is_dir).label) * dir || a.name.localeCompare(b.name)
        default: return a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' }) * dir
      }
    }
    return [...list].sort(cmp)
  }, [data, results, filter, sort, desc])

  const files = entries.filter(e => !e.is_dir)
  const selected = entries.filter(e => sel.has(e.path))
  const writable = !!data?.writable
  const spaceInfo = data?.space

  // ?open=name opens a file's preview (links from Starred / search)
  useEffect(() => {
    const open = params.get('open')
    if (!open || !data) return
    const i = files.findIndex(f => f.name === open)
    if (i >= 0) setPreview(i)
    setParams(p => { const n = new URLSearchParams(p); n.delete('open'); return n }, { replace: true })
  }, [data, params]) // eslint-disable-line react-hooks/exhaustive-deps

  const open = (e: Entry) => {
    if (e.is_dir) { navigate(filesRoute(space, e.path)); return }
    if (results) { navigate(filesRoute(space, e.path.split('/').slice(0, -1).join('/') || '/') + `?open=${encodeURIComponent(e.name)}`); return }
    setPreview(files.indexOf(e))
  }

  const click = (e: Entry, ev: React.MouseEvent) => {
    if (ev.metaKey || ev.ctrlKey) { toggle(e.path); setAnchor(e.path); return }
    if (ev.shiftKey && anchor) {
      const a = entries.findIndex(x => x.path === anchor), b = entries.findIndex(x => x.path === e.path)
      const [lo, hi] = a < b ? [a, b] : [b, a]
      setSel(new Set(entries.slice(lo, hi + 1).map(x => x.path)))
      return
    }
    if (sel.size) { toggle(e.path); setAnchor(e.path); return }
    open(e)
  }
  const toggle = (p: string) => setSel(s => { const n = new Set(s); if (n.has(p)) n.delete(p); else n.add(p); return n })

  // keyboard
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (dialog || preview !== null || (e.target as HTMLElement)?.closest('input, textarea, .modal')) return
      if ((e.metaKey || e.ctrlKey) && e.key === 'a') { e.preventDefault(); setSel(new Set(entries.map(x => x.path))) }
      else if (e.key === 'Escape') setSel(new Set())
      else if ((e.key === 'Delete' || (e.key === 'Backspace' && e.metaKey)) && selected.length && writable) setDialog({ kind: 'delete', entries: selected })
      else if (e.key === 'F2' && selected.length === 1 && writable) setDialog({ kind: 'rename', entry: selected[0] })
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [entries, selected, dialog, preview, writable])

  // ---- uploads: drop anywhere on the page ----
  const isFileDrag = (e: React.DragEvent) => Array.from(e.dataTransfer.types).includes('Files')
  const onDragEnter = (e: React.DragEvent) => { if (!isFileDrag(e) || !writable) return; e.preventDefault(); dragDepth.current++; setDragOver(true) }
  const onDragLeave = (e: React.DragEvent) => { if (!isFileDrag(e)) return; dragDepth.current--; if (dragDepth.current <= 0) { dragDepth.current = 0; setDragOver(false) } }
  const onDragOver = (e: React.DragEvent) => { if (isFileDrag(e) && writable) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy' } }
  const onDrop = async (e: React.DragEvent, intoFolder?: string) => {
    if (!isFileDrag(e)) return
    e.preventDefault(); e.stopPropagation()
    dragDepth.current = 0; setDragOver(false); setDropFolder(null)
    if (!writable) return
    const list = await filesFromDrop(e.dataTransfer)
    if (list.length) { add(space, intoFolder || path, list); toast(`Uploading ${plural(list.length, 'file')}`) }
  }

  // ---- moving rows onto folders ----
  const onRowDragStart = (e: React.DragEvent, en: Entry) => {
    const paths = sel.has(en.path) ? [...sel] : [en.path]
    e.dataTransfer.setData('application/x-pocket-files', JSON.stringify({ space, paths }))
    e.dataTransfer.effectAllowed = 'move'
  }
  const onFolderDrop = async (e: React.DragEvent, folder: Entry) => {
    const raw = e.dataTransfer.getData('application/x-pocket-files')
    if (!raw) return onDrop(e, folder.path)
    e.preventDefault(); setDropFolder(null)
    const m = JSON.parse(raw) as { space: string; paths: string[] }
    if (m.paths.includes(folder.path)) return
    await doMove('move', m.paths, space, folder.path, m.space)
  }

  const doMove = async (kind: 'move' | 'copy', paths: string[], toSpace: string, toPath: string, fromSpace = space) => {
    try {
      const res = await api.post<{ from: string; to?: string; error?: string }[]>(`${V1}/fs/${kind}`, { space: fromSpace, paths, to_space: toSpace, to_path: toPath, conflict: 'rename' })
      const bad = res.filter(r => r.error)
      if (bad.length) toast(`${bad.length} could not be ${kind === 'move' ? 'moved' : 'copied'}: ${bad[0].error}`, 'err')
      else toast(`${kind === 'move' ? 'Moved' : 'Copied'} ${plural(res.length, 'item')}`)
      setSel(new Set()); bump()
    } catch (e) { toast((e as Error).message, 'err') }
  }

  const doDelete = async (list: Entry[]) => {
    setDialog(null)
    try {
      const r = await api.post<{ trashed: number; deleted: number; errors?: string[] }>(`${V1}/fs/delete`, { space, paths: list.map(e => e.path) })
      if (r.errors?.length) toast(r.errors[0], 'err')
      else toast(r.trashed ? `Moved ${plural(r.trashed, 'item')} to the trash` : `Deleted ${plural(r.deleted, 'item')}`)
      setSel(new Set()); bump()
    } catch (e) { toast((e as Error).message, 'err') }
  }

  const star = async (e: Entry) => {
    try { await api.put(`${V1}/stars`, { space, path: e.path, on: !e.starred }); load() } catch (er) { toast((er as Error).message, 'err') }
  }

  const download = (list: Entry[]) => {
    if (list.length === 1 && !list[0].is_dir) { location.href = rawURL(space, list[0].path, true); return }
    location.href = zipURL(space, list.map(e => e.path), list.length === 1 ? list[0].name : undefined)
  }

  const crumbs = path.split('/').filter(Boolean)
  const spaceName = spaceInfo?.name || spaces.find(s => s.id === space)?.name || (space === 'home' ? 'My files' : 'Files')
  const isMount = space.startsWith('mount:')

  const actionsFor = (list: Entry[], inMenu = false) => {
    const one = list.length === 1 ? list[0] : null
    const B = ({ icon, label, on, danger }: { icon: Parameters<typeof Icon>[0]['name']; label: string; on: () => void; danger?: boolean }) =>
      <button className={danger ? 'danger-text' : ''} onClick={on}><Icon name={icon} size={15} /> {label}</button>
    return <>
      {one && !one.is_dir && previewKind(one) !== 'none' && inMenu && <B icon="eye" label="Open" on={() => open(one)} />}
      {one?.is_dir && inMenu && <B icon="folder" label="Open" on={() => open(one)} />}
      <B icon="download" label={one && !one.is_dir ? 'Download' : 'Download as zip'} on={() => download(list)} />
      <B icon="link" label="Get a link" on={() => setDialog({ kind: 'share', entries: list, tab: 'link' })} />
      {space === 'home' && one && <B icon="users" label="Share with people" on={() => setDialog({ kind: 'share', entries: list, tab: 'people' })} />}
      {one && <B icon="star" label={one.starred ? 'Remove star' : 'Star'} on={() => star(one)} />}
      <hr />
      {writable && one && <B icon="edit" label="Rename" on={() => setDialog({ kind: 'rename', entry: one })} />}
      {writable && <B icon="move" label="Move to…" on={() => setDialog({ kind: 'move', entries: list })} />}
      <B icon="copy" label="Copy to…" on={() => setDialog({ kind: 'copy', entries: list })} />
      {writable && <><hr /><B icon="trash" label={isMount ? 'Delete for good' : 'Move to trash'} danger on={() => setDialog({ kind: 'delete', entries: list })} /></>}
    </>
  }

  return (
    <div className="page browser" onDragEnter={onDragEnter} onDragLeave={onDragLeave} onDragOver={onDragOver} onDrop={e => onDrop(e)}>
      <div className="browser-head">
        <nav className="crumbs">
          <Link to={filesRoute(space)} className="crumb-root">
            <Icon name={space === 'home' ? 'home' : isMount ? 'disk' : 'folderShared'} size={16} />
            <span>{spaceName}</span>
          </Link>
          {crumbs.map((c, i) => <React.Fragment key={i}><Icon name="chevronRight" size={14} className="faint" /><Link to={filesRoute(space, '/' + crumbs.slice(0, i + 1).join('/'))} className={i === crumbs.length - 1 ? 'cur' : ''}>{c}</Link></React.Fragment>)}
          {spaceInfo?.owner && <span className="badge">from {spaceInfo.owner}</span>}
          {data && !writable && <span className="badge" title="You can look and download, not change"><Icon name="lock" size={11} /> view only</span>}
        </nav>
        <div className="toolbar">
          <div className="tool-search">
            <Icon name="search" size={15} className="muted" />
            <input value={q || filter} onChange={e => { if (q) setParams(p => { const n = new URLSearchParams(p); n.delete('q'); return n }); setFilter(e.target.value) }}
              onKeyDown={e => { if (e.key === 'Enter' && filter.trim()) setParams({ q: filter.trim() }); if (e.key === 'Escape') { setFilter(''); setParams({}) } }}
              placeholder={`Filter · Enter searches all of ${crumbs.length ? crumbs[crumbs.length - 1] : spaceName}`} />
            {(filter || q) && <button className="icon-btn sm" onClick={() => { setFilter(''); setParams({}) }}><Icon name="x" size={13} /></button>}
          </div>
          <span className="spacer" />
          {writable && <>
            <Dropdown align="right" className="btn primary" label={<><Icon name="upload" size={15} /> Upload</>}>{close => <>
              <button onClick={() => { close(); fileInput.current?.click() }}><Icon name="file" size={15} /> Files…</button>
              <button onClick={() => { close(); dirInput.current?.click() }}><Icon name="folderUp" size={15} /> A folder…</button>
              <hr />
              <button onClick={() => { close(); setDialog({ kind: 'request' }) }}><Icon name="inbox" size={15} /> Ask someone for files…</button>
            </>}</Dropdown>
            <button className="btn" onClick={() => setDialog({ kind: 'mkdir' })} title="New folder"><Icon name="folderPlus" size={15} /><span className="hide-sm">New folder</span></button>
          </>}
          <Dropdown align="right" className="btn icon-only" title="View and sort" label={<Icon name={view === 'grid' ? 'grid' : 'list'} size={16} />}>{close => <>
            <button onClick={() => { close(); saveSettings({ view: 'list' }) }}><Icon name="list" size={15} /> List {view === 'list' && '✓'}</button>
            <button onClick={() => { close(); saveSettings({ view: 'grid' }) }}><Icon name="grid" size={15} /> Grid {view === 'grid' && '✓'}</button>
            <hr />
            {(['name', 'modified', 'size', 'type'] as const).map(k => <button key={k} onClick={() => { close(); saveSettings(sort === k ? { sort_desc: !desc } : { sort: k, sort_desc: k === 'modified' || k === 'size' }) }}>Sort by {k}{sort === k ? (desc ? ' ↓' : ' ↑') : ''}</button>)}
            <hr />
            <button onClick={() => { close(); saveSettings({ show_hidden: !settings?.show_hidden }); setTimeout(bump, 100) }}>{settings?.show_hidden ? 'Hide' : 'Show'} hidden files</button>
          </>}</Dropdown>
        </div>
        <input ref={fileInput} type="file" multiple hidden onChange={e => { const l = filesFromInput(e.target.files); if (l.length) add(space, path, l); e.target.value = '' }} />
        <input ref={dirInput} type="file" hidden {...{ webkitdirectory: '', directory: '' } as Record<string, string>} onChange={e => { const l = filesFromInput(e.target.files); if (l.length) add(space, path, l); e.target.value = '' }} />
      </div>

      {sel.size > 0 && <div className="sel-bar">
        <button className="icon-btn" onClick={() => setSel(new Set())} title="Clear selection"><Icon name="x" size={15} /></button>
        <b>{sel.size} selected</b>
        <span className="spacer" />
        <button className="btn sm" onClick={() => download(selected)}><Icon name="download" size={14} /> Download</button>
        <button className="btn sm" onClick={() => setDialog({ kind: 'share', entries: selected, tab: 'link' })}><Icon name="link" size={14} /> Link</button>
        {writable && <button className="btn sm" onClick={() => setDialog({ kind: 'move', entries: selected })}><Icon name="move" size={14} /> Move</button>}
        {writable && <button className="btn sm danger-text" onClick={() => setDialog({ kind: 'delete', entries: selected })}><Icon name="trash" size={14} /> Delete</button>}
      </div>}

      {err && <div className="callout danger mt">{err}</div>}
      {!data && !err && <Loading />}
      {q && <div className="search-note">{searching ? <><span className="spinner sm" /> Searching…</> : <>{plural(results?.length || 0, 'match', 'matches')} for “{q}” below {crumbs.length ? crumbs[crumbs.length - 1] : spaceName}</>}</div>}

      {data && !entries.length && !searching && (
        q || filter ? <Empty title="Nothing matches" icon={<Icon name="search" size={28} />}>Try another word.</Empty>
          : <div className={`empty-drop ${writable ? '' : 'ro'}`} onClick={() => writable && fileInput.current?.click()}>
            <Icon name="upload" size={30} />
            <h3>{writable ? 'Drop files or folders here' : 'This folder is empty'}</h3>
            {writable && <p className="muted">or click to choose them — they go to <b>{crumbs.length ? crumbs[crumbs.length - 1] : spaceName}</b></p>}
          </div>
      )}

      {entries.length > 0 && view === 'list' && (
        <div className="file-table" role="grid">
          <div className="file-row head">
            <span className="c-check"><input type="checkbox" checked={sel.size > 0 && sel.size === entries.length} onChange={e => setSel(e.target.checked ? new Set(entries.map(x => x.path)) : new Set())} aria-label="Select all" /></span>
            <SortHead k="name" label="Name" cls="c-name" />
            <SortHead k="size" label="Size" cls="c-size" />
            <SortHead k="modified" label="Modified" cls="c-mod" />
            <span className="c-more" />
          </div>
          {entries.map(e => (
            <div key={e.path} className={`file-row ${sel.has(e.path) ? 'selected' : ''} ${dropFolder === e.path ? 'drop-over' : ''}`}
              draggable={writable} onDragStart={ev => onRowDragStart(ev, e)}
              onDragOver={e.is_dir ? ev => { ev.preventDefault(); ev.stopPropagation(); setDropFolder(e.path) } : undefined}
              onDragLeave={e.is_dir ? () => setDropFolder(d => d === e.path ? null : d) : undefined}
              onDrop={e.is_dir ? ev => { ev.stopPropagation(); onFolderDrop(ev, e) } : undefined}
              onClick={ev => click(e, ev)} onContextMenu={ev => { ev.preventDefault(); setMenu({ x: ev.clientX, y: ev.clientY, entry: e }) }}>
              <span className="c-check" onClick={ev => { ev.stopPropagation(); toggle(e.path); setAnchor(e.path) }}><input type="checkbox" checked={sel.has(e.path)} readOnly aria-label={`Select ${e.name}`} /></span>
              <span className="c-name">
                {THUMB_TYPES.test(e.mime) && e.size < 60e6 ? <img className="mini-thumb" src={thumbURL(space, e.path, 64, e.modified)} alt="" loading="lazy" onError={ev => { (ev.target as HTMLImageElement).style.display = 'none' }} /> : <FileGlyph name={e.name} mime={e.mime} isDir={e.is_dir} size={22} shared={!!e.shared} />}
                <span className="name ellipsis" title={e.name}>{e.name}{results && <small className="muted path-hint"> {e.path.split('/').slice(0, -1).join('/') || '/'}</small>}</span>
                {e.starred && <Icon name="star" size={13} fill className="star-on" />}
                {!!e.shared && <span className="shared-pill" title={e.shared >= 1000 ? 'Shared with everyone' : `Shared with ${plural(e.shared, 'person', 'people')}`}><Icon name="users" size={11} /></span>}
              </span>
              <span className="c-size muted">{e.is_dir ? '—' : bytes(e.size)}</span>
              <span className="c-mod muted" title={stamp(e.modified)}>{ago(e.modified)}</span>
              <span className="c-more" onClick={ev => ev.stopPropagation()}>
                <button className={`icon-btn row-more ${menu?.entry.path === e.path ? 'on' : ''}`} title="More" aria-label="More"
                  onClick={ev => { const r = ev.currentTarget.getBoundingClientRect(); setMenu({ x: r.right - 220, y: r.bottom + 4, entry: e }) }}><Icon name="more" size={16} /></button>
              </span>
            </div>
          ))}
          <div className="file-foot muted small">{plural(entries.filter(e => e.is_dir).length, 'folder')} · {plural(files.length, 'file')} · {bytes(files.reduce((s, f) => s + f.size, 0))}</div>
        </div>
      )}

      {entries.length > 0 && view === 'grid' && (
        <div className="file-grid">
          {entries.map(e => (
            <div key={e.path} className={`file-card ${sel.has(e.path) ? 'selected' : ''} ${dropFolder === e.path ? 'drop-over' : ''}`}
              draggable={writable} onDragStart={ev => onRowDragStart(ev, e)}
              onDragOver={e.is_dir ? ev => { ev.preventDefault(); ev.stopPropagation(); setDropFolder(e.path) } : undefined}
              onDragLeave={e.is_dir ? () => setDropFolder(d => d === e.path ? null : d) : undefined}
              onDrop={e.is_dir ? ev => { ev.stopPropagation(); onFolderDrop(ev, e) } : undefined}
              onClick={ev => click(e, ev)} onContextMenu={ev => { ev.preventDefault(); setMenu({ x: ev.clientX, y: ev.clientY, entry: e }) }}>
              <div className="card-thumb">
                {THUMB_TYPES.test(e.mime) && e.size < 60e6 ? <img src={thumbURL(space, e.path, 320, e.modified)} alt="" loading="lazy" /> : <FileGlyph name={e.name} mime={e.mime} isDir={e.is_dir} size={52} shared={!!e.shared} />}
                <span className="card-check" onClick={ev => { ev.stopPropagation(); toggle(e.path) }}><input type="checkbox" checked={sel.has(e.path)} readOnly /></span>
                {e.starred && <Icon name="star" size={14} fill className="star-on card-star" />}
              </div>
              <div className="card-name ellipsis" title={e.name}>{e.name}</div>
              <div className="card-meta muted">{e.is_dir ? 'Folder' : bytes(e.size)}</div>
            </div>
          ))}
        </div>
      )}

      {dragOver && <div className="drop-overlay"><div><Icon name="upload" size={36} /><h2>Drop to upload</h2><p>into <b>{crumbs.length ? crumbs[crumbs.length - 1] : spaceName}</b></p></div></div>}

      {menu && <ContextMenu x={menu.x} y={menu.y} onClose={() => setMenu(null)}>{actionsFor(sel.has(menu.entry.path) && sel.size > 1 ? selected : [menu.entry], true)}</ContextMenu>}

      {preview !== null && files[preview] && <Preview space={space} entries={files} index={preview} writable={writable} onClose={() => setPreview(null)} onIndex={setPreview} onSaved={load}
        actions={e => <>
          <button className="icon-btn" onClick={() => star(e)} title={e.starred ? 'Remove star' : 'Star'}><Icon name="star" size={16} fill={e.starred} className={e.starred ? 'star-on' : ''} /></button>
          <button className="icon-btn" onClick={() => setDialog({ kind: 'share', entries: [e], tab: 'link' })} title="Get a link"><Icon name="link" size={16} /></button>
        </>} />}

      {dialog?.kind === 'mkdir' && <Prompt title="New folder" placeholder="Folder name" button="Create" onClose={() => setDialog(null)} onDone={async name => {
        try { await api.post(`${V1}/fs/mkdir`, { space, path: (path === '/' ? '' : path) + '/' + name }); setDialog(null); bump(); navigate(filesRoute(space, (path === '/' ? '' : path) + '/' + name)) } catch (e) { toast((e as Error).message, 'err') }
      }} />}
      {dialog?.kind === 'rename' && <Prompt title={`Rename “${dialog.entry.name}”`} value={dialog.entry.name} button="Rename" onClose={() => setDialog(null)} onDone={async name => {
        try { await api.post(`${V1}/fs/rename`, { space, path: dialog.entry.path, name }); setDialog(null); setSel(new Set()); bump() } catch (e) { toast((e as Error).message, 'err') }
      }} />}
      {dialog?.kind === 'delete' && <Confirm title={isMount ? 'Delete for good?' : 'Move to trash?'} onCancel={() => setDialog(null)} onConfirm={() => doDelete(dialog.entries)}
        message={isMount ? <>This is a server folder: <b>{dialog.entries.length === 1 ? dialog.entries[0].name : plural(dialog.entries.length, 'item')}</b> will be deleted from the disk and cannot be restored here.</> : <><b>{dialog.entries.length === 1 ? dialog.entries[0].name : plural(dialog.entries.length, 'item')}</b> go{dialog.entries.length === 1 ? 'es' : ''} to the trash{space === 'home' ? '' : ' of its owner'}; shares and links to it stop working.</>} />}
      {(dialog?.kind === 'move' || dialog?.kind === 'copy') && <FolderPicker title={`${dialog.kind === 'move' ? 'Move' : 'Copy'} ${dialog.entries.length === 1 ? '“' + dialog.entries[0].name + '”' : plural(dialog.entries.length, 'item')}`} button={dialog.kind === 'move' ? 'Move here' : 'Copy here'}
        startSpace={writable ? space : 'home'} startPath={writable ? path : '/'} onClose={() => setDialog(null)} onPick={(s, p) => { const d = dialog; setDialog(null); doMove(d.kind, d.entries.map(e => e.path), s, p) }} />}
      {dialog?.kind === 'share' && <ShareDialog space={space} entries={dialog.entries} initialTab={dialog.tab} onClose={() => { setDialog(null); load() }} />}
      {dialog?.kind === 'request' && <RequestDialog space={space} path={path} folderName={crumbs.length ? crumbs[crumbs.length - 1] : spaceName} onClose={() => setDialog(null)} />}
    </div>
  )

  function SortHead({ k, label, cls }: { k: 'name' | 'size' | 'modified'; label: string; cls: string }) {
    return <button className={`${cls} sort-head ${sort === k ? 'on' : ''}`} onClick={() => saveSettings(sort === k ? { sort_desc: !desc } : { sort: k, sort_desc: k !== 'name' })}>{label}{sort === k ? (desc ? ' ↓' : ' ↑') : ''}</button>
  }
}
