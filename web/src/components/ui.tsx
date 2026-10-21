import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'

type Toast = { id: number; msg: string; kind: 'ok' | 'err' }
const ToastCtx = createContext<{ toast: (msg: string, kind?: 'ok' | 'err') => void }>({ toast: () => {} })
export function useToast() { return useContext(ToastCtx) }
export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = useState<Toast[]>([])
  const toast = useCallback((msg: string, kind: 'ok' | 'err' = 'ok') => {
    const id = Date.now() + Math.random()
    setItems(t => [...t, { id, msg, kind }])
    setTimeout(() => setItems(t => t.filter(x => x.id !== id)), kind === 'err' ? 6000 : 2500)
  }, [])
  return (
    <ToastCtx.Provider value={{ toast }}>
      {children}
      <div className="toasts">{items.map(t => <div key={t.id} className={`toast ${t.kind}`}>{t.msg}</div>)}</div>
    </ToastCtx.Provider>
  )
}

export function PageHeader({ title, sub, actions }: { title: React.ReactNode; sub?: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <div className="page-header">
      <div><h1>{title}</h1>{sub && <div className="sub">{sub}</div>}</div>
      {actions && <div className="actions">{actions}</div>}
    </div>
  )
}

export function Card({ title, actions, children, flush, className = '' }: { title?: React.ReactNode; actions?: React.ReactNode; children: React.ReactNode; flush?: boolean; className?: string }) {
  return (
    <section className={`card ${className}`}>
      {(title || actions) && <div className="card-head"><h2>{title}</h2>{actions && <div className="row">{actions}</div>}</div>}
      <div className={`card-body ${flush ? 'flush' : ''}`}>{children}</div>
    </section>
  )
}

/** A labelled form control. Use group for button groups and star inputs: a
 *  <label> would forward clicks on its text to the first button inside. */
export function Field({ label, help, children, className = '', group = false }: { label: React.ReactNode; help?: React.ReactNode; children: React.ReactNode; className?: string; group?: boolean }) {
  const Tag = group ? 'div' : 'label'
  return <Tag className={`field ${className}`}><span className="label">{label}</span>{children}{help && <span className="help">{help}</span>}</Tag>
}

export function Loading() { return <div className="loading"><span className="spinner" /> Loading…</div> }

export function Empty({ title, children, icon }: { title: string; children?: React.ReactNode; icon?: React.ReactNode }) {
  return <div className="empty">{icon && <div className="empty-ico">{icon}</div>}<h3>{title}</h3>{children && <p className="muted">{children}</p>}</div>
}

export function Modal({ title, onClose, children, size = '' }: { title: React.ReactNode; onClose: () => void; children: React.ReactNode; size?: '' | 'lg' | 'xl' }) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [onClose])
  return (
    <div className="modal-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className={`modal ${size}`} role="dialog" aria-modal="true">
        <div className="modal-head"><h2>{title}</h2><button className="btn ghost icon" onClick={onClose} aria-label="Close">✕</button></div>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  )
}

export function Confirm({ title, message, onConfirm, onCancel, danger = true }: { title: string; message: React.ReactNode; onConfirm: () => void; onCancel: () => void; danger?: boolean }) {
  return (
    <Modal title={title} onClose={onCancel}>
      <p>{message}</p>
      <div className="form-actions">
        <button className="btn" onClick={onCancel}>Cancel</button>
        <button className={`btn ${danger ? 'danger' : 'primary'}`} onClick={onConfirm}>Confirm</button>
      </div>
    </Modal>
  )
}

export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: { id: T; label: string }[]; value: T; onChange: (t: T) => void }) {
  return <div className="tabs">{tabs.map(t => <button key={t.id} type="button" className={t.id === value ? 'active' : ''} onClick={() => onChange(t.id)}>{t.label}</button>)}</div>
}

export function Segmented<T extends string | number>({ options, value, onChange, size = '' }: { options: { value: T; label: React.ReactNode; title?: string }[]; value: T | null; onChange: (v: T) => void; size?: '' | 'sm' | 'lg' }) {
  return (
    <div className={`segmented ${size}`} role="radiogroup">
      {options.map(o => <button key={String(o.value)} type="button" role="radio" aria-checked={o.value === value} title={o.title} className={o.value === value ? 'active' : ''} onClick={() => onChange(o.value)}>{o.label}</button>)}
    </div>
  )
}

export function useAsync<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string>('')
  const [loading, setLoading] = useState(true)
  const run = useCallback(() => {
    setLoading(true)
    return fn().then(d => { setData(d); setError('') }).catch(e => setError((e as Error).message)).finally(() => setLoading(false))
  }, deps) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { run() }, [run])
  return { data, setData, error, loading, reload: run }
}

export function useDebounce<T>(value: T, ms = 300): T {
  const [v, setV] = useState(value)
  useEffect(() => { const t = setTimeout(() => setV(value), ms); return () => clearTimeout(t) }, [value, ms])
  return v
}

export function Prompt({ title, value = '', placeholder, button = 'Save', onDone, onClose, children }: { title: string; value?: string; placeholder?: string; button?: string; onDone: (v: string) => void; onClose: () => void; children?: React.ReactNode }) {
  const [v, setV] = useState(value)
  return (
    <Modal title={title} onClose={onClose}>
      <form onSubmit={e => { e.preventDefault(); if (v.trim()) onDone(v.trim()) }} className="grid" style={{ gap: 14 }}>
        <input autoFocus value={v} onChange={e => setV(e.target.value)} placeholder={placeholder} onFocus={e => e.target.select()} />
        {children}
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">{button}</button></div>
      </form>
    </Modal>
  )
}

/** Is a dialog open? Then page shortcuts stay quiet. */
export function modalOpen(): boolean { return !!document.querySelector('.modal-backdrop') }

/** A button with a popover under it; closes on a click outside or Escape. */
export function Dropdown({ label, className = 'btn sm', title, align = 'left', children, open: forced, onOpenChange }: { label: React.ReactNode; className?: string; title?: string; align?: 'left' | 'right'; children: (close: () => void) => React.ReactNode; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  const [open, setOpenRaw] = useState(false)
  const isOpen = forced ?? open
  const setOpen = (o: boolean) => { setOpenRaw(o); onOpenChange?.(o) }
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!isOpen) return
    const h = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false) }
    const k = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); setOpen(false) } }
    document.addEventListener('mousedown', h); document.addEventListener('keydown', k, true)
    return () => { document.removeEventListener('mousedown', h); document.removeEventListener('keydown', k, true) }
  }, [isOpen]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div className="menu-wrap" ref={ref}>
      <button type="button" className={className} title={title} aria-label={title} onClick={e => { e.stopPropagation(); setOpen(!isOpen) }}>{label}</button>
      {isOpen && <div className={`menu pop ${align}`} onClick={e => e.stopPropagation()}>{children(() => setOpen(false))}</div>}
    </div>
  )
}

/** A menu at the pointer (right click / long press). */
export function ContextMenu({ x, y, onClose, children }: { x: number; y: number; onClose: () => void; children: React.ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState({ left: x, top: y })
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const r = el.getBoundingClientRect()
    setPos({ left: Math.max(6, Math.min(x, window.innerWidth - r.width - 6)), top: Math.max(6, Math.min(y, window.innerHeight - r.height - 6)) })
    const k = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', k)
    return () => window.removeEventListener('keydown', k)
  }, [x, y, onClose])
  return <>
    <div className="ctx-scrim" onMouseDown={onClose} onContextMenu={e => { e.preventDefault(); onClose() }} />
    <div ref={ref} className="ctx-menu" style={pos} onClick={onClose}>{children}</div>
  </>
}

/** A dropdown that picks one value (instead of a native select). */
export function Select<T extends string | number>({ value, options, onChange, className = 'btn sm', placeholder = 'Choose…' }: { value: T; options: { value: T; label: React.ReactNode }[]; onChange: (v: T) => void; className?: string; placeholder?: string }) {
  const cur = options.find(o => o.value === value)
  return (
    <Dropdown className={`${className} select-btn`} label={<><span className="grow ellipsis">{cur ? cur.label : placeholder}</span><span className="caret">▾</span></>}>{close => <div className="select-menu">
      {options.map(o => <button key={String(o.value)} type="button" className={o.value === value ? 'sel' : ''} onClick={() => { onChange(o.value); close() }}>{o.label}</button>)}
    </div>}</Dropdown>
  )
}

export function Switch({ checked, onChange, label, help }: { checked: boolean; onChange: (v: boolean) => void; label: React.ReactNode; help?: React.ReactNode }) {
  return (
    <label className="switch-row">
      <span className="grow"><span className="switch-label">{label}</span>{help && <span className="help">{help}</span>}</span>
      <input type="checkbox" className="toggle" checked={checked} onChange={e => onChange(e.target.checked)} />
    </label>
  )
}

/** A read-only value with a copy button. */
export function CopyField({ value, label = 'Copy', mono = true }: { value: string; label?: string; mono?: boolean }) {
  const { toast } = useToast()
  return (
    <div className="copy-field">
      <input readOnly value={value} className={mono ? 'mono' : ''} onFocus={e => e.target.select()} />
      <button type="button" className="btn" onClick={() => copyText(value).then(() => toast('Copied'))}>{label}</button>
    </div>
  )
}

export async function copyText(s: string) {
  try { await navigator.clipboard.writeText(s) } catch {
    const t = document.createElement('textarea'); t.value = s; document.body.appendChild(t); t.select(); document.execCommand('copy'); t.remove()
  }
}

export function Progress({ value, total, label }: { value: number; total: number; label?: React.ReactNode }) {
  const pct = total > 0 ? Math.min(100, Math.round((value / total) * 100)) : 0
  return <div className="progress"><div className="bar lg"><span className="fill" style={{ width: pct + '%' }} /></div>{label !== undefined && <div className="row small muted"><span className="grow">{label}</span><span>{pct}%</span></div>}</div>
}
