import { test, expect } from '@playwright/test'
import { openTV, focused } from './helpers.js'

test.describe('телевизор: раскладка и меню', () => {
  test('меню короткое, «Ещё» раскрывает остальное', async ({ page }) => {
    await openTV(page)
    const items = () => page.$$eval('.side .nav', ns => ns.map(n => n.textContent.trim()))
    const short = await items()
    expect(short.length).toBeLessThanOrEqual(6)
    expect(short).toContain('Продолжить смотреть')
    expect(short).toContain('Ещё')
    expect(short).not.toContain('Настройки')

    await page.click('.side .nav:has-text("Ещё")')
    const long = await items()
    expect(long.length).toBeGreaterThan(short.length)
    expect(long).toContain('Настройки')
    expect(long).toContain('История')
  })

  test('быстрое листание не выбрасывает из меню', async ({ page }) => {
    await openTV(page)
    await page.evaluate(() => document.querySelector('.side .nav').focus())
    // Двадцать нажатий подряд без пауз — как удержанная кнопка пульта.
    for (let i = 0; i < 20; i++) await page.keyboard.press('ArrowDown')
    const where = await page.evaluate(() => {
      const a = document.activeElement
      return { вМеню: !!a.closest('.side'), кто: (a.textContent || '').trim().slice(0, 24) }
    })
    expect(where.вМеню).toBe(true)
  })

  test('вправо из меню — в содержимое, и обратно влево', async ({ page }) => {
    await openTV(page)
    await page.evaluate(() => document.querySelector('.side .nav').focus())
    await page.keyboard.press('ArrowRight')
    expect(await page.evaluate(() => !!document.activeElement.closest('.content'))).toBe(true)
    await page.keyboard.press('ArrowLeft')
    expect(await page.evaluate(() => !!document.activeElement.closest('.side'))).toBe(true)
  })

  test('подсветка одна: текущий раздел и фокус выглядят по-разному', async ({ page }) => {
    await openTV(page, '/history')
    await page.waitForSelector('.side .nav:has-text("Ещё")')
    await page.click('.side .nav:has-text("Ещё")')
    await page.waitForSelector('.side .nav.on')
    const style = await page.evaluate(() => {
      const on = document.querySelector('.side .nav.on')
      const cs = getComputedStyle(on)
      return { фон: cs.backgroundColor, тень: cs.boxShadow }
    })
    // Текущий раздел — без заливки, только полоска слева.
    expect(style.фон).toMatch(/rgba\(0, 0, 0, 0\)|transparent/)
    expect(style.тень).not.toBe('none')
  })

  test('в шапке нет строки поиска, поиск — на своей странице', async ({ page }) => {
    await openTV(page)
    expect(await page.$$eval('.topbar input', els => els.filter(e => e.offsetParent !== null).length)).toBe(0)
    await openTV(page, '/search')
    await page.waitForSelector('.tvsearch')
    expect(await page.$$('.tvsearch input')).toHaveLength(1)
  })

  test('поиск с готовым запросом не открывает клавиатуру сам', async ({ page }) => {
    await openTV(page, '/search/герой')
    await page.waitForSelector('.tvsearch')
    // Поле появляется только после нажатия: пока — кнопка.
    expect(await page.$$('.tvsearch input')).toHaveLength(0)
    const f = await focused(page)
    expect(f).not.toContain('input')
    await page.click('button.tvsearch')
    expect(await page.$$('.tvsearch input')).toHaveLength(1)
  })
})

test.describe('телевизор: настройки', () => {
  test('фокус проходит все строки, кольцо видно', async ({ page }) => {
    await openTV(page, '/settings')
    await page.waitForSelector('.setrow input')
    const boxes = await page.$$('.setrow input')
    expect(boxes.length).toBeGreaterThanOrEqual(3)

    await page.evaluate(() => document.querySelector('.setrow input').focus())
    const seen = []
    for (let i = 0; i < boxes.length; i++) {
      seen.push(await page.evaluate(() => document.activeElement.closest('.setrow')?.querySelector('.setname')?.textContent))
      await page.keyboard.press('ArrowDown')
    }
    expect(seen).toContain('Режим телевизора')

    // Кольцо фокуса на галочке должно быть видимым, а не снятым.
    const ring = await page.evaluate(() => {
      const el = [...document.querySelectorAll('.setrow input')].pop()
      el.focus()
      const cs = getComputedStyle(el)
      return { ширина: cs.outlineWidth, стиль: cs.outlineStyle, размер: cs.width }
    })
    expect(ring.стиль).not.toBe('none')
    expect(parseFloat(ring.ширина)).toBeGreaterThanOrEqual(2)
    expect(parseFloat(ring.размер)).toBeGreaterThanOrEqual(20)
  })

  test('строка «Об устройстве» показывает движок и нехватки', async ({ page }) => {
    await openTV(page, '/settings')
    const text = await page.textContent('.content')
    expect(text).toContain('Об устройстве')
    expect(text).toMatch(/Движок/)
    expect(text).toMatch(/плотность/)
  })
})
