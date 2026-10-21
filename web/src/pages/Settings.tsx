import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, bytes } from '../lib/format'
import { EXPIRY_OPTIONS, DOWNLOAD_OPTIONS } from '../lib/sends'
import type { APIToken, Access, Mount, Person, Perm, Settings as S, SystemInfo, User } from '../lib/types'
import { Icon } from '../components/icons'
import { Avatar } from '../components/people'
import { downloadsLabel } from '../components/SendOptions'
import { Card, Confirm, CopyField, Field, Loading, Modal, PageHeader, Segmented, Select, Switch, Tabs, useAsync, useToast } from '../components/ui'

type Tab = 'profile' | 'links' | 'webdav' | 'mounts' | 'people' | 'about'

export default function Settings() {
  const { user } = useApp()
  const [params, setParams] = useSearchParams()
  const tab = (params.get('tab') as Tab) || 'profile'
  const admin = user?.role === 'admin'
  const tabs: { id: Tab; label: string }[] = [
    { id: 'profile', label: 'Profile' }, { id: 'links', label: 'Links & alerts' }, { id: 'webdav', label: 'WebDAV & apps' },
    ...(admin ? [{ id: 'mounts' as Tab, label: 'Mounts' }, { id: 'people' as Tab, label: 'People' }] : []), { id: 'about', label: 'About' },
  ]
  return (
    <div className="page settings">
      <PageHeader title="Settings" />
      <Tabs tabs={tabs} value={tab} onChange={t => setParams({ tab: t })} />
      <div className="mt">
        {tab === 'profile' && <Profile />}
        {tab === 'links' && <LinksAlerts />}
        {tab === 'webdav' && <WebDAV />}
        {tab === 'mounts' && admin && <Mounts />}
        {tab === 'people' && admin && <People />}
        {tab === 'about' && <About />}
      </div>
    </div>
  )
}

function Profile() {
  const { user, setUser, settings, saveSettings, status } = useApp()
  const { toast } = useToast()
  const [form, setForm] = useState({ name: user?.name || '', email: user?.email || '', username: user?.username || '', current_password: '', new_password: '' })
  const [title, setTitle] = useState(settings?.title || '')
  const save = async () => {
    try { const u = await api.put<User>(`${V1}/auth/me`, form); setUser(u); setForm(f => ({ ...f, current_password: '', new_password: '' })); toast('Saved') } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <div className="two-col">
      <Card title="You">
        <div className="grid" style={{ gap: 12 }}>
          <Field label="Name"><input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} /></Field>
          <Field label="Email"><input type="email" value={form.email} onChange={e => setForm({ ...form, email: e.target.value })} /></Field>
          <Field label="WebDAV user name" help="Shown in the Nextcloud-style WebDAV address; any name works with an app password."><input value={form.username} onChange={e => setForm({ ...form, username: e.target.value })} /></Field>
          {status?.auth_mode === 'local' && <>
            <Field label="Current password"><input type="password" value={form.current_password} onChange={e => setForm({ ...form, current_password: e.target.value })} autoComplete="current-password" /></Field>
            <Field label="New password" help="At least 8 characters. Leave empty to keep."><input type="password" value={form.new_password} onChange={e => setForm({ ...form, new_password: e.target.value })} autoComplete="new-password" /></Field>
          </>}
          <div className="form-actions"><button className="btn primary" onClick={save}>Save</button></div>
        </div>
      </Card>
      <Card title="Browsing">
        <div className="grid" style={{ gap: 12 }}>
          <Switch checked={!!settings?.show_hidden} onChange={v => saveSettings({ show_hidden: v })} label="Show hidden files" help="Names starting with a dot." />
          <Field label="Trash" group><Select className="btn" value={settings?.trash_days ?? 30} options={[7, 14, 30, 60, 90, 0].map(n => ({ value: n, label: n ? `Empty after ${n} days` : 'Keep until I empty it' }))} onChange={v => saveSettings({ trash_days: v })} /></Field>
          {user?.role === 'admin' && <Field label="App title (whole box)"><div className="row"><input value={title} onChange={e => setTitle(e.target.value)} /><button className="btn" onClick={() => saveSettings({ title }).then(() => toast('Saved'))}>Save</button></div></Field>}
        </div>
      </Card>
    </div>
  )
}

function LinksAlerts() {
  const { settings, saveSettings } = useApp()
  const { toast } = useToast()
  const [n, setN] = useState({ notify_kind: settings?.notify_kind || 'apprise', notify_url: settings?.notify_url || '', notify_token: settings?.notify_token || '' })
  if (!settings) return <Loading />
  return (
    <div className="two-col">
      <Card title="New links start with">
        <div className="grid" style={{ gap: 12 }}>
          <Field label="Expiry" group><Select className="btn" value={settings.send_expiry} options={EXPIRY_OPTIONS} onChange={v => saveSettings({ send_expiry: v })} /></Field>
          <Field label="Download limit" group><Select className="btn" value={settings.send_downloads} options={DOWNLOAD_OPTIONS.map(d => ({ value: d, label: downloadsLabel(d) }))} onChange={v => saveSettings({ send_downloads: v })} /></Field>
          <Switch checked={settings.send_encrypted} onChange={v => saveSettings({ send_encrypted: v })} label="End-to-end encrypted" help="Files are encrypted in the browser and the key travels only in the link." />
        </div>
      </Card>
      <Card title="Tell me when…">
        <div className="grid" style={{ gap: 12 }}>
          <Switch checked={settings.notify_sends} onChange={v => saveSettings({ notify_sends: v })} label="…a link of mine is downloaded" />
          <Switch checked={settings.notify_uploads} onChange={v => saveSettings({ notify_uploads: v })} label="…files arrive through an upload link" />
          <Field label="Send to" group><Segmented size="sm" value={n.notify_kind} onChange={v => setN({ ...n, notify_kind: v })} options={[{ value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'webhook', label: 'Webhook' }]} /></Field>
          <Field label="Address" help={n.notify_kind === 'apprise' ? 'An Apprise route, e.g. http://apprise:8000/notify/files' : n.notify_kind === 'ntfy' ? 'A topic URL, e.g. https://ntfy.sh/my-files' : 'Gets {title, body, url} as JSON'}><input value={n.notify_url} onChange={e => setN({ ...n, notify_url: e.target.value })} placeholder="Empty: the box default (owner only)" /></Field>
          <Field label="Token (optional)"><input type="password" value={n.notify_token} onChange={e => setN({ ...n, notify_token: e.target.value })} /></Field>
          <div className="form-actions">
            <button className="btn" onClick={() => api.post(`${V1}/notify/test`).then(() => toast('Sent')).catch(e => toast((e as Error).message, 'err'))}>Send a test</button>
            <button className="btn primary" onClick={() => saveSettings(n as Partial<S>).then(() => toast('Saved'))}>Save</button>
          </div>
        </div>
      </Card>
    </div>
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
    <div className="two-col">
      <Card title="Your drive over WebDAV">
        {!info ? <Loading /> : <div className="grid" style={{ gap: 12 }}>
          <p className="small muted">Mount your files in Finder (⌘K), Windows Explorer, Files on iPhone, rclone or any WebDAV app. Your home is the root; <b>Shared with me</b> and <b>Mounts</b> appear as folders.</p>
          <Field label="Address" group><CopyField value={info.webdav.url} /></Field>
          <Field label="Nextcloud-style address" help="For apps that only speak Nextcloud." group><CopyField value={info.webdav.nextcloud_url} /></Field>
          <Field label="User name" group><CopyField value={info.webdav.username} mono={false} /></Field>
          <p className="small muted">Password: an app password from the right. Other Pocket apps (Office, Documents, Collaborator) connect through Pocket Core instead and need nothing from you.</p>
        </div>}
      </Card>
      <Card title="App passwords">
        <div className="grid" style={{ gap: 12 }}>
          <div className="row"><input value={name} onChange={e => setName(e.target.value)} placeholder="Name, e.g. MacBook Finder" onKeyDown={e => e.key === 'Enter' && create()} /><button className="btn primary" onClick={create}>Create</button></div>
          {fresh && <div className="callout success grid" style={{ gap: 8 }}><span className="small">Copy it now — it is shown once.</span><CopyField value={fresh} /></div>}
          {tokens.data?.length ? <table className="table compact"><tbody>{tokens.data.map(t => <tr key={t.id}><td><b>{t.name}</b><br /><small className="muted mono">{t.prefix}…</small></td><td className="small muted">{t.last_used_at ? 'used ' + ago(t.last_used_at) : 'never used'}</td><td className="right"><button className="icon-btn" onClick={() => setDel(t)} title="Revoke"><Icon name="trash" size={15} /></button></td></tr>)}</tbody></table> : <p className="small muted">No app passwords yet.</p>}
        </div>
      </Card>
      {del && <Confirm title="Revoke this app password?" message={<>Apps using <b>{del.name}</b> lose access.</>} onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); tokens.reload() }} />}
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
    <div className="grid" style={{ gap: 16 }}>
      {!data.roots.length ? <div className="callout"><b>Mounting is off.</b> Set <code>MOUNT_ROOTS</code> (for example <code>/mnt,/srv/media</code>) and mount those folders into the container; then admins can show any folder below them here.</div>
        : <p className="muted small">Server folders shown in the sidebar for the people you pick. Allowed below: {data.roots.map(r => <code key={r} style={{ marginRight: 6 }}>{r}</code>)}</p>}
      <div className="row"><span className="grow" />{data.roots.length > 0 && <button className="btn primary" onClick={() => setEdit({ name: '', path: '', read_only: false, everyone_perm: '', access: [] })}><Icon name="plus" size={15} /> Mount a folder</button>}</div>
      <div className="link-list">
        {data.mounts.map(m => (
          <div key={m.id} className={`link-card ${m.missing || !m.allowed ? 'off' : ''}`}>
            <div className="link-ico"><Icon name="disk" size={18} /></div>
            <div className="grow"><div className="row wrap"><b>{m.name}</b>{m.read_only && <span className="badge"><Icon name="lock" size={11} /> read only</span>}{m.missing && <span className="badge danger">missing</span>}{!m.allowed && <span className="badge danger">outside MOUNT_ROOTS</span>}</div>
              <div className="small muted mono">{m.path}</div>
              <div className="small muted">{[m.everyone_perm ? `everyone (${m.everyone_perm})` : '', ...(m.access || []).map(a => `${a.name || a.email} (${a.perm})`)].filter(Boolean).join(', ') || 'nobody yet'}</div></div>
            <button className="btn sm" onClick={() => setEdit(m)}><Icon name="edit" size={13} /> Edit</button>
            <button className="icon-btn" title="Unmount" onClick={() => setDel(m)}><Icon name="x" size={15} /></button>
          </div>
        ))}
        {!data.mounts.length && data.roots.length > 0 && <p className="muted small">No mounts yet.</p>}
      </div>
      {edit && <MountDialog mount={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reload(); bump() }} />}
      {del && <Confirm title={`Unmount “${del.name}”?`} message="It disappears from the app for everyone. The folder on the server is not touched." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/mounts/${del.id}`); setDel(null); reload(); bump() }} />}
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
  const setPerm = (uid: number, perm: '' | Perm) => setM(x => ({ ...x, access: perm ? [...(x.access || []).filter(a => a.user_id !== uid), { user_id: uid, perm } as Access] : (x.access || []).filter(a => a.user_id !== uid) }))
  const save = async () => {
    try { if (m.id) await api.put(`${V1}/mounts/${m.id}`, m); else await api.post(`${V1}/mounts`, m); toast('Saved'); onSaved() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Modal title={m.id ? `Mount “${m.name}”` : 'Mount a server folder'} onClose={onClose}>
      <div className="grid" style={{ gap: 12 }}>
        <Field label="Name in the sidebar"><input value={m.name || ''} onChange={e => setM({ ...m, name: e.target.value })} placeholder="e.g. Media" /></Field>
        <Field label="Folder on the server" group><div className="row"><input className="mono" value={m.path || ''} onChange={e => setM({ ...m, path: e.target.value })} placeholder="/mnt/media" /><button type="button" className="btn" onClick={() => setBrowse(true)}>Browse…</button></div></Field>
        <Switch checked={!!m.read_only} onChange={v => setM({ ...m, read_only: v })} label="Read only" help="Nobody can change, add or delete files here, whatever their access says." />
        <Field label="Everyone on this box" group><Segmented size="sm" value={m.everyone_perm || ''} onChange={v => setM({ ...m, everyone_perm: v })} options={[{ value: '' as const, label: 'No' }, { value: 'view' as const, label: 'Can view' }, { value: 'edit' as const, label: 'Can edit' }]} /></Field>
        <div className="member-list">
          {people.map(p => {
            const a = access.find(x => x.user_id === p.id)
            return <div key={p.id} className="member-row">
              <Avatar user={{ ...p, username: '', role: 'member', created_at: '' }} />
              <span className="grow"><b>{p.name || p.email}</b><br /><small className="muted">{p.email}</small></span>
              <Segmented size="sm" value={a?.perm || ''} onChange={v => setPerm(p.id, v)} options={[{ value: '' as const, label: 'None' }, { value: 'view' as const, label: 'View' }, { value: 'edit' as const, label: 'Edit' }]} />
            </div>
          })}
        </div>
        <p className="small muted">Mounted files are deleted for good (no trash) and are not covered by this app's backups.</p>
        <div className="form-actions"><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={save}>Save</button></div>
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
    <Modal title="Pick a server folder" onClose={onClose}>
      <div className="picker-list">
        <div className="small mono muted mb-s">{data?.path || 'Allowed roots'}</div>
        {err && <div className="callout danger">{err}</div>}
        {data?.path && <button className="pick-row" onClick={() => setPath(data.parent || '')}><Icon name="arrowUp" size={15} /> ..</button>}
        {data?.dirs.map(d => <button key={d.path} className="pick-row" onClick={() => setPath(d.path)}><Icon name="folder" size={15} /> {d.name}</button>)}
        {data && !data.dirs.length && <p className="small muted pad center">No sub folders</p>}
      </div>
      <div className="form-actions"><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={!data?.path} onClick={() => onPick(data!.path)}>Use this folder</button></div>
    </Modal>
  )
}

function People() {
  const { user: me, status } = useApp()
  const { data, reload } = useAsync(() => api.get<User[]>(`${V1}/users`), [])
  const [edit, setEdit] = useState<Partial<User> & { password?: string } | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const { toast } = useToast()
  const save = async () => {
    if (!edit) return
    try { if (edit.id) await api.put(`${V1}/users/${edit.id}`, edit); else await api.post(`${V1}/users`, edit); setEdit(null); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <div className="grid" style={{ gap: 16 }}>
      <p className="muted small">Everyone has a private home folder — admins included cannot open other people's files. With Pocket Core, people get an account the first time they sign in.{status?.core_configured ? '' : ''}</p>
      <div className="row"><span className="grow" /><button className="btn primary" onClick={() => setEdit({ email: '', name: '', role: 'member', password: '' })}><Icon name="plus" size={15} /> Add person</button></div>
      <div className="link-list">
        {data?.map(u => (
          <div key={u.id} className="link-card">
            <Avatar user={u} />
            <div className="grow"><b>{u.name || u.email}</b> {u.role === 'admin' && <span className="badge">admin</span>}<div className="small muted">{u.email} · since {new Date(u.created_at).toLocaleDateString()}</div></div>
            <button className="btn sm" onClick={() => setEdit({ ...u, password: '' })}><Icon name="edit" size={13} /> Edit</button>
            {u.id !== me?.id && <button className="icon-btn" title="Delete" onClick={() => setDel(u)}><Icon name="trash" size={15} /></button>}
          </div>
        ))}
      </div>
      {edit && <Modal title={edit.id ? 'Edit person' : 'Add a person'} onClose={() => setEdit(null)}>
        <div className="grid" style={{ gap: 12 }}>
          <Field label="Name"><input value={edit.name || ''} onChange={e => setEdit({ ...edit, name: e.target.value })} /></Field>
          <Field label="Email"><input type="email" value={edit.email || ''} onChange={e => setEdit({ ...edit, email: e.target.value })} /></Field>
          <Field label={edit.id ? 'New password (optional)' : 'Password'} help="At least 8 characters"><input type="password" value={edit.password || ''} onChange={e => setEdit({ ...edit, password: e.target.value })} autoComplete="new-password" /></Field>
          <Field label="Role" group><Segmented value={edit.role || 'member'} onChange={v => setEdit({ ...edit, role: v })} options={[{ value: 'member' as const, label: 'Member' }, { value: 'admin' as const, label: 'Admin (mounts, people)' }]} /></Field>
          <div className="form-actions"><button className="btn" onClick={() => setEdit(null)}>Cancel</button><button className="btn primary" onClick={save}>Save</button></div>
        </div>
      </Modal>}
      {del && <Confirm title={`Delete ${del.name || del.email}?`} message="Their account, shares and links go. Their files are moved aside on the server (data/deleted-users), not deleted." onCancel={() => setDel(null)} onConfirm={async () => { try { await api.del(`${V1}/users/${del.id}`); setDel(null); reload() } catch (e) { toast((e as Error).message, 'err') } }} />}
    </div>
  )
}

function About() {
  const { data: info } = useAsync(() => api.get<SystemInfo>(`${V1}/settings/system`), [])
  if (!info) return <Loading />
  return (
    <div className="two-col">
      <Card title="This box">
        <div className="kv">
          <span>Version</span><b>{info.version}</b>
          <span>Your files</span><b>{bytes(info.home.size)} in {info.home.files} files</b>
          <span>Free on the server</span><b>{bytes(info.disk.free)} of {bytes(info.disk.total)}</b>
          <span>Links point to</span><b className="mono small">{info.public_url || '—'}</b>
          <span>Public listener</span><b>{info.public_listener ? 'on (share pages only)' : 'off'}</b>
          <span>Largest send</span><b>{info.max_send_mb ? bytes(info.max_send_mb * 1048576) : 'no limit'}</b>
          {info.data_dir && <><span>Data folder</span><b className="mono small">{info.data_dir}</b></>}
        </div>
      </Card>
      <Card title="Pocket Core">
        <div className="kv">
          <span>Connected</span><b>{info.core.configured ? (info.core.registered ? 'yes' : 'not yet') : 'no'}</b>
          {info.core.url && <><span>Address</span><b className="mono small">{info.core.url}</b></>}
          {info.core.error && <><span>Last error</span><b className="danger-text small">{info.core.error}</b></>}
        </div>
        <p className="small muted mt">With Core, people sign in with one account and Pocket Office, Documents and Collaborator can keep files here (scopes files:read and files:write, switchable per person in Core → Links).</p>
      </Card>
    </div>
  )
}
