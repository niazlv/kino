import { useCallback } from 'react'
import { api, type Title } from '../api'
import { usePagedList, MoreRow } from '../paging'
import { Tile, subtitleOf } from '../components/Tile'
import { GridSkeleton } from '../components/Skeleton'

const HEADS = {
  following: { title: 'Я смотрю', empty: 'Пока никого не отслеживаете. Отметьте сериал на его странице — и новые серии не потеряются.' },
  watchlist: { title: 'Буду смотреть', empty: 'Список пуст. Откладывайте сюда то, до чего руки пока не дошли.' },
} as const

/**
 * Список по отметкам. Не обрезается на первой странице: карточки
 * подгружаются по мере прокрутки, счётчик в заголовке — общее число
 * в списке, а не показанное.
 */
export function ListPage({ kind }: { kind: keyof typeof HEADS }) {
  const load = useCallback((offset: number) => api.list(kind, offset), [kind])
  const { items, total, hasMore, err, loading, more, loadMore, sentinel } =
    usePagedList<Title>(load, kind, t => t.id)

  const head = HEADS[kind]
  if (err && items.length === 0) return <div className="err">{err}</div>
  if (loading && items.length === 0) return <GridSkeleton />

  return (
    <>
      <h1>{head.title}{total ? <span className="n">{total}</span> : null}</h1>
      {err && <div className="err" style={{ marginBottom: 12 }}>{err}</div>}
      {items.length === 0 ? (
        <div className="empty"><p>{head.empty}</p></div>
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
          <MoreRow more={more} hasMore={hasMore} total={total} shown={items.length}
                   onMore={loadMore} />
        </>
      )}
    </>
  )
}
