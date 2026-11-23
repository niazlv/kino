/**
 * Управление с пульта и клавиатуры: стрелки ведут фокус, OK нажимает,
 * «назад» закрывает то, что открыто, и лишь потом уходит со страницы.
 *
 * Работает везде, не только на телевизоре: на компьютере это стрелки
 * вместо Tab. Телевизор отличается раскладкой (класс tv) и тем, что
 * «назад» на нём — единственный способ вернуться.
 */
import { applyTV, isTV } from './mode'
import { DIRS, focusEl, navigate, preferred, scope, usable, type Dir } from './spatial'

const TEXT = 'input:not([type=checkbox]):not([type=radio]):not([type=range]):not([type=button]):not([type=submit]),textarea'

let routeAt = 0
let keyAt = 0
/** Что было в фокусе на каждом адресе: возврат назад попадает туда же. */
const memory = new Map<string, string>()
/**
 * Где фокус стоял в последний раз, в координатах экрана.
 *
 * Элемент под фокусом исчезает чаще, чем кажется: список обновился, кнопка
 * сменилась раскрытым списком. Браузер тогда роняет фокус на body — и без
 * этой памяти следующая стрелка увела бы в начало страницы.
 */
let lastRect: DOMRect | null = null

/** Элемент сам распоряжается стрелками: текст, ползунок, список, пометка data-arrows. */
function ownsArrows(t: HTMLElement, dir: Dir): boolean {
  const horizontal = dir === 'left' || dir === 'right'
  // Пометка — первой: поле поиска с открытыми подсказками забирает и
  // вертикальные стрелки (ходит по списку, не теряя набранного).
  const own = t.closest('[data-arrows]')?.getAttribute('data-arrows') ?? ''
  if (own === 'xy' || (own === 'x' && horizontal) || (own === 'y' && !horizontal)) return true
  if (t.matches(TEXT) || t.matches('input[type=range]')) return horizontal
  // Список на телевизоре стрелки НЕ забирает: они там единственный способ
  // уйти с элемента, и попавший в список фокус уже не выбирался наружу.
  // Значение меняют, открыв список кнопкой OK, — так же, как везде на ТВ.
  if (t.matches('select')) return !isTV()
  return false
}

/**
 * Пульт принадлежит плееру: телевизор, панель спрятана, а плеер — это и
 * есть экран (развёрнут во весь экран или держит фокус). Тогда ТВ-слой
 * стрелок не трогает: их обрабатывает сам плеер — перематывает, открывает
 * панель. Прежде фокус в этот момент уводило на страницу под плеером, и
 * пульт переставал управлять просмотром.
 */
function playerOwnsRemote(t: HTMLElement | null, dir: Dir): boolean {
  const kp = document.querySelector<HTMLElement>('.kp')
  if (!kp) return false
  if (isTV()) {
    if (!kp.classList.contains('idle')) return false
  } else {
    if (dir !== 'left' && dir !== 'right') return false
    if (!kp.hasAttribute('data-arrows')) return false
  }
  // Развёрнут не сам плеер, а обёртка страницы вокруг него.
  return Boolean(document.fullscreenElement?.contains(kp)) || kp.contains(t) || kp.contains(document.activeElement)
}

function isBack(e: KeyboardEvent, t: HTMLElement | null): boolean {
  if (e.key === 'Escape' || e.key === 'GoBack' || e.key === 'BrowserBack') return true
  // webOS и Tizen шлют свои коды, а не имена клавиш.
  if (e.keyCode === 461 || e.keyCode === 10009) return true
  return e.key === 'Backspace' && isTV() && !(t && t.matches(TEXT))
}

/**
 * «Назад» внутри страницы. true — было что закрыть. Оболочка Android
 * зовёт это перед тем, как листать историю, через window.kinoBack.
 */
export function back(): boolean {
  const dlg = document.querySelector<HTMLDialogElement>('dialog[open]')
  if (dlg) { dlg.close(); return true }
  // Меню плеера — раньше полного экрана: открытый поверх кадра список
  // закрывается первым, а плеер остаётся развёрнутым. Закрывает его
  // подложка под меню — она есть у любого меню плеера.
  const shade = document.querySelector<HTMLElement>('.kp-shade')
  if (shade) { shade.click(); return true }
  if (document.fullscreenElement) { void document.exitFullscreen(); return true }
  if (document.querySelector('.side.open')) {
    document.querySelector<HTMLElement>('.navclose')?.click()
    return true
  }
  return false
}

function onKey(e: KeyboardEvent): void {
  if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return
  const t = e.target instanceof HTMLElement ? e.target : null
  keyAt = Date.now()
  const dir = DIRS[e.key]
  if (dir) {
    if (t && ownsArrows(t, dir)) return
    // Пульт у плеера — не вмешиваемся, стрелку разберёт он сам.
    if (playerOwnsRemote(t, dir)) { e.preventDefault(); return }
    // Спящий плеер от первого нажатия только просыпается: иначе фокус
    // прыгнул бы с невидимой кнопки неизвестно куда. Без фокуса вовсе
    // (кнопка «играть» исчезла после старта) стрелка ведёт на полосу
    // времени: человек смотрит и хочет перемотать, а не листать страницу.
    if (t?.closest('.kp.idle')) { e.preventDefault(); return }
    const idle = document.querySelector<HTMLElement>('.kp.idle .kp-seek')
    if (idle && (!t || t === document.body)) {
      e.preventDefault()
      idle.focus({ preventScroll: true })
      return
    }
    const next = navigate(dir, document.activeElement === document.body ? lastRect : null)
    if (next) { e.preventDefault(); e.stopPropagation(); focusEl(next) }
    // Идти некуда — значит стоим. Нажатие всё равно перехватываем: у
    // WebView телевизора есть СВОЯ навигация стрелками, и всё, что мы не
    // разобрали, она разбирает по-своему — уводит фокус наискосок, из меню
    // в сетку фильмов. Два движка фокуса на один пульт — это и выглядело
    // как «фокус живёт своей жизнью». В браузере на компьютере стрелки
    // по умолчанию прокручивают страницу, и отбирать их не за что.
    else if (isTV()) e.preventDefault()
    return
  }
  if (isBack(e, t)) {
    if (back()) { e.preventDefault(); e.stopPropagation() }
    else if (isTV()) { e.preventDefault(); history.back() }
    return
  }
  // OK на ссылке: браузеры телевизоров не всегда превращают его в переход,
  // а кнопки они нажимают сами. Своё нажатие вместо встроенного, а не
  // вдобавок к нему — иначе на обычном браузере переход случался бы дважды.
  if (e.key === 'Enter' && t?.matches('a[href]')) {
    e.preventDefault()
    t.click()
  }
}

/** Запомнить, где стоял фокус, чтобы вернуться на то же место. */
function onFocus(e: FocusEvent): void {
  const t = e.target instanceof HTMLElement ? e.target : null
  if (!t) return
  const key = t.getAttribute('data-focus-key')
  const href = t.getAttribute('href')
  const sel = key ? `[data-focus-key="${CSS.escape(key)}"]` : href ? `a[href="${CSS.escape(href)}"]` : null
  if (sel) memory.set(location.hash, sel)
  lastRect = t.getBoundingClientRect()
}

/**
 * Новая страница: поставить фокус, когда она отрисуется. Содержимое
 * приходит с сервера, поэтому ждём до четырёх секунд, но отступаем, как
 * только человек пошёл стрелками сам.
 */
function settle(): void {
  let tries = 0
  const tick = () => {
    if (keyAt > routeAt) return
    const remembered = memory.get(location.hash)
    if (remembered) {
      // Возврат туда, откуда ушли: карточка появится вместе с сеткой,
      // и лучше подождать её пару секунд, чем встать на первую кнопку.
      const el = document.querySelector<HTMLElement>(remembered)
      if (el && usable(el)) { focusEl(el); return }
      if (tries < 20) { tries++; setTimeout(tick, 100); return }
    }
    // Первые полторы секунды ждём именно содержимое: иначе на пустой пока
    // странице фокус вставал бы на знак в меню и там оставался.
    const el = preferred(scope(), tries < 15)
    if (el && usable(el)) { focusEl(el); return }
    if (++tries < 40) setTimeout(tick, 100)
  }
  setTimeout(tick, 50)
}

/** Отдать фокус элементу, если человек ещё не пошёл сам после перехода. */
export function claimFocus(el: HTMLElement | null): void {
  if (el && keyAt <= routeAt) focusEl(el)
}

export function installTV(): void {
  applyTV()
  window.addEventListener('keydown', onKey, true)
  document.addEventListener('focusin', onFocus)
  window.addEventListener('hashchange', () => { routeAt = Date.now(); lastRect = null; settle() })
  ;(window as unknown as { kinoBack: () => boolean }).kinoBack = back
  if (isTV()) { routeAt = Date.now(); settle() }
}

export { isTV } from './mode'
