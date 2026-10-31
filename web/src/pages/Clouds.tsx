// Settings → Connected drives: other clouds copied one way into Pocket Files, on a schedule or once.
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, filesRoute, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, bytes, stamp } from '../lib/format'
import type { Cloud, CloudConflict, CloudKind, CloudProvider, Policy } from '../lib/types'
import { FolderPicker } from '../components/FolderPicker'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { Field, Loading, Modal, Progress, Segmented, SettingRow, Toggle, useAsync, useToast } from '../components/ui'
import { Section } from './Settings'
import { type CloudApp, CloudAppDialog, CloudApps } from './CloudApps'

const INTERVALS = [1, 6, 12, 24, 168].map(h => ({ value: h, label: h === 1 ? 'Every hour' : h === 24 ? 'Every day' : h === 168 ? 'Every week' : `Every ${h} hours` }))
const COLORS: Record<CloudKind, string> = { gdrive: '#1a73e8', dropbox: '#0061fe', onedrive: '#0078d4', icloud: '#3b82f6', rclone: '#64748b' }

// One line per drive in the list; the full help shows on the next step.
const SHORT: Record<CloudKind, string> = {
  gdrive: 'Sign in with Google, read-only.',
  dropbox: 'Sign in with Dropbox, read-only.',
  onedrive: 'Sign in with Microsoft, read-only.',
  icloud: 'Apple ID and password, then a code on your devices.',
  rclone: 'S3, SFTP, WebDAV, pCloud, Mega, Box… any rclone remote.',
}

const policies = (provider: string): { value: Policy; label: string }[] => [
  { value: 'ask', label: 'Ask me (keep both)' },
  { value: 'newer', label: 'The newer change wins' },
  { value: 'cloud', label: `${provider} wins` },
  { value: 'pocket', label: 'Pocket Files wins' },
]

type Providers = { providers: CloudProvider[]; rclone: string; rclone_error?: string; redirect: string }

export function Clouds() {
  const { bump, user } = useApp()
  const admin = user?.role === 'admin'
  const { toast } = useToast()
  const navigate = useNavigate()
  const providers = useAsync(() => api.get<Providers>(`${V1}/clouds/providers`), [])
  const [list, setList] = useState<Cloud[] | null>(null)
  const [adding, setAdding] = useState(false)
  const [edit, setEdit] = useState<Cloud | null>(null)
  const [del, setDel] = useState<Cloud | null>(null)
  const [code, setCode] = useState<Cloud | null>(null)
  const [reconnect, setReconnect] = useState<Cloud | null>(null)
  const [conflicts, setConflicts] = useState<Cloud | null>(null)

  const load = () => api.get<Cloud[]>(`${V1}/clouds`).then(setList).catch(e => toast((e as Error).message, 'err'))
  useEffect(() => { load() }, []) // eslint-disable-line react-hooks/exhaustive-deps
  // Poll while something runs (live progress), slowly otherwise.
  const busy = !!list?.some(c => c.status === 'syncing')
  useEffect(() => { const t = setInterval(load, busy ? 2500 : 30000); return () => clearInterval(t) }, [busy]) // eslint-disable-line react-hooks/exhaustive-deps

  // Back from a cloud's sign-in page.
  useEffect(() => {
    const q = new URLSearchParams(window.location.search)
    if (q.get('connected')) { toast('Connected. The first copy is running.'); bump() }
    if (q.get('error')) toast(q.get('error')!, 'err')
    if (q.toString()) window.history.replaceState(null, '', window.location.pathname)
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { const c = list?.find(x => x.status === 'code' && x.question); if (c && !code) setCode(c) }, [list]) // eslint-disable-line react-hooks/exhaustive-deps

  const act = async (c: Cloud, what: 'sync' | 'stop') => {
    try { await api.post(`${V1}/clouds/${c.id}/${what}`); load() } catch (e) { toast((e as Error).message, 'err') }
  }
  const signInAgain = async (c: Cloud) => {
    if (c.kind === 'icloud') { setReconnect(c); return }
    try { const r = await api.get<{ url: string }>(`${V1}/clouds/oauth/${c.kind}/start?cloud=${c.id}`); window.location.href = r.url } catch (e) { toast((e as Error).message, 'err') }
  }

  if (!list || !providers.data) return <Loading />
  const kinds = Object.fromEntries(providers.data.providers.map(p => [p.kind, p]))
  return (
    <div className="stack loose">
      {providers.data.rclone_error && <div className="callout danger"><Icon name="info" size={15} /><span>{providers.data.rclone_error}</span></div>}
      <Section title="Your drives" sub="Two-way drives are yours to edit here, and changes go both ways; one-way drives only bring the cloud in. Only you see your drives."
        actions={<button type="button" className="btn primary sm" onClick={() => setAdding(true)}><Icon name="plus" size={15} /> Connect a drive</button>}>
        <div className="set-people">
          {list.map(c => (
            <div key={c.id} className={`set-person ${c.status === 'reconnect' ? 'off' : ''}`}>
              <span className="set-token-ico" style={{ color: COLORS[c.kind] }}><Icon name="cloud" size={17} /></span>
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="set-person-name">{c.name}
                  {c.direction === 'once' ? <span className="badge">copied once</span> : <>
                    <span className="badge">{c.direction === 'two_way' ? '⇄ two-way' : '→ one-way'}</span>
                    <span className="badge"><Icon name="refresh" size={11} /> {INTERVALS.find(i => i.value === c.interval_hours)?.label.toLowerCase() || `every ${c.interval_hours} h`}</span>
                  </>}
                  {c.conflicts > 0 && <button type="button" className="badge warn" onClick={() => setConflicts(c)}>{c.conflicts} to pick</button>}
                </div>
                <div className="small muted ellipsis">{c.provider}{c.login ? ` · ${c.login}` : ''}{c.source ? ` · /${c.source}` : ''}</div>
                <Status c={c} />
              </div>
              <div className="row" style={{ flexWrap: 'wrap', justifyContent: 'flex-end' }}>
                {c.status === 'reconnect' && <button type="button" className="btn primary sm" onClick={() => signInAgain(c)}>Sign in again</button>}
                {c.conflicts > 0 && <button type="button" className="btn primary sm" onClick={() => setConflicts(c)}>Pick versions</button>}
                {c.status === 'error' && c.error?.includes('Sync anyway') && <button type="button" className="btn sm danger-text" onClick={() => api.post(`${V1}/clouds/${c.id}/sync?force=1`).then(load).catch(e => toast((e as Error).message, 'err'))}>Sync anyway</button>}
                {c.status === 'code' && <button type="button" className="btn primary sm" onClick={() => c.question ? setCode(c) : setReconnect(c)}>{c.question ? 'Enter code' : 'Sign in again'}</button>}
                {c.status === 'syncing'
                  ? <button type="button" className="btn sm" onClick={() => act(c, 'stop')}>Stop</button>
                  : c.status !== 'reconnect' && c.status !== 'code' && <button type="button" className="btn sm" onClick={() => act(c, 'sync')}><Icon name="refresh" size={13} /> {c.interval_hours === 0 ? 'Copy again' : 'Sync now'}</button>}
                {c.space && c.last_ok_at && <button type="button" className="btn sm" onClick={() => navigate(filesRoute(c.space!, c.path || '/'))}>Open</button>}
                <button type="button" className="btn ghost icon-only sm" title="Edit" aria-label={`Edit ${c.name}`} onClick={() => setEdit(c)}><Icon name="edit" size={14} /></button>
                <button type="button" className="btn ghost icon-only sm" title="Disconnect" aria-label={`Disconnect ${c.name}`} onClick={() => setDel(c)}><Icon name="x" size={15} /></button>
              </div>
            </div>
          ))}
          {!list.length && <p className="muted small">No drives yet. Connect one to bring its files into Pocket Files.</p>}
        </div>
      </Section>

      {admin && <CloudApps onChanged={providers.reload} />}

      {adding && <ConnectDialog providers={providers.data} admin={admin} onSetUp={() => providers.reload()} onClose={() => setAdding(false)} onCreated={c => { setAdding(false); load(); bump(); if (c.status === 'code') setCode(c) }} />}
      {edit && <EditDialog cloud={edit} kind={kinds[edit.kind]} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); load(); bump() }} />}
      {del && <DisconnectDialog cloud={del} onClose={() => setDel(null)} onDone={folder => { setDel(null); load(); bump(); toast(folder ? `Disconnected; the copy is now in My files${folder}` : 'Disconnected') }} />}
      {code && <CodeDialog cloud={code} onClose={() => setCode(null)} onDone={() => { setCode(null); load(); bump() }} />}
      {conflicts && <ConflictsDialog cloud={conflicts} onClose={() => { setConflicts(null); load(); bump() }} />}
      {reconnect && <ICloudPassword cloud={reconnect} onClose={() => setReconnect(null)} onDone={c => { setReconnect(null); load(); if (c.question) setCode(c) }} />}
    </div>
  )
}

function Status({ c }: { c: Cloud }) {
  const p = c.progress
  if (c.status === 'syncing' && p) {
    if (p.waiting) return <div className="small muted">Waiting for another drive to finish…</div>
    const label = p.total_files > 0
      ? `${p.files.toLocaleString()} of ${p.total_files.toLocaleString()} files · ${bytes(p.bytes)} of ${bytes(p.total_bytes)}${p.speed ? ` · ${bytes(p.speed)}/s` : ''}`
      : `Checking… ${p.checked.toLocaleString()} files compared`
    return <div className="mt-s"><Progress value={p.bytes} total={p.total_bytes || 1} label={label} /></div>
  }
  if (c.status === 'syncing') return <div className="small muted">Starting…</div>
  if (c.status === 'reconnect') return <div className="small danger-text">{c.error || 'The cloud no longer accepts the sign-in.'}</div>
  if (c.status === 'code') return <div className="small muted">{c.question ? 'Waiting for the code Apple sent to your devices.' : 'Sign in again to finish connecting.'}</div>
  const totals = c.last_ok_at ? `${c.files.toLocaleString()} files · ${bytes(c.bytes)}` : ''
  if (c.status === 'error') return <div className="small"><span className="danger-text">{c.error}</span>{totals && <span className="muted"> · last good copy {ago(c.last_ok_at!)}</span>}</div>
  const into = c.dest_space ? `${c.dest_name}${c.dest_path === '/' ? '' : c.dest_path}` : `My files${c.folder}`
  if (c.status === 'done') return <div className="small muted" title={c.last_ok_at ? stamp(c.last_ok_at) : ''}>Copied {ago(c.last_ok_at || '')} into {into} · {totals}</div>
  return <div className="small muted" title={c.last_sync_at ? stamp(c.last_sync_at) : ''}>
    {c.last_sync_at ? `Synced ${ago(c.last_sync_at)}` : 'Not synced yet'}{c.dest_space && ` into ${into}`}{totals && ` · ${totals}`}{c.next_sync_at && new Date(c.next_sync_at) > new Date() ? ` · next ${new Date(c.next_sync_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}` : ''}
  </div>
}

function ConnectDialog({ providers, admin, onSetUp, onClose, onCreated }: { providers: Providers; admin: boolean; onSetUp: () => void; onClose: () => void; onCreated: (c: Cloud) => void }) {
  const [setup, setSetup] = useState<CloudApp | null>(null)
  const { toast } = useToast()
  const [kind, setKind] = useState<CloudKind | null>(null)
  const [name, setName] = useState('')
  const [mode, setMode] = useState<'two_way' | 'one_way' | 'once'>('two_way')
  const [policy, setPolicy] = useState<Policy>('ask')
  const [interval, setInterval_] = useState(6)
  const [shared, setShared] = useState(false)
  const [source, setSource] = useState('')
  const [fields, setFields] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [dest, setDest] = useState<{ space: string; path: string } | null>(null)
  const [picking, setPicking] = useState(false)
  const { spaces } = useApp()
  const destLabel = dest ? `${spaces.find(s => s.id === dest.space)?.name || 'My files'}${dest.path === '/' ? '' : dest.path}` : ''
  const p = providers.providers.find(x => x.kind === kind)

  const connect = async () => {
    if (!p) return
    setBusy(true)
    const hours = mode === 'once' ? 0 : interval
    try {
      if (p.auth === 'oauth') {
        const q = new URLSearchParams({ name, interval_hours: String(hours), source, dest_space: dest?.space || '', dest_path: dest?.path || '', direction: mode === 'once' ? '' : mode, conflict: policy })
        if (p.kind === 'gdrive') q.set('shared_with_me', shared ? '1' : '0')
        const r = await api.get<{ url: string }>(`${V1}/clouds/oauth/${p.kind}/start?${q}`)
        window.location.href = r.url
        return
      }
      const c = await api.post<Cloud>(`${V1}/clouds`, { kind: p.kind, name, interval_hours: hours, source, fields, dest_space: dest?.space || '', dest_path: dest?.path || '', direction: mode === 'once' ? '' : mode, conflict: policy })
      onCreated(c)
    } catch (e) { toast((e as Error).message, 'err'); setBusy(false) }
  }

  if (!p) return (
    <Modal title="Connect a drive" onClose={onClose}>
      <div className="cloud-pick">
        {providers.providers.map(x => (
          <button type="button" key={x.kind} className="cloud-pick-row" disabled={!x.configured && !admin}
            onClick={() => {
              if (!x.configured) { api.get<CloudApp[]>(`${V1}/clouds/apps`).then(list => setSetup(list.find(a => a.kind === x.kind) || null)).catch(e => toast((e as Error).message, 'err')); return }
              setKind(x.kind); setName(x.kind === 'rclone' ? '' : x.name)
            }}>
            <span className="cloud-pick-ico" style={{ color: COLORS[x.kind] }}><Icon name="cloud" size={20} /></span>
            <span className="cloud-pick-text">
              <b>{x.name}</b>
              <small>{x.configured ? SHORT[x.kind] : admin ? 'Not set up yet. Tap to set it up (about five minutes).' : 'Not set up yet: ask the admin of this box.'}</small>
            </span>
            {x.configured ? <Icon name="chevronRight" size={16} className="muted" /> : admin && <span className="btn sm">Set up</span>}
          </button>
        ))}
      </div>
      {setup && <CloudAppDialog app={setup} onClose={() => setSetup(null)} onSaved={() => { setSetup(null); onSetUp() }} />}
    </Modal>
  )

  const ready = p.auth === 'oauth' || (p.fields || []).every(f => (fields[f.key] || '').trim())
  return (
    <Modal title={`Connect ${p.name}`} onClose={onClose} footer={<>
      <button type="button" className="btn" onClick={() => setKind(null)}>Back</button>
      <button type="button" className="btn primary" disabled={!ready || busy} onClick={connect}>{p.auth === 'oauth' ? `Sign in with ${p.name}` : 'Connect'}</button>
    </>}>
      <div className="stack">
        <p className="small muted">{p.help}</p>
        <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} placeholder={p.name} /></Field>
        {(p.fields || []).map(f => (
          <Field key={f.key} label={f.label}>
            {f.multiline
              ? <textarea className="mono" rows={7} value={fields[f.key] || ''} placeholder={f.placeholder} onChange={e => setFields({ ...fields, [f.key]: e.target.value })} />
              : <input type={f.secret ? 'password' : 'text'} autoComplete="off" value={fields[f.key] || ''} placeholder={f.placeholder} onChange={e => setFields({ ...fields, [f.key]: e.target.value })} />}
          </Field>
        ))}
        <Field label="Sync" group><Segmented size="sm" value={mode} onChange={setMode} options={[{ value: 'two_way' as const, label: 'Two-way' }, { value: 'one_way' as const, label: 'One-way' }, { value: 'once' as const, label: 'Copy once' }]} /></Field>
        <p className="small muted">{mode === 'two_way'
          ? <>Edit here or in {p.name}: changes and deletions go both ways. Whatever one side loses is kept 30 days ({p.name}’s trash, and versions here).{p.kind === 'gdrive' ? ' Google Docs, Sheets and Slides arrive as Word, Excel and PowerPoint, and your edits go back into the same Doc.' : ''}</>
          : mode === 'one_way' ? <>{p.name} comes in; nothing here is ever sent back.</> : <>A one-time snapshot; it never syncs again.</>}</p>
        {mode !== 'once' && <SettingRow title="How often" help="Changes are picked up on this schedule, and whenever you press Sync now."><Select value={interval} options={INTERVALS} onChange={setInterval_} label="How often" /></SettingRow>}
        {mode === 'two_way' && <SettingRow title="Changed on both sides" help="What happens when the same file changed here and in the cloud since the last sync."><Select value={policy} options={policies(p.name)} onChange={setPolicy} label="Changed on both sides" /></SettingRow>}
        <Field label="Where" group>
          <Segmented size="sm" value={dest ? 'pick' : 'auto'} onChange={v => v === 'pick' ? setPicking(true) : setDest(null)}
            options={[{ value: 'auto' as const, label: mode === 'once' ? 'New folder in My files' : 'Its own place' }, { value: 'pick' as const, label: 'A folder I choose' }]} />
        </Field>
        <p className="small muted">{dest
          ? <>Files go straight into <b>{destLabel}</b> <button type="button" className="btn ghost sm" onClick={() => setPicking(true)}>Change…</button><br />{mode === 'two_way' ? 'Everything in this folder goes to the cloud too, so give the drive a folder of its own.' : 'Files you change there are kept: a sync only replaces a file when the cloud’s copy is newer.'}</>
          : mode === 'two_way' ? 'A place of its own under Drives in the sidebar, editable like My files.' : mode === 'one_way' ? 'A read-only place under Drives in the sidebar, kept exactly like the cloud.' : 'Everything is copied once into a new folder in My files, where you can edit, move and share it like your own files.'}</p>
        {p.kind === 'gdrive' && <SettingRow title="Also “Shared with me”" help="Files others shared with you, in a folder of their own. Can be many small files."><Toggle checked={shared} onChange={setShared} label="Also Shared with me" /></SettingRow>}
        <Field label="Only this folder (optional)" help="A folder inside the cloud, like Photos/2024. Empty copies everything."><input value={source} onChange={e => setSource(e.target.value)} placeholder="Everything" /></Field>
        {busy && p.auth === 'form' && <p className="small muted">Checking the connection…</p>}
      </div>
      {picking && <FolderPicker title={`Where should ${name || p.name} go?`} button="Use this folder" kinds={['home', 'mount']} startSpace={dest?.space || 'home'} startPath={dest?.path || '/'}
        onClose={() => setPicking(false)} onPick={(sp, path) => { setDest({ space: sp, path }); setPicking(false) }} />}
    </Modal>
  )
}

function CodeDialog({ cloud, onClose, onDone }: { cloud: Cloud; onClose: () => void; onDone: () => void }) {
  const { toast } = useToast()
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const send = async () => {
    setBusy(true)
    try {
      const c = await api.post<Cloud>(`${V1}/clouds/${cloud.id}/code`, { code })
      if (c.question) { toast(c.question.error || 'Apple asks again; enter the new code.', 'err'); setCode(''); setBusy(false); return }
      toast('Connected. The first copy is running.'); onDone()
    } catch (e) { toast((e as Error).message, 'err'); setBusy(false) }
  }
  return (
    <Modal title={`Code for ${cloud.name}`} size="sm" onClose={onClose} footer={<><button type="button" className="btn" onClick={onClose}>Later</button><button type="button" className="btn primary" disabled={code.trim().length < 6 || busy} onClick={send}>Connect</button></>}>
      <div className="stack">
        <p className="small muted">{cloud.question?.help || 'Enter the six-digit code Apple shows on your iPhone, iPad or Mac.'}</p>
        <Field label="Code"><input inputMode="numeric" autoComplete="one-time-code" autoFocus value={code} onChange={e => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))} onKeyDown={e => { if (e.key === 'Enter' && code.length >= 6) send() }} placeholder="123456" /></Field>
      </div>
    </Modal>
  )
}

function ICloudPassword({ cloud, onClose, onDone }: { cloud: Cloud; onClose: () => void; onDone: (c: Cloud) => void }) {
  const { toast } = useToast()
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const send = async () => {
    setBusy(true)
    try { onDone(await api.post<Cloud>(`${V1}/clouds/${cloud.id}/reconnect`, { password })) } catch (e) { toast((e as Error).message, 'err'); setBusy(false) }
  }
  return (
    <Modal title={`Sign in to ${cloud.name} again`} size="sm" onClose={onClose} footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" disabled={!password || busy} onClick={send}>Continue</button></>}>
      <div className="stack">
        <p className="small muted">Apple ID {cloud.login}. Apple sends a code to your devices next.</p>
        <Field label="Password"><input type="password" autoFocus value={password} onChange={e => setPassword(e.target.value)} /></Field>
      </div>
    </Modal>
  )
}

function EditDialog({ cloud, kind, onClose, onSaved }: { cloud: Cloud; kind?: CloudProvider; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [name, setName] = useState(cloud.name)
  const [interval, setInterval_] = useState(cloud.interval_hours)
  const [shared, setShared] = useState(!!cloud.shared_with_me)
  const [source, setSource] = useState(cloud.source || '')
  const [direction, setDirection] = useState<'two_way' | 'one_way'>(cloud.direction === 'two_way' ? 'two_way' : 'one_way')
  const [policy, setPolicy] = useState<Policy>(cloud.conflict || 'ask')
  const scheduled = cloud.interval_hours > 0
  const switching = scheduled && direction !== cloud.direction
  const save = async () => {
    try {
      await api.put(`${V1}/clouds/${cloud.id}`, { name, interval_hours: interval, shared_with_me: shared, source, ...(scheduled ? { direction, conflict: policy } : {}) })
      onSaved()
    } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Modal title={`Edit ${cloud.name}`} onClose={onClose} footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" onClick={save}>Save</button></>}>
      <div className="stack">
        <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} /></Field>
        {scheduled && <Field label="Sync" group><Segmented size="sm" value={direction} onChange={setDirection} options={[{ value: 'two_way' as const, label: 'Two-way' }, { value: 'one_way' as const, label: 'One-way' }]} /></Field>}
        {switching && direction === 'two_way' && <div className="callout"><Icon name="info" size={15} /><span>The first two-way sync merges both sides (nothing is deleted); after that, changes and deletions go both ways.{cloud.kind !== 'icloud' && cloud.kind !== 'rclone' ? ` ${cloud.provider} asks you to sign in again once, to allow changes.` : ''}</span></div>}
        {switching && direction === 'one_way' && <div className="callout"><Icon name="info" size={15} /><span>From now on nothing here goes to {cloud.provider}; {cloud.dest_space ? 'the folder stays editable' : 'the drive’s place becomes read-only'}.</span></div>}
        {scheduled
          ? <SettingRow title="How often"><Select value={interval} options={INTERVALS} onChange={setInterval_} label="How often" /></SettingRow>
          : <p className="small muted">Copied once into {cloud.dest_space ? `${cloud.dest_name}${cloud.dest_path}` : `My files${cloud.folder}`}. “Copy again” brings in new and changed files.</p>}
        {scheduled && direction === 'two_way' && <SettingRow title="Changed on both sides"><Select value={policy} options={policies(cloud.provider)} onChange={setPolicy} label="Changed on both sides" /></SettingRow>}
        {cloud.dest_space && scheduled && <p className="small muted">Syncs with {cloud.dest_name}{cloud.dest_path === '/' ? '' : cloud.dest_path}. To use another folder, connect the drive again.</p>}
        {cloud.kind === 'gdrive' && <SettingRow title="Also “Shared with me”" help="Always one-way: files others shared with you are never changed from here."><Toggle checked={shared} onChange={setShared} label="Also Shared with me" /></SettingRow>}
        <Field label="Only this folder" help={kind ? `A folder inside ${kind.name}. Empty syncs everything.` : undefined}><input value={source} onChange={e => setSource(e.target.value)} placeholder="Everything" /></Field>
      </div>
    </Modal>
  )
}

function ConflictsDialog({ cloud, onClose }: { cloud: Cloud; onClose: () => void }) {
  const { toast } = useToast()
  const { data, reload } = useAsync(() => api.get<CloudConflict[]>(`${V1}/clouds/${cloud.id}/conflicts`), [cloud.id])
  const pick = async (cf: CloudConflict, keep: 'pocket' | 'cloud' | 'both') => {
    try { await api.post(`${V1}/clouds/${cloud.id}/conflicts/${cf.id}`, { keep }); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  useEffect(() => { if (data && !data.length) onClose() }, [data]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <Modal title="Changed on both sides" onClose={onClose}>
      <div className="stack">
        <p className="small muted">These files changed here and in {cloud.provider} since the last sync. Pick which version stays; the other is kept 30 days ({cloud.provider}’s trash, and versions here). Your choice reaches {cloud.provider} on the next sync, which starts right away.</p>
        {!data ? <Loading /> : data.map(cf => (
          <div key={cf.id} className="conflict-row">
            <div className="grow" style={{ minWidth: 0 }}>
              <div className="ellipsis"><b>{cf.path.split('/').pop()}</b>{cf.doc && <span className="badge">Google {cf.path.endsWith('.xlsx') ? 'Sheet' : cf.path.endsWith('.pptx') ? 'Slides' : 'Doc'}</span>}</div>
              <div className="small muted ellipsis">{cf.path.includes('/') ? '/' + cf.path.split('/').slice(0, -1).join('/') : '/'} · {ago(cf.created_at)}</div>
              <div className="small muted ellipsis">{cloud.provider}: {cf.cloud_name.split('/').pop()} · here: {cf.pocket_name.split('/').pop()}</div>
            </div>
            <div className="row conflict-actions">
              <button type="button" className="btn sm primary" onClick={() => pick(cf, 'pocket')}>Keep mine</button>
              <button type="button" className="btn sm" onClick={() => pick(cf, 'cloud')}>Keep {cloud.provider}’s</button>
              <button type="button" className="btn sm" onClick={() => pick(cf, 'both')}>Keep both</button>
            </div>
          </div>
        ))}
      </div>
    </Modal>
  )
}

function DisconnectDialog({ cloud, onClose, onDone }: { cloud: Cloud; onClose: () => void; onDone: (folder: string) => void }) {
  const { toast } = useToast()
  const [keep, setKeep] = useState(true)
  const go = async () => {
    try { const r = await api.del<{ folder?: string }>(`${V1}/clouds/${cloud.id}${keep && cloud.interval_hours > 0 && !cloud.dest_space ? '?keep=1' : ''}`); onDone(r?.folder || '') } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Modal title={`Disconnect ${cloud.name}?`} size="sm" onClose={onClose} footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn danger" onClick={go}>Disconnect</button></>}>
      <div className="stack">
        <p className="small">Pocket Files stops copying from {cloud.provider}. Nothing changes in the cloud.</p>
        {cloud.interval_hours > 0 && !cloud.dest_space
          ? <SettingRow title="Keep the copied files" help={keep ? 'They move into My files as an ordinary folder.' : `The copy (${bytes(cloud.bytes)}) is deleted from this server.`}><Toggle checked={keep} onChange={setKeep} label="Keep the copied files" /></SettingRow>
          : <p className="small muted">The files already in {cloud.dest_space ? `${cloud.dest_name}${cloud.dest_path === '/' ? '' : cloud.dest_path}` : `My files${cloud.folder}`} stay.</p>}
      </div>
    </Modal>
  )
}
