import { useEffect, useMemo, useRef, useState } from 'react'
import { api, type HistoryItem } from '../api'
import { describeError } from '../App'
import { CardsSkeleton } from '../components/Skeleton'
import { Pager } from '../components/Pager'
import { go, historyPath, replace, titlePath } from '../router'

const KINDS = [
  { id: '', label: 'Всё' },
  { id: 'movie', label: 'Фильмы' },
  { id: 'series', label: 'Сериалы' },
] as const

/**
 * История просмотров — личная и никому больше не видная.
 *
 * Кадры серий сеткой, сгруппированные по дням: так история читается как
 * дневник — «в среду смотрел это», а не как таблица. Страницы по номеру,
 * номер живёт в адресе: «назад» возвращает на ту же страницу, а ссылку на
 * десятую можно открыть заново. Вид и поиск — в состоянии страницы: они
 * уточняют, что смотреть, а не где человек находится.
 */
export function History({ page }: { page: number }) {
  const [items, setItems] = useState<HistoryItem[] | null>(null)
  const [pages, setPages] = useState(0)
  const [total, setTotal] = useState(0)
  const [kind, setKind] = useState<string>('')
  const [q, setQ] = useState('')
  const [query, setQuery] = useState('')
  const [err, setErr] = useState('')
  // Последнее убранное — чтобы вернуть, если рука дрогнула.
  const [removed, setRemoved] = useState<HistoryItem | null>(null)

  // Поиск применяется с задержкой: запрос на каждую букву — это шквал
  // обращений к базе ради того, что человек ещё не дописал.
  useEffect(() => {
    const t = setTimeout(() => setQuery(q.trim()), 350)
    return () => clearTimeout(t)
  }, [q])

  // Номер запроса: ответ, устаревший к моменту прихода (человек успел
  // сменить фильтр или убрать строку), не должен перезаписывать более
  // свежее состояние — иначе убранная карточка возвращалась бы на экран.
  const seq = useRef(0)

  const load = () => {
    const mine = ++seq.current
    return api.history(page, { kind, q: query })
      .then(r => {
        if (mine !== seq.current) return
        // Страница за концом (история сжалась, ссылка устарела) ведёт
        // на последнюю, а не в пустоту.
        if (r.pages > 0 && page > r.pages) { replace(historyPath(r.pages)); return }
        setItems(r.items ?? []); setPages(r.pages); setTotal(r.total)
      })
      .catch(e => { if (mine === seq.current) setErr(describeError(e)) })
  }

  // Смена вида или поиска возвращает на первую страницу: старый номер
  // относится к другому списку. Возврат делается ЗДЕСЬ, а не в обработчике
  // кнопки, и до запроса: иначе страница успевала сходить за вторым листом
  // нового фильтра и только потом за первым.
  const filters = useRef(`${kind}\u0000${query}`)
  useEffect(() => {
    const key = `${kind}\u0000${query}`
    if (key !== filters.current && page !== 1) {
      replace(historyPath(1))
      return
    }
    filters.current = key
    setItems(null); setErr('')
    void load()
    window.scrollTo({ top: 0 })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, kind, query])

  // Убрать из истории — это забыть строку прогресса: в ней и позиция, и
  // отметка «просмотрено», отдельной истории у платформы нет. Отсюда
  // возврат: восстановить либо отметку, либо позицию.
  async function remove(it: HistoryItem) {
    // Ответ запроса, уже летящего сюда, отменяется: он не знает об удалении.
    seq.current++
    setItems(prev => prev?.filter(x => x.episodeId !== it.episodeId) ?? prev)
    setTotal(t => Math.max(0, t - 1))
    setRemoved(it)
    try { await api.setWatched(it.episodeId, false) } catch { await load() }
  }
  async function restore() {
    if (!removed) return
    const it = removed
    setRemoved(null)
    try {
      if (it.status === 'completed') await api.setWatched(it.episodeId, true)
      else await api.progress(it.episodeId, it.positionMs, it.durationMs)
    } catch { /* перечитаем всё равно */ }
    await load()
  }

  const groups = useMemo(() => groupByDay(items ?? []), [items])

  return (
    <>
      <div className="title-top" style={{ alignItems: 'center' }}>
        <h1 style={{ margin: 0 }}>История{total ? <span className="n">{total.toLocaleString('ru-RU')}</span> : null}</h1>
        <span className="spacer" />
        <input className="hsearch" placeholder="Поиск в истории…" value={q}
               onChange={e => setQ(e.target.value)} aria-label="Поиск в истории" />
      </div>
      <div className="row" style={{ margin: '12px 0 18px' }}>
        {KINDS.map(k => (
          <button key={k.id} className={kind === k.id ? 'on' : ''} onClick={() => setKind(k.id)}>
            {k.label}
          </button>
        ))}
      </div>

      {removed && (
        <p className="muted small" style={{ margin: '-6px 0 14px' }}>
          Убрано из истории: {nameOf(removed.title)[0]}{removed.seasonNo > 0 ? ` · s${removed.seasonNo}e${removed.episodeNo}` : ''}.{' '}
          <button className="linky" onClick={() => void restore()}>Вернуть</button>
        </p>
      )}
      {err && <div className="err" style={{ marginBottom: 12 }}>{err}</div>}

      {items === null ? <CardsSkeleton /> : items.length === 0 ? (
        <div className="empty"><p>
          {query || kind ? 'Ничего не нашлось.' : 'Пока пусто. Здесь появится всё, что вы включали.'}
        </p></div>
      ) : groups.map(g => (
        <section key={g.key} className="hday">
          <h2>{g.label}</h2>
          <div className="hgrid">
            {g.items.map(it => {
              const [name, original] = nameOf(it.title)
              const pct = it.durationMs ? (it.positionMs / it.durationMs) * 100 : 0
              return (
                <div className="hcard" key={it.episodeId}>
                  <a href={`#${titlePath(it.titleId, it.seasonNo, it.episodeNo)}`}>
                    {/* Кадр серии, а не постер тайтла: в истории подряд идут
                        серии одного сериала, и одинаковые постеры не помогают
                        отличить их друг от друга. */}
                    <span className="hframe">
                      {it.hasShot
                        ? <img src={`/api/v1/episodes/${it.episodeId}/art`} alt="" loading="lazy" decoding="async" />
                        : it.hasArt
                          ? <img src={`/api/v1/titles/${it.titleId}/art`} alt="" loading="lazy" decoding="async" />
                          : <span className="noart" />}
                      <span className="hep">
                        {it.seasonNo > 0 ? `Сезон ${it.seasonNo}. Эпизод ${it.episodeNo}` : 'Фильм'}
                      </span>
                      {it.status === 'completed' && <span className="hdone">✓</span>}
                      {it.status !== 'completed' && pct > 0 && (
                        <span className="pbar"><i style={{ width: `${Math.min(100, pct)}%` }} /></span>
                      )}
                    </span>
                    <span className="hname clip">{name}</span>
                    {original && <span className="horig clip">{original}</span>}
                  </a>
                  <button className="htrash" title="Убрать из истории"
                          aria-label={`Убрать «${name}» из истории`} onClick={() => void remove(it)}>
                    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
                      <path d="M6 7h12M9 7V5h6v2M8 7l1 13h6l1-13" fill="none" stroke="currentColor"
                            strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
                    </svg>
                  </button>
                </div>
              )
            })}
          </div>
        </section>
      ))}

      <Pager page={page} pages={pages} onChange={p => go(historyPath(p))} />
    </>
  )
}

/** Площадка склеивает название и оригинал через « / »: показываем двумя строками. */
function nameOf(title: string): [string, string] {
  const i = title.indexOf(' / ')
  return i > 0 ? [title.slice(0, i), title.slice(i + 3)] : [title, '']
}

interface DayGroup { key: string; label: string; items: HistoryItem[] }

/** Группы по календарному дню в часовом поясе человека, в порядке ленты. */
function groupByDay(items: HistoryItem[]): DayGroup[] {
  const out: DayGroup[] = []
  for (const it of items) {
    const d = new Date(it.updatedAt)
    const key = `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`
    const last = out[out.length - 1]
    if (last && last.key === key) last.items.push(it)
    else out.push({ key, label: dayLabel(d), items: [it] })
  }
  return out
}

function dayLabel(d: Date): string {
  const today = new Date(); today.setHours(0, 0, 0, 0)
  const day = new Date(d); day.setHours(0, 0, 0, 0)
  const diff = Math.round((today.getTime() - day.getTime()) / 86400000)
  if (diff === 0) return 'Сегодня'
  if (diff === 1) return 'Вчера'
  const md = d.toLocaleDateString('ru-RU', { day: 'numeric', month: 'long' })
  return d.getFullYear() === today.getFullYear() ? md : `${md} ${d.getFullYear()}`
}
