import { useEffect } from 'react'
import { Mark } from '../components/Logo'
import { hasLibrary } from '../native'
import { Downloads } from './Downloads'

/**
 * Сервер не ответил при запуске витрины.
 *
 * Обычно без сети витрина не грузится вовсе, и тогда оболочка показывает
 * свою страницу (www/error.html). Сюда попадают, когда страница пришла, а
 * API — нет: связь оборвалась на полпути или лежит сам core. Вход по коду
 * здесь всё равно не сработает, а скачанное должно быть под рукой — качают
 * как раз на такой случай.
 */
export function Offline() {
  useEffect(() => {
    // Сеть вернулась — пробуем сами, не дожидаясь кнопки.
    const on = () => location.reload()
    addEventListener('online', on)
    return () => removeEventListener('online', on)
  }, [])

  return (
    <div className="offline">
      <div className="auth-card">
        <div className="auth-brand"><Mark />kino</div>
        <h1>Нет связи с kino</h1>
        <p className="muted">Сервер не ответил. Проверьте интернет и попробуйте ещё раз.</p>
        <button className="primary" onClick={() => location.reload()}>Повторить</button>
      </div>
      {hasLibrary() && <div className="offline-list"><Downloads /></div>}
    </div>
  )
}
