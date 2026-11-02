import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { bytes, plural } from '../lib/format'
import type { Send, SendVisit } from '../lib/types'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { countryName, flagOf, VisitRow } from '../components/Visits'
import { Empty, Loading, PageHeader, useAsync, useToast } from '../components/ui'

/** Every open of every link: who, from where, on what, and what they took. */
export default function Downloads() {
  const { version } = useApp()
  const { toast } = useToast()
  const [params, setParams] = useSearchParams()
  const link = params.get('link') || ''
  const links = useAsync(() => api.get<Send[]>(`${V1}/sends`), [version])
  const [list, setList] = useState<SendVisit[] | null>(null)
  const [more, setMore] = useState(false)

  const load = async (before = 0) => {
    try {
      const q = new URLSearchParams({ ...(link ? { link } : {}), ...(before ? { before: String(before) } : {}) })
      const page = await api.get<SendVisit[]>(`${V1}/sends/visits?${q}`)
      setList(l => before && l ? [...l, ...page] : page)
      setMore(page.length === 100)
    } catch (e) { toast((e as Error).message, 'err') }
  }
  useEffect(() => { setList(null); load() }, [link, version]) // eslint-disable-line react-hooks/exhaustive-deps

  const stats = useMemo(() => {
    if (!list?.length) return null
    const ips = new Set(list.map(v => v.ip)).size
    const cc = new Map<string, number>()
    list.forEach(v => v.country && cc.set(v.country, (cc.get(v.country) || 0) + 1))
    const top = [...cc.entries()].sort((a, b) => b[1] - a[1]).slice(0, 4)
    return { ips, top, sent: list.reduce((s, v) => s + v.bytes, 0) }
  }, [list])

  const setLink = (id: string) => setParams(id ? { link: id } : {})
  const current = links.data?.find(s => s.id === link)

  return (
    <div className="page">
      <PageHeader title="Downloads" sub="Who opened your links — when, from where, on what, and which files they took." />
      <div className="row wrap mb-s">
        <Select value={link} searchable className="downloads-filter" label="Link" onChange={setLink}
          options={[{ value: '', label: 'All links' }, ...(links.data || []).filter(s => s.downloads > 0 || s.id === link).map(s => ({ value: s.id, label: s.label || (s.kind === 'note' ? 'Note' : 'Link'), hint: String(s.downloads) }))]} />
        {link && <button type="button" className="btn sm" onClick={() => setLink('')}><Icon name="x" size={13} /> {current?.label || 'This link'}</button>}
      </div>
      {stats && list && <div className="small muted mb-s">
        {plural(list.length, 'open')}{more ? '+' : ''} · {plural(stats.ips, 'IP address', 'IP addresses')} · {bytes(stats.sent)} sent
        {stats.top.length > 0 && <> · {stats.top.map(([c, n]) => `${flagOf(c)} ${countryName(c)} ${n}`).join(', ')}</>}
      </div>}
      {!list ? <Loading /> : !list.length ? <Empty title="No downloads yet" icon={<Icon name="download" size={26} />}>When someone opens one of your links it shows up here with their device, place and the files they took.</Empty>
        : <div className="link-list">{list.map(v => <VisitRow key={v.id} v={v} showLink={!link} onLink={setLink} />)}</div>}
      {more && list && <div className="form-actions"><button type="button" className="btn" onClick={() => load(list[list.length - 1].id)}>Show older</button></div>}
      <p className="small muted mt">Place comes from Cloudflare (country always; city only with its visitor location headers). Device and time zone are what the visitor's browser says, so treat them as hints.</p>
    </div>
  )
}
