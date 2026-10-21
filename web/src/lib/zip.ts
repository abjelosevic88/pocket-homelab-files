// A small zip writer (stored, no compression) for "Download all" of
// encrypted sends, which only exist decrypted in the browser.

const table = (() => {
  const t = new Uint32Array(256)
  for (let n = 0; n < 256; n++) {
    let c = n
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
    t[n] = c >>> 0
  }
  return t
})()

export class Crc32 {
  private c = 0xffffffff
  update(b: Uint8Array) {
    let c = this.c
    for (let i = 0; i < b.length; i++) c = table[(c ^ b[i]) & 0xff] ^ (c >>> 8)
    this.c = c
  }
  digest(): number { return (this.c ^ 0xffffffff) >>> 0 }
}

export interface ZipItem { name: string; parts: BlobPart[]; size: number; crc: number; modified?: Date }

function dosTime(d: Date): [number, number] {
  const time = (d.getHours() << 11) | (d.getMinutes() << 5) | Math.floor(d.getSeconds() / 2)
  const date = ((Math.max(1980, d.getFullYear()) - 1980) << 9) | ((d.getMonth() + 1) << 5) | d.getDate()
  return [time, date]
}

/** Builds a zip Blob from finished items (each item's CRC and size known). */
export function buildZip(items: ZipItem[]): Blob {
  const enc = new TextEncoder()
  const out: BlobPart[] = []
  const central: Uint8Array[] = []
  let offset = 0
  for (const it of items) {
    const name = enc.encode(it.name)
    const [time, date] = dosTime(it.modified || new Date())
    const local = new Uint8Array(30 + name.length)
    const v = new DataView(local.buffer)
    v.setUint32(0, 0x04034b50, true)
    v.setUint16(4, 20, true)
    v.setUint16(6, 0x0800, true) // UTF-8 names
    v.setUint16(8, 0, true)
    v.setUint16(10, time, true)
    v.setUint16(12, date, true)
    v.setUint32(14, it.crc, true)
    v.setUint32(18, it.size, true)
    v.setUint32(22, it.size, true)
    v.setUint16(26, name.length, true)
    local.set(name, 30)
    out.push(local, ...it.parts)
    const c = new Uint8Array(46 + name.length)
    const cv = new DataView(c.buffer)
    cv.setUint32(0, 0x02014b50, true)
    cv.setUint16(4, 20, true)
    cv.setUint16(6, 20, true)
    cv.setUint16(8, 0x0800, true)
    cv.setUint16(12, time, true)
    cv.setUint16(14, date, true)
    cv.setUint32(16, it.crc, true)
    cv.setUint32(20, it.size, true)
    cv.setUint32(24, it.size, true)
    cv.setUint16(28, name.length, true)
    cv.setUint32(42, offset, true)
    c.set(name, 46)
    central.push(c)
    offset += local.length + it.size
  }
  const cdSize = central.reduce((s, c) => s + c.length, 0)
  const end = new Uint8Array(22)
  const ev = new DataView(end.buffer)
  ev.setUint32(0, 0x06054b50, true)
  ev.setUint16(8, items.length, true)
  ev.setUint16(10, items.length, true)
  ev.setUint32(12, cdSize, true)
  ev.setUint32(16, offset, true)
  return new Blob([...out, ...central, end] as BlobPart[], { type: 'application/zip' })
}

/** Starts a browser download of a Blob. */
export function saveBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
}
