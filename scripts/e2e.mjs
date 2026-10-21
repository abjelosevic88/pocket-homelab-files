// Drives the real UI in headless Chrome over CDP (no npm dependencies) and
// captures the README screenshots: browse, upload, an encrypted send with a
// password opened as a stranger, a live link, a mount, an upload link.
// Setup: docs/screenshots/README.md.
import fs from 'node:fs/promises'
const base = process.env.BASE || 'http://127.0.0.1:18198'
const pub = process.env.PUBLIC || 'http://127.0.0.1:18199'
const chrome = process.env.CHROME || 'http://127.0.0.1:19228'
const shots = process.env.OUT || new URL('../docs/screenshots/', import.meta.url).pathname
await fs.mkdir(shots, { recursive: true })
const version = await (await fetch(`${chrome}/json/version`)).json()
const ws = new WebSocket(version.webSocketDebuggerUrl)
await new Promise((r, j) => { ws.onopen = r; ws.onerror = j })
let id = 0; const pending = new Map(); const events = []
ws.onmessage = e => { const m = JSON.parse(e.data); if (m.id) pending.get(m.id)?.(m); else events.push(m) }
const send = (method, params = {}, sessionId) => new Promise((res, rej) => {
  const i = ++id; const t = setTimeout(() => { pending.delete(i); rej(new Error('timeout ' + method)) }, 30000)
  pending.set(i, m => { clearTimeout(t); pending.delete(i); m.error ? rej(new Error(method + ' ' + JSON.stringify(m.error))) : res(m.result) })
  ws.send(JSON.stringify({ id: i, method, params, sessionId }))
})
const { targetId } = await send('Target.createTarget', { url: 'about:blank' })
const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true })
const s = (m, p) => send(m, p, sessionId)
await s('Page.enable'); await s('Runtime.enable'); await s('DOM.enable')
await s('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: '/data/dl' })
const errors = []
const ev = async expr => { const r = await s('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 400)); return r.result.value }
const waitFor = async (expr, ms = 20000) => { const d = Date.now() + ms; while (Date.now() < d) { try { if (await ev(`Boolean(${expr})`)) return } catch {} await new Promise(r => setTimeout(r, 150)) } throw new Error('not ready: ' + expr) }
const go = async url => { await s('Page.navigate', { url }); await new Promise(r => setTimeout(r, 700)) }
const shot = async (name, w = 1280, h = 860, mobile = false) => {
  await s('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: mobile ? 2 : 1, mobile })
  await new Promise(r => setTimeout(r, 500))
  const { data } = await s('Page.captureScreenshot', { format: 'png' })
  await fs.writeFile(shots + name + '.png', Buffer.from(data, 'base64'))
}
const click = async sel => ev(`(() => { const el = ${sel}; if (!el) throw new Error('no element'); el.click(); return true })()`)
const byText = (tag, text) => `[...document.querySelectorAll('${tag}')].find(b => b.textContent.trim().includes(${JSON.stringify(text)}))`
const setFiles = async (selector, files) => {
  const { root } = await s('DOM.getDocument', { depth: -1 })
  const { nodeId } = await s('DOM.querySelector', { nodeId: root.nodeId, selector })
  await s('DOM.setFileInputFiles', { nodeId, files })
}
const sessionLog = () => events.filter(e => e.method === 'Runtime.exceptionThrown' || (e.method === 'Runtime.consoleAPICalled' && e.params.type === 'error')).map(e => JSON.stringify(e.params).slice(0, 300))

// 1. browse home
await s('Emulation.setDeviceMetricsOverride', { width: 1280, height: 860, deviceScaleFactor: 1, mobile: false })
await go(base + '/f/home')
await waitFor(`document.querySelector('.file-row:not(.head)')`)
await shot('home')
// 2. upload files into Documents via the hidden input
await go(base + '/f/home/Documents')
await waitFor(`document.querySelector('.file-row:not(.head)')`)
await setFiles('input[type=file][multiple]', ['/data/in/hello.txt', '/data/in/random.bin'])
await waitFor(`[...document.querySelectorAll('.file-row .name')].some(n => n.textContent.startsWith('random.bin'))`)
console.log('upload ok')
await shot('documents')
// 3. preview a text file
await click(`[...document.querySelectorAll('.file-row .name')].find(n => n.textContent.startsWith('Trip to Kotor'))`)
await waitFor(`document.querySelector('.viewer-text pre')`)
await shot('preview-text')
await ev(`document.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape'})); window.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape'}))`)
// 4. encrypted send from the Send page
await go(base + '/send')
await waitFor(`document.querySelector('.send-drop')`)
await setFiles('.send-card input[type=file][multiple]', ['/data/in/random.bin', '/data/in/hello.txt'])
await waitFor(`document.querySelectorAll('.send-items li').length >= 2`)
await ev(`(() => { const i = document.querySelector('.pw-field input'); const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; set.call(i, 'tajna123'); i.dispatchEvent(new Event('input', { bubbles: true })); return true })()`)
await shot('send')
await click(byText('button', 'Create link'))
await waitFor(`document.querySelector('.modal .copy-field input')`, 60000)
const link = await ev(`document.querySelector('.modal .copy-field input').value`)
console.log('encrypted link', link)
await shot('send-done')
// 5. open it as a stranger on the public listener
await go(link.replace(base, pub))
await waitFor(`document.querySelector('.pub-pw input')`)
await shot('public-locked', 900, 760)
await ev(`(() => { const i = document.querySelector('.pub-pw input'); const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; set.call(i, 'tajna123'); i.dispatchEvent(new Event('input', { bubbles: true })); return true })()`)
await click(byText('button', 'Unlock'))
await waitFor(`document.querySelector('.pub-files') && ${byText('button', 'Download all')}`, 60000)
await shot('public-open', 900, 760)
await click(`document.querySelector('.pub-files li button')`)
await new Promise(r => setTimeout(r, 2500))
// 6. live link with password from the browser (Photos folder)
await go(base + '/f/home')
await waitFor(`document.querySelector('.file-row:not(.head)')`)
console.log('errors so far', sessionLog())
// 7. mounts settings page + mobile
await go(base + '/settings?tab=mounts')
await waitFor(`document.querySelector('.settings .btn.primary')`)
await shot('settings-mounts')
await go(base + '/f/home')
await waitFor(`document.querySelector('.file-row:not(.head)')`)
await shot('mobile-home', 390, 844, true)
await go(base + '/send')
await waitFor(`document.querySelector('.send-drop')`)
await shot('mobile-send', 390, 844, true)
// ---- sharing, mounts, upload links ----
await s('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: mobile ? 2 : 1, mobile })
  await new Promise(r => setTimeout(r, 500))
  const { data } = await s('Page.captureScreenshot', { format: 'png' })
  await fs.writeFile(shots + name + '.png', Buffer.from(data, 'base64'))
}
const click = async sel => ev(`(() => { const el = ${sel}; if (!el) throw new Error('no element'); el.click(); return true })()`)
const byText = (tag, text) => `[...document.querySelectorAll('${tag}')].find(b => b.textContent.trim().includes(${JSON.stringify(text)}))`
const setFiles = async (selector, files) => {
  const { root } = await s('DOM.getDocument', { depth: -1 })
  const { nodeId } = await s('DOM.querySelector', { nodeId: root.nodeId, selector })
  await s('DOM.setFileInputFiles', { nodeId, files })
}
const sessionLog = () => events.filter(e => e.method === 'Runtime.exceptionThrown' || (e.method === 'Runtime.consoleAPICalled' && e.params.type === 'error')).map(e => JSON.stringify(e.params).slice(0, 300))

await s('Emulation.setDeviceMetricsOverride', { width: 1280, height: 860, deviceScaleFactor: 1, mobile: false })
await go(base + '/f/home')
await waitFor(`document.querySelector('.file-row:not(.head)')`)
await shot('home-mount')
// share dialog -> live link for the Photos folder
await ev(`(() => { const row = [...document.querySelectorAll('.file-row')].find(r => r.textContent.includes('Projects')); row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 500, clientY: 300 })); return true })()`)
await waitFor(`document.querySelector('.ctx-menu')`)
await shot('context-menu')
await click(byText('.ctx-menu button', 'Get a link'))
await waitFor(`document.querySelector('.modal .send-options')`)
await shot('share-link')
await click(byText('.modal button', 'Create link'))
await waitFor(`document.querySelector('.modal .copy-field input')`)
const live = await ev(`document.querySelector('.modal .copy-field input').value`)
console.log('live link', live)
await click(byText('.modal button', 'Done'))
await go(live)
await waitFor(`document.querySelector('.pub-main')`)
await click(`document.querySelector('.pub-main')`)
await waitFor(`document.querySelector('.pub-files button.link-btn')`)
await shot('public-live', 900, 760)
// mount listing and grid view with thumbnail
await go(base + '/f/mount%3A1')
await waitFor(`document.querySelector('.file-row:not(.head)')`)
await click(`document.querySelector('.toolbar .icon-only')`)
await click(byText('.menu button', 'Grid'))
await waitFor(`document.querySelector('.file-grid')`)
await new Promise(r => setTimeout(r, 800))
await shot('mount-grid')
await click(`document.querySelector('.toolbar .icon-only')`)
await click(byText('.menu button', 'List'))
// share with people dialog (home folder)
await go(base + '/f/home')
await waitFor(`document.querySelector('.file-row:not(.head)')`)
await ev(`(() => { const row = [...document.querySelectorAll('.file-row')].find(r => r.textContent.includes('Photos')); row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 500, clientY: 300 })); return true })()`)
await click(byText('.ctx-menu button', 'Share with people'))
await waitFor(`document.querySelector('.modal .member-list')`)
await shot('share-people')
await ev(`document.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape'})); window.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape'}))`)
// upload link
await go(base + '/requests')
await click(byText('button', 'New upload link'))
await waitFor(`document.querySelector('.modal .folder-btn')`)
await shot('request-new')
await click(byText('.modal button', 'Create link'))
await waitFor(`document.querySelector('.modal .copy-field input')`)
const rq = await ev(`document.querySelector('.modal .copy-field input').value`)
console.log('request link', rq)
await go(rq.replace(base, pub))
await waitFor(`document.querySelector('.send-drop')`)
await setFiles('input[type=file]', ['/data/in/hello.txt'])
await waitFor(`document.querySelector('.callout.success')`)
await shot('public-request', 900, 760)
await go(base + '/send')
await waitFor(`document.querySelectorAll('.link-card').length >= 2`)
await shot('links')
console.log('final errors', sessionLog())
await send('Target.closeTarget', { targetId })
ws.close()
