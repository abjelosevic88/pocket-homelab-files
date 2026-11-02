import type { SendVisit } from '../lib/types'
import { ago, bytes, stamp } from '../lib/format'
import { Icon } from './icons'
import { copyText, useToast } from './ui'

let regions: Intl.DisplayNames | null = null
/** "BA" → "Bosnia and Herzegovina" in the reader's language. */
export function countryName(cc: string): string {
  try { regions ??= new Intl.DisplayNames(undefined, { type: 'region' }); return regions.of(cc) || cc } catch { return cc }
}
export const flagOf = (cc: string) => cc.length === 2 ? String.fromCodePoint(...[...cc.toUpperCase()].map(c => 0x1f1e6 + c.charCodeAt(0) - 65)) : ''

const ordinal = (n: number) => n + (n % 100 >= 11 && n % 100 <= 13 ? 'th' : ['th', 'st', 'nd', 'rd'][n % 10] || 'th')

/** "Chrome 156 on Android 14 phone (SM-A528B)". */
export function deviceOf(v: SendVisit): string {
  let dev = [v.os, v.device].filter(Boolean).join(' ')
  if (v.model && v.model !== 'iPhone' && v.model !== 'iPad') dev += ` (${v.model})`
  if (v.browser && !v.app) return dev ? `${v.browser} on ${dev}` : v.browser
  return dev || 'Unknown device'
}

export function placeOf(v: SendVisit): string {
  if (!v.country) return ''
  return [countryName(v.country), v.region && v.region !== v.city ? v.region : '', v.city].filter(Boolean).reverse().join(', ')
}

const hostOf = (u: string) => { try { return new URL(u).host.replace(/^www\./, '') } catch { return u } }

/** One open of a link, with everything the server learned about it. */
export function VisitRow({ v, showLink, onLink }: { v: SendVisit; showLink?: boolean; onLink?: (id: string) => void }) {
  const { toast } = useToast()
  const place = placeOf(v)
  const took = v.kind === 'note' ? (v.files.length ? 'Read the note' : 'Opened, note not loaded')
    : v.files.length ? `Took ${v.files.length === 1 ? v.files[0] : `${v.files.length} files`}` : 'Opened, nothing downloaded yet'
  return (
    <div className="visit">
      <div className="visit-ico" title={v.device || ''}>{v.country ? <span className="visit-flag">{flagOf(v.country)}</span> : <Icon name="globe" size={17} />}</div>
      <div className="grow" style={{ minWidth: 0 }}>
        <div className="row wrap visit-head">
          {showLink && <button type="button" className="link-btn ellipsis" onClick={() => onLink?.(v.send_id)} title="Only this link"><b>{v.label || (v.kind === 'note' ? 'Note' : 'Link')}</b></button>}
          <span className="badge" title="Which download of this link">{ordinal(v.nth)}</span>
          {v.app && <span className="badge" title="Opened inside an app's browser">{v.app}</span>}
          <span className="small muted" title={stamp(v.at)}>{ago(v.at)}</span>
        </div>
        <div className="small">{deviceOf(v)}</div>
        <div className="small muted">
          {place && <>{place} · </>}
          {v.tz && <span title="The visitor's time zone, as their browser reports it">{v.tz} · </span>}
          {v.lang && <span title="Browser language">{v.lang} · </span>}
          <button type="button" className="link-btn mono" title="Copy IP address" onClick={() => copyText(v.ip).then(() => toast('IP copied'))}>{v.ip}</button>
        </div>
        <div className="small">
          <span title={v.files.join('\n')}>{took}</span>
          {v.bytes > 0 && <span className="muted"> · {bytes(v.bytes)} sent{v.last_at && v.last_at !== v.at ? ` · last ${ago(v.last_at)}` : ''}</span>}
        </div>
        {v.files.length > 1 && <ul className="visit-files small muted">{v.files.slice(0, 8).map(f => <li key={f} className="ellipsis">{f}</li>)}{v.files.length > 8 && <li>…and {v.files.length - 8} more</li>}</ul>}
        {v.referer && <div className="small muted ellipsis" title={v.referer}>Came from {hostOf(v.referer)}</div>}
        <details className="visit-raw small muted"><summary>Browser details</summary><span className="mono">{v.agent || 'none sent'}</span><br />Opened {stamp(v.at)}</details>
      </div>
    </div>
  )
}
