import { useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import { api, putWithProgress } from '../lib/api'
import { bytes, plural } from '../lib/format'
import { filesFromDrop, filesFromInput } from '../lib/uploads'
import { FileGlyph, Icon } from '../components/icons'
import { Loading } from '../components/ui'

const PUB = '/api/v1/public'

interface Info { id: string; title: string; message: string; has_password: boolean; open: boolean; reason: string; max_files: number; max_file_mb: number; files_received: number; expires_at: string; chunk_mb: number; owner?: string; token?: string }
interface Job { file: File; name: string; sent: number; state: 'waiting' | 'uploading' | 'done' | 'failed'; error?: string }

/** The page behind an upload link: drop files, they go to the owner's folder. */
export default function PublicRequest() {
  const { id = '' } = useParams()
  const [info, setInfo] = useState<Info | null>(null)
  const [err, setErr] = useState('')
  const [token, setToken] = useState('')
  const [password, setPassword] = useState('')
  const [from, setFrom] = useState(() => localStorage.getItem('pf-from') || '')
  const [jobs, setJobs] = useState<Job[]>([])
  const [drag, setDrag] = useState(false)
  const running = useRef(false)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    document.title = 'Upload files · Pocket Files'
    api.get<Info>(`${PUB}/requests/${id}`).then(i => { setInfo(i); if (i.token) setToken(i.token) }).catch(e => setErr((e as Error).message))
  }, [id])

  const unlock = async () => {
    try { const r = await api.post<{ token: string }>(`${PUB}/requests/${id}/unlock`, { password }); setToken(r.token); setErr('') } catch (e) { setErr((e as Error).message) }
  }

  const patch = (i: number, p: Partial<Job>) => setJobs(js => js.map((j, k) => k === i ? { ...j, ...p } : j))

  const queue = useRef<{ idx: number; job: Job }[]>([])
  const count = useRef(0)

  const pump = async () => {
    if (running.current) return
    running.current = true
    localStorage.setItem('pf-from', from)
    while (queue.current.length) {
      const { idx, job: j } = queue.current.shift()!
      if (info?.max_file_mb && j.file.size > info.max_file_mb * 1048576) { patch(idx, { state: 'failed', error: `larger than ${info.max_file_mb} MB` }); continue }
      patch(idx, { state: 'uploading' })
      try {
        const up = await api.post<{ id: string; chunk_mb: number }>(`${PUB}/requests/${id}/uploads`, { token, name: j.name, size: j.file.size, from })
        const chunk = Math.max(1, Math.min(up.chunk_mb || 64, 90)) * 1048576
        let off = 0
        while (off < j.file.size) {
          const piece = j.file.slice(off, off + chunk)
          let tries = 0
          for (;;) {
            try { await putWithProgress(`${PUB}/requests/${id}/uploads/${up.id}?offset=${off}&token=${encodeURIComponent(token)}`, piece, n => patch(idx, { sent: off + n })); break } catch (e) { if (++tries >= 4) throw e; await new Promise(r => setTimeout(r, 1500 * tries)) }
          }
          off += piece.size
        }
        await api.post(`${PUB}/requests/${id}/uploads/${up.id}/finish`, { token })
        patch(idx, { state: 'done', sent: j.file.size })
      } catch (e) { patch(idx, { state: 'failed', error: (e as Error).message }) }
    }
    running.current = false
  }

  const add = (files: { file: File; rel: string }[]) => {
    const fresh = files.map(f => ({ file: f.file, name: f.rel.split('/').pop() || f.file.name, sent: 0, state: 'waiting' as const }))
    for (const job of fresh) queue.current.push({ idx: count.current++, job })
    setJobs(js => [...js, ...fresh])
    pump()
  }

  if (!info && !err) return <Shell><Loading /></Shell>
  if (!info) return <Shell><div className="pub-head"><div className="pub-ico off"><Icon name="clock" size={26} /></div><h1>Nothing here any more</h1><p className="muted">{err}</p></div></Shell>
  if (!info.open) return <Shell><div className="pub-head"><div className="pub-ico off"><Icon name="inbox" size={26} /></div><h1>{info.title}</h1><p className="muted">This upload link {info.reason === 'expired' ? 'has expired' : info.reason === 'full' ? 'has received all the files it takes' : 'is closed'}.</p></div></Shell>

  const done = jobs.filter(j => j.state === 'done').length
  return (
    <Shell>
      <div className="pub-head">
        <div className="pub-ico" style={{ background: '#dcfce7', color: '#15803d' }}><Icon name="inbox" size={26} /></div>
        <h1>{info.title}</h1>
        {info.owner && <p className="muted small">{info.owner} asks you for files</p>}
        {info.message && <p className="pub-msg">{info.message}</p>}
        <p className="muted small">{[info.max_files ? `up to ${plural(info.max_files, 'file')}` : '', info.max_file_mb ? `${info.max_file_mb >= 1024 ? info.max_file_mb / 1024 + ' GB' : info.max_file_mb + ' MB'} each` : '', 'you will not see what others sent'].filter(Boolean).join(' · ')}</p>
      </div>
      {!token ? <form className="grid pub-pw" style={{ gap: 10 }} onSubmit={e => { e.preventDefault(); unlock() }}>
        <label className="field"><span className="label"><Icon name="lock" size={12} /> Password</span><input type="password" autoFocus value={password} onChange={e => setPassword(e.target.value)} /></label>
        <button type="submit" className="btn primary lg" disabled={!password}>Continue</button>
      </form> : <>
        <label className="field"><span className="label">Your name (optional, so they know who sent it)</span><input value={from} onChange={e => setFrom(e.target.value)} placeholder="e.g. Marko" /></label>
        <div className={`send-drop ${drag ? 'on' : ''}`} onClick={() => input.current?.click()}
          onDragOver={e => { e.preventDefault(); setDrag(true) }} onDragLeave={() => setDrag(false)}
          onDrop={async e => { e.preventDefault(); setDrag(false); add(await filesFromDrop(e.dataTransfer)) }}>
          <Icon name="upload" size={30} /><b>Drop files here</b><span className="muted small">or click to choose</span>
        </div>
        <input ref={input} type="file" multiple hidden onChange={e => { add(filesFromInput(e.target.files)); e.target.value = '' }} />
        {jobs.length > 0 && <ul className="pub-files">
          {jobs.map((j, i) => <li key={i} className={j.state}>
            <FileGlyph name={j.name} mime={j.file.type} isDir={false} size={18} />
            <span className="grow ellipsis">{j.name}</span>
            <small className="muted">{j.state === 'uploading' ? `${Math.round((j.sent / Math.max(1, j.file.size)) * 100)}%` : j.state === 'failed' ? <span className="danger-text">{j.error}</span> : j.state === 'done' ? <Icon name="check" size={14} className="ok-ico" /> : 'waiting'} · {bytes(j.file.size)}</small>
          </li>)}
        </ul>}
        {done > 0 && <div className="callout success row"><Icon name="check" size={16} /> {plural(done, 'file')} delivered.</div>}
      </>}
      {err && token && <div className="callout danger mt">{err}</div>}
    </Shell>
  )
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="pub-wrap">
      <div className="pub-card">{children}</div>
      <p className="pub-foot small muted"><img src="/favicon.svg" alt="" width={16} height={16} /> Pocket Files</p>
    </div>
  )
}
