import { test, expect } from '@playwright/test'
import { openTV } from './helpers.js'

/**
 * Витрина на старом движке.
 *
 * Точной эмуляции нет и быть не может, но главное воспроизводится честно:
 * объявление, которого движок не понимает, он выбрасывает целиком. Здесь
 * стили на лету лишаются таких объявлений — ровно как это сделал бы
 * телевизор 2020 года, — и проверяется, что интерфейс остаётся рабочим.
 */
/**
 * Убрать из стилей объявления, которых старый движок не понял бы.
 *
 * Именно объявления целиком — так поступает и браузер: непонятое значение
 * выбрасывает вместе со свойством, а всё остальное в правиле оставляет.
 * Разбирать таблицу на блоки по скобкам нельзя: `@keyframes` и `@media`
 * вложены, и наивное деление рвёт таблицу — тогда проверка судит уже
 * не о витрине, а о собственной поломке.
 */
function ослабить(css, фичи) {
  let out = css
  if (фичи.includes('color-mix')) {
    out = out.replace(/[^;{}]*color-mix\([^;{}]*\)[^;{}]*(;|(?=\}))/g, '')
  }
  if (фичи.includes('aspect-ratio')) {
    out = out.replace(/aspect-ratio\s*:[^;{}]*(;|(?=\}))/g, '')
  }
  // Незнакомый псевдокласс роняет правило целиком, вместе со всем списком
  // селекторов, — так и здесь.
  if (фичи.includes(':focus-visible')) {
    out = out.replace(/[^{}]*:focus-visible[^{}]*\{[^{}]*\}/g, '')
  }
  return out
}

for (const набор of [['color-mix'], ['aspect-ratio'], ['color-mix', 'aspect-ratio'], [':focus-visible']]) {
  test(`движок без ${набор.join(' и без ')}`, async ({ page }) => {
    await page.route('**/*.css', async route => {
      const r = await route.fetch()
      // Заголовки исходного ответа переиспользовать нельзя: длина в них
      // от нетронутых стилей, а тело мы укоротили — браузер обрезал бы
      // таблицу на полуслове, и проверка врала бы через раз.
      route.fulfill({
        status: 200,
        contentType: 'text/css; charset=utf-8',
        body: ослабить(await r.text(), набор),
      })
    })
    await openTV(page, '/browse//')
    await page.waitForSelector('.tilelink', { timeout: 20000 })

    const вид = await page.evaluate(() => {
      const art = document.querySelector('.art')
      const nav = document.querySelector('.side .nav')
      const b = art.getBoundingClientRect()
      return {
        карточка: [Math.round(b.width), Math.round(b.height)],
        класс: art.className,
        минВысота: getComputedStyle(art).minHeight,
        картинка: art.querySelector('img')?.naturalWidth ?? 'нет',
        фонСтраницы: getComputedStyle(document.body).backgroundColor,
        цветПункта: getComputedStyle(nav).color,
        текстВиден: getComputedStyle(nav).color !== 'rgba(0, 0, 0, 0)',
      }
    })
    console.log(`  без ${набор.join('+')}: карточка ${вид.карточка.join('×')} (${вид.класс}, min-height ${вид.минВысота}, картинка ${вид.картинка}), фон ${вид.фонСтраницы}`)

    // Постер не должен схлопнуться в полоску, а фон — стать прозрачным.
    expect(вид.карточка[1]).toBeGreaterThan(80)
    expect(вид.фонСтраницы).not.toBe('rgba(0, 0, 0, 0)')
    expect(вид.текстВиден).toBe(true)

    // Подсветка фокуса на телевизоре не зависит от :focus-visible: пункт
    // меню под фокусом залит, карточка — в кольце. Без этого на движке
    // постарше фокус был невидим — «курсор терялся».
    const фокус = await page.evaluate(() => {
      const nav = document.querySelector('.side .nav')
      nav.focus()
      const пункт = getComputedStyle(nav).backgroundColor
      const tile = document.querySelector('.tilelink')
      tile.focus()
      return { пункт, кольцо: getComputedStyle(tile.querySelector('.art')).boxShadow }
    })
    expect(фокус.пункт).toBe('rgb(61, 220, 132)')
    expect(фокус.кольцо).not.toBe('none')

    // И круг «играть» на тайтле обязан стоять ровно посреди кадра: старый
    // движок уводил значок к левому краю.
    await page.goto(`/?tv=1&n=${Date.now()}#/title/237`)
    await page.waitForSelector('button.kp-center', { timeout: 25000 })
    const гео = await page.evaluate(() => {
      const kp = document.querySelector('.kp').getBoundingClientRect()
      const c = document.querySelector('button.kp-center').getBoundingClientRect()
      return {
        dx: Math.round(c.left + c.width / 2 - (kp.left + kp.width / 2)),
        dy: Math.round(c.top + c.height / 2 - (kp.top + kp.height / 2)),
      }
    })
    expect(Math.abs(гео.dx)).toBeLessThanOrEqual(1)
    expect(Math.abs(гео.dy)).toBeLessThanOrEqual(1)
  })
}
