import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Snippet } from '../components/Snippet'
import { OfficeFrame } from '../components/OfficeFrame'
import { isOfficeDoc, useOffice } from '../lib/office'
import { api, filesRoute, qs, rawURL, thumbURL, V1, zipURL } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, bytes, plural, stamp } from '../lib/format'
import { filesFromDrop, filesFromInput, useUploads } from '../lib/uploads'
import type { Entry, Listing } from '../lib/types'
import { FileGlyph, Icon, fileKind } from '../components/icons'
import { setHere } from '../lib/here'
import { type Anchor, Confirm, Empty, Loading, Menu, type MenuItem, MenuButton, Prompt, useIsMobile, useToast } from '../components/ui'
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
  const mobile = useIsMobile()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const [data, setData] = useState<Listing | null>(null)
  const [err, setErr] = useState('')
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [anchor, setAnchor] = useState<string | null>(null)
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const [menu, setMenu] = useState<{ at: Anchor; align: 'left' | 'right'; entry: Entry } | null>(null)
  const [preview, setPreview] = useState<number | null>(null)
  const [officeDoc, setOfficeDoc] = useState<Entry | null>(null)
  const office = useOffice()
  const inOffice = (e: Entry) => office.available && isOfficeDoc(e, space, writable)
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
    } catch (e) {
      const offline = (e as { status?: number }).status === 0 || e instanceof TypeError
      setErr(offline ? 'Cannot reach the server right now — trying again…' : (e as Error).message)
      if (!offline) setData(null)
      if (offline) setTimeout(() => setRetry(n => n + 1), 3000)
    }
  }, [space, path, navigate])

  useEffect(() => { setData(null); setSel(new Set()); setFilter(''); setPreview(null) }, [space, path])
  const [retry, setRetry] = useState(0)
  useEffect(() => { load() }, [load, version, retry])
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
    if (i >= 0 && inOffice(files[i])) setOfficeDoc(files[i])
    else if (i >= 0) setPreview(i)
    setParams(p => { const n = new URLSearchParams(p); n.delete('open'); return n }, { replace: true })
  }, [data, params]) // eslint-disable-line react-hooks/exhaustive-deps

  const open = (e: Entry) => {
    if (e.is_dir) { navigate(filesRoute(space, e.path)); return }
    if (results) { navigate(filesRoute(space, e.path.split('/').slice(0, -1).join('/') || '/') + `?open=${encodeURIComponent(e.name)}`); return }
    if (inOffice(e)) { setOfficeDoc(e); return }
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
      if (dialog || preview !== null || (e.target as HTMLElement)?.closest?.('input, textarea, .modal, .menu, .palette-backdrop')) return
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

  const actionsFor = (list: Entry[]): MenuItem[] => {
    const one = list.length === 1 ? list[0] : null
    const items: MenuItem[] = []
    if (one && inOffice(one)) items.push({ label: 'Edit in Pocket Office', icon: 'edit', onSelect: () => setOfficeDoc(one) })
    if (one && !one.is_dir && previewKind(one) !== 'none') items.push({ label: inOffice(one) ? 'Preview' : 'Open', icon: 'eye', onSelect: () => setPreview(files.indexOf(one)) })
    if (one?.is_dir) items.push({ label: 'Open', icon: 'folder', onSelect: () => open(one) })
    items.push({ label: one && !one.is_dir ? 'Download' : 'Download as zip', icon: 'download', onSelect: () => download(list) })
    items.push({ label: 'Share…', icon: 'send', onSelect: () => setDialog({ kind: 'share', entries: list }) })
    if (one) items.push({ label: one.starred ? 'Remove star' : 'Star', icon: 'star', onSelect: () => star(one) })
    items.push('sep')
    if (writable && one) items.push({ label: 'Rename', icon: 'edit', hint: 'F2', onSelect: () => setDialog({ kind: 'rename', entry: one }) })
    if (writable) items.push({ label: 'Move to…', icon: 'move', onSelect: () => setDialog({ kind: 'move', entries: list }) })
    items.push({ label: 'Copy to…', icon: 'copy', onSelect: () => setDialog({ kind: 'copy', entries: list }) })
    if (writable) items.push('sep', { label: isMount ? 'Delete for good' : 'Move to trash', icon: 'trash', danger: true, onSelect: () => setDialog({ kind: 'delete', entries: list }) })
    return items
  }

  // the open folder, for the sidebar's New menu, the palette and the shortcuts
  const folderName = crumbs.length ? crumbs[crumbs.length - 1] : spaceName
  useEffect(() => { if (data) setHere({ space, path, writable, name: folderName }) }, [data, space, path, writable, folderName])
  useEffect(() => () => setHere(null), [])

  const sortItems: MenuItem[] = [
    { heading: 'View' },
    { label: 'List', checked: view === 'list', onSelect: () => saveSettings({ view: 'list' }) },
    { label: 'Grid', checked: view === 'grid', onSelect: () => saveSettings({ view: 'grid' }) },
    'sep',
    { heading: 'Sort by' },
    ...(['name', 'modified', 'size', 'type'] as const).map(k => ({ label: k[0].toUpperCase() + k.slice(1), checked: sort === k, hint: sort === k ? <Icon name={desc ? 'arrowDown' : 'arrowUp'} size={13} /> : undefined, onSelect: () => saveSettings(sort === k ? { sort_desc: !desc } : { sort: k, sort_desc: k === 'modified' || k === 'size' }) })),
    'sep',
    { label: `${settings?.show_hidden ? 'Hide' : 'Show'} hidden files`, icon: settings?.show_hidden ? 'eyeOff' : 'eye', onSelect: () => { saveSettings({ show_hidden: !settings?.show_hidden }); setTimeout(bump, 100) } },
  ]

  return (
    <div className="page browser" onDragEnter={onDragEnter} onDragLeave={onDragLeave} onDragOver={onDragOver} onDrop={e => onDrop(e)}>
      <div className="browser-head">
        <div className="toolbar">
          <div className="tool-search">
            <Icon name="search" size={15} className="muted" />
            <input value={q || filter} onChange={e => { if (q) setParams(p => { const n = new URLSearchParams(p); n.delete('q'); return n }); setFilter(e.target.value) }}
              onKeyDown={e => { if (e.key === 'Enter' && filter.trim()) setParams({ q: filter.trim() }); if (e.key === 'Escape') { setFilter(''); setParams({}) } }}
              placeholder={mobile ? 'Filter · Enter to search' : `Filter · Enter searches all of ${folderName}`} aria-label="Filter or search" />
            {(filter || q) && <button type="button" className="btn ghost icon-only xs" aria-label="Clear" onClick={() => { setFilter(''); setParams({}) }}><Icon name="x" size={13} /></button>}
          </div>
          {spaceInfo?.owner && <span className="badge">from {spaceInfo.owner}</span>}
          {data && !writable && <span className="badge" title="You can look and download, not change"><Icon name="lock" size={11} /> view only</span>}
          <span className="spacer" />
          {writable && <>
            <MenuButton align="right" width={240} className="btn primary" label="Upload" items={[
              { label: 'Files…', icon: 'file', onSelect: () => fileInput.current?.click() },
              { label: 'A folder…', icon: 'folderUp', onSelect: () => dirInput.current?.click() },
              'sep',
              { label: 'Ask someone for files…', icon: 'inbox', onSelect: () => setDialog({ kind: 'request' }) },
            ]}><Icon name="upload" size={15} /><span className="hide-sm">Upload</span></MenuButton>
            <button type="button" className="btn" onClick={() => setDialog({ kind: 'mkdir' })} title="New folder (n)"><Icon name="folderPlus" size={15} /><span className="hide-sm">New folder</span></button>
          </>}
          <MenuButton align="right" width={220} className="btn icon-only" label="View and sort" items={sortItems}><Icon name={view === 'grid' ? 'grid' : 'list'} size={16} /></MenuButton>
        </div>
        <input ref={fileInput} type="file" multiple hidden onChange={e => { const l = filesFromInput(e.target.files); if (l.length) add(space, path, l); e.target.value = '' }} />
        <input ref={dirInput} type="file" hidden {...{ webkitdirectory: '', directory: '' } as Record<string, string>} onChange={e => { const l = filesFromInput(e.target.files); if (l.length) add(space, path, l); e.target.value = '' }} />
      </div>

      {sel.size > 0 && <div className="sel-bar">
        <button type="button" className="btn ghost icon-only sm" onClick={() => setSel(new Set())} aria-label="Clear selection" title="Clear selection (esc)"><Icon name="x" size={15} /></button>
        <b>{sel.size} selected</b>
        <span className="spacer" />
        <button type="button" className="btn sm" onClick={() => download(selected)}><Icon name="download" size={14} /> Download</button>
        <button type="button" className="btn sm" onClick={() => setDialog({ kind: 'share', entries: selected })}><Icon name="send" size={14} /> Share</button>
        {writable && <button type="button" className="btn sm" onClick={() => setDialog({ kind: 'move', entries: selected })}><Icon name="move" size={14} /> Move</button>}
        {writable && <button type="button" className="btn sm danger-text" onClick={() => setDialog({ kind: 'delete', entries: selected })}><Icon name="trash" size={14} /> Delete</button>}
      </div>}

      {err && <div className="callout danger mt">{err}</div>}
      {!data && !err && <Loading />}
      {q && <div className="search-note">{searching ? <><span className="spinner sm" /> Searching…</> : <>{plural(results?.length || 0, 'match', 'matches')} for “{q}” below {folderName}, in names and inside documents</>}</div>}

      {data && !entries.length && !searching && (
        q || filter ? <Empty title="Nothing matches" icon={<Icon name="search" size={28} />}>Try another word.</Empty>
          : <div className={`empty-drop ${writable ? '' : 'ro'}`} onClick={() => writable && fileInput.current?.click()}>
            <Icon name="upload" size={30} />
            <h3>{writable ? 'Drop files or folders here' : 'This folder is empty'}</h3>
            {writable && <p className="muted">or click to choose them — they go to <b>{folderName}</b></p>}
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
              onClick={ev => click(e, ev)} onContextMenu={ev => { ev.preventDefault(); setMenu({ at: { x: ev.clientX, y: ev.clientY, w: 0, h: 0 }, align: 'left', entry: e }) }}>
              <span className="c-check" onClick={ev => { ev.stopPropagation(); toggle(e.path); setAnchor(e.path) }}><input type="checkbox" checked={sel.has(e.path)} readOnly aria-label={`Select ${e.name}`} /></span>
              <span className="c-name">
                {THUMB_TYPES.test(e.mime) && e.size < 60e6 ? <img className="mini-thumb" src={thumbURL(space, e.path, 64, e.modified)} alt="" loading="lazy" onError={ev => { (ev.target as HTMLImageElement).style.display = 'none' }} /> : <FileGlyph name={e.name} mime={e.mime} isDir={e.is_dir} size={22} shared={!!e.shared} />}
                <span className="name ellipsis" title={e.name}>{e.name}{results && <small className="muted path-hint"> {e.path.split('/').slice(0, -1).join('/') || '/'}</small>}{results && e.snippet && <Snippet text={e.snippet} className="row-snippet" />}</span>
                {e.starred && <Icon name="star" size={13} fill className="star-on" />}
                {!!e.shared && <span className="shared-pill" title={e.shared >= 1000 ? 'Shared with everyone' : `Shared with ${plural(e.shared, 'person', 'people')}`}><Icon name="users" size={11} /></span>}
              </span>
              <span className="c-size muted">{e.is_dir ? '—' : bytes(e.size)}</span>
              <span className="c-mod muted" title={stamp(e.modified)}>{ago(e.modified)}</span>
              <span className="c-more" onClick={ev => ev.stopPropagation()}>
                <button type="button" className={`btn ghost icon-only sm row-more ${menu?.entry.path === e.path ? 'on' : ''}`} title="More" aria-label={`More for ${e.name}`} aria-haspopup="menu"
                  onClick={ev => { const r = ev.currentTarget.getBoundingClientRect(); setMenu({ at: { x: r.left, y: r.top, w: r.width, h: r.height }, align: 'right', entry: e }) }}><Icon name="more" size={16} /></button>
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
              onClick={ev => click(e, ev)} onContextMenu={ev => { ev.preventDefault(); setMenu({ at: { x: ev.clientX, y: ev.clientY, w: 0, h: 0 }, align: 'left', entry: e }) }}>
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

      {dragOver && <div className="drop-overlay"><div><Icon name="upload" size={36} /><h2>Drop to upload</h2><p>into <b>{folderName}</b></p></div></div>}

      {menu && <Menu anchor={menu.at} align={menu.align} width={220} onClose={() => setMenu(null)} items={actionsFor(sel.has(menu.entry.path) && sel.size > 1 ? selected : [menu.entry])} />}

      {officeDoc && <OfficeFrame space={space} entry={officeDoc} onClose={() => { setOfficeDoc(null); load() }} />}
      {preview !== null && files[preview] && <Preview space={space} entries={files} index={preview} writable={writable} onClose={() => setPreview(null)} onIndex={setPreview} onSaved={load}
        actions={e => <>
          <button type="button" className="icon-btn" onClick={() => star(e)} title={e.starred ? 'Remove star' : 'Star'}><Icon name="star" size={16} fill={e.starred} className={e.starred ? 'star-on' : ''} /></button>
          <button type="button" className="icon-btn" onClick={() => setDialog({ kind: 'share', entries: [e] })} title="Share"><Icon name="send" size={16} /></button>
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
      {dialog?.kind === 'request' && <RequestDialog space={space} path={path} folderName={folderName} onClose={() => setDialog(null)} />}
    </div>
  )

  function SortHead({ k, label, cls }: { k: 'name' | 'size' | 'modified'; label: string; cls: string }) {
    return <button type="button" className={`${cls} sort-head ${sort === k ? 'on' : ''}`} onClick={() => saveSettings(sort === k ? { sort_desc: !desc } : { sort: k, sort_desc: k !== 'name' })}>{label}{sort === k && <Icon name={desc ? 'arrowDown' : 'arrowUp'} size={12} />}</button>
  }
}
