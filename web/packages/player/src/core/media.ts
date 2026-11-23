import type { PlayerSource } from './types'

const SEEK = 10

/**
 * Системная медиа-панель: шторка уведомлений Android, экран блокировки
 * iPhone, кнопки на наушниках. Без неё телефон показывает воспроизведение
 * безымянным уведомлением, а пауза с локскрина — как повезёт.
 *
 * Панель ничего не знает о платформе: название и постер приходят с
 * источником, «следующая серия» — страницей, потому что только она
 * решает, что считать следующей.
 */
export class MediaBridge {
  private video: HTMLVideoElement | null = null
  private next: (() => void) | null = null
  private on: [string, () => void][] | null = null

  private supported(): boolean {
    return typeof navigator !== 'undefined' && 'mediaSession' in navigator
  }

  /** Куда вести «следующая серия» из шторки. null — кнопки не будет. */
  setNext(onNext: (() => void) | null): void {
    this.next = onNext
    if (!this.supported()) return
    try {
      navigator.mediaSession.setActionHandler('nexttrack', onNext ? () => this.next?.() : null)
    } catch { /* панель не умеет это действие — обойдёмся */ }
  }

  attach(video: HTMLVideoElement, source: PlayerSource): void {
    this.detach()
    if (!this.supported()) return
    const ms = navigator.mediaSession
    this.video = video    // Кнопки панели ведут как наши: пауза — паузой, перемотка — на те же
    // десять секунд, что и всё остальное в плеере.
    const seek = (d: MediaSessionActionDetails, forward: boolean) => {
      const by = d.seekOffset ?? SEEK
      video.currentTime = Math.max(0, video.currentTime + (forward ? by : -by))
    }
    const actions: [MediaSessionAction, (d: MediaSessionActionDetails) => void][] = [
      ['play', () => { void video.play() }],
      ['pause', () => video.pause()],
      ['seekbackward', d => seek(d, false)],
      ['seekforward', d => seek(d, true)],
      ['seekto', d => { if (d.seekTime != null) video.currentTime = d.seekTime }],
    ]
    for (const [action, handler] of actions) {
      try { ms.setActionHandler(action, handler) } catch { /* не поддержано — и не надо */ }
    }
    // «Дальше» ставится страницей, когда ей вздумается — в том числе до
    // attach: detach выше снёс бы её обработчик. Восстанавливаем из
    // сохранённого, чтобы кнопка в шторке пережила смену серии.
    try { ms.setActionHandler('nexttrack', this.next ? () => this.next?.() : null) } catch { /* пусто */ }

    // Название и постер — в шторку уведомлений. MediaMetadata отсутствует
    // там, где нет и самой медиасессии, — проверка выше это уже покрыла,
    // но движки бывают разные.
    if (typeof MediaMetadata !== 'undefined') {
      ms.metadata = new MediaMetadata({
        title: source.title ?? '',
        artwork: source.poster ? [{ src: source.poster }] : [],
      })
    }

    // Состояние — с самого <video>: панель отражает то, что играет,
    // а не то, что должно было играть.
    this.on = [
      ['play', () => { ms.playbackState = 'playing'; this.position() }],
      ['pause', () => { ms.playbackState = 'paused' }],
      ['ended', () => { ms.playbackState = 'paused' }],
      ['timeupdate', () => this.position()],
      ['ratechange', () => this.position()],
      ['seeked', () => this.position()],
    ]
    for (const [name, handler] of this.on) video.addEventListener(name, handler)
    ms.playbackState = 'paused'
    this.position()
  }

  detach(): void {
    const v = this.video
    this.video = null
    if (v && this.on) {
      for (const [name, handler] of this.on) v.removeEventListener(name, handler)
      this.on = null
    }
    if (!this.supported()) return
    const ms = navigator.mediaSession
    for (const a of ['play', 'pause', 'seekbackward', 'seekforward', 'seekto', 'nexttrack'] as const) {
      try { ms.setActionHandler(a, null) } catch { /* пусто */ }
    }
    try { ms.setPositionState() } catch { /* пусто */ }
    ms.metadata = null
  }

  /** Полоса позиции в шторке ползёт, только если сообщать ей положение. */
  private position(): void {
    const v = this.video
    if (!v) return
    // У потока без конца сообщать нечего: вызов с бесконечной длительностью
    // панель отклоняет, а нам лишних исключений не надо.
    if (!Number.isFinite(v.duration) || v.duration <= 0) return
    try {
      navigator.mediaSession.setPositionState({
        duration: v.duration, playbackRate: v.playbackRate, position: v.currentTime,
      })
    } catch { /* не та длительность — панель обойдётся без полосы */ }
  }
}
