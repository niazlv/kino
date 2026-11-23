import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type Me, type MySession } from '../api'
import { describeError } from '../App'
import { DeviceLink, deviceLabel } from '../components/DeviceLink'
import { DevComputer, DevPhone, DevTv } from '../components/NavIcons'
import { PasswordDialog } from '../components/PasswordDialog'

/**
 * Профиль: кто вошёл, подключение устройств, где вы вошли, пароль и выход.
 *
 * Главное здесь — «Подключить устройство»: телевизор и приложение входят
 * кодом с экрана, и подтвердить его человек должен уметь сам, без
 * администратора. Админка умеет то же для чужого аккаунта — для того, у кого
 * ещё нет ни одного устройства, где он вошёл.
 *
 * Переноса истории с kinopub здесь нет и быть не должно: это сотни запросов
 * к общему аккаунту площадки, его запускает только администратор.
 */
export function Profile({ me }: { me: Me }) {
  const [sessions, setSessions] = useState<MySession[] | null>(null)
  const [sessErr, setSessErr] = useState('')
  const [pw, setPw] = useState(false)
  const timers = useRef<ReturnType<typeof setTimeout>[]>([])

  const load = useCallback(() => {
    api.mySessions()
      .then(r => { setSessions(r.items ?? []); setSessErr('') })
      .catch(e => setSessErr(describeError(e)))
  }, [])
  useEffect(() => {
    load()
    const t = timers.current
    return () => t.forEach(clearTimeout)
  }, [load])

  // Устройство забирает сессию при следующем опросе — через несколько
  // секунд после «Впустить». Список перечитывается дважды, чтобы новый
  // телевизор появился в нём без перезагрузки страницы.
  const approved = () => {
    timers.current.push(setTimeout(load, 6000), setTimeout(load, 15000))
  }

  const since = me.createdAt ? new Date(me.createdAt) : null

  return (
    <div className="profile">
      <section className="phead">
        <span className="avatar xl">{initials(me)}</span>
        <div className="pwho">
          <h1>{me.displayName || me.login}</h1>
          <div className="muted">
            @{me.login}
            {since && !isNaN(since.getTime()) && (
              <> · в kino с {since.toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' })}</>
            )}
          </div>
          {me.role === 'admin' && (
            <div className="chips"><span className="chip accent">администратор</span></div>
          )}
        </div>
      </section>

      {me.local ? (
        <>
          <h2 className="dlhead">Локальный режим</h2>
          <div className="pcard">
            <p className="pnote">
              Всё работает на этом устройстве: просмотр и закладки хранятся здесь,
              без сервера. Пароля и выхода нет, другие устройства сюда не входят.
            </p>
          </div>
        </>
      ) : (
        <>
          <h2 className="dlhead">Подключить устройство</h2>
          <div className="pcard">
            <p className="pnote">
              Телевизор или телефон при входе показывает код из восьми знаков. Введите
              его здесь — устройство войдёт под вашим именем. Можно и просто навести
              на QR с того экрана камеру телефона, где вы уже вошли.
            </p>
            <DeviceLink onApproved={approved} />
          </div>

          <h2 className="dlhead">Где вы вошли</h2>
          <Sessions items={sessions} err={sessErr} reload={load} />

          <h2 className="dlhead">Аккаунт</h2>
          <div className="setlist">
            <div className="setrow static">
              <span className="setmain">
                <span className="setname">Пароль</span>
                <span className="sethint">
                  Нужен для входа с компьютера. Смена пароля выводит из аккаунта все
                  остальные устройства — телевизоры придётся подключить заново.
                </span>
              </span>
              <button className="sm" onClick={() => setPw(true)}>Сменить</button>
            </div>
            <div className="setrow static">
              <span className="setmain">
                <span className="setname">Выйти</span>
                <span className="sethint">Только здесь. Остальные устройства останутся в аккаунте.</span>
              </span>
              <button className="sm" onClick={() => api.logout().then(() => { location.hash = '/'; location.reload() })}>
                Выйти
              </button>
            </div>
          </div>
        </>
      )}
      {pw && <PasswordDialog onClose={() => { setPw(false); load() }} />}
    </div>
  )
}

function Sessions({ items, err, reload }:
  { items: MySession[] | null; err: string; reload: () => void }) {
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState('')

  if (err) return <div className="err">{err}</div>
  if (!items) return <p className="muted small">Загружаю…</p>

  // Это устройство — первым: от него человек отсчитывает остальные.
  const others = items.filter(s => !s.current)
  const rows = [...items.filter(s => s.current), ...others]

  const signOut = async (s: MySession) => {
    setBusy(s.id); setNote('')
    try {
      await api.signOutSession(s.id)
      reload()
    } catch (e) {
      setNote(describeError(e))
    } finally {
      setBusy('')
    }
  }

  const signOutOthers = async () => {
    if (!confirm(`Выйти на ${others.length === 1 ? 'другом устройстве' : `всех остальных устройствах (${others.length})`}? Здесь вы останетесь.`)) return
    setBusy('*'); setNote('')
    try {
      await api.signOutOthers()
      reload()
    } catch (e) {
      setNote(describeError(e))
    } finally {
      setBusy('')
    }
  }

  return (
    <>
      <div className="setlist">
        {rows.map(s => (
          <div key={s.id} className={`setrow static sess${s.current ? ' current' : ''}`}>
            <span className="sessico" aria-hidden>{deviceIcon(s.device)}</span>
            <span className="setmain">
              <span className="setname">
                {deviceLabel(s.device)}
                {s.current && <span className="chip accent">это устройство</span>}
              </span>
              <span className="sethint">
                {s.renewable ? 'по коду · ' : ''}вход {ago(s.issuedAt)}
              </span>
            </span>
            {!s.current && (
              <button className="ghost sm" disabled={busy !== ''} onClick={() => void signOut(s)}>
                {busy === s.id ? 'Выхожу…' : 'Выйти'}
              </button>
            )}
          </div>
        ))}
      </div>
      {note && <div className="err" style={{ marginTop: 8 }}>{note}</div>}
      {others.length > 0 && (
        <div className="row" style={{ marginTop: 10 }}>
          <button className="ghost sm" disabled={busy !== ''} onClick={() => void signOutOthers()}>
            Выйти на всех, кроме этого
          </button>
        </div>
      )}
    </>
  )
}

function deviceIcon(kind: string) {
  if (kind === 'tv') return <DevTv />
  if (kind === 'android' || kind === 'ios') return <DevPhone />
  return <DevComputer />
}

/** «5 мин назад», «вчера», «3 дн назад», дальше — дата. */
function ago(iso: string): string {
  const t = new Date(iso).getTime()
  if (isNaN(t)) return ''
  const m = Math.floor((Date.now() - t) / 60000)
  if (m < 1) return 'только что'
  if (m < 60) return `${m} мин назад`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} ч назад`
  if (h < 48) return 'вчера'
  const d = Math.floor(h / 24)
  if (d < 30) return `${d} дн назад`
  return new Date(t).toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' })
}

/** Две буквы для аватара: по имени из двух слов, иначе начало логина. */
export function initials(me: Me): string {
  const words = (me.displayName || '').trim().split(/\s+/).filter(Boolean)
  if (words.length >= 2) return (words[0]![0]! + words[1]![0]!).toUpperCase()
  const src = me.displayName || me.login
  return [...src].slice(0, 2).join('').toUpperCase()
}
