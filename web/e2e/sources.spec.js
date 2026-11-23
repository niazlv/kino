import { test, expect } from '@playwright/test'
import { USER } from './helpers.js'

// Площадки, дополняющие друг друга (ADR-0008). Номера тайтлов — стенда:
// «Принцесса-шпионка» у kinopub, к которой склейкой привязан animego.me,
// и «Принцесса-медуза», которой у kinopub нет вовсе (только animego.fr).
const MERGED = 38219
const ANIME_ONLY = 53018

test.describe.configure({ mode: 'serial' })

async function openDesktop(page, hash) {
  await page.goto(`/?n=${Date.now()}`)
  await page.evaluate(async u => {
    await fetch('/api/v1/auth/login', {
      method: 'POST', credentials: 'include',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ ...u, device: 'desktop' }),
    })
  }, USER)
  await page.goto(`/?n=${Date.now()}#${hash}`)
  await page.waitForSelector('.content')
}

// Меню переживает смену потока — открывать, только если закрыто.
async function openMenu(page) {
  await page.locator('.kp').hover()
  if (!(await page.locator('.kp-menu').isVisible())) {
    await page.getByRole('button', { name: 'Настройки' }).click()
  }
}

const playing = page => page.waitForFunction(() => {
  const v = document.querySelector('.kp video')
  return v && !v.paused && v.currentTime > 0.5
}, null, { timeout: 40000 })

test('склеенный тайтл: играет kinopub, переключение на AnimeGO.me с озвучкой', async ({ page }) => {
  test.setTimeout(120000)
  // Запомненный прошлым прогоном выбор сбрасываем: проверяется приоритет.
  await page.goto(`/?n=${Date.now()}`)
  await page.evaluate(async ([u, id]) => {
    await fetch('/api/v1/auth/login', { method: 'POST', credentials: 'include',
      headers: { 'content-type': 'application/json' }, body: JSON.stringify({ ...u, device: 'desktop' }) })
    await fetch(`/api/v1/titles/${id}/prefs`, { method: 'PUT', credentials: 'include',
      headers: { 'content-type': 'application/json' }, body: JSON.stringify({ source: '' }) })
  }, [USER, MERGED])

  await openDesktop(page, `/title/${MERGED}/s1e10`)
  await expect(page.locator('dl.info')).toContainText('AnimeGO.me')
  await page.waitForSelector('button.kp-center', { timeout: 30000 })
  await page.locator('button.kp-center').click()
  await playing(page)
  await expect(page.locator('.engine', { hasText: 'площадка' })).toContainText('kinopub')

  await openMenu(page)
  // Площадка — «Источник» под «Видео»: «Автоматически», kinopub и хотя
  // бы AnimeGO.me; досбор мог добавить и другие площадки.
  const group = page.locator('.kp-choice', { has: page.locator('h4', { hasText: 'Источник' }) })
  await expect(group.getByRole('button', { name: 'kinopub', exact: true })).toBeVisible()
  expect(await group.locator('button').count()).toBeGreaterThanOrEqual(3)
  const posBefore = await page.evaluate(() => document.querySelector('.kp video').currentTime)
  await group.getByRole('button', { name: 'AnimeGO.me', exact: true }).click()

  await expect(page.locator('.engine', { hasText: 'площадка' })).toContainText('AnimeGO.me', { timeout: 40000 })
  await playing(page)
  // Продолжает с того же места, а не с начала серии.
  const posAfter = await page.evaluate(() => document.querySelector('.kp video').currentTime)
  expect(posAfter).toBeGreaterThanOrEqual(Math.floor(posBefore))
  // Панель следит за новым потоком, а не застыла на старом.
  await expect(page.locator('.kp-time')).not.toHaveText('0:00 / 0:00', { timeout: 10000 })

  await openMenu(page)
  // Озвучки всех площадок одним списком, играющая отмечена.
  const voices = page.locator('.kp-choice', { has: page.locator('h4', { hasText: 'Озвучка' }) })
  await expect(voices.locator('button.on')).toHaveCount(1)
  const voiceTitles = await voices.locator('button').allTextContents()
  await page.screenshot({ path: 'test-results/sources-merged.png' })
  console.log('озвучки серии:', voiceTitles.join(' | '))

  // Выбор площадки запомнился для тайтла.
  const prefs = await page.evaluate(async id => (await (await fetch(`/api/v1/titles/${id}`)).json()).prefs, MERGED)
  expect(prefs.source).toBe('animego_me')
})

test('тайтл только с AnimeGO.fr играет', async ({ page }) => {
  test.setTimeout(90000)
  await openDesktop(page, `/title/${ANIME_ONLY}/s1e3`)
  await expect(page.locator('h1')).toContainText('Принцесса-медуза')
  await page.waitForSelector('button.kp-center', { timeout: 30000 })
  await page.locator('button.kp-center').click()
  await playing(page)
  const poster = await page.evaluate(async id => (await fetch(`/api/v1/titles/${id}/art`)).status, ANIME_ONLY)
  expect(poster).toBe(200)
  // Кадры серий площадка отдаёт по запросу (у CVH): карточки получают
  // настоящие кадры, а не подписи «Эпизод N».
  await page.waitForFunction(() =>
    [...document.querySelectorAll('.epcard .shot img')].filter(i => i.complete && i.naturalWidth > 0).length >= 3,
  null, { timeout: 40000 })
  await page.locator('.epgrid').scrollIntoViewIfNeeded()
  await page.locator('.epgrid').screenshot({ path: 'test-results/sources-anime-only.png' })
})
