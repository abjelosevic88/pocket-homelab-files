import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, filesRoute, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago } from '../lib/format'
import type { Entry, Share } from '../lib/types'
import { FileGlyph, Icon } from '../components/icons'
import { ShareDialog } from '../components/ShareDialog'
import { Confirm, Empty, Loading, PageHeader, Tabs, useAsync } from '../components/ui'

/** What others share with me, and what I share with them. */
export default function Shared() {
  const { version, bump } = useApp()
  const [tab, setTab] = useState<'with' | 'by'>('with')
  const { data, loading } = useAsync(() => api.get<{ by_me: Share[]; with_me: Share[] }>(`${V1}/shares`), [version])
  const [edit, setEdit] = useState<Share | null>(null)
  const [stop, setStop] = useState<Share | null>(null)
  const perm = (s: Share) => s.everyone_perm ? `everyone ${s.everyone_perm === 'edit' ? 'can edit' : 'can view'}` : ''
  return (
    <div className="page">
      <PageHeader title="Shared" sub="Folders and files people on this box share with each other. For strangers, use a link." />
      <Tabs tabs={[{ id: 'with', label: `With me${data ? ` (${data.with_me.length})` : ''}` }, { id: 'by', label: `By me${data ? ` (${data.by_me.length})` : ''}` }]} value={tab} onChange={setTab} />
      {loading && !data && <Loading />}
      {data && tab === 'with' && (data.with_me.length ? <div className="link-list mt">
        {data.with_me.map(s => (
          <Link key={s.id} to={filesRoute(s.space!)} className="link-card as-link">
            <div className="link-ico plain"><FileGlyph name={s.name || ''} mime="" isDir={s.is_dir} size={24} shared /></div>
            <div className="grow"><b>{s.name}</b><div className="small muted">from {s.owner_name || s.owner_email} · {s.perm === 'edit' ? 'you can edit' : 'view only'} · {ago(s.created_at)}</div>{s.note && <div className="small">{s.note}</div>}</div>
            <Icon name="chevronRight" size={16} className="faint" />
          </Link>
        ))}
      </div> : <Empty title="Nothing shared with you yet" icon={<Icon name="users" size={28} />}>When someone on this box shares a folder with you, it appears here, in the sidebar and in your WebDAV drive under “Shared with me”.</Empty>)}
      {data && tab === 'by' && (data.by_me.length ? <div className="link-list mt">
        {data.by_me.map(s => (
          <div key={s.id} className="link-card">
            <div className="link-ico plain"><FileGlyph name={s.path.split('/').pop() || ''} mime="" isDir={s.is_dir} size={24} shared /></div>
            <div className="grow"><Link to={s.is_dir ? filesRoute('home', s.path) : filesRoute('home', s.path.split('/').slice(0, -1).join('/') || '/') + '?open=' + encodeURIComponent(s.path.split('/').pop() || '')}><b>{s.path}</b></Link>
              <div className="small muted">{[...(s.members || []).map(m => `${m.name || m.email} (${m.perm === 'edit' ? 'edit' : 'view'})`), perm(s)].filter(Boolean).join(', ')}</div></div>
            <button className="btn sm" onClick={() => setEdit(s)}><Icon name="users" size={13} /> People</button>
            <button className="icon-btn" title="Stop sharing" onClick={() => setStop(s)}><Icon name="x" size={15} /></button>
          </div>
        ))}
      </div> : <Empty title="You share nothing yet" icon={<Icon name="folderShared" size={28} />}>Open a folder's menu in My files → “Share with people”.</Empty>)}
      {edit && <ShareDialog space="home" initialTab="people" entries={[{ name: edit.path.split('/').pop() || '/', path: edit.path, is_dir: edit.is_dir, size: 0, modified: '', mime: '' } as Entry]} onClose={() => { setEdit(null); bump() }} />}
      {stop && <Confirm title="Stop sharing?" message={<>Nobody else will see <b>{stop.path}</b> any more. The files stay in your home.</>} onCancel={() => setStop(null)} onConfirm={async () => { await api.del(`${V1}/shares/${stop.id}`); setStop(null); bump() }} />}
    </div>
  )
}
