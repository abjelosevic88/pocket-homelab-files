import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, bytes, stamp } from '../lib/format'
import type { TrashItem } from '../lib/types'
import { FileGlyph, Icon } from '../components/icons'
import { Confirm, Empty, Loading, PageHeader, useAsync, useToast } from '../components/ui'

export default function Trash() {
  const { version, bump, settings } = useApp()
  const { data, loading } = useAsync(() => api.get<TrashItem[]>(`${V1}/trash`), [version])
  const [empty, setEmpty] = useState(false)
  const { toast } = useToast()
  const restore = async (t: TrashItem) => { try { const r = await api.post<{ path: string }>(`${V1}/trash/${t.id}/restore`); toast(`Restored to ${r.path}`); bump() } catch (e) { toast((e as Error).message, 'err') } }
  const purge = async (t: TrashItem) => { await api.del(`${V1}/trash/${t.id}`); bump() }
  return (
    <div className="page">
      <PageHeader title="Trash" sub={settings?.trash_days ? `Emptied automatically after ${settings.trash_days} days. Things deleted in mounts skip the trash.` : 'Kept until you empty it.'}
        actions={data?.length ? <button className="btn danger-text" onClick={() => setEmpty(true)}><Icon name="trash" size={15} /> Empty trash</button> : undefined} />
      {loading && !data && <Loading />}
      {data && !data.length && <Empty title="The trash is empty" icon={<Icon name="trash" size={28} />} />}
      {data && data.length > 0 && <div className="file-table trash">
        {data.map(t => (
          <div key={t.id} className="file-row">
            <span className="c-name"><FileGlyph name={t.name} mime="" isDir={t.is_dir} size={20} /><span className="name ellipsis" title={t.orig_path}>{t.name}<small className="muted path-hint"> {t.orig_path.split('/').slice(0, -1).join('/') || '/'}</small></span></span>
            <span className="c-size muted">{bytes(t.size)}</span>
            <span className="c-mod muted" title={stamp(t.deleted_at)}>{ago(t.deleted_at)}</span>
            <span className="row"><button className="btn sm" onClick={() => restore(t)}><Icon name="restore" size={13} /> Restore</button><button className="icon-btn" title="Delete for good" onClick={() => purge(t)}><Icon name="x" size={15} /></button></span>
          </div>
        ))}
      </div>}
      {empty && <Confirm title="Empty the trash?" message="Everything in it is deleted for good." onCancel={() => setEmpty(false)} onConfirm={async () => { await api.del(`${V1}/trash`); setEmpty(false); bump() }} />}
    </div>
  )
}
