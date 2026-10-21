import { useState } from 'react'
import { useUploads } from '../lib/uploads'
import { bytes } from '../lib/format'
import { Icon } from './icons'

/** Bottom-right card with every upload's progress. */
export function UploadTray() {
  const { jobs, cancel, clear } = useUploads()
  const [min, setMin] = useState(false)
  if (!jobs.length) return null
  const active = jobs.filter(j => j.state === 'uploading' || j.state === 'waiting')
  const failed = jobs.filter(j => j.state === 'failed')
  const total = jobs.reduce((s, j) => s + (j.state === 'cancelled' ? 0 : j.size), 0)
  const sent = jobs.reduce((s, j) => s + (j.state === 'done' ? j.size : j.state === 'cancelled' ? 0 : j.sent), 0)
  const pct = total ? Math.round((sent / total) * 100) : 100
  return (
    <div className={`upload-tray ${min ? 'min' : ''}`}>
      <div className="tray-head">
        <span className="grow">{active.length ? `Uploading ${active.length} ${active.length === 1 ? 'file' : 'files'} · ${pct}%` : failed.length ? `${failed.length} failed` : 'Uploads done'}</span>
        <button className="icon-btn sm" onClick={() => setMin(m => !m)} title={min ? 'Show' : 'Hide'}><Icon name={min ? 'chevronLeft' : 'chevronDown'} size={14} /></button>
        {!active.length && <button className="icon-btn sm" onClick={clear} title="Close"><Icon name="x" size={14} /></button>}
      </div>
      <div className="bar"><span className="fill" style={{ width: pct + '%' }} /></div>
      {!min && <ul className="tray-list">
        {jobs.slice(-60).reverse().map(j => (
          <li key={j.id} className={j.state}>
            <span className="grow ellipsis" title={j.name}>{j.name}</span>
            <small>{j.state === 'uploading' ? `${bytes(j.sent)} / ${bytes(j.size)}` : j.state === 'failed' ? j.error : j.state === 'waiting' ? 'waiting' : j.state === 'cancelled' ? 'cancelled' : bytes(j.size)}</small>
            {(j.state === 'uploading' || j.state === 'waiting') ? <button className="icon-btn sm" onClick={() => cancel(j.id)} title="Cancel"><Icon name="x" size={13} /></button>
              : j.state === 'done' ? <Icon name="check" size={14} className="ok-ico" /> : <span style={{ width: 26 }} />}
          </li>
        ))}
      </ul>}
    </div>
  )
}
