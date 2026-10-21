// Minimal fetch wrapper for the JSON API.
export class ApiError extends Error {
  status: number
  data: Record<string, unknown>
  constructor(status: number, message: string, data: Record<string, unknown> = {}) {
    super(message)
    this.status = status
    this.data = data
  }
}

async function request<T>(method: string, url: string, body?: unknown, raw = false): Promise<T> {
  const headers: Record<string, string> = { 'X-Pocket-Source': 'web' }
  const init: RequestInit = { method, headers, credentials: 'same-origin' }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }
  const res = await fetch(url, init)
  if (res.status === 401 && !url.includes('/auth/')) {
    window.dispatchEvent(new CustomEvent('pf:unauthorized'))
  }
  if (!res.ok) {
    let msg = res.statusText
    let data: Record<string, unknown> = {}
    try {
      data = await res.json()
      msg = (data.error as string) || msg
    } catch {}
    throw new ApiError(res.status, msg, data)
  }
  if (raw) return (await res.text()) as unknown as T
  if (res.status === 204) return undefined as T
  return res.json()
}

export const api = {
  get: <T>(url: string) => request<T>('GET', url),
  getText: (url: string) => request<string>('GET', url, undefined, true),
  post: <T>(url: string, body?: unknown) => request<T>('POST', url, body),
  put: <T>(url: string, body?: unknown) => request<T>('PUT', url, body),
  del: <T>(url: string) => request<T>('DELETE', url),
}

export const V1 = '/api/v1'

/** POST files as multipart/form-data. */
export async function upload<T>(url: string, field: string, files: File | File[], extra: Record<string, string> = {}): Promise<T> {
  const fd = new FormData()
  for (const f of Array.isArray(files) ? files : [files]) fd.append(field, f, f.name)
  for (const [k, v] of Object.entries(extra)) fd.append(k, v)
  const res = await fetch(url, { method: 'POST', body: fd, credentials: 'same-origin', headers: { 'X-Pocket-Source': 'web' } })
  const j = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, j.error || res.statusText, j)
  return j as T
}

export const qs = (o: Record<string, string | number | boolean | undefined | null>) => {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(o)) if (v !== undefined && v !== null && v !== '' && v !== false) p.set(k, String(v))
  const s = p.toString()
  return s ? '?' + s : ''
}

/** PUT a body with progress (XHR: fetch has no upload progress). */
export function putWithProgress(url: string, body: Blob | ArrayBuffer | Uint8Array, onProgress?: (sent: number) => void, signal?: AbortSignal, headers: Record<string, string> = {}): Promise<Record<string, unknown>> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', url)
    xhr.withCredentials = true
    xhr.setRequestHeader('X-Pocket-Source', 'web')
    for (const [k, v] of Object.entries(headers)) xhr.setRequestHeader(k, v)
    xhr.upload.onprogress = e => onProgress?.(e.loaded)
    xhr.onload = () => {
      let j: Record<string, unknown> = {}
      try { j = JSON.parse(xhr.responseText || '{}') } catch {}
      if (xhr.status >= 200 && xhr.status < 300) resolve(j)
      else reject(new ApiError(xhr.status, (j.error as string) || xhr.statusText || 'upload failed', j))
    }
    xhr.onerror = () => reject(new ApiError(0, 'network error'))
    xhr.onabort = () => reject(new ApiError(0, 'cancelled'))
    signal?.addEventListener('abort', () => xhr.abort())
    xhr.send(body as XMLHttpRequestBodyInit)
  })
}

/** Join a space and path into a browser route. */
export function filesRoute(space: string, path = '/'): string {
  const parts = path.split('/').filter(Boolean).map(encodeURIComponent)
  return `/f/${encodeURIComponent(space)}${parts.length ? '/' + parts.join('/') : ''}`
}

export function rawURL(space: string, path: string, download = false): string {
  return `${V1}/fs/raw${qs({ space, path, dl: download ? 1 : undefined })}`
}

export function thumbURL(space: string, path: string, size = 320, stamp = ''): string {
  return `${V1}/fs/thumb${qs({ space, path, size, v: stamp })}`
}

export function zipURL(space: string, paths: string[], name?: string): string {
  const p = new URLSearchParams({ space })
  for (const x of paths) p.append('path', x)
  if (name) p.set('name', name)
  return `${V1}/fs/zip?${p}`
}
