import { useEffect, useState } from 'react'

/**
 * Маршрутизация на hash без библиотеки.
 *
 * Отдельного экрана просмотра НЕТ намеренно: плеер живёт на странице тайтла.
 * Иначе, попав в него из «продолжить смотреть», человек не видит ни названия,
 * ни соседних серий, ни сезонов — и не может переключиться, не вернувшись
 * назад.
 */
export type Route =
  | { name: 'home' }
  | { name: 'list'; kind: 'following' | 'watchlist' }
  | { name: 'new' }
  | { name: 'browse'; kind: string; genre: string }
  // Свежие серии всего каталога — не только того, за чем следишь
  // («Новые эпизоды»): сериалы по последнему обновлению на любой площадке.
  | { name: 'fresh' }
  // Страница в адресе: «назад» возвращает на ту же страницу, а не на первую.
  | { name: 'history'; page: number }
  // Скачанное: раздел появляется только в приложении, но маршрут общий —
  // иначе ссылку на него нельзя было бы открыть после перезагрузки страницы.
  | { name: 'downloads' }
  | { name: 'settings' }
  // Профиль: кто вошёл, подключение устройств по коду, где вы вошли.
  | { name: 'profile' }
  | { name: 'search'; q: string }
  // Серия в адресе — сезоном и номером, как её называют люди: такую ссылку
  // можно переслать и прочитать. Числовой id серии остаётся ради старых
  // ссылок и уже сохранённых закладок.
  | { name: 'title'; id: number; episodeId?: number; season?: number; episode?: number }
  // Ссылка из инвайта: открывает регистрацию с уже подставленным кодом.
  | { name: 'invite'; code: string }
  // Подтверждение входа устройства: код из ссылки или введённый руками.
  | { name: 'link'; code: string }

export function parse(hash: string): Route {
  const [head, a, b] = hash.replace(/^#\/?/, '').split('/')
  if (head === 'title' && a) {
    const se = b ? /^s(\d+)e(\d+)$/i.exec(b) : null
    if (se) return { name: 'title', id: Number(a), season: Number(se[1]), episode: Number(se[2]) }
    return { name: 'title', id: Number(a), episodeId: b ? Number(b) : undefined }
  }
  if (head === 'search') return { name: 'search', q: a ? decodeURIComponent(a) : '' }
  if (head === 'invite' && a) return { name: 'invite', code: decodeURIComponent(a) }
  if (head === 'link') return { name: 'link', code: a ? decodeURIComponent(a) : '' }
  if (head === 'new') return { name: 'new' }
  if (head === 'fresh') return { name: 'fresh' }
  if (head === 'browse') {
    // Вид и жанр — две разные оси: аниме это жанр, а не вид, поэтому
    // «Сериалы» и «Аниме» могут пересекаться и должны задаваться отдельно.
    return { name: 'browse', kind: a ?? '', genre: b ? decodeURIComponent(b) : '' }
  }
  if (head === 'history') {
    const page = Number(a)
    return { name: 'history', page: Number.isInteger(page) && page > 1 ? page : 1 }
  }
  if (head === 'downloads') return { name: 'downloads' }
  if (head === 'settings') return { name: 'settings' }
  if (head === 'profile') return { name: 'profile' }
  if (head === 'list' && (a === 'following' || a === 'watchlist')) {
    return { name: 'list', kind: a }
  }
  return { name: 'home' }
}

export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => parse(location.hash))
  useEffect(() => {
    const on = () => setRoute(parse(location.hash))
    addEventListener('hashchange', on)
    return () => removeEventListener('hashchange', on)
  }, [])
  return route
}

export const go = (to: string) => { location.hash = to }

/**
 * Подменить адрес без записи в историю: страница уточняет, какую серию
 * открыла, и «назад» не должно возвращать на тот же тайтл без серии.
 * replaceState не шлёт hashchange, поэтому событие поднимается вручную.
 */
export const replace = (to: string) => {
  if (location.hash === `#${to}`) return
  history.replaceState(null, '', `#${to}`)
  dispatchEvent(new HashChangeEvent('hashchange'))
}

/** Адрес страницы истории; первая — без номера, как и было. */
export const historyPath = (page: number) => page > 1 ? `/history/${page}` : '/history'

/** Адрес серии. Фильм — один эпизод без сезона, ему хватает тайтла. */
export const titlePath = (titleId: number, seasonNo: number, episodeNo: number) =>
  seasonNo > 0 ? `/title/${titleId}/s${seasonNo}e${episodeNo}` : `/title/${titleId}`
