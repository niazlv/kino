import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type Title } from '../api'
import { subtitleOf } from './Tile'

/**
 * История поиска — общая для шапки и телевизионного поиска.
 *
 * Хранится на сервере и привязана к пользователю, как прогресс и отметки:
 * история одна на все устройства, а не локальные обрывки в каждом браузере.
 * Все операции сервер отвечает свежим списком — состояние обновляется из
 * ответа, без перечитывания.
 */
export function useSearchHistory() {
  const [items, setItems] = useState<string[]>([])
  const refresh = useCallback(() => {
    api.searchHistory().then(r => setItems(r.items ?? [])).catch(() => { /* нет истории — нет подсказок */ })
  }, [])
  const remember = useCallback(async (q: string) => {
    try { setItems((await api.rememberSearch(q)).items ?? []) } catch { /* подсказка не критична */ }
  }, [])
  const forget = useCallback(async (q: string) => {
    try { setItems((await api.forgetSearch(q)).items ?? []) } catch { /* оставляем как было */ }
  }, [])
  const clear = useCallback(async () => {
    try { setItems((await api.clearSearchHistory()).items ?? []) } catch { /* оставляем как было */ }
  }, [])
  return { items, refresh, remember, forget, clear }
}

// Ответы подсказок на минуту: стирая букву, человек повторяет только что
// заданный запрос, и ждать его второй раз незачем.
const suggestCache = new Map<string, { at: number; items: Title[] }>()
const SUGGEST_TTL = 60_000

/**
 * Тайтлы под строкой поиска, пока человек набирает.
 *
 * Спрашивает после короткой паузы, а не на каждую букву: быстро набранное
 * «дюна» — один запрос, а не четыре. Прошлый ответ остаётся на экране, пока
 * не пришёл новый, — список не мигает между буквами. Короче двух букв
 * подсказок нет: на одну подходит полкаталога.
 */
export function useTitleSuggest(q: string, limit = 6): Title[] {
  const [items, setItems] = useState<Title[]>([])
  const seq = useRef(0)
  const v = q.trim()
  useEffect(() => {
    const mine = ++seq.current
    if ([...v].length < 2) { setItems([]); return }
    const key = `${limit}:${v.toLowerCase()}`
    const hit = suggestCache.get(key)
    if (hit && Date.now() - hit.at < SUGGEST_TTL) { setItems(hit.items); return }
    const timer = setTimeout(() => {
      api.suggest(v, limit)
        .then(r => {
          const got = r.items ?? []
          if (suggestCache.size > 200) suggestCache.clear()
          suggestCache.set(key, { at: Date.now(), items: got })
          if (mine === seq.current) setItems(got)
        })
        .catch(() => { /* подсказка не критична: останется прежняя */ })
    }, 150)
    return () => clearTimeout(timer)
  }, [v, limit])
  return items
}

/** Название и подпись строки-подсказки: русское название крупно, а
 *  оригинальное — в подписи рядом с годом. У kinopub они идут одной
 *  строкой через « / ». */
function titleParts(t: Title): [string, string] {
  const cut = t.title.indexOf(' / ')
  const name = cut > 0 ? t.title.slice(0, cut) : t.title
  const orig = cut > 0 ? t.title.slice(cut + 3) : ''
  const sub = subtitleOf(t.kind, t.year)
  return [name, orig ? `${orig} · ${sub}` : sub]
}

/**
 * Строки подсказок: недавние запросы с часами, крестик удаления появляется
 * при наведении — как в YouTube. Под ними — тайтлы каталога, которые
 * подходят к набранному: щелчок ведёт прямо на страницу тайтла, минуя
 * выдачу. Номер подсветки сквозной: сначала запросы, потом тайтлы.
 *
 * onMouseDown с отменой по умолчанию держит фокус в поле ввода: иначе
 * щелчок по подсказке сначала уводил бы фокус, и список закрывался раньше
 * клика. Кнопки — type="button": в шапке список стоит внутри формы поиска,
 * и крестик по умолчанию отправлял бы её — форма закрывала бы список на
 * каждом удалении.
 */
export function SuggestRows({ items, titles = [], hi, onHover, onPick, onTitle, onForget, onClear }: {
  items: string[]
  titles?: Title[]
  hi: number
  onHover: (i: number) => void
  onPick: (q: string) => void
  onTitle?: (t: Title) => void
  onForget: (q: string) => void
  onClear?: () => void
}) {
  if (!items.length && !titles.length) return null
  return (
    <div className="sugg" onMouseDown={e => e.preventDefault()}>
      {items.map((h, i) => (
        <div className={`srow${i === hi ? ' on' : ''}`} key={h} onMouseEnter={() => onHover(i)}>
          <button type="button" className="q" onClick={() => onPick(h)} title={h}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden>
              <path d="M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM12 7.5V12l3 2" />
            </svg>
            <span>{h}</span>
          </button>
          <button type="button" className="sdel" aria-label={`Удалить из истории: ${h}`}
                  onClick={e => { e.stopPropagation(); onForget(h) }}>✕</button>
        </div>
      ))}
      {titles.length > 0 && items.length > 0 && <div className="ssep" aria-hidden />}
      {titles.map((t, j) => {
        const i = items.length + j
        const [name, sub] = titleParts(t)
        return (
          <div className={`srow stitle${i === hi ? ' on' : ''}`} key={`t${t.id}`} onMouseEnter={() => onHover(i)}>
            <button type="button" className="q" onClick={() => onTitle?.(t)} title={t.title}>
              <i className="sart" aria-hidden>
                {t.hasArt && <img src={`/api/v1/titles/${t.id}/art`} alt="" loading="lazy" decoding="async" />}
              </i>
              <span className="stx">
                <b>{name}</b>
                <small>{sub}</small>
              </span>
            </button>
          </div>
        )
      })}
      {onClear && (
        <div className="srow srowall">
          <button type="button" className="q" onClick={onClear}>Очистить историю</button>
        </div>
      )}
    </div>
  )
}
