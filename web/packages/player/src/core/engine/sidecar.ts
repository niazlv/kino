import type { PlayerSource, TextTrack } from '../types'

/** Номера внешних дорожек — со своим префиксом: номера дорожек потока раздаёт движок. */
const PREFIX = 'x'

/**
 * Субтитры отдельными файлами (WebVTT) рядом с потоком.
 *
 * Играют одинаково у обоих движков: это <track> на самом <video>, файл тянет
 * и разбирает браузер, рисует — он же, как и субтитры потока. Время в файле
 * — от начала серии, то есть в тех же единицах, что и currentTime, и у
 * hls.js, и у нативного плеера. hls.js чужие <track> не трогает: переключает
 * он только свои.
 *
 * Включена ли дорожка, решает режим: 'showing' — да, 'disabled' — нет.
 * Браузер начинает качать файл, только когда дорожку включили.
 */
export class SidecarTexts {
  private items: { el: HTMLTrackElement; info: TextTrack }[] = []

  attach(video: HTMLVideoElement, source: PlayerSource): void {
    this.detach()
    for (const [i, s] of (source.subtitles ?? []).entries()) {
      const el = document.createElement('track')
      el.kind = 'subtitles'
      el.label = s.label || s.language || `Субтитры ${i + 1}`
      if (s.language) el.srclang = s.language
      el.src = s.url
      video.appendChild(el)
      // Режим — явно: без атрибута default дорожка и так выключена, но
      // полагаться на это у каждого браузера незачем.
      el.track.mode = 'disabled'
      this.items.push({
        el,
        info: { id: PREFIX + i, language: s.language, label: el.label, url: s.url, default: s.default },
      })
    }
  }

  detach(): void {
    for (const { el } of this.items) {
      el.track.mode = 'disabled'
      el.remove()
    }
    this.items = []
  }

  tracks(): TextTrack[] { return this.items.map((x) => x.info) }

  /** Своя ли это дорожка — по номеру из tracks(). */
  has(id: string | null): boolean {
    return id !== null && this.items.some((x) => x.info.id === id)
  }

  /** Своя ли это дорожка <video>: нативный движок перечисляет их все подряд. */
  owns(t: globalThis.TextTrack): boolean {
    return this.items.some((x) => x.el.track === t)
  }

  current(): string | null {
    return this.items.find((x) => x.el.track.mode === 'showing')?.info.id ?? null
  }

  /** Включить одну свою, остальные выключить; чужой номер или null — выключить все. */
  select(id: string | null): void {
    for (const { el, info } of this.items) {
      const mode = info.id === id ? 'showing' : 'disabled'
      if (el.track.mode !== mode) el.track.mode = mode
    }
  }
}
