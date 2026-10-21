/** RFC 3339 → "just now", "5 min ago", "3 h ago", "Yesterday", "Mon", "14 Mar", "14 Mar 2025". */
export function ago(t: string | number): string {
  if (!t) return ''
  const ms = typeof t === 'number' ? t * 1000 : new Date(t).getTime()
  if (isNaN(ms)) return ''
  const s = Math.max(0, (Date.now() - ms) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)} min ago`
  const d = new Date(ms), now = new Date()
  const days = Math.round((new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() - new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()) / 86400000)
  if (days === 0) return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  if (days === 1) return 'Yesterday'
  if (days < 7) return d.toLocaleDateString(undefined, { weekday: 'long' })
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric' })
}

/** Full date and time for a tooltip. */
export function stamp(t: string | number): string {
  if (!t) return ''
  const d = typeof t === 'number' ? new Date(t * 1000) : new Date(t)
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

/** Local time of day of a timestamp, "9:12". */
export function clock(t: string): string {
  const d = new Date(t)
  return isNaN(d.getTime()) ? '' : d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })
}

export function bytes(n: number): string {
  if (!n) return '0 B'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1048576).toFixed(1)} MB`
  return `${(n / 1073741824).toFixed(2)} GB`
}

export function initials(name: string): string {
  const parts = name.trim().split(/[\s@._-]+/).filter(Boolean)
  if (parts.length === 0) return '?'
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

export const PALETTE = ['#2563eb', '#7c3aed', '#0891b2', '#059669', '#d97706', '#dc2626', '#db2777', '#4b5563', '#b45309', '#0d9488']
export function colorFor(id: number): string { return PALETTE[Math.abs(id) % PALETTE.length] }

/** A stable colour for a tag name. */
export function tint(name: string): string {
  let h = 0
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) | 0
  return PALETTE[Math.abs(h) % PALETTE.length]
}

/** Snippets mark the words found with [[[ ]]]; split them for highlighting. */
export function snippetParts(s: string): { text: string; hit: boolean }[] {
  const out: { text: string; hit: boolean }[] = []
  const re = /\[\[\[(.*?)\]\]\]|<b>(.*?)<\/b>|<mark>(.*?)<\/mark>/g
  let last = 0, m: RegExpExecArray | null
  while ((m = re.exec(s))) {
    if (m.index > last) out.push({ text: s.slice(last, m.index), hit: false })
    out.push({ text: m[1] ?? m[2] ?? m[3] ?? '', hit: true })
    last = m.index + m[0].length
  }
  if (last < s.length) out.push({ text: s.slice(last), hit: false })
  return out
}

/** 0 none, 1-4 high, 5 medium, 6-9 low. */
export function priorityLevel(p: number): 'none' | 'high' | 'medium' | 'low' {
  if (!p) return 'none'
  if (p <= 4) return 'high'
  if (p === 5) return 'medium'
  return 'low'
}
export const PRIORITY_MARK: Record<string, string> = { high: '!!!', medium: '!!', low: '!', none: '' }
export const PRIORITY_VALUE: Record<string, number> = { none: 0, high: 1, medium: 5, low: 9 }

export function plural(n: number, one: string, many = one + 's'): string { return `${n} ${n === 1 ? one : many}` }
