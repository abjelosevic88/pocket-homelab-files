import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, filesRoute, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, bytes, plural, stamp } from '../lib/format'
import { EXPIRY_OPTIONS } from '../lib/sends'
import type { Request } from '../lib/types'
import { Icon } from '../components/icons'
import { FolderPicker } from '../components/FolderPicker'
import { Confirm, CopyField, Empty, Field, Loading, Modal, PageHeader, Select, Switch, copyText, useAsync, useToast } from '../components/ui'

/** Upload links: people without an account drop files into one of your folders. */
export default function Requests() {
  const { version, bump } = useApp()
  const { data, loading } = useAsync(() => api.get<Request[]>(`${V1}/requests`), [version])
  const [create, setCreate] = useState(false)
  const [edit, setEdit] = useState<Request | null>(null)
  const [del, setDel] = useState<Request | null>(null)
  const { toast } = useToast()
  return (
    <div className="page">
      <PageHeader title="Upload links" sub="Anyone with the link can drop files into a folder of yours — without seeing what is there." actions={<button className="btn primary" onClick={() => setCreate(true)}><Icon name="plus" size={15} /> New upload link</button>} />
      {loading && !data && <Loading />}
      {data && !data.length && <Empty title="No upload links yet" icon={<Icon name="inbox" size={30} />}>Ask a friend for the wedding photos, a client for their documents: make a link, send it, the files land in the folder you pick.</Empty>}
      <div className="link-list">
        {data?.map(r => (
          <div key={r.id} className={`link-card ${r.open ? '' : 'off'}`}>
            <div className="link-ico" style={{ background: '#dcfce7', color: '#15803d' }}><Icon name="inbox" size={18} /></div>
            <div className="grow">
              <div className="row wrap"><b>{r.title}</b>{!r.open && <span className="badge">{r.reason}</span>}{r.has_password && <span className="badge"><Icon name="lock" size={11} /> password</span>}</div>
              <div className="small muted">
                into <Link to={filesRoute(r.space, r.path)}>{r.space_name}{r.path === '/' ? '' : r.path}</Link>{r.missing && <span className="danger-text"> (folder missing)</span>}
                {' · '}{plural(r.files_received, 'file')}{r.bytes_received ? ` (${bytes(r.bytes_received)})` : ''}{r.last_upload_at && <> · last {ago(r.last_upload_at)}</>}
                {' · '}{r.expires_at ? <span title={stamp(r.expires_at)}>{new Date(r.expires_at) < new Date() ? 'expired' : 'until ' + new Date(r.expires_at).toLocaleDateString()}</span> : 'no end date'}
                {r.max_files ? ` · up to ${plural(r.max_files, 'file')}` : ''}
              </div>
            </div>
            <div className="row">
              <button className="btn sm" onClick={() => copyText(r.url).then(() => toast('Link copied'))}><Icon name="copy" size={13} /> Copy</button>
              <button className="icon-btn" title="Edit" onClick={() => setEdit(r)}><Icon name="edit" size={15} /></button>
              <button className="icon-btn" title="Delete" onClick={() => setDel(r)}><Icon name="trash" size={15} /></button>
            </div>
          </div>
        ))}
      </div>
      {create && <RequestDialog onClose={() => setCreate(false)} />}
      {edit && <RequestDialog existing={edit} onClose={() => setEdit(null)} />}
      {del && <Confirm title="Delete this upload link?" message="The link stops working. Files already received stay where they are." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/requests/${del.id}`); setDel(null); bump() }} />}
    </div>
  )
}

export function RequestDialog({ space: s0 = 'home', path: p0 = '/', folderName, existing, onClose }: { space?: string; path?: string; folderName?: string; existing?: Request; onClose: () => void }) {
  const { spaces, bump } = useApp()
  const { toast } = useToast()
  const [form, setForm] = useState({
    title: existing?.title || (folderName ? `Files for ${folderName}` : 'Send me files'), message: existing?.message || '',
    space: existing?.space || s0, path: existing?.path || p0, expires: existing ? '' : '7d', max_files: existing?.max_files || 0, max_file_mb: existing?.max_file_mb || 0,
    password: '', remove_password: false, notify: existing?.notify ?? true, closed: existing?.closed ?? false,
  })
  const [pick, setPick] = useState(false)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState<Request | null>(null)
  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) => setForm(f => ({ ...f, [k]: v }))
  const spaceName = spaces.find(x => x.id === form.space)?.name || 'My files'
  const save = async () => {
    setBusy(true)
    try {
      const r = existing ? await api.put<Request>(`${V1}/requests/${existing.id}`, form) : await api.post<Request>(`${V1}/requests`, form)
      bump()
      if (existing) { toast('Saved'); onClose() } else setDone(r)
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  if (done) return (
    <Modal title="Upload link ready" onClose={onClose}>
      <div className="grid" style={{ gap: 12 }}>
        <p className="muted">Whoever opens it can upload into <b>{spaceName}{done.path === '/' ? '' : done.path}</b>.</p>
        <CopyField value={done.url} label="Copy link" />
        <div className="form-actions"><button className="btn primary" onClick={onClose}>Done</button></div>
      </div>
    </Modal>
  )
  return (
    <Modal title={existing ? 'Edit upload link' : 'Ask for files'} onClose={onClose}>
      <div className="grid" style={{ gap: 12 }}>
        <Field label="Title they see"><input value={form.title} onChange={e => set('title', e.target.value)} /></Field>
        <Field label="Message (optional)"><textarea value={form.message} onChange={e => set('message', e.target.value)} placeholder="Hi! Drop the photos from Saturday here." rows={2} /></Field>
        <Field label="Files land in" group><button type="button" className="btn folder-btn" onClick={() => setPick(true)}><Icon name="folder" size={15} /> {spaceName}{form.path === '/' ? '' : form.path}</button></Field>
        <div className="two-col tight">
          <Field label={existing ? 'Expires (leave to keep)' : 'Expires after'} group><Select className="btn" value={form.expires} options={[...(existing ? [{ value: '', label: existing.expires_at ? 'Keep: ' + new Date(existing.expires_at).toLocaleDateString() : 'Keep: never' }] : []), ...EXPIRY_OPTIONS]} onChange={v => set('expires', v)} /></Field>
          <Field label="Most files" group><Select className="btn" value={form.max_files} options={[0, 1, 5, 10, 25, 50, 100, 500].map(n => ({ value: n, label: n ? String(n) : 'No limit' }))} onChange={v => set('max_files', v)} /></Field>
        </div>
        <Field label="Largest file" group><Select className="btn" value={form.max_file_mb} options={[0, 10, 100, 500, 1024, 4096, 20480].map(n => ({ value: n, label: n ? (n >= 1024 ? `${n / 1024} GB` : `${n} MB`) : 'No limit' }))} onChange={v => set('max_file_mb', v)} /></Field>
        <Field label={existing?.has_password ? 'New password (leave empty to keep)' : 'Password (optional)'}><input type="password" value={form.password} onChange={e => set('password', e.target.value)} autoComplete="new-password" /></Field>
        {existing?.has_password && <Switch checked={form.remove_password} onChange={v => set('remove_password', v)} label="Remove the password" />}
        <Switch checked={form.notify} onChange={v => set('notify', v)} label="Tell me when files arrive" />
        {existing && <Switch checked={form.closed} onChange={v => set('closed', v)} label="Closed (stop taking files)" />}
        <div className="form-actions"><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy} onClick={save}>{existing ? 'Save' : 'Create link'}</button></div>
      </div>
      {pick && <FolderPicker title="Where should the files go?" button="Use this folder" startSpace={form.space} startPath={form.path} onClose={() => setPick(false)} onPick={(sp, p) => { setForm(f => ({ ...f, space: sp, path: p })); setPick(false) }} />}
    </Modal>
  )
}
