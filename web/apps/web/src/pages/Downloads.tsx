import { useCallback, useEffect, useState } from 'react'
import {
  bytes, hasLibrary, library, LIBRARY_EVENT, localArt, playLocal, removeLocal,
  resolveLocalTitles, setLocalWatched, syncLocalProgress, type LocalEpisode,
} from '../native'
import { titlePath } from '../router'

/**
 * Скачанное — раздел, который есть только в приложении.
 *
 * Смысл в том, чтобы не ходить на сервер за тем, что уже лежит на устройстве:
 * скачанная серия играет с диска, и прогресс по ней копится на месте, потому
 * что смотрят скачанное как раз без сети. При первом заходе со связью
 * накопленное уезжает на сервер — дальше история общая, как у обычного
 * просмотра.
 *
 * Ничего не удаляется само: место освобождает человек, и для этого просмотренное
 * лежит отдельным списком.
 */
export function Downloads() {
  const [items, setItems] = useState<LocalEpisode[]>(() => library())
  const [note, setNote] = useState('')

  const reload = useCallback(() => setItems(library()), [])

  // Пока играл родной плеер, витрина стояла: позиция и «просмотрено»
  // поменялись в описи без неё.
  // Оболочка в это время могла найти старые файлы и нарезать кадры.
  useEffect(() => {
    const on = () => { if (document.visibilityState === 'visible') reload() }
    document.addEventListener('visibilitychange', on)
    addEventListener(LIBRARY_EVENT, reload)
    return () => {
      document.removeEventListener('visibilitychange', on)
      removeEventListener(LIBRARY_EVENT, reload)
    }
  }, [reload])

  useEffect(() => {
    // Найденным в папке файлам — номер тайтла, затем прогресс, накопленный
    // без сети: при первом же заходе со связью.
    void resolveLocalTitles()
      .then(found => { if (found > 0) reload() })
      .then(() => syncLocalProgress())
      .then(n => { if (n > 0) { setNote(`Прогресс отправлен: ${n}`); reload() } })
  }, [reload])

  if (!hasLibrary()) {
    return (
      <>
        <h1>Скачанное</h1>
        <div className="empty">
          <p>Этот раздел работает в приложении. Обновите его, чтобы смотреть скачанное с устройства.</p>
        </div>
      </>
    )
  }

  const fresh = items.filter(e => !e.watched)
  const seen = items.filter(e => e.watched)
  const total = items.reduce((n, e) => n + e.size, 0)

  const drop = (e: LocalEpisode) => {
    if (!removeLocal(e)) { setNote('Файл не удалось стереть'); return }
    reload()
  }

  const dropSeen = () => {
    if (seen.length === 0) return
    const size = bytes(seen.reduce((n, e) => n + e.size, 0))
    if (!confirm(`Стереть просмотренное (${seen.length}) и освободить ${size}?`)) return
    let failed = 0
    for (const e of seen) if (!removeLocal(e)) failed++
    setNote(failed ? `Не удалось стереть: ${failed}` : '')
    reload()
  }

  return (
    <>
      <h1>
        Скачанное
        {items.length ? <span className="n">{items.length}</span> : null}
      </h1>
      {items.length > 0 && (
        <p className="dlsum">
          Занято {bytes(total)}
          {seen.length > 0 && (
            <button className="sm" onClick={dropSeen}>Стереть просмотренное</button>
          )}
        </p>
      )}
      {note && <div className="err">{note}</div>}

      {items.length === 0 ? (
        <div className="empty">
          <p>Пока пусто. Нажмите «Скачать» на странице серии, и она появится здесь.</p>
        </div>
      ) : (
        <>
          <Group items={fresh} onPlay={playLocal} onDrop={drop}
                 onWatched={e => { setLocalWatched(e, true); reload() }} watched={false} />
          {seen.length > 0 && (
            <>
              <h2 className="dlhead">Просмотрено</h2>
              <Group items={seen} onPlay={playLocal} onDrop={drop}
                     onWatched={e => { setLocalWatched(e, false); reload() }} watched />
            </>
          )}
        </>
      )}
    </>
  )
}

function Group({ items, watched, onPlay, onDrop, onWatched }: {
  items: LocalEpisode[]
  watched: boolean
  onPlay: (e: LocalEpisode) => void
  onDrop: (e: LocalEpisode) => void
  onWatched: (e: LocalEpisode) => void
}) {
  if (items.length === 0) {
    return <div className="empty"><p>Всё просмотрено. Можно освободить место.</p></div>
  }
  // Внутри — по тайтлу и по порядку серий: скачивают сериал целиком, и
  // вперемешку его читать невозможно.
  const sorted = [...items].sort((a, b) =>
    a.title.localeCompare(b.title) || a.season - b.season || a.episode - b.episode)

  return (
    <div className="eplist">
      {sorted.map(e => {
        const pct = e.durationMs ? (e.positionMs / e.durationMs) * 100 : 0
        // У фильма сезон и серия первые — подпись «Эпизод 1» на фильме врёт.
        const label = e.season <= 1 && e.episode <= 1
          ? 'Фильм'
          : `Сезон ${e.season}, эпизод ${e.episode}`
        return (
          <div className="hrow dlrow" key={e.id}>
            <button className="hshot dlshot" onClick={() => onPlay(e)}
                    aria-label={`Смотреть: ${e.title}`}>
              {/* Кадр и постер — с устройства, сервер только запасом. Если и
                  его нет — пустая плашка лучше значка битой картинки (hidden
                  не годится: display у картинки задан стилем). */}
              {localArt(e) && (
                <img src={localArt(e)} alt="" loading="lazy" decoding="async"
                     onError={ev => { ev.currentTarget.style.visibility = 'hidden' }} />
              )}
              {watched && <span className="hdone">✓</span>}
              {!watched && pct > 0 && (
                <span className="pbar"><i style={{ width: `${Math.min(100, pct)}%` }} /></span>
              )}
            </button>
            <div className="hmain">
              {/* Найденный в папке файл без номера тайтла — вести некуда,
                  пока витрина не найдёт тайтл по названию. */}
              {e.titleId > 0
                ? <a className="clip" href={`#${titlePath(e.titleId, e.season, e.episode)}`}>{e.title}</a>
                : <span className="clip">{e.title}</span>}
              <span className="sub">{label} · {bytes(e.size)}</span>
            </div>
            <div className="dlacts">
              <button className="sm" onClick={() => onPlay(e)}>Смотреть</button>
              <button className="sm" onClick={() => onWatched(e)}>
                {watched ? 'Не просмотрено' : 'Просмотрено'}
              </button>
              <button className="sm danger" onClick={() => onDrop(e)}>Стереть</button>
            </div>
          </div>
        )
      })}
    </div>
  )
}
