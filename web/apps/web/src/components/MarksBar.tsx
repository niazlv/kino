import { useEffect, useState } from 'react'
import { api, type Marks } from '../api'

/**
 * Намеренные отметки о тайтле.
 *
 * Отдельно от позиции просмотра: позиция выводится из того, что человек
 * включил, а «слежу» и «буду смотреть» — его решение. Случайно открытая
 * серия не должна объявлять сериал отслеживаемым.
 *
 * Оценок на платформе нет: десять звёзд в шапке тайтла никому не
 * понадобились — загромождали строку, а на телевизоре ещё и ловили фокус
 * по пути к сериям. Вместе с UI убран и путь оценок в API.
 */
export function MarksBar({ titleId }: { titleId: number }) {
  const [m, setM] = useState<Marks | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setM(null)
    api.marks(titleId).then(setM).catch(() => setM(null))
  }, [titleId])

  async function save(next: Marks) {
    // Показываем сразу, не дожидаясь сервера: отметка — мгновенное действие,
    // и задержка в полсекунды здесь читается как «кнопка не работает».
    setM(next); setBusy(true)
    try { setM(await api.setMarks(titleId, next)) } finally { setBusy(false) }
  }

  if (!m) return null

  return (
    <div className="marks">
      <button className={m.following ? 'on' : ''} disabled={busy}
              onClick={() => save({ ...m, following: !m.following })}>
        {m.following ? '✓ Слежу' : 'Следить'}
      </button>
      <button className={m.watchlist ? 'on' : ''} disabled={busy}
              onClick={() => save({ ...m, watchlist: !m.watchlist })}>
        {m.watchlist ? '✓ Буду смотреть' : 'Буду смотреть'}
      </button>

      {/* Скрыть — единственный способ убрать брошенное с главной, не
          объявляя его просмотренным. Снимается само, если включить снова. */}
      <button className={`ghost${m.hidden ? ' on' : ''}`} disabled={busy}
              title="Не показывать на главной и в списках"
              onClick={() => save({ ...m, hidden: !m.hidden })}>
        {m.hidden ? '✓ Скрыт' : 'Скрыть'}
      </button>
    </div>
  )
}
