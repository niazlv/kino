import { useEffect, useState } from 'react'
import { api, type Title } from '../api'
import { describeError } from '../App'
import { Tile, subtitleOf } from '../components/Tile'
import { GridSkeleton } from '../components/Skeleton'
import { Pager } from '../components/Pager'

// «Новинки» — по первому появлению хоть на одной площадке, «Обновлённые» —
// по последнему обновлению хоть на одной (у сериала это вышедшая серия).
// Даты не площадки-хозяина, а любой: так разделы не держатся на одной.
const SORTS = [
  { id: 'new', label: 'Новинки' },
  { id: 'updated', label: 'Обновлённые' },
  { id: 'year', label: 'По году' },
  { id: 'title', label: 'По названию' },
] as const

/**
 * Раздел каталога.
 *
 * Вид (фильм/сериал) и жанр — две разные оси. Аниме у площадки приходит
 * жанром, а не видом, поэтому «Сериалы» и «Аниме» пересекаются и задаются
 * независимо. heading и initialSort задают готовые разделы вроде «Свежих
 * серий» — тот же каталог с другим порядком.
 */
export function Browse({ kind, genre, heading: fixedHeading, initialSort }: {
  kind: string; genre: string; heading?: string; initialSort?: string
}) {
  const [items, setItems] = useState<Title[] | null>(null)
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [sort, setSort] = useState<string>(initialSort ?? 'new')
  const [err, setErr] = useState('')

  useEffect(() => { setPage(1) }, [kind, genre, sort])

  useEffect(() => {
    setItems(null); setErr('')
    api.browse({ kind, genre, sort, page })
      .then(r => { setItems(r.items ?? []); setTotal(r.total) })
      .catch(e => setErr(describeError(e)))
  }, [kind, genre, sort, page])

  const heading = fixedHeading || genre || (kind === 'movie' ? 'Фильмы' : kind === 'series' ? 'Сериалы' : 'Весь каталог')
  const pages = Math.ceil(total / 60)

  return (
    <>
      <h1>{heading}{total ? <span className="n">{total.toLocaleString('ru-RU')}</span> : null}</h1>

      <div className="row" style={{ marginBottom: 16 }}>
        {SORTS.map(s => (
          <button key={s.id} className={sort === s.id ? 'on' : ''} onClick={() => setSort(s.id)}>
            {s.label}
          </button>
        ))}
      </div>

      {err && <div className="err">{err}</div>}
      {items === null ? <GridSkeleton /> : items.length === 0 ? (
        <div className="empty"><p>
          Пусто. Каталог наполняется обходом площадки — возможно, до этого
          раздела ещё не дошло.
        </p></div>
      ) : (
        <>
          <div className="grid">
            {items.map(t => (
              <Tile key={t.id} href={`#/title/${t.id}`} titleId={t.id}
                    name={t.title} hasArt={t.hasArt} sub={subtitleOf(t.kind, t.year)} />
            ))}
          </div>
          <Pager page={page} pages={pages} onChange={p => { setPage(p); window.scrollTo({ top: 0 }) }} />
        </>
      )}
    </>
  )
}
