import Hls from 'hls.js'
import type { AudioTrack, PlayerSource, Quality, TextTrack } from '../types'
import type { EngineEvent, EnginePayload, PlaybackEngine } from './engine'
import { SidecarTexts } from './sidecar'

// Ближе этого к месту просмотра запас при смене качества не сбрасывается:
// новое качество всё равно придёт не раньше, чем скачается его сегмент.
const KEEP_MIN_S = 10

/** Конец непрерывного запаса от текущего места. */
function bufferedEnd(v: HTMLVideoElement): number {
  const t = v.currentTime
  for (let i = 0; i < v.buffered.length; i++) {
    if (v.buffered.start(i) <= t + 0.5 && v.buffered.end(i) > t) return v.buffered.end(i)
  }
  return t
}

/**
 * Десктоп и Android: HLS через MSE. Здесь, в отличие от нативного пути,
 * доступны и управление качеством, и перехват запросов сегментов.
 */
export class HlsJsEngine implements PlaybackEngine {
  readonly impl = 'hlsjs' as const

  private hls: Hls | null = null
  private video: HTMLVideoElement | null = null
  private handlers = new Map<EngineEvent, ((p: EnginePayload) => void)[]>()
  // Перезапуски загрузки после фатальной сетевой ошибки подряд; первый
  // скачанный сегмент обнуляет счёт.
  private netRestarts = 0
  // Субтитры отдельными файлами: hls.js о них не знает, это <track> рядом.
  private sidecar = new SidecarTexts()

  static supported(): boolean { return Hls.isSupported() }

  async attach(video: HTMLVideoElement, source: PlayerSource): Promise<void> {
    const hls = new Hls({
      // Стартовать с качества, которое канал тянул в прошлый раз: время до
      // первого кадра важнее мгновенного 1080p (метрика ttff_ms), но и
      // беспричинное мыло на старте каждой серии не нужно.
      startLevel: -1,
      ...(source.bandwidthHintBps ? { abrEwmaDefaultEstimate: source.bandwidthHintBps } : {}),
      backBufferLength: 60,
      // Сегмент 4K весит 10–20 МБ, и первый байт может прийти позже
      // стандартных 10 с hls.js: шлюз отдаёт сегмент по мере закачки, но
      // сегмент может ждать слот в очереди шлюза за чужими 4K-сегментами.
      // Оборванный по сроку запрос — это 4K, которое не стартует.
      fragLoadPolicy: {
        default: { ...Hls.DefaultConfig.fragLoadPolicy.default, maxTimeToFirstByteMs: 30_000 },
      },
      // Запас впрок — минута вместо стандартных 30 с. Через VPN каждый
      // десятый сегмент kinopub качается медленнее, чем играет (замер
      // 05.10.2026: 4–7 Мбит/с при потоке того же порядка), и полминуты
      // запаса такой провал не покрывают. Память держит maxBufferSize (60 МБ по
      // умолчанию), он не тронут.
      maxBufferLength: 60,
    })
    this.hls = hls
    this.video = video

    hls.on(Hls.Events.MANIFEST_PARSED, () => this.emit('manifest', {}))
    hls.on(Hls.Events.LEVEL_SWITCHED, (_e, d) => {
      this.emit('levelswitch', { height: hls.levels[d.level]?.height })
    })
    // Смена дорожки без клика: выбор по умолчанию hls.js делает сам, а при
    // ошибке трека молча переезжает на другой. Панель обязана об этом узнать.
    hls.on(Hls.Events.AUDIO_TRACK_SWITCHED, (_e, d) => {
      this.emit('audioswitch', { id: String(d.id) })
    })
    hls.on(Hls.Events.FRAG_LOADED, () => { this.netRestarts = 0 })
    hls.on(Hls.Events.ERROR, (_e, d) => {
      const status = d.response?.code || undefined
      this.emit('error', { code: d.details, fatal: d.fatal, message: d.reason ?? d.details, status })
      if (!d.fatal) return
      // Восстановимые фатальные ошибки: сеть и медиа лечатся по-разному.
      if (d.type === Hls.ErrorTypes.NETWORK_ERROR) {
        // 404 и 410 — ссылки больше нет (тикет протух или стёрт): повтор
        // даст то же, и без предела это вечное кольцо на минуты. Новую
        // ссылку выдаёт хозяин плеера.
        if (status === 404 || status === 410) return
        if (this.netRestarts++ < 3) hls.startLoad()
      } else if (d.type === Hls.ErrorTypes.MEDIA_ERROR) hls.recoverMediaError()
    })

    this.sidecar.attach(video, source)
    hls.loadSource(source.url)
    hls.attachMedia(video)

    await new Promise<void>((resolve) => {
      hls.once(Hls.Events.MANIFEST_PARSED, () => resolve())
    })

    if (source.preferredAudioId) this.selectAudio(source.preferredAudioId)
  }

  detach(): void {
    this.hls?.destroy()
    this.hls = null
    this.video = null
    this.sidecar.detach()
  }

  audioTracks(): AudioTrack[] {
    const list = this.hls?.audioTracks ?? []
    return list.map((t, i) => ({
      id: String(t.id),
      language: t.lang ?? '',
      // У манифестов площадок дорожки часто без имени и языка: номера
      // движка человеку ничего не говорят, остаются порядковые.
      label: t.name || t.lang || `Дорожка ${i + 1}`,
    }))
  }

  textTracks(): TextTrack[] {
    const list = this.hls?.subtitleTracks ?? []
    return [...list.map((t, i) => ({
      id: String(t.id),
      language: t.lang ?? '',
      label: t.name || t.lang || `Субтитры ${i + 1}`,
    })), ...this.sidecar.tracks()]
  }

  qualities(): Quality[] {
    return (this.hls?.levels ?? []).map((l, i) => ({
      id: String(i),
      height: l.height,
      bitrateKbps: Math.round(l.bitrate / 1000),
    }))
  }

  // Дорожку по умолчанию выбирает сам hls.js при разборе манифеста, и она
  // уже играет: активная — она и есть, даже если выбор сделан молча.
  currentAudioId(): string | null {
    const t = this.hls?.audioTrack
    return t === undefined || t < 0 ? null : String(t)
  }

  /** -1 у hls.js значит «выключены». */
  currentTextId(): string | null {
    const own = this.sidecar.current()
    if (own !== null) return own
    const t = this.hls?.subtitleTrack
    return t === undefined || t < 0 ? null : String(t)
  }

  stats() {
    const hls = this.hls
    if (!hls) return {}
    const level = hls.levels?.[hls.currentLevel]
    return {
      bandwidthKbps: Math.round((hls.bandwidthEstimate ?? 0) / 1000) || undefined,
      codecs: [level?.videoCodec, level?.audioCodec].filter(Boolean).join(', ') || undefined,
      levelKbps: level ? Math.round(level.bitrate / 1000) : undefined,
    }
  }

  selectAudio(id: string): void {
    if (this.hls) this.hls.audioTrack = Number(id)
  }

  /**
   * null -> вернуть управление ABR.
   *
   * Как у YouTube: скачанное доигрывается в прежнем качестве, а новые
   * сегменты качаются уже в выбранном — кадр не встаёт ни на миг.
   *
   * currentLevel не годится: он выбрасывает весь буфер картинки, звук идёт
   * дальше, кадр стоит. nextLevel сбрасывает запас сразу за ближайшим
   * сегментом и рассчитывает успеть скачать следующий по оценке канала —
   * через VPN она скачет вдвое, и на узком канале кадр встаёт. Поэтому
   * качество меняется через loadLevel (ничего не сбрасывает), а
   * сбрасывается только дальний хвост запаса — см. trimAhead.
   */
  selectQuality(id: string | null): void {
    const hls = this.hls
    if (!hls) return
    const level = id === null ? -1 : Number(id)
    hls.loadLevel = level
    if (level >= 0) this.trimAhead(level)
  }

  /**
   * Сбросить запас в прежнем качестве дальше того места, куда новое
   * гарантированно успеет.
   *
   * Без этого новое качество показалось бы через весь запас: минута, а на
   * низком качестве hls.js копит и две (потолок у него в мегабайтах). Отступ —
   * такой, чтобы сегмент нового качества скачался даже при канале вдвое хуже
   * нынешней оценки, и не меньше KEEP_MIN_S.
   *
   * Режем по границе сегмента НОВОГО качества: у kinopub качества нарезаны
   * по-разному (406p — 33, 43…, 1080p — 36, 43…), и при срезе по старой
   * границе hls.js докачивал новый сегмент, начинающийся раньше среза, —
   * поверх сохранённого запаса, и качество менялось через 6 с, а не после запаса.
   * Поход за ненужным уже сегментом обрывается, а загрузка возобновляется,
   * когда буфер действительно сброшен: иначе первым качался бы сегмент
   * за старым концом запаса, а не тот, что нужен у среза.
   */
  private trimAhead(level: number): void {
    const hls = this.hls
    const v = this.video
    const want = hls?.levels[level]
    if (!hls || !v || !want) return
    const details = want.details
    if (!details) {
      // Плейлист нового качества ещё качается — границ его сегментов не знаем.
      const onLoaded = (_e: unknown, d: { level: number }) => {
        if (d.level !== level) return
        hls.off(Hls.Events.LEVEL_LOADED, onLoaded)
        if (this.hls === hls && hls.loadLevel === level) this.trimAhead(level)
      }
      hls.on(Hls.Events.LEVEL_LOADED, onLoaded)
      return
    }
    const bw = hls.bandwidthEstimate
    if (!(bw > 0)) return
    const fragSec = details.averagetargetduration || 6
    const fetch = 1 + hls.ttfbEstimate / 1000 + (want.maxBitrate * fragSec) / (bw / 2)
    const keepUntil = v.currentTime + Math.max(KEEP_MIN_S, 1.5 * fetch)
    // Сегмент kinopub не всегда начинается с ключевого кадра, и тогда hls.js
    // докачивает ещё и предыдущий сегмент нового качества — поверх запаса.
    // Срез — сегментом дальше: новое качество начнётся не раньше keepUntil.
    const frags = details.fragments
    const i = frags.findIndex((f) => f.start >= keepUntil)
    const cut = i >= 0 ? frags[i + 1]?.start : undefined
    if (cut === undefined || bufferedEnd(v) - cut < 1) return
    hls.stopLoad()
    // При отдельной звуковой дорожке её буфер не трогаем: качество — у картинки.
    const altAudio = !!hls.audioTracks[hls.audioTrack]?.url
    let resumed = false
    const resume = () => {
      if (resumed || this.hls !== hls) return
      resumed = true
      hls.startLoad(v.currentTime, true)
    }
    hls.once(Hls.Events.BUFFER_FLUSHED, resume)
    // Сброс не отчитался — загрузка всё равно не должна остаться стоять.
    setTimeout(resume, 1000)
    hls.trigger(Hls.Events.BUFFER_FLUSHING, {
      startOffset: cut, endOffset: Number.POSITIVE_INFINITY, type: altAudio ? 'video' : null,
    })
  }

  /** null -> выключить. hls.js трактует -1 именно так. */
  selectText(id: string | null): void {
    // Показывается одна дорожка: своя из потока или файл рядом.
    const own = this.sidecar.has(id)
    this.sidecar.select(own ? id : null)
    if (this.hls) this.hls.subtitleTrack = id === null || own ? -1 : Number(id)
  }

  on(event: EngineEvent, cb: (p: EnginePayload) => void): void {
    const list = this.handlers.get(event) ?? []
    list.push(cb)
    this.handlers.set(event, list)
  }

  private emit(event: EngineEvent, p: EnginePayload): void {
    for (const cb of this.handlers.get(event) ?? []) cb(p)
  }
}
