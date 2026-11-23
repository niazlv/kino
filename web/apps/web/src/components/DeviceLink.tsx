import { useEffect, useRef, useState } from 'react'
import { api, ApiError, type DeviceAuthInfo } from '../api'
import { describeError } from '../App'

/**
 * Подключить устройство по коду: телевизор или телефон показывает код,
 * человек вводит его здесь и пускает устройство в СВОЙ аккаунт.
 *
 * Сначала код проверяется и показывается, кто просит, — потом решение.
 * Код могли продиктовать по телефону или прислать в чате; «Впустить» вслепую
 * отдало бы аккаунт тому, кто его прислал.
 *
 * С кодом из ссылки (QR с экрана устройства) проверка начинается сразу.
 */
export function DeviceLink({ code: fromLink = '', onApproved }:
  { code?: string; onApproved?: () => void }) {
  const [input, setInput] = useState(() => formatCode(fromLink))
  const [code, setCode] = useState(() => (fromLink ? formatCode(fromLink) : ''))
  const [info, setInfo] = useState<DeviceAuthInfo | null>(null)
  const [done, setDone] = useState<'approved' | 'denied' | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const field = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!code) return
    let alive = true
    setInfo(null); setErr(''); setDone(null); setBusy(true)
    api.deviceAuth(code)
      .then(i => { if (alive) setInfo(i) })
      .catch(e => { if (alive) setErr(describeCode(e)) })
      .finally(() => { if (alive) setBusy(false) })
    return () => { alive = false }
  }, [code])

  const ready = input.replace(/[^0-9A-Z]/g, '').length >= 8

  async function decide(approve: boolean) {
    setBusy(true); setErr('')
    try {
      if (approve) await api.approveDevice(code)
      else await api.denyDevice(code)
      setDone(approve ? 'approved' : 'denied')
      if (approve) onApproved?.()
    } catch (e) {
      setErr(describeCode(e))
    } finally {
      setBusy(false)
    }
  }

  // Заново — с пустым полем и фокусом в нём: следующий код другой.
  const again = () => {
    setInput(''); setCode(''); setInfo(null); setErr(''); setDone(null)
    setTimeout(() => field.current?.focus(), 0)
  }

  if (done) {
    return (
      <div className="devlink-box">
        <div className={`devlink-state ${done === 'approved' ? 'ok' : ''}`}>
          {done === 'approved'
            ? <>Готово. <b>{deviceLabel(info?.device ?? '')}</b> войдёт под вашим именем в ближайшие секунды — нажимать там ничего не нужно.</>
            : <>Не пустили. Устройство узнает об этом само.</>}
        </div>
        <button type="button" className="ghost sm" onClick={again}>Подключить ещё одно</button>
      </div>
    )
  }

  if (info) {
    return (
      <div className="devlink-box">
        <div className="devlink-who">
          <span className="devlink-code">{code}</span>
          <span>
            <b>{deviceLabel(info.device)}</b> просит войти в ваш аккаунт. Оно будет
            смотреть от вашего имени и видеть ваш прогресс, пока вы не выйдете
            на нём в «Где вы вошли».
          </span>
        </div>
        <p className="muted small">
          Впускайте, только если этот код сейчас на экране вашего устройства.
          Код действует до {new Date(info.expiresAt).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })}.
        </p>
        {err && <div className="err">{err}</div>}
        <div className="row">
          <button className="primary" disabled={busy} onClick={() => decide(true)}>Впустить</button>
          <button className="ghost" disabled={busy} onClick={() => decide(false)}>Не впускать</button>
        </div>
      </div>
    )
  }

  return (
    <form className="devlink-box" onSubmit={e => {
      e.preventDefault()
      if (!ready) return
      const c = formatCode(input)
      // Тот же код повторно: эффект не перезапустится, проверяем руками.
      if (c === code) { setCode(''); setTimeout(() => setCode(c), 0) } else setCode(c)
    }}>
      <div className="devlink-input">
        <input ref={field} value={input} placeholder="KJ7Q-3MTR" maxLength={12}
               onChange={e => { setInput(formatCode(e.target.value)); setErr('') }}
               autoCapitalize="characters" autoComplete="off" autoCorrect="off" spellCheck={false}
               inputMode="text" aria-label="Код с экрана устройства" />
        <button className="primary" disabled={!ready || busy}>{busy ? 'Проверяю…' : 'Дальше'}</button>
      </div>
      {err && <div className="err">{err}</div>}
    </form>
  )
}

/**
 * Код — как на экране устройства: заглавные, четыре и четыре через дефис.
 * Пробелы, строчные и лишние знаки сервер и так прощает, а человеку
 * так проще сверить введённое с экраном.
 */
export function formatCode(raw: string): string {
  const s = raw.toUpperCase().replace(/[^0-9A-Z]/g, '').slice(0, 8)
  return s.length > 4 ? `${s.slice(0, 4)}-${s.slice(4)}` : s
}

/** Имя устройства для человека. Неизвестное — как прислало само. */
export function deviceLabel(kind: string): string {
  switch (kind) {
    case 'tv': return 'Телевизор'
    case 'android': return 'Телефон на Android'
    case 'ios': return 'iPhone или iPad'
    case 'desktop': return 'Компьютер'
    case 'web': return 'Браузер'
    case '': return 'Устройство'
  }
  return kind
}

/** Коды платформы — в слова: «не найден» и «истёк» для человека разные вещи. */
function describeCode(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.status === 404) return 'Такого кода нет — проверьте, не опечатались ли, или попросите устройство показать новый.'
    if (e.status === 410) return 'Код истёк — устройство покажет новый.'
    if (e.status === 409) return 'На этот код уже ответили.'
  }
  return describeError(e)
}
