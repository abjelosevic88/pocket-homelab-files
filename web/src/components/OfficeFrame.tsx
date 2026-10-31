// Pocket Office's editor over the whole window, for one file; its own bar has the Close button.
import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { api, qs, V1 } from '../lib/api'
import type { Entry } from '../lib/types'
import { Icon } from './icons'
import { Spinner } from './ui'

export function OfficeFrame({ space, entry, onClose }: { space: string; entry: Entry; onClose: () => void }) {
  const [url, setUrl] = useState('')
  const [err, setErr] = useState('')
  useEffect(() => {
    api.get<{ url: string }>(`${V1}/office/open${qs({ space, path: entry.path })}`).then(r => setUrl(r.url)).catch(e => setErr((e as Error).message))
  }, [space, entry.path])
  useEffect(() => {
    if (!url) return
    const origin = new URL(url).origin
    const onMsg = (e: MessageEvent) => { if (e.origin === origin && e.data?.type === 'pocket-office:close') onClose() }
    window.addEventListener('message', onMsg)
    return () => window.removeEventListener('message', onMsg)
  }, [url, onClose])
  useEffect(() => { const o = document.body.style.overflow; document.body.style.overflow = 'hidden'; return () => { document.body.style.overflow = o } }, [])
  return createPortal(
    <div className="office-frame" role="dialog" aria-label={`${entry.name} in Pocket Office`}>
      {url && <iframe src={url} title={`${entry.name} in Pocket Office`} allow="clipboard-read *; clipboard-write *; fullscreen *" />}
      {!url && !err && <div className="office-frame-msg"><Spinner size={18} /> Opening {entry.name} in Pocket Office…</div>}
      {err && <div className="office-frame-msg"><Icon name="info" size={18} /><span>{err}</span><button type="button" className="btn" onClick={onClose}>Close</button></div>}
      {url && <a className="office-frame-tab" href={url.replace('embed=1', 'embed=0')} target="_blank" rel="noreferrer" title="Open in a new tab (if the editor does not load here)"><Icon name="external" size={14} /></a>}
    </div>, document.body)
}
