import { useEffect, useState } from 'react'
import { api, rawURL, V1 } from '../lib/api'
import { bytes, stamp } from '../lib/format'
import type { Entry } from '../lib/types'
import { FileGlyph, Icon } from './icons'
import { Loading, useToast } from './ui'

const TEXT_MAX = 1024 * 1024

export function previewKind(e: Entry): 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'none' {
  const m = e.mime || ''
  if (/^image\/(jpeg|png|gif|webp|svg\+xml|avif|bmp)$/.test(m)) return 'image'
  if (/^video\/(mp4|webm|quicktime|ogg)$/.test(m) || /\.(mp4|m4v|webm|mov)$/i.test(e.name)) return 'video'
  if (m.startsWith('audio/')) return 'audio'
  if (m === 'application/pdf') return 'pdf'
  if ((m.startsWith('text/') || /^application\/(json|xml|x-yaml)$/.test(m) || /\.(md|txt|log|csv|json|ya?ml|toml|ini|conf|srt|vtt|nfo|cue|sh|go|py|js|ts|tsx|html|css|sql|nix|env|m3u)$/i.test(e.name)) && e.size <= TEXT_MAX) return 'text'
  return 'none'
}

/** Full-window preview with previous / next through the folder's files. */
export function Preview({ space, entries, index, writable, onClose, onIndex, onSaved, actions }: { space: string; entries: Entry[]; index: number; writable: boolean; onClose: () => void; onIndex: (i: number) => void; onSaved?: () => void; actions?: (e: Entry) => React.ReactNode }) {
  const e = entries[index]
  const kind = previewKind(e)
  useEffect(() => {
    const h = (ev: KeyboardEvent) => {
      if ((ev.target as HTMLElement)?.tagName === 'TEXTAREA' || document.querySelector('.modal-backdrop, .menu')) return
      if (ev.key === 'Escape') onClose()
      else if (ev.key === 'ArrowRight' && index < entries.length - 1) onIndex(index + 1)
      else if (ev.key === 'ArrowLeft' && index > 0) onIndex(index - 1)
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [index, entries.length, onClose, onIndex])
  const src = rawURL(space, e.path)
  return (
    <div className="viewer" role="dialog" aria-modal="true">
      <div className="viewer-head">
        <FileGlyph name={e.name} mime={e.mime} isDir={false} size={18} />
        <span className="grow ellipsis"><b>{e.name}</b> <small className="muted">{bytes(e.size)} · {stamp(e.modified)}</small></span>
        {actions?.(e)}
        <a className="icon-btn" href={rawURL(space, e.path, true)} title="Download"><Icon name="download" size={17} /></a>
        <a className="icon-btn" href={src} target="_blank" rel="noreferrer" title="Open in a new tab"><Icon name="external" size={16} /></a>
        <button type="button" className="icon-btn" onClick={onClose} title="Close (Esc)"><Icon name="x" size={18} /></button>
      </div>
      <div className="viewer-body" onClick={ev => { if (ev.target === ev.currentTarget) onClose() }}>
        {index > 0 && <button type="button" className="viewer-nav left" onClick={() => onIndex(index - 1)} aria-label="Previous"><Icon name="chevronLeft" size={26} /></button>}
        {kind === 'image' && <img key={src} src={src} alt={e.name} />}
        {kind === 'video' && <video key={src} src={src} controls autoPlay playsInline />}
        {kind === 'audio' && <div className="viewer-audio"><FileGlyph name={e.name} mime={e.mime} isDir={false} size={64} /><audio key={src} src={src} controls autoPlay /></div>}
        {kind === 'pdf' && <iframe key={src} src={src} title={e.name} />}
        {kind === 'text' && <TextView key={src} space={space} entry={e} writable={writable} onSaved={onSaved} />}
        {kind === 'none' && <div className="viewer-none"><FileGlyph name={e.name} mime={e.mime} isDir={false} size={72} /><p>No preview for this kind of file.</p><a className="btn primary" href={rawURL(space, e.path, true)}><Icon name="download" size={15} /> Download ({bytes(e.size)})</a></div>}
        {index < entries.length - 1 && <button type="button" className="viewer-nav right" onClick={() => onIndex(index + 1)} aria-label="Next"><Icon name="chevronRight" size={26} /></button>}
      </div>
    </div>
  )
}

function TextView({ space, entry, writable, onSaved }: { space: string; entry: Entry; writable: boolean; onSaved?: () => void }) {
  const [text, setText] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const { toast } = useToast()
  useEffect(() => { fetch(rawURL(space, entry.path), { credentials: 'same-origin' }).then(r => r.text()).then(t => { setText(t); setDraft(t) }).catch(() => setText('')) }, [space, entry.path])
  if (text === null) return <Loading />
  const save = async () => {
    setBusy(true)
    try { await api.post(`${V1}/fs/text`, { space, path: entry.path, content: draft }); setText(draft); setEditing(false); toast('Saved'); onSaved?.() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <div className="viewer-text">
      {writable && <div className="row viewer-text-bar">
        <span className="grow small muted">{editing ? 'Editing' : 'Plain text'}</span>
        {editing ? <><button type="button" className="btn sm" onClick={() => { setDraft(text); setEditing(false) }}>Cancel</button><button type="button" className="btn sm primary" disabled={busy} onClick={save}>Save</button></>
          : <button type="button" className="btn sm" onClick={() => setEditing(true)}><Icon name="edit" size={13} /> Edit</button>}
      </div>}
      {editing ? <textarea value={draft} onChange={e => setDraft(e.target.value)} spellCheck={false} autoFocus onKeyDown={e => { if ((e.metaKey || e.ctrlKey) && e.key === 's') { e.preventDefault(); save() } }} /> : <pre>{text}</pre>}
    </div>
  )
}
