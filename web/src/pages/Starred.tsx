import { Link } from 'react-router-dom'
import { api, filesRoute, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { bytes } from '../lib/format'
import type { Star } from '../lib/types'
import { FileGlyph, Icon } from '../components/icons'
import { Empty, Loading, PageHeader, useAsync } from '../components/ui'

export default function Starred() {
  const { version, bump } = useApp()
  const { data, loading } = useAsync(() => api.get<Star[]>(`${V1}/stars`), [version])
  const unstar = async (s: Star) => { await api.put(`${V1}/stars`, { space: s.space, path: s.path, on: false }); bump() }
  const to = (s: Star) => s.is_dir ? filesRoute(s.space, s.path) : filesRoute(s.space, s.path.split('/').slice(0, -1).join('/') || '/') + '?open=' + encodeURIComponent(s.path.split('/').pop() || '')
  return (
    <div className="page">
      <PageHeader title="Starred" sub="Folders and files you keep close." />
      {loading && !data && <Loading />}
      {data && !data.length && <Empty title="No stars yet" icon={<Icon name="star" size={28} />}>Star a folder or a file from its menu and it shows up here.</Empty>}
      <div className="link-list">
        {data?.map(s => (
          <div key={s.space + s.path} className={`link-card ${s.missing ? 'off' : ''}`}>
            <div className="link-ico plain">{s.entry ? <FileGlyph name={s.entry.name} mime={s.entry.mime} isDir={s.entry.is_dir} size={24} /> : <Icon name="file" size={20} />}</div>
            <div className="grow">{s.missing ? <b>{s.path.split('/').pop()}</b> : <Link to={to(s)}><b>{s.entry?.name || s.path}</b></Link>}
              <div className="small muted">{s.missing ? 'no longer there' : `${s.space_name}${s.path.split('/').slice(0, -1).join('/') || ''}${s.entry && !s.entry.is_dir ? ' · ' + bytes(s.entry.size) : ''}`}</div></div>
            <button type="button" className="icon-btn" title="Remove star" onClick={() => unstar(s)}><Icon name="star" size={16} fill className="star-on" /></button>
          </div>
        ))}
      </div>
    </div>
  )
}
