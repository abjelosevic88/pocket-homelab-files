import { AskButton } from '../components/AskButton'
import React, { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, bytes } from '../lib/format'
import { EXPIRY_OPTIONS, DOWNLOAD_OPTIONS } from '../lib/sends'
import type { APIToken, Access, Mount, Person, Perm, Settings as S, SystemInfo, User } from '../lib/types'
import { Icon, type IconName } from '../components/icons'
import { Avatar } from '../components/people'
import { Clouds } from './Clouds'
import { downloadsLabel } from '../components/SendOptions'
import { LAST_KEY } from '../components/Layout'
import { Select } from '../components/Select'
import { Confirm, CopyField, Field, Loading, Modal, Segmented, SettingRow, Tabs, Toggle, useAsync, useIsMobile, useToast } from '../components/ui'

type TabId = 'profile' | 'general' | 'links' | 'alerts' | 'email' | 'webdav' | 'clouds' | 'mounts' | 'people' | 'about'

const TABS: { id: TabId; label: string; icon: IconName; sub: string; admin?: boolean }[] = [
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email, WebDAV user name and password.' },
  { id: 'general', label: 'Browsing', icon: 'sliders', sub: 'Hidden files, how long the trash keeps things, and the app title.' },
  { id: 'links', label: 'Links', icon: 'link', sub: 'What a new link starts with: expiry, download limit and encryption.' },
  { id: 'alerts', label: 'Notifications', icon: 'bell', sub: 'A push when a link of yours is downloaded or files arrive through an upload link.' },
  { id: 'email', label: 'Email', icon: 'mail', sub: 'Your own mail account for emailing links, and the wording of those emails.' },
  { id: 'webdav', label: 'WebDAV & apps', icon: 'key', sub: 'Mount your drive in Finder, Explorer or Files on iPhone; app passwords for it.' },
  { id: 'clouds', label: 'Connected drives', icon: 'cloud', sub: 'Copy Google Drive, Dropbox, OneDrive, iCloud Drive or any rclone remote into Pocket Files, on a schedule or once.' },
  { id: 'mounts', label: 'Mounts', icon: 'disk', sub: 'Server folders shown in the sidebar for the people you pick.', admin: true },
  { id: 'people', label: 'People', icon: 'users', sub: 'Everyone who has an account on this box.', admin: true },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, storage, the share listener and Pocket Core.' },
]

export default function Settings() {
  const { user } = useApp()
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const admin = user?.role === 'admin'
  const tabs = TABS.filter(t => !t.admin || admin)
  // old links: /settings?tab=email
  const legacy = new URLSearchParams(window.location.search).get('tab') || ''
  const want = raw || legacy
  const tab: TabId = tabs.find(t => t.id === want)?.id || 'profile'
  const current = tabs.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })
  const back = () => navigate(sessionStorage.getItem(LAST_KEY) || '/f/home')

  useEffect(() => { if (legacy && !raw) navigate(`/settings/${tab}`, { replace: true }) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => { if (mobile) document.querySelector('.set-tabs-mobile .tabs button.active')?.scrollIntoView({ inline: 'center', block: 'nearest' }) }, [tab, mobile])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop, .menu, .select-pop')) back() }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back to your files" title="Back to your files (esc)" onClick={back}><Icon name="chevronLeft" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
        <span style={{ flex: 1 }} />
        <AskButton />
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={tabs.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {tabs.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner" key={tab}>
            <div className="set-title">
              <h2>{current.label}</h2>
              <p>{current.sub}</p>
            </div>
            {tab === 'profile' && <Profile />}
            {tab === 'general' && <Browsing />}
            {tab === 'links' && <LinkDefaults />}
            {tab === 'alerts' && <Alerts />}
            {tab === 'email' && <EmailSettings />}
            {tab === 'webdav' && <WebDAV />}
            {tab === 'clouds' && <Clouds />}
            {tab === 'mounts' && admin && <Mounts />}
            {tab === 'people' && admin && <People />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
    </div>
  )
}

/** A settings card: title, explanation, actions on the right — Pocket Calendar's section. */
export function Section({ title, sub, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [form, setForm] = useState({ name: user?.name || '', email: user?.email || '', username: user?.username || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e?: React.FormEvent) => {
    e?.preventDefault()
    setBusy(true)
    try { const u = await api.put<User>(`${V1}/auth/me`, form); setUser(u); setForm(f => ({ ...f, current_password: '', new_password: '' })); toast('Saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <form className="stack loose" onSubmit={save}>
      <Section title="You">
        <div className="set-profile">
          {user && <Avatar user={user} size={56} />}
          <div className="stack grow">
            <div className="two-col">
              <Field label="Name"><input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} autoComplete="name" /></Field>
              <Field label="Email"><input type="email" value={form.email} onChange={e => setForm({ ...form, email: e.target.value })} autoComplete="email" /></Field>
            </div>
            <Field label="WebDAV user name" help="Shown in the Nextcloud-style WebDAV address; any name works with an app password."><input value={form.username} onChange={e => setForm({ ...form, username: e.target.value })} autoComplete="off" /></Field>
          </div>
        </div>
      </Section>
      {status?.auth_mode === 'local' && <Section title="Password" sub="At least 8 characters. Leave empty to keep the one you have.">
        <div className="two-col">
          <Field label="Current password"><input type="password" value={form.current_password} onChange={e => setForm({ ...form, current_password: e.target.value })} autoComplete="current-password" /></Field>
          <Field label="New password"><input type="password" value={form.new_password} onChange={e => setForm({ ...form, new_password: e.target.value })} autoComplete="new-password" /></Field>
        </div>
      </Section>}
      <div className="row"><button type="submit" className="btn primary" disabled={busy}>Save changes</button></div>
    </form>
  )
}

function Browsing() {
  const { user, settings, saveSettings } = useApp()
  const { toast } = useToast()
  const [title, setTitle] = useState(settings?.title || '')
  if (!settings) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Files">
        <SettingRow title="Show hidden files" help="Names starting with a dot."><Toggle checked={!!settings.show_hidden} onChange={v => saveSettings({ show_hidden: v })} label="Show hidden files" /></SettingRow>
        <SettingRow title="Trash" help="Things deleted in mounts skip the trash.">
          <Select value={settings.trash_days ?? 30} options={[7, 14, 30, 60, 90, 0].map(n => ({ value: n, label: n ? `Empty after ${n} days` : 'Keep until I empty it' }))} onChange={v => saveSettings({ trash_days: v })} label="Trash" />
        </SettingRow>
      </Section>
      {user?.role === 'admin' && <Section title="This box" sub="Applies to everyone on this box.">
        <SettingRow title="App title" help="Shown in the sidebar and the browser tab.">
          <form className="row" onSubmit={e => { e.preventDefault(); saveSettings({ title }).then(() => toast('Saved')).catch(er => toast((er as Error).message, 'err')) }}>
            <input value={title} onChange={e => setTitle(e.target.value)} style={{ width: 200 }} aria-label="App title" />
            <button type="submit" className="btn">Save</button>
          </form>
        </SettingRow>
      </Section>}
    </div>
  )
}

function LinkDefaults() {
  const { settings, saveSettings } = useApp()
  if (!settings) return <Loading />
  return (
    <div className="stack loose">
      <Section title="New links start with" sub="You can change each link when you make it.">
        <SettingRow title="Expiry"><Select value={settings.send_expiry} options={EXPIRY_OPTIONS} onChange={v => saveSettings({ send_expiry: v })} label="Expiry" /></SettingRow>
        <SettingRow title="Download limit"><Select value={settings.send_downloads} options={DOWNLOAD_OPTIONS.map(d => ({ value: d, label: downloadsLabel(d) }))} onChange={v => saveSettings({ send_downloads: v })} label="Download limit" /></SettingRow>
        <SettingRow title="End-to-end encrypted" help="Files are encrypted in the browser and the key travels only in the link."><Toggle checked={settings.send_encrypted} onChange={v => saveSettings({ send_encrypted: v })} label="End-to-end encrypted" /></SettingRow>
      </Section>
    </div>
  )
}

function Alerts() {
  const { settings } = useApp()
  return settings ? <AlertsForm /> : <Loading />
}

function AlertsForm() {
  const { settings, saveSettings } = useApp()
  const { toast } = useToast()
  const [n, setN] = useState({ notify_kind: settings?.notify_kind || 'apprise', notify_url: settings?.notify_url || '', notify_token: settings?.notify_token || '' })
  if (!settings) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Tell me when…">
        <SettingRow title="…a link of mine is downloaded"><Toggle checked={settings.notify_sends} onChange={v => saveSettings({ notify_sends: v })} label="A link of mine is downloaded" /></SettingRow>
        <SettingRow title="…files arrive through an upload link"><Toggle checked={settings.notify_uploads} onChange={v => saveSettings({ notify_uploads: v })} label="Files arrive through an upload link" /></SettingRow>
      </Section>
      <Section title="Send to" sub="Empty address: the box default, which reaches the owner only.">
        <div className="stack">
          <Field label="Service" group><Segmented size="sm" value={n.notify_kind} onChange={v => setN({ ...n, notify_kind: v })} options={[{ value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'webhook', label: 'Webhook' }]} /></Field>
          <Field label="Address" help={n.notify_kind === 'apprise' ? 'An Apprise route, e.g. http://apprise:8000/notify/files' : n.notify_kind === 'ntfy' ? 'A topic URL, e.g. https://ntfy.sh/my-files' : 'Gets {title, body, url} as JSON'}><input value={n.notify_url} onChange={e => setN({ ...n, notify_url: e.target.value })} placeholder="Empty: the box default (owner only)" /></Field>
          <Field label="Token (optional)"><input type="password" value={n.notify_token} onChange={e => setN({ ...n, notify_token: e.target.value })} autoComplete="off" /></Field>
          <div className="form-actions">
            <button type="button" className="btn" onClick={() => api.post(`${V1}/notify/test`).then(() => toast('Sent')).catch(e => toast((e as Error).message, 'err'))}><Icon name="bell" size={14} />Send a test</button>
            <button type="button" className="btn primary" onClick={() => saveSettings(n as Partial<S>).then(() => toast('Saved')).catch(e => toast((e as Error).message, 'err'))}>Save</button>
          </div>
        </div>
      </Section>
    </div>
  )
}

function EmailSettings() {
  const { settings } = useApp()
  return settings ? <EmailSettingsForm /> : <Loading />
}

function EmailSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const pick = (st: S | null) => ({ smtp_host: st?.smtp_host || '', smtp_port: st?.smtp_port || 587, smtp_user: st?.smtp_user || '', smtp_password: '', smtp_from: st?.smtp_from || '', smtp_from_name: st?.smtp_from_name || '', smtp_tls: st?.smtp_tls || 'starttls' })
  const [f, setF] = useState(() => pick(settings))
  const [to, setTo] = useState(user?.email || '')
  const [busy, setBusy] = useState(false)
  if (!settings) return <Loading />
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: k === 'smtp_port' ? Number(e.target.value) : e.target.value })
  const save = async () => {
    const next = await api.put<S>(`${V1}/settings`, f)
    setSettings(next); setF(pick(next))
    return next
  }
  const run = async (test: boolean) => {
    setBusy(true)
    try {
      await save()
      if (test) { await api.post(`${V1}/email/test`, { to: [to] }); toast(`Test email sent to ${to}`) } else toast('Saved')
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <div className="stack loose">
      <Section title="Your mail account" sub="Links and upload links are emailed from this account, like invoices from Pocket Invoicing. For Gmail use smtp.gmail.com, port 587, STARTTLS and an app password.">
        <div className="stack">
          <div className="two-col">
            <Field label="Server"><input value={f.smtp_host} onChange={set('smtp_host')} placeholder="smtp.example.com" /></Field>
            <Field label="Port"><input type="number" value={f.smtp_port} onChange={set('smtp_port')} /></Field>
          </div>
          <Field label="Encryption" group><Segmented size="sm" value={f.smtp_tls} onChange={v => setF({ ...f, smtp_tls: v, smtp_port: v === 'tls' ? 465 : v === 'none' ? 25 : 587 })} options={[{ value: 'starttls' as const, label: 'STARTTLS (587)' }, { value: 'tls' as const, label: 'TLS/SSL (465)' }, { value: 'none' as const, label: 'None (25)' }]} /></Field>
          <div className="two-col">
            <Field label="Username"><input value={f.smtp_user} onChange={set('smtp_user')} autoComplete="off" /></Field>
            <Field label="Password" help={settings.smtp_password_set ? 'A password is stored. Leave blank to keep it.' : undefined}><input type="password" value={f.smtp_password} onChange={set('smtp_password')} autoComplete="new-password" placeholder={settings.smtp_password_set ? '••••••••' : ''} /></Field>
          </div>
          <div className="two-col">
            <Field label="From address"><input value={f.smtp_from} onChange={set('smtp_from')} placeholder="you@example.com" /></Field>
            <Field label="From name"><input value={f.smtp_from_name} onChange={set('smtp_from_name')} placeholder={user?.name} /></Field>
          </div>
          <div className="form-actions"><button type="button" className="btn primary" disabled={busy} onClick={() => run(false)}>Save</button></div>
        </div>
      </Section>
      <Section title="Check it" sub="The link's password is never put in an email. Encrypted links pass through this server once, with their key, only to write the email.">
        <div className="row wrap">
          <input className="grow" value={to} onChange={e => setTo(e.target.value)} aria-label="Send a test to" placeholder="Send a test to…" style={{ minWidth: 200 }} />
          <button type="button" className="btn" disabled={busy || !f.smtp_host || !to} onClick={() => run(true)}><Icon name="send" size={14} /> Save & send test</button>
        </div>
      </Section>
      <MailTemplates />
    </div>
  )
}

interface TplDefaults { subject: string; body: string; request_subject: string; request_body: string; placeholders: string[] }
const PLACEHOLDER_HELP: Record<string, string> = {
  '{sender}': 'your name', '{title}': 'what you share', '{link}': 'the link (a button in the email)', '{message}': 'your note, if any',
  '{expires}': '“until Fri 24 Oct, 15:00”', '{limit}': 'downloads left, if limited', '{password_note}': 'says a password follows separately', '{encrypted_note}': 'keep-the-link-whole note',
}

/** The wording of the emails, with a live preview. */
function MailTemplates() {
  const { settings, setSettings } = useApp()
  const { toast } = useToast()
  const [defs, setDefs] = useState<TplDefaults | null>(null)
  const [kind, setKind] = useState<'send' | 'request'>('send')
  const [t, setT] = useState({ mail_subject: settings?.mail_subject || '', mail_body: settings?.mail_body || '', mail_request_subject: settings?.mail_request_subject || '', mail_request_body: settings?.mail_request_body || '' })
  const [preview, setPreview] = useState<{ subject: string; html: string } | null>(null)
  useEffect(() => { api.get<TplDefaults>(`${V1}/email/templates`).then(setDefs).catch(() => {}) }, [])
  const sk = kind === 'send' ? 'mail_subject' : 'mail_request_subject'
  const bk = kind === 'send' ? 'mail_body' : 'mail_request_body'
  const subject = t[sk] || (kind === 'send' ? defs?.subject : defs?.request_subject) || ''
  const body = t[bk] || (kind === 'send' ? defs?.body : defs?.request_body) || ''
  useEffect(() => {
    if (!defs) return
    const id = setTimeout(() => { api.post<{ subject: string; html: string }>(`${V1}/email/preview`, { kind, subject, body }).then(setPreview).catch(() => {}) }, 300)
    return () => clearTimeout(id)
  }, [kind, subject, body, defs])
  if (!defs) return <Loading />
  const isDefault = (k: typeof sk | typeof bk) => !t[k] || t[k] === (k === 'mail_subject' ? defs.subject : k === 'mail_body' ? defs.body : k === 'mail_request_subject' ? defs.request_subject : defs.request_body)
  const save = async () => {
    // a template equal to the built-in one is stored as empty, so it follows future improvements
    const clean = Object.fromEntries(Object.entries(t).map(([k, v]) => [k, isDefault(k as typeof sk) ? '' : v]))
    try { setSettings(await api.put<S>(`${V1}/settings`, clean)); toast('Saved') } catch (e) { toast((e as Error).message, 'err') }
  }
  const insert = (p: string) => setT({ ...t, [bk]: body + (body.endsWith('\n') ? '' : ' ') + p })
  return (
    <Section title="Email text" sub="Click a placeholder to add it. A line whose placeholders are empty (no message, no password) is left out."
      actions={<Segmented size="sm" value={kind} onChange={setKind} options={[{ value: 'send' as const, label: 'Sharing a link' }, { value: 'request' as const, label: 'Asking for files' }]} />}>
      <div className="tpl-grid">
        <div className="stack">
          <Field label="Subject"><input value={subject} onChange={e => setT({ ...t, [sk]: e.target.value })} /></Field>
          <Field label="Text"><textarea className="tpl-body" value={body} onChange={e => setT({ ...t, [bk]: e.target.value })} rows={12} /></Field>
          <div className="chips">{defs.placeholders.map(p => <button key={p} type="button" className="chip mono" title={PLACEHOLDER_HELP[p]} onClick={() => insert(p)}>{p}</button>)}</div>
          <div className="form-actions">
            <button type="button" className="btn" disabled={isDefault(sk) && isDefault(bk)} onClick={() => setT({ ...t, [sk]: '', [bk]: '' })}>Reset to default</button>
            <button type="button" className="btn primary" onClick={save}>Save</button>
          </div>
        </div>
        <div className="tpl-preview">
          <div className="label small muted">Preview</div>
          <div className="tpl-subject"><b>{preview?.subject}</b></div>
          {preview && <iframe title="Email preview" sandbox="" srcDoc={preview.html} />}
        </div>
      </div>
    </Section>
  )
}

function WebDAV() {
  const { data: info } = useAsync(() => api.get<SystemInfo>(`${V1}/settings/system`), [])
  const tokens = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const { toast } = useToast()
  const create = async () => {
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'App password' }); setFresh(r.token); setName(''); tokens.reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <div className="stack loose">
      <Section title="Your drive over WebDAV" sub={<>Mount your files in Finder (<kbd>⌘</kbd> <kbd>K</kbd>), Windows Explorer, Files on iPhone, rclone or any WebDAV app. Your home is the root; <b>Shared with me</b> and <b>Mounts</b> appear as folders.</>}>
        {!info ? <Loading /> : <div className="stack">
          <Field label="Address" group><CopyField value={info.webdav.url} /></Field>
          <Field label="Nextcloud-style address" help="For apps that only speak Nextcloud." group><CopyField value={info.webdav.nextcloud_url} /></Field>
          <Field label="User name" group><CopyField value={info.webdav.username} mono={false} /></Field>
          <p className="small muted">Password: an app password from below. Other Pocket apps (Office, Documents, Review) connect through Pocket Core instead and need nothing from you.</p>
        </div>}
      </Section>
      <Section title="App passwords" sub="One per device or app, so you can revoke it alone.">
        <div className="stack">
          <form className="row" onSubmit={e => { e.preventDefault(); create() }}>
            <input className="grow" value={name} onChange={e => setName(e.target.value)} placeholder="Name, e.g. MacBook Finder" aria-label="Name of the app password" />
            <button type="submit" className="btn primary"><Icon name="plus" size={15} />Create</button>
          </form>
          {fresh && <div className="set-fresh"><div className="row small"><Icon name="check" size={15} />Copy it now — it is shown once.</div><CopyField value={fresh} /></div>}
          {tokens.data?.length ? <div className="set-people">{tokens.data.map(t => (
            <div key={t.id} className="set-person">
              <span className="set-token-ico"><Icon name="key" size={16} /></span>
              <div className="grow"><div className="set-person-name">{t.name}</div><div className="small muted"><span className="mono">{t.prefix}…</span> · {t.last_used_at ? 'used ' + ago(t.last_used_at) : 'never used'}</div></div>
              <button type="button" className="btn ghost icon-only sm" onClick={() => setDel(t)} title="Revoke" aria-label={`Revoke ${t.name}`}><Icon name="trash" size={15} /></button>
            </div>
          ))}</div> : <p className="small muted">No app passwords yet.</p>}
        </div>
      </Section>
      {del && <Confirm title="Revoke this app password?" confirmLabel="Revoke" message={<>Apps using <b>{del.name}</b> lose access.</>} onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); tokens.reload() }} />}
    </div>
  )
}

interface MountView extends Mount { missing?: boolean; allowed?: boolean; error?: string }

function Mounts() {
  const { bump } = useApp()
  const { data, reload } = useAsync(() => api.get<{ mounts: MountView[]; roots: string[] }>(`${V1}/mounts`), [])
  const [edit, setEdit] = useState<Partial<Mount> | null>(null)
  const [del, setDel] = useState<Mount | null>(null)
  if (!data) return <Loading />
  return (
    <div className="stack loose">
      {!data.roots.length && <div className="callout"><Icon name="info" size={15} /><span><b>Mounting is off.</b> Set <code>MOUNT_ROOTS</code> (for example <code>/mnt,/srv/media</code>) and mount those folders into the container; then admins can show any folder below them here.</span></div>}
      <Section title="Mounted folders" sub={data.roots.length ? <>Allowed below: {data.roots.map(r => <code key={r} className="set-code">{r}</code>)}</> : undefined}
        actions={data.roots.length > 0 ? <button type="button" className="btn primary sm" onClick={() => setEdit({ name: '', path: '', read_only: false, everyone_perm: '', access: [] })}><Icon name="plus" size={15} /> Mount a folder</button> : undefined}>
        <div className="set-people">
          {data.mounts.map(m => (
            <div key={m.id} className={`set-person ${m.missing || !m.allowed ? 'off' : ''}`}>
              <span className="set-token-ico"><Icon name="disk" size={16} /></span>
              <div className="grow">
                <div className="set-person-name">{m.name}{m.read_only && <span className="badge"><Icon name="lock" size={11} /> read only</span>}{m.missing && <span className="badge danger">missing</span>}{!m.allowed && <span className="badge danger">outside MOUNT_ROOTS</span>}</div>
                <div className="small muted mono ellipsis">{m.path}</div>
                <div className="small muted">{[m.everyone_perm ? `everyone (${m.everyone_perm})` : '', ...(m.access || []).map(a => `${a.name || a.email} (${a.perm})`)].filter(Boolean).join(', ') || 'nobody yet'}</div>
              </div>
              <button type="button" className="btn sm" onClick={() => setEdit(m)}><Icon name="edit" size={13} /> Edit</button>
              <button type="button" className="btn ghost icon-only sm" title="Unmount" aria-label={`Unmount ${m.name}`} onClick={() => setDel(m)}><Icon name="x" size={15} /></button>
            </div>
          ))}
          {!data.mounts.length && <p className="muted small">No mounts yet.</p>}
        </div>
      </Section>
      {edit && <MountDialog mount={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reload(); bump() }} />}
      {del && <Confirm title={`Unmount “${del.name}”?`} confirmLabel="Unmount" message="It disappears from the app for everyone. The folder on the server is not touched." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/mounts/${del.id}`); setDel(null); reload(); bump() }} />}
    </div>
  )
}

function MountDialog({ mount, onClose, onSaved }: { mount: Partial<Mount>; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [m, setM] = useState<Partial<Mount>>(mount)
  const [people, setPeople] = useState<Person[]>([])
  const [browse, setBrowse] = useState(false)
  useEffect(() => { api.get<Person[]>(`${V1}/people`).then(setPeople).catch(() => {}) }, [])
  const access = m.access || []
  const same = (a: Access, p: Person) => (a.email || '').toLowerCase() === p.email.toLowerCase() || (!!p.id && a.user_id === p.id)
  const setPerm = (p: Person, perm: '' | Perm) => setM(x => ({ ...x, access: perm ? [...(x.access || []).filter(a => !same(a, p)), { user_id: p.id, email: p.email, perm } as Access] : (x.access || []).filter(a => !same(a, p)) }))
  const save = async () => {
    try { if (m.id) await api.put(`${V1}/mounts/${m.id}`, m); else await api.post(`${V1}/mounts`, m); toast('Saved'); onSaved() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Modal title={m.id ? `Mount “${m.name}”` : 'Mount a server folder'} onClose={onClose} footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" onClick={save}>Save</button></>}>
      <div className="stack">
        <Field label="Name in the sidebar"><input value={m.name || ''} onChange={e => setM({ ...m, name: e.target.value })} placeholder="e.g. Media" /></Field>
        <Field label="Folder on the server" group><div className="row"><input className="mono" value={m.path || ''} onChange={e => setM({ ...m, path: e.target.value })} placeholder="/mnt/media" aria-label="Folder on the server" /><button type="button" className="btn" onClick={() => setBrowse(true)}>Browse…</button></div></Field>
        <SettingRow title="Read only" help="Nobody can change, add or delete files here, whatever their access says."><Toggle checked={!!m.read_only} onChange={v => setM({ ...m, read_only: v })} label="Read only" /></SettingRow>
        <Field label="Everyone on this box" group><Segmented size="sm" value={m.everyone_perm || ''} onChange={v => setM({ ...m, everyone_perm: v })} options={[{ value: '' as const, label: 'No' }, { value: 'view' as const, label: 'Can view' }, { value: 'edit' as const, label: 'Can edit' }]} /></Field>
        <div className="member-list">
          {people.map(p => {
            const a = access.find(x => same(x, p))
            return <div key={p.email} className="member-row">
              <Avatar user={p} size={28} />
              <span className="grow"><b>{p.name || p.email}</b><br /><small className="muted">{p.email}</small></span>
              <Segmented size="sm" value={a?.perm || ''} onChange={v => setPerm(p, v)} options={[{ value: '' as const, label: 'None' }, { value: 'view' as const, label: 'View' }, { value: 'edit' as const, label: 'Edit' }]} />
            </div>
          })}
        </div>
        <p className="small muted">Mounted files are deleted for good (no trash) and are not covered by this app's backups.</p>
      </div>
      {browse && <ServerBrowser start={m.path || ''} onPick={p => { setM(x => ({ ...x, path: p, name: x.name || p.split('/').pop() || '' })); setBrowse(false) }} onClose={() => setBrowse(false)} />}
    </Modal>
  )
}

function ServerBrowser({ start, onPick, onClose }: { start: string; onPick: (p: string) => void; onClose: () => void }) {
  const [path, setPath] = useState(start)
  const [data, setData] = useState<{ path: string; parent?: string; dirs: { name: string; path: string }[] } | null>(null)
  const [err, setErr] = useState('')
  useEffect(() => { setErr(''); api.get<typeof data>(`${V1}/mounts/browse?path=${encodeURIComponent(path)}`).then(setData).catch(e => { setErr((e as Error).message); if (path) setPath('') }) }, [path])
  return (
    <Modal title="Pick a server folder" onClose={onClose} footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" disabled={!data?.path} onClick={() => onPick(data!.path)}>Use this folder</button></>}>
      <div className="small mono muted mb-s">{data?.path || 'Allowed roots'}</div>
      <div className="picker-list">
        {err && <div className="callout danger">{err}</div>}
        {data?.path && <button type="button" className="pick-row" onClick={() => setPath(data.parent || '')}><Icon name="arrowUp" size={15} /> ..</button>}
        {data?.dirs.map(d => <button type="button" key={d.path} className="pick-row" onClick={() => setPath(d.path)}><Icon name="folder" size={15} /> {d.name}</button>)}
        {data && !data.dirs.length && <p className="small muted pad center">No sub folders</p>}
      </div>
    </Modal>
  )
}

function People() {
  const { user: me } = useApp()
  const { data, reload } = useAsync(() => api.get<User[]>(`${V1}/users`), [])
  const [edit, setEdit] = useState<Partial<User> & { password?: string } | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const { toast } = useToast()
  const save = async () => {
    if (!edit) return
    try { if (edit.id) await api.put(`${V1}/users/${edit.id}`, edit); else await api.post(`${V1}/users`, edit); setEdit(null); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <div className="stack loose">
      <Section title="Accounts" sub="Everyone has a private home folder — admins included cannot open other people's files. With Pocket Core, people get an account the first time they sign in."
        actions={<button type="button" className="btn primary sm" onClick={() => setEdit({ email: '', name: '', role: 'member', password: '' })}><Icon name="plus" size={15} /> Add person</button>}>
        <div className="set-people">
          {!data && <Loading />}
          {data?.map(u => (
            <div key={u.id} className="set-person">
              <Avatar user={u} size={34} />
              <div className="grow"><div className="set-person-name">{u.name || u.email}{u.role === 'admin' && <span className="badge">admin</span>}{u.id === me?.id && <span className="badge">you</span>}</div><div className="small muted">{u.email} · since {new Date(u.created_at).toLocaleDateString()}</div></div>
              <button type="button" className="btn sm" onClick={() => setEdit({ ...u, password: '' })}><Icon name="edit" size={13} /> Edit</button>
              {u.id !== me?.id && <button type="button" className="btn ghost icon-only sm" title="Delete" aria-label={`Delete ${u.name || u.email}`} onClick={() => setDel(u)}><Icon name="trash" size={15} /></button>}
            </div>
          ))}
        </div>
      </Section>
      {edit && <Modal title={edit.id ? 'Edit person' : 'Add a person'} onClose={() => setEdit(null)} footer={<><button type="button" className="btn" onClick={() => setEdit(null)}>Cancel</button><button type="button" className="btn primary" onClick={save}>Save</button></>}>
        <div className="stack">
          <Field label="Name"><input value={edit.name || ''} onChange={e => setEdit({ ...edit, name: e.target.value })} /></Field>
          <Field label="Email"><input type="email" value={edit.email || ''} onChange={e => setEdit({ ...edit, email: e.target.value })} /></Field>
          <Field label={edit.id ? 'New password (optional)' : 'Password'} help="At least 8 characters"><input type="password" value={edit.password || ''} onChange={e => setEdit({ ...edit, password: e.target.value })} autoComplete="new-password" /></Field>
          <Field label="Role" group><Segmented value={edit.role || 'member'} onChange={v => setEdit({ ...edit, role: v })} options={[{ value: 'member' as const, label: 'Member' }, { value: 'admin' as const, label: 'Admin (mounts, people)' }]} /></Field>
        </div>
      </Modal>}
      {del && <Confirm title={`Delete ${del.name || del.email}?`} confirmLabel="Delete" message="Their account, shares and links go. Their files are moved aside on the server (data/deleted-users), not deleted." onCancel={() => setDel(null)} onConfirm={async () => { try { await api.del(`${V1}/users/${del.id}`); setDel(null); reload() } catch (e) { toast((e as Error).message, 'err') } }} />}
    </div>
  )
}

function About() {
  const { data: info } = useAsync(() => api.get<SystemInfo>(`${V1}/settings/system`), [])
  if (!info) return <Loading />
  const used = info.disk.total ? info.disk.total - info.disk.free : 0
  return (
    <div className="stack loose">
      <div className="set-about-hero">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div><h3>Pocket Files</h3><p className="muted">Version {info.version} · <a href="https://github.com/abjelosevic88/pocket-homelab-files/releases" target="_blank" rel="noreferrer">releases</a></p></div>
      </div>
      <div className="set-stats">
        <div className="set-stat"><Icon name="home" size={16} /><span className="set-stat-n">{bytes(info.home.size)}</span><span className="set-stat-l">Your files</span></div>
        <div className="set-stat"><Icon name="file" size={16} /><span className="set-stat-n">{info.home.files.toLocaleString()}</span><span className="set-stat-l">Files in your home</span></div>
        <div className="set-stat"><Icon name="disk" size={16} /><span className="set-stat-n">{bytes(info.disk.free)}</span><span className="set-stat-l">Free on the server</span></div>
        <div className="set-stat"><Icon name="server" size={16} /><span className="set-stat-n">{info.disk.total ? Math.round((used / info.disk.total) * 100) + '%' : '—'}</span><span className="set-stat-l">Disk used of {bytes(info.disk.total)}</span></div>
      </div>
      <Section title="This box">
        <dl className="set-dl">
          <div><dt>Links point to</dt><dd className="mono small">{info.public_url || '—'}</dd></div>
          <div><dt>Public listener</dt><dd>{info.public_listener ? 'on (share pages only)' : 'off'}</dd></div>
          <div><dt>Largest send</dt><dd>{info.max_send_mb ? bytes(info.max_send_mb * 1048576) : 'no limit'}</dd></div>
          {info.data_dir && <div><dt>Data folder</dt><dd className="mono small">{info.data_dir}</dd></div>}
        </dl>
      </Section>
      <Section title="Pocket Core" sub="With Core, people sign in with one account and Pocket Office, Documents and Review can keep files here (scopes files:read and files:write, switchable per person in Core → Links).">
        <dl className="set-dl">
          <div><dt>Connected</dt><dd className="row"><span className={`set-status ${info.core.configured ? (info.core.registered ? 'ok' : 'warn') : ''}`} />{info.core.configured ? (info.core.registered ? 'yes' : 'not yet') : 'no'}</dd></div>
          {info.core.url && <div><dt>Address</dt><dd className="mono small">{info.core.url}</dd></div>}
          {info.core.error && <div><dt>Last error</dt><dd className="danger-text small">{info.core.error}</dd></div>}
        </dl>
      </Section>
    </div>
  )
}
