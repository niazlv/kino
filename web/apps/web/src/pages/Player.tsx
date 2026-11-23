import { useEffect, useMemo, useRef, useState } from 'react'
import { usePlayer } from '@kino/player/react'
import { Controls, type ChoiceGroup, type NextItem } from '@kino/player/ui'
import { canPip, requestPip, showStats } from '../native'
import type { AudioTrack, KinoPlayer, PlayerOptions, SessionStats, TextTrack } from '@kino/player'
import { api, ApiError, deviceKind, type Playback, type PlaybackChoice, type Prefs, type SkipSegment, type VoiceList } from '../api'
import { learnVoiceKeys, unknownVoiceLabels, voiceKey, voiceTitle } from '../voices'

/**
 * Почему играет не та площадка: иначе смена озвучки выглядит поломкой.
 * Отказ площадки объясняется всегда, «нет этой серии» — только у той,
 * что человек выбрал сам: остальные и не должны были играть.
 */
function passedNote(pb: Playback | null, chosen?: string, voice?: string | null): string {
  if (!pb?.source) return ''
  const why = (pb.passed ?? []).flatMap(p => {
    switch (p.reason) {
      case 'down':
      case 'failed': return [`${p.name} не отвечает`]
      case 'busy': return [`${p.name} просит подождать`]
      // Протух вход, и сам он не продлился: чинит администратор.
      case 'reauth': return [`${p.name}: нужна переавторизация`]
      case 'slow': return [`${p.name} тормозит`]
      case 'missing': return p.id === chosen ? [`у ${p.name} нет этой серии`] : []
      // Запомненная студия важнее запомненной площадки.
      case 'novoice': return [voice ? `у ${p.name} нет «${voiceTitle(voice)}»` : `у ${p.name} нет этой озвучки`]
    }
    return []
  })
  return why.length ? `${why.join(', ')} — играет ${pb.source.name}` : ''
}

/** Что запомнить для тайтла после удачного переключения. */
type Remember = Partial<{ audio: string; source: string }>

/**
 * Площадки, чей поток на этом устройстве только что тормозил, — до какого
 * времени их обходить. Память страницы, а не тайтла и не сервера: следующая
 * серия тоже не должна начинаться с площадки, которая сейчас не тянет, но
 * узел VPN за четверть часа мог смениться — и ей снова дают шанс.
 */
const slowUntil = new Map<string, number>()
const SLOW_FOR_MS = 15 * 60_000

/** Выбор с просьбой обойти тормозящие площадки. Строгий — как есть:
 *  человек нажал именно эту. */
function avoidSlow(c: PlaybackChoice): PlaybackChoice {
  if (c.strict) return c
  const now = Date.now()
  const avoid = [...slowUntil].filter(([, until]) => until > now).map(([id]) => id)
  return avoid.length ? { ...c, avoid } : c
}

/**
 * Оценка канала по площадке на этом устройстве, бит/с: с неё плеер начнёт
 * следующую серию. По площадке, а не одна на всё: kinopub идёт через VPN
 * (10–20 Мбит/с), AnimeGO напрямую (около 40) — с общей оценкой одна
 * стартовала бы мылом, другая — с остановками.
 */
const BW_KEY = 'kino.bw.'
function bandwidthHint(source?: string): number | undefined {
  if (!source) return undefined
  try {
    const v = Number(localStorage.getItem(BW_KEY + source))
    return v > 0 ? v : undefined
  } catch { return undefined }
}
function rememberBandwidth(source: string | undefined, kbps: number | undefined) {
  if (!source || !kbps) return
  try { localStorage.setItem(BW_KEY + source, String(kbps * 1000)) } catch { /* без памяти — старт как раньше */ }
}

// Сколько раз за окно поток можно поднимать без человека. Больше — значит,
// не помогает, и честная ошибка с «Повторить» лучше карусели площадок.
const RECOVER_MAX = 3
const RECOVER_WINDOW_MS = 2 * 60_000
// Тормозит, а той же озвучки больше нигде нет — столько не дёргать.
const QUIET_MS = 5 * 60_000

/**
 * Почему переключение не вышло — строкой поверх кадра. Серия при этом
 * идёт дальше на прежнем, и это тоже сказано: иначе непонятно, что играет.
 */
function switchFailed(what: string, e: unknown, stays: string): string {
  const why = e instanceof ApiError && e.status === 404 ? 'нет этой серии' : describeError(e)
  return `${what}: ${why}${stays ? ` — ${stays}` : ''}`
}
import { describeError } from '../App'
import { claimFocus, isTV } from '../tv'

const APP_VERSION = '0.1.0'

/**
 * Блок воспроизведения на странице тайтла.
 *
 * Единственная точка склейки платформы с плеером: плеер получает обычный URL
 * и инжектированные приёмники, про тикеты и эндпоинты не знает ничего.
 */
export function Player({ episodeId, label, poster, onSource, next, prev, autoplay, prefs, onPrefs, onEnded, onTracks, fullscreenRef }:
  { episodeId: number; label: string; poster?: string
    /**
     * Сообщает странице, что источник предлагает — дорожки, субтитры,
     * качества. Тикет всё равно выпускается здесь, поэтому диалог
     * скачивания берёт эти данные даром, без второго похода на площадку.
     */
    onSource?: (pb: Playback) => void
    /** Следующая серия — решает страница: плеер не знает, что такое сезон. */
    next?: NextItem
    /** Предыдущая серия — кнопка в развёрнутом плеере, как у кинопаба. */
    prev?: NextItem
    /** Стартовать сразу: серию выбрали на этой же странице, жест уже был. */
    autoplay?: boolean
    /** Что человек выбирал для этого тайтла раньше. */
    prefs?: Prefs
    /** Человек переключил дорожку, озвучку или площадку — страница
     *  запомнит. Автовыбор по предпочтению сюда не приходит: он и так
     *  из памяти. */
    onPrefs?: (p: Partial<{ audio: string; subtitle: string; source: string }>) => void
    /** Серия досмотрена: страница отметит её сразу, не дожидаясь перезагрузки. */
    onEnded?: () => void
    /**
     * Дорожки, как их видит плеер — с именами из плейлиста. Диалог скачивания
     * называет ими дорожки утилите: та отбирает по тем же именам, а подписи
     * площадки в тикете («Orig») с плейлистом не совпадают.
     */
    onTracks?: (audio: AudioTrack[], text: TextTrack[]) => void
    /**
     * Обёртка страницы, которую плеер разворачивает во весь экран вместо
     * себя. Сам он пересоздаётся на каждую серию (key на странице), и
     * развёрнутый плеер падал бы вместе с ней.
     */
    fullscreenRef?: React.RefObject<HTMLElement> }) {
  const [pb, setPb] = useState<Playback | null>(null)
  const [startMs, setStartMs] = useState(0)
  const [err, setErr] = useState('')
  const [ready, setReady] = useState(false)
  const lastReport = useRef<{ positionMs: number; durationMs: number; clientTs: string } | null>(null)
  // Досмотрено до титров и ушли к следующей: последний отчёт плеера
  // сообщит конец серии, а не место на титрах. Отдельным запросом нельзя —
  // он гонится с этим отчётом, и сервер оставлял того, кто пришёл вторым.
  const watchedTo = useRef(0)
  const boxRef = useRef<HTMLElement>(null)
  // Откуда играть. Сначала — что запомнено для тайтла: площадка и студия
  // озвучки (студия подходит к любой площадке, номер варианта — нет).
  // Удачное переключение в меню заменяет выбор на строгий: человек нажал
  // именно эту площадку, и обновление тикета не должно её подменить.
  const choice = useRef<PlaybackChoice>({
    source: prefs?.source || undefined,
    variantLabel: prefs?.audio || undefined,
  })
  // Выпустить тикет заново — «Повторить».
  const [attempt, setAttempt] = useState(0)
  // Играло ли в момент переключения: тогда и новый поток стартует сам —
  // жест уже был, и остановка посреди серии читалась бы как поломка.
  const resumePlaying = useRef(false)
  // Идущее переключение и строка поверх кадра — что с ним.
  const switching = useRef<AbortController | null>(null)
  const [notice, setNotice] = useState<{ text: string; busy?: boolean } | null>(null)
  // Новый поток уже выдан, но первого кадра ещё нет: кольцо ожидания, как
  // при первом старте.
  const [swapping, setSwapping] = useState(false)
  // Что играло до подмены. Тикет выдан — ещё не значит, что поток пойдёт:
  // CDN площадки может не пустить ссылку. Тогда возвращаемся сюда, а не
  // оставляем чёрный кадр.
  const before = useRef<{
    pb: Playback; positionMs: number; choice: PlaybackChoice; failed: string
    // Невыполненное «запомнить» того, к чему возвращаемся: человек мог
    // переключиться ещё раз, не дождавшись первого кадра.
    remember: Remember | null
    // Студия, чей поток не пошёл: в меню она станет «не вышло».
    voice?: string
  } | null>(null)
  // Выбор запоминается, только когда новый поток пошёл.
  const remember = useRef<Remember | null>(null)
  // Озвучки серии со всех площадок одним списком; null — ещё не пришли.
  const [voices, setVoices] = useState<VoiceList | null>(null)
  // Студии, которые в этой серии не запустились ни с одной площадки.
  const [voiceFailed, setVoiceFailed] = useState<ReadonlySet<string>>(new Set())
  const markFailed = (key?: string) => { if (key) setVoiceFailed(s => new Set(s).add(key)) }
  // Студия, выбранная только что: по ней новый поток выберет дорожку
  // (у kinopub студия — дорожка потока), пока она не запомнена.
  const wantVoice = useRef<string | null>(null)

  useEffect(() => {
    let cancelled = false
    // Ушли с серии — выпуск тикета отменяется и на сервере: он бросит
    // площадку, а не будет держать её ради закрытой страницы.
    const abort = new AbortController()
    setReady(false); setErr(''); setPb(null)
    ;(async () => {
      try {
        // Позицию берём ДО выпуска тикета: если резолв сорвётся, мы всё
        // равно знаем, докуда человек досмотрел.
        const pos = await api.progress0(episodeId).catch(() => ({ positionMs: 0, status: '' }))
        const ticket = await api.playback(episodeId, avoidSlow(choice.current), abort.signal)
        if (cancelled) return
        // Просмотренную серию открывают, чтобы пересмотреть: позиция у неё
        // стоит в конце, и продолжение «с места» дало бы секунду видео и
        // сразу «дальше — следующая».
        setStartMs(pos.status === 'completed' ? 0 : (pos.positionMs ?? 0))
        setPb(ticket)
        setReady(true)
        onSource?.(ticket)
      } catch (e) {
        if (!cancelled) setErr(describeError(e))
      }
    })()
    return () => { cancelled = true; abort.abort() }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [episodeId, attempt])

  // Общий список озвучек просится, когда серия уже играет: поток не ждёт,
  // пока ответят все площадки. Не пришёл — меню остаётся прежним.
  useEffect(() => {
    if (!ready || voices) return
    let live = true
    api.voices(episodeId).then(l => { if (live) setVoices(l) }).catch(() => {})
    return () => { live = false }
  }, [ready, episodeId, voices])

  // Ушли с серии посреди переключения — его тикет больше никому не нужен.
  useEffect(() => () => switching.current?.abort(), [])

  // Итог переключения висит над кадром несколько секунд и гаснет сам;
  // «переключаю…» держится, пока ждём площадку.
  useEffect(() => {
    if (!notice || notice.busy) return
    const t = setTimeout(() => setNotice(null), 6000)
    return () => clearTimeout(t)
  }, [notice])

  // Приёмники стабильны по ссылке: пересоздание пересобрало бы плеер
  // и обрывало воспроизведение на каждом рендере.
  const progressSink = useMemo(() => ({
    report: (positionMs: number, durationMs: number) => {
      if (watchedTo.current > positionMs) positionMs = watchedTo.current
      // Ядро сообщает позицию только после восстановления источника.
      // Ноль здесь допустим: человек мог перемотать к самому началу.
      if (positionMs < 0 || durationMs <= 0) return
      const clientTs = new Date().toISOString()
      lastReport.current = { positionMs, durationMs, clientTs }
      void api.progress(episodeId, positionMs, durationMs, clientTs).catch(() => {})
    },
    flush: () => {
      if (!lastReport.current) return
      // sendBeacon переживает закрытие вкладки, обычный fetch — нет.
      navigator.sendBeacon?.(
        `/api/v1/progress/${episodeId}`,
        new Blob([JSON.stringify(lastReport.current)], { type: 'application/json' }),
      )
    },
  }), [episodeId])

  const telemetrySink = useMemo(() => ({
    session: (s: SessionStats) => {
      if (!pb) return
      void api.telemetry({
        ...s, ticketId: pb.ticketId, episodeId,
        deviceKind: deviceKind(), appVersion: APP_VERSION,
      }).catch(() => {})
    },
  }), [pb, episodeId])

  // Закрытие вкладки или уход в фон — последний репорт уходит биконом.
  const pbRef = useRef(pb)
  pbRef.current = pb

  // Подписки ставятся при создании плеера и живут дольше рендера, поэтому
  // обработчик берётся через ref — иначе он запомнил бы первый onPrefs.
  const prefsRef = useRef(onPrefs)
  prefsRef.current = onPrefs
  const endedRef = useRef(onEnded)
  endedRef.current = onEnded
  const sourceRef = useRef(onSource)
  sourceRef.current = onSource
  // Подъём потока без человека (см. recover): обработчики плеера ставятся
  // один раз, а нужен им сегодняшний pb.
  const recoverRef = useRef<(why: 'expired' | 'slow' | 'broken') => void>(() => {})
  // Сам плеер появляется ниже (usePlayer), а обработчикам он нужен уже здесь.
  const playerRef = useRef<React.MutableRefObject<KinoPlayer | null> | null>(null)
  const listeners = useMemo<PlayerOptions['on']>(() => ({
    error: e => {
      if (!e.fatal) return
      const b = before.current
      before.current = null
      remember.current = b?.remember ?? null
      wantVoice.current = null
      if (!b) {
        // Поток оборвался сам. 404 и 410 — ссылка протухла (вкладку
        // оставили на сутки), её выдают заново с той же площадки; иначе
        // площадка не отдаёт — другая с той же озвучкой. Одна пауза здесь
        // оставила бы плеер крутить кольцо без конца.
        setSwapping(false)
        const gone = (e.status === 404 || e.status === 410) && !refreshedLink.current
        refreshedLink.current = false
        recoverRef.current(gone ? 'expired' : 'broken')
        return
      }
      markFailed(b.voice)
      choice.current = b.choice
      setStartMs(b.positionMs)
      setPb(b.pb)
      setNotice({ text: b.failed })
      sourceRef.current?.(b.pb)
    },
    ended: () => endedRef.current?.(),
    // Источник не успевает за просмотром — серия встаёт снова и снова.
    stall: () => recoverRef.current('slow'),
    // Раз в 15 с, пока плеер жив: оценка канала — для старта следующей серии.
    timeupdate: () => rememberBandwidth(pbRef.current?.source?.id, playerRef.current?.current?.stats().bandwidthKbps),
    // Запоминается студия, а не подпись плейлиста: «01. Одноголосый.
    // Воротилин (RUS)» у kinopub и «Воротилин» у других площадок — одна.
    audiochange: e => { if (!e.auto) prefsRef.current?.({ audio: voiceTitle(e.label || e.language) }) },
    textchange: e => {
      if (e.auto) return
      prefsRef.current?.({ subtitle: e.id === null ? '' : (e.label || e.language) })
    },
  }), [])

  const source = useMemo(
    () => (pb ? {
      url: pb.url, startPositionMs: startMs, poster, autoplay: autoplay || resumePlaying.current,
      bandwidthHintBps: bandwidthHint(pb.source?.id),
      // Площадка может отдать файл целиком (Sibnet): тогда не HLS.
      type: pb.protocol === 'PROGRESSIVE' ? 'progressive' as const : 'hls' as const,
      preferredAudioLabel: wantVoice.current ?? prefs?.audio ?? undefined,
      // Вариант «Субтитры» (оригинал с подписями) выбран только что и
      // руками: выключенные когда-то субтитры здесь не в счёт.
      preferredTextLabel: prefs?.subtitle === '' && pb.subtitles.some(s => s.default)
        ? undefined : prefs?.subtitle ?? undefined,
      // Субтитры отдельными файлами площадка кладёт рядом с потоком, и шлюз
      // отдаёт их по тикету — плеер добавит их к дорожкам потока сам.
      subtitles: pb.subtitles.flatMap(s => s.url
        ? [{ url: s.url, label: s.title, language: s.language, default: s.default }] : []),
      // Название для системной медиа-панели: шторка уведомлений и локскрин.
      title: label,
    } : null),
    // Предпочтения нужны только на момент загрузки: их смена потом источник
    // не пересобирает (плеер смотрит на url), и в зависимостях они лишние.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [pb, startMs, poster, autoplay],
  )

  const { videoRef, player, impl, audio, qualities, text, audioId, textId } = usePlayer(source, {
    progress: progressSink,
    telemetry: telemetrySink,
    on: listeners,
    audioLabelKey: voiceKey,
  })
  playerRef.current = player

  // Закрытие вкладки или уход в фон — последний репорт уходит биконом:
  // 15-секундный таймер к этому моменту мог не дотикать. React-клинап при
  // выгрузке страницы не выполняется, поэтому pagehide здесь обязанность
  // страницы, а не плеера. Телеметрия едет тем же биконом: обычный fetch
  // при выгрузке браузер убивает, и сессии оставались без конца.
  useEffect(() => {
    const flush = () => {
      const p = player.current
      p?.flushProgress()
      if (!p || !pbRef.current) return
      const s = p.sessionStats()
      if (!s) return
      navigator.sendBeacon?.(
        '/api/v1/telemetry/session',
        new Blob([JSON.stringify({
          ...s, ticketId: pbRef.current.ticketId, episodeId,
          deviceKind: deviceKind(), appVersion: APP_VERSION,
        })], { type: 'application/json' }),
      )
    }
    const onHide = () => { if (document.visibilityState === 'hidden') flush() }
    window.addEventListener('pagehide', flush)
    document.addEventListener('visibilitychange', onHide)
    return () => {
      window.removeEventListener('pagehide', flush)
      document.removeEventListener('visibilitychange', onHide)
    }
  }, [progressSink, player])

  // Новый поток пошёл — первый кадр разобран: выбор запоминается, путь
  // назад больше не нужен. Не раньше: разобранный плейлист ещё не значит,
  // что CDN отдаст сегменты.
  useEffect(() => {
    const v = videoRef.current
    if (!swapping || !v) return
    const done = () => {
      setSwapping(false)
      before.current = null
      refreshedLink.current = false
      wantVoice.current = null
      if (remember.current) prefsRef.current?.(remember.current)
      remember.current = null
    }
    v.addEventListener('loadeddata', done)
    return () => v.removeEventListener('loadeddata', done)
  }, [swapping, pb, videoRef])

  // Площадка и озвучка-поток — выбор, который делает платформа, а не
  // плеер: новый тикет, тот же момент серии.
  //
  // Пока площадка отвечает, играет прежний поток, и меняется он только
  // на готовый: погасни плеер сразу по нажатию, при отказе на его месте
  // осталось бы «не найдено», и серию было бы не вернуть без перехода на
  // другую. Отказ — строка поверх кадра, и серия идёт дальше.
  // Запоминается тоже только удавшийся выбор.
  // voice — студия из общего списка: не вышло — в меню она «не вышло».
  const switchTo = (next: PlaybackChoice, what: string, stays: string, prefsAfter: Remember, voice?: string) => {
    switching.current?.abort()
    const abort = new AbortController()
    switching.current = abort
    // Площадку нажали руками — значит, готовы ждать и тормозящую.
    if (next.strict && next.source) slowUntil.delete(next.source)
    setNotice({ text: `${what} — переключаю…`, busy: true })
    void (async () => {
      try {
        const ticket = await api.playback(episodeId, avoidSlow(next), abort.signal)
        if (abort.signal.aborted) return
        swapTo(ticket, next, switchFailed(what, new Error('поток не запустился'), stays), prefsAfter, null, voice)
      } catch (e) {
        if (abort.signal.aborted) return
        wantVoice.current = null
        markFailed(voice)
        setNotice({ text: switchFailed(what, e, stays) })
      } finally {
        if (switching.current === abort) switching.current = null
      }
    })()
  }

  // Подмена потока на выданный: тот же момент серии. Прежний остаётся путём
  // назад, пока у нового нет первого кадра (см. обработчик error); failed —
  // что сказать, если придётся вернуться (null — некуда: прежняя ссылка
  // мертва), said — что сказать сразу.
  const swapTo = (ticket: Playback, next: PlaybackChoice, failed: string | null,
    prefsAfter: Remember | null, said: string | null, voice?: string) => {
    // Место берётся в момент подмены, а не нажатия: пока площадка
    // отвечала, серия шла дальше.
    const v = videoRef.current
    const positionMs = v && v.currentTime > 0 ? Math.floor(v.currentTime * 1000) : startMs
    before.current = pb && failed !== null
      ? { pb, positionMs, choice: choice.current, remember: remember.current, voice, failed } : null
    remember.current = prefsAfter
    resumePlaying.current = !!v && !v.paused
    setStartMs(positionMs)
    choice.current = next
    setPb(ticket)
    setSwapping(true)
    setNotice(said ? { text: said } : null)
    onSource?.(ticket)
  }

  // Поток сам не поднимется: ссылка протухла, источник тормозит или
  // оборвался. У вкладки, оставленной на сутки, тикет стирается, и плеер,
  // доиграв буфер, крутил бы кольцо; площадка через VPN может вставать по
  // три раза за две минуты, хотя та же серия есть у другой. Поэтому
  // протухшая ссылка выдаётся заново с той же площадки и озвучкой,
  // а тормоз или обрыв — повод взять другую площадку с той же озвучкой.
  // Человек при этом ничего не нажимает; его ручной выбор в меню сильнее —
  // он прерывает подъём (switchTo отменяет switching).
  const recovered = useRef<number[]>([])
  const quietUntil = useRef(0)
  // Поток — свежая ссылка взамен протухшей, и первого кадра у него ещё не
  // было: отказ теперь — не срок, а площадка.
  const refreshedLink = useRef(false)
  const recover = (why: 'expired' | 'slow' | 'broken') => {
    const cur = pb?.source
    if (!pb || !cur || switching.current || before.current) return
    const now = Date.now()
    if (why === 'slow' && now < quietUntil.current) return
    recovered.current = recovered.current.filter(t => now - t < RECOVER_WINDOW_MS)
    if (recovered.current.length >= RECOVER_MAX) {
      // Тормоз — не ошибка: серия идёт, пусть и с остановками.
      if (why !== 'slow') setErr('Поток обрывается: площадки не отдают серию')
      return
    }
    recovered.current.push(now)
    const label = curVoice?.title ?? choice.current.variantLabel
    let next: PlaybackChoice
    if (why === 'expired') {
      next = { source: cur.id, strict: true, variant: pb.variant, variantLabel: label }
    } else {
      slowUntil.set(cur.id, now + SLOW_FOR_MS)
      // Студия из общего списка — тогда сервер возьмёт ровно её; иначе
      // подпись, по которой он поищет её сам.
      const key = curVoice && voices?.voices.some(v => v.key === curVoice.key) ? curVoice.key : undefined
      next = { source: choice.current.source, variantLabel: label, ...(key ? { voice: key } : {}) }
    }
    const trouble = `${cur.name} ${why === 'slow' ? 'тормозит' : 'не отдаёт поток'}`
    if (why !== 'expired') setNotice({ text: `${trouble} — ищу другую площадку…`, busy: true })
    const abort = new AbortController()
    switching.current = abort
    void (async () => {
      try {
        const ticket = await api.playback(episodeId, avoidSlow(next), abort.signal)
        if (abort.signal.aborted) return
        const to = ticket.source
        if (why === 'slow' && to?.id === cur.id) {
          // Той же озвучки больше нигде нет — остаёмся: медленный поток
          // лучше чужой студии. Обходить площадку дальше незачем, и
          // дёргать её — пять минут тоже.
          slowUntil.delete(cur.id)
          quietUntil.current = Date.now() + QUIET_MS
          setNotice({ text: `${trouble}, а ${curVoice ? `«${curVoice.title}»` : 'этой озвучки'} больше нигде нет` })
          return
        }
        const moved = why !== 'expired' && to && to.id !== cur.id
        // Прежняя ссылка протухла — возвращаться к ней некуда; не пошла и
        // свежая — значит, дело не в сроке, и это уже обрыв (см. error).
        refreshedLink.current = why === 'expired'
        // Студия та же — запоминается, чтобы и следующая серия начиналась
        // с неё, а не с озвучки площадки по умолчанию.
        swapTo(ticket, next,
          why === 'expired' ? null : switchFailed(to?.name ?? 'Площадка', new Error('поток не запустился'), `играет ${cur.name}`),
          moved && curVoice ? { audio: curVoice.title } : null,
          moved ? `${trouble} — играет ${to.name}` : null)
      } catch (e) {
        if (abort.signal.aborted) return
        if (why === 'slow') {
          // Других площадок нет или не ответили — серия идёт как шла.
          quietUntil.current = Date.now() + QUIET_MS
          setNotice({ text: `${trouble}: другие площадки не ответили` })
          return
        }
        setErr(describeError(e))
      } finally {
        if (switching.current === abort) switching.current = null
      }
    })()
  }
  recoverRef.current = recover

  // Вкладку оставили на паузе дольше срока ссылки — «играть» не ждёт, пока
  // буфер кончится и шлюз ответит отказом: ссылка обновляется сразу.
  useEffect(() => {
    const v = videoRef.current
    if (!v || !pb) return
    const onPlay = () => {
      const exp = Date.parse(pb.expiresAt)
      if (Number.isFinite(exp) && exp < Date.now()) recoverRef.current('expired')
    }
    v.addEventListener('play', onPlay)
    return () => v.removeEventListener('play', onPlay)
  }, [pb, videoRef])

  // Ключи дорожек потока и вариантов площадки считает сервер: у него
  // псевдонимы студий из админки. До ответа (и без сети) — тот же разбор
  // подписи здесь; ответ пересобирает меню и «что играет».
  const [voiceKeysAt, setVoiceKeysAt] = useState(0)
  useEffect(() => {
    const labels = unknownVoiceLabels([
      ...audio.map(t => t.label || t.language),
      ...(pb?.variants ?? []).map(v => v.title),
    ])
    if (!labels.length) return
    let live = true
    api.voiceKeys(labels.slice(0, 64))
      .then(r => { learnVoiceKeys(r.keys ?? {}); if (live) setVoiceKeysAt(Date.now()) })
      .catch(() => { /* остаётся свой разбор */ })
    return () => { live = false }
  }, [audio, pb])

  // Что играет сейчас — ключ студии: вариант площадки или дорожка потока.
  const curVoice = useMemo(() => {
    const v = pb?.variants?.find(x => x.id === pb.variant)
    if (v) return { key: voiceKey(v.title), title: voiceTitle(v.title) }
    const a = audio.find(t => t.id === audioId)
    return a ? { key: voiceKey(a.label || a.language), title: voiceTitle(a.label || a.language) } : null
    // voiceKeysAt — сервер назвал ключи: тот же ввод, другой ответ.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pb, audio, audioId, voiceKeysAt])

  const choices = useMemo<ChoiceGroup[]>(() => {
    if (!pb?.source) return []
    const cur = pb.source
    const groups: ChoiceGroup[] = []
    const stays = curVoice ? `осталась ${curVoice.title}` : ''
    const list = voices?.voices ?? []

    if (list.length > 0) {
      // Озвучка одним списком: студия один раз, сколько бы площадок её ни
      // отдавали, — откуда взять, решает сервер. Человек выбирает студию,
      // а не площадку.
      const known = new Set(list.map(v => v.key))
      const items: ChoiceGroup['items'] = list.map(v => ({
        id: v.key, label: v.title, hint: voiceFailed.has(v.key) ? 'не вышло' : undefined,
      }))
      // Играющая озвучка и дорожки потока, которых в списке нет (площадка
      // назвала их иначе или список устарел), — всё равно в меню. Дорожки —
      // только там, где озвучка и есть дорожка (kinopub): у аниме-площадки
      // в потоке одна безымянная дорожка, и она — сама выбранная студия.
      const cv = pb.variants?.find(x => x.id === pb.variant)
      if (cv && !known.has(voiceKey(cv.title))) {
        known.add(voiceKey(cv.title))
        items.unshift({ id: `variant:${cv.id}`, label: cv.title })
      }
      for (const t of cv ? [] : audio) {
        const k = voiceKey(t.label || t.language)
        if (known.has(k)) continue
        known.add(k)
        items.push({ id: `track:${t.id}`, label: voiceTitle(t.label || t.language) })
      }
      const selected = !curVoice ? null
        : list.some(v => v.key === curVoice.key) ? curVoice.key
          : cv ? `variant:${cv.id}` : audioId ? `track:${audioId}` : null
      groups.push({
        title: 'Озвучка',
        items,
        selected,
        onSelect: id => {
          if (id.startsWith('track:')) { player.current?.selectAudio(id.slice(6)); return }
          const v = list.find(x => x.key === id)
          if (!v) return
          // Дорожка того же потока (kinopub) — переключает сам плеер, сразу.
          if (v.ways.some(w => w.source === cur.id && !w.variant)) {
            const t = audio.find(x => voiceKey(x.label || x.language) === v.key)
            if (t) { player.current?.selectAudio(t.id); return }
          }
          wantVoice.current = v.title
          switchTo({ voice: v.key, source: cur.id, variantLabel: v.title }, v.title, stays,
            { audio: v.title }, v.key)
        },
      })
    } else if ((pb.variants ?? []).length > 0) {
      // Общего списка ещё нет (или сервер его не дал) — озвучки той
      // площадки, что играет, как раньше.
      const variants = pb.variants ?? []
      // Одна студия у двух плееров — различаем плеером, иначе в меню два
      // одинаковых пункта.
      const dup = new Set(variants.map(v => v.title).filter((t, i, a) => a.indexOf(t) !== i))
      groups.push({
        title: 'Озвучка',
        items: variants.map(v => ({ id: v.id, label: dup.has(v.title) && v.player ? `${v.title} · ${v.player}` : v.title })),
        selected: pb.variant ?? null,
        onSelect: id => {
          const v = variants.find(x => x.id === id)
          switchTo({ source: cur.id, strict: true, variant: id }, v?.title ?? 'Озвучка', stays,
            { source: cur.id, ...(v ? { audio: v.title } : {}) })
        },
      })
    }

    // Площадка — во «Видео»: от неё зависят картинка и качество, а не
    // голос. По умолчанию её выбирает сервер; ручной выбор строгий и
    // запоминается для тайтла.
    const sources = pb.sources ?? []
    if (sources.length > 1) {
      const auto = !prefs?.source
      groups.push({
        title: 'Источник',
        tab: 'video',
        items: [
          { id: '', label: 'Автоматически', hint: auto ? cur.name : undefined },
          ...sources.map(x => ({
            id: x.id, label: x.name,
            hint: !auto && x.id === cur.id && prefs?.source !== cur.id ? 'сейчас' : undefined,
          })),
        ],
        selected: auto ? '' : prefs?.source ?? '',
        onSelect: id => {
          // «Автоматически» и уже играющая площадка — только память.
          if (id === '' || id === cur.id) { prefsRef.current?.({ source: id }); return }
          // Та же студия на другой площадке — если она там есть.
          switchTo({ source: id, strict: true, variantLabel: curVoice?.title ?? prefs?.audio ?? undefined },
            sources.find(x => x.id === id)?.name ?? id, `играет ${cur.name}`, { source: id })
        },
      })
    }
    return groups
    // eslint-disable-next-line react-hooks/exhaustive-deps
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pb, voices, voiceFailed, audio, audioId, curVoice, prefs?.source, voiceKeysAt])

  // На телевизоре человек пришёл смотреть: как только источник готов, фокус
  // встаёт на «играть», и OK на пульте запускает серию. Если он уже пошёл
  // стрелками сам, фокус не отбирается.
  useEffect(() => {
    if (!ready || !isTV()) return
    const box = boxRef.current
    claimFocus(box?.querySelector<HTMLElement>('.kp-center') ?? box?.querySelector<HTMLElement>('.kp-seek') ?? null)
  }, [ready])

  // Разметка пропуска: что пришло с потоком — сразу, остальное (площадки,
  // отдающие её отдельно, и AniSkip) — вдогонку. Первый кадр её не ждёт:
  // кнопка «пропустить» нужна не раньше заставки.
  const [moreSkips, setMoreSkips] = useState<SkipSegment[]>([])
  useEffect(() => {
    setMoreSkips([])
    if (!pb) return
    let gone = false
    api.skips(episodeId, pb.source?.id, pb.variant)
      .then(r => { if (!gone) setMoreSkips(r.segments ?? []) })
      .catch(() => { /* без разметки серия смотрится как раньше */ })
    return () => { gone = true }
  }, [pb, episodeId])
  const segments = useMemo(() => [...(pb?.segments ?? []), ...moreSkips], [pb, moreSkips])

  // С титров — к следующей серии: конца файла не было, но серия досмотрена.
  // Последний отчёт плеера (он уходит, когда плеер снимается со страницы)
  // сообщит конец серии, как по окончании, а страница отметит её сразу.
  const creditsNext = () => {
    const v = videoRef.current
    if (v && Number.isFinite(v.duration)) watchedTo.current = Math.floor(v.duration * 1000)
    endedRef.current?.()
  }

  const tracksRef = useRef(onTracks)
  tracksRef.current = onTracks
  useEffect(() => { tracksRef.current?.(audio, text) }, [audio, text])

  // «Дальше» для системной медиа-панели: шторка уведомлений и локскрин
  // получают кнопку следующей серии. Через ref: next пересоздаётся родителем
  // на каждом рендере, а обработчик ставится один раз на плеер.
  const nextActionRef = useRef<NextItem | undefined>(next)
  nextActionRef.current = next
  useEffect(() => {
    player.current?.setNextAction(() => nextActionRef.current?.onSelect())
  }, [player, pb])

  // Ошибка во весь экран — чёрный лист с одной строкой и без выхода,
  // кроме «назад». Возвращаем страницу: там и текст, и список серий.
  useEffect(() => {
    if (err && document.fullscreenElement) void document.exitFullscreen().catch(() => {})
  }, [err])

  if (err) {
    // Сюда попадает только первый выпуск серии: запомненная площадка
    // строгой не бывает, сервер и так перебрал все. «Повторить» — потому
    // что площадка могла ожить, а перезагружать страницу незачем.
    // Неудачное переключение из меню плеер не гасит — см. switchTo.
    return (
      <div className="err">
        {err}
        <button style={{ marginLeft: 12 }} onClick={() => setAttempt(n => n + 1)}>Повторить</button>
      </div>
    )
  }

  const note = passedNote(pb, choice.current.source, prefs?.audio)

  return (
    <section ref={boxRef}>
      {/* Управление своё: нативная панель у каждого браузера разная,
          не умеет переключать озвучку и на телефоне занимает пол-экрана. */}
      <div className="playbox">
        <Controls
          videoRef={videoRef}
          player={player}
          audio={audio}
          qualities={qualities}
          subtitles={text}
          audioId={audioId}
          textId={textId}
          title={label}
          preparing={!ready || swapping}
          next={next}
          prev={prev}
          stats={showStats()}
          tv={isTV()}
          onPiP={canPip() ? requestPip : undefined}
          fullscreenRef={fullscreenRef}
          choices={choices}
          notice={notice?.text}
          // Общий список озвучек уже содержит дорожки потока.
          audioTracks={!voices?.voices.length}
          segments={segments}
          onCreditsNext={creditsNext}
        />
      </div>
      <div className="row" style={{ marginTop: 8 }}>
        {note && <span className="srcnote">{note}</span>}
        <span className="spacer" />
        {/* Площадка видна и одна: откуда идёт поток — часть картины. */}
        {pb?.source && <span className="engine">площадка · {pb.source.name}</span>}
        <span className="engine">движок · {impl ?? '—'}</span>
      </div>
    </section>
  )
}
