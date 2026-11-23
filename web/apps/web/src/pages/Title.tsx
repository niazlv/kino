import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, type Episode, type Playback, type Prefs, type Title } from '../api'
import type { AudioTrack, TextTrack } from '@kino/player'
import { describeError } from '../App'
import { Player } from './Player'
import { go, replace, titlePath } from '../router'
import { MarksBar } from '../components/MarksBar'
import { ShareDialog } from '../components/ShareDialog'
import { DownloadDialog, hasApp } from '../components/DownloadDialog'
import { canPlayExternal, LIBRARY_EVENT, localFor, playExternal, playLocal, syncLocalProgress, type LocalEpisode } from '../native'
import { TitleSkeleton } from '../components/Skeleton'
import { speciesLabel } from '../components/Tile'
import { isTV } from '../tv'

/**
 * Страница тайтла. Плеер живёт ЗДЕСЬ, а не на отдельном экране: иначе,
 * попав в него из «продолжить смотреть», человек не видит ни названия,
 * ни соседних серий, ни сезонов.
 */
export function TitlePage({ id, episodeId, season, episode }:
  { id: number; episodeId?: number; season?: number; episode?: number }) {
  const [title, setTitle] = useState<Title | null>(null)
  const [eps, setEps] = useState<Episode[]>([])
  // Что из этого тайтла уже лежит на устройстве. Читается у оболочки,
  // поэтому в браузере всегда пусто и разметка не меняется.
  const [local, setLocal] = useState<Map<string, LocalEpisode>>(() => localFor(id))
  const [prefs, setPrefs] = useState<Prefs>({ audio: null, subtitle: null })
  const [seasonTab, setSeasonTab] = useState<number | null>(null)
  const [err, setErr] = useState('')
  const [shareOpen, setShareOpen] = useState(false)
  const [dlOpen, setDlOpen] = useState(false)
  const [tracks, setTracks] = useState<{ audio: AudioTrack[]; text: TextTrack[] }>({ audio: [], text: [] })
  // Состав серии приходит из плеера: тикет он выпускает всё равно, и
  // диалогу эти данные достаются без второго запроса к площадке.
  const [source, setSource] = useState<Playback | null>(null)
  const [externalLoading, setExternalLoading] = useState(false)
  // Серию выбрали на этой странице — значит жест был, и стартовать можно
  // сразу. При открытии по ссылке браузер автостарт всё равно не разрешит.
  const autoplay = useRef(false)
  // Обёртка плеера, которая и разворачивается во весь экран. Сам плеер
  // пересоздаётся на каждую серию, и развёрнутым ему быть нельзя: исчезая
  // из документа, он ронял полный экран на каждом автопереходе.
  const playWrap = useRef<HTMLDivElement>(null)

  // Скачанное перечитываем при смене тайтла и при возврате из плеера: он
  // родной, и пока он играл, витрина ничего не знала о продвижении.
  useEffect(() => {
    const refresh = () => setLocal(localFor(id))
    refresh()
    void syncLocalProgress().then(n => { if (n > 0) refresh() })
    const on = () => { if (document.visibilityState === 'visible') refresh() }
    document.addEventListener('visibilitychange', on)
    addEventListener(LIBRARY_EVENT, refresh)
    return () => {
      document.removeEventListener('visibilitychange', on)
      removeEventListener(LIBRARY_EVENT, refresh)
    }
  }, [id])

  useEffect(() => {
    setTitle(null); setErr(''); autoplay.current = false
    api.title(id)
      .then(r => {
        setTitle(r.title); setEps(r.episodes ?? [])
        setPrefs(r.prefs ?? { audio: null, subtitle: null })
        setSeasonTab(null)
      })
      .catch(e => setErr(describeError(e)))
  }, [id])

  // Отметка применяется сразу, до ответа сервера: галочка — мгновенное
  // действие, и задержка читается как «кнопка не работает».
  const setWatched = useCallback(async (ep: Episode, watched: boolean) => {
    setEps(prev => prev.map(e =>
      e.id === ep.id
        ? { ...e, status: watched ? 'completed' : undefined, positionMs: watched ? undefined : 0 }
        : e))
    try { await api.setWatched(ep.id, watched) } catch { /* вернём при следующей загрузке */ }
  }, [])

  const setSeasonWatched = useCallback(async (season: number, watched: boolean) => {
    setEps(prev => prev.map(e =>
      e.seasonNo === season
        ? { ...e, status: watched ? 'completed' : undefined, positionMs: watched ? undefined : 0 }
        : e))
    try { await api.setSeasonWatched(id, season, watched) } catch { /* то же */ }
  }, [id])

  const seasons = useMemo(
    () => [...new Set(eps.map(e => e.seasonNo))].sort((a, b) => a - b),
    [eps],
  )

  // Выбранная серия: из адреса (сезон и номер, либо старый числовой id),
  // иначе первая недосмотренная, иначе первая. «Первая недосмотренная» —
  // то, чего человек ждёт, открыв сериал. Несуществующая в адресе серия
  // ведёт туда же, а не в пустой экран.
  const current = useMemo(() => {
    const fallback = eps.find(e => e.status !== 'completed') ?? eps[0] ?? null
    if (season !== undefined && episode !== undefined) {
      return eps.find(e => e.seasonNo === season && e.episodeNo === episode) ?? fallback
    }
    if (episodeId) return eps.find(e => e.id === episodeId) ?? fallback
    return fallback
  }, [eps, episodeId, season, episode])

  // Адрес всегда называет открытую серию: страница выбрала её сама, и без
  // этого ссылку нельзя ни переслать, ни открыть заново на том же месте.
  // Подмена, а не переход: «назад» должно вести на предыдущую страницу.
  useEffect(() => {
    if (!current || current.seasonNo === 0) return
    if (season === current.seasonNo && episode === current.episodeNo) return
    replace(titlePath(id, current.seasonNo, current.episodeNo))
  }, [current, id, season, episode])

  // Следующая по порядку сезонов и номеров — сервер отдаёт серии именно так.
  const [prev, next] = useMemo(() => {
    if (!current) return [null, null]
    const i = eps.findIndex(e => e.id === current.id)
    return i >= 0 ? [eps[i - 1] ?? null, eps[i + 1] ?? null] : [null, null]
  }, [eps, current])

  useEffect(() => { setSource(null); setShareOpen(false); setDlOpen(false); setTracks({ audio: [], text: [] }) }, [current?.id])

  const open = useCallback((e: Episode) => {
    autoplay.current = true
    // На телевизоре серию выбирают, чтобы смотреть: экран отдаётся плееру
    // здесь же, в том самом нажатии. Позже нельзя — тикет и плейлист идут
    // с площадки не одну секунду, а разрешение на полный экран живёт лишь
    // несколько секунд после нажатия; опоздав, плеер оставался окошком
    // посреди страницы. Фокус — туда же, иначе OK нажимал бы карточку
    // серии под развёрнутым плеером.
    const wrap = playWrap.current
    if (isTV() && wrap && !document.fullscreenElement) {
      void wrap.requestFullscreen().catch(() => {})
      wrap.querySelector<HTMLElement>('.kp-center, .kp-seek')?.focus({ preventScroll: true })
    }
    go(titlePath(id, e.seasonNo, e.episodeNo))
  }, [id])

  // Плеер сообщает об одном переключении за раз; локально сливаем.
  const onPrefs = useCallback((p: Partial<{ audio: string; subtitle: string; source: string }>) => {
    setPrefs(prev => ({ ...prev, ...p }))
    void api.setPrefs(id, p).catch(() => {})
  }, [id])

  const shownSeason = seasonTab ?? current?.seasonNo ?? seasons[0] ?? 0
  const shown = eps.filter(e => e.seasonNo === shownSeason)
  const watched = eps.filter(e => e.status === 'completed').length
  const shownDone = shown.filter(e => e.status === 'completed').length
  const seasonDone = shown.length > 0 && shownDone === shown.length
  // У фильма серия одна и сезон нулевой — «видел сезон» там звучит нелепо.
  const unit = shownSeason > 0 ? 'сезон' : 'фильм'

  if (err) {
    // Без сети тайтл не загрузить, но скачанное из него смотреть можно —
    // ради этого и качали.
    const saved = [...local.values()].sort((a, b) => a.season - b.season || a.episode - b.episode)
    return (
      <>
        <div className="err">{err}</div>
        {saved.length > 0 && (
          <div className="empty offline-saved">
            <p>{saved[0]!.title}: скачанное можно смотреть и без сети.</p>
            <div className="row">
              {saved.map(e => (
                <button key={e.id} className="sm" onClick={() => playLocal(e)}>
                  {e.season <= 1 && e.episode <= 1 ? 'Смотреть' : `Сезон ${e.season}, эпизод ${e.episode}`}
                </button>
              ))}
            </div>
            <p><a href="#/downloads">Всё скачанное</a></p>
          </div>
        )}
      </>
    )
  }
  if (!title) return <TitleSkeleton />

  const epCode = (e: Episode) => `s${e.seasonNo}e${e.episodeNo}`
  const epLabel = (e: Episode) => e.seasonNo > 0
    ? `${title.title} · ${epCode(e)}${e.title ? ` · ${e.title}` : ''}`
    : title.title
  // Подпись для «дальше»: номер, а при смене сезона — и сезон.
  const nextLabel = (e: Episode, from: Episode) =>
    (e.seasonNo !== from.seasonNo ? `Сезон ${e.seasonNo}, эпизод ${e.episodeNo}` : `Эпизод ${e.episodeNo}`)
    + (e.title ? ` · ${e.title}` : '')

  return (
    <>
      {/* Название — над плеером, а не только внутри него: пока источник
          готовится, человек уже видит, что открыл, и может поставить отметку. */}
      <div className="title-top">
        <div>
          <div className="eyebrow">
            <span>{speciesLabel(title)}</span>
            {title.year ? <span>· {title.year}</span> : null}
            {title.finished ? <span>· завершён</span> : null}
            {current && current.seasonNo > 0 && (
              <span className="now">· {epCode(current)}</span>
            )}
          </div>
          <h1>{title.title}</h1>
        </div>
        <span className="spacer" />
        <MarksBar titleId={title.id} />
      </div>

      <div className="playwrap" ref={playWrap}>
      {current && <Player
          key={current.id}
          episodeId={current.id}
          label={epLabel(current)}
          poster={current.hasArt ? `/api/v1/episodes/${current.id}/art` : undefined}
          onSource={setSource}
          next={next ? { label: nextLabel(next, current), onSelect: () => open(next) } : undefined}
          prev={prev ? { label: nextLabel(prev, current), onSelect: () => open(prev) } : undefined}
          autoplay={autoplay.current}
          prefs={prefs}
          onPrefs={onPrefs}
          onEnded={() => setEps(prev => prev.map(e =>
            e.id === current.id ? { ...e, status: 'completed' } : e))}
          onTracks={(audio, text) => setTracks({ audio, text })}
          fullscreenRef={playWrap}
        />}
      </div>

      {current && dlOpen && (
        <DownloadDialog
          title={title.title}
          episodeLabel={epLabel(current)}
          season={current.seasonNo}
          seasons={seasons}
          source={source}
          audio={tracks.audio}
          text={tracks.text}
          onClose={() => setDlOpen(false)}
        />
      )}

      {current && shareOpen && (
        <ShareDialog
          episodeId={current.id}
          label={epLabel(current)}
          source={source}
          onClose={() => setShareOpen(false)}
        />
      )}

      {eps.length === 0 && (
        <div className="empty"><p>
          Серии подтягиваются с площадки при первом открытии. Если их не видно —
          площадка сейчас недоступна, попробуйте позже.
        </p></div>
      )}

      {seasons.length > 1 && (
        <div className="seasons">
          {seasons.map(s => {
            const inSeason = eps.filter(e => e.seasonNo === s)
            const done = inSeason.length > 0 && inSeason.every(e => e.status === 'completed')
            return (
              <button key={s} className={s === shownSeason ? 'on' : ''} onClick={() => setSeasonTab(s)}>
                {done && <span className="tick">✓ </span>}
                {s === 0 ? 'Фильм' : `Сезон ${s}`}
              </button>
            )
          })}
        </div>
      )}

      {shown.length > 0 && (
        <div className="row" style={{ margin: '0 0 12px' }}>
          {/* «Видел сезон» — переключатель всего сезона, как «Слежу» у тайтла.
              У сезона, просмотренного наполовину, он лишь дометит остальное,
              поэтому рядом встаёт «не видел»: раньше снять отметки с такого
              сезона можно было только через полный — двумя нажатиями,
              о которых не догадаться. */}
          <button className={seasonDone ? 'on' : ''}
                  title={seasonDone
                    ? (shownSeason > 0 ? 'Снять отметки со всех серий сезона' : 'Снять отметку')
                    : (shownSeason > 0 ? 'Отметить все серии сезона просмотренными' : 'Отметить просмотренным')}
                  onClick={() => setSeasonWatched(shownSeason, !seasonDone)}>
            {seasonDone ? `✓ Видел ${unit}` : `Видел ${unit}`}
          </button>
          {shownDone > 0 && !seasonDone && (
            <button className="ghost" title="Снять отметки со всех серий сезона"
                    onClick={() => setSeasonWatched(shownSeason, false)}>
              Не видел {unit}
            </button>
          )}
          <span className="muted" style={{ fontSize: 12 }}>
            просмотрено {shownDone} из {shown.length}
          </span>
          {current && (
            <span className="row" style={{ marginLeft: 'auto', gap: 8 }}>
              {/* В приложении «Скачать» открывает выбор дорожек, и качает
                  встроенная утилита. В браузере — обычная ссылка: скачивание
                  ведёт браузер, и докачка с перемоткой достаются даром. */}
              {/* Уже лежит на устройстве — играем с диска, а не тянем
                  с сервера заново. Родным плеером: WebView не открывает
                  Matroska, в котором лежат озвучки и субтитры. */}
              {local.get(`${current.seasonNo}:${current.episodeNo}`) && (
                <button className="primary"
                        onClick={() => playLocal(local.get(`${current.seasonNo}:${current.episodeNo}`)!)}>
                  Смотреть скачанное
                </button>
              )}
              {/* Тем же потоком, но чужим плеером. Список приложений
                  показывает система — вшивать его у себя нельзя, он
                  устареет на следующем установленном плеере. */}
              {canPlayExternal() && source && (
                <button disabled={externalLoading} onClick={async () => {
                  setExternalLoading(true)
                  try {
                    // Свой тикет: только у внешнего плеера прогресс приходится
                    // оценивать по сегментам. Встроенный сохраняет точную позицию.
                    const video = playWrap.current?.querySelector('video')
                    video?.pause()
                    const positionMs = video ? Math.round(video.currentTime * 1000) : current.positionMs
                    const ticket = await api.playback(current.id, {
                      source: source.source?.id, strict: true, variant: source.variant, external: true,
                    })
                    if (!playExternal(ticket.url, `${title?.title ?? ''} · ${epLabel(current)}`,
                                      positionMs, ticket.protocol === 'PROGRESSIVE' ? 'video/mp4' : 'application/x-mpegurl')) {
                      alert('На устройстве нет приложения, которое открывает видео')
                    }
                  } catch (e) { alert(describeError(e)) }
                  finally { setExternalLoading(false) }
                }}>Внешний плеер</button>
              )}
              {hasApp()
                ? <button onClick={() => setDlOpen(true)}>Скачать</button>
                : <a className="btn" href={`/api/v1/episodes/${current.id}/download`}>Скачать</a>}
              <button onClick={() => setShareOpen(true)}>Поделиться</button>
            </span>
          )}
        </div>
      )}

      {shown.length > 0 && (
        <div className="epgrid">
          {shown.map(e => {
            const done = e.status === 'completed'
            const pct = e.positionMs && e.durationSec
              ? (e.positionMs / (e.durationSec * 1000)) * 100 : 0
            return (
              <button
                key={e.id}
                className={`epcard${current?.id === e.id ? ' on' : ''}`}
                data-focus-key={`ep-${e.id}`}
                onClick={() => open(e)}
              >
                <span
                  className={`check${done ? ' on' : ''}`}
                  role="checkbox"
                  aria-checked={done}
                  title={done ? 'Снять отметку' : 'Отметить просмотренной'}
                  onClick={ev => { ev.stopPropagation(); void setWatched(e, !done) }}
                >
                  {done ? '✓' : ''}
                </span>
                <span className="shot">
                  <EpisodeShot id={e.id} hasArt={e.hasArt} poster={title.hasArt ? `/api/v1/titles/${title.id}/art` : ''}
                               label={e.seasonNo > 0 ? `Эпизод ${e.episodeNo}` : 'Смотреть'} />
                  {!done && pct > 0 && <span className="pbar"><i style={{ width: `${Math.min(100, pct)}%` }} /></span>}
                  {/* Метка «лежит на устройстве»: по ней видно, что серию
                      можно включить без сети. */}
                  {local.has(`${e.seasonNo}:${e.episodeNo}`) && (
                    <span className="dlmark" title="Скачано" aria-label="Скачано">↓</span>
                  )}
                </span>
                <span className="cap">
                  {e.seasonNo > 0 ? `Эпизод ${e.episodeNo}` : 'Смотреть'}
                  {e.durationSec > 0 && <span className="muted"> · {Math.round(e.durationSec / 60)} мин</span>}
                </span>
                {/* Название серии — отдельной строкой под номером: номер
                    нужен для навигации, название — чтобы узнать серию. */}
                {e.title && <span className="epname clip">{e.title}</span>}
              </button>
            )
          })}
        </div>
      )}

      <div className="about">
      <div className="title-head">
        <div className="art">
          {title.hasArt
            ? <img src={`/api/v1/titles/${title.id}/art`} alt="" decoding="async" />
            : <div className="noart">{title.title}</div>}
        </div>
        <div>
          <h2>О тайтле</h2>
          {title.plot && <p className="plot">{title.plot}</p>}
          <dl className="info">
            <dt>Тип</dt>
            <dd>{speciesLabel(title)}{title.finished ? ' · завершён' : ''}</dd>
            {title.year ? <><dt>Год</dt><dd>{title.year}</dd></> : null}
            {(title.sources?.length ?? 0) > 1 ? (
              // Площадки дополняют друг друга: какая из них играет, видно в
              // плеере, а переключить можно в его меню.
              <><dt>Площадки</dt><dd><div className="chips">
                {title.sources!.map(x => <span className="chip" key={x.id}>{x.name}</span>)}
              </div></dd></>
            ) : null}
            {eps.length > 0 && (
              <><dt>Всего</dt><dd>
                {seasons.length > 1 ? `${seasons.length} сезона, ` : ''}
                {eps.length} {eps.length === 1 ? 'эпизод' : 'эпизодов'}
                {watched > 0 && <span className="muted"> · просмотрено {watched}</span>}
              </dd></>
            )}
            {title.genres?.length ? (
              <><dt>Жанр</dt><dd><div className="chips">
                {title.genres.map(g => <span className="chip" key={g}>{g}</span>)}
              </div></dd></>
            ) : null}
            {title.imdb ? (
              <><dt>IMDb</dt><dd>
                <a style={{ color: 'var(--link)' }} target="_blank" rel="noreferrer"
                   href={`https://www.imdb.com/title/${title.imdb}/`}>{title.imdb}</a>
              </dd></>
            ) : null}
          </dl>
        </div>
      </div>
      </div>
    </>
  )
}

/**
 * Кадр серии, а при его отсутствии — приглушённый постер тайтла с подписью.
 *
 * hasArt у серии аниме-площадки означает «кадр стоит попросить», а не «он
 * есть»: площадка отдаёт кадры по запросу, и у части видео их нет. Не
 * пришёл — вместо битой картинки остаётся подпись, как у серии без кадра.
 *
 * Кадр есть не у всех площадок (балансер Alloha картинок серий не отдаёт
 * вовсе), и сетка из голых серых номеров читалась как поломка. Постер под
 * подписью говорит «серия тут, кадра просто нет».
 */
function EpisodeShot({ id, hasArt, poster, label }: { id: number; hasArt: boolean; poster?: string; label: string }) {
  const [failed, setFailed] = useState(false)
  if (hasArt && !failed) {
    return <img src={`/api/v1/episodes/${id}/art`} alt="" loading="lazy" decoding="async"
                onError={() => setFailed(true)} />
  }
  return (
    <>
      {poster && <img className="stub" src={poster} alt="" loading="lazy" decoding="async" />}
      <span className="noart">{label}</span>
    </>
  )
}
