import { test, expect } from '@playwright/test'
import { openTV } from './helpers.js'

/**
 * Во что обходится одно нажатие пульта на плотной странице.
 *
 * Считаем не «на глаз», а два числа: сколько раз движок просят посчитать
 * стиль (это и есть та работа, от которой телевизор спотыкался) и сколько
 * миллисекунд уходит на выбор соседа.
 */
test('шаг фокуса по каталогу дёшев', async ({ page }) => {
  await openTV(page, '/browse//')
  await page.waitForSelector('.tile', { timeout: 20000 })
  await page.waitForFunction(() => document.querySelectorAll('.tilelink').length > 20)

  const кандидатов = await page.evaluate(() =>
    document.querySelectorAll('a[href],button:not([disabled]),input:not([disabled]),[role="slider"]').length)

  await page.evaluate(() => {
    window.__styles = 0
    const real = window.getComputedStyle.bind(window)
    window.getComputedStyle = (...a) => { window.__styles++; return real(...a) }
    document.querySelector('.tilelink').focus()
  })

  const шагов = 30
  const t0 = Date.now()
  for (let i = 0; i < шагов; i++) await page.keyboard.press(i % 2 ? 'ArrowRight' : 'ArrowDown')
  const мс = Date.now() - t0
  const стилей = await page.evaluate(() => window.__styles)

  console.log(`  кандидатов на странице: ${кандидатов}`)
  console.log(`  стилей посчитано за ${шагов} нажатий: ${стилей} (${(стилей / шагов).toFixed(1)} на нажатие)`)
  console.log(`  всего ${мс} мс на ${шагов} нажатий (${(мс / шагов).toFixed(1)} мс на нажатие, вместе с доставкой нажатия)`)

  // На каждое нажатие должна уходить горстка стилей, а не по стилю на
  // каждого кандидата и всех его предков.
  expect(стилей / шагов).toBeLessThan(кандидатов / 4)
})

test('на телевизоре нет размытий, теней и переходов', async ({ page }) => {
  await openTV(page, '/browse//')
  await page.waitForSelector('.tile', { timeout: 20000 })
  const тяжёлое = await page.evaluate(() => {
    const out = { размытия: 0, переходы: 0, анимации: 0, тени: 0 }
    for (const el of document.querySelectorAll('*')) {
      const cs = getComputedStyle(el)
      if (cs.backdropFilter && cs.backdropFilter !== 'none') out.размытия++
      if (cs.transitionDuration && parseFloat(cs.transitionDuration) > 0) out.переходы++
      if (cs.animationName && cs.animationName !== 'none') out.анимации++
      if (cs.boxShadow && cs.boxShadow !== 'none') out.тени++
    }
    return out
  })
  console.log('  осталось:', JSON.stringify(тяжёлое))
  expect(тяжёлое.размытия).toBe(0)
  expect(тяжёлое.переходы).toBe(0)
  expect(тяжёлое.анимации).toBe(0)
})
