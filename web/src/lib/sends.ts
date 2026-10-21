import { ApiError, V1, api, putWithProgress } from './api'
import { b64url, cipherSize, deriveKeys, encryptBlob, randomBytes, sealMeta, sha256hex, unb64url, decryptStream, openMeta, type Meta, type NoteFormat } from './crypto'
import type { PublicSend, Send } from './types'
import { Crc32, buildZip, saveBlob, type ZipItem } from './zip'

export interface SendOptions { expires: string; max_downloads: number; password: string; encrypted: boolean; notify: boolean; label: string }

export interface Item { file: Blob; name: string; type: string }

const CHUNK_RECORDS = 128 // 8 MiB of plaintext per request
const PLAIN_CHUNK = 8 * 1024 * 1024

/** Links remember their key only in this browser (the server never has it). */
const LINKS_KEY = 'pf-links'
export function rememberLink(id: string, url: string) {
  const m = JSON.parse(localStorage.getItem(LINKS_KEY) || '{}')
  m[id] = url
  localStorage.setItem(LINKS_KEY, JSON.stringify(m))
}
export function knownLink(s: Send): string | null {
  if (!s.encrypted) return s.url
  const m = JSON.parse(localStorage.getItem(LINKS_KEY) || '{}')
  return m[s.id] || null
}
export function forgetLink(id: string) {
  const m = JSON.parse(localStorage.getItem(LINKS_KEY) || '{}')
  delete m[id]
  localStorage.setItem(LINKS_KEY, JSON.stringify(m))
}

/**
 * Uploads files (or a note) as a new send. Encrypted sends are encrypted
 * here first; the returned link carries the key after #.
 */
export async function createSend(items: Item[], opt: SendOptions, onProgress: (done: number, total: number) => void, signal?: AbortSignal, note?: NoteFormat): Promise<{ send: Send; link: string }> {
  const kind = note ? 'note' : 'files'
  if (!opt.encrypted) {
    const total = items.reduce((s, i) => s + i.file.size, 0)
    const send = await api.post<Send>(`${V1}/sends`, { kind, encrypted: false, label: opt.label, expires: opt.expires, max_downloads: opt.max_downloads, notify: opt.notify, password: opt.password, files: items.map(i => ({ expected: i.file.size, name: i.name, type: i.type })) })
    let done = 0
    for (const [idx, it] of items.entries()) {
      for (let off = 0; off < it.file.size || (off === 0 && it.file.size === 0); off += PLAIN_CHUNK) {
        const piece = it.file.slice(off, off + PLAIN_CHUNK)
        await putWithProgress(`${V1}/sends/${send.id}/files/${idx}?offset=${off}`, piece, n => onProgress(done + n, total), signal)
        done += piece.size
        onProgress(done, total)
        if (it.file.size === 0) break
      }
    }
    const fin = await api.post<Send>(`${V1}/sends/${send.id}/finish`)
    rememberLink(fin.id, fin.url)
    return { send: fin, link: fin.url }
  }
  const secret = randomBytes(32)
  const salt = opt.password ? randomBytes(16) : undefined
  const keys = await deriveKeys(secret, opt.password || undefined, salt)
  const meta: Meta = { v: 1, files: items.map(i => ({ name: i.name, size: i.file.size, type: i.type, iv: b64url(randomBytes(12)) })) }
  if (note) meta.note = { format: note }
  if (opt.label) meta.label = opt.label
  const sealed = await sealMeta(keys.meta, meta)
  const sizes = items.map(i => cipherSize(i.file.size))
  const total = sizes.reduce((a, b) => a + b, 0)
  const send = await api.post<Send>(`${V1}/sends`, {
    kind, encrypted: true, label: opt.label, meta: sealed, auth_hash: await sha256hex(keys.auth), password_salt: salt ? b64url(salt) : '',
    expires: opt.expires, max_downloads: opt.max_downloads, notify: opt.notify, files: sizes.map(s => ({ expected: s })),
  })
  let done = 0
  for (const [idx, it] of items.entries()) {
    let off = 0
    await encryptBlob(keys.content, unb64url(meta.files[idx].iv), it.file, async chunk => {
      const base = done
      await putWithProgress(`${V1}/sends/${send.id}/files/${idx}?offset=${off}`, chunk, n => onProgress(base + n, total), signal)
      off += chunk.length
      done += chunk.length
      onProgress(done, total)
    }, CHUNK_RECORDS, signal)
  }
  const fin = await api.post<Send>(`${V1}/sends/${send.id}/finish`)
  const link = `${fin.url}#${b64url(secret)}`
  rememberLink(fin.id, link)
  return { send: fin, link }
}

// ---------- recipient side ----------

const PUB = `${V1}/public`

export async function publicSend(id: string): Promise<PublicSend> {
  return api.get<PublicSend>(`${PUB}/sends/${id}`)
}

export interface Opened { send: PublicSend; meta: Meta | null; keys: Awaited<ReturnType<typeof deriveKeys>> | null }

/** Derives the keys from the link (and password), checks the metadata, then counts a download. */
export async function openSend(ps: PublicSend, fragment: string, password: string): Promise<Opened> {
  if (!ps.encrypted) {
    const send = await api.post<PublicSend>(`${PUB}/sends/${ps.id}/open`, { password })
    return { send, meta: null, keys: null }
  }
  if (!fragment) throw new Error('this link is missing its key (the part after #)')
  const keys = await deriveKeys(unb64url(fragment), ps.has_password ? password : undefined, ps.password_salt ? unb64url(ps.password_salt) : undefined)
  const meta = await openMeta(keys.meta, ps.meta || '')
  const send = await api.post<PublicSend>(`${PUB}/sends/${ps.id}/open`, { auth: keys.auth })
  return { send, meta, keys }
}

/** Peeks at the metadata without counting a download (names and sizes). */
export async function peekMeta(ps: PublicSend, fragment: string, password: string): Promise<Meta> {
  const keys = await deriveKeys(unb64url(fragment), ps.has_password ? password : undefined, ps.password_salt ? unb64url(ps.password_salt) : undefined)
  return openMeta(keys.meta, ps.meta || '')
}

export function fileURL(id: string, idx: number, ticket: string, inline = false) {
  return `${PUB}/sends/${id}/files/${idx}?t=${encodeURIComponent(ticket)}${inline ? '&inline=1' : ''}`
}

/** Fetches and decrypts one file into a Blob (and its CRC for zips). */
export async function decryptFile(o: Opened, idx: number, onProgress: (n: number) => void): Promise<{ blob: Blob; crc: number }> {
  const f = o.meta!.files[idx]
  const res = await fetch(fileURL(o.send.id, idx, o.send.ticket!))
  if (!res.ok) throw new Error(res.status === 403 ? 'this download expired — open the link again' : 'download failed')
  const parts: BlobPart[] = []
  const crc = new Crc32()
  await decryptStream(o.keys!.content, unb64url(f.iv), res, o.send.sizes?.[idx] ?? cipherSize(f.size), p => { crc.update(p); parts.push(p as BlobPart) }, onProgress)
  return { blob: new Blob(parts, { type: f.type || 'application/octet-stream' }), crc: crc.digest() }
}

export async function downloadDecrypted(o: Opened, idx: number, onProgress: (n: number) => void) {
  const { blob } = await decryptFile(o, idx, onProgress)
  saveBlob(blob, o.meta!.files[idx].name.split('/').pop() || 'file')
}

export async function downloadAllDecrypted(o: Opened, onProgress: (n: number) => void, zipName = 'files.zip') {
  const items: ZipItem[] = []
  let base = 0
  for (let i = 0; i < o.meta!.files.length; i++) {
    const { blob, crc } = await decryptFile(o, i, n => onProgress(base + n))
    base += o.send.sizes?.[i] ?? 0
    items.push({ name: o.meta!.files[i].name, parts: [blob], size: blob.size, crc })
  }
  saveBlob(buildZip(items), zipName)
}

export async function readNote(o: Opened): Promise<string> {
  if (o.keys) {
    const { blob } = await decryptFile(o, 0, () => {})
    return blob.text()
  }
  const res = await fetch(fileURL(o.send.id, 0, o.send.ticket!, true))
  if (!res.ok) throw new Error('could not read the note')
  return res.text()
}

export const EXPIRY_OPTIONS = [
  { value: '5m', label: '5 minutes' }, { value: '1h', label: '1 hour' }, { value: '1d', label: '1 day' },
  { value: '7d', label: '7 days' }, { value: '30d', label: '30 days' }, { value: 'never', label: 'Never' },
]
export const DOWNLOAD_OPTIONS = [1, 2, 3, 5, 10, 20, 50, 100, 0]
