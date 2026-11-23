import { api } from './api'

/**
 * Мост в Android-оболочку.
 *
 * Витрина одна и та же везде; в приложении в окне появляется объект kinoApp,
 * и только тогда включается всё «нативное»: своё окно скачивания, раздел со
 * скачанным, воспроизведение с диска. В браузере ничего этого нет и быть не
 * должно — иначе получились бы две расходящиеся витрины.
 */
export interface KinoApp {
  download(json: string): void
  folderName(): string
  chooseFolder(): void
  version(): string
  // Скачанное. Появилось позже окна скачивания, поэтому все методы
  // необязательные: на телефоне может стоять старая оболочка, а витрина
  // обновляется сама и обязана с ней уживаться.
  library?(): string
  librarySize?(): string
  play?(id: string, positionMs: string): void
  remove?(id: string): string
  setWatched?(id: string, watched: boolean): void
  synced?(id: string): void
  /** Найденному файлу (его не было в описи) — номер тайтла по названию папки. */
  setTitleId?(id: string, titleId: string): void
  playExternal?(json: string): string
  pip?(width: string, height: string): string
  /** Играет ли видео и какого оно размера — по этому оболочка сама уводит
   *  приложение в форточку, когда человек уходит на домашний экран. */
  mediaState?(playing: string, width: string, height: string): void
  getSetting?(key: string): string
  setSetting?(key: string, value: string): void
}

declare global {
  interface Window {
    kinoApp?: KinoApp
    /** Оболочка сообщает о входе в форточку и выходе из неё. */
    kinoPip?: (on: boolean) => void
    /** Кнопки системы в форточке: пауза и перемотка. */
    kinoPipAction?: (action: string) => void
    /** Оболочка нашла старые файлы, нарезала кадры или докачала постеры. */
    kinoLibraryChanged?: () => void
  }
}

/** Событие окна, на которое подписываются страницы со скачанным. */
export const LIBRARY_EVENT = 'kino-library'

/** Сигнал оболочки об изменениях в описи — событием окна, для любых страниц. */
export function installLibraryEvents(): void {
  if (!hasApp()) return
  window.kinoLibraryChanged = () => { window.dispatchEvent(new Event(LIBRARY_EVENT)) }
}

export const hasApp = (): boolean =>
  typeof window !== 'undefined' && Boolean(window.kinoApp)

/** Оболочка умеет скачанное (не только качать). */
export const hasLibrary = (): boolean =>
  hasApp() && typeof window.kinoApp?.library === 'function'

/**
 * Скачанная серия. Ключ — «тайтл:сезон:серия»: номера переживают переобход
 * каталога, а числовой id серии может смениться, и тогда скачанное потеряло
 * бы связь со своей серией.
 */
export interface LocalEpisode {
  id: string
  titleId: number
  title: string
  season: number
  episode: number
  uri: string
  size: number
  addedAt: number
  positionMs: number
  durationMs: number
  watched: boolean
  dirty: boolean
  /** Кадр из самого файла и постер тайтла — с устройства, видны без сети.
   *  Есть только у оболочки 0.1.8 и новее, и не сразу: готовятся фоном. */
  frame?: string
  poster?: string
}

/** Картинка скачанной серии: своя, с устройства, а сервер — запасом. */
export const localArt = (e: LocalEpisode): string | undefined =>
  e.frame ?? e.poster ?? (e.titleId > 0 ? `/api/v1/titles/${e.titleId}/art` : undefined)

export function library(): LocalEpisode[] {
  if (!hasLibrary()) return []
  try {
    const raw = window.kinoApp!.library!()
    const list = JSON.parse(raw) as LocalEpisode[]
    return Array.isArray(list) ? list : []
  } catch {
    return []
  }
}

export const localKey = (titleId: number, season: number, episode: number) =>
  `${titleId}:${season}:${episode}`

/** Скачанное одним тайтлом, ключ — «сезон:серия». */
export function localFor(titleId: number): Map<string, LocalEpisode> {
  const m = new Map<string, LocalEpisode>()
  for (const e of library()) {
    if (e.titleId === titleId) m.set(`${e.season}:${e.episode}`, e)
  }
  return m
}

/**
 * Позицию не передаём: при нуле оболочка берёт её из описи сама, и это
 * всегда свежее значение. Список на странице мог устареть, пока играл родной
 * плеер, — тогда «Смотреть» отматывал к месту, где страницу открыли.
 *
 * Досмотренное — с начала: с места остановки там одни титры, и плеер
 * закрылся бы через пару секунд. Ноль занят под «с места в описи», поэтому
 * начало — одна миллисекунда.
 */
export function playLocal(e: LocalEpisode): void {
  const fresh = library().find(x => x.id === e.id) ?? e
  window.kinoApp?.play?.(e.id, fresh.watched ? '1' : '0')
}

export const removeLocal = (e: LocalEpisode): boolean =>
  window.kinoApp?.remove?.(e.id) === '1'

export const setLocalWatched = (e: LocalEpisode, watched: boolean) =>
  window.kinoApp?.setWatched?.(e.id, watched)

export const bytes = (n: number): string => {
  if (n <= 0) return '0'
  const u = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ']
  const i = Math.min(u.length - 1, Math.floor(Math.log(n) / Math.log(1024)))
  const v = n / 1024 ** i
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${u[i]}`
}

/**
 * Отдаёт серверу прогресс, накопленный без сети.
 *
 * Смотрят скачанное как раз тогда, когда сети нет, поэтому плеер пишет
 * прогресс в опись, а сюда он попадает позже — при первом же открытии витрины
 * со связью. Номер серии на сервере узнаём по её месту в тайтле: оболочка
 * знает сезон и серию, но не числовой id.
 *
 * Молча переживает отсутствие сети: это не ошибка, а обычное состояние.
 */
export async function syncLocalProgress(): Promise<number> {
  if (!hasLibrary()) return 0
  // Найденному файлу без номера тайтла слать некуда — дождётся resolveLocalTitles.
  const pending = library().filter(e => e.dirty && e.titleId > 0)
  if (pending.length === 0) return 0

  const byTitle = new Map<number, LocalEpisode[]>()
  for (const e of pending) {
    const list = byTitle.get(e.titleId) ?? []
    list.push(e)
    byTitle.set(e.titleId, list)
  }

  let sent = 0
  for (const [titleId, items] of byTitle) {
    try {
      const { episodes } = await api.title(titleId)
      for (const e of items) {
        const ep = episodes.find(x => x.seasonNo === e.season && x.episodeNo === e.episode)
        if (!ep) continue
        if (e.positionMs > 0 && e.durationMs > 0) {
          await api.progress(ep.id, e.positionMs, e.durationMs)
        }
        if (e.watched) await api.setWatched(ep.id, true)
        window.kinoApp?.synced?.(e.id)
        sent++
      }
    } catch {
      // Нет сети или сервер молчит — попробуем в следующий раз.
    }
  }
  return sent
}

/**
 * Номер тайтла для серий, которые оболочка нашла в папке загрузок, а в
 * описи их не было (скачаны до неё). О них известно только название папки
 * — по нему и ищем, пока есть сеть: с номером серия встаёт на страницу
 * тайтла, получает постер, а её прогресс уходит на сервер.
 *
 * Ищем подсказками — только по нашему каталогу, площадки не спрашиваются.
 * Одноимённых не угадываем: лучше без номера, чем чужой тайтл.
 */
export function resolveLocalTitles(): Promise<number> {
  // Зовут и при запуске, и из «Скачанного» — одновременные вызовы делят один поиск.
  resolving ??= findLocalTitles().finally(() => { resolving = null })
  return resolving
}

let resolving: Promise<number> | null = null

async function findLocalTitles(): Promise<number> {
  if (typeof window.kinoApp?.setTitleId !== 'function') return 0
  const byName = new Map<string, LocalEpisode[]>()
  for (const e of library()) {
    if (e.titleId > 0 || !e.title) continue
    byName.set(e.title, [...(byName.get(e.title) ?? []), e])
  }
  let fixed = 0
  for (const [name, items] of byName) {
    try {
      const { items: found } = await api.suggest(name, 10)
      const hits = found.filter(t => sameName(t.title, name))
      if (hits.length !== 1) continue
      for (const e of items) window.kinoApp?.setTitleId?.(e.id, String(hits[0]!.id))
      fixed += items.length
    } catch {
      // Нет сети — найдём в следующий раз.
    }
  }
  return fixed
}

/** Папку называет оболочка: «/» и прочее недопустимое в имени — пробелами. */
const sameName = (a: string, b: string) => {
  const norm = (s: string) => s.toLowerCase().replace(/ё/g, 'е')
    .replace(/[\s.,:;!?'"«»()[\]\\/*<>|–—-]+/g, ' ').trim()
  return norm(a) === norm(b)
}

// ── настройки ──────────────────────────────────────────────────────────
// В приложении живут у оболочки: их должны одинаково видеть витрина,
// встроенный плеер и страница без сети, у которой своего хранилища нет.
// В браузере — обычный localStorage.

export type SettingKey = 'player' | 'stats' | 'maxq' | 'hevc' | 'av1'

export function setting(key: SettingKey): string {
  try {
    if (typeof window.kinoApp?.getSetting === 'function') return window.kinoApp.getSetting(key)
    return localStorage.getItem(`kino.${key}`) ?? ''
  } catch {
    return ''
  }
}

export function saveSetting(key: SettingKey, value: string): void {
  try {
    if (typeof window.kinoApp?.setSetting === 'function') window.kinoApp.setSetting(key, value)
    else localStorage.setItem(`kino.${key}`, value)
  } catch { /* приватный режим или запрет хранилища — не повод падать */ }
}

/** Оболочка умеет настройки и внешний плеер (старая — нет). */
export const hasSettings = (): boolean =>
  hasApp() && typeof window.kinoApp?.getSetting === 'function'

export const wantsExternalPlayer = (): boolean => setting('player') === 'external'
export const showStats = (): boolean => setting('stats') === '1'

/**
 * Открывает ссылку в стороннем плеере через системный выбор. Список
 * приложений не наш: его показывает Android, и он не устаревает.
 *
 * Возвращает false, если открыть нечем, — тогда зовущий играет сам.
 */
export function playExternal(url: string, title: string, positionMs = 0, mime = ''): boolean {
  if (typeof window.kinoApp?.playExternal !== 'function') return false
  // Ссылка тикета авторизует сама, поэтому чужому плееру не нужна наша
  // сессия; на всякий случай делаем её абсолютной.
  const abs = url.startsWith('http') ? url : new URL(url, location.origin).toString()
  return window.kinoApp.playExternal(JSON.stringify({
    url: abs, title, positionMs: Math.max(0, Math.round(positionMs)), mime,
  })) === '1'
}

export const canPlayExternal = (): boolean =>
  typeof window.kinoApp?.playExternal === 'function'

/**
 * Системная «картинка в картинке». В WebView браузерного PiP нет:
 * document.pictureInPictureEnabled там false, а запрос отклоняется молча.
 */
export function requestPip(width: number, height: number): boolean {
  if (typeof window.kinoApp?.pip !== 'function') return false
  return window.kinoApp.pip(String(Math.round(width)), String(Math.round(height))) === '1'
}

export const canPip = (): boolean => typeof window.kinoApp?.pip === 'function'

/** Шаг перемотки кнопками форточки — как у панели во весь экран. */
const PIP_SKIP = 15

/**
 * Форточка по-нативному: оболочка сама уводит приложение в «картинку в
 * картинке», когда человек уходит на домашний экран посреди просмотра, и
 * рисует в ней системные кнопки.
 *
 * Для этого оболочке нужно знать, играет ли видео, — витрина сообщает ей
 * о каждом старте и паузе. В ответ оболочка зовёт kinoPip (страница
 * показывает только плеер, во всё окно) и kinoPipAction (кнопки форточки).
 * Со старой оболочкой, где mediaState нет, ничего этого не включается.
 */
export function installPip(): void {
  if (typeof window.kinoApp?.mediaState !== 'function') return
  const video = () => document.querySelector<HTMLVideoElement>('.kp video')
  let last = ''
  const report = () => {
    const v = video()
    const playing = Boolean(v && !v.paused && !v.ended)
    const state = `${playing ? 1 : 0}:${v?.videoWidth ?? 0}x${v?.videoHeight ?? 0}`
    if (state === last) return
    last = state
    window.kinoApp?.mediaState?.(playing ? '1' : '0', String(v?.videoWidth ?? 0), String(v?.videoHeight ?? 0))
  }
  // События видео не всплывают — ловим на погружении, у документа: плеер
  // пересоздаётся на каждую серию, и подписываться на сам элемент нельзя.
  for (const e of ['play', 'playing', 'pause', 'ended', 'emptied', 'loadedmetadata']) {
    document.addEventListener(e, report, true)
  }
  // Ушли со страницы плеера — видео исчезло вместе с ней, а «пауза» от
  // удалённого элемента приходит не везде.
  addEventListener('hashchange', () => setTimeout(report, 0))

  window.kinoPip = on => { document.documentElement.classList.toggle('pip', on) }
  window.kinoPipAction = action => {
    const v = video()
    if (!v) return
    if (action === 'toggle') { if (v.paused) void v.play(); else v.pause() }
    else if (action === 'pause') v.pause()
    else if (action === 'back') v.currentTime = Math.max(0, v.currentTime - PIP_SKIP)
    else if (action === 'fwd') {
      const end = Number.isFinite(v.duration) && v.duration > 0 ? v.duration : Infinity
      v.currentTime = Math.min(end, v.currentTime + PIP_SKIP)
    }
  }
}
