import { useEffect, useState } from 'react'
import { api, rawURL, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { createSend, emailSend, EXPIRY_OPTIONS, type SendOptions } from '../lib/sends'
import type { Access, Entry, Person, Perm, Send, Share } from '../lib/types'
import { Avatar } from './people'
import { Icon } from './icons'
import { EmailSetupHint, RecipientList } from './EmailLink'
import { defaultOptions, downloadsLabel, SendOptionsForm } from './SendOptions'
import { CopyField, Modal, Progress, Segmented, copyText, useToast } from './ui'
import { Select } from './Select'

/**
 * Share files: type an email and press Send (a link is made and mailed in
 * one go). Copy the link instead, or share with people on this box.
 */
export function ShareDialog({ space, entries, onClose, initialTab }: { space: string; entries: Entry[]; onClose: () => void; initialTab?: 'people' | 'link' }) {
  const canPeople = space === 'home' && entries.length === 1
  const [people, setPeople] = useState(initialTab === 'people' && canPeople)
  const title = entries.length === 1 ? `Share “${entries[0].name}”` : `Share ${entries.length} items`
  return (
    <Modal title={title} onClose={onClose}>
      {people ? <PeopleShare entry={entries[0]} onClose={onClose} onBack={() => setPeople(false)} />
        : <SendShare space={space} entries={entries} onClose={onClose} onPeople={canPeople ? () => setPeople(true) : undefined} />}
    </Modal>
  )
}

function expiryLabel(v: string) {
  const o = EXPIRY_OPTIONS.find(x => x.value === v)
  return v === 'never' ? 'never expires' : `expires in ${o ? o.label.toLowerCase() : v}`
}

function SendShare({ space, entries, onClose, onPeople }: { space: string; entries: Entry[]; onClose: () => void; onPeople?: () => void }) {
  const { settings, bump } = useApp()
  const { toast } = useToast()
  const files = entries.filter(e => !e.is_dir)
  const canEncrypt = files.length === entries.length && files.reduce((s, f) => s + f.size, 0) < 2 * 1024 ** 3
  // From here a link serves the files where they are (instant, works for
  // folders); an encrypted copy is an option.
  const [opt, setOpt] = useState<SendOptions>(() => ({ ...defaultOptions(settings), encrypted: false }))
  const [showOpts, setShowOpts] = useState(false)
  const [to, setTo] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState<'' | 'send' | 'copy'>('')
  const [progress, setProgress] = useState<[number, number] | null>(null)
  const [made, setMade] = useState<{ id: string; link: string; key: string } | null>(null)
  const [abort] = useState(() => new AbortController())
  const canEmail = !!settings?.smtp_host
  const optKey = JSON.stringify(opt)

  // makes the link once (again only when the options changed)
  const makeLink = async () => {
    if (made && made.key === optKey) return made
    let id: string, link: string
    if (opt.encrypted && canEncrypt) {
      setProgress([0, 1])
      const items = []
      for (const f of files) {
        const res = await fetch(rawURL(space, f.path), { credentials: 'same-origin', signal: abort.signal })
        if (!res.ok) throw new Error(`could not read ${f.name}`)
        items.push({ file: await res.blob(), name: f.name, type: f.mime })
      }
      const r = await createSend(items, { ...opt, label: opt.label || (files.length === 1 ? files[0].name : `${files[0].name} and ${files.length - 1} more`) }, (d, t) => setProgress([d, t]), abort.signal)
      id = r.send.id; link = r.link
    } else {
      const label = opt.label || (entries.length === 1 ? entries[0].name : `${entries[0].name} and ${entries.length - 1} more`)
      const s = await api.post<Send>(`${V1}/sends`, { kind: 'live', live: { space, paths: entries.map(e => e.path) }, expires: opt.expires, max_downloads: opt.max_downloads, password: opt.password, notify: opt.notify, label })
      id = s.id; link = s.url
    }
    const m = { id, link, key: optKey }
    setMade(m); bump()
    return m
  }

  const send = async () => {
    setBusy('send')
    try {
      const m = await makeLink()
      const sent = await emailSend(m.id, to, message, m.link)
      toast(`Sent to ${sent.join(', ')}`)
      onClose()
    } catch (e) { if (!abort.signal.aborted) toast((e as Error).message, 'err') } finally { setBusy(''); setProgress(null) }
  }
  const copy = async () => {
    setBusy('copy')
    try { const m = await makeLink(); await copyText(m.link); toast('Link copied') } catch (e) { if (!abort.signal.aborted) toast((e as Error).message, 'err') } finally { setBusy(''); setProgress(null) }
  }

  const summary = [expiryLabel(opt.expires), opt.max_downloads ? downloadsLabel(opt.max_downloads) : 'no download limit', opt.password ? 'password' : '', opt.encrypted && canEncrypt ? 'encrypted copy' : ''].filter(Boolean).join(' · ')

  return (
    <div className="grid" style={{ gap: 12 }}>
      {canEmail ? (
        <form className="grid" style={{ gap: 8 }} onSubmit={e => { e.preventDefault(); if (to.trim()) send() }}>
          <div className="row">
            <input autoFocus type="text" inputMode="email" list="rcpt-share" value={to} onChange={e => setTo(e.target.value)} placeholder="Email address" />
            <button type="submit" className="btn primary" disabled={!!busy || !to.trim()}><Icon name="send" size={15} /> {busy === 'send' ? 'Sending…' : 'Send'}</button>
          </div>
          <RecipientList id="rcpt-share" />
          <textarea value={message} onChange={e => setMessage(e.target.value)} rows={2} placeholder="Add a message (optional)" />
        </form>
      ) : <EmailSetupHint />}

      <button type="button" className="link-btn share-opts" onClick={() => setShowOpts(s => !s)}>
        <Icon name={showOpts ? 'chevronDown' : 'chevronRight'} size={13} /><span className="muted small">Options: {summary}</span>
      </button>
      {showOpts && <SendOptionsForm value={opt} onChange={setOpt} allowEncrypt={canEncrypt} showLabel={false} hideEmail
        encryptHelp={opt.encrypted ? 'An encrypted copy is made in this browser; later changes to the file are not in it.' : 'Off: the link serves the files where they are.'} />}
      {opt.password && <p className="small muted">The password is not put in the email — tell them separately.</p>}

      {progress && <Progress value={progress[0]} total={progress[1]} label="Encrypting and uploading…" />}
      {made && <CopyField value={made.link} label="Copy" />}

      <div className="form-actions">
        {onPeople && <button type="button" className="btn ghost" onClick={onPeople}><Icon name="users" size={15} /> People on this box</button>}
        <span className="spacer" />
        <button type="button" className="btn" disabled={!!busy} onClick={copy}><Icon name="link" size={15} /> {busy === 'copy' ? 'Making link…' : 'Copy link'}</button>
      </div>
    </div>
  )
}

function PeopleShare({ entry, onClose, onBack }: { entry: Entry; onClose: () => void; onBack: () => void }) {
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
  const save = async () => {
    setBusy(true)
    try {
      await api.put(`${V1}/shares/path`, { path: entry.path, members, everyone_perm: everyone })
      toast(members.length || everyone ? 'Sharing saved' : 'No longer shared'); bump(); onClose()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <div className="grid" style={{ gap: 14 }}>
      <p className="muted small">They see it under <b>Shared with me</b> in their Pocket Files. With <b>Can edit</b> they may change it; what they delete goes to your trash.</p>
      <div className="member-list">
        {members.map(m => (
          <div key={key(m)} className="member-row">
            <Avatar user={{ id: m.user_id, name: m.name || '', email: m.email || '' }} size={28} />
            <span className="grow"><b>{m.name || m.email}</b><br /><small className="muted">{m.email}</small></span>
            <Segmented size="sm" value={m.perm} options={[{ value: 'view' as Perm, label: 'Can view' }, { value: 'edit' as Perm, label: 'Can edit' }]} onChange={p => setMembers(ms => ms.map(x => key(x) === key(m) ? { ...x, perm: p } : x))} />
            <button type="button" className="icon-btn" onClick={() => setMembers(ms => ms.filter(x => key(x) !== key(m)))} title="Remove"><Icon name="x" size={15} /></button>
          </div>
        ))}
        {!members.length && <p className="muted small">Not shared with anyone here yet.</p>}
      </div>
      {others.length > 0 ? <Select value="" placeholder="Add a person…" label="Add a person" searchable={others.length > 5} options={others.map(p => ({ value: p.email, label: p.name || p.email, description: p.name ? p.email : undefined, keywords: p.email, icon: <Avatar user={p} size={22} /> }))} onChange={email => { const p = people.find(x => x.email === email)!; setMembers(ms => [...ms, { user_id: p.id, name: p.name, email: p.email, perm: 'view' }]) }} />
        : !members.length && <p className="small muted">Nobody else is in this household yet (add people in Pocket Core).</p>}
      <div className="row wrap"><span className="grow">Everyone on this box</span><Segmented size="sm" value={everyone} options={[{ value: '' as const, label: 'No' }, { value: 'view' as const, label: 'Can view' }, { value: 'edit' as const, label: 'Can edit' }]} onChange={setEveryone} /></div>
      <div className="form-actions">
        <button type="button" className="btn ghost" onClick={onBack}><Icon name="chevronLeft" size={15} /> Back</button>
        {share?.id ? <span className="grow small muted right">Shared since {new Date(share.created_at).toLocaleDateString()}</span> : <span className="grow" />}
        <button type="button" className="btn primary" disabled={busy} onClick={save}>Save</button>
      </div>
    </div>
  )
}
