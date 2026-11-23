import { test, expect } from '@playwright/test'
import { openTV } from './helpers.js'

const TITLE = 237 // сериал с сезонами: и плеер, и список серий на одной странице

/** Тикет выпускается площадкой; ждём дольше обычного. */
test.describe.configure({ mode: 'serial' })

test('клавиатура компьютера перематывает; повторный вход сохраняет перемотку назад', async ({ page }) => {
  let episodeID
  page.on('request', r => {
    const match = new URL(r.url()).pathname.match(/^\/api\/v1\/playback\/(\d+)$/)
    if (match) episodeID = match[1]
  })
  await openTV(page)
  await page.goto(`/?tv=0&n=${Date.now()}#/title/${TITLE}`)
  const video = page.locator('.kp video')
  await page.waitForFunction(() => document.querySelector('.kp video')?.readyState >= 2)
  await video.evaluate(v => { v.pause(); v.currentTime = 300 })
  await page.waitForFunction(() => !document.querySelector('.kp video').seeking)

  await page.locator('.kp-play').focus()
  await page.keyboard.press('ArrowRight')
  await expect.poll(() => video.evaluate(v => Math.round(v.currentTime))).toBe(310)
  await expect(page.locator('.kp-play')).toBeFocused()
  await page.locator('.kp-full').focus()
  await page.keyboard.press('ArrowLeft')
  await expect.poll(() => video.evaluate(v => Math.round(v.currentTime))).toBe(300)
  await expect(page.locator('.kp-full')).toBeFocused()

  await video.evaluate(v => { v.currentTime = 120.5 })
  await page.waitForFunction(() => !document.querySelector('.kp video').seeking)
  await expect.poll(async () => {
    const response = await page.request.get(`/api/v1/progress/${episodeID}`)
    return (await response.json()).positionMs
  }).toBe(120500)
  await page.reload()
  await page.waitForFunction(() => {
    const v = document.querySelector('.kp video')
    return v?.readyState >= 2 && !v.seeking && v.currentTime > 0
  })
  expect(Math.abs(await video.evaluate(v => v.currentTime) - 120.5)).toBeLessThan(1)
})

test('кнопка «играть» — круг ровно в центре кадра, значок в центре круга', async ({ page }) => {
  await openTV(page, `/title/${TITLE}`)
  await page.waitForSelector('button.kp-center', { timeout: 25000 })
  const geo = await page.evaluate(() => {
    const kp = document.querySelector('.kp')
    const c = document.querySelector('button.kp-center')
    const svg = c.querySelector('svg')
    const a = kp.getBoundingClientRect(), b = c.getBoundingClientRect(), g = svg.getBoundingClientRect()
    const mid = r => [r.left + r.width / 2, r.top + r.height / 2]
    const [ax, ay] = mid(a), [bx, by] = mid(b), [gx, gy] = mid(g)
    return {
      кругВЦентре: [Math.round(bx - ax), Math.round(by - ay)],
      значокВКруге: [Math.round(gx - bx), Math.round(gy - by)],
      круглая: Math.abs(b.width - b.height) < 1 && getComputedStyle(c).borderRadius === '50%',
      подложка: getComputedStyle(c).backgroundColor,
    }
  })
  // Старые движки телевизоров не растягивали кнопку и уводили значок к
  // краю — отсюда проверка центра, а не только наличия.
  expect(Math.abs(geo.кругВЦентре[0])).toBeLessThanOrEqual(1)
  expect(Math.abs(geo.кругВЦентре[1])).toBeLessThanOrEqual(1)
  expect(Math.abs(geo.значокВКруге[0])).toBeLessThanOrEqual(1)
  expect(Math.abs(geo.значокВКруге[1])).toBeLessThanOrEqual(1)
  expect(geo.круглая).toBe(true)
  expect(geo.подложка).not.toBe('rgba(0, 0, 0, 0)')
})

test('пульт в плеере: старт, перемотка, пауза, панель', async ({ page }) => {
  await openTV(page, `/title/${TITLE}`)
  await page.waitForSelector('button.kp-center', { timeout: 25000 })

  // Фокус витрина ставит сама на «играть»; OK запускает.
  await page.waitForFunction(() => document.activeElement?.classList.contains('kp-center'), null, { timeout: 15000 })
  await page.keyboard.press('Enter')

  // Пошло воспроизведение.
  await page.waitForFunction(() => {
    const v = document.querySelector('.kp video')
    return v && !v.paused && v.currentTime > 0
  }, null, { timeout: 30000 })

  const полныйЭкран = await page.evaluate(() => !!document.fullscreenElement)

  // Панель засыпает сама через пару секунд бездействия.
  await page.waitForSelector('.kp.idle', { timeout: 15000 })

  // Вправо — перемотка вперёд, независимо от того, где стоит фокус.
  const до = await page.evaluate(() => document.querySelector('.kp video').currentTime)
  await page.keyboard.press('ArrowRight')
  await page.waitForFunction(t => document.querySelector('.kp video').currentTime > t + 5,
    до, { timeout: 5000 })
  const после = await page.evaluate(() => document.querySelector('.kp video').currentTime)
  expect(после - до).toBeGreaterThan(5)

  // Влево — назад.
  await page.waitForSelector('.kp.idle', { timeout: 15000 })
  const до2 = await page.evaluate(() => document.querySelector('.kp video').currentTime)
  await page.keyboard.press('ArrowLeft')
  await page.waitForFunction(t => document.querySelector('.kp video').currentTime < t - 5,
    до2, { timeout: 5000 })

  // Вниз при спящей панели — панель просыпается, фокус на полосе времени.
  await page.waitForSelector('.kp.idle', { timeout: 15000 })
  await page.keyboard.press('ArrowDown')
  expect(await page.evaluate(() => document.querySelector('.kp').classList.contains('idle'))).toBe(false)
  expect(await page.evaluate(() => document.activeElement?.classList.contains('kp-seek'))).toBe(true)

  // OK при спящей панели — пауза.
  await page.waitForSelector('.kp.idle', { timeout: 15000 })
  await page.keyboard.press('Enter')
  await page.waitForFunction(() => document.querySelector('.kp video').paused, null, { timeout: 5000 })

  test.info().annotations.push({ type: 'полный экран при старте', description: String(полныйЭкран) })
})

test('полный экран переживает следующую серию без жеста', async ({ page }) => {
  await openTV(page, `/title/${TITLE}`)
  await page.waitForSelector('button.kp-center', { timeout: 25000 })
  await page.waitForFunction(() => document.activeElement?.classList.contains('kp-center'), null, { timeout: 15000 })
  await page.evaluate(() => {
    window.__fs = []
    document.addEventListener('fullscreenchange', () => window.__fs.push(document.fullscreenElement ? 'вход' : 'выход'))
  })
  await page.keyboard.press('Enter')
  await page.waitForFunction(() => document.fullscreenElement && !document.querySelector('.kp video').paused,
    null, { timeout: 30000 })
  const было = await page.evaluate(() => location.hash)

  // Переход без жеста — так его делает обратный отсчёт в конце серии.
  // Любому evaluate Playwright дорисовывает жест сам, поэтому голый
  // протокол: без него разрешения развернуться заново не будет.
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Runtime.evaluate', {
    expression: `document.querySelector('.kp-btn[aria-label="Следующая серия"]').click()`,
    userGesture: false,
  })
  await page.waitForFunction(h => location.hash !== h, было, { timeout: 5000 })
  await page.waitForFunction(() => {
    const v = document.querySelector('.kp video')
    return v && !v.paused && v.currentTime > 0
  }, null, { timeout: 30000 })

  const после = await page.evaluate(() => ({
    развёрнуто: !!document.fullscreenElement,
    кто: document.fullscreenElement?.className,
    события: window.__fs,
  }))
  expect(после.развёрнуто).toBe(true)
  expect(после.кто).toBe('playwrap')
  // Ни выхода, ни повторного входа: экран не моргнул страницей.
  expect(после.события).toEqual(['вход'])
})

test('серия из списка разворачивается сразу, не дожидаясь тикета', async ({ page }) => {
  await openTV(page, `/title/${TITLE}`)
  await page.waitForSelector('.epgrid .epcard', { timeout: 25000 })
  await page.evaluate(() => document.querySelectorAll('.epgrid .epcard')[1].focus())
  await page.keyboard.press('Enter')
  // Разворот — в том же нажатии, пока источник ещё готовится.
  await page.waitForFunction(() => !!document.fullscreenElement, null, { timeout: 1500 })
  expect(await page.evaluate(() => document.fullscreenElement.className)).toBe('playwrap')
  await page.waitForFunction(() => {
    const v = document.querySelector('.kp video')
    return v && !v.paused && v.currentTime > 0
  }, null, { timeout: 30000 })
  expect(await page.evaluate(() => !!document.fullscreenElement)).toBe(true)
  // Стрелки при спящей панели — у плеера: перемотка, а не листание страницы.
  await page.waitForSelector('.kp.idle', { timeout: 15000 })
  const до = await page.evaluate(() => document.querySelector('.kp video').currentTime)
  await page.keyboard.press('ArrowRight')
  await page.waitForFunction(t => document.querySelector('.kp video').currentTime > t + 5, до, { timeout: 5000 })
})

test('с открытой панелью стрелки водят фокус, как раньше', async ({ page }) => {
  await openTV(page, `/title/${TITLE}`)
  await page.waitForSelector('.kp-seek', { timeout: 20000 })
  await page.evaluate(() => document.querySelector('.kp-seek').focus())
  // Панель не спит: видео не запускали.
  expect(await page.evaluate(() => document.querySelector('.kp').classList.contains('idle'))).toBe(false)
  await page.keyboard.press('ArrowDown')
  const где = await page.evaluate(() => {
    const a = document.activeElement
    return { вПлеере: !!a.closest('.kp'), кто: (a.getAttribute('aria-label') || a.textContent || '').slice(0, 24) }
  })
  expect(где.вПлеере).toBe(true)
})

test('«назад» сначала закрывает список дорожек, плеер остаётся развёрнутым', async ({ page }) => {
  await openTV(page, `/title/${TITLE}`)
  await page.waitForSelector('button.kp-center', { timeout: 25000 })
  await page.waitForFunction(() => document.activeElement?.classList.contains('kp-center'), null, { timeout: 15000 })
  await page.keyboard.press('Enter')
  await page.waitForFunction(() => document.fullscreenElement && !document.querySelector('.kp video').paused,
    null, { timeout: 30000 })

  // Список «Аудио» открывается с надписи под названием и сразу забирает
  // фокус на выбранный пункт — с пульта до него иначе не добраться.
  await page.keyboard.press('ArrowUp')
  await page.evaluate(() => [...document.querySelectorAll('.kp-tab')][1].focus())
  await page.keyboard.press('Enter')
  await page.waitForSelector('.kp-pop', { timeout: 3000 })
  expect(await page.evaluate(() => !!document.activeElement?.closest('.kp-pop'))).toBe(true)

  await page.keyboard.press('Escape')
  await page.waitForFunction(() => !document.querySelector('.kp-pop'), null, { timeout: 3000 })
  expect(await page.evaluate(() => !!document.fullscreenElement)).toBe(true)

  // Второе «назад» — уже выход из полного экрана.
  await page.keyboard.press('Escape')
  await page.waitForFunction(() => !document.fullscreenElement, null, { timeout: 3000 })
})
