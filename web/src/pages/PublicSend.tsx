import { useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { bytes, plural } from '../lib/format'
import { downloadAllDecrypted, downloadDecrypted, fileURL, openSend, peekMeta, publicSend, readNote, type Opened } from '../lib/sends'
import type { Meta } from '../lib/crypto'
import type { Entry, PublicSend as PS } from '../lib/types'
import { FileGlyph, Icon } from '../components/icons'
import { copyText, Loading, Progress, useToast } from '../components/ui'

const PUB = '/api/v1/public'

function until(t: string) {
  if (!t) return 'never expires'
  const ms = new Date(t).getTime() - Date.now()
  if (ms <= 0) return 'expired'
  const m = Math.round(ms / 60000)
  if (m < 60) return `expires in ${m} min`
  const h = Math.round(m / 60)
  if (h < 48) return `expires in ${h} h`
  return `expires in ${Math.round(h / 24)} days`
}

/** What a stranger sees when they open a link. */
export default function PublicSend() {
  const { id = '' } = useParams()
  const fragment = useMemo(() => decodeURIComponent(location.hash.replace(/^#/, '')), [])
  const [ps, setPs] = useState<PS | null>(null)
  const [err, setErr] = useState('')
  const [password, setPassword] = useState('')
  const [meta, setMeta] = useState<Meta | null>(null)
  const [opened, setOpened] = useState<Opened | null>(null)
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState<[number, number] | null>(null)
  const [note, setNote] = useState<string | null>(null)
  const [reveal, setReveal] = useState(false)
  const { toast } = useToast()

  useEffect(() => {
    document.title = 'Shared with you · Pocket Files'
    publicSend(id).then(async p => {
      setPs(p)
      if (p.available && p.encrypted && !p.has_password && fragment) {
        try { setMeta(await peekMeta(p, fragment, '')) } catch (e) { setErr((e as Error).message) }
      }
    }).catch(e => setErr((e as Error).message))
  }, [id, fragment])

  const unlock = async () => {
    if (!ps) return
    setBusy(true); setErr('')
    try {
      if (ps.encrypted && ps.has_password) setMeta(await peekMeta(ps, fragment, password))
      const o = await openSend(ps, fragment, password)
      setOpened(o)
      if (o.meta) setMeta(o.meta)
      if (ps.kind === 'note') setNote(await readNote(o))
    } catch (e) { setErr((e as Error).message) } finally { setBusy(false) }
  }

  const getAll = async () => {
    if (!opened) return
    setBusy(true)
    try {
      const total = opened.send.sizes?.reduce((a, b) => a + b, 0) || 1
      setProgress([0, total])
      if (opened.meta!.files.length === 1) await downloadDecrypted(opened, 0, n => setProgress([n, total]))
      else await downloadAllDecrypted(opened, n => setProgress([n, total]), (opened.meta!.label || 'files') + '.zip')
    } catch (e) { setErr((e as Error).message) } finally { setBusy(false); setProgress(null) }
  }
  const getOne = async (i: number) => {
    if (!opened) return
    setBusy(true)
    try { const t = opened.send.sizes?.[i] || 1; setProgress([0, t]); await downloadDecrypted(opened, i, n => setProgress([n, t])) } catch (e) { setErr((e as Error).message) } finally { setBusy(false); setProgress(null) }
  }

  if (!ps && !err) return <Shell><Loading /></Shell>
  if (!ps) return <Shell><Gone msg={err} /></Shell>
  if (!ps.available && !opened) return <Shell><Gone msg={ps.reason === 'uploading' ? 'This link is still being uploaded — try again in a moment.' : ps.reason === 'used up' ? 'This link has been downloaded as often as allowed.' : ps.reason === 'expired' ? 'This link has expired.' : ps.reason || 'This link is no longer available.'} /></Shell>

  const files = meta?.files || opened?.send.files?.map(f => ({ name: f.name || 'file', size: f.size, type: f.type || '' })) || ps.files?.map(f => ({ name: f.name || 'file', size: f.size, type: f.type || '' }))
  const isNote = ps.kind === 'note'
  const needsPw = ps.has_password && !opened
  const title = isNote ? 'A note for you' : ps.kind === 'live' ? (ps.entries?.length === 1 ? ps.entries[0].name : plural(ps.file_count, 'item')) : meta?.label || (files ? (files.length === 1 ? files[0].name : plural(files.length, 'file')) : plural(ps.file_count, 'file'))

  return (
    <Shell>
      <div className="pub-head">
        <div className="pub-ico">{isNote ? <Icon name="note" size={26} /> : <Icon name={ps.kind === 'live' ? 'folder' : 'download'} size={26} />}</div>
        <h1>{title}</h1>
        <p className="muted small">
          {!isNote && <>{bytes(ps.total_size)} · </>}{until(ps.expires_at)}{ps.downloads_left >= 0 && <> · {ps.downloads_left === 1 ? 'last download' : `${ps.downloads_left} downloads left`}</>}
        </p>
        {ps.encrypted && <p className="pub-badge"><Icon name="shield" size={13} /> End-to-end encrypted — decrypted here, in your browser</p>}
      </div>
      {ps.encrypted && !fragment && <div className="callout danger">This link is missing its key (the part after #). Ask for the full link.</div>}

      {needsPw && <form className="grid pub-pw" style={{ gap: 10 }} onSubmit={e => { e.preventDefault(); unlock() }}>
        <label className="field"><span className="label"><Icon name="lock" size={12} /> Password</span><input type="password" autoFocus value={password} onChange={e => setPassword(e.target.value)} /></label>
        <button className="btn primary lg" disabled={busy || !password}>{busy ? 'Checking…' : 'Unlock'}</button>
      </form>}

      {!needsPw && !opened && <>
        {files && !isNote && <FileList files={files} />}
        {ps.kind === 'live' && ps.entries && <LiveList entries={ps.entries} />}
        <button className="btn primary lg pub-main" disabled={busy || (ps.encrypted && !fragment)} onClick={unlock}>
          {busy ? <><span className="spinner sm" /> Opening…</> : isNote ? <><Icon name="eye" size={16} /> Show the note</> : <><Icon name="download" size={16} /> {ps.downloads_left === 1 ? 'Open (only once)' : 'Open'}</>}
        </button>
        {ps.downloads_left === 1 && <p className="small muted center"><Icon name="flame" size={12} style={{ display: 'inline', verticalAlign: '-2px', color: '#ea580c' }} /> This link works one time: after you open it, it is gone.</p>}
      </>}

      {opened && isNote && note !== null && <NoteView text={note} format={meta?.note?.format || 'text'} reveal={reveal} setReveal={setReveal} onCopy={() => copyText(note).then(() => toast('Copied'))} />}

      {opened && !isNote && ps.encrypted && files && <>
        <FileList files={files} onGet={busy ? undefined : getOne} />
        <button className="btn primary lg pub-main" disabled={busy} onClick={getAll}><Icon name="download" size={16} /> {files.length === 1 ? 'Download' : 'Download all (.zip)'}</button>
      </>}
      {opened && !isNote && !ps.encrypted && ps.kind !== 'live' && files && <>
        <FileList files={files} hrefs={files.map((_, i) => fileURL(ps.id, i, opened.send.ticket!))} />
        {files.length > 1 && <p className="small muted center">Download each file above.</p>}
        {files.length === 1 && <a className="btn primary lg pub-main" href={fileURL(ps.id, 0, opened.send.ticket!)}><Icon name="download" size={16} /> Download</a>}
      </>}
      {opened && ps.kind === 'live' && <LiveBrowser id={ps.id} ticket={opened.send.ticket!} entries={opened.send.entries || []} />}

      {progress && <Progress value={progress[0]} total={progress[1]} label="Downloading and decrypting…" />}
      {err && <div className="callout danger mt">{err}</div>}
    </Shell>
  )
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="pub-wrap">
      <div className="pub-card">{children}</div>
      <p className="pub-foot small muted"><img src="/favicon.svg" alt="" width={16} height={16} /> Shared with Pocket Files</p>
    </div>
  )
}

function Gone({ msg }: { msg: string }) {
  return <div className="pub-head"><div className="pub-ico off"><Icon name="clock" size={26} /></div><h1>Nothing here any more</h1><p className="muted">{msg}</p></div>
}

function FileList({ files, onGet, hrefs }: { files: { name: string; size: number; type: string }[]; onGet?: (i: number) => void; hrefs?: string[] }) {
  return (
    <ul className="pub-files">
      {files.slice(0, 300).map((f, i) => (
        <li key={i}>
          <FileGlyph name={f.name} mime={f.type} isDir={false} size={18} />
          <span className="grow ellipsis" title={f.name}>{f.name}</span>
          <small className="muted">{bytes(f.size)}</small>
          {hrefs ? <a className="icon-btn" href={hrefs[i]} title="Download"><Icon name="download" size={15} /></a> : onGet && files.length > 1 ? <button className="icon-btn" onClick={() => onGet(i)} title="Download"><Icon name="download" size={15} /></button> : null}
        </li>
      ))}
      {files.length > 300 && <li className="muted small">…and {files.length - 300} more</li>}
    </ul>
  )
}

function LiveList({ entries }: { entries: Entry[] }) {
  return <ul className="pub-files">{entries.map(e => <li key={e.path}><FileGlyph name={e.name} mime={e.mime} isDir={e.is_dir} size={18} /><span className="grow ellipsis">{e.name}</span><small className="muted">{e.is_dir ? 'folder' : bytes(e.size)}</small></li>)}</ul>
}

function LiveBrowser({ id, ticket, entries: top }: { id: string; ticket: string; entries: Entry[] }) {
  const [path, setPath] = useState<string | null>(null)
  const [list, setList] = useState<Entry[]>(top)
  const t = encodeURIComponent(ticket)
  useEffect(() => {
    if (path === null) { setList(top); return }
    fetch(`${PUB}/sends/${id}/live/list?t=${t}&path=${encodeURIComponent(path)}`).then(r => r.json()).then(j => setList(j.entries || []))
  }, [path, id, t, top])
  const up = path && !top.some(e => e.path === path) ? path.split('/').slice(0, -1).join('/') || null : null
  return <>
    <ul className="pub-files">
      {path !== null && <li><button className="link-btn row" onClick={() => setPath(up)}><Icon name="arrowUp" size={15} /> Back</button></li>}
      {list.map(e => (
        <li key={e.path}>
          <FileGlyph name={e.name} mime={e.mime} isDir={e.is_dir} size={18} />
          {e.is_dir ? <button className="link-btn grow ellipsis" onClick={() => setPath(e.path)}><b>{e.name}</b></button> : <span className="grow ellipsis">{e.name}</span>}
          <small className="muted">{e.is_dir ? 'folder' : bytes(e.size)}</small>
          <a className="icon-btn" title={e.is_dir ? 'Download as zip' : 'Download'} href={e.is_dir ? `${PUB}/sends/${id}/live/zip?t=${t}&path=${encodeURIComponent(e.path)}` : `${PUB}/sends/${id}/live/file?t=${t}&path=${encodeURIComponent(e.path)}`}><Icon name="download" size={15} /></a>
        </li>
      ))}
    </ul>
    {(top.length > 1 || top.some(e => e.is_dir)) && <a className="btn primary lg pub-main" href={`${PUB}/sends/${id}/live/zip?t=${t}`}><Icon name="download" size={16} /> Download everything (.zip)</a>}
    {top.length === 1 && !top[0].is_dir && <a className="btn primary lg pub-main" href={`${PUB}/sends/${id}/live/file?t=${t}&path=${encodeURIComponent(top[0].path)}`}><Icon name="download" size={16} /> Download</a>}
  </>
}

function NoteView({ text, format, reveal, setReveal, onCopy }: { text: string; format: string; reveal: boolean; setReveal: (v: boolean) => void; onCopy: () => void }) {
  return (
    <div className="pub-note">
      <div className="row"><span className="grow small muted">{format === 'secret' ? 'Secret' : format === 'code' ? 'Code' : format === 'markdown' ? 'Markdown' : 'Text'}</span>
        {format === 'secret' && <button className="btn sm" onClick={() => setReveal(!reveal)}><Icon name={reveal ? 'eyeOff' : 'eye'} size={13} /> {reveal ? 'Hide' : 'Show'}</button>}
        <button className="btn sm" onClick={onCopy}><Icon name="copy" size={13} /> Copy</button></div>
      {format === 'secret' ? <pre className="mono secret">{reveal ? text : '•'.repeat(Math.min(24, Math.max(8, text.length)))}</pre>
        : format === 'code' ? <pre className="mono">{text}</pre>
          : <div className="note-text">{text}</div>}
      <p className="small muted">This note will not be shown again once the link is used up — copy what you need.</p>
    </div>
  )
}
