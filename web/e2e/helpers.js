export const USER = { login: 'tvtest', password: 'tvtest-parol-1' }

/**
 * Войти и открыть витрину в режиме телевизора.
 *
 * Уникальный параметр в адресе обязателен: без него переход на тот же
 * документ с другим адресом после решётки — не загрузка, а событие
 * hashchange, и goto ждёт load до самого таймаута.
 */
export async function openTV(page, hash = '') {
  // Обе загрузки с уникальным параметром: иначе браузер берёт index.html
  // из кеша, а с ним и ссылки на старые сборки стилей — проверка тогда
  // судит о вчерашней витрине.
  await page.goto(`/?tv=1&n=${Date.now()}`)
  await page.evaluate(async u => {
    await fetch('/api/v1/auth/login', {
      method: 'POST', credentials: 'include',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ ...u, device: 'tv' }),
    })
  }, USER)
  await page.goto(`/?tv=1&n=${Date.now()}${hash ? '#' + hash : ''}`)
  await page.waitForFunction(() => document.documentElement.classList.contains('tv'))
  await page.waitForSelector('.content')
}

/** Что сейчас в фокусе — человекочитаемо. */
export const focused = page => page.evaluate(() => {
  const a = document.activeElement
  if (!a || a === document.body) return 'body'
  const name = (a.getAttribute('aria-label') || a.textContent || '').trim().slice(0, 30)
  return `${a.tagName.toLowerCase()}${a.className ? '.' + String(a.className).split(' ').join('.') : ''}|${name}`
})
