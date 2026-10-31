// Settings → Connected drives → Sign-in apps (admins): the Google, Dropbox and Microsoft apps
// people sign in through, set up with a step-by-step guide and checked with the provider.
import React, { useEffect, useRef, useState } from 'react'
import { api, V1 } from '../lib/api'
import { Icon } from '../components/icons'
import { CopyField, Field, Modal, useAsync, useToast } from '../components/ui'
import { Section } from './Settings'

export interface CloudApp { kind: 'gdrive' | 'dropbox' | 'onedrive'; name: string; configured: boolean; source?: 'settings' | 'env'; client_id?: string; secret_set: boolean; tenant?: string; redirect: string }

const LABELS: Record<CloudApp['kind'], { id: string; secret: string; idHint: string }> = {
  gdrive: { id: 'Client ID', secret: 'Client secret', idHint: '…apps.googleusercontent.com' },
  dropbox: { id: 'App key', secret: 'App secret', idHint: '15 letters and digits' },
  onedrive: { id: 'Application (client) ID', secret: 'Client secret value', idHint: '00000000-0000-0000-0000-000000000000' },
}

const ext = (href: string, text: React.ReactNode) => <a href={href} target="_blank" rel="noreferrer" className="ext-link">{text}<Icon name="external" size={11} /></a>

/** The steps for one provider; `redirect` is the address to paste, `domain` its registrable domain. */
function guide(app: CloudApp): { steps: React.ReactNode[]; note?: React.ReactNode } {
  const domain = new URL(app.redirect).hostname.split('.').slice(-2).join('.')
  const redirect = <CopyField value={app.redirect} label="Copy" />
  switch (app.kind) {
    case 'gdrive': return {
      steps: [
        <>Open the {ext('https://console.cloud.google.com/projectselector2/home/dashboard', 'Google Cloud console')} and pick a project, or create one (for example “Pocket Files”). It is free.</>,
        <>Turn on the {ext('https://console.cloud.google.com/apis/library/drive.googleapis.com', 'Google Drive API')}: press <b>Enable</b>.</>,
        <>Open {ext('https://console.cloud.google.com/auth/branding', 'Google Auth Platform → Branding')}: give the app a name and your email. Under <b>Authorized domains</b> add <code>{domain}</code>.</>,
        <>Open {ext('https://console.cloud.google.com/auth/audience', 'Audience')}: choose <b>External</b>, then <b>Publish app</b> so it says <b>In production</b>. Left in “Testing”, every sign-in stops working after 7 days. “Needs verification” can be ignored for your own household.</>,
        <>Open {ext('https://console.cloud.google.com/auth/clients/create', 'Clients → Create client')}: type <b>Web application</b>. Under <b>Authorized redirect URIs</b> add exactly:{redirect}Then <b>Create</b>.</>,
        <>Copy the <b>Client ID</b> and <b>Client secret</b> it shows into the fields below.</>,
      ],
      note: <>The first time someone connects, Google says <i>“Google hasn’t verified this app”</i>: tap <b>Advanced → Go to … (unsafe)</b>. That is expected for an app only your household uses. One-way drives ask for read-only access; two-way drives ask to see and change Drive files.</>,
    }
    case 'dropbox': return {
      steps: [
        <>Open the {ext('https://www.dropbox.com/developers/apps/create', 'Dropbox App Console → Create app')} and sign in with any Dropbox account.</>,
        <>Choose <b>Scoped access</b> and <b>Full Dropbox</b>, give it a name nobody else has used (for example “Pocket Files {domain}”), then <b>Create app</b>.</>,
        <>On the <b>Permissions</b> tab tick <code>account_info.read</code>, <code>files.metadata.read</code>, <code>files.content.read</code> and, for two-way sync, <code>files.content.write</code>. Then <b>Submit</b> at the bottom.</>,
        <>On the <b>Settings</b> tab, under <b>OAuth 2 → Redirect URIs</b>, add exactly:{redirect}and press <b>Add</b>.</>,
        <>Still on Settings, copy the <b>App key</b> and the <b>App secret</b> (press Show) into the fields below.</>,
      ],
      note: <>An app in “Development” status can be connected by up to 500 people, plenty for a household; there is nothing to submit to Dropbox.</>,
    }
    case 'onedrive': return {
      steps: [
        <>Open {ext('https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/CreateApplicationBlade/quickStartType~/null/isMSAApp~/false', 'Microsoft Entra → App registrations → New registration')} and sign in with any Microsoft account (if asked, accept the free directory it makes for you).</>,
        <>Name it (for example “Pocket Files”). Under <b>Supported account types</b> choose <b>Accounts in any organizational directory and personal Microsoft accounts</b>.</>,
        <>Under <b>Redirect URI</b> pick the platform <b>Web</b> and paste exactly:{redirect}Then <b>Register</b>.</>,
        <>On the app’s Overview, copy the <b>Application (client) ID</b> into the field below.</>,
        <>Open <b>Certificates &amp; secrets → New client secret</b>, choose 24 months, and copy its <b>Value</b> right away (not the “Secret ID”; the value is shown only once).</>,
        <>Optional: under <b>API permissions</b> add Microsoft Graph delegated <code>Files.ReadWrite</code>, <code>Files.ReadWrite.All</code>, <code>User.Read</code> and <code>offline_access</code> (one-way drives only use the read part). People are asked for these when they connect anyway.</>,
      ],
      note: <>Microsoft client secrets expire (at most 24 months). When it does, make a new one here; connected drives then ask to sign in again.</>,
    }
  }
}

export function CloudApps({ onChanged }: { onChanged: () => void }) {
  const { data, reload } = useAsync(() => api.get<CloudApp[]>(`${V1}/clouds/apps`), [])
  const [open, setOpen] = useState<CloudApp | null>(null)
  if (!data) return null
  return (
    <Section title="Sign-in apps" sub="Google Drive, Dropbox and OneDrive only let people sign in through an app registered by this box. Set each up once (free, about five minutes); then everyone here can connect their own account.">
      <div className="set-people">
        {data.map(a => (
          <div key={a.kind} className="set-person">
            <span className="set-token-ico"><Icon name="key" size={16} /></span>
            <div className="grow" style={{ minWidth: 0 }}>
              <div className="set-person-name">{a.name}
                {a.configured ? <span className="badge accent"><Icon name="check" size={11} /> ready</span> : <span className="badge warn">not set up</span>}
              </div>
              <div className="small muted ellipsis">{a.configured ? (a.source === 'env' ? 'From the server’s environment (.env)' : `Client ${a.client_id}`) : 'People cannot connect it yet.'}</div>
            </div>
            <button type="button" className={`btn sm ${a.configured ? '' : 'primary'}`} onClick={() => setOpen(a)}>{a.configured ? 'Change' : 'Set up'}</button>
          </div>
        ))}
      </div>
      {open && <CloudAppDialog app={open} onClose={() => setOpen(null)} onSaved={() => { setOpen(null); reload(); onChanged() }} />}
    </Section>
  )
}

export function CloudAppDialog({ app, onClose, onSaved }: { app: CloudApp; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [id, setId] = useState(app.source === 'settings' ? app.client_id || '' : '')
  const [secret, setSecret] = useState('')
  const [tenant, setTenant] = useState(app.source === 'settings' ? app.tenant || '' : '')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const g = guide(app)
  const L = LABELS[app.kind]
  // Start at step 1: the dialog focuses the first field, which would scroll the guide away.
  const steps = useRef<HTMLOListElement>(null)
  useEffect(() => {
    const t = setTimeout(() => { steps.current?.focus({ preventScroll: true }); steps.current?.closest('.modal-body, .modal')?.scrollTo?.({ top: 0 }) }, 60)
    return () => clearTimeout(t)
  }, [])
  const keepSecret = app.source === 'settings' && app.secret_set && id === app.client_id
  const save = async () => {
    setBusy(true); setErr('')
    try {
      await api.put(`${V1}/clouds/apps/${app.kind}`, { client_id: id, client_secret: secret, tenant })
      toast(`${app.name} is set up: people can connect it now`)
      onSaved()
    } catch (e) { setErr((e as Error).message); setBusy(false) }
  }
  const remove = async () => {
    try { await api.del(`${V1}/clouds/apps/${app.kind}`); toast('Removed'); onSaved() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Modal title={`Set up ${app.name} sign-in`} onClose={onClose} footer={<>
      {app.source === 'settings' && <button type="button" className="btn ghost danger-text" onClick={remove} style={{ marginRight: 'auto' }}>Remove</button>}
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" disabled={busy || !id.trim() || (!secret.trim() && !keepSecret)} onClick={save}>{busy ? 'Checking…' : 'Check & save'}</button>
    </>}>
      <div className="stack">
        <ol className="setup-steps" ref={steps} tabIndex={-1}>{g.steps.map((s, i) => <li key={i}>{s}</li>)}</ol>
        {g.note && <div className="callout"><Icon name="info" size={15} /><span>{g.note}</span></div>}
        {app.source === 'env' && <p className="small muted">It is set in the server’s environment now. Saving here takes over from that.</p>}
        <Field label={L.id}><input value={id} onChange={e => setId(e.target.value)} placeholder={L.idHint} autoComplete="off" spellCheck={false} className="mono" /></Field>
        <Field label={L.secret} help={keepSecret ? 'Leave empty to keep the saved one.' : undefined}><input type="password" value={secret} onChange={e => setSecret(e.target.value)} autoComplete="new-password" spellCheck={false} placeholder={keepSecret ? '••••••••' : ''} /></Field>
        {app.kind === 'onedrive' && <Field label="Directory (tenant), optional" help="Leave empty for personal and work accounts alike. Only for an app limited to one organisation."><input value={tenant} onChange={e => setTenant(e.target.value)} placeholder="common" className="mono" /></Field>}
        {err && <div className="callout danger"><Icon name="info" size={15} /><span>{err}</span></div>}
        <p className="small muted">“Check &amp; save” asks {app.name} whether it knows this app before saving. The secret is stored encrypted and never shown again.</p>
      </div>
    </Modal>
  )
}
