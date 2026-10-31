// "Edit in Pocket Office": only offered when the server says Pocket Office is installed and
// connected (both links on in Pocket Core). The answer is shared by every view for a minute.
import { useEffect, useState } from 'react'
import { api, V1 } from './api'
import type { Entry } from './types'

export interface OfficeStatus { available: boolean; reason?: string }

let cached: { at: number; st: OfficeStatus } | null = null
let pending: Promise<OfficeStatus> | null = null

function load(): Promise<OfficeStatus> {
  if (cached && Date.now() - cached.at < 60_000) return Promise.resolve(cached.st)
  pending ??= api.get<OfficeStatus>(`${V1}/office`).catch(() => ({ available: false }))
    .then(st => { cached = { at: Date.now(), st }; pending = null; return st })
  return pending
}

export function useOffice(): OfficeStatus {
  const [st, setSt] = useState<OfficeStatus>(cached?.st || { available: false })
  useEffect(() => { let live = true; load().then(s => { if (live) setSt(s) }); return () => { live = false } }, [])
  return st
}

const OFFICE_EXT = new Set(['docx', 'docm', 'dotx', 'doc', 'odt', 'rtf', 'xlsx', 'xlsm', 'xltx', 'xls', 'ods', 'csv', 'pptx', 'pptm', 'potx', 'ppt', 'odp'])

/** A document Pocket Office edits, in a place it can save to (a two-way drive, not a one-way copy). */
export function isOfficeDoc(e: Entry, space: string, writable = true): boolean {
  if (e.is_dir || (space.startsWith('cloud:') && !writable)) return false
  return OFFICE_EXT.has((e.name.split('.').pop() || '').toLowerCase())
}
