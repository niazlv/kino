import { isTV } from './tv/mode'
import { claimedCodecs, maxQuality } from './quality'
/**
 * Клиент публичного API.
 *
 * Сессия живёт в HttpOnly-cookie, поэтому токенов здесь нет и быть не может:
 * из JS её не прочитать, и XSS не крадёт сессию. Отсюда credentials:'include'
 * и полное отсутствие работы с заголовком Authorization.
 */

export class ApiError extends Error {
  constructor(readonly status: number, readonly code: string, message: string) {
    super(message)
  }
  /** Сессия кончилась — повод показать вход, а не ошибку. */
  get unauthorized() { return this.status === 401 || this.status === 403 }
  /** Ответа не было вовсе: нет сети или сервер недоступен. */
  get offline() { return this.status === 0 }
}

/** Сколько витрина ждёт выпуска тикета: серверный потолок 25 с плюс запас
 *  на дорогу. */
const PLAYBACK_WAIT_MS = 40_000

async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
  let res: Response
  try {
    res = await fetch(`/api/v1${path}`, {
      ...init,
      credentials: 'include',
      headers: { 'content-type': 'application/json', ...(init.headers ?? {}) },
    })
  } catch (e) {
    // Без ответа fetch падает TypeError с текстом браузера («Failed to
    // fetch»), и он доходил до человека как есть. Отмена (AbortError) —
    // не про связь, её отдаём дальше.
    if (e instanceof TypeError) throw new ApiError(0, 'offline', 'нет связи с сервером')
    throw e
  }
  if (res.status === 204) return undefined as T
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError(res.status, body.error ?? 'unknown', body.message ?? res.statusText)
  }
  return body as T
}

export interface Me {
  id: number; login: string; displayName: string; role?: string
  /** Когда заведён — «в kino с …» в профиле. */
  createdAt?: string
  /**
   * Локальный движок на этом устройстве (ADR-0015): пароля, выхода и входа
   * по коду нет — аккаунт один и привязан к устройству.
   */
  local?: boolean
}
/** Где человек вошёл: своя сессия витрины. */
export interface MySession {
  id: string; device: string; issuedAt: string
  /** Когда вход кончится сам, если устройством не пользоваться. */
  until: string
  /** Вошло по коду и продлевается само: телевизор, приложение. */
  renewable?: boolean
  /** Та самая сессия, из которой смотрят профиль. */
  current?: boolean
}
/** Запрос устройства на вход: кто просит и до какого момента код жив. */
export interface DeviceAuthInfo { device: string; requestedAt: string; expiresAt: string }
export interface DeviceBegin {
  deviceCode: string; userCode: string; verificationUrl: string; expiresAt: string; interval: number
}
export interface ContinueItem {
  episodeId: number; titleId: number; title: string
  kind: string; year: number; hasArt: boolean
  seasonNo: number; episodeNo: number; positionMs: number; durationMs: number
}
/** Площадка, с которой приходит видео. */
export interface SourceRef { id: string; name: string }
export interface Title {
  id: number; kind: string; title: string; year: number
  /** Вид произведения: anime, cartoon, dorama, series, movie. Точнее kind. */
  species?: string
  imdb?: string; plot?: string; genres?: string[]; finished?: boolean; hasArt: boolean
  /** Площадки тайтла по приоритету; только на странице тайтла. */
  sources?: SourceRef[]
}
export interface Episode {
  id: number; seasonNo: number; episodeNo: number; title?: string; durationSec: number
  positionMs?: number; status?: string; hasArt: boolean
}
/** Выбор в плеере для тайтла. null — не выбирал; субтитры '' — выключены;
 *  площадка '' — «автоматически». */
export interface Prefs { audio: string | null; subtitle: string | null; source?: string | null }
export interface NewEpisodeItem {
  titleId: number; title: string; kind: string; year: number
  hasArt: boolean; unwatched: number; total: number
}
export interface HistoryItem {
  episodeId: number; titleId: number; title: string; kind: string
  seasonNo: number; episodeNo: number
  positionMs: number; durationMs: number; status: string
  hasArt: boolean; hasShot: boolean; updatedAt: string
}
export interface Track {
  id: string; language: string; title: string
  /** Только у субтитров отдельным файлом: адрес на шлюзе. В плейлисте их
   *  нет, поэтому утилите скачивания и внешнему плееру они не достанутся. */
  url?: string
  /** Включить сразу: вариант «с субтитрами». */
  default?: boolean
}
export interface Rendition { height: number; codec?: string }
/** Поток той же серии у той же площадки: у аниме-площадок так устроены озвучки. */
export interface Variant { id: string; title: string; player?: string }
export interface Playback {
  ticketId: string; url: string; protocol: string; expiresAt: string
  audios: Track[]; subtitles: Track[]; renditions: Rendition[]
  /** Площадка, выдавшая поток, и все, у кого эта серия есть. */
  source?: SourceRef; sources?: SourceRef[]
  /** Площадки, стоявшие в очереди раньше выдавшей, и почему поток не с них. */
  passed?: PassedSource[]
  variants?: Variant[]; variant?: string
  /** Разметка пропуска, пришедшая вместе с потоком. Остальная — skips(). */
  segments?: SkipSegment[]
}
/**
 * Что в серии можно пропустить. Время — секунды от начала файла; kind
 * skip — «пропустить», а что это, плеер решит по месту в серии.
 * forDuration — длина размеченного файла, если разметка чужая (AniSkip).
 */
export interface SkipSegment {
  kind: 'intro' | 'outro' | 'recap' | 'preview' | 'skip'
  start: number; end: number; forDuration?: number; origin?: string
}
/** down — лежит по свежей памяти сервера, failed — не ответила,
 *  busy — просит притормозить, missing — нет этой серии, novoice — серия
 *  есть, но без запомненной студии, а у выдавшей площадки она есть,
 *  slow — её поток только что тормозил на этом устройстве (avoid),
 *  reauth — отвергла наш вход, ждёт переавторизации администратором. */
export interface PassedSource extends SourceRef { reason: 'down' | 'failed' | 'busy' | 'missing' | 'novoice' | 'slow' | 'reauth' }
/**
 * Озвучка серии в общем списке: студия один раз, сколько бы площадок её ни
 * отдавали. ways — где она есть; без variant — дорожка общего потока
 * (kinopub), её переключает сам плеер.
 */
export interface VoiceOption {
  key: string; title: string
  ways: { source: string; variant?: string; player?: string }[]
}
/** silent — площадки, не рассказавшие о своих озвучках: список неполон. */
export interface VoiceList { voices: VoiceOption[]; silent?: PassedSource[] }
/** Чего хочет человек от выпуска тикета. */
export interface PlaybackChoice {
  /** Площадка: запомненная или выбранная только что. */
  source?: string
  /** Выбрана только что руками — не подменять другой площадкой. */
  strict?: boolean
  variant?: string
  /** Запомненная студия озвучки — подходит к любой площадке. */
  variantLabel?: string
  /** Студия из общего списка (VoiceOption.key): сервер возьмёт её с той
   *  площадки, где она есть; source тогда — лишь предпочтение. */
  voice?: string
  /** Площадки, чей поток на этом устройстве только что тормозил: сервер
   *  спросит их последними. Строгий выбор сильнее. */
  avoid?: string[]
}

export type ShareKind = 'watch' | 'download'
export interface ShareLink {
  token: string; kind: ShareKind; url: string
  command?: string; expiresAt: string
}
/** Пустой список = «всё»: так по умолчанию выбрано всё, без перечисления. */
export interface ShareSelection {
  maxHeight?: number; audios?: string[]; subtitles?: string[]
}
export interface Marks {
  following: boolean; watchlist: boolean; hidden: boolean
}

/**
 * Визитка сервера (GET /api/v1/server, без входа): по ней экран входа
 * решает, показывать ли регистрацию и нужен ли для неё код приглашения.
 */
export type ServerInfo = {
  kind: string
  api: number
  name?: string
  /** invite — по коду приглашения, open — любой, closed — только из админки. */
  registration: 'invite' | 'open' | 'closed'
  local?: boolean
}

export const api = {
  me: () => call<Me>('/me'),
  serverInfo: () => call<ServerInfo>('/server'),
  login: (login: string, password: string) =>
    call<{ expiresAt: string }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ login, password, device: deviceKind() }),
    }),
  register: (invite: string, login: string, password: string, displayName: string) =>
    call<Me>('/auth/register', {
      method: 'POST',
      body: JSON.stringify({ invite, login, password, displayName }),
    }),
  logout: () => call<void>('/auth/logout', { method: 'POST' }),
  // Вход устройства: посмотреть запрос по коду человека, разрешить, отклонить.
  // Вход этого устройства по коду: телевизор показывает код, человек
  // подтверждает с телефона. cookie: true — сессию ставим в cookie, как при
  // обычном входе, токены этой странице ни к чему.
  deviceBegin: () => call<DeviceBegin>('/auth/device', {
    method: 'POST', body: JSON.stringify({ device: deviceKind() }),
  }),
  devicePoll: (deviceCode: string) => call<void>('/auth/device/token', {
    method: 'POST', body: JSON.stringify({ deviceCode, cookie: true }),
  }),
  deviceAuth: (code: string) => call<DeviceAuthInfo>(`/auth/device/${encodeURIComponent(code)}`),
  approveDevice: (code: string) =>
    call<void>(`/auth/device/${encodeURIComponent(code)}/approve`, { method: 'POST' }),
  denyDevice: (code: string) =>
    call<void>(`/auth/device/${encodeURIComponent(code)}/deny`, { method: 'POST' }),
  /** «Где вы вошли»: свои сессии витрины, выход на одной и на остальных. */
  mySessions: () => call<{ items: MySession[] }>('/me/sessions'),
  signOutSession: (id: string) =>
    call<void>(`/me/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  signOutOthers: () => call<{ signedOut: number }>('/me/sessions', { method: 'DELETE' }),

  /** «Продолжить смотреть» страницей: total — всё начатое и недосмотренное. */
  continueWatching: (offset = 0) =>
    call<{ items: ContinueItem[]; total: number; nextOffset: number; hasMore: boolean }>(
      `/continue${offset > 0 ? `?offset=${offset}` : ''}`),
  /** Поиск страницей: offset — где продолжать, total — сколько нашлось всего. */
  search: (q: string, offset = 0) =>
    call<{ items: Title[]; total: number; nextOffset: number; hasMore: boolean }>(
      `/search?q=${encodeURIComponent(q)}${offset > 0 ? `&offset=${offset}` : ''}`),
  /** Тайтлы под строкой, пока набирают: только наш каталог, площадки
   *  не спрашиваются — это делает сам поиск по Enter. */
  suggest: (q: string, limit = 6) =>
    call<{ items: Title[] }>(`/search/suggest?q=${encodeURIComponent(q)}&limit=${limit}`),
  /** История поиска: своя у каждого, живёт на сервере — подсказки над
   *  строкой на телефоне, в браузере и на телевизоре одни и те же.
   *  Все операции возвращают свежий список. */
  searchHistory: () => call<{ items: string[] }>('/search/history'),
  rememberSearch: (q: string) =>
    call<{ items: string[] }>('/search/history', { method: 'POST', body: JSON.stringify({ query: q }) }),
  forgetSearch: (q: string) =>
    call<{ items: string[] }>(`/search/history?q=${encodeURIComponent(q)}`, { method: 'DELETE' }),
  clearSearchHistory: () => call<{ items: string[] }>('/search/history', { method: 'DELETE' }),
  title: (id: number) => call<{ title: Title; episodes: Episode[]; prefs?: Prefs }>(`/titles/${id}`),
  /** Одно переключение за раз: поле, которого нет, не меняется. */
  setPrefs: (titleId: number, p: Partial<{ audio: string; subtitle: string; source: string }>) =>
    call<Prefs>(`/titles/${titleId}/prefs`, { method: 'PUT', body: JSON.stringify(p) }),

  marks: (titleId: number) => call<Marks>(`/titles/${titleId}/marks`),
  setMarks: (titleId: number, m: Marks) =>
    call<Marks>(`/titles/${titleId}/marks`, { method: 'PUT', body: JSON.stringify(m) }),

  /** Скрыть с главной и из списков, или вернуть. Отметки пишутся целиком,
   *  поэтому сначала читаем текущие: остальное должно уцелеть. */
  setHidden: async (titleId: number, hidden: boolean) =>
    api.setMarks(titleId, { ...(await api.marks(titleId)), hidden }),

  /** Списки по намеренным отметкам: слежу, буду смотреть.
   *  Страница с total/hasMore — список докручивается, а не обрезан. */
  list: (kind: 'following' | 'watchlist', offset = 0) =>
    call<{ items: Title[]; total: number; nextOffset: number; hasMore: boolean }>(
      `/lists/${kind}${offset > 0 ? `?offset=${offset}` : ''}`),

  browse: (p: { kind?: string; genre?: string; sort?: string; page?: number }) => {
    const q = new URLSearchParams()
    if (p.kind) q.set('kind', p.kind)
    if (p.genre) q.set('genre', p.genre)
    if (p.sort) q.set('sort', p.sort)
    if (p.page && p.page > 1) q.set('page', String(p.page))
    return call<{ items: Title[]; total: number }>(`/browse?${q}`)
  },
  genres: () => call<{ items: string[] }>('/genres'),

  setWatched: (episodeId: number, watched: boolean) =>
    call<{ watched: boolean }>(`/progress/${episodeId}/watched`, {
      method: 'PUT', body: JSON.stringify({ watched }),
    }),
  setSeasonWatched: (titleId: number, season: number, watched: boolean) =>
    call<{ watched: boolean; episodes: number }>(
      `/titles/${titleId}/seasons/${season}/watched`,
      { method: 'PUT', body: JSON.stringify({ watched }) },
    ),

  /** only=new — лишь с непросмотренным; иначе всё слежение со счётчиками.
   *  Лента страницей: total — всё слежение, activeTotal — с непросмотренным. */
  newEpisodes: (onlyNew: boolean, offset = 0) => {
    const q = new URLSearchParams()
    if (onlyNew) q.set('only', 'new')
    if (offset > 0) q.set('offset', String(offset))
    const qs = q.toString()
    return call<{ items: NewEpisodeItem[]; total: number; activeTotal: number; nextOffset: number; hasMore: boolean }>(
      `/new-episodes${qs ? `?${qs}` : ''}`)
  },
  /** Страница истории по номеру, с общим числом страниц; kind и q сужают. */
  history: (page = 1, f: { kind?: string; q?: string } = {}) => {
    const s = new URLSearchParams()
    if (page > 1) s.set('page', String(page))
    if (f.kind) s.set('kind', f.kind)
    if (f.q) s.set('q', f.q)
    const qs = s.toString()
    return call<{ items: HistoryItem[]; page: number; pages: number; total: number }>(
      `/history${qs ? `?${qs}` : ''}`)
  },

  /**
   * Выпуск тикета. Сервер сам не ждёт площадки дольше 25 с (и переходит к
   * запасной через 8 с), но связь до сервера тоже бывает мёртвой — поэтому
   * и здесь есть предел: крутилка без конца хуже честной ошибки с «Повторить».
   * signal — отмена, когда серию закрыли или переключили: сервер бросит
   * площадку и не засчитает ей отказ.
   */
  playback: async (episodeId: number, choice: PlaybackChoice & { external?: boolean } = {}, signal?: AbortSignal) => {
    const ctl = new AbortController()
    const timer = setTimeout(() => ctl.abort(), PLAYBACK_WAIT_MS)
    const onAbort = () => ctl.abort()
    signal?.addEventListener('abort', onAbort)
    try {
      return await call<Playback>(`/playback/${episodeId}`, {
        method: 'POST',
        signal: ctl.signal,
        body: JSON.stringify({
          // Профиль клиента отдаёт браузер: выбор кодека делает провайдер,
          // а не прибитая к одной площадке логика на сервере. Что заявлять —
          // настройка этого устройства (раздел «Качество»).
          codecs: claimedCodecs(),
          maxHeight: maxQuality(),
          supportsHls: true,
          ...choice,
        }),
      })
    } catch (e) {
      if (ctl.signal.aborted && !signal?.aborted) {
        throw new ApiError(504, 'timeout', 'площадки не ответили')
      }
      throw e
    } finally {
      clearTimeout(timer)
      signal?.removeEventListener('abort', onAbort)
    }
  },

  /** Текущая позиция — чтобы продолжить с места. */
  /** Озвучки серии со всех площадок. Просится, когда серия уже играет. */
  voices: (episodeId: number) => call<VoiceList>(`/episodes/${episodeId}/voices`),
  /** Ключи озвучек по подписям дорожек потока: правило склейки и
   *  псевдонимы студий — на сервере, клиент их не повторяет. */
  voiceKeys: (labels: string[]) =>
    call<{ keys: Record<string, { key: string; title: string }> }>('/voices/keys', {
      method: 'POST', body: JSON.stringify({ labels }),
    }),

  /** Разметка пропуска, которой не было в ответе на выпуск тикета:
   *  площадки, отдающие её отдельно, и AniSkip. source и variant — что
   *  играет: разметка привязана к файлу. */
  skips: (episodeId: number, source?: string, variant?: string) => {
    const q = new URLSearchParams()
    if (source) q.set('source', source)
    if (variant) q.set('variant', variant)
    return call<{ segments: SkipSegment[] }>(`/episodes/${episodeId}/skips?${q}`)
  },

  progress0: (episodeId: number) =>
    call<{ positionMs: number; durationMs: number; status: string }>(`/progress/${episodeId}`),

  progress: (episodeId: number, positionMs: number, durationMs: number, clientTs = new Date().toISOString()) =>
    call<void>(`/progress/${episodeId}`, {
      method: 'PUT',
      body: JSON.stringify({ positionMs, durationMs, clientTs }),
    }),

  telemetry: (payload: Record<string, unknown>) =>
    call<void>('/telemetry/session', { method: 'POST', body: JSON.stringify(payload) }),

  createShare: (episodeId: number, kind: ShareKind, sel: ShareSelection = {}, hours = 0) =>
    call<ShareLink>(`/share/${episodeId}`, {
      method: 'POST',
      body: JSON.stringify({ kind, hours, ...sel }),
    }),
  changePassword: (oldPassword: string, newPassword: string) =>
    call<{ expiresAt?: string; reloginRequired?: boolean }>('/me/password', {
      method: 'PUT',
      body: JSON.stringify({ oldPassword, newPassword }),
    }),
  listShares: () => call<{ items: ShareLink[] }>('/share'),
  revokeShare: (token: string) =>
    call<void>(`/share/${token}`, { method: 'DELETE' }),
}

export function deviceKind(): string {
  if (isTV()) return 'tv'
  const ua = navigator.userAgent
  if (/iPhone|iPad|iPod/.test(ua)) return 'ios'
  if (/Android/.test(ua)) return 'android'
  if (/TV|SmartTV|Tizen|Web0S/i.test(ua)) return 'tv'
  return 'desktop'
}
