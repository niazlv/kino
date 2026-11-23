import type { PlayerImpl, PlayerSource, AudioTrack, TextTrack, Quality } from '../types'

/**
 * Граница между двумя несовместимыми способами играть HLS.
 *
 * Причина существования интерфейса, а не ветвления внутри плеера: пути
 * действительно разные (MSE против нативного проигрывателя), у них разный
 * набор багов, разные метрики и разные ограничения по авторизации. Разведя их
 * типом, мы получаем возможность тестировать каждый отдельно и честно
 * подписывать телеметрию значением `impl`.
 */
export interface PlaybackEngine {
  readonly impl: PlayerImpl

  attach(video: HTMLVideoElement, source: PlayerSource): Promise<void>
  detach(): void

  audioTracks(): AudioTrack[]
  textTracks(): TextTrack[]
  qualities(): Quality[]

  /**
   * Какая дорожка играет сейчас. При загрузке движок выбирает её сам, до
   * всякого клика — без этих ответов панель не знает, что помечать в меню.
   */
  currentAudioId(): string | null
  /** Активные субтитры; null — выключены, и для меню это тоже состояние. */
  currentTextId(): string | null

  selectAudio(id: string): void
  /** null — автовыбор по пропускной способности. */
  selectQuality(id: string | null): void
  /** null — выключить субтитры. */
  selectText(id: string | null): void

  on(event: EngineEvent, cb: (p: EnginePayload) => void): void

  /**
   * Что происходит с потоком прямо сейчас — для оверлея статистики.
   * Необязателен: нативный проигрыватель таких чисел не отдаёт.
   */
  stats?(): EngineStats
}

/** Мгновенные показатели потока. Любое поле может отсутствовать. */
export interface EngineStats {
  /** Оценка канала движком, кбит/с. */
  bandwidthKbps?: number
  /** Кодеки текущего уровня, как их назвал манифест. */
  codecs?: string
  /** Битрейт выбранного уровня, кбит/с. */
  levelKbps?: number
}

export type EngineEvent =
  | 'manifest'
  | 'levelswitch'
  // Движок сменил аудиодорожку сам: дефолт по манифесту, фолбэк при ошибке
  // трека. Без этого события панель не узнает о смене — галочка отстанет.
  | 'audioswitch'
  | 'error'

export interface EnginePayload {
  height?: number
  // id — для audioswitch: какая дорожка теперь активна.
  id?: string
  code?: string
  fatal?: boolean
  message?: string
  // status — HTTP-код отказа у сетевой ошибки.
  status?: number
}

/**
 * Выбор движка — единственное место во всём фронтенде, где ветвится
 * платформа. Нативный путь обязателен: в Safari на iPhone MSE недоступен,
 * и hls.js там просто не запустится.
 */
export function canUseNativeHls(video: HTMLVideoElement): boolean {
  return video.canPlayType('application/vnd.apple.mpegurl') !== ''
}
