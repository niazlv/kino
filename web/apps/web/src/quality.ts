import { saveSetting, setting } from './native'

/**
 * Что это устройство заявляет площадкам при запросе потока: кодеки и
 * наибольшее качество.
 *
 * Как «Поддержка 4K / HEVC» в настройках устройства у kinopub: выбор делает
 * площадка по заявке, а заявка — настройка этого устройства (этого входа),
 * а не аккаунта. Телевизор тянет 4K, телефон в дороге — нет.
 *
 * Выше 1080p у площадок бывает в разных кодеках: у kinopub — HEVC, у
 * ЛордФильма — только AV1. Заявить кодек, которого устройство не умеет, —
 * получить чёрный экран или звук без картинки, поэтому по умолчанию кодеки
 * определяются сами, а качество — не выше 1080p.
 */

/** Настройка кодека: '' — определять самому, 'on' — заявлять, 'off' — нет. */
export type Claim = '' | 'on' | 'off'

export const QUALITIES = [1080, 1440, 2160] as const

// Строки кодеков — те, что стоят в плейлистах площадок: HEVC kinopub и AV1
// балансера ЛордФильма (av01, main, 8 бит, уровень 5.0).
const HEVC_TYPE = 'video/mp4; codecs="hvc1.1.6.L93.B0"'
const AV1_TYPE = 'video/mp4; codecs="av01.0.12M.08"'

/** Ответ самого элемента video — так HEVC определялся и до настройки. */
function playable(type: string): boolean {
  try {
    return document.createElement('video').canPlayType(type) !== ''
  } catch {
    return false
  }
}

/**
 * Умеет ли устройство AV1. Плеер играет через MediaSource (hls.js), а на
 * iOS — сам элемент video, поэтому годится любой из двух ответов.
 */
function playableAv1(): boolean {
  try {
    if (window.MediaSource?.isTypeSupported?.(AV1_TYPE)) return true
  } catch { /* старый движок */ }
  return playable(AV1_TYPE)
}

export const detectHevc = (): boolean => playable(HEVC_TYPE)
export const detectAv1 = (): boolean => playableAv1()

const claim = (key: 'hevc' | 'av1'): Claim => {
  const v = setting(key)
  return v === 'on' || v === 'off' ? v : ''
}

export const hevcClaim = (): Claim => claim('hevc')
export const av1Claim = (): Claim => claim('av1')

export function saveClaim(key: 'hevc' | 'av1', v: Claim): void {
  saveSetting(key, v)
}

/** Наибольшее качество, которое заявляется площадкам. */
export function maxQuality(): number {
  const n = Number(setting('maxq'))
  return (QUALITIES as readonly number[]).includes(n) ? n : 1080
}

export function saveMaxQuality(n: number): void {
  saveSetting('maxq', n === 1080 ? '' : String(n))
}

const claimed = (c: Claim, detected: () => boolean): boolean =>
  c === 'on' || (c === '' && detected())

/** Кодеки для заявки: H.264 умеют все, остальное — по настройке. */
export function claimedCodecs(): string[] {
  const out = ['avc1']
  if (claimed(hevcClaim(), detectHevc)) out.push('hvc1')
  if (claimed(av1Claim(), detectAv1)) out.push('av01')
  return out
}
