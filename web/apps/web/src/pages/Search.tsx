import { useEffect, useRef, useState } from 'react'
import { api, type Title } from '../api'
import { describeError } from '../App'
import { go } from '../router'
import { isTV } from '../tv'
import { Tile, subtitleOf } from '../components/Tile'
import { GridSkeleton } from '../components/Skeleton'
import { useSearchHistory, useTitleSuggest, SuggestRows } from '../components/SearchSuggest'

const PAGE = 30

/**
 * Страница поиска.
 *
 * Список не обрезается на первой странице: найденное подгружается по мере
 * прокрутки — и вниз (сентинел за последним рядом), и кнопкой, когда
 * прокрутить нельзя или не хочется. Кнопка остаётся и рядом с автозагрузкой:
 * она же показывает, сколько ещё не показано, и работает как «прыжок» сразу
 * на страницу. На телевизоре прокрутки в привычном виде нет — там кнопка
 * и есть основной способ.
 */
export function Search({ q }: { q: string }) {
  const [items, setItems] = useState<Title[]>([])
  const [total, setTotal] = useState(0)
  // Где продолжать: не «сколько показано», а ответ сервера о позиции в
  // каталоге — живой поиск добавляет находки сверх окна, и следующий
  // запрос должен продолжать каталог, не перескакивая через них.
  const [next, setNext] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(false)
  const [more, setMore] = useState(false)
  // Номер запроса: ответ, устаревший к моменту прихода (человек успел
  // докрутить до низа и обратно или сменить запрос), не должен затирать
  // более свежее состояние.
  const seq = useRef(0)
  const sentinel = useRef<HTMLDivElement | null>(null)

  const fetchPage = (offset: number, append: boolean) => {
    const mine = ++seq.current
    setMore(true)
    api.search(q, offset)
      .then(r => {
        if (mine !== seq.current) return
        setTotal(r.total ?? 0)
        setHasMore(!!r.hasMore)
        setNext(r.nextOffset ?? offset)
        // Живой поиск кладёт находки в каталог, и следующее окно могло бы
        // вернуть их снова — фильтруем по id.
        setItems(prev => {
          if (!append) return r.items ?? []
          const have = new Set(prev.map(t => t.id))
          return [...prev, ...(r.items ?? []).filter(t => !have.has(t.id))]
        })
      })
      .catch(e => { if (mine === seq.current) setErr(describeError(e)) })
      .finally(() => { if (mine === seq.current) setMore(false) })
  }

  useEffect(() => {
    if (q.trim().length < 2) { setItems([]); setTotal(0); setHasMore(false); return }
    setErr(''); setLoading(true)
    fetchPage(0, false)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q])
  // setLoading гасим по завершении первой загрузки.
  useEffect(() => { if (!more) setLoading(false) }, [more])

  // Автозагрузка при подходе к концу списка: сентинел под последним рядом,
  // 400px запаса, чтобы следующая страница успела приехать до упора.
  useEffect(() => {
    if (q.trim().length < 2 || !hasMore || more) return
    const el = sentinel.current
    if (!el) return
    const io = new IntersectionObserver(es => {
      if (es.some(e => e.isIntersecting)) fetchPage(next, true)
    }, { rootMargin: '400px' })
    io.observe(el)
    return () => io.disconnect()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, hasMore, more, next])

  // На телевизоре строка поиска живёт здесь, а не в шапке: там она ловила
  // фокус мимоходом, и телевизор сразу выкидывал экранную клавиатуру.
  const field = isTV() ? <TvQuery q={q} /> : null

  if (!q) {
    return field ?? <p className="muted">Введите название в строке поиска сверху.</p>
  }
  if (loading && items.length === 0) return <>{field}<GridSkeleton n={6} /></>
  if (err && items.length === 0) return <>{field}<div className="err">{err}</div></>

  return (
    <>
      {field}
      <h1>Поиск: {q}<span className="n">{total}</span></h1>
      {/* Ошибка видна и поверх прошлых результатов: смена запроса могла
          споткнуться, а старые карточки — ещё на экране. */}
      {err && <div className="err" style={{ marginBottom: 12 }}>{err}</div>}
      {items.length === 0 && !loading && !err ? (
        <div className="empty"><p>
          Ничего не нашлось. Каталог наполняется обходом площадки — возможно,
          до этого тайтла ещё не дошло.
        </p></div>
      ) : (
        <>
          <div className="grid">
            {items.map(t => (
              <Tile key={t.id} href={`#/title/${t.id}`} titleId={t.id}
                    name={t.title} hasArt={t.hasArt} sub={subtitleOf(t.kind, t.year)} />
            ))}
          </div>
          {/* Сентинел автозагрузки: div без содержимого, виден только
              IntersectionObserver'у. */}
          <div ref={sentinel} style={{ height: 1 }} />
          <div className="row" style={{ marginTop: 22, justifyContent: 'center' }}>
            {more
              ? <span className="muted">Ищу ещё…</span>
              : hasMore
                ? <button onClick={() => fetchPage(next, true)}>
                    Показать ещё{total > items.length ? ` (осталось ${total - items.length})` : ''}
                  </button>
                : total > PAGE
                  ? <span className="muted">Это всё, что нашлось — {total}</span>
                  : null}
          </div>
        </>
      )}
    </>
  )
}

/**
 * Ввод названия на телевизоре.
 *
 * Клавиатура открывается только по нажатию OK, а не от того, что фокус
 * пробежал мимо поля: экранная клавиатура закрывает пол-экрана, и вылезать
 * сама она не должна. Пока не нажали — на месте поля обычная кнопка.
 *
 * Под полем — недавние запросы: на телевизоре набирать каждый раз мучительно,
 * а история позволяет вернуться к прошлому поиску стрелками пульта. Пока
 * набирают — и подходящие тайтлы: часто хватает трёх букв, чтобы дойти
 * стрелкой до нужного и не набирать название целиком.
 */
function TvQuery({ q }: { q: string }) {
  const [typing, setTyping] = useState(!q)
  const [text, setText] = useState(q)
  const [hi, setHi] = useState(-1)
  const hist = useSearchHistory()
  useEffect(() => { setText(q); setTyping(!q); setHi(-1) }, [q])
  useEffect(() => { hist.refresh() }, [hist.refresh])

  const v = text.trim().toLowerCase()
  const sug = v ? hist.items.filter(h => h.toLowerCase().includes(v)).slice(0, 3) : hist.items.slice(0, 8)
  const titles = useTitleSuggest(typing ? text : '')

  if (!typing) {
    return (
      <button className="tvsearch" data-autofocus onClick={() => setTyping(true)}>
        Искать: {q}
      </button>
    )
  }
  return (
    <>
      <form className="tvsearch" onSubmit={e => {
        e.preventDefault()
        const s = text.trim()
        setTyping(false)
        if (s.length >= 2) { void hist.remember(s); go(`/search/${encodeURIComponent(s)}`) }
      }}>
        <input value={text} onChange={e => { setText(e.target.value); setHi(-1) }} autoFocus
               placeholder="Название или имя…" aria-label="Поиск по каталогу" />
      </form>
      {/* Потоком под полем, а не всплывающим списком: на телевизоре строки
          — обычные кнопки, и пульт ходит по ним стрелками. Крестик удаления
          виден всегда: навести курсор там нечем. */}
      <SuggestRows items={sug} titles={titles} hi={hi} onHover={setHi}
                   onPick={h => { void hist.remember(h); go(`/search/${encodeURIComponent(h)}`) }}
                   onTitle={t => go(`/title/${t.id}`)}
                   onForget={h => void hist.forget(h)}
                   onClear={!v && hist.items.length ? () => void hist.clear() : undefined} />
    </>
  )
}
