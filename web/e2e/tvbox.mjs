// Стенд «настоящий телевизор»: нажатия идут пультом через adb, а состояние
// читается прямо из WebView приложения по отладочному протоколу. Playwright
// сюда не подходит — WebView не умеет управлять контекстами браузера,
// поэтому протокол используется напрямую, благо нужен один вызов.
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
const run = promisify(execFile)
const ADB = process.env.HOME + '/Library/Android/sdk/platform-tools/adb'
const DEV = process.env.KINO_DEV || 'emulator-5556'
const PORT = process.env.KINO_CDP || 9333

export const sleep = ms => new Promise(r => setTimeout(r, ms))

/** Нажатие на пульте. Имена — как у android input keyevent. */
export async function key(name, times = 1) {
  for (let i = 0; i < times; i++) {
    await run(ADB, ['-s', DEV, 'shell', 'input', 'keyevent', name])
    await sleep(220)
  }
}

export async function screenshot(path) {
  await run('/bin/sh', ['-c', `${ADB} -s ${DEV} exec-out screencap -p > ${path}`])
}

export async function open() {
  const list = await (await fetch(`http://localhost:${PORT}/json`)).json()
  const target = list.find(p => p.webSocketDebuggerUrl)
  if (!target) throw new Error('страница витрины не найдена в приложении')
  const ws = new WebSocket(target.webSocketDebuggerUrl)
  await new Promise((ok, bad) => { ws.onopen = ok; ws.onerror = bad })
  let id = 0
  const ждут = new Map()
  ws.onmessage = e => {
    const m = JSON.parse(e.data)
    const p = ждут.get(m.id)
    if (p) { ждут.delete(m.id); m.error ? p.bad(new Error(m.error.message)) : p.ok(m.result) }
  }
  const send = (method, params) => new Promise((ok, bad) => {
    const n = ++id
    ждут.set(n, { ok, bad })
    ws.send(JSON.stringify({ id: n, method, params }))
  })
  return {
    /** Выполнить выражение в витрине и вернуть значение. */
    async $(expr) {
      const r = await send('Runtime.evaluate', {
        expression: `(() => { const v = (${expr}); return JSON.stringify(v === undefined ? null : v) })()`,
        awaitPromise: true, returnByValue: true,
      })
      if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? 'ошибка в витрине')
      return JSON.parse(r.result.value)
    },
    close: () => ws.close(),
  }
}

export const WHO = `(() => {
  const a = document.activeElement
  if (!a || a === document.body) return 'body'
  const t = (a.getAttribute('aria-label') || a.textContent || '').trim().slice(0, 28)
  return a.tagName.toLowerCase() + '.' + String(a.className).split(' ').filter(Boolean).join('.') + ' «' + t + '»'
})()`
