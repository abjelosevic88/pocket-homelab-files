// Settings → Connected drives: other clouds copied one way into Pocket Files, on a schedule or once.
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, filesRoute, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, bytes, stamp } from '../lib/format'
import type { Cloud, CloudKind, CloudProvider } from '../lib/types'
import { FolderPicker } from '../components/FolderPicker'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { Field, Loading, Modal, Progress, Segmented, SettingRow, Toggle, useAsync, useToast } from '../components/ui'
import { Section } from './Settings'

const INTERVALS = [1, 6, 12, 24, 168].map(h => ({ value: h, label: h === 1 ? 'Every hour' : h === 24 ? 'Every day' : h === 168 ? 'Every week' : `Every ${h} hours` }))
const COLORS: Record<CloudKind, string> = { gdrive: '#1a73e8', dropbox: '#0061fe', onedrive: '#0078d4', icloud: '#3b82f6', rclone: '#64748b' }

type Providers = { providers: CloudProvider[]; rclone: string; rclone_error?: string; redirect: string }

export function Clouds() {
  const { bump } = useApp()
  const { toast } = useToast()
  const navigate = useNavigate()
  const providers = useAsync(() => api.get<Providers>(`${V1}/clouds/providers`), [])
  const [list, setList] = useState<Cloud[] | null>(null)
  const [adding, setAdding] = useState(false)
  const [edit, setEdit] = useState<Cloud | null>(null)
  const [del, setDel] = useState<Cloud | null>(null)
  const [code, setCode] = useState<Cloud | null>(null)
  const [reconnect, setReconnect] = useState<Cloud | null>(null)

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
      <Section title="Your drives" sub="Copied one way: nothing is ever changed in the cloud, and files deleted there stay here. Only you see your drives."
        actions={<button type="button" className="btn primary sm" onClick={() => setAdding(true)}><Icon name="plus" size={15} /> Connect a drive</button>}>
        <div className="set-people">
          {list.map(c => (
            <div key={c.id} className={`set-person ${c.status === 'reconnect' ? 'off' : ''}`}>
              <span className="set-token-ico" style={{ color: COLORS[c.kind] }}><Icon name="cloud" size={17} /></span>
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="set-person-name">{c.name}
                  {c.interval_hours === 0 ? <span className="badge">copied once</span> : <span className="badge"><Icon name="refresh" size={11} /> {INTERVALS.find(i => i.value === c.interval_hours)?.label.toLowerCase() || `every ${c.interval_hours} h`}</span>}
                </div>
                <div className="small muted ellipsis">{c.provider}{c.login ? ` · ${c.login}` : ''}{c.source ? ` · /${c.source}` : ''}</div>
                <Status c={c} />
              </div>
              <div className="row" style={{ flexWrap: 'wrap', justifyContent: 'flex-end' }}>
                {c.status === 'reconnect' && <button type="button" className="btn primary sm" onClick={() => signInAgain(c)}>Sign in again</button>}
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

      {adding && <ConnectDialog providers={providers.data} onClose={() => setAdding(false)} onCreated={c => { setAdding(false); load(); bump(); if (c.status === 'code') setCode(c) }} />}
      {edit && <EditDialog cloud={edit} kind={kinds[edit.kind]} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); load(); bump() }} />}
      {del && <DisconnectDialog cloud={del} onClose={() => setDel(null)} onDone={folder => { setDel(null); load(); bump(); toast(folder ? `Disconnected; the copy is now in My files${folder}` : 'Disconnected') }} />}
      {code && <CodeDialog cloud={code} onClose={() => setCode(null)} onDone={() => { setCode(null); load(); bump() }} />}
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

function ConnectDialog({ providers, onClose, onCreated }: { providers: Providers; onClose: () => void; onCreated: (c: Cloud) => void }) {
  const { toast } = useToast()
  const [kind, setKind] = useState<CloudKind | null>(null)
  const [name, setName] = useState('')
  const [mode, setMode] = useState<'sync' | 'once'>('sync')
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
        const q = new URLSearchParams({ name, interval_hours: String(hours), source, dest_space: dest?.space || '', dest_path: dest?.path || '' })
        if (p.kind === 'gdrive') q.set('shared_with_me', shared ? '1' : '0')
        const r = await api.get<{ url: string }>(`${V1}/clouds/oauth/${p.kind}/start?${q}`)
        window.location.href = r.url
        return
      }
      const c = await api.post<Cloud>(`${V1}/clouds`, { kind: p.kind, name, interval_hours: hours, source, fields, dest_space: dest?.space || '', dest_path: dest?.path || '' })
      onCreated(c)
    } catch (e) { toast((e as Error).message, 'err'); setBusy(false) }
  }

  if (!p) return (
    <Modal title="Connect a drive" onClose={onClose}>
      <div className="picker-list">
        {providers.providers.map(x => (
          <button type="button" key={x.kind} className="pick-row" disabled={!x.configured} onClick={() => { setKind(x.kind); setName(x.kind === 'rclone' ? '' : x.name) }}>
            <span style={{ color: COLORS[x.kind] }}><Icon name="cloud" size={17} /></span>
            <span className="grow" style={{ textAlign: 'left' }}><b>{x.name}</b><br /><small className="muted">{x.configured ? x.help : 'Not set up on this box yet: the admin adds its OAuth client (docs/clouds.md).'}</small></span>
            <Icon name="chevronRight" size={15} />
          </button>
        ))}
      </div>
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
        <Field label="How" group><Segmented size="sm" value={mode} onChange={setMode} options={[{ value: 'sync' as const, label: 'Keep in sync' }, { value: 'once' as const, label: 'Copy once' }]} /></Field>
        {mode === 'sync' && <SettingRow title="How often" help="New and changed files come in on this schedule."><Select value={interval} options={INTERVALS} onChange={setInterval_} label="How often" /></SettingRow>}
        <Field label="Where" group>
          <Segmented size="sm" value={dest ? 'pick' : 'auto'} onChange={v => v === 'pick' ? setPicking(true) : setDest(null)}
            options={[{ value: 'auto' as const, label: mode === 'sync' ? 'Its own place' : 'New folder in My files' }, { value: 'pick' as const, label: 'A folder I choose' }]} />
        </Field>
        <p className="small muted">{dest
          ? <>Files go straight into <b>{destLabel}</b> <button type="button" className="btn ghost sm" onClick={() => setPicking(true)}>Change…</button><br />Files you change there are kept: a sync only replaces a file when the cloud's copy is newer.</>
          : mode === 'sync' ? 'A read-only place under Drives in the sidebar, kept exactly like the cloud.' : 'Everything is copied once into a new folder in My files, where you can edit, move and share it like your own files.'}</p>
        {p.kind === 'gdrive' && <SettingRow title="Also “Shared with me”" help="Files others shared with you, in a folder of their own. Can be many small files."><Toggle checked={shared} onChange={setShared} label="Also Shared with me" /></SettingRow>}
        <Field label="Only this folder (optional)" help="A folder inside the cloud, like Photos/2024. Empty copies everything."><input value={source} onChange={e => setSource(e.target.value)} placeholder="Everything" /></Field>
        {(p.fields || []).map(f => (
          <Field key={f.key} label={f.label}>
            {f.multiline
              ? <textarea className="mono" rows={7} value={fields[f.key] || ''} placeholder={f.placeholder} onChange={e => setFields({ ...fields, [f.key]: e.target.value })} />
              : <input type={f.secret ? 'password' : 'text'} autoComplete="off" value={fields[f.key] || ''} placeholder={f.placeholder} onChange={e => setFields({ ...fields, [f.key]: e.target.value })} />}
          </Field>
        ))}
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
  const save = async () => {
    try { await api.put(`${V1}/clouds/${cloud.id}`, { name, interval_hours: interval, shared_with_me: shared, source }); onSaved() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Modal title={`Edit ${cloud.name}`} onClose={onClose} footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" onClick={save}>Save</button></>}>
      <div className="stack">
        <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} /></Field>
        {cloud.interval_hours > 0
          ? <SettingRow title="How often"><Select value={interval} options={INTERVALS} onChange={setInterval_} label="How often" /></SettingRow>
          : <p className="small muted">Copied once into {cloud.dest_space ? `${cloud.dest_name}${cloud.dest_path}` : `My files${cloud.folder}`}. “Copy again” brings in new and changed files.</p>}
        {cloud.dest_space && cloud.interval_hours > 0 && <p className="small muted">Copies into {cloud.dest_name}{cloud.dest_path === '/' ? '' : cloud.dest_path}. To copy somewhere else, connect the drive again.</p>}
        {cloud.kind === 'gdrive' && <SettingRow title="Also “Shared with me”"><Toggle checked={shared} onChange={setShared} label="Also Shared with me" /></SettingRow>}
        <Field label="Only this folder" help={kind ? `A folder inside ${kind.name}. Empty copies everything.` : undefined}><input value={source} onChange={e => setSource(e.target.value)} placeholder="Everything" /></Field>
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
