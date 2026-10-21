import { useEffect, useState } from 'react'
import { api, rawURL, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { bytes } from '../lib/format'
import { createSend, type SendOptions } from '../lib/sends'
import type { Access, Entry, Person, Perm, Send, Share } from '../lib/types'
import { Avatar } from './people'
import { Icon } from './icons'
import { defaultOptions, SendOptionsForm } from './SendOptions'
import { CopyField, Modal, Progress, Segmented, Select, Tabs, useToast } from './ui'

/**
 * Share files: with people on this box (folders and files of your own home),
 * or with anyone through a link (expiry, downloads, password, encryption).
 */
export function ShareDialog({ space, entries, onClose, initialTab }: { space: string; entries: Entry[]; onClose: () => void; initialTab?: 'people' | 'link' }) {
  const canPeople = space === 'home' && entries.length === 1
  const [tab, setTab] = useState<'people' | 'link'>(initialTab && (initialTab !== 'people' || canPeople) ? initialTab : canPeople ? 'people' : 'link')
  const title = entries.length === 1 ? `Share “${entries[0].name}”` : `Share ${entries.length} items`
  return (
    <Modal title={title} onClose={onClose}>
      {canPeople && <Tabs tabs={[{ id: 'people', label: 'People on this box' }, { id: 'link', label: 'Link for anyone' }]} value={tab} onChange={setTab} />}
      <div className="mt">
        {tab === 'people' && canPeople ? <PeopleShare entry={entries[0]} onClose={onClose} /> : <LinkShare space={space} entries={entries} onClose={onClose} />}
      </div>
    </Modal>
  )
}

function PeopleShare({ entry, onClose }: { entry: Entry; onClose: () => void }) {
  const { user, bump } = useApp()
  const { toast } = useToast()
  const [people, setPeople] = useState<Person[]>([])
  const [share, setShare] = useState<Share | null>(null)
  const [members, setMembers] = useState<Access[]>([])
  const [everyone, setEveryone] = useState<'' | Perm>('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.get<Person[]>(`${V1}/people`).then(setPeople).catch(() => {})
    api.get<Share>(`${V1}/shares/path?path=${encodeURIComponent(entry.path)}`).then(s => { setShare(s); setMembers(s.members || []); setEveryone(s.everyone_perm || '') }).catch(() => {})
  }, [entry.path])

  const key = (x: { user_id?: number; id?: number; email?: string }) => (x.email || '').toLowerCase() || String(x.user_id ?? x.id)
  const others = people.filter(p => p.email.toLowerCase() !== user?.email.toLowerCase() && !members.some(m => key(m) === key(p)))
  const [typed, setTyped] = useState('')
  const addEmail = () => {
    const e = typed.trim().toLowerCase()
    if (!e || members.some(m => (m.email || '').toLowerCase() === e)) return
    const p = people.find(x => x.email.toLowerCase() === e)
    setMembers(ms => [...ms, { user_id: p?.id || 0, name: p?.name || '', email: e, perm: 'view' }]); setTyped('')
  }
  const save = async () => {
    setBusy(true)
    try {
      await api.put(`${V1}/shares/path`, { path: entry.path, members, everyone_perm: everyone })
      toast(members.length || everyone ? 'Sharing saved' : 'No longer shared'); bump(); onClose()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <div className="grid" style={{ gap: 14 }}>
      <p className="muted small">They see it under <b>Shared with me</b> and over WebDAV. With <b>Can edit</b> they may add, change and delete inside it; what they delete goes to your trash.</p>
      <div className="member-list">
        {members.map(m => (
          <div key={key(m)} className="member-row">
            <Avatar user={{ id: m.user_id, name: m.name || '', email: m.email || '', username: '', role: 'member', created_at: '' }} />
            <span className="grow"><b>{m.name || m.email}</b><br /><small className="muted">{m.email}</small></span>
            <Segmented size="sm" value={m.perm} options={[{ value: 'view' as Perm, label: 'Can view' }, { value: 'edit' as Perm, label: 'Can edit' }]} onChange={p => setMembers(ms => ms.map(x => key(x) === key(m) ? { ...x, perm: p } : x))} />
            <button className="icon-btn" onClick={() => setMembers(ms => ms.filter(x => key(x) !== key(m)))} title="Remove"><Icon name="x" size={15} /></button>
          </div>
        ))}
        {!members.length && <p className="muted small">Not shared with anyone yet.</p>}
      </div>
      {others.length > 0 && <Select value="" placeholder="Add a person…" className="btn" options={others.map(p => ({ value: p.email, label: <span className="row"><b>{p.name || p.email}</b><small className="muted">{p.name ? p.email : ''}</small></span> }))} onChange={email => { const p = people.find(x => x.email === email)!; setMembers(ms => [...ms, { user_id: p.id, name: p.name, email: p.email, perm: 'view' }]) }} />}
      <form className="row" onSubmit={e => { e.preventDefault(); addEmail() }}>
        <input type="email" value={typed} onChange={e => setTyped(e.target.value)} placeholder="…or type their email" />
        <button className="btn" disabled={!typed.trim()}>Add</button>
      </form>
      {!others.length && !members.length && <p className="small muted">Nobody else is in this household yet. Add people in Pocket Core (or Settings → People here).</p>}
      <div className="row wrap"><span className="grow">Everyone on this box</span><Segmented size="sm" value={everyone} options={[{ value: '' as const, label: 'No' }, { value: 'view' as const, label: 'Can view' }, { value: 'edit' as const, label: 'Can edit' }]} onChange={setEveryone} /></div>
      <div className="form-actions">
        {share?.id ? <span className="grow small muted">Shared since {new Date(share.created_at).toLocaleDateString()}</span> : <span className="grow" />}
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn primary" disabled={busy} onClick={save}>Save</button>
      </div>
    </div>
  )
}

function LinkShare({ space, entries, onClose }: { space: string; entries: Entry[]; onClose: () => void }) {
  const { settings, bump } = useApp()
  const { toast } = useToast()
  const files = entries.filter(e => !e.is_dir)
  const canEncrypt = files.length === entries.length && files.reduce((s, f) => s + f.size, 0) < 2 * 1024 ** 3
  const [opt, setOpt] = useState<SendOptions>(() => ({ ...defaultOptions(settings), encrypted: canEncrypt && (settings?.send_encrypted ?? true) }))
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState<[number, number] | null>(null)
  const [link, setLink] = useState('')
  const [abort] = useState(() => new AbortController())

  const create = async () => {
    setBusy(true)
    try {
      if (opt.encrypted) {
        setProgress([0, 1])
        const items = []
        for (const f of files) {
          const res = await fetch(rawURL(space, f.path), { credentials: 'same-origin', signal: abort.signal })
          if (!res.ok) throw new Error(`could not read ${f.name}`)
          items.push({ file: await res.blob(), name: f.name, type: f.mime })
        }
        const r = await createSend(items, { ...opt, label: opt.label || (files.length === 1 ? files[0].name : `${files[0].name} and ${files.length - 1} more`) }, (d, t) => setProgress([d, t]), abort.signal)
        setLink(r.link)
      } else {
        const s = await api.post<Send>(`${V1}/sends`, { kind: 'live', live: { space, paths: entries.map(e => e.path) }, expires: opt.expires, max_downloads: opt.max_downloads, password: opt.password, notify: opt.notify, label: opt.label })
        setLink(s.url)
      }
      bump()
    } catch (e) { if (!abort.signal.aborted) toast((e as Error).message, 'err') } finally { setBusy(false); setProgress(null) }
  }

  if (link) return (
    <div className="grid" style={{ gap: 12 }}>
      <div className="callout success row"><Icon name="check" size={16} /> Link ready{opt.encrypted ? ' — the key is in the link, keep it whole' : ''}.</div>
      <CopyField value={link} label="Copy link" />
      {opt.password && <p className="small muted">Send the password separately.</p>}
      <div className="form-actions"><a className="btn" href="/send">All my links</a><button className="btn primary" onClick={onClose}>Done</button></div>
    </div>
  )
  return (
    <div className="grid" style={{ gap: 12 }}>
      <SendOptionsForm value={opt} onChange={setOpt} allowEncrypt={canEncrypt}
        encryptHelp={opt.encrypted ? `An encrypted copy (${bytes(files.reduce((s, f) => s + f.size, 0))}) is made in this browser; later changes to the file are not in it.` : 'Off: the link serves the file where it is, live — changes show up, folders can be browsed and zipped.'} />
      {!canEncrypt && <p className="small muted">Folders and very large files are shared live (not encrypted): the link serves them where they are.</p>}
      {progress && <Progress value={progress[0]} total={progress[1]} label="Encrypting and uploading…" />}
      <div className="form-actions">
        <button className="btn" onClick={() => { abort.abort(); onClose() }}>Cancel</button>
        <button className="btn primary" disabled={busy} onClick={create}><Icon name="link" size={15} /> Create link</button>
      </div>
    </div>
  )
}
