import { useRef, useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, bytes, plural, stamp } from '../lib/format'
import { createSend, forgetLink, knownLink, type Item, type SendOptions } from '../lib/sends'
import type { NoteFormat } from '../lib/crypto'
import type { Send } from '../lib/types'
import { filesFromDrop, filesFromInput } from '../lib/uploads'
import { FileGlyph, Icon } from '../components/icons'
import { defaultOptions, downloadsLabel, SendOptionsForm } from '../components/SendOptions'
import { Card, Confirm, CopyField, Empty, Modal, PageHeader, Progress, Segmented, copyText, useAsync, useToast } from '../components/ui'

/** SkySend-style: drop files (or write a note), get a link that expires. */
export default function SendPage() {
  const { settings, version, bump } = useApp()
  const { toast } = useToast()
  const [mode, setMode] = useState<'files' | 'note'>('files')
  const [items, setItems] = useState<Item[]>([])
  const [note, setNote] = useState('')
  const [format, setFormat] = useState<NoteFormat>('text')
  const [opt, setOpt] = useState<SendOptions>(() => defaultOptions(settings))
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState<[number, number] | null>(null)
  const [result, setResult] = useState<{ link: string; password: boolean } | null>(null)
  const [drag, setDrag] = useState(false)
  const abort = useRef<AbortController | null>(null)
  const input = useRef<HTMLInputElement>(null)
  const dirInput = useRef<HTMLInputElement>(null)
  const links = useAsync(() => api.get<Send[]>(`${V1}/sends`), [version])

  const addFiles = (list: { file: File; rel: string }[]) => setItems(it => [...it, ...list.map(f => ({ file: f.file as Blob, name: f.rel, type: f.file.type }))])
  const total = items.reduce((s, i) => s + i.file.size, 0)

  const go = async () => {
    setBusy(true)
    abort.current = new AbortController()
    try {
      let r
      if (mode === 'note') {
        if (!note.trim()) return
        r = await createSend([{ file: new Blob([note], { type: 'text/plain' }), name: 'note.txt', type: 'text/plain' }], { ...opt, label: opt.label || note.trim().split('\n')[0].slice(0, 40) }, (d, t) => setProgress([d, t]), abort.current.signal, format)
      } else {
        if (!items.length) return
        r = await createSend(items, { ...opt, label: opt.label || (items.length === 1 ? items[0].name : `${items[0].name.split('/')[0]} and ${items.length - 1} more`) }, (d, t) => setProgress([d, t]), abort.current.signal)
      }
      setResult({ link: r.link, password: !!opt.password })
      setItems([]); setNote(''); setOpt(o => ({ ...o, password: '', label: '' }))
      bump()
    } catch (e) { if (!abort.current?.signal.aborted) toast((e as Error).message, 'err') } finally { setBusy(false); setProgress(null) }
  }

  return (
    <div className="page send-page">
      <PageHeader title="Send files" sub="A link for anyone — encrypted in your browser, gone after the time or downloads you choose." />
      <div className="send-grid">
        <Card className="send-card">
          <Segmented value={mode} onChange={setMode} options={[{ value: 'files', label: <><Icon name="file" size={14} /> Files</> }, { value: 'note', label: <><Icon name="note" size={14} /> Note</> }]} />
          {mode === 'files' ? <>
            <div className={`send-drop ${drag ? 'on' : ''}`} onClick={() => input.current?.click()}
              onDragOver={e => { e.preventDefault(); setDrag(true) }} onDragLeave={() => setDrag(false)}
              onDrop={async e => { e.preventDefault(); setDrag(false); addFiles(await filesFromDrop(e.dataTransfer)) }}>
              <Icon name="upload" size={30} />
              <b>Drop files or folders</b>
              <span className="muted small">or click to choose · <button type="button" className="link-btn" onClick={e => { e.stopPropagation(); dirInput.current?.click() }}><u>pick a folder</u></button></span>
            </div>
            <input ref={input} type="file" multiple hidden onChange={e => { addFiles(filesFromInput(e.target.files)); e.target.value = '' }} />
            <input ref={dirInput} type="file" hidden {...{ webkitdirectory: '' } as Record<string, string>} onChange={e => { addFiles(filesFromInput(e.target.files)); e.target.value = '' }} />
            {items.length > 0 && <ul className="send-items">
              {items.slice(0, 200).map((it, i) => <li key={i}><FileGlyph name={it.name} mime={it.type} isDir={false} size={16} /><span className="grow ellipsis">{it.name}</span><small className="muted">{bytes(it.file.size)}</small><button className="icon-btn sm" onClick={() => setItems(x => x.filter((_, j) => j !== i))}><Icon name="x" size={13} /></button></li>)}
              {items.length > 200 && <li className="muted small">…and {items.length - 200} more</li>}
              <li className="send-total"><span className="grow">{plural(items.length, 'file')}</span><b>{bytes(total)}</b></li>
            </ul>}
          </> : <>
            <Segmented size="sm" value={format} onChange={setFormat} options={[{ value: 'text', label: 'Text' }, { value: 'markdown', label: 'Markdown' }, { value: 'code', label: 'Code' }, { value: 'secret', label: 'Password / secret' }]} />
            <textarea className={`note-input ${format === 'code' || format === 'secret' ? 'mono' : ''}`} value={note} onChange={e => setNote(e.target.value)} rows={9} placeholder={format === 'secret' ? 'The password, API key or secret to pass on…' : 'Write the note…'} spellCheck={format === 'text' || format === 'markdown'} />
          </>}
          <SendOptionsForm value={opt} onChange={setOpt} />
          {progress && <Progress value={progress[0]} total={progress[1]} label={opt.encrypted ? 'Encrypting and uploading…' : 'Uploading…'} />}
          <div className="form-actions">
            {busy && <button className="btn" onClick={() => abort.current?.abort()}>Cancel</button>}
            <button className="btn primary lg" disabled={busy || (mode === 'files' ? !items.length : !note.trim())} onClick={go}><Icon name="link" size={16} /> {busy ? 'Working…' : 'Create link'}</button>
          </div>
        </Card>
        <div className="send-side">
          <h3 className="mb-s">My links</h3>
          {links.data && !links.data.length && <Empty title="No links yet" icon={<Icon name="link" size={26} />}>Links you make — here or from a file's menu — show up here with their downloads.</Empty>}
          <div className="link-list">{links.data?.map(s => <LinkRow key={s.id} s={s} onChanged={links.reload} />)}</div>
        </div>
      </div>
      {result && <Modal title="Your link is ready" onClose={() => setResult(null)}>
        <div className="grid" style={{ gap: 12 }}>
          <CopyField value={result.link} label="Copy link" />
          <p className="small muted">{result.link.includes('#') ? 'The part after # is the key: whoever has the whole link can open it, this server cannot.' : 'Anyone with this link can download until it expires.'}{result.password && ' Send the password another way.'}</p>
          <div className="form-actions"><button className="btn primary" onClick={() => setResult(null)}>Done</button></div>
        </div>
      </Modal>}
    </div>
  )
}

function LinkRow({ s, onChanged }: { s: Send; onChanged: () => void }) {
  const { toast } = useToast()
  const [open, setOpen] = useState(false)
  const [del, setDel] = useState(false)
  const link = knownLink(s)
  const kindIcon = s.kind === 'note' ? 'note' : s.kind === 'live' ? 'folder' : 'file'
  const left = s.max_downloads ? `${s.downloads}/${s.max_downloads} downloads` : `${plural(s.downloads, 'download')}`
  return (
    <div className={`link-card ${s.available ? '' : 'off'}`}>
      <div className="link-ico"><Icon name={kindIcon} size={17} /></div>
      <div className="grow" onClick={() => setOpen(true)} style={{ cursor: 'pointer', minWidth: 0 }}>
        <div className="row wrap"><b className="ellipsis">{s.label || (s.kind === 'note' ? 'Note' : plural(s.file_count, 'file'))}</b>
          {s.encrypted && <span className="badge" title="End-to-end encrypted"><Icon name="shield" size={11} /></span>}
          {s.has_password && <span className="badge" title="Password"><Icon name="lock" size={11} /></span>}
          {s.kind === 'live' && <span className="badge">live</span>}
          {!s.available && <span className="badge">{s.reason}</span>}
        </div>
        <div className="small muted">{s.kind !== 'note' && s.total_size > 0 && <>{bytes(s.total_size)} · </>}{left} · {s.expires_at ? <span title={stamp(s.expires_at)}>{new Date(s.expires_at) < new Date() ? 'expired ' + ago(s.expires_at) : 'expires ' + new Date(s.expires_at).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })}</span> : 'never expires'}</div>
      </div>
      <div className="row">
        {link ? <button className="btn sm" onClick={() => copyText(link).then(() => toast('Link copied'))}><Icon name="copy" size={13} /> Copy</button>
          : <span className="small muted" title="The key of an encrypted link lives only in the browser that made it">key elsewhere</span>}
        <button className="icon-btn" title="Delete" onClick={() => setDel(true)}><Icon name="trash" size={15} /></button>
      </div>
      {open && <LinkDetails id={s.id} onClose={() => { setOpen(false); onChanged() }} />}
      {del && <Confirm title="Delete this link?" message="It stops working right away; uploaded copies are removed from the server." onCancel={() => setDel(false)} onConfirm={async () => { await api.del(`${V1}/sends/${s.id}`); forgetLink(s.id); setDel(false); onChanged() }} />}
    </div>
  )
}

function LinkDetails({ id, onClose }: { id: string; onClose: () => void }) {
  const { data, reload } = useAsync(() => api.get<Send>(`${V1}/sends/${id}`), [id])
  const { toast } = useToast()
  if (!data) return null
  const link = knownLink(data)
  const patch = async (p: Record<string, unknown>) => { try { await api.put(`${V1}/sends/${id}`, p); reload() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Modal title={data.label || 'Link'} onClose={onClose}>
      <div className="grid" style={{ gap: 12 }}>
        {link ? <CopyField value={link} label="Copy" /> : <div className="callout small">This link is end-to-end encrypted and its key was made in another browser — copy it from there.</div>}
        <div className="kv"><span>Downloads</span><b>{data.downloads}{data.max_downloads ? ` of ${data.max_downloads}` : ''}</b>
          <span>Expires</span><b>{data.expires_at ? stamp(data.expires_at) : 'never'}</b>
          <span>Size</span><b>{bytes(data.total_size)} · {plural(data.file_count, data.kind === 'live' ? 'item' : 'file')}</b>
          <span>Made</span><b>{stamp(data.created_at)}</b></div>
        <div className="row wrap">
          <button className="btn sm" onClick={() => patch({ expires: '1d' })}>Extend 1 day</button>
          <button className="btn sm" onClick={() => patch({ expires: '7d' })}>Extend 7 days</button>
          <button className="btn sm" onClick={() => patch({ max_downloads: data.max_downloads ? data.max_downloads + 5 : 0 })} disabled={!data.max_downloads}>+5 downloads</button>
          <button className="btn sm danger-text" onClick={() => patch({ expires: 'now' })}>Expire now</button>
        </div>
        <p className="small muted">Limit: {downloadsLabel(data.max_downloads)}.</p>
        <h4>Downloads</h4>
        {data.log?.length ? <table className="table compact"><tbody>{data.log.map((l, i) => <tr key={i}><td>{stamp(l.at)}</td><td className="mono small">{l.ip}</td><td className="small muted ellipsis" style={{ maxWidth: 200 }} title={l.agent}>{l.agent}</td></tr>)}</tbody></table> : <p className="muted small">Not downloaded yet.</p>}
      </div>
    </Modal>
  )
}
