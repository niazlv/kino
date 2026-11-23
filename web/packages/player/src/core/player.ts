import { canUseNativeHls, type EngineStats, type PlaybackEngine } from './engine/engine'
import { HlsJsEngine } from './engine/hlsjs'
import { MediaBridge } from './media'
import { NativeEngine } from './engine/native'
import { SessionRecorder } from './telemetry'
import { StallWatch, type StallTrip } from './stallwatch'
import type {
  EventName, Listener, PlayerEvents, PlayerSource, ProgressSink, SessionStats, TelemetrySink,
} from './types'

export interface PlayerOptions {
  /** Куда отдавать прогресс. Не задан — плеер просто не репортит. */
  progress?: ProgressSink
  telemetry?: TelemetrySink
  progressIntervalMs?: number
  /** Подмена движка в тестах и на демо-стенде. */
  engineFactory?: (video: HTMLVideoElement) => PlaybackEngine
  /** Подписки с момента создания: событие о выборе дорожки при загрузке
   *  не должно проскочить мимо тех, кто подписался бы после load(). */
  on?: { [E in EventName]?: Listener<E> }
  /**
   * Как сравнивать подпись звуковой дорожки с запомненной. По умолчанию —
   * без регистра. Хозяин знает свои площадки: «01. Одноголосый. Воротилин
   * (RUS)» в плейлисте и «Воротилин» в памяти для него одна студия.
   */
  audioLabelKey?: (label: string) => string
}

/**
 * Фасад плеера. Ванильный TS: React-обвязка лежит отдельно в ./react и
 * является тонкой — плеер переживает смену UI-фреймворка.
 */
export class KinoPlayer {
  private engine: PlaybackEngine | null = null
  private media = new MediaBridge()
  private recorder: SessionRecorder | null = null
  private stalls: StallWatch | null = null
  private timer: ReturnType<typeof setInterval> | null = null
  private listeners = new Map<EventName, Listener<EventName>[]>()
  private lastSource: PlayerSource | null = null
  private progressReady = false
  // С какого места загружен источник и шёл ли он с тех пор: открытый и
  // не включённый плеер сообщить серверу ничего не может.
  private startMs = 0
  private played = false
  private unbindVideoEvents: (() => void) | null = null

  constructor(
    private readonly video: HTMLVideoElement,
    private readonly opts: PlayerOptions = {},
  ) {
    for (const [event, cb] of Object.entries(opts.on ?? {})) {
      if (cb) this.on(event as EventName, cb as Listener<EventName>)
    }
  }

  /** Единственное место во фронтенде, где ветвится платформа. */
  private pickEngine(source: PlayerSource): PlaybackEngine {
    if (this.opts.engineFactory) return this.opts.engineFactory(this.video)
    // Обычный файл (MP4 одним куском) играет сам <video>: hls.js ждёт
    // плейлист и на файле падает с ошибкой разбора манифеста.
    if (source.type === 'progressive') return new NativeEngine()
    // Порядок важен: на iPadOS доступны оба, и hls.js там предпочтительнее
    // (управление качеством и дорожками). На iPhone остаётся только нативный.
    if (HlsJsEngine.supported()) return new HlsJsEngine()
    if (canUseNativeHls(this.video)) return new NativeEngine()
    throw new Error('no playback engine available')
  }

  async load(source: PlayerSource): Promise<void> {
    await this.unload('abandoned')

    const engine = this.pickEngine(source)
    this.engine = engine
    this.recorder = new SessionRecorder(engine.impl)
    this.recorder.start()
    this.stalls = new StallWatch((t) => this.onStall(t))

    engine.on('levelswitch', (p) => {
      this.recorder?.levelSwitch(p.height)
      if (p.height) this.emit('qualitychange', { id: String(p.height), height: p.height })
    })
    // Дорожку сменил сам движок (дефолт, фолбэк) — объявляем как автовыбор:
    // человеческим он не был, и предпочтение запоминать за него не нужно.
    engine.on('audioswitch', (p) => {
      if (p.id === undefined) return
      const a = this.engine?.audioTracks().find((t) => t.id === p.id)
      this.emit('audiochange', { id: p.id, label: a?.label ?? '', language: a?.language ?? '', auto: true })
    })
    engine.on('error', (p) => {
      this.emit('error', {
        code: p.code ?? 'unknown', fatal: Boolean(p.fatal), message: p.message ?? '', status: p.status,
      })
    })

    // Постер показывается до первого кадра. Без него плеер до нажатия —
    // чёрный прямоугольник, а старт через узкий канал занимает секунды.
    if (source.poster) this.video.poster = source.poster
    else this.video.removeAttribute('poster')

    this.bindVideoEvents()
    // Системная панель узнаёт о серии раньше, чем пойдёт кадр: шторка
    // показывает название уже во время подготовки источника.
    this.media.attach(this.video, source)
    this.lastSource = source
    await engine.attach(this.video, source)

    if (source.startPositionMs) this.video.currentTime = source.startPositionMs / 1000
    this.startMs = source.startPositionMs ?? 0
    this.played = false
    this.progressReady = true
    this.applyPreferences(source)
    this.announceCurrentTracks()

    this.emit('ready', { impl: engine.impl, durationMs: this.durationMs() })
    this.emit('tracks', {
      audio: engine.audioTracks(), text: engine.textTracks(), qualities: engine.qualities(),
    })
    this.startProgressTimer()
    // Отказ браузера — не ошибка: без жеста он просто оставит паузу.
    if (source.autoplay) void this.video.play().catch(() => {})
  }

  /**
   * Дорожки по подписи выбираются здесь, а не в движке: подпись — понятие
   * человека («LostFilm»), а движок знает только номера в манифесте.
   */
  private applyPreferences(source: PlayerSource): void {
    if (!this.engine) return
    if (source.preferredAudioLabel) {
      const t = pickTrack(this.engine.audioTracks(), source.preferredAudioLabel, this.opts.audioLabelKey)
      if (t) this.setAudio(t.id, true)
    }
    const wanted = source.preferredTextLabel
    if (wanted === '') {
      this.setText(null, true)
      return
    }
    // Подпись человека сильнее: её он выбирал сам. Нет такой — включаются
    // субтитры, без которых источник не задуман (оригинал с подписями).
    const text = this.engine.textTracks()
    const t = (wanted ? pickTrack(text, wanted) : undefined) ?? text.find((x) => x.default)
    if (t) this.setText(t.id, true)
  }

  /**
   * Что играет с самого старта: движок сам выбирает дорожку по умолчанию,
   * и до первого клика этот выбор нигде не объявлен — в меню не стояла бы
   * галочка, хотя звук уже идёт. auto: true — выбор не человеческий, и как
   * предпочтение его запоминать не нужно.
   */
  private announceCurrentTracks(): void {
    if (!this.engine) return
    const aid = this.engine.currentAudioId()
    if (aid !== null) {
      const a = this.engine.audioTracks().find((t) => t.id === aid)
      this.emit('audiochange', { id: aid, label: a?.label ?? '', language: a?.language ?? '', auto: true })
    }
    const tid = this.engine.currentTextId()
    const x = tid === null ? undefined : this.engine.textTracks().find((t) => t.id === tid)
    this.emit('textchange', { id: tid, label: x?.label ?? '', language: x?.language ?? '', auto: true })
  }

  /** Тот же источник с начала. Нужен после конца: движок уже отпущен. */
  replay(): Promise<void> {
    if (!this.lastSource) return Promise.resolve()
    return this.load({ ...this.lastSource, startPositionMs: 0, autoplay: true })
  }

  private bindVideoEvents(): void {
    const v = this.video
    const onPlaying = () => {
      this.played = true
      this.stalls?.playing()
      this.recorder?.firstFrame()
      const d = this.recorder?.rebufferEnd() ?? 0
      if (d > 0) this.emit('rebufferend', { durationMs: d })
      this.emit('play', undefined)
    }
    const onWaiting = () => {
      this.stalls?.waiting(v.seeking)
      this.recorder?.rebufferStart()
      this.emit('rebufferstart', undefined)
    }
    const onPause = () => { this.stalls?.paused(); this.reportProgress(); this.emit('pause', undefined) }
    const onSeeked = () => this.reportProgress()
    const onEnded = () => { void this.unload('completed') }
    const events: [string, () => void][] = [
      ['playing', onPlaying], ['waiting', onWaiting], ['pause', onPause],
      ['seeked', onSeeked], ['ended', onEnded],
    ]
    events.forEach(([event, on]) => v.addEventListener(event, on))
    this.unbindVideoEvents = () => events.forEach(([event, on]) => v.removeEventListener(event, on))
  }

  private reportProgress(): void {
    if (!this.progressReady || this.video.seeking || this.durationMs() <= 0) return
    // Плеер открыли и ушли, не включив и не сдвинув: позиция — точка
    // загрузки, а у просмотренной серии это ноль. Такой отчёт стирал
    // «просмотрено», поставленное руками, — открытая серия с галочкой
    // при уходе со страницы снова становилась непросмотренной.
    if (!this.played && Math.abs(this.positionMs() - this.startMs) < 1000) return
    this.opts.progress?.report(this.positionMs(), this.durationMs())
  }

  /** Последний кадр при уходе со страницы, а не снимок прошлого таймера. */
  flushProgress(): void {
    this.reportProgress()
    this.opts.progress?.flush()
  }

  private startProgressTimer(): void {
    const every = this.opts.progressIntervalMs ?? 15_000
    this.timer = setInterval(() => {
      const pos = this.positionMs()
      this.emit('timeupdate', { positionMs: pos, durationMs: this.durationMs() })
      if (!this.video.paused) this.reportProgress()
    }, every)
  }

  async unload(reason: 'completed' | 'abandoned' | 'error', errorCode?: string): Promise<void> {
    if (this.timer) { clearInterval(this.timer); this.timer = null }
    this.stalls?.stop()
    this.stalls = null
    this.media.detach()
    if (!this.engine) return

    this.flushProgress()
    this.progressReady = false
    this.unbindVideoEvents?.()
    this.unbindVideoEvents = null

    if (this.recorder) this.opts.telemetry?.session(this.recorder.finish(reason, errorCode))
    this.recorder = null

    this.engine.detach()
    this.engine = null
    if (reason === 'completed') this.emit('ended', undefined)
  }

  play(): Promise<void> { return this.video.play() }
  pause(): void { this.video.pause() }
  seekMs(ms: number): void { this.video.currentTime = ms / 1000 }
  positionMs(): number { return Math.round(this.video.currentTime * 1000) }
  durationMs(): number {
    return Number.isFinite(this.video.duration) ? Math.round(this.video.duration * 1000) : 0
  }

  /**
   * Итог сессии без её закрытия: страница отправляет его биконом при
   * выгрузке, потому что обычный отчёт (fetch) браузер там убивает.
   * finish() только читает накопленное — сессия продолжается как шла.
   */
  sessionStats(): SessionStats | null {
    if (!this.recorder) return null
    return this.recorder.finish('abandoned')
  }

  selectAudio(id: string): void { this.setAudio(id, false) }
  selectQuality(id: string | null): void { this.engine?.selectQuality(id) }
  /** Мгновенные показатели потока; пусто, если движок их не отдаёт. */
  stats(): EngineStats { return this.engine?.stats?.() ?? {} }

  /**
   * Что играет сейчас. Нужен панели для превью кадра: второй плеер под
   * курсором полосы обязан играть тот же адрес. null до load() и после
   * unload().
   */
  get source(): PlayerSource | null { return this.lastSource }
  selectText(id: string | null): void { this.setText(id, false) }

  /**
   * Куда вести «следующая серия» с системной панели (шторка, локскрин,
   * наушники). Плеер сам не знает, что такое следующая серия, — решает тот,
   * кто его собрал. null убирает кнопку из шторки.
   */
  setNextAction(onNext: (() => void) | null): void { this.media.setNext(onNext) }

  // Событие несёт подпись, а не только номер: тем, кто запоминает выбор,
  // нужна именно она. И флаг auto: выбор из памяти запоминать заново незачем.
  private setAudio(id: string, auto: boolean): void {
    this.engine?.selectAudio(id)
    const t = this.engine?.audioTracks().find((a) => a.id === id)
    this.emit('audiochange', { id, label: t?.label ?? '', language: t?.language ?? '', auto })
  }
  private setText(id: string | null, auto: boolean): void {
    this.engine?.selectText(id)
    const t = id === null ? undefined : this.engine?.textTracks().find((x) => x.id === id)
    this.emit('textchange', { id, label: t?.label ?? '', language: t?.language ?? '', auto })
  }

  on<E extends EventName>(event: E, cb: Listener<E>): () => void {
    const list = (this.listeners.get(event) ?? []) as Listener<EventName>[]
    list.push(cb as Listener<EventName>)
    this.listeners.set(event, list)
    return () => {
      this.listeners.set(event, (this.listeners.get(event) ?? []).filter((f) => f !== cb))
    }
  }

  /**
   * Сторож задержки. Зависание по ходу просмотра ('long'/'often') — повод
   * сменить площадку, им занимается тот, кто плеер собрал. А «поток не
   * начался» ('startup') проводим как фатальный сбой: по одному 'stall'
   * смена площадки на старте зацикливалась бы на той же, тогда как путь
   * фатальной ошибки уже умеет перебрать площадки и, если ни одна не
   * отдаёт, честно показать отказ с «Повторить» — как при мёртвой ссылке.
   */
  private onStall(t: StallTrip): void {
    if (t.reason === 'startup') {
      this.emit('error', { code: 'startupStalled', fatal: true, message: 'поток не начался' })
      return
    }
    this.emit('stall', { reason: t.reason, waitedMs: t.waitedMs, recentMs: t.recentMs })
  }

  private emit<E extends EventName>(event: E, payload: PlayerEvents[E]): void {
    for (const cb of this.listeners.get(event) ?? []) (cb as Listener<E>)(payload)
  }
}

/**
 * Дорожка по подписи, иначе по языку. Регистр не важен: подписи приходят
 * из манифеста, и «Lostfilm» против «LostFilm» — не повод не узнать.
 * key — правило хозяина, если подписи у него сложнее регистра.
 */
function pickTrack<T extends { label: string; language: string }>(
  tracks: T[], wanted: string, key: (s: string) => string = plainKey,
): T | undefined {
  const w = wanted.trim().toLowerCase()
  if (!w) return undefined
  const k = key(wanted)
  return (k ? tracks.find((t) => key(t.label) === k) : undefined)
    ?? tracks.find((t) => t.language.trim().toLowerCase() === w)
}

function plainKey(s: string): string { return s.trim().toLowerCase() }
