import { useState } from 'react'
import { api, type ContinueItem } from '../api'
import { Tile, subtitleOf } from '../components/Tile'
import { GridSkeleton } from '../components/Skeleton'
import { titlePath } from '../router'
import { usePagedList, MoreRow } from '../paging'

/**
 * «Продолжить смотреть». Список не обрезается: начатого за годы набирается
 * под сотню, и обрезанное окно прятало бы хвост. Скрытие работает поверх
 * подгрузки — карточка исчезает сразу, а не после перечитывания.
 */
export function Home() {
  const load = (offset: number) => api.continueWatching(offset)
  const { items, total, hasMore, err, loading, more, loadMore, sentinel, reload } =
    usePagedList<ContinueItem>(load, '', it => it.episodeId)
  // Скрытые локально: карточка исчезает сразу, а не после перечитывания.
  const [dropped, setDropped] = useState<Set<number>>(new Set())
  // Последнее скрытое — чтобы вернуть, если рука дрогнула.
  const [hidden, setHidden] = useState<{ id: number; name: string } | null>(null)

  // Карточка исчезает сразу: скрыть — мгновенное действие, а ошибка
  // сервера — редкость, и тогда список просто перечитывается.
  async function hide(it: ContinueItem) {
    setDropped(prev => new Set(prev).add(it.titleId))
    setHidden({ id: it.titleId, name: it.title })
    try { await api.setHidden(it.titleId, true) } catch { setDropped(p => { const s = new Set(p); p.delete(it.titleId); return p }); await reload() }
  }
  async function unhide() {
    if (!hidden) return
    const h = hidden
    setHidden(null)
    setDropped(prev => { const n = new Set(prev); n.delete(h.id); return n })
    try { await api.setHidden(h.id, false) } catch { /* перечитаем всё равно */ }
    reload()
  }

  if (err && items.length === 0) return <div className="err">{err}</div>
  if (loading && items.length === 0) return <GridSkeleton />
  return (
    <>
      <h1>Продолжить смотреть{total ? <span className="n">{total}</span> : null}</h1>
      <p className="muted lead">
        Начатое и недосмотренное — ведёт на первую непросмотренную серию.
      </p>
      {err && <div className="err" style={{ marginBottom: 12 }}>{err}</div>}
      {hidden && (
        <p className="muted small" style={{ marginTop: -8, marginBottom: 14 }}>
          Скрыто: {hidden.name}. Вернётся само, если включить снова.{' '}
          <button className="linky" onClick={() => void unhide()}>Вернуть сейчас</button>
        </p>
      )}

      {items.length > 0 ? (
        <>
          <div className="grid">
            {items.filter(it => !dropped.has(it.titleId)).map(it => (
              <Tile
                key={it.episodeId}
                href={`#${titlePath(it.titleId, it.seasonNo, it.episodeNo)}`}
                titleId={it.titleId}
                onHide={() => void hide(it)}
                name={it.title}
                hasArt={it.hasArt}
                sub={it.seasonNo > 0
                  ? `s${it.seasonNo}e${it.episodeNo} · осталось ${left(it)}`
                  : `осталось ${left(it)}`}
                progressPct={it.durationMs ? (it.positionMs / it.durationMs) * 100 : 0}
              />
            ))}
          </div>
          {/* Сентинел автозагрузки: div без содержимого, виден только
              IntersectionObserver'у. */}
          <div ref={sentinel} style={{ height: 1 }} />
          <MoreRow more={more} hasMore={hasMore} total={total} shown={items.length}
                   onMore={loadMore} />
        </>
      ) : (
        // Перенос истории с kinopub делает администратор из админки:
        // кнопка у каждого тратила общий аккаунт площадки.
        <div className="empty">
          <p>Пока пусто. Начатое появится здесь, как только включите серию.
             Историю с kinopub переносит администратор — если нужна, попросите его.</p>
        </div>
      )}
    </>
  )
}

function left(it: ContinueItem): string {
  const ms = Math.max(0, it.durationMs - it.positionMs)
  const m = Math.round(ms / 60000)
  return m >= 60 ? `${Math.floor(m / 60)} ч ${m % 60} мин` : `${m} мин`
}

export { subtitleOf }
