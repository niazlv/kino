/**
 * Разметка пропуска: что в серии можно пропустить — заставку, титры,
 * пересказ, анонс.
 *
 * Плеер её не добывает, её приносит хозяин. Здесь только решение, какой
 * разметке верить для этого потока: разметка привязана к файлу, а длину
 * файла знает лишь плеер, получивший плейлист.
 */

/** Вид отрезка. skip — «пропустить», а что это — решается по месту. */
export type SkipKind = 'intro' | 'outro' | 'recap' | 'preview' | 'skip'

/** Отрезок, как его приносит хозяин. Время — секунды от начала файла. */
export interface SkipSegment {
  kind: SkipKind
  start: number
  end: number
  /**
   * Длина файла, по которому размечено, когда разметка не с этого потока
   * (общая база зрителей размечает оригинал, а у дубляжа заставка студии
   * сдвигает всё на секунды). Нет — разметка с этого самого потока.
   */
  forDuration?: number
}

/** Отрезок, который плеер покажет: вид уже известен. */
export interface Skip {
  kind: Exclude<SkipKind, 'skip'>
  start: number
  end: number
}

/**
 * Насколько длина потока может разойтись с длиной размеченного файла.
 * Сверено на «Фрирен» (2-я серия): поток Анилибрии короче релиза из
 * AniSkip на 6,5 с, и опенинг у него на 5,6 с раньше — такую разметку
 * брать нельзя. Секунда-другая — это округление длины, а не другой монтаж.
 */
export const DURATION_TOLERANCE = 2.5

/** Короче этого пропускать нечего: кнопка мелькнула бы и исчезла. */
const MIN_LENGTH = 3

/**
 * Какие отрезки показывать в потоке длиной duration.
 *
 * Разметка с этого потока важнее чужой: если площадка разметила заставку,
 * заставка из общей базы не нужна, даже если длина совпала. Безымянный
 * отрезок (Kodik и Alloha пишут голые «от-до») в первой половине серии —
 * заставка, во второй — титры.
 */
export function pickSkips(raw: readonly SkipSegment[], duration: number): Skip[] {
  if (!(duration > 0)) return []
  const exact: Skip[] = []
  const foreign: Skip[] = []
  for (const s of raw) {
    if (!(s.end > s.start) || s.start < 0 || s.start >= duration) continue
    if (s.forDuration !== undefined && s.forDuration > 0
      && Math.abs(s.forDuration - duration) > DURATION_TOLERANCE) continue
    const kind = s.kind === 'skip' ? (s.start < duration / 2 ? 'intro' : 'outro') : s.kind
    // Титры, кончающиеся у самого конца, — до конца: иначе после пропуска
    // оставалась бы секунда чёрного кадра перед «дальше».
    let end = Math.min(s.end, duration)
    if (kind === 'outro' && duration - end < 5) end = duration
    if (end - s.start < MIN_LENGTH) continue
    ;(s.forDuration ? foreign : exact).push({ kind, start: s.start, end })
  }
  const have = new Set(exact.map(s => s.kind))
  const all = [...exact, ...foreign.filter(s => !have.has(s.kind))].sort((a, b) => a.start - b.start)
  // Перекрытия: из двух налезающих остаётся первый. Пропуск, ведущий
  // в середину другого пропуска, читался бы как глюк.
  const out: Skip[] = []
  for (const s of all) {
    const last = out[out.length - 1]
    if (last && s.start < last.end) continue
    out.push(s)
  }
  return out
}

/**
 * Отрезок, внутри которого сейчас позиция. У самого конца отрезка кнопка
 * уже не нужна — пропускать там почти нечего.
 */
export function activeSkip(skips: readonly Skip[], pos: number): Skip | null {
  for (const s of skips) {
    if (pos >= s.start - 0.25 && pos < s.end - 1) return s
  }
  return null
}

/** Подпись кнопки пропуска. */
export function skipLabel(kind: Skip['kind']): string {
  switch (kind) {
    case 'intro': return 'Пропустить заставку'
    case 'outro': return 'Пропустить титры'
    case 'recap': return 'Пропустить пересказ'
    case 'preview': return 'Пропустить анонс'
  }
}

/** Название отрезка — подпись полоски на шкале времени. */
export function skipName(kind: Skip['kind']): string {
  switch (kind) {
    case 'intro': return 'Заставка'
    case 'outro': return 'Титры'
    case 'recap': return 'Пересказ'
    case 'preview': return 'Анонс'
  }
}

/** Что пропущено — для подсказки после автоматического пропуска. */
export function skippedLabel(kind: Skip['kind']): string {
  switch (kind) {
    case 'intro': return 'Заставка пропущена'
    case 'outro': return 'Титры пропущены'
    case 'recap': return 'Пересказ пропущен'
    case 'preview': return 'Анонс пропущен'
  }
}
