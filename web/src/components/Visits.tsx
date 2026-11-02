import type { SendVisit } from '../lib/types'
import { ago, bytes, stamp } from '../lib/format'
import { Icon, type IconName } from './icons'
import { copyText, useToast } from './ui'

let regions: Intl.DisplayNames | null = null
/** "BA" → "Bosnia and Herzegovina" in the reader's language. */
export function countryName(cc: string): string {
  try { regions ??= new Intl.DisplayNames(undefined, { type: 'region' }); return regions.of(cc) || cc } catch { return cc }
}
export const flagOf = (cc: string) => cc.length === 2 ? String.fromCodePoint(...[...cc.toUpperCase()].map(c => 0x1f1e6 + c.charCodeAt(0) - 65)) : ''

const ordinal = (n: number) => n + (n % 100 >= 11 && n % 100 <= 13 ? 'th' : ['th', 'st', 'nd', 'rd'][n % 10] || 'th')

/** "Android 14 · SM-A528B". */
export function deviceOf(v: SendVisit): string {
  const model = v.model && v.model !== 'iPhone' && v.model !== 'iPad' ? v.model : ''
  return [v.os || (v.device ? v.device[0].toUpperCase() + v.device.slice(1) : ''), model].filter(Boolean).join(' · ') || 'Unknown device'
}

/** "Banja Luka, Srpska" (the country goes beside it). */
export function placeOf(v: SendVisit): string {
  return [v.city, v.region && v.region !== v.city ? v.region : ''].filter(Boolean).join(', ')
}

/** Shortens "\"Telekomunikacije Republike Srpske\" akcionarsko drustvo Banja Luka". */
export const networkOf = (n?: string) => (n || '').replace(/"/g, '').replace(/\s+(d\.o\.o\.|akcionarsko drustvo|a\.d\.|LLC|Inc\.?|Ltd\.?|GmbH|S\.A\.|AG)\b.*$/i, '').trim()

const hostOf = (u: string) => { try { return new URL(u).host.replace(/^www\./, '') } catch { return u } }
const deviceIcon: Record<string, IconName> = { phone: 'phone', tablet: 'tablet', computer: 'monitor' }
const baseName = (f: string) => f.split('/').pop() || f

function Chip({ icon, children, title }: { icon?: IconName; children: React.ReactNode; title?: string }) {
  return <span className="visit-chip" title={title}>{icon && <Icon name={icon} size={13} />}{children}</span>
}

/** One open of a link, with everything the server learned about it. */
export function VisitRow({ v, showLink, onLink }: { v: SendVisit; showLink?: boolean; onLink?: (id: string) => void }) {
  const { toast } = useToast()
  const place = placeOf(v)
  const net = networkOf(v.network)
  const note = v.kind === 'note'
  return (
    <article className="visit">
      <div className={`visit-flag ${v.country ? '' : 'none'}`} title={v.country ? countryName(v.country) : 'Place unknown'}>
        {v.country ? flagOf(v.country) : <Icon name="globe" size={20} />}
      </div>
      <div className="visit-body">
        <header className="visit-head">
          <div className="visit-where">
            <b>{place || (v.country ? countryName(v.country) : 'Somewhere unknown')}</b>
            {place && v.country && <span className="muted"> · {countryName(v.country)}</span>}
          </div>
          <span className="visit-when" title={stamp(v.at)}>{ago(v.at)}</span>
        </header>
        <div className="visit-sub small muted">
          {showLink && <><button type="button" className="link-btn" onClick={() => onLink?.(v.send_id)} title="Only this link"><Icon name={note ? 'note' : v.kind === 'live' ? 'folder' : 'link'} size={12} /> {v.label || (note ? 'Note' : 'Link')}</button> · </>}
          <span title="Which download of this link">{ordinal(v.nth)} open</span>
          {v.seen && v.seen > 1 ? <span title="Opens of your links from this address"> · {v.seen}× from this address</span> : null}
        </div>
        <div className="visit-chips">
          <Chip icon={deviceIcon[v.device || ''] || 'globe'} title={v.device}>{deviceOf(v)}</Chip>
          {v.browser && <Chip icon="globe">{v.browser}</Chip>}
          {v.app && <Chip icon="external" title="Opened inside this app's own browser">{v.app} app</Chip>}
          {net && <Chip icon="wifi" title={`${v.network}${v.asn ? ` (AS${v.asn})` : ''}`}>{net}</Chip>}
          {v.tz && <Chip icon="clock" title="Time zone the visitor's browser reports">{v.tz.replace(/_/g, ' ')}</Chip>}
          {v.lang && <Chip title="Browser language">{v.lang}</Chip>}
        </div>
        <div className="visit-took">
          {v.files.length ? <>
            <Icon name={note ? 'note' : 'download'} size={14} />
            <span className="visit-files">{note ? <span className="visit-file">Read the note</span> : <>
              {v.files.slice(0, 6).map(f => <span key={f} className="visit-file" title={f}>{baseName(f)}</span>)}
              {v.files.length > 6 && <span className="muted small">+{v.files.length - 6} more</span>}
            </>}</span>
            {v.bytes > 0 && <span className="muted small nowrap">{bytes(v.bytes)}{v.last_at && v.last_at !== v.at ? ` · last ${ago(v.last_at)}` : ''}</span>}
          </> : <span className="muted small"><Icon name="eye" size={13} /> Opened the page, {note ? 'note not loaded' : 'nothing downloaded'}</span>}
        </div>
        <footer className="visit-foot small">
          <button type="button" className="link-btn mono" title="Copy IP address" onClick={() => copyText(v.ip).then(() => toast('IP copied'))}><Icon name="copy" size={12} /> {v.ip}</button>
          {v.lat || v.lon ? <a className="link-btn" href={`https://www.openstreetmap.org/?mlat=${v.lat}&mlon=${v.lon}#map=10/${v.lat}/${v.lon}`} target="_blank" rel="noreferrer" title="Approximate — the address's registered area"><Icon name="mapPin" size={12} /> Map</a> : null}
          {v.referer && <span className="muted ellipsis" title={v.referer}>via {hostOf(v.referer)}</span>}
          <details className="visit-raw muted"><summary>Details</summary>
            <div className="kv small"><span>Opened</span><b>{stamp(v.at)}</b>
              {v.asn ? <><span>Network</span><b>{v.network} · AS{v.asn}</b></> : null}
              <span>Browser</span><b className="mono">{v.agent || 'none sent'}</b>
              {v.referer && <><span>Came from</span><b className="mono">{v.referer}</b></>}</div>
          </details>
        </footer>
      </div>
    </article>
  )
}
