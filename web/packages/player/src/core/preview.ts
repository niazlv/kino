import Hls from 'hls.js'
import type { PlayerSource } from './types'

/**
 * Превью кадра под курсором полосы времени — как у YouTube.
 *
 * Второй, невидимый <video> играет тот же поток: наведение ставит ему
 * currentTime, готовый кадр рисуется на канвас и уходит подписчикам. Здесь
 * экономия важнее мгновенности: у YouTube кадры нарезаны в спрайты заранее,
 * нашим площадкам их взять неоткуда, и единственный способ — качать. Поэтому
 * инстанс заводится только по первому наведению, буфер у него в несколько
 * секунд самого низкого качества, а без наведений 15 секунд он доживает на
 * таймере и уничтожается целиком — в простое ни сети, ни памяти. Сегменты
 * при этом идут через шлюз, и дисковый кеш сегментов делает повторное
 * наведение туда же бесплатным для площадки.
 */

/** Тишина после перемещения мыши, прежде чем просить кадр. */
const SEEK_DEBOUNCE_MS = 180
/** Сколько живёт без наведений, прежде чем освободить сеть и память. */
const IDLE_MS = 15_000
/** Кадры дальше этого от запрошенного места — хвост старой перемотки. */
const FRAME_TOL_S = 6
/** Кадр новее этого от места под курсором — кадр этого места, а не хвост. */
export const PREVIEW_MATCH_S = 8
/** Габарит превью; высоту канваса дорисовывает сам кадр. */
export const PREVIEW_W = 168
export const PREVIEW_H = 94

export interface PreviewFrame {
  time: number
  canvas: HTMLCanvasElement
}

/**
 * Второй плеер за кадром. Отдельный интерфейс — тот же шов, что engineFactory
 * у ядра: тесты подставляют подделку и не запускают hls.js.
 */
export interface PreviewBackend {
  onFrame(cb: (frame: PreviewFrame) => void): void
  onFail(cb: () => void): void
  seek(t: number): void
  detach(): void
}

export interface HoverPreviewDeps {
  /** Что играет сейчас: второй плеер должен играть тот же адрес. */
  getSource: () => PlayerSource | null
  /** Подмена в тестах; null — источник этой копии плееру не по зубам. */
  createBackend?: (source: PlayerSource, video: HTMLVideoElement) => PreviewBackend | null
  createVideo?: () => HTMLVideoElement
}

export class HoverPreview {
  private deps: HoverPreviewDeps
  private backend: PreviewBackend | null = null
  private url: string | null = null
  /** Куда наведены сейчас: с ним сверяются пришедшие кадры. */
  private wanted = 0
  private seekTimer: ReturnType<typeof setTimeout> | null = null
  private idleTimer: ReturnType<typeof setTimeout> | null = null
  /** Адреса, отказавшие под превью: повторять их нечего. */
  private dead = new Set<string>()
  private frames = new Set<(f: PreviewFrame) => void>()
  private fails = new Set<() => void>()
  private aliveCbs = new Set<(ok: boolean) => void>()
  /** Пока true, «снова жив» подписчикам не сообщается: они и так считают его живым. */
  private live = true

  constructor(deps: HoverPreviewDeps) { this.deps = deps }

  /** false — поток не отдал кадр, превью больше не показывать. */
  onAlive(cb: (ok: boolean) => void): () => void {
    this.aliveCbs.add(cb)
    return () => { this.aliveCbs.delete(cb) }
  }
  onFrame(cb: (f: PreviewFrame) => void): () => void {
    this.frames.add(cb)
    return () => { this.frames.delete(cb) }
  }

  /** Курсор на полосе; время — в секундах. */
  request(time: number): void {
    const s = this.deps.getSource()
    if (!s?.url) return
    if (s.url !== this.url) {
      this.drop()
      this.url = s.url
      // Новый источник — прежний отказ на него не переносится.
      if (!this.live) {
        this.live = true
        for (const cb of this.aliveCbs) cb(true)
      }
    }
    if (this.dead.has(this.url)) return
    this.wanted = time
    if (!this.backend) {
      const video = (this.deps.createVideo ?? (() => document.createElement('video')))()
      const be = (this.deps.createBackend ?? defaultBackend)(s, video)
      if (!be) { this.dead.add(this.url); this.fail(); return }
      this.backend = be
      be.onFrame((f) => {
        if (Math.abs(f.time - this.wanted) <= FRAME_TOL_S) {
          for (const cb of this.frames) cb(f)
        }
      })
      be.onFail(() => { this.fail(); this.drop() })
    }
    if (this.seekTimer) clearTimeout(this.seekTimer)
    this.seekTimer = setTimeout(() => {
      this.seekTimer = null
      this.backend?.seek(this.wanted)
    }, SEEK_DEBOUNCE_MS)
    if (this.idleTimer) clearTimeout(this.idleTimer)
    this.idleTimer = setTimeout(() => { this.idleTimer = null; this.drop() }, IDLE_MS)
  }

  /** Курсор ушёл с полосы: просить кадр оттуда уже незачем. */
  cancel(): void {
    if (this.seekTimer) { clearTimeout(this.seekTimer); this.seekTimer = null }
  }

  /** Панель умирает — со страницей или с серией. */
  stop(): void {
    this.cancel()
    if (this.idleTimer) { clearTimeout(this.idleTimer); this.idleTimer = null }
    this.drop()
  }

  private fail(): void {
    this.live = false
    if (this.url) this.dead.add(this.url)
    for (const cb of this.aliveCbs) cb(false)
  }

  private drop(): void {
    this.cancel()
    this.backend?.detach()
    this.backend = null
  }
}

function defaultBackend(source: PlayerSource, video: HTMLVideoElement): PreviewBackend | null {
  if (source.type === 'progressive') return new FileBackend(video, source.url)
  if (!Hls.isSupported()) return null
  return new HlsBackend(video, source.url)
}

/**
 * Рисует кадр видео на канвас ширины PREVIEW_W; высоту берёт из кадра.
 * Событий о готовности бывает несколько на одну перемотку — повтор кадра
 * глушится, подписчики видят перемотку один раз.
 */
class ThumbPainter {
  private canvas: HTMLCanvasElement | null = null
  private last = -1

  paint(video: HTMLVideoElement): PreviewFrame | null {
    if (video.readyState < 2 || !video.videoWidth) return null
    if (video.currentTime === this.last) return null
    this.canvas ??= document.createElement('canvas')
    const c = this.canvas
    const h = Math.max(1, Math.round((PREVIEW_W * video.videoHeight) / video.videoWidth))
    if (c.width !== PREVIEW_W || c.height !== h) { c.width = PREVIEW_W; c.height = h }
    c.getContext('2d')?.drawImage(video, 0, 0, c.width, c.height)
    this.last = video.currentTime
    return { time: video.currentTime, canvas: c }
  }

  reset(): void { this.canvas = null; this.last = -1 }
}

abstract class VideoBackend implements PreviewBackend {
  private frames = new Set<(f: PreviewFrame) => void>()
  private fails = new Set<() => void>()
  private painter = new ThumbPainter()

  constructor(protected video: HTMLVideoElement) {
    // seeked — перемотка дошла; canplay — первый кадр после старта загрузки.
    video.addEventListener('seeked', this.onPaint)
    video.addEventListener('canplay', this.onPaint)
    video.muted = true
  }

  onFrame(cb: (f: PreviewFrame) => void): void { this.frames.add(cb) }
  onFail(cb: () => void): void { this.fails.add(cb) }

  protected fail(): void { for (const cb of this.fails) cb() }
  private onPaint = () => {
    const f = this.painter.paint(this.video)
    if (f) for (const cb of this.frames) cb(f)
  }

  abstract seek(t: number): void

  detach(): void {
    const v = this.video
    v.removeEventListener('seeked', this.onPaint)
    v.removeEventListener('canplay', this.onPaint)
    v.removeAttribute('src')
    v.load()
    this.painter.reset()
  }
}

/**
 * HLS вторым инстансом hls.js, прижатым к самому низкому качеству. Сегменты
 * в muxed-манифестах несут в себе и звук — тихий <video> тут ничего не
 * экономит, а вот
 * буфер в несколько секунд экономит память; заодно превью прогревает кеш
 * сегментов шлюза перед настоящим просмотром.
 */
class HlsBackend extends VideoBackend {
  private hls: Hls | null = null
  private started = false

  constructor(video: HTMLVideoElement, url: string) {
    super(video)
    const hls = new Hls({
      // Загрузка — только по наведению: без него второй плеер молчит.
      autoStartLoad: false,
      // Впереди курса — 8 секунд, позади — 10: подрагать мышью туда-сюда
      // не должно перекачивать сегменты, а запас больше превью не нужен.
      maxBufferLength: 8,
      maxMaxBufferLength: 12,
      backBufferLength: 10,
    })
    this.hls = hls
    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      // Самое низкое качество манифеста. Дорожки без высоты пропускаются:
      // ноль здесь значит «неизвестно», и неизвестное качать рискованно.
      const levels = hls.levels ?? []
      let low = -1
      let lowH = Infinity
      levels.forEach((l, i) => {
        if (l.height && l.height < lowH) { low = i; lowH = l.height }
      })
      if (low >= 0) hls.currentLevel = low
    })
    hls.on(Hls.Events.ERROR, (_e, d) => { if (d.fatal) this.fail() })
    hls.loadSource(url)
    hls.attachMedia(video)
  }

  seek(t: number): void {
    const hls = this.hls
    if (!hls) return
    // Первый кадр — startLoad с места наведения: старт с нуля ради превью
    // качал бы фрагмент, который никто не просил.
    if (!this.started) { this.started = true; hls.startLoad(t) }
    else this.video.currentTime = t
  }

  detach(): void {
    this.hls?.destroy()
    this.hls = null
    super.detach()
  }
}

/** Обычный файл (mp4): браузер сам тянет нужные куски по Range. */
class FileBackend extends VideoBackend {
  constructor(video: HTMLVideoElement, url: string) {
    super(video)
    video.preload = 'metadata'
    video.src = url
  }

  seek(t: number): void { this.video.currentTime = t }
}
