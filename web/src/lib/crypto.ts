// End-to-end encryption for sends, after SkySend / Firefox Send.
//
// A random 256-bit secret lives only in the link's #fragment (browsers never
// send it to a server). HKDF-SHA256 turns it into three keys: one for file
// contents, one for the metadata (names, sizes, the note's format) and the
// bits of an auth token. The server stores the SHA-256 of that token and
// hands out the ciphertext only to someone who can show it.
//
// With a password, PBKDF2-SHA256 (600 000 rounds, random salt) stretches the
// password and the result is mixed into the secret, so the link alone is not
// enough and the server never learns the password either.
//
// Files are encrypted in records of 64 KiB with AES-256-GCM. Each record's
// nonce is the file's random 96-bit IV with its last 32 bits XORed with the
// record number, and the record says whether it is the last one (additional
// data), so records cannot be reordered, dropped or cut off unnoticed.

export const RECORD = 64 * 1024
export const TAG = 16
const enc = new TextEncoder()
const dec = new TextDecoder()

export function b64url(b: Uint8Array): string {
  let s = ''
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000))
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function unb64url(s: string): Uint8Array {
  s = s.replace(/-/g, '+').replace(/_/g, '/')
  while (s.length % 4) s += '='
  const bin = atob(s)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export function randomBytes(n: number): Uint8Array {
  const b = new Uint8Array(n)
  crypto.getRandomValues(b)
  return b
}

/** Bytes stored for a file of plain size n. */
export function cipherSize(n: number): number {
  const records = Math.max(1, Math.ceil(n / RECORD))
  return n + records * TAG
}

export interface Keys { content: CryptoKey; meta: CryptoKey; auth: string }

export async function deriveKeys(secret: Uint8Array, password?: string, salt?: Uint8Array): Promise<Keys> {
  let ikm = secret
  if (password) {
    if (!salt) throw new Error('missing salt')
    const pw = await crypto.subtle.importKey('raw', enc.encode(password), 'PBKDF2', false, ['deriveBits'])
    const bits = new Uint8Array(await crypto.subtle.deriveBits({ name: 'PBKDF2', hash: 'SHA-256', salt: salt as BufferSource, iterations: 600_000 }, pw, 256))
    ikm = new Uint8Array(secret.length + bits.length)
    ikm.set(secret)
    ikm.set(bits, secret.length)
  }
  const base = await crypto.subtle.importKey('raw', ikm as BufferSource, 'HKDF', false, ['deriveKey', 'deriveBits'])
  const hk = (info: string) => ({ name: 'HKDF', hash: 'SHA-256', salt: enc.encode('pocket-files/v1'), info: enc.encode(info) })
  const content = await crypto.subtle.deriveKey(hk('content'), base, { name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt'])
  const meta = await crypto.subtle.deriveKey(hk('meta'), base, { name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt'])
  const auth = b64url(new Uint8Array(await crypto.subtle.deriveBits(hk('auth'), base, 256)))
  return { content, meta, auth }
}

export async function sha256hex(s: string): Promise<string> {
  const h = new Uint8Array(await crypto.subtle.digest('SHA-256', enc.encode(s)))
  return Array.from(h, b => b.toString(16).padStart(2, '0')).join('')
}

function nonce(iv: Uint8Array, n: number): Uint8Array {
  const out = new Uint8Array(iv)
  const v = new DataView(out.buffer)
  v.setUint32(8, v.getUint32(8) ^ n)
  return out
}

export async function encryptRecord(key: CryptoKey, iv: Uint8Array, n: number, last: boolean, plain: Uint8Array): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce(iv, n) as BufferSource, additionalData: new Uint8Array([last ? 1 : 0]) }, key, plain as BufferSource))
}

export async function decryptRecord(key: CryptoKey, iv: Uint8Array, n: number, last: boolean, data: Uint8Array): Promise<Uint8Array> {
  try {
    return new Uint8Array(await crypto.subtle.decrypt({ name: 'AES-GCM', iv: nonce(iv, n) as BufferSource, additionalData: new Uint8Array([last ? 1 : 0]) }, key, data as BufferSource))
  } catch {
    throw new Error('the file does not decrypt — the link is wrong or the file was damaged')
  }
}

export interface MetaFile { name: string; size: number; type: string; iv: string }
export interface Meta { v: 1; files: MetaFile[]; note?: { format: NoteFormat } ; label?: string }
export type NoteFormat = 'text' | 'markdown' | 'code' | 'secret'

export async function sealMeta(key: CryptoKey, meta: Meta): Promise<string> {
  const iv = randomBytes(12)
  const ct = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: iv as BufferSource }, key, enc.encode(JSON.stringify(meta))))
  const out = new Uint8Array(iv.length + ct.length)
  out.set(iv)
  out.set(ct, iv.length)
  return b64url(out)
}

export async function openMeta(key: CryptoKey, sealed: string): Promise<Meta> {
  const b = unb64url(sealed)
  try {
    const pt = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: b.subarray(0, 12) as BufferSource }, key, b.subarray(12) as BufferSource)
    return JSON.parse(dec.decode(pt))
  } catch {
    throw new Error('wrong password or incomplete link')
  }
}

/**
 * Encrypts a Blob piece by piece; onChunk receives ciphertext of about
 * chunkRecords records at a time (in order), to upload as it comes.
 */
export async function encryptBlob(key: CryptoKey, iv: Uint8Array, blob: Blob, onChunk: (c: Uint8Array) => Promise<void>, chunkRecords = 128, signal?: AbortSignal): Promise<void> {
  const records = Math.max(1, Math.ceil(blob.size / RECORD))
  for (let start = 0; start < records; start += chunkRecords) {
    if (signal?.aborted) throw new Error('cancelled')
    const end = Math.min(records, start + chunkRecords)
    const plain = new Uint8Array(await blob.slice(start * RECORD, Math.min(blob.size, end * RECORD)).arrayBuffer())
    const parts: Uint8Array[] = []
    let total = 0
    for (let n = start; n < end; n++) {
      const off = (n - start) * RECORD
      const ct = await encryptRecord(key, iv, n, n === records - 1, plain.subarray(off, Math.min(plain.length, off + RECORD)))
      parts.push(ct)
      total += ct.length
    }
    const out = new Uint8Array(total)
    let o = 0
    for (const p of parts) { out.set(p, o); o += p.length }
    await onChunk(out)
  }
}

/**
 * Downloads and decrypts one file. onPlain gets plain pieces in order;
 * onProgress the ciphertext bytes read so far.
 */
export async function decryptStream(key: CryptoKey, iv: Uint8Array, res: Response, cipherTotal: number, onPlain: (p: Uint8Array) => Promise<void> | void, onProgress?: (n: number) => void): Promise<void> {
  if (!res.body) throw new Error('no data')
  const reader = res.body.getReader()
  const records = Math.max(1, Math.ceil((cipherTotal) / (RECORD + TAG)))
  let buf = new Uint8Array(0)
  let n = 0
  let read = 0
  const full = RECORD + TAG
  for (;;) {
    const { done, value } = await reader.read()
    if (value) {
      read += value.length
      onProgress?.(read)
      const nb = new Uint8Array(buf.length + value.length)
      nb.set(buf)
      nb.set(value, buf.length)
      buf = nb
      while (buf.length >= full && n < records - 1) {
        await onPlain(await decryptRecord(key, iv, n, false, buf.subarray(0, full)))
        buf = buf.slice(full)
        n++
      }
    }
    if (done) break
  }
  if (n !== records - 1 || buf.length < TAG) throw new Error('the download was cut short')
  await onPlain(await decryptRecord(key, iv, n, true, buf))
}
