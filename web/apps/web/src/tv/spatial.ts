/**
 * Навигация фокусом по стрелкам: пульт телевизора, клавиатура, геймпад.
 *
 * Кандидат выбирается по геометрии — ближайший в направлении нажатия,
 * с предпочтением тем, кто перекрывается по поперечной оси: соседняя
 * карточка в ряду выигрывает у далёкой, но чуть более близкой по прямой.
 * Никаких «секций» и специальной разметки: страницы про пульт не знают.
 * Открытый модальный диалог и полноэкранный элемент замыкают навигацию
 * на себе, как это делает и обычный Tab.
 */
import { isTV } from './mode'

export type Dir = 'up' | 'down' | 'left' | 'right'

export const DIRS: Record<string, Dir> = {
  ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right',
}

const FOCUSABLE = [
  'a[href]', 'button:not([disabled])', 'input:not([disabled])', 'select:not([disabled])',
  'textarea:not([disabled])', '[tabindex]:not([tabindex="-1"])', '[role="slider"]', '[role="checkbox"]',
].join(',')

/** Где искать: диалог или полный экран, иначе вся страница. */
export function scope(): Element {
  return document.fullscreenElement
    ?? document.querySelector('dialog[open]')
    ?? document.body
}

/** Виден и доступен: не спрятан, не прозрачен, не выключен для указателя. */
export function usable(el: HTMLElement, top: Element = scope()): boolean {
  if (el.closest('[hidden],[inert]')) return false
  const cs = getComputedStyle(el)
  if (cs.visibility === 'hidden' || cs.display === 'none' || cs.pointerEvents === 'none') return false
  const r = el.getBoundingClientRect()
  if (r.width === 0 || r.height === 0) return false
  // Прозрачность не наследуется в вычисленном стиле, а спрятанная в покое
  // панель плеера прячет именно родителя.
  for (let n: HTMLElement | null = el; n && n !== top; n = n.parentElement) {
    if (getComputedStyle(n).opacity === '0') return false
  }
  return true
}

/**
 * Кто вообще может получить фокус. Видимость здесь НЕ проверяется: она
 * стоит вычисленных стилей на элемент и всех его предков, а кандидатов на
 * сетке каталога под сотню. Проверяет тот, кто выбрал победителя.
 */
function candidates(top: Element, cur: Element | null): HTMLElement[] {
  return Array.from(top.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(el =>
    el !== cur && !(cur && (cur.contains(el) || el.contains(cur))))
}

/** Меньше — лучше; null — кандидат не в этом направлении. */
function score(from: DOMRect, to: DOMRect, dir: Dir): number | null {
  const horizontal = dir === 'left' || dir === 'right'
  let ahead: number, overlap: number, across: number
  if (horizontal) {
    ahead = dir === 'left' ? from.left - to.right : to.left - from.right
    overlap = Math.min(from.bottom, to.bottom) - Math.max(from.top, to.top)
    across = Math.abs((from.top + from.bottom - to.top - to.bottom) / 2)
  } else {
    ahead = dir === 'up' ? from.top - to.bottom : to.top - from.bottom
    overlap = Math.min(from.right, to.right) - Math.max(from.left, to.left)
    across = Math.abs((from.left + from.right - to.left - to.right) / 2)
  }
  // Впереди — значит дальний край кандидата не ближе середины текущего:
  // так не теряются элементы, слегка налезающие друг на друга.
  if (ahead < -(horizontal ? from.width : from.height) / 2) return null
  // Вниз и вверх — строго по своему столбцу: кандидат обязан стоять под
  // нами, а не наискосок: иначе пункт в конце меню «видит» карточку
  // фильма правее и ниже, и стрелка вниз уносит из меню в сетку. Некуда
  // идти — остаёмся на месте: на телевизоре это понятнее, чем полёт через
  // экран. Вбок так строго нельзя: колонки по высоте не совпадают, и из
  // меню было бы не выйти вправо — там кандидат ищется мягче,
  // с большим штрафом за расхождение по высоте.
  if (!horizontal && overlap <= 0) return null
  ahead = Math.max(0, ahead)
  return overlap > 0 ? ahead + across * 0.3 : ahead + -overlap * 3 + across
}

/**
 * Куда вести фокус.
 *
 * `was` — где фокус стоял, если сейчас его нет. Элемент под фокусом
 * исчезает чаще, чем кажется: страница обновила список, кнопка сменилась
 * на раскрытый список. Браузер в таком случае роняет фокус на body, и без
 * этой подсказки следующая стрелка увела бы человека в начало страницы —
 * это и есть «фокус сбился».
 */
export function navigate(dir: Dir, was?: DOMRect | null): HTMLElement | null {
  const top = scope()
  const cur = document.activeElement as HTMLElement | null
  const живой = cur && cur !== document.body && top.contains(cur) && usable(cur, top)
  if (!живой && !was) return preferred(top)
  const from = живой ? cur.getBoundingClientRect() : was as DOMRect
  // Сначала одна только геометрия: прямоугольники читаются подряд и стоят
  // одного пересчёта раскладки на всё нажатие. Дорогую проверку видимости
  // проходят потом и лишь те, кто уже победил, — обычно первый же. На
  // сетке каталога прежний порядок съедал по стилю на каждого кандидата
  // и на каждого его предка, и телевизор шёл по сетке рывками.
  const ranked: { el: HTMLElement; s: number }[] = []
  for (const el of candidates(top, живой ? cur : null)) {
    const r = el.getBoundingClientRect()
    if (r.width === 0 || r.height === 0) continue
    const s = score(from, r, dir)
    if (s !== null) ranked.push({ el, s })
  }
  ranked.sort((a, b) => a.s - b.s)
  for (const { el } of ranked) if (usable(el, top)) return el
  // Фокуса не было и по направлению ничего нет — лучше встать хоть куда-то,
  // чем оставить пульт мёртвым.
  return живой ? null : preferred(top)
}

/**
 * С чего начать на странице: явно помеченное, иначе кнопка «играть»
 * (человек пришёл смотреть), иначе первое в содержимом.
 */
export function preferred(top: Element = scope(), contentOnly = false): HTMLElement | null {
  const content = top.querySelector('.content') ?? top
  for (const sel of ['[data-autofocus]', '.kp-center', '.kp-seek']) {
    const el = content.querySelector<HTMLElement>(sel)
    if (el && usable(el, top)) return el
  }
  // Содержимое ещё грузится — лучше подождать его, чем встать на меню.
  const first = (where: Element) => candidates(where, null).find(el => usable(el, top)) ?? null
  return first(content) ?? (contentOnly ? null : first(top))
}

export function focusEl(el: HTMLElement): void {
  el.focus({ preventScroll: true })
  // Кнопка плеера — часть плеера: в кадр ставится он целиком, а не одна
  // кнопка посередине экрана с обрезанным видео над ней. Липкую шапку
  // учитывает scroll-margin-top у .playbox в стилях.
  const kp = el.closest<HTMLElement>('.kp')
  // Плавная прокрутка на телевизоре — беда, а не украшение. Пока она
  // едет, разметка под следующее нажатие ещё старая: при быстром листании
  // меню фокус считался от несдвинувшегося положения и уезжал из меню в
  // сетку фильмов. Прыгаем сразу — и по кратчайшему пути, а не в центр:
  // на каждый шаг гонять весь список мимо глаз ни к чему.
  const tv = isTV()
  const smooth = !tv && !matchMedia('(prefers-reduced-motion: reduce)').matches
  ;(kp ?? el).scrollIntoView({
    block: kp || tv ? 'nearest' : 'center', inline: 'nearest',
    behavior: smooth ? 'smooth' : 'auto',
  })
}
