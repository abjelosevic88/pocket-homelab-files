import { useState } from 'react'
import { EmailSetupHint, RecipientList } from './EmailLink'
import { DOWNLOAD_OPTIONS, EXPIRY_OPTIONS, type SendOptions } from '../lib/sends'
import { useApp } from '../lib/app-context'
import { Field, Select, Switch } from './ui'
import { Icon } from './icons'

export function defaultOptions(s: ReturnType<typeof useApp>['settings']): SendOptions {
  return { expires: s?.send_expiry || '7d', max_downloads: s?.send_downloads ?? 0, password: '', encrypted: s?.send_encrypted ?? true, notify: true, label: '' }
}

export const downloadsLabel = (n: number) => n === 0 ? 'No limit' : n === 1 ? '1 download' : `${n} downloads`

/** Expiry, download limit, password and encryption — the knobs of a link. */
export function SendOptionsForm({ value, onChange, allowEncrypt = true, encryptHelp, showLabel = true, hideEmail = false }: { value: SendOptions; onChange: (v: SendOptions) => void; allowEncrypt?: boolean; encryptHelp?: string; showLabel?: boolean; hideEmail?: boolean }) {
  const [showPw, setShowPw] = useState(false)
  const { settings } = useApp()
  const set = <K extends keyof SendOptions>(k: K, v: SendOptions[K]) => onChange({ ...value, [k]: v })
  return (
    <div className="send-options">
      <div className="two-col tight">
        <Field label="Expires after" group><Select value={value.expires} options={EXPIRY_OPTIONS} onChange={v => set('expires', v)} className="btn" /></Field>
        <Field label="Download limit" group><Select value={value.max_downloads} options={DOWNLOAD_OPTIONS.map(n => ({ value: n, label: downloadsLabel(n) }))} onChange={v => set('max_downloads', v)} className="btn" /></Field>
      </div>
      {value.max_downloads === 1 && <p className="small muted mt-s"><Icon name="flame" size={12} style={{ display: 'inline', verticalAlign: '-2px', color: '#ea580c' }} /> Burns after the first download.</p>}
      <Field label="Password (optional)" help={value.encrypted && allowEncrypt ? 'Mixed into the key: without it the link opens nothing, and this server never sees it.' : 'Checked by the server before anything is shown.'}>
        <div className="pw-field">
          <input type={showPw ? 'text' : 'password'} value={value.password} onChange={e => set('password', e.target.value)} placeholder="No password" autoComplete="new-password" />
          <button type="button" className="icon-btn" onClick={() => setShowPw(s => !s)} title={showPw ? 'Hide' : 'Show'}><Icon name={showPw ? 'eyeOff' : 'eye'} size={15} /></button>
        </div>
      </Field>
      {allowEncrypt && <Switch checked={value.encrypted} onChange={v => set('encrypted', v)} label={<><Icon name="shield" size={14} style={{ display: 'inline', verticalAlign: '-2px', color: 'var(--accent)' }} /> End-to-end encrypted</>} help={encryptHelp || 'Encrypted in this browser; the key travels only in the link (after #). The server stores ciphertext.'} />}
      <Switch checked={value.notify} onChange={v => set('notify', v)} label="Tell me when it is downloaded" />
      {showLabel && <Field label="Name for your list (optional)"><input value={value.label} onChange={e => set('label', e.target.value)} placeholder="e.g. Contract for Marko" /></Field>}
      {hideEmail ? null : settings?.smtp_host ? <>
        <Field label="Email the link to (optional)" help={value.email_to && value.encrypted && allowEncrypt ? 'The link with its key passes through this server once to write the email; it is not stored.' : 'Sent from your mail account as soon as the link is ready. The password is never put in the email.'}>
          <input type="text" inputMode="email" list="rcpt-new" value={value.email_to || ''} onChange={e => set('email_to', e.target.value)} placeholder="name@example.com, another@example.com" />
        </Field>
        <RecipientList id="rcpt-new" />
        {value.email_to?.trim() && <textarea value={value.email_message || ''} onChange={e => set('email_message', e.target.value)} rows={2} placeholder="A short message (optional)" />}
      </> : <EmailSetupHint />}
    </div>
  )
}
