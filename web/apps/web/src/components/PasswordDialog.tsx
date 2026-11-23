import { useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { describeError } from '../App'

/** Минимум задаётся сервером; здесь он только для подсказки заранее. */
const MIN_LEN = 10

/**
 * Смена собственного пароля.
 *
 * Старый пароль спрашивается всегда: смена — обычная реакция на «кажется,
 * меня взломали», и без него угнанная сессия позволила бы запереть хозяина.
 * Сервер по той же причине гасит остальные сессии и выдаёт новую.
 */
export function PasswordDialog({ onClose }: { onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null)
  const [oldPassword, setOld] = useState('')
  const [newPassword, setNew] = useState('')
  const [repeat, setRepeat] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [done, setDone] = useState(false)

  useEffect(() => { ref.current?.showModal() }, [])

  // Считаем символы, а не длину строки: на кириллице они не совпадают, и
  // подсказка врала бы ровно там, где пароль слабее всего.
  const len = [...newPassword].length
  const mismatch = repeat !== '' && newPassword !== repeat
  const ok = len >= MIN_LEN && !mismatch && oldPassword !== ''

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!ok) return
    setBusy(true); setErr('')
    try {
      const r = await api.changePassword(oldPassword, newPassword)
      if (r?.reloginRequired) {
        location.reload()
        return
      }
      setDone(true)
    } catch (e) {
      setErr(describeError(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog ref={ref} className="sharedlg" onClose={onClose}>
      <h3>Смена пароля</h3>
      {done ? (
        <>
          <p className="small">Пароль изменён. Остальные сессии завершены.</p>
          <div className="row" style={{ marginTop: 14 }}>
            <button className="primary" onClick={() => ref.current?.close()}>Готово</button>
          </div>
        </>
      ) : (
        <form onSubmit={submit}>
          <p className="muted small">
            Нужен текущий пароль: без него любой, кто добрался до открытой
            сессии, мог бы сменить пароль и закрыть вам вход.
          </p>
          <div className="pwform">
            <label>
              <span className="muted small">Текущий пароль</span>
              <input type="password" autoComplete="current-password"
                     value={oldPassword} onChange={e => setOld(e.target.value)} />
            </label>
            <label>
              <span className="muted small">Новый пароль</span>
              <input type="password" autoComplete="new-password"
                     value={newPassword} onChange={e => setNew(e.target.value)} />
              <span className="muted small">
                {len === 0 ? `не короче ${MIN_LEN} символов`
                  : len < MIN_LEN ? `ещё ${MIN_LEN - len} символов`
                  : 'длина достаточна'}
              </span>
            </label>
            <label>
              <span className="muted small">Ещё раз</span>
              <input type="password" autoComplete="new-password"
                     value={repeat} onChange={e => setRepeat(e.target.value)} />
              {mismatch && <span className="err small">не совпадает</span>}
            </label>
          </div>

          {err && <p className="err small">{err}</p>}

          <div className="row" style={{ marginTop: 14 }}>
            <button className="primary" type="submit" disabled={!ok || busy}>
              {busy ? 'Меняем…' : 'Сменить пароль'}
            </button>
            <button className="ghost" type="button" onClick={() => ref.current?.close()}>
              Отмена
            </button>
          </div>
        </form>
      )}
    </dialog>
  )
}
