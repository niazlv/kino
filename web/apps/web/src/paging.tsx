import { useCallback, useEffect, useRef, useState } from 'react'
import { describeError } from './App'

/**
 * Ответ сервера одной страницей списка. activeTotal — необязательный
 * счётчик «живой» части ленты (у слежения: то, где есть непросмотренное);
 * сервер отдаёт его, только когда он отличается от total.
 */
export type Page<T> = {
  items: T[] | null
  total: number
  activeTotal?: number
  nextOffset: number
  hasMore: boolean
}

/**
 * Список с подгрузкой — общий каркас для витрин без обрезки.
 *
 * Страницы докручиваются вниз (сентинел под последним рядом, 400px запаса,
 * чтобы следующая успела приехать до упора) и кнопкой — на телевизоре
 * прокрутки в привычном виде нет, там кнопка и есть основной способ.
 * Кнопка остаётся и рядом с автозагрузкой: она же показывает, сколько
 * ещё не показано.
 *
 * Номер запроса отсекает устаревшие ответы: человек успел докрутить до низа
 * и сбросить список (resetKey сменился), а ответ старой страницы пришёл —
 * он не должен затирать свежее состояние. Дедуп по keyOf страхует и другое:
 * живой каталог переставляет карточки, и соседние окна могли бы пересечься.
 *
 * auto=false останавливает автозагрузку, не выключая кнопку: лента «Я смотрю»
 * держит досмотренное свёрнутым, и качать скрытые страницы в этот момент
 * незачем — догрузятся, когда блок развернут.
 */
export function usePagedList<T>(load: (offset: number) => Promise<Page<T>>,
  resetKey: string | boolean | number, keyOf: (x: T) => unknown, auto = true) {
  const [items, setItems] = useState<T[]>([])
  const [total, setTotal] = useState(0)
  const [activeTotal, setActiveTotal] = useState<number | undefined>(undefined)
  const [next, setNext] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)
  const [more, setMore] = useState(false)
  const seq = useRef(0)
  const sentinel = useRef<HTMLDivElement | null>(null)
  // Продолжение в ref, а не в состоянии: два быстрых клика по кнопке
  // не должны успеть увидеть устаревший next до перерисовки.
  const nextRef = useRef(0)
  // load и keyOf передаются стрелкой на каждой отрисовке; в эффекты и
  // зависимости идут ref'ы. Иначе смена идентичности (инлайн-стрелка на
  // каждой отрисовке — норма) перезапускала бы эффект первичной загрузки
  // на КАЖДОЙ отрисовке: запрос, ответ, состояние, новая стрелка — и по
  // кругу, страница висит на скелете, а сеть молотит.
  const loadRef = useRef(load)
  loadRef.current = load
  const keyRef = useRef(keyOf)
  keyRef.current = keyOf

  const fetchPage = useCallback((offset: number) => {
    const mine = ++seq.current
    setMore(true)
    loadRef.current(offset)
      .then(r => {
        if (mine !== seq.current) return
        setTotal(r.total)
        setActiveTotal(r.activeTotal)
        setHasMore(!!r.hasMore)
        setNext(r.nextOffset)
        nextRef.current = r.nextOffset
        setItems(prev => {
          const fresh = r.items ?? []
          if (!prev.length) return fresh
          const have = new Set(prev.map(keyRef.current))
          const add = fresh.filter(x => !have.has(keyRef.current(x)))
          // Страница не добавила ни одной карточки: смещением дальше не
          // проехать (порядок между запросами поехал). Останавливаем
          // автозагрузку — иначе сентинел так и стоял бы на виду, и запросы
          // пошли бы по кругу.
          if (!add.length && offset > 0) setHasMore(false)
          return [...prev, ...add]
        })
      })
      .catch(e => { if (mine === seq.current) setErr(describeError(e)) })
      .finally(() => { if (mine === seq.current) setMore(false) })
  }, [])

  // Догрузка «следующего окна»: одна точка входа для сентинела и кнопки.
  const loadMore = useCallback(() => fetchPage(nextRef.current), [fetchPage])

  // Полный перечит с первой страницы: после импорта истории, возврата
  // скрытого и других действий, изменивших список на сервере.
  const reload = useCallback(() => {
    seq.current++
    setItems([]); setTotal(0); setActiveTotal(undefined)
    setNext(0); setHasMore(false); setErr(''); setLoading(true)
    fetchPage(0)
  }, [fetchPage])

  useEffect(() => {
    // Сброс по ключу: старые ответы отсекает seq, состояние чистим сразу.
    reload()
  }, [resetKey, reload])
  useEffect(() => { if (!more) setLoading(false) }, [more])

  useEffect(() => {
    if (!auto || !hasMore || more) return
    const el = sentinel.current
    if (!el) return
    const io = new IntersectionObserver(es => {
      if (es.some(e => e.isIntersecting)) loadMore()
    }, { rootMargin: '400px' })
    io.observe(el)
    return () => io.disconnect()
  }, [auto, hasMore, more, loadMore])

  return { items, total, activeTotal, hasMore, next, err, loading, more, fetchPage, loadMore, reload, sentinel }
}

/** Подвал списка: кнопка «Показать ещё» — она же показывает, сколько не показано. */
export function MoreRow({ more, hasMore, total, shown, onMore }: {
  more: boolean; hasMore: boolean; total: number; shown: number; onMore: () => void
}) {
  if (more) {
    return <div className="row" style={{ marginTop: 22, justifyContent: 'center' }}>
      <span className="muted">Загружаю ещё…</span>
    </div>
  }
  if (!hasMore) return null
  return (
    <div className="row" style={{ marginTop: 22, justifyContent: 'center' }}>
      <button onClick={onMore}>Показать ещё{total > shown ? ` (осталось ${total - shown})` : ''}</button>
    </div>
  )
}
