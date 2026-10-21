import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { emailRequest, emailSend, recentRecipients } from '../lib/sends'
import type { Person } from '../lib/types'
import { Icon } from './icons'
import { Field, useToast } from './ui'

/** Addresses people on the box and recent recipients, for the To field. */
export function RecipientList({ id }: { id: string }) {
  const [people, setPeople] = useState<Person[]>([])
  useEffect(() => { api.get<Person[]>(`${V1}/people`).then(setPeople).catch(() => {}) }, [])
  const all = [...new Set([...recentRecipients(), ...people.map(p => p.email)])]
  return <datalist id={id}>{all.map(e => <option key={e} value={e} />)}</datalist>
}

export function EmailSetupHint() {
  return <p className="small muted"><Icon name="info" size={12} style={{ display: 'inline', verticalAlign: '-2px' }} /> To email links, set up your mail account in <Link to="/settings?tab=email">Settings → Email</Link>.</p>
}

/** The "email it" box under a ready link. */
export function EmailLink({ kind, id, link = '', encrypted = false, onSent }: { kind: 'send' | 'request'; id: string; link?: string; encrypted?: boolean; onSent?: (to: string[]) => void }) {
  const { settings } = useApp()
  const { toast } = useToast()
  const [to, setTo] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState<string[]>([])
  if (!settings?.smtp_host) return <EmailSetupHint />
  if (kind === 'send' && encrypted && !link.includes('#')) return <p className="small muted">This encrypted link's key lives in the browser that made it — email it from there.</p>
  const send = async () => {
    setBusy(true)
    try {
      const r = kind === 'send' ? await emailSend(id, to, message, link) : await emailRequest(id, to, message)
      setSent(s => [...s, ...r]); setTo(''); setMessage(''); toast(`Emailed to ${r.join(', ')}`); onSent?.(r)
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <form className="email-box" onSubmit={e => { e.preventDefault(); if (to.trim()) send() }}>
      <Field label="Email it to" help={encrypted ? 'The link (with its key) passes through this server once to write the email; it is not stored.' : undefined}>
        <div className="row">
          <input type="text" inputMode="email" list={`rcpt-${id}`} value={to} onChange={e => setTo(e.target.value)} placeholder="name@example.com, another@example.com" />
          <button className="btn primary" disabled={busy || !to.trim()}><Icon name="send" size={14} /> {busy ? 'Sending…' : 'Send'}</button>
        </div>
      </Field>
      <RecipientList id={`rcpt-${id}`} />
      <textarea value={message} onChange={e => setMessage(e.target.value)} rows={2} placeholder="A short message (optional)" />
      {sent.length > 0 && <p className="small ok"><Icon name="check" size={12} style={{ display: 'inline', verticalAlign: '-2px' }} /> Sent to {sent.join(', ')}</p>}
    </form>
  )
}
