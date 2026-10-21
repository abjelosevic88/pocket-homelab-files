import React, { createContext, useCallback, useContext, useRef, useState } from 'react'
import { V1, putWithProgress, qs } from './api'

// The upload queue of the file browser: files (with their folder path when a
// folder was dropped) go up three at a time, each with its own progress.

export interface QueuedFile { file: File; rel: string }
export interface UploadJob {
  id: number; name: string; space: string; dir: string; size: number; sent: number
  state: 'waiting' | 'uploading' | 'done' | 'failed' | 'cancelled'; error?: string; abort?: AbortController
}

interface UploadState {
  jobs: UploadJob[]
  add: (space: string, dir: string, files: QueuedFile[], conflict?: string) => void
  cancel: (id: number) => void
  clear: () => void
  onDone: (fn: () => void) => () => void
}

const Ctx = createContext<UploadState>(null!)
export function useUploads() { return useContext(Ctx) }

const PARALLEL = 3

export function UploadProvider({ children }: { children: React.ReactNode }) {
  const [jobs, setJobs] = useState<UploadJob[]>([])
  const queue = useRef<{ job: UploadJob; file: File; conflict: string }[]>([])
  const running = useRef(0)
  const listeners = useRef(new Set<() => void>())
  const seq = useRef(1)

  const patch = (id: number, p: Partial<UploadJob>) => setJobs(js => js.map(j => j.id === id ? { ...j, ...p } : j))

  const pump = useCallback(() => {
    while (running.current < PARALLEL && queue.current.length) {
      const { job, file, conflict } = queue.current.shift()!
      if (job.state === 'cancelled') continue
      running.current++
      const abort = new AbortController()
      job.abort = abort
      patch(job.id, { state: 'uploading', abort })
      const url = `${V1}/fs/upload${qs({ space: job.space, path: job.dir, name: job.name, conflict, mtime: file.lastModified || undefined })}`
      putWithProgress(url, file, n => patch(job.id, { sent: n }), abort.signal)
        .then(() => { patch(job.id, { state: 'done', sent: job.size }); listeners.current.forEach(f => f()) })
        .catch(e => patch(job.id, { state: abort.signal.aborted ? 'cancelled' : 'failed', error: (e as Error).message }))
        .finally(() => { running.current--; pump() })
    }
  }, [])

  const add = useCallback((space: string, dir: string, files: QueuedFile[], conflict = 'rename') => {
    const fresh = files.map(f => ({ job: { id: seq.current++, name: f.rel, space, dir, size: f.file.size, sent: 0, state: 'waiting' as const }, file: f.file, conflict }))
    setJobs(js => [...js.filter(j => j.state !== 'done' || js.length < 200), ...fresh.map(f => f.job)])
    queue.current.push(...fresh)
    pump()
  }, [pump])

  const cancel = useCallback((id: number) => {
    setJobs(js => js.map(j => {
      if (j.id !== id) return j
      j.abort?.abort()
      const q = queue.current.find(q => q.job.id === id)
      if (q) q.job.state = 'cancelled'
      return { ...j, state: j.state === 'done' ? 'done' : 'cancelled' }
    }))
  }, [])

  const clear = useCallback(() => setJobs(js => js.filter(j => j.state === 'uploading' || j.state === 'waiting')), [])
  const onDone = useCallback((fn: () => void) => { listeners.current.add(fn); return () => { listeners.current.delete(fn) } }, [])

  return <Ctx.Provider value={{ jobs, add, cancel, clear, onDone }}>{children}</Ctx.Provider>
}

/** Reads dropped items, walking into dropped folders. */
export async function filesFromDrop(dt: DataTransfer): Promise<QueuedFile[]> {
  const out: QueuedFile[] = []
  const entries: FileSystemEntry[] = []
  for (const it of Array.from(dt.items || [])) {
    const e = it.kind === 'file' ? it.webkitGetAsEntry?.() : null
    if (e) entries.push(e)
  }
  if (!entries.length) {
    for (const f of Array.from(dt.files || [])) out.push({ file: f, rel: f.name })
    return out
  }
  const walk = async (e: FileSystemEntry, prefix: string): Promise<void> => {
    if (e.isFile) {
      const f = await new Promise<File>((res, rej) => (e as FileSystemFileEntry).file(res, rej))
      out.push({ file: f, rel: prefix + f.name })
    } else if (e.isDirectory) {
      const reader = (e as FileSystemDirectoryEntry).createReader()
      for (;;) {
        const batch = await new Promise<FileSystemEntry[]>((res, rej) => reader.readEntries(res, rej))
        if (!batch.length) break
        for (const c of batch) await walk(c, prefix + e.name + '/')
      }
    }
  }
  for (const e of entries) await walk(e, '')
  return out
}

/** Files from an <input type=file> (with folder paths for webkitdirectory). */
export function filesFromInput(list: FileList | null): QueuedFile[] {
  return Array.from(list || []).map(f => ({ file: f, rel: (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name }))
}
