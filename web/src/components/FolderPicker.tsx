import { useEffect, useState } from 'react'
import { api, qs, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Listing } from '../lib/types'
import { FileGlyph, Icon } from './icons'
import { Loading, Modal } from './ui'

/** Pick a folder (in any place you may write to). */
export function FolderPicker({ title, button, startSpace = 'home', startPath = '/', onPick, onClose, writableOnly = true }: { title: string; button: string; startSpace?: string; startPath?: string; onPick: (space: string, path: string) => void; onClose: () => void; writableOnly?: boolean }) {
  const { spaces } = useApp()
  const [space, setSpace] = useState(startSpace)
  const [path, setPath] = useState(startPath)
  const [data, setData] = useState<Listing | null>(null)
  const [err, setErr] = useState('')
  const usable = spaces.filter(s => s.is_dir && (!writableOnly || s.perm === 'edit'))

  useEffect(() => {
    setData(null); setErr('')
    api.get<Listing>(`${V1}/fs/list${qs({ space, path })}`).then(setData).catch(e => setErr((e as Error).message))
  }, [space, path])

  const crumbs = path.split('/').filter(Boolean)
  const cur = usable.find(s => s.id === space)
  return (
    <Modal title={title} onClose={onClose}>
      <div className="picker">
        <div className="picker-spaces">
          {usable.map(s => <button key={s.id} type="button" className={`chip ${s.id === space ? 'active' : ''}`} onClick={() => { setSpace(s.id); setPath('/') }}><Icon name={s.kind === 'home' ? 'home' : s.kind === 'mount' ? 'disk' : 'folderShared'} size={13} /> {s.name}</button>)}
        </div>
        <div className="crumbs small">
          <button type="button" className="link-btn" onClick={() => setPath('/')}>{cur?.name || 'Files'}</button>
          {crumbs.map((c, i) => <span key={i} className="row" style={{ gap: 4 }}><Icon name="chevronRight" size={12} className="faint" /><button type="button" className="link-btn" onClick={() => setPath('/' + crumbs.slice(0, i + 1).join('/'))}>{c}</button></span>)}
        </div>
        <div className="picker-list">
          {err && <div className="callout danger">{err}</div>}
          {!data && !err && <Loading />}
          {data && path !== '/' && <button type="button" className="pick-row" onClick={() => setPath('/' + crumbs.slice(0, -1).join('/'))}><Icon name="arrowUp" size={15} /> ..</button>}
          {data?.entries.filter(e => e.is_dir).map(e => <button type="button" key={e.path} className="pick-row" onClick={() => setPath(e.path)}><FileGlyph name={e.name} mime="" isDir size={18} /> {e.name}</button>)}
          {data && !data.entries.some(e => e.is_dir) && <p className="muted small center pad">No folders here</p>}
        </div>
      </div>
      <div className="form-actions">
        <span className="grow small muted ellipsis">{cur?.name}{path === '/' ? '' : path}</span>
        <button type="button" className="btn" onClick={onClose}>Cancel</button>
        <button type="button" className="btn primary" disabled={!data || (writableOnly && !data.writable)} onClick={() => onPick(space, path)}>{button}</button>
      </div>
    </Modal>
  )
}
