import type { AudioTrack, PlayerSource, Quality, TextTrack } from '../types'
import type { EngineEvent, EnginePayload, PlaybackEngine } from './engine'
import { SidecarTexts } from './sidecar'

/**
 * iOS Safari: HLS играет нативный проигрыватель.
 *
 * Следствие, определяющее архитектуру доставки: нативный плеер НЕ позволяет
 * подставлять HTTP-заголовки в запросы сегментов. Значит авторизация медиа
 * обязана жить в подписанном URL или в same-origin cookie — не в
 * `Authorization`. См. ADR-0003.
 *
 * Второе следствие: качество и ABR полностью во власти системы, поэтому
 * `selectQuality` здесь честно ничего не делает, а не притворяется.
 */
export class NativeEngine implements PlaybackEngine {
  readonly impl = 'native_hls' as const

  private video: HTMLVideoElement | null = null
  private handlers = new Map<EngineEvent, ((p: EnginePayload) => void)[]>()
  // Субтитры отдельными файлами — <track> на том же <video>. В textTracks
  // они лежат вперемешку с дорожками потока, поэтому их учёт — отдельно.
  private sidecar = new SidecarTexts()

  async attach(video: HTMLVideoElement, source: PlayerSource): Promise<void> {
    this.video = video
    this.sidecar.attach(video, source)
    video.src = source.url
    await new Promise<void>((resolve, reject) => {
      const ok = () => { cleanup(); resolve() }
      const fail = () => {
        cleanup()
        this.emit('error', { code: 'native_load_failed', fatal: true, message: 'media load failed' })
        reject(new Error('native_load_failed'))
      }
      const cleanup = () => {
        video.removeEventListener('loadedmetadata', ok)
        video.removeEventListener('error', fail)
      }
      video.addEventListener('loadedmetadata', ok, { once: true })
      video.addEventListener('error', fail, { once: true })
    })
    this.emit('manifest', {})
  }

  detach(): void {
    this.sidecar.detach()
    if (!this.video) return
    this.video.removeAttribute('src')
    this.video.load()
    this.video = null
  }

  audioTracks(): AudioTrack[] {
    // Safari отдаёт дорожки через нестандартный video.audioTracks.
    const raw = (this.video as unknown as { audioTracks?: ArrayLike<{ id: string; language: string; label: string }> })?.audioTracks
    if (!raw) return []
    return Array.from(raw, (t, i) => ({
      id: t.id, language: t.language,
      label: t.label || t.language || `Дорожка ${i + 1}`,
    }))
  }

  // Дорожки потока — по номеру в video.textTracks (свои <track> пропускаются,
  // но номер считается по всему списку), файлы рядом — своими номерами.
  textTracks(): TextTrack[] {
    if (!this.video) return []
    const stream = Array.from(this.video.textTracks, (t, i) => ({ t, i }))
      .filter(({ t }) => !this.sidecar.owns(t))
      .map(({ t, i }) => ({ id: String(i), language: t.language, label: t.label || t.language }))
    return [...stream, ...this.sidecar.tracks()]
  }

  /** ABR принадлежит системе — управлять качеством нельзя. */
  qualities(): Quality[] { return [] }
  selectQuality(_id: string | null): void { /* намеренно no-op */ }

  // Активная дорожка — включённая. Без audioTracks API (Chrome) список и
  // так пуст, меню по нему не строится — возвращать нечего.
  currentAudioId(): string | null {
    const raw = (this.video as unknown as { audioTracks?: ArrayLike<{ id: string; enabled: boolean }> })?.audioTracks
    if (!raw) return null
    const t = Array.from(raw).find((t) => t.enabled)
    return t ? t.id : null
  }

  // Субтитры включаются режимом 'showing' — он же признак активной дорожки.
  currentTextId(): string | null {
    if (!this.video) return null
    const own = this.sidecar.current()
    if (own !== null) return own
    const i = Array.from(this.video.textTracks).findIndex((t) => t.mode === 'showing' && !this.sidecar.owns(t))
    return i < 0 ? null : String(i)
  }

  // У нативного плеера субтитры переключаются режимом дорожки на <video>:
  // отдельного API, как у hls.js, здесь нет.
  selectText(id: string | null): void {
    if (!this.video) return
    this.sidecar.select(id)
    Array.from(this.video.textTracks).forEach((t, i) => {
      if (this.sidecar.owns(t)) return
      t.mode = id !== null && String(i) === id ? 'showing' : 'disabled'
    })
  }

  selectAudio(id: string): void {
    const raw = (this.video as unknown as { audioTracks?: ArrayLike<{ id: string; enabled: boolean }> })?.audioTracks
    if (!raw) return
    for (const t of Array.from(raw)) t.enabled = t.id === id
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
