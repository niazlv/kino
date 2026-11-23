import { useEffect, useRef, useState } from 'react'

/**
 * Карточка каталога.
 *
 * Постер идёт через наш прокси, а не прямой ссылкой на площадку: её CDN
 * у домашних может быть недоступен, и сетка превратилась бы в стену битых
 * картинок. hasArt приходит с сервера, чтобы не ходить за отсутствующим.
 */
export function Tile({
  href, titleId, name, sub, hasArt, badge, progressPct, onHide,
}: {
  href: string
  titleId: number
  name: string
  sub?: string
  hasArt?: boolean
  badge?: number
  progressPct?: number
  /** Убрать с главной. Кнопка лежит рядом со ссылкой, а не внутри неё:
   *  кнопка внутри ссылки — невалидная разметка с непредсказуемым кликом. */
  onHide?: () => void
}) {
  // Постер появляется плавно, а до него — мерцающая тень. Пустой тёмный
  // прямоугольник читался как «картинки нет», хотя она ещё ехала через
  // прокси. Если не приехала вовсе — вместо неё название, как и без постера.
  const [state, setState] = useState<'loading' | 'ok' | 'fail'>(hasArt ? 'loading' : 'fail')
  const img = useRef<HTMLImageElement>(null)
  useEffect(() => {
    // Из кеша картинка бывает готова раньше, чем повесится onLoad.
    const el = img.current
    if (el && el.complete && el.naturalWidth > 0) setState('ok')
  }, [])

  return (
    <div className="tile">
      <a className="tilelink" href={href}>
        <div className={`art${state === 'loading' ? ' sk' : ''}`}>
          {hasArt && state !== 'fail' && (
            // loading=lazy обязателен: в сетке легко оказывается сотня карточек,
            // и грузить все постеры сразу — это сотня запросов через прокси.
            <img ref={img} src={`/api/v1/titles/${titleId}/art`} alt="" loading="lazy" decoding="async"
                 className={state === 'ok' ? 'in' : ''}
                 onLoad={() => setState('ok')} onError={() => setState('fail')} />
          )}
          {state === 'fail' && <div className="noart"><span>{name}</span></div>}
          <span className="play" aria-hidden>
            <svg viewBox="0 0 24 24"><path d="M8 5v14l11-7z" /></svg>
          </span>
          {badge !== undefined && badge > 0 && <span className="badge">{badge}</span>}
          {progressPct !== undefined && progressPct > 0 && (
            <div className="pbar"><i style={{ width: `${Math.min(100, progressPct)}%` }} /></div>
          )}
        </div>
        <div className="name clip">{name}</div>
        {sub && <div className="sub">{sub}</div>}
      </a>
      {onHide && (
        <button className="hide" title="Скрыть с главной" aria-label={`Скрыть «${name}» с главной`}
                onClick={onHide}>×</button>
      )}
    </div>
  )
}

/**
 * Как назвать вид произведения по-человечески. kind различает только
 * «фильм или сериал»; вид точнее — дорама это корейский сериал или фильм,
 * аниме — по жанру. Неизвестный вид (не приехали страны) — старые подписи.
 */
export const SPECIES_LABEL: Record<string, string> = {
  anime: 'аниме', cartoon: 'мультфильм', dorama: 'дорама', series: 'сериал', movie: 'фильм',
}

export function speciesLabel(title: { species?: string; kind: string }): string {
  return SPECIES_LABEL[title.species ?? ''] ?? (title.kind === 'series' ? 'сериал' : 'фильм')
}

/** Единый формат подписи под карточкой. */
export function subtitleOf(kind: string, year?: number, species?: string): string {
  const k = SPECIES_LABEL[species ?? ''] ?? (kind === 'series' ? 'сериал' : 'фильм')
  return year ? `${year} · ${k}` : k
}
