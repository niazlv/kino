/**
 * Публичный контракт плеера. Ни одного понятия платформы: нет тикетов,
 * пользователей, эпизодов и провайдеров. Только то, что нужно, чтобы играть.
 */

export type PlayerImpl = 'hlsjs' | 'native_hls'

export interface AudioTrack {
  id: string
  language: string
  /** Для русского контента это озвучка, а не язык: дорожек бывает 8+. */
  label: string
}

export interface TextTrack {
  id: string
  language: string
  label: string
  /** Только у субтитров отдельным файлом: в самом потоке их нет. */
  url?: string
  /** Включить сразу, если человек не выбрал другое. */
  default?: boolean
}

/** Субтитры отдельным файлом (WebVTT), которых нет в самом потоке. */
export interface SidecarText {
  url: string
  label: string
  language: string
  /** Включить сразу, если человек не выбрал другое. */
  default?: boolean
}

export interface Quality {
  id: string
  height: number
  bitrateKbps: number
}

/** Вход плеера. Обычный объект — откуда он взялся, плееру не важно. */
export interface PlayerSource {
  url: string
  /** HLS по умолчанию; progressive — mp4 напрямую. */
  type?: 'hls' | 'progressive'
  startPositionMs?: number
  /**
   * Оценка канала, бит/с, с прошлого просмотра. Без неё hls.js стартует
   * с 500 кбит/с — то есть с худшего качества — и каждая серия первые
   * секунды идёт мылом без всякой причины.
   */
  bandwidthHintBps?: number
  preferredAudioId?: string
  /**
   * Предпочтительная озвучка по подписи (студия) или языку, а не по номеру:
   * номера дорожек у серий одного сериала расходятся, подпись держится.
   */
  preferredAudioLabel?: string
  /**
   * Субтитры по подписи; пустая строка — выключить намеренно. Без
   * предпочтения (или если такой подписи нет) включаются субтитры с
   * default, если они есть.
   */
  preferredTextLabel?: string
  /**
   * Субтитры отдельными файлами — к дорожкам потока. Движок показывает их
   * в одном списке с ними, человек разницы не видит.
   */
  subtitles?: SidecarText[]
  /** Стартовать сразу. Браузер разрешит, если человек уже трогал страницу. */
  autoplay?: boolean
  poster?: string
  /** Название для системной медиа-панели: шторка уведомлений, локскрин. */
  title?: string
}

export interface PlayerEvents {
  ready: { impl: PlayerImpl; durationMs: number }
  play: void
  pause: void
  timeupdate: { positionMs: number; durationMs: number }
  ended: void
  /** Пересобранный список после парсинга манифеста. */
  tracks: { audio: AudioTrack[]; text: TextTrack[]; qualities: Quality[] }
  /** auto — выбор сделан ядром по предпочтению, а не человеком. */
  audiochange: { id: string; label: string; language: string; auto?: boolean }
  /** null — субтитры выключены. */
  textchange: { id: string | null; label: string; language: string; auto?: boolean }
  qualitychange: { id: string; height: number }
  rebufferstart: void
  rebufferend: { durationMs: number }
  /**
   * Источник не успевает за просмотром: одно долгое ожидание или частые
   * подряд (см. StallWatch). Плеер сам ничего не меняет — решает хозяин.
   */
  stall: { reason: 'long' | 'often'; waitedMs: number; recentMs: number }
  /** status — HTTP-код отказа, если ошибка сетевая: 404 и 410 значат, что
   *  ссылки больше нет, и повторять её бессмысленно. */
  error: { code: string; fatal: boolean; message: string; status?: number }
}

export type EventName = keyof PlayerEvents
export type Listener<E extends EventName> = (payload: PlayerEvents[E]) => void

/**
 * Куда плеер отдаёт прогресс. Инжектируется снаружи — плеер НЕ знает
 * эндпоинтов и не делает сетевых вызовов сам. Это то, что позволяет
 * вынуть его из платформы и положить в любой другой проект.
 */
export interface ProgressSink {
  report(positionMs: number, durationMs: number): void
  flush(): void
}

/**
 * Куда плеер отдаёт метрики качества. Плеер их СОБИРАЕТ, но не отправляет:
 * решение «одна запись на сессию, а не на запрос» принимается снаружи.
 */
export interface TelemetrySink {
  session(stats: SessionStats): void
}

export interface SessionStats {
  impl: PlayerImpl
  ttffMs: number
  rebufferCount: number
  rebufferMsTotal: number
  bitrateSwitches: number
  maxHeight: number
  endReason: 'completed' | 'abandoned' | 'error'
  errorCode?: string
}
