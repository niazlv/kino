import { useCallback, useRef, useState } from 'react'
import { api, type NewEpisodeItem } from '../api'
import { usePagedList, MoreRow } from '../paging'
import { Tile } from '../components/Tile'
import { GridSkeleton } from '../components/Skeleton'

/**
 * Непросмотренное в том, за чем человек следит.
 *
 * Только по following: непросмотренное во всём каталоге — это десятки тысяч
 * строк, и такой список не значит ничего. Слежение и есть заявление
 * «мне интересны новые серии именно здесь».
 *
 * Досмотренное на «Я смотрю» не стоит в общем ряду: сериал, у которого
 * новых серий не выходило, сейчас никто не смотрит. Но и снимать с него
 * слежение нельзя — тогда новый сезон пройдёт мимо. Поэтому такие уходят
 * в свёрнутую группу внизу и сами возвращаются наверх, когда серии выйдут.
 * Деление здесь, а не в запросе: сервер и так отдаёт всё слежение со
 * счётчиками, второй эндпоинт ради одного фильтра разъехался бы с первым.
 *
 * Страницы едут по порядку «недосмотренное раньше досмотренного», поэтому
 * активная часть всегда в первых страницах, а досмотренное докатывается
 * в хвосте. Пока блок досмотренного свёрнут, автозагрузка стоит: качать
 * скрытые страницы незачем — развернут, и список докрутится сам.
 */
export function NewEpisodes({ onlyNew }: { onlyNew: boolean }) {
  const [showDone, setShowDone] = useState(false)
  // Автозагрузка глядит на карточки прошлой отрисовки (self-ссылку в
  // аргументы хука передать нельзя), задержка на один кадр ни на что
  // здесь не влияет.
  const itemsRef = useRef<NewEpisodeItem[]>([])
  const load = useCallback((offset: number) => api.newEpisodes(onlyNew, offset), [onlyNew])
  const { items, total, activeTotal, hasMore, err, loading, more, loadMore, sentinel } =
    usePagedList<NewEpisodeItem>(load, onlyNew, it => it.titleId,
      // Автозагрузка активных карточек и догрузка развёрнутого досмотренного;
      // свёрнутый хвост страницами не качается.
      showDone || itemsRef.current.every(it => it.unwatched > 0))
  itemsRef.current = items

  if (err && items.length === 0) return <div className="err">{err}</div>
  if (loading && items.length === 0) return <GridSkeleton />

  // Для «новых эпизодов» сервер досмотренное не отдаёт, done там пуст.
  const active = items.filter(it => it.unwatched > 0)
  const done = items.filter(it => it.unwatched === 0)
  // Общие числа известны с первой страницы: заголовок показывает их,
  // а не число загруженных карточек.
  const activeCount = activeTotal ?? active.length
  const doneTotal = Math.max(total - activeCount, done.length)

  const emptyText = !onlyNew && total === 0
    ? 'Пока ни за чем не следите. Отметьте сериал «Слежу» на его странице.'
    : 'Всё просмотрено. Новые серии появятся здесь, когда выйдут.'

  return (
    <>
      <h1>
        {onlyNew ? 'Новые эпизоды' : 'Я смотрю'}
        {activeCount ? <span className="n">{activeCount}</span> : null}
      </h1>
      <p className="muted lead">
        {onlyNew
          ? 'Из отслеживаемого — только то, где есть непросмотренные серии.'
          : 'То, за чем вы следите и где есть что смотреть. Цифра на карточке — сколько серий не просмотрено.'}
      </p>
      {err && <div className="err" style={{ marginBottom: 12 }}>{err}</div>}
      {active.length === 0 ? (
        <div className="empty"><p>{emptyText}</p></div>
      ) : (
        <div className="grid">
          {active.map(it => (
            <Tile key={it.titleId} href={`#/title/${it.titleId}`} titleId={it.titleId}
                  name={it.title} hasArt={it.hasArt} badge={it.unwatched}
                  sub={`${it.unwatched} из ${it.total} не просмотрено`} />
          ))}
        </div>
      )}
      {(done.length > 0 || doneTotal > 0) && (
        <section>
          <h2>
            Досмотрено <span className="muted">{doneTotal}</span>{' '}
            <button className="linky" aria-expanded={showDone}
                    onClick={() => setShowDone(v => !v)}>
              {showDone ? 'свернуть' : 'показать'}
            </button>
          </h2>
          {showDone ? (
            <div className="grid">
              {done.map(it => (
                <Tile key={it.titleId} href={`#/title/${it.titleId}`} titleId={it.titleId}
                      name={it.title} hasArt={it.hasArt}
                      sub={`${it.total} из ${it.total} просмотрено`} />
              ))}
            </div>
          ) : (
            <p className="muted small">
              Слежение остаётся: как только выйдут новые серии, сериал вернётся наверх.
              Снять слежение можно на странице сериала.
            </p>
          )}
        </section>
      )}
      {/* Автозагрузка и кнопка живут на уровне страницы, а не внутри блока
          досмотренного: у «Новых эпизодов» блока нет вовсе, и хвост списка
          был бы недостижим. Качаем, пока едут активные карточки или блок
          развёрнут; свёрнутый хвост страницами не тянем — развёрнут, и
          список докрутится сам. */}
      {hasMore && (showDone || done.length === 0) && (
        <>
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
