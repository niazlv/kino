/**
 * Режим телевизора: экран в трёх метрах и пульт вместо мыши.
 *
 * Определяется по UA (Android-оболочка на телевизоре дописывает «kino-tv»,
 * у браузеров Samsung и LG свои метки) или руками: ?tv=1 в адресе включает
 * и запоминает, ?tv=0 выключает. Ручной способ нужен для проверки на
 * обычном компьютере и на случай телевизора, который не опознался.
 */
const KEY = 'kino.tv'

/**
 * Ответ не меняется в течение жизни страницы: переключатель в настройках
 * так и написан — «применится после перезагрузки». А спрашивают отсюда
 * часто, на каждый шаг фокуса, и каждый раз лезть в localStorage на
 * телевизоре заметно.
 */
let cached: boolean | null = null

export function isTV(): boolean {
  if (cached === null) cached = detectTV()
  return cached
}

function detectTV(): boolean {
  try {
    const q = new URLSearchParams(location.search).get('tv')
    if (q === '1') { localStorage.setItem(KEY, '1'); return true }
    if (q === '0') { localStorage.removeItem(KEY); return false }
    if (localStorage.getItem(KEY) === '1') return true
  } catch { /* приватный режим: только по UA */ }
  return /kino-tv|SmartTV|Tizen|Web0S|AFT[A-Z]|BRAVIA|SHIELD|Android TV|CrKey/i.test(navigator.userAgent)
}

/**
 * Масштаб. Раскладка рассчитана на ~960 CSS-пикселей ширины при Full HD:
 * Android TV сам даёт такую плотность, а браузеры телевизоров рисуют
 * 1920 пикселей один к одному, и с дивана это нечитаемо.
 */
export function applyTV(): void {
  if (!isTV()) return
  document.documentElement.classList.add('tv')
  const zoom = Math.min(2.2, Math.max(1, 2 / (window.devicePixelRatio || 1)))
  if (zoom > 1.05) document.documentElement.style.setProperty('zoom', String(zoom))
}
