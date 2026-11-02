import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { bytes, plural } from '../lib/format'

const dayOf = (t: string) => {
  const d = new Date(t), now = new Date()
  const diff = Math.round((new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() - new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()) / 86400000)
  return diff === 0 ? 'Today' : diff === 1 ? 'Yesterday' : d.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long', year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric' })
}
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
    const cc = new Map<string, number>()
    list.forEach(v => v.country && cc.set(v.country, (cc.get(v.country) || 0) + 1))
    return {
      ips: new Set(list.map(v => v.ip)).size,
      took: list.filter(v => v.files.length).length,
      countries: [...cc.entries()].sort((a, b) => b[1] - a[1]),
      sent: list.reduce((s, v) => s + v.bytes, 0),
    }
  }, [list])
  const days = useMemo(() => {
    const out: { day: string; items: SendVisit[] }[] = []
    list?.forEach(v => {
      const day = dayOf(v.at)
      if (out[out.length - 1]?.day !== day) out.push({ day, items: [] })
      out[out.length - 1].items.push(v)
    })
    return out
  }, [list])

  const setLink = (id: string) => setParams(id ? { link: id } : {})

  return (
    <div className="page">
      <PageHeader title="Downloads" sub="Who opened your links — when, from where, on what, and which files they took." />
      <div className="row wrap mb-s">
        <Select value={link} searchable className="downloads-filter" label="Link" onChange={setLink}
          options={[{ value: '', label: 'All links' }, ...(links.data || []).filter(s => s.downloads > 0 || s.id === link).map(s => ({ value: s.id, label: s.label || (s.kind === 'note' ? 'Note' : 'Link'), hint: String(s.downloads) }))]} />
        {link && <button type="button" className="btn sm" onClick={() => setLink('')}><Icon name="x" size={13} /> Show all links</button>}
      </div>
      {stats && list && <div className="visit-stats">
        <div className="visit-stat"><b>{list.length}{more ? '+' : ''}</b><span>{list.length === 1 ? 'open' : 'opens'}</span></div>
        <div className="visit-stat"><b>{stats.ips}</b><span>{stats.ips === 1 ? 'address' : 'addresses'}</span></div>
        <div className="visit-stat"><b>{stats.took}</b><span>took files</span></div>
        <div className="visit-stat"><b>{bytes(stats.sent)}</b><span>sent</span></div>
        {stats.countries.length > 0 && <div className="visit-stat wide"><b className="visit-stat-flags">{stats.countries.slice(0, 8).map(([c, n]) => <span key={c} title={`${countryName(c)}: ${plural(n, 'open')}`}>{flagOf(c)}<small>{n}</small></span>)}</b><span>{plural(stats.countries.length, 'country', 'countries')}</span></div>}
      </div>}
      {!list ? <Loading /> : !list.length ? <Empty title="No downloads yet" icon={<Icon name="download" size={26} />}>When someone opens one of your links it shows up here with their device, place and the files they took.</Empty>
        : days.map(d => <section key={d.day} className="visit-day">
          <h4 className="visit-day-title">{d.day}</h4>
          <div className="link-list">{d.items.map(v => <VisitRow key={v.id} v={v} showLink={!link} onLink={setLink} />)}</div>
        </section>)}
      {more && list && <div className="form-actions"><button type="button" className="btn" onClick={() => load(list[list.length - 1].id)}>Show older</button></div>}
      <p className="small muted mt">Places are approximate: the area an address is registered to, from <a href="https://db-ip.com" target="_blank" rel="noreferrer">IP geolocation by DB-IP</a> and Cloudflare. Device, time zone and language are what the visitor's browser says — hints, not proof.</p>
    </div>
  )
}
