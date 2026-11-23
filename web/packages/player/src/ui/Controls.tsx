import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import type { KinoPlayer } from '../core/player'
import { HoverPreview, PREVIEW_H, PREVIEW_MATCH_S, PREVIEW_W, type PreviewFrame } from '../core/preview'
import type { AudioTrack, Quality, TextTrack } from '../core/types'
import {
  activeSkip, pickSkips, skipLabel, skipName, skippedLabel, type Skip, type SkipSegment,
} from '../core/skips'
import {
  IconArrowBack, IconCheck, IconExitFull, IconFull, IconMute, IconNext, IconPause,
  IconPiP, IconPlay, IconPrev, IconReplay, IconSettings, IconSkip, IconSpeed, IconVolume,
  IconVolumeLow,
} from './icons'
import './controls.css'

const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2]
const SKIP = 10
/** Шаг перемотки в раскладке кинопаба — у него 15 секунд. */
const CINE_SKIP = 15
const IDLE_MS = 2600
const DOUBLE_TAP_MS = 300
const AUTONEXT_KEY = 'kp.autonext'
/** Как пропускать заставки: off — никак, button — кнопкой, auto — само. */
export const SKIP_KEY = 'kp.skip'
export type SkipMode = 'off' | 'button' | 'auto'
/** Сколько висит подсказка «пропущено · вернуть». */
const TOAST_MS = 5000
/**
 * Сколько серий подряд плеер включает сам, пока человек ни к чему не
 * прикоснулся. Дальше — «Вы ещё смотрите?»: уснувший зритель иначе
 * засчитал бы себе полсезона, а у каждого в семье своя история просмотра.
 */
const MAX_CHAIN = 3
const CHAIN_KEY = 'kp.chain'

function readChain(): number {
  try { return Number(sessionStorage.getItem(CHAIN_KEY)) || 0 } catch { return 0 }
}
function writeChain(n: number) {
  try { sessionStorage.setItem(CHAIN_KEY, String(n)) } catch { /* приватный режим */ }
}

export function readSkipMode(): SkipMode {
  try {
    const v = localStorage.getItem(SKIP_KEY)
    return v === 'off' || v === 'auto' ? v : 'button'
  } catch { return 'button' }
}

/** Что дальше: подпись для оверлея и действие перехода. */
export interface NextItem {
  label: string
  onSelect: () => void
}

/**
 * Выбор, который делает хозяин, а не ядро плеера.
 *
 * Бывает, что озвучка — не дорожка внутри потока, а другой поток: у
 * площадки, где каждая студия — отдельное видео, переключить её может
 * только тот, кто выдаёт адреса. Плеер лишь показывает пункты рядом с
 * дорожками и сообщает, что выбрали; откуда они и что значат, он не знает.
 */
export interface ChoiceGroup {
  title: string
  /** hint — серая пометка справа («не вышло», «сейчас AnimeGO.fr»). */
  items: { id: string; label: string; hint?: string }[]
  selected?: string | null
  onSelect: (id: string) => void
  /** Под какой надписью раскладки кинопаба: «Аудио» (по умолчанию) или
   *  «Видео» — выбор, от которого зависит картинка, а не звук. */
  tab?: 'audio' | 'video'
}

export interface ControlsProps {
  videoRef: React.RefObject<HTMLVideoElement>
  player: React.MutableRefObject<KinoPlayer | null>
  audio: AudioTrack[]
  qualities?: Quality[]
  subtitles?: TextTrack[]
  /** Что выбрано сейчас. Без этого панель не узнала бы о выборе по
   *  предпочтению, сделанном при загрузке, и галочка стояла бы не там. */
  audioId?: string | null
  textId?: string | null
  title?: string
  /** Показывать оверлей ожидания, пока платформа готовит источник. */
  preparing?: boolean
  /** Следующая серия: кнопка в панели и переход по окончании. */
  next?: NextItem
  /** Предыдущая серия: кнопка в развёрнутой раскладке. Без неё та же
   *  кнопка возвращает серию к началу. */
  prev?: NextItem
  /** Секунд до автоперехода после конца. 0 — только по кнопке. */
  autoNextSec?: number
  /** Полупрозрачная сводка о потоке в углу. Гаснет вместе с панелью. */
  stats?: boolean
  /**
   * Пультом управляемый экран: телевизор.
   *
   * Плеер тогда ведёт себя как ТВ-приложение, а не как окно на странице:
   * пока панель спрятана, стрелки перематывают, OK ставит паузу, вниз
   * открывает панель. Ловить фокус на невидимой полосе времени человеку
   * с пультом невозможно — это и читалось как «плеер не ловит фокус».
   */
  tv?: boolean
  /**
   * Запасной способ уменьшить окно, когда браузерного PiP нет.
   *
   * В Android WebView `document.pictureInPictureEnabled` равен false, и
   * запрос отклоняется молча — кнопка выглядит рабочей, но ничего не делает.
   * Оболочка передаёт сюда системный способ. Возврат false означает «не
   * вышло»: кнопка тогда не показывается вовсе.
   */
  onPiP?: (width: number, height: number) => boolean | Promise<boolean>
  /**
   * Что разворачивать во весь экран вместо самого плеера.
   *
   * Хозяин пересоздаёт плеер на каждую серию, а развёрнутый элемент,
   * исчезнув из документа, роняет полный экран: после каждого автоперехода
   * страница вспыхивала, а на телевизоре развернуться заново уже нельзя —
   * разрешение живёт секунды после нажатия. Обёртка вокруг плеера смену
   * серии переживает; растянуть в ней плеер — дело стилей хозяина.
   */
  fullscreenRef?: React.RefObject<HTMLElement>
  /** Группы выбора хозяина — в меню перед дорожками. */
  choices?: ChoiceGroup[]
  /**
   * Показывать дорожки потока своим списком. false — хозяин сам ведёт
   * список озвучек (дорожки потока в нём уже есть), и второй список с
   * теми же студиями только путал бы.
   */
  audioTracks?: boolean
  /**
   * Строка поверх кадра от хозяина: как идёт его выбор («переключаю…»,
   * «нет этой серии»). Видна и при спрятанной панели, и во весь экран —
   * там, где страница со своими надписями не видна.
   */
  notice?: string
  /**
   * Что в серии можно пропустить: заставка, титры, пересказ. Какой
   * разметке верить для этого потока, решает плеер — длину файла знает
   * только он (см. pickSkips). Не задано — хозяин пропуск не поддерживает.
   */
  segments?: SkipSegment[]
  /**
   * Человек ушёл к следующей серии с титров: конца файла не было, но
   * серия досмотрена. Хозяин отмечает её — как по концу.
   */
  onCreditsNext?: () => void
}

/**
 * Управление плеером.
 *
 * Своё, а не системное `controls`: нативная панель у каждого браузера своя,
 * не умеет переключать озвучку и на телефоне занимает пол-экрана. Здесь же
 * дорожки, качество, скорость и субтитры в одном месте.
 *
 * Живёт в пакете плеера и ничего не знает о платформе — как и ядро.
 * «Следующая серия» приходит снаружи готовым действием: что считать
 * следующей, плееру знать не положено.
 */
export function Controls({
  videoRef, player, audio, qualities = [], subtitles = [], audioId, textId,
  title, preparing, next, prev, autoNextSec = 5, stats, tv, onPiP, fullscreenRef, choices = [],
  notice, audioTracks = true, segments, onCreditsNext,
}: ControlsProps) {
  const box = useRef<HTMLDivElement>(null)
  const seekEl = useRef<HTMLDivElement>(null)
  const popEl = useRef<HTMLDivElement>(null)
  const [playing, setPlaying] = useState(false)
  // Поток встал и ждёт данных: в центре крутится кольцо, как у кинопаба.
  const [buffering, setBuffering] = useState(false)
  const [ended, setEnded] = useState(false)
  const [pos, setPos] = useState(0)
  const [dur, setDur] = useState(0)
  const [buffered, setBuffered] = useState(0)
  const [volume, setVolume] = useState(1)
  const [muted, setMuted] = useState(false)
  const [rate, setRate] = useState(1)
  const [full, setFull] = useState(false)
  const [menu, setMenu] = useState(false)
  // Выпадающий список раскладки кинопаба: какой и где. Открывается от
  // своей надписи («Видео», «Аудио», «Субтитры») или от кнопки скорости
  // и прижат к ней, а не накрывает кадр целиком.
  const [pop, setPop] = useState<Pop | null>(null)
  const [idle, setIdle] = useState(false)
  const [tick, setTick] = useState(0)
  const [audioSel, setAudioSel] = useState<string | null>(null)
  const [qualityId, setQualityId] = useState<string | null>(null)
  const [textSel, setTextSel] = useState<string | null>(null)
  // Перемотка тягой: доля полосы под пальцем, пока не отпустили.
  const [scrub, setScrub] = useState<number | null>(null)
  const [hover, setHover] = useState<number | null>(null)
  // Тяга по полосе мышью, а не пальцем: только мышь водит превью кадра.
  const [mouseScrub, setMouseScrub] = useState(false)
  // Подсказка о перемотке двойным тапом: с какой стороны и когда.
  const [flash, setFlash] = useState<{ side: 'left' | 'right'; at: number } | null>(null)
  const [countdown, setCountdown] = useState<number | null>(null)
  const [cancelled, setCancelled] = useState(false)
  const [autoNext, setAutoNext] = useState(() => {
    try { return localStorage.getItem(AUTONEXT_KEY) !== 'off' } catch { return true }
  })
  const [skipMode, setSkipMode] = useState<SkipMode>(readSkipMode)
  // Подсказка после автоматического пропуска: что пропущено, с возвратом.
  const [toast, setToast] = useState<{ skip: Skip; at: number } | null>(null)
  // Палец — основной ввод: тогда во весь экран включается раскладка
  // кинопаба. Угадывается заранее по указателю устройства и подтверждается
  // первым же касанием.
  const [touchMode, setTouchMode] = useState(
    () => typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches,
  )
  // Раскладка кинопаба — на телевизоре и у пальца во весь экран: надписи
  // «Видео / Аудио / Субтитры» под названием, время по краям полосы,
  // центрированный ряд кнопок. Встроенный в страницу плеер на телефоне
  // остаётся компактным, как на компьютере: в окошке на полэкрана крупный
  // ряд из семи кнопок закрывал весь кадр.
  const cine = Boolean(tv) || (touchMode && full)
  const skip = cine ? CINE_SKIP : SKIP
  const anyMenu = menu || pop !== null
  // Сводка — только когда плеер и есть экран: во встроенном окошке телефона
  // она съедала половину строки с названием.
  const showStats = Boolean(stats) && (cine || !touchMode)

  // Выбор, сделанный ядром по предпочтению, отражается здесь: панель —
  // отражение состояния, а не его владелец.
  useEffect(() => { if (audioId !== undefined) setAudioSel(audioId) }, [audioId])
  useEffect(() => { if (textId !== undefined) setTextSel(textId) }, [textId])

  // Состояние читается с самого <video>: он остаётся источником правды,
  // а панель — его отражением. Иначе они разъезжаются при перемотке
  // с клавиатуры или из системного меню.
  useEffect(() => {
    const v = videoRef.current
    if (!v) return
    const sync = () => {
      setPlaying(!v.paused && !v.ended)
      setPos(v.currentTime)
      setDur(Number.isFinite(v.duration) ? v.duration : 0)
      setVolume(v.volume); setMuted(v.muted); setRate(v.playbackRate)
      setBuffered(v.buffered.length ? v.buffered.end(v.buffered.length - 1) : 0)
    }
    // seeking — сразу: timeupdate после перемотки приходит, лишь когда
    // докачались данные, и до тех пор кнопка «пропустить» висела бы над
    // уже пропущенной заставкой.
    const events = ['play', 'pause', 'timeupdate', 'durationchange',
      'volumechange', 'ratechange', 'progress', 'ended', 'loadedmetadata', 'seeking']
    events.forEach(e => v.addEventListener(e, sync))
    // Конец — отдельное состояние, а не «пауза в конце»: ядро после него
    // отпускает движок, и «играть» уже ничего не включит. Снимается
    // только реальным стартом: сброс источника после конца — не он.
    const onEnded = () => setEnded(true)
    const onStart = () => setEnded(false)
    v.addEventListener('ended', onEnded)
    v.addEventListener('play', onStart)
    v.addEventListener('seeking', onStart)
    // Ожидание данных кончается любым из этих событий; на паузе кольцо
    // не нужно — там в центре своё.
    const onWait = () => setBuffering(true)
    const onGo = () => setBuffering(false)
    const goes = ['playing', 'pause', 'canplay', 'seeked', 'ended', 'emptied']
    v.addEventListener('waiting', onWait)
    goes.forEach(e => v.addEventListener(e, onGo))
    sync()
    return () => {
      events.forEach(e => v.removeEventListener(e, sync))
      v.removeEventListener('ended', onEnded)
      v.removeEventListener('play', onStart)
      v.removeEventListener('seeking', onStart)
      v.removeEventListener('waiting', onWait)
      goes.forEach(e => v.removeEventListener(e, onGo))
    }
  }, [videoRef])

  useEffect(() => {
    const on = () => setFull(
      Boolean(document.fullscreenElement) ||
      // iPhone: полный экран там нативный, у самого видео, и ни о каком
      // fullscreenElement он не сообщает — только о режиме представления.
      Boolean((videoRef.current as (HTMLVideoElement & { webkitPresentationMode?: string }) | null)
        ?.webkitPresentationMode === 'fullscreen'),
    )
    document.addEventListener('fullscreenchange', on)
    const v = videoRef.current
    v?.addEventListener('webkitpresentationmodechanged', on)
    return () => {
      document.removeEventListener('fullscreenchange', on)
      v?.removeEventListener('webkitpresentationmodechanged', on)
    }
  }, [videoRef])

  // Панель прячется в покое, но только во время воспроизведения: на паузе
  // она нужна, и во время перемотки тягой — тоже. Таймер перезапускается
  // действием человека (tick), а не ходом времени: завись он от позиции,
  // он перезапускался бы каждые четверть секунды, и панель на телефоне не
  // пряталась бы никогда.
  const scrubbing = scrub !== null
  useEffect(() => {
    if (!playing || anyMenu || scrubbing) { setIdle(false); return }
    const t = setTimeout(() => setIdle(true), IDLE_MS)
    return () => clearTimeout(t)
  }, [playing, anyMenu, scrubbing, tick])

  // Смена раскладки (вход и выход из полного экрана) закрывает меню:
  // открытое в одной раскладке висело бы в другой не на своём месте.
  useEffect(() => { setMenu(false); setPop(null) }, [cine])
  const wake = useCallback(() => { setIdle(false); setTick(t => t + 1) }, [])

  useEffect(() => {
    if (!flash) return
    const t = setTimeout(() => setFlash(null), 700)
    return () => clearTimeout(t)
  }, [flash])

  // ── пропуск заставок и титров ─────────────────────────────────────────
  // Какой разметке верить, решается по длине потока; что показывать —
  // по позиции. Позиция обновляется с timeupdate, то есть несколько раз
  // в секунду, — этого кнопке хватает.
  const skips = useMemo(() => pickSkips(segments ?? [], dur), [segments, dur])
  const cur = skipMode === 'off' || preparing || ended ? null : activeSkip(skips, pos)
  // Титры — только по согласию. Ни отсчёта, ни карточки: человек вправе
  // досмотреть эндинг, и переключать серию у него из-под носа нельзя.
  // Кнопка та же, что у заставки. Титры до самого конца файла при
  // следующей серии ведут к ней («Следующая серия»): после них смотреть
  // нечего. Если после титров что-то есть — анонс, сцена после титров, —
  // кнопка перематывает к нему. Сам плеер переключает серию лишь по
  // настоящему концу — обычным автозапуском.
  const toNext = (sk: Skip | null) => Boolean(sk && next && dur > 0 &&
    (sk.kind === 'outro' || sk.kind === 'preview') && sk.end >= dur - 1)

  // Обратный отсчёт до следующей серии. Действие берётся через ref:
  // объект next пересоздаётся родителем на каждом рендере, и зависимость
  // от него перезапускала бы отсчёт с начала.
  const nextRef = useRef(next)
  nextRef.current = next
  const creditsRef = useRef(onCreditsNext)
  creditsRef.current = onCreditsNext
  const hasNext = Boolean(next)
  /** К следующей серии с титров или анонса: эта серия досмотрена. */
  const nextFromCredits = useCallback(() => {
    creditsRef.current?.()
    nextRef.current?.onSelect()
  }, [])
  // Серии подряд без касаний: счёт живёт в сессии вкладки, потому что
  // плеер пересоздаётся на каждую серию. Любое нажатие его обнуляет.
  const [chain, setChain] = useState(readChain)
  const stillWatching = chain >= MAX_CHAIN
  useEffect(() => {
    const human = () => { if (readChain() !== 0) writeChain(0); setChain(0) }
    addEventListener('pointerdown', human, true)
    addEventListener('keydown', human, true)
    return () => {
      removeEventListener('pointerdown', human, true)
      removeEventListener('keydown', human, true)
    }
  }, [])
  // Отсчёт — только после настоящего конца серии.
  const counting = !stillWatching && ended && !cancelled
  useEffect(() => { if (!ended) setCancelled(false) }, [ended])
  useEffect(() => {
    if (!counting || !hasNext || !autoNext || autoNextSec <= 0) {
      setCountdown(null)
      return
    }
    setCountdown(autoNextSec)
    const started = Date.now()
    const t = setInterval(() => {
      const left = autoNextSec - Math.floor((Date.now() - started) / 1000)
      if (left <= 0) {
        clearInterval(t)
        setCountdown(null)
        writeChain(readChain() + 1)
        nextRef.current?.onSelect()
      } else {
        setCountdown(left)
      }
    }, 250)
    return () => clearInterval(t)
  }, [counting, hasNext, autoNext, autoNextSec])

  /** Нажали «пропустить»: титры до конца файла — к следующей серии,
   *  иначе — к тому, что после отрезка. */
  const doSkip = (sk: Skip) => {
    if (toNext(sk)) {
      nextFromCredits()
      return
    }
    const v = videoRef.current
    if (v) v.currentTime = sk.end
  }

  // Автоматический пропуск — только при естественном входе в отрезок:
  // серия дошла до заставки сама или началась с неё. Человек, перемотавший
  // внутрь заставки, хочет её видеть; отменивший пропуск — тоже. Каждый
  // отрезок пропускается сам не больше раза за загрузку. Титры и анонс
  // сами не пропускаются никогда: досмотреть их — право человека, и после
  // титров бывает сцена.
  const skipsRef = useRef(skips)
  skipsRef.current = skips
  const modeRef = useRef(skipMode)
  modeRef.current = skipMode
  const autoDone = useRef(new Set<string>())
  const prevPos = useRef<number | null>(null)
  const userSeek = useRef(false)
  const selfSeek = useRef(false)
  useEffect(() => {
    if (!preparing) return
    autoDone.current.clear()
    prevPos.current = null
    setToast(null)
  }, [preparing])
  useEffect(() => {
    const v = videoRef.current
    if (!v) return
    const onSeeking = () => {
      if (selfSeek.current) selfSeek.current = false
      else userSeek.current = true
    }
    const onTime = () => {
      const t = v.currentTime
      const prev = prevPos.current
      prevPos.current = t
      if (userSeek.current) { userSeek.current = false; return }
      if (modeRef.current !== 'auto' || v.paused) return
      const s = activeSkip(skipsRef.current, t)
      if (!s || s.kind === 'outro' || s.kind === 'preview') return
      const key = `${s.kind}@${s.start}`
      if (autoDone.current.has(key)) return
      const entered = prev === null ? t - s.start < 1.5 : prev < s.start + 1.5 && t >= prev
      if (!entered) return
      autoDone.current.add(key)
      selfSeek.current = true
      v.currentTime = s.end
      setToast({ skip: s, at: Date.now() })
    }
    v.addEventListener('timeupdate', onTime)
    v.addEventListener('seeking', onSeeking)
    return () => {
      v.removeEventListener('timeupdate', onTime)
      v.removeEventListener('seeking', onSeeking)
    }
  }, [videoRef])
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), TOAST_MS)
    return () => clearTimeout(t)
  }, [toast])
  const undoSkip = () => {
    const v = videoRef.current
    if (v && toast) v.currentTime = toast.skip.start
    setToast(null)
  }
  const pickSkipMode = (m: SkipMode) => {
    setSkipMode(m)
    try { localStorage.setItem(SKIP_KEY, m) } catch { /* приватный режим */ }
  }
  // Кнопка «пропустить» — на всё, что сейчас идёт. На паузе во встроенном
  // окошке телефона её нет: там посередине круг «играть», и места на двоих
  // в полэкрана не хватает.
  const pill = cur && !(touchMode && !cine && !playing) ? cur : null
  const pillText = pill ? (toNext(pill) ? 'Следующая серия' : skipLabel(pill.kind)) : ''

  const toggle = useCallback(() => {
    const v = videoRef.current
    if (!v) return
    if (v.paused) void v.play(); else v.pause()
  }, [videoRef])

  const seekBy = useCallback((sec: number) => {
    const v = videoRef.current
    if (!v) return
    // Длительность бывает неизвестна: пока разбирается плейлист, у потока
    // без конца — всегда. Ограничивать тогда нечем, и `duration || 0`
    // превращал перемотку вперёд в прыжок в самое начало.
    const end = Number.isFinite(v.duration) && v.duration > 0 ? v.duration : Infinity
    v.currentTime = Math.max(0, Math.min(end, v.currentTime + sec))
  }, [videoRef])

  const replay = useCallback(() => {
    setEnded(false)
    void player.current?.replay()
  }, [player])

  const toggleAutoNext = () => setAutoNext(v => {
    try { localStorage.setItem(AUTONEXT_KEY, v ? 'off' : 'on') } catch { /* приватный режим */ }
    return !v
  })

  /**
   * Пульт сейчас принадлежит плееру.
   *
   * Только когда плеер — это и есть экран: он развёрнут во весь экран или
   * фокус внутри него. Видео, играющее в глубине страницы, пульт не
   * забирает: человек листает серии, а не смотрит.
   */
  const ownsRemote = useCallback(() => {
    const b = box.current
    // Развёрнут бывает не сам плеер, а обёртка хозяина вокруг него.
    return Boolean(b && (document.fullscreenElement?.contains(b) || b.contains(document.activeElement)))
  }, [])

  // Горячие клавиши — те же, что везде: иначе человек жмёт пробел и
  // страница прокручивается вместо паузы.
  //
  // На компьютере влево-вправо перематывают из любой кнопки плеера.
  // Открытое меню и поля ввода сохраняют свои стрелки. На телевизоре
  // видимая панель по-прежнему управляется пультом через фокус.
  //
  // На телевизоре, пока панель спрятана, всё иначе: плеер забирает пульт
  // себе. Стрелки перематывают откуда угодно внутри него, OK ставит паузу,
  // вниз открывает панель. Требовать, чтобы фокус стоял ровно на невидимой
  // полосе времени, с пультом нельзя — оттуда и бралось «не ловит фокус».
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null
      if (el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT')) return
      const v = videoRef.current
      if (!v) return
      const remote = Boolean(tv) && idle && ownsRemote()
      wake()
      const seeks = remote || (!tv && !anyMenu && !ended && ownsRemote())
        || Boolean(el?.closest('[data-arrows="x"]') && box.current?.contains(el))
      // Первая стрелка будит панель. Следующие должны продолжать перемотку,
      // даже если до засыпания фокус оставался на другой кнопке.
      if (remote && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
        seekEl.current?.focus({ preventScroll: true })
      }
      switch (e.key) {
        case ' ':
          // Пробел на кнопке — это её нажатие, а не вторая пауза сверху.
          if (el?.tagName === 'BUTTON' || el?.getAttribute('role') === 'checkbox') return
          e.preventDefault(); toggle(); break
        case 'k': toggle(); break
        case 'ArrowLeft': if (seeks) { e.preventDefault(); seekBy(-skip) } break
        case 'ArrowRight': if (seeks) { e.preventDefault(); seekBy(skip) } break
        // Вниз — в панель, к кнопкам; вверх её просто будит (wake выше).
        case 'ArrowDown':
          if (remote) { e.preventDefault(); seekEl.current?.focus({ preventScroll: true }) }
          break
        case 'ArrowUp': if (remote) e.preventDefault(); break
        // OK по спрятанной панели — пауза. С открытой панелью OK нажимает
        // то, что в фокусе, и перехватывать его нельзя.
        case 'Enter':
          // Кнопка пропуска под фокусом — нажимается она.
          if (remote && !el?.closest('.kp-skip')) { e.preventDefault(); toggle() }
          break
        // S — «пропустить», как у онлайн-кинотеатров.
        case 's': case 'S':
          if (pill) doSkip(pill)
          break
        case 'm': v.muted = !v.muted; break
        case 'f': void toggleFull(); break
        case 'n': case 'MediaTrackNext': nextRef.current?.onSelect(); break
        case 'MediaPlayPause': toggle(); break
        case 'MediaPlay': void v.play(); break
        case 'MediaPause': case 'MediaStop': v.pause(); break
        case 'MediaFastForward': seekBy(skip); break
        case 'MediaRewind': seekBy(-skip); break
      }
    }
    addEventListener('keydown', on)
    return () => removeEventListener('keydown', on)
  })

  // Телевизор: первый старт серии — сразу во весь экран. Серию выбрали,
  // чтобы смотреть, а не чтобы разглядывать её в окошке посреди страницы.
  // Ровно один раз на источник: иначе выход из полного экрана откатывался
  // бы сам при каждой паузе. Запрос вне жеста человека браузер может и
  // отклонить — тогда просто останемся в окне.
  const wentFull = useRef(false)
  useEffect(() => { if (preparing) wentFull.current = false }, [preparing])
  useEffect(() => {
    if (!tv || !playing || wentFull.current) return
    wentFull.current = true
    if (!document.fullscreenElement) void toggleFull()
  })

  // С пульта нажимают то, что в фокусе: открывшееся меню и экран «дальше»
  // сразу отдают фокус своей первой кнопке, иначе до них не добраться.
  // Выпадающий список — сразу на выбранном пункте: с пульта так видно,
  // что стоит сейчас, и до соседнего один шаг.
  const popKind = pop?.kind
  useEffect(() => {
    if (!menu && !popKind) return
    const m = box.current?.querySelector('.kp-menu')
    ;(m?.querySelector<HTMLElement>('button.on') ?? m?.querySelector<HTMLElement>('button'))?.focus()
  }, [menu, popKind])
  useEffect(() => {
    if (ended) box.current?.querySelector<HTMLElement>('.kp-end-go')?.focus()
  }, [ended])
  // Пульт: всплывшая кнопка пропуска сразу под фокусом — OK
  // нажимает её, как на любом ТВ-кинотеатре. Только если человек не ходит
  // сейчас по панели. Исчезнув, она возвращает фокус полосе времени,
  // иначе он падал бы на страницу под плеером.
  const offer = pill ? `skip:${pill.kind}@${pill.start}` : ''
  useEffect(() => {
    const b = box.current
    if (!b) return
    if (offer) {
      if (!tv || !ownsRemote()) return
      const a = document.activeElement
      if (!idle && a && a !== seekEl.current && b.contains(a) && !a.closest('.kp-skip')) return
      b.querySelector<HTMLElement>('.kp-skip-go')?.focus({ preventScroll: true })
      return
    }
    const a = document.activeElement
    if (tv && (!a || a === document.body)) seekEl.current?.focus({ preventScroll: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [offer])

  /** Что разворачивать: обёртка хозяина, если он её дал, иначе сам плеер. */
  const fullTarget = () => fullscreenRef?.current ?? box.current
  // Поворот запрашивали мы: выйдя из полного экрана, вернём как было.
  const lockedOrientation = useRef(false)
  // lock/unlock в типах DOM нет: API Android-only по факту, и живёт
  // не во всех движках. Ловим руками, с проверками на месте вызова.
  const orientation = () =>
    (screen as { orientation?: ScreenOrientation & {
      lock?: (o: string) => Promise<void>; unlock?: () => void
    } }).orientation
  async function toggleFull() {
    if (document.fullscreenElement) {
      await document.exitFullscreen().catch(() => {})
      if (lockedOrientation.current) {
        lockedOrientation.current = false
        try { orientation()?.unlock?.() } catch { /* уже отпущено */ }
      }
      return
    }
    const el = fullTarget()
    if (el && typeof el.requestFullscreen === 'function') {
      await el.requestFullscreen().catch(() => {})
      // Телефон держат вертикально, а кино почти всегда горизонтальное:
      // просим поворот, когда кадр того просит. Где не разрешено — смотрят
      // как получилось, это подсказка, а не правило.
      const v = videoRef.current
      if (v && v.videoWidth > v.videoHeight) {
        try {
          await orientation()?.lock?.('landscape')
          lockedOrientation.current = true
        } catch { /* не поддерживается или не разрешено */ }
      }
      return
    }
    // iPhone: стандартного полного экрана у страниц нет вовсе — запрос
    // глотает ошибку, и кнопка была бы мёртвой. Разворачивается
    // только само видео, системными средствами и без нашей панели.
    const v = videoRef.current as (HTMLVideoElement & { webkitEnterFullscreen?: () => void }) | null
    if (typeof v?.webkitEnterFullscreen === 'function') {
      try { v.webkitEnterFullscreen() } catch { /* и тут не вышло */ }
    }
  }

  async function togglePiP() {
    const v = videoRef.current
    if (!v) return
    // Внутри оболочки уменьшать окно умеет только система: у WebView своего
    // PiP нет, и запрос к нему отклоняется молча. В форточку попадает всё
    // окно, поэтому сначала разворачиваем плеер — иначе там оказалась бы
    // вся страница целиком.
    if (!document.pictureInPictureEnabled && onPiP) {
      if (!document.fullscreenElement) {
        await fullTarget()?.requestFullscreen().catch(() => {})
      }
      await onPiP(v.videoWidth || 16, v.videoHeight || 9)
      return
    }
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture()
      else await v.requestPictureInPicture()
    } catch { /* браузер может не разрешить — не повод падать */ }
  }
  // Кнопки, которая заведомо ничего не сделает, быть не должно.
  const canPiP = (typeof document !== 'undefined' && document.pictureInPictureEnabled) || Boolean(onPiP)

  // ── сводка о потоке ───────────────────────────────────────────────────
  // Раз в секунду и только пока её видно: числа берутся у движка и у видео,
  // и опрашивать их в пустоту незачем. Гаснет вместе с панелью — за это
  // отвечает класс idle на контейнере.
  const [info, setInfo] = useState<string[]>([])
  useEffect(() => {
    if (!showStats || idle) return
    const mbit = (kbps: number) => `${(kbps / 1000).toFixed(1).replace('.', ',')} Мбит/с`
    const read = () => {
      const v = videoRef.current
      if (!v) return
      const s = player.current?.stats() ?? {}
      // «avc1.640028, mp4a.40.2» — половина ширины ради цифр, которые ничего
      // не говорят. Оставляем семейство кодека, как это делает kinopub.
      const codecs = s.codecs
        ? s.codecs.split(',').map(c => c.trim().split('.')[0]).filter(Boolean).join(', ')
        : ''
      const top = [
        codecs,
        v.videoWidth ? `${v.videoWidth}×${v.videoHeight}` : '',
        s.levelKbps ? mbit(s.levelKbps) : '',
      ].filter(Boolean).join(' · ')

      const buf = v.buffered.length
        ? Math.max(0, v.buffered.end(v.buffered.length - 1) - v.currentTime) : 0
      const bottom = [
        s.bandwidthKbps ? `канал ${mbit(s.bandwidthKbps)}` : '',
        `буфер ${Math.round(buf)} с`,
      ].filter(Boolean).join(' · ')

      // Потерянные кадры показываем, только когда они есть: строка «0»
      // висела бы всегда и ничего не сообщала.
      const q = v.getVideoPlaybackQuality?.()
      const dropped = q && q.droppedVideoFrames > 0 ? `пропущено кадров ${q.droppedVideoFrames}` : ''

      setInfo([top, bottom, dropped].filter(Boolean))
    }
    read()
    const id = setInterval(read, 1000)
    return () => clearInterval(id)
  }, [showStats, idle, videoRef, player])

  // ── тапы и тяга по кадру ──────────────────────────────────────────────
  // Мышь: клик — пауза, двойной — полный экран. Палец: одиночный тап
  // показывает и прячет панель, двойной у края перематывает на 10 секунд,
  // как в любом мобильном плеере. Различаются по pointerType, а не по
  // устройству: на планшете с мышью бывает и то и другое.
  const touch = useRef(false)
  const lastTap = useRef<{ t: number; x: number } | null>(null)
  const tapTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Тяга по самому кадру: горизонтальный драг перематывает, как у YouTube.
  // Уживается с тапами: жест и тяга различаются движением — у тяги палец
  // смещается по горизонтали до отпускания, и последующий клик глушится.
  const drag = useRef<{ id: number; x0: number; t0: number; pulled: boolean } | null>(null)
  const dragClick = useRef(false)
  const [dragScrub, setDragScrub] = useState<number | null>(null)

  const onVideoPointerDown = (e: React.PointerEvent<HTMLElement>) => {
    touch.current = e.pointerType === 'touch'
    if (e.pointerType === 'touch') setTouchMode(true)
    dragClick.current = false
    const v = videoRef.current
    if (e.pointerType !== 'touch' || !dur || !v) return
    drag.current = { id: e.pointerId, x0: e.clientX, t0: v.currentTime, pulled: false }
  }
  const onVideoPointerMove = (e: React.PointerEvent<HTMLElement>) => {
    const d = drag.current
    if (!d || e.pointerId !== d.id) return
    const dx = e.clientX - d.x0
    // Порог отличает тап от тяги: до него это ещё тап, и панель слушается.
    if (!d.pulled && Math.abs(dx) < 24) return
    if (!d.pulled) {
      d.pulled = true
      if (tapTimer.current) { clearTimeout(tapTimer.current); tapTimer.current = null }
      lastTap.current = null
      e.currentTarget.setPointerCapture(e.pointerId)
      wake()
      setDragScrub(d.t0)
    }
    // Полный свайп через кадр — четверть фильма, но не меньше двух и не
    // больше пятнадцати минут: на коротком ролике иначе перелетишь через
    // весь ролик одним движением, на длинном — не разгонишься.
    const w = e.currentTarget.getBoundingClientRect().width
    const span = Math.min(Math.max(dur / 4, 120), 900)
    setDragScrub(Math.min(dur, Math.max(0, d.t0 + (dx / Math.max(1, w)) * span)))
  }
  const onVideoPointerUp = (e: React.PointerEvent<HTMLElement>) => {
    const d = drag.current
    drag.current = null
    if (!d?.pulled) return
    dragClick.current = true
    const v = videoRef.current
    if (v && dragScrub !== null) v.currentTime = dragScrub
    setDragScrub(null)
    setTick(t => t + 1)
  }
  const onVideoPointerCancel = () => {
    drag.current = null
    setDragScrub(null)
  }

  const onVideoClick = (e: React.MouseEvent<HTMLElement>) => {
    // Тяга кончилась отпусканием — кликом она быть не должна: после
    // перемотки панель не вздрагивает лишний раз.
    if (dragClick.current) { dragClick.current = false; return }
    if (!touch.current) { toggle(); return }
    const now = Date.now()
    const prev = lastTap.current
    if (prev && now - prev.t < DOUBLE_TAP_MS && Math.abs(prev.x - e.clientX) < 80) {
      lastTap.current = null
      if (tapTimer.current) clearTimeout(tapTimer.current)
      const r = e.currentTarget.getBoundingClientRect()
      const f = (e.clientX - r.left) / r.width
      if (f < 0.35) { seekBy(-skip); setFlash({ side: 'left', at: now }) }
      else if (f > 0.65) { seekBy(skip); setFlash({ side: 'right', at: now }) }
      else toggle()
      return
    }
    lastTap.current = { t: now, x: e.clientX }
    tapTimer.current = setTimeout(() => {
      lastTap.current = null
      setIdle(v => !v)
      setTick(t => t + 1)
    }, DOUBLE_TAP_MS)
  }

  // ── полоса времени ────────────────────────────────────────────────────
  const fracAt = (clientX: number) => {
    const el = seekEl.current
    if (!el) return 0
    const r = el.getBoundingClientRect()
    return Math.min(1, Math.max(0, (clientX - r.left) / r.width))
  }
  const onSeekDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dur) return
    e.preventDefault()
    e.currentTarget.setPointerCapture(e.pointerId)
    setScrub(fracAt(e.clientX))
    setMouseScrub(e.pointerType === 'mouse')
    wake()
  }
  const onSeekMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (scrub !== null) setScrub(fracAt(e.clientX))
    else if (e.pointerType === 'mouse') setHover(fracAt(e.clientX))
  }
  const onSeekUp = (e: React.PointerEvent<HTMLDivElement>) => {
    if (scrub === null) return
    const v = videoRef.current
    if (v && dur) v.currentTime = fracAt(e.clientX) * dur
    setScrub(null)
    setMouseScrub(false)
    if (e.pointerType !== 'mouse') setHover(null)
  }

  // ── выпадающие списки раскладки кинопаба ──────────────────────────────
  // Координаты — от контейнера плеера в его собственных пикселях: на
  // телевизоре страница увеличена zoom, и прямоугольники экрана приходят
  // в масштабе, а left/top внутри — без него.
  const openPop = (kind: PopKind, el: HTMLElement) => {
    if (pop?.kind === kind) { setPop(null); return }
    const b = box.current
    if (!b) return
    const br = b.getBoundingClientRect()
    const r = el.getBoundingClientRect()
    const k = br.width / (b.offsetWidth || br.width) || 1
    setMenu(false)
    setPop(kind === 'speed'
      ? { kind, up: true, x: Math.max(8, (br.right - r.right) / k), y: (br.bottom - r.top) / k + 6 }
      : { kind, up: false, x: Math.max(8, (r.left - br.left) / k), y: (r.bottom - br.top) / k + 4 })
  }
  const closeMenus = () => { setMenu(false); setPop(null) }
  // Список у правого края не должен вылезать за кадр: сдвигаем влево
  // после того, как стала известна его ширина.
  useLayoutEffect(() => {
    const m = popEl.current
    const b = box.current
    if (!m || !b || !pop || pop.up) return
    const over = m.offsetLeft + m.offsetWidth - (b.clientWidth - 8)
    if (over > 0) m.style.left = `${Math.max(8, m.offsetLeft - over)}px`
  }, [pop])

  // «Следующая» с титров — тоже досмотренная серия.
  const goNext = () => {
    if (cur?.kind === 'outro' || cur?.kind === 'preview') nextFromCredits()
    else next?.onSelect()
  }
  const goPrev = () => {
    if (prev) { prev.onSelect(); return }
    const v = videoRef.current
    if (v) v.currentTime = 0
  }

  // ── пункты меню: одни и те же для плоского меню мыши и для списков ────
  // done — что сделать после выбора: список раскладки кинопаба закрывается,
  // плоское меню мыши остаётся открытым.
  const mark = (on: boolean) => <span className="mark">{on ? <IconCheck /> : null}</span>
  const qualityItems = (done?: () => void) => (
    <>
      <button className={qualityId === null ? 'on' : ''}
              onClick={() => { player.current?.selectQuality(null); setQualityId(null); done?.() }}>
        {mark(qualityId === null)}Авто
      </button>
      {qualities.map(q => (
        <button key={q.id} className={q.id === qualityId ? 'on' : ''}
                onClick={() => { player.current?.selectQuality(q.id); setQualityId(q.id); done?.() }}>
          {mark(q.id === qualityId)}{q.height}p
        </button>
      ))}
    </>
  )
  // Группа приходит готовой с хозяина; и один пункт показывается: по нему
  // видно, какая озвучка или площадка играют.
  const choiceItems = (tab: 'audio' | 'video', done?: () => void) => choices
    .filter(g => g.items.length > 0 && (g.tab ?? 'audio') === tab).map(g => (
      <div key={g.title} className="kp-choice">
        <h4>{g.title}</h4>
        {g.items.map(it => (
          <button key={it.id} className={it.id === g.selected ? 'on' : ''}
                  onClick={() => { if (it.id !== g.selected) g.onSelect(it.id); done?.() }}>
            {mark(it.id === g.selected)}{it.label}
            {it.hint && <span className="kp-hint">{it.hint}</span>}
          </button>
        ))}
      </div>
    ))
  const audioChoices = choices.some(g => g.items.length > 0 && (g.tab ?? 'audio') === 'audio')
  // Дорожка и одна показывается: без раздела не видно, что звук вообще
  // есть и какой он.
  const audioItems = (done?: () => void) => audioTracks && audio.length > 0 && (
    <>
      <h4>Озвучка</h4>
      {audio.map(a => (
        <button key={a.id} className={a.id === audioSel ? 'on' : ''}
                onClick={() => { player.current?.selectAudio(a.id); setAudioSel(a.id); done?.() }}>
          {mark(a.id === audioSel)}{a.label}
        </button>
      ))}
    </>
  )
  const subsItems = (done?: () => void) => (
    <>
      <button className={textSel === null ? 'on' : ''}
              onClick={() => { player.current?.selectText(null); setTextSel(null); done?.() }}>
        {mark(textSel === null)}Выключены
      </button>
      {subtitles.map(t => (
        <button key={t.id} className={t.id === textSel ? 'on' : ''}
                onClick={() => { player.current?.selectText(t.id); setTextSel(t.id); done?.() }}>
          {mark(t.id === textSel)}{t.label || t.language}
        </button>
      ))}
    </>
  )
  const speedItems = (done?: () => void) => SPEEDS.map(sp => (
    <button key={sp} className={rate === sp ? 'on' : ''}
            onClick={() => { const v = videoRef.current; if (v) v.playbackRate = sp; done?.() }}>
      {mark(rate === sp)}{sp === 1 ? 'Обычная' : `${sp}×`}
    </button>
  ))

  // Показанное время: тяга по кадру, затем тяга по полосе, затем ход игры.
  const shownPos = dragScrub ?? (scrub !== null ? scrub * dur : pos)
  const shownFrac = dur ? shownPos / dur : 0
  const pct = shownFrac * 100
  const bufPct = dur ? (buffered / dur) * 100 : 0
  const bubble = scrub ?? hover
  // Под курсором полоска заставки или титров — подсказка называет её.
  const bubbleSkip = bubble === null || skipMode === 'off' ? undefined
    : skips.find(s => bubble * dur >= s.start && bubble * dur < s.end)

  // ── превью кадра под курсором полосы ──────────────────────────────────
  // Кадр в точке наведения рисует второй плеер из ядра (core/preview):
  // заводится первым же наведением, гаснет после 15 секунд без них.
  // Ведёт его только мышь: палец при тяге по полосе увидел бы вечную
  // докачку сегментов через VPN, и пользы в ней меньше, чем трафика.
  const previewRef = useRef<HoverPreview | null>(null)
  const thumbRef = useRef<HTMLCanvasElement | null>(null)
  const [frame, setFrame] = useState<PreviewFrame | null>(null)
  const [previewOk, setPreviewOk] = useState(true)
  useEffect(() => {
    const p = new HoverPreview({ getSource: () => player.current?.source ?? null })
    previewRef.current = p
    const off = [
      p.onFrame((f) => {
        setFrame(f)
        const c = thumbRef.current
        c?.getContext('2d')?.drawImage(f.canvas, 0, 0, c.width, c.height)
      }),
      p.onAlive(setPreviewOk),
    ]
    return () => { off.forEach(f => f()); p.stop(); previewRef.current = null; setFrame(null) }
  }, [player])
  const previewing = previewOk && dur > 0 &&
    (hover !== null || (scrub !== null && mouseScrub))
  // Просьба кадра — на каждое движение курсора; дибаунс и экономия внутри.
  useEffect(() => {
    const p = previewRef.current
    if (!p) return
    if (bubble === null || dur <= 0 || (hover === null && !(scrub !== null && mouseScrub))) {
      p.cancel()
      return
    }
    p.request(bubble * dur)
  }, [bubble, dur, hover, scrub, mouseScrub])

  const pipButton = canPiP && (
    <button className="kp-btn kp-pip" onClick={togglePiP} aria-label="Картинка в картинке">
      <IconPiP />
    </button>
  )
  const fullButton = (
    <button className="kp-btn kp-full" onClick={toggleFull}
            aria-label={full ? 'Выйти из полноэкранного' : 'Полный экран'}>
      {full ? <IconExitFull /> : <IconFull />}
    </button>
  )
  const playButton = (
    <button className="kp-btn kp-play" onClick={toggle} aria-label={playing ? 'Пауза' : 'Играть'}>
      {playing ? <IconPause /> : <IconPlay />}
    </button>
  )

  return (
    <div
      ref={box}
      className={`kp${idle ? ' idle' : ''}${cine ? ' cine' : ''}${touchMode ? ' touch' : ''}`}
      data-arrows={!tv && !anyMenu && !ended ? 'x' : undefined}
      onPointerMove={e => { if (e.pointerType === 'mouse') wake() }}
      onMouseLeave={() => playing && setIdle(true)}
      onDoubleClick={() => { if (!touch.current) void toggleFull() }}
    >
      <video ref={videoRef} playsInline
             onPointerDown={onVideoPointerDown} onPointerMove={onVideoPointerMove}
             onPointerUp={onVideoPointerUp} onPointerCancel={onVideoPointerCancel}
             onPointerLeave={onVideoPointerCancel}
             onClick={onVideoClick} />

      {/* Затемнение кадра, пока видна панель: полосами сверху и снизу, где
          надписи и кнопки, и слегка по всему кадру — на светлой сцене
          белое иначе не читается. Первым после видео и без z-index: всё
          остальное рисуется поверх него. */}
      {(cine || touchMode) && <div className="kp-scrim" aria-hidden />}

      {/* Тяга по кадру: под пальцем видно, куда перематывает. */}
      {dragScrub !== null && dur > 0 && (
        <div className="kp-dragtime" aria-hidden>
          {fmt(dragScrub)} <span className="arrow">{dragScrub > pos ? '→' : '←'}</span>
        </div>
      )}

      {/* Шапка: «назад», название и сводка в одну строку, под ними у
          раскладки кинопаба — надписи разделов настроек. */}
      {(title || cine || (showStats && info.length > 0)) && (
        <div className="kp-top">
          <div className="kp-topline">
            {cine && full && (
              <button className="kp-btn kp-exit" onClick={toggleFull} aria-label="Свернуть">
                <IconArrowBack />
              </button>
            )}
            {title && <div className="kp-title">{title}</div>}
            {showStats && info.length > 0 && (
              <div className="kp-stats" aria-hidden>
                {info.map((line, i) => <div key={i}>{line}</div>)}
              </div>
            )}
            {cine && pipButton}
          </div>
          {cine && (
            <div className="kp-tabs">
              {TABS.map(t => (
                <button key={t.id} className={`kp-tab${pop?.kind === t.id ? ' on' : ''}`}
                        aria-haspopup="menu" aria-expanded={pop?.kind === t.id}
                        onClick={e => openPop(t.id, e.currentTarget)}>
                  {t.label}
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      {notice && <div className="kp-notice" role="status">{notice}</div>}

      {flash && (
        <div key={flash.at} className={`kp-flash ${flash.side}`} aria-hidden>
          {flash.side === 'left' ? `−${skip} с` : `+${skip} с`}
        </div>
      )}

      {/* Круг «играть» на паузе — у мыши и на телевизоре (пульту нужна
          цель посреди экрана). У пальца во весь экран его нет, как у
          кинопаба: пауза и так посередине ряда, второй кнопке там не место. */}
      {!playing && !preparing && !ended && (!cine || tv) && (
        <button className="kp-center" aria-label="Воспроизвести" data-arrows="x"
                onClick={() => {
                  toggle()
                  // На телевизоре смотрят во весь экран: страница под плеером
                  // с дивана не нужна. Заодно полный экран замыкает на плеере
                  // и пульт, и стрелки — искать фокус больше не нужно. Здесь
                  // запрос наверняка разрешён: это прямое нажатие человека.
                  if (tv && !document.fullscreenElement) { wentFull.current = true; void toggleFull() }
                  // Кнопка сейчас исчезнет вместе с фокусом; с пульта дальше
                  // ждут перемотку — она на полосе времени. И плеер целиком
                  // в кадр: панель внизу иначе остаётся за краем экрана.
                  seekEl.current?.focus({ preventScroll: true })
                  box.current?.scrollIntoView({ block: 'nearest', behavior: tv ? 'auto' : 'smooth' })
                }}>
          <IconPlay />
        </button>
      )}
      {(preparing || (buffering && playing)) && (
        <div className="kp-wait" aria-hidden><div className="kp-spin" /></div>
      )}

      {ended && !preparing && (
        <div className="kp-end">
          {next ? (
            <>
              <div className="kp-end-k">{stillWatching ? 'Вы ещё смотрите?' : 'Дальше'}</div>
              <div className="kp-end-t">{next.label}</div>
              <div className="kp-end-row">
                <button className="kp-end-go" onClick={next.onSelect}>
                  <IconPlay /> Смотреть{countdown !== null ? ` · ${countdown}` : ''}
                </button>
                {countdown !== null
                  ? <button className="kp-end-alt" onClick={() => setCancelled(true)}>Остаться</button>
                  : <button className="kp-end-alt" onClick={replay}><IconReplay /> Заново</button>}
              </div>
            </>
          ) : (
            <div className="kp-end-row">
              <button className="kp-end-go" onClick={replay}><IconReplay /> Смотреть заново</button>
            </div>
          )}
        </div>
      )}

      {/* Пропуск: кнопка на время отрезка и подсказка после пропуска сам.
          Видны и при спрятанной панели: ради них её никто не будит. */}
      {pill && (
        <div className="kp-skip" onClick={e => e.stopPropagation()}>
          <button className="kp-skip-go" onClick={() => doSkip(pill)}>{pillText}</button>
        </div>
      )}
      {toast && !pill && (
        <div className="kp-skip" onClick={e => e.stopPropagation()}>
          <span className="kp-skip-note">{skippedLabel(toast.skip.kind)}</span>
          <button className="kp-skip-undo" onClick={undoSkip}>Вернуть</button>
        </div>
      )}
      <div className="kp-bar" onClick={e => e.stopPropagation()}>
        {/* Время по краям полосы — у кинопаба; у мыши оно в ряду. */}
        <div className="kp-seekrow">
          {cine && <span className="kp-time">{fmt(shownPos)}</span>}
          <div
            ref={seekEl}
            className={`kp-seek${scrub !== null || dragScrub !== null ? ' active' : ''}`}
            role="slider" tabIndex={0} data-arrows="x" aria-label="Позиция" aria-valuemin={0} aria-valuemax={Math.round(dur)}
            aria-valuenow={Math.round(shownFrac * dur)} aria-valuetext={fmt(shownFrac * dur)}
            onPointerDown={onSeekDown} onPointerMove={onSeekMove}
            onPointerUp={onSeekUp} onPointerCancel={() => { setScrub(null); setMouseScrub(false) }}
            onPointerLeave={() => setHover(null)}
          >
            <div className="kp-seek-track">
              <div className="kp-seek-buf" style={{ width: `${bufPct}%` }} />
              {/* Заставка, титры, пересказ — полосками: видно заранее, где
                  они и сколько длятся. Только разметка, которой плеер верит
                  для этого потока (pickSkips); пройденное закрывает зелёный. */}
              {skipMode !== 'off' && dur > 0 && skips.map(s => (
                <div key={`${s.kind}-${s.start}`} className={`kp-seek-seg ${s.kind}`}
                     style={{ left: `${(s.start / dur) * 100}%`, width: `${((s.end - s.start) / dur) * 100}%` }} />
              ))}
              <div className="kp-seek-fill" style={{ width: `${pct}%` }} />
              <div className="kp-seek-knob" style={{ left: `${pct}%` }} />
            </div>
            {bubble !== null && dur > 0 && (
              // У краёв шкалы подсказка прижимается к краю, а не режется
              // наполовину: с кадром она стала шириной с превью.
              <div className={`kp-bubble${previewing ? ' has-thumb' : ''}`} style={{
                left: `${bubble * 100}%`,
                transform: `translateX(${bubble < 0.12 ? 0 : bubble > 0.88 ? -100 : -50}%)`,
              }}>
                {previewing && (
                  <div className={`kp-thumb${frame &&
                      Math.abs(frame.time - bubble * dur) <= PREVIEW_MATCH_S ? '' : ' load'}`}>
                    <canvas ref={(el) => {
                      thumbRef.current = el
                      // Пока курсора не было, канвас был размонтирован:
                      // вернём последний готовый кадр, не дожидаясь новой
                      // перемотки.
                      if (el && frame) el.getContext('2d')?.drawImage(frame.canvas, 0, 0, el.width, el.height)
                    }} width={PREVIEW_W} height={PREVIEW_H} aria-hidden />
                  </div>
                )}
                {bubbleSkip && <span className="kp-bubble-seg">{skipName(bubbleSkip.kind)}</span>}
                {fmt(bubble * dur)}
              </div>
            )}
          </div>
          {cine && <span className="kp-time">{fmt(dur)}</span>}
        </div>

        {cine ? (
          // Ряд кинопаба: полный экран, предыдущая, −15, пауза, +15,
          // следующая, скорость. Недоступное гаснет, но место держит —
          // пауза остаётся посередине.
          <div className="kp-row">
            {fullButton}
            <button className="kp-btn kp-prev" onClick={goPrev}
                    aria-label={prev ? 'Предыдущая серия' : 'С начала'}
                    title={prev ? `Назад: ${prev.label}` : undefined}>
              <IconPrev />
            </button>
            <button className="kp-btn kp-back" onClick={() => seekBy(-skip)} aria-label={`Назад ${skip} секунд`}>
              <IconSkip sec={skip} />
            </button>
            {playButton}
            <button className="kp-btn kp-fwd" onClick={() => seekBy(skip)} aria-label={`Вперёд ${skip} секунд`}>
              <IconSkip sec={skip} fwd />
            </button>
            <button className="kp-btn kp-next" onClick={goNext} disabled={!next}
                    aria-label="Следующая серия" title={next ? `Дальше: ${next.label}` : undefined}>
              <IconNext />
            </button>
            <button className={`kp-btn kp-speed${pop?.kind === 'speed' ? ' on' : ''}`}
                    onClick={e => openPop('speed', e.currentTarget)} aria-label="Скорость"
                    aria-haspopup="menu" aria-expanded={pop?.kind === 'speed'}>
              <IconSpeed />
            </button>
          </div>
        ) : (
          <div className="kp-row">
            {playButton}
            <button className="kp-btn kp-back" onClick={() => seekBy(-skip)} aria-label={`Назад ${skip} секунд`}>
              <IconSkip sec={skip} />
            </button>
            <button className="kp-btn kp-fwd" onClick={() => seekBy(skip)} aria-label={`Вперёд ${skip} секунд`}>
              <IconSkip sec={skip} fwd />
            </button>
            {next && (
              <button className="kp-btn kp-next" onClick={goNext}
                      aria-label="Следующая серия" title={`Дальше: ${next.label}`}>
                <IconNext />
              </button>
            )}

            <span className="kp-time">{fmt(shownFrac * dur)} / {fmt(dur)}</span>

            <div className="kp-vol">
              <button className="kp-btn"
                      onClick={() => { const v = videoRef.current; if (v) v.muted = !v.muted }}
                      aria-label={muted ? 'Включить звук' : 'Выключить звук'}>
                {muted || volume === 0 ? <IconMute /> : volume <= 0.5 ? <IconVolumeLow /> : <IconVolume />}
              </button>
              <input type="range" min={0} max={1} step={0.02}
                     value={muted ? 0 : volume} aria-label="Громкость"
                     style={{ '--v': muted ? 0 : volume } as CSSProperties}
                     onChange={e => {
                       const v = videoRef.current
                       if (!v) return
                       v.volume = Number(e.target.value); v.muted = false
                     }} />
            </div>

            <span className="kp-spacer" />

            <button className={`kp-btn kp-set${menu ? ' on' : ''}`}
                    onClick={() => { setPop(null); setMenu(m => !m) }} aria-label="Настройки">
              <IconSettings />
            </button>
            {pipButton}
            {fullButton}
          </div>
        )}
      </div>

      {/* Подложка под открытым меню ловит тап мимо него и закрывает. У мыши
          она ещё и приглушает кадр: плоское меню стоит поверх видео. */}
      {anyMenu && <div className="kp-shade" onClick={closeMenus} aria-hidden />}

      {pop && (
        <div ref={popEl} className="kp-menu kp-pop" role="menu" onClick={e => e.stopPropagation()}
             style={pop.up
               ? { right: pop.x, bottom: pop.y, left: 'auto', top: 'auto', maxHeight: `calc(100% - ${Math.round(pop.y + 8)}px)` }
               : { left: pop.x, top: pop.y, right: 'auto', bottom: 'auto', maxHeight: `calc(100% - ${Math.round(pop.y + 8)}px)` }}>
          {/* Выбор хозяина под «Видео» (площадка) — над качествами: их
              набор от него и зависит, и пунктов в нём мало. */}
          {pop.kind === 'video' && <>{choiceItems('video', closeMenus)}<h4>Качество</h4>{qualityItems(closeMenus)}</>}
          {pop.kind === 'audio' && (
            audioChoices || (audioTracks && audio.length > 0)
              ? <>{choiceItems('audio', closeMenus)}{audioItems(closeMenus)}</>
              : <div className="kp-none">Дорожка одна, выбирать не из чего</div>
          )}
          {pop.kind === 'subs' && subsItems(closeMenus)}
          {pop.kind === 'speed' && <><h4>Скорость</h4>{speedItems(closeMenus)}</>}
        </div>
      )}

      {menu && !cine && (
        <div className="kp-menu" onClick={e => e.stopPropagation()}>
          {choiceItems('audio')}
          {audioItems()}
          {choiceItems('video')}
          {qualities.length > 0 && <><h4>Качество</h4>{qualityItems()}</>}
          <h4>Скорость</h4>
          {speedItems()}
          {subtitles.length > 0 && <><h4>Субтитры</h4>{subsItems()}</>}
          {next && (
            <>
              <h4>После серии</h4>
              <button className={autoNext ? 'on' : ''} onClick={toggleAutoNext}>
                {mark(autoNext)}Следующая автоматически
              </button>
            </>
          )}
          {segments !== undefined && (
            <>
              <h4>Заставки и титры</h4>
              {SKIP_MODES.map(m => (
                <button key={m.id} className={skipMode === m.id ? 'on' : ''} onClick={() => pickSkipMode(m.id)}>
                  {mark(skipMode === m.id)}{m.label}
                </button>
              ))}
            </>
          )}
        </div>
      )}
    </div>
  )
}

const SKIP_MODES: { id: SkipMode; label: string }[] = [
  { id: 'button', label: 'Пропускать кнопкой' },
  { id: 'auto', label: 'Пропускать сами' },
  { id: 'off', label: 'Не предлагать' },
]

type PopKind = 'video' | 'audio' | 'subs' | 'speed'
interface Pop { kind: PopKind; x: number; y: number; up: boolean }
const TABS: { id: PopKind; label: string }[] = [
  { id: 'video', label: 'Видео' }, { id: 'audio', label: 'Аудио' }, { id: 'subs', label: 'Субтитры' },
]

function fmt(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) return '0:00'
  const s = Math.floor(sec % 60)
  const m = Math.floor((sec / 60) % 60)
  const h = Math.floor(sec / 3600)
  const mm = h > 0 ? String(m).padStart(2, '0') : String(m)
  return `${h > 0 ? `${h}:` : ''}${mm}:${String(s).padStart(2, '0')}`
}
