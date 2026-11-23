import { useEffect, useState } from 'react'
import { api, ApiError, type DeviceBegin, type ServerInfo } from '../api'
import { isTV } from '../tv'
import { hasApp } from '../native'
import { describeError } from '../App'
import { Mark } from '../components/Logo'
import { Qr } from '../components/Qr'

/** Минимум задаётся сервером; здесь он только для подсказки заранее. */
const MIN_LEN = 10

/**
 * Где вход по коду идёт первым: телевизор и приложение.
 *
 * Общее у них — набирать неудобно: пультом по экранной клавиатуре или
 * пальцем по стеклу, причём пароль вслепую. Отсканировать код с чужой
 * камеры быстрее и не заставляет вспоминать пароль. В браузере на
 * компьютере первым остаётся обычный вход: там клавиатура под руками.
 */
const codeFirst = (): boolean => isTV() || hasApp()

/**
 * Вход и регистрация.
 *
 * Как регистрироваться, решает сервер (REGISTRATION, см. визитку
 * /api/v1/server): семейный — только по инвайту (по умолчанию), открытый —
 * без кода, закрытый — никак, людей заводит админка. Код может прийти из
 * ссылки, которую выдала админка, — тогда форма сразу открывается на
 * регистрации с кодом внутри.
 */
export function Login({ onDone, initialInvite = '' }: { onDone: () => void; initialInvite?: string }) {
  // По умолчанию — вход кодом там, где набирать мучительно; обычная форма
  // остаётся запасным ходом и уходит в текстовую кнопку внизу. Ссылка из
  // инвайта важнее: по ней пришли регистрироваться, а не входить.
  const [byCode, setByCode] = useState(() => codeFirst() && !initialInvite)
  const [mode, setMode] = useState<'login' | 'register'>(initialInvite ? 'register' : 'login')
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [invite, setInvite] = useState(initialInvite)
  const [name, setName] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  // Пока визитка не пришла (или сервер старый), считаем как раньше: по инвайту.
  const [server, setServer] = useState<ServerInfo | null>(null)
  useEffect(() => { api.serverInfo().then(setServer).catch(() => {}) }, [])
  const registration = server?.registration ?? 'invite'
  const openReg = registration === 'open'
  // Ссылка с кодом ведёт на регистрацию и на закрытом сервере: код скажет сам.
  const canRegister = registration !== 'closed' || !!initialInvite

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true); setErr('')
    try {
      if (mode === 'register') await api.register(invite.trim(), login.trim(), password, name.trim())
      await api.login(login.trim(), password)
      onDone()
    } catch (e) {
      setErr(describeError(e))
    } finally {
      setBusy(false)
    }
  }

  const len = [...password].length

  if (byCode) return <CodeLogin onDone={onDone} onManual={() => setByCode(false)} />

  return (
    <div className="auth">
      <form className="auth-card" onSubmit={submit}>
        <div className="auth-brand"><Mark />kino</div>
        <h1>{mode === 'login' ? 'Вход' : openReg ? 'Регистрация' : 'Регистрация по приглашению'}</h1>

        {mode === 'register' && initialInvite && (
          <p className="invite-note">
            Код приглашения подставлен из ссылки — осталось придумать логин и пароль.
          </p>
        )}
        {mode === 'register' && (
          <>
            <label>
              {openReg ? 'Код приглашения, если есть' : 'Код приглашения'}
              <input value={invite} onChange={e => setInvite(e.target.value)}
                     autoCapitalize="characters" spellCheck={false} required={!openReg} />
            </label>
            <label>
              Как вас зовут
              <input value={name} onChange={e => setName(e.target.value)} placeholder="имя для шапки" />
            </label>
          </>
        )}
        <label>
          Логин
          <input value={login} autoComplete="username" autoCapitalize="off" spellCheck={false}
                 onChange={e => setLogin(e.target.value)} required autoFocus={!initialInvite} />
        </label>
        <label>
          Пароль
          <input type="password" value={password}
                 autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
                 onChange={e => setPassword(e.target.value)} required />
          {mode === 'register' && (
            <span className="small">
              {len === 0 ? `не короче ${MIN_LEN} символов`
                : len < MIN_LEN ? `ещё ${MIN_LEN - len}` : 'длина достаточна'}
            </span>
          )}
        </label>

        {err && <div className="err">{err}</div>}

        <button className="primary" disabled={busy}>
          {busy ? 'Секунду…' : mode === 'login' ? 'Войти' : 'Зарегистрироваться'}
        </button>
        {(canRegister || mode === 'register') && (
          <button type="button" className="linky switch"
                  onClick={() => { setMode(mode === 'login' ? 'register' : 'login'); setErr('') }}>
            {mode === 'login' ? (openReg ? 'Зарегистрироваться' : 'У меня есть приглашение') : 'Уже есть аккаунт'}
          </button>
        )}
        {codeFirst() && mode === 'login' && (
          <button type="button" className="linky switch" onClick={() => setByCode(true)}>
            Войти по коду
          </button>
        )}
      </form>
    </div>
  )
}

/**
 * Вход по коду. Экран показывает QR и тот же код словами; человек открывает
 * ссылку на устройстве, где уже вошёл, и подтверждает. Страница опрашивает
 * сервер с тем интервалом, который он назначил, и по подтверждению получает
 * cookie сессии — дальше всё как после обычного входа.
 *
 * Код словами и адрес остаются рядом с картинкой намеренно: камеры может не
 * быть под рукой, а подтвердить с чужого ноутбука так же законно.
 */
function CodeLogin({ onDone, onManual }: { onDone: () => void; onManual: () => void }) {
  const [start, setStart] = useState<DeviceBegin | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    let alive = true
    let timer: ReturnType<typeof setTimeout> | null = null
    const begin = async () => {
      setErr('')
      try {
        const s = await api.deviceBegin()
        if (!alive) return
        setStart(s)
        let interval = Math.max(2, s.interval || 5) * 1000
        const poll = async () => {
          if (!alive) return
          try {
            await api.devicePoll(s.deviceCode)
            if (alive) onDone()
            return
          } catch (e) {
            const code = e instanceof ApiError ? e.code : ''
            if (code === 'slow_down') interval += 5000
            else if (code === 'expired_token' || code === 'invalid_grant') { void begin(); return }
            else if (code === 'access_denied') { setErr('Вход отклонён с телефона.'); return }
            // authorization_pending и сетевые сбои: просто спросим ещё раз.
          }
          timer = setTimeout(poll, interval)
        }
        timer = setTimeout(poll, interval)
      } catch (e) {
        if (alive) setErr(describeError(e))
      }
    }
    void begin()
    return () => { alive = false; if (timer) clearTimeout(timer) }
  }, [onDone])

  const url = start?.verificationUrl.replace(/^https?:\/\//, '') ?? ''
  const short = url.replace(/\/#\/link\/.*$/, '')

  return (
    <div className="auth">
      <div className="auth-card codelogin">
        <div className="auth-brand"><Mark />kino</div>
        <h1>Вход по коду</h1>
        <p className="muted">
          Наведите камеру устройства, на котором уже вошли, и подтвердите вход.
        </p>
        <div className="qrbox">
          {start ? <Qr text={start.verificationUrl} /> : <div className="qrwait" aria-hidden />}
        </div>
        {/* Без камеры код вводят в профиле: на главной поля для него нет,
            и «откройте сайт и введите код» оставляло гадать, куда. */}
        <p className="muted small">
          Без камеры: на <b>{short || 'kino'}</b> откройте профиль (имя вверху справа),
          «Подключить устройство» — и введите код
        </p>
        <div className="code" aria-live="polite">{start ? start.userCode : '· · · ·'}</div>
        {err ? <div className="err">{err}</div> : <p className="muted small">Ждём подтверждения…</p>}
        <button type="button" className="linky switch" onClick={onManual} data-autofocus>
          Или войти по логину и паролю
        </button>
      </div>
    </div>
  )
}
