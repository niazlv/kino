/**
 * Проверка на настоящем телевизоре.
 *
 * Нажатия идут пультом через adb, состояние читается прямо из WebView
 * приложения по отладочному протоколу. Это единственный способ поймать то,
 * чего не видно в браузере на компьютере: у WebView телевизора есть своя
 * навигация стрелками, свой полный экран и своя скорость отрисовки.
 *
 * Перед запуском: поднят стенд, приложение открыто на устройстве, и порт
 * проброшен (см. README).
 */
import { open, key, WHO, sleep, screenshot } from './tvbox.mjs'

// Начинаем с чистой загрузки. Витрина в приложении кешируется — без этого
// проверялась бы вчерашняя сборка; да и состояние экрана (раскрытое меню,
// развёрнутые звёзды) живёт до перезагрузки и путало бы следующий прогон.
{
  const было = await open()
  const origin = await было.$(`location.origin`)
  await было.$(`(location.href = '${'$'}{location.origin}/?n=' + Date.now(), 1)`.replace('${location.origin}', origin))
  было.close()
  await sleep(4000)
}

const app = await open()
const п = (...a) => console.log(...a)
let сбоев = 0
const проверить = (что, ок) => { if (!ок) сбоев++; п(`  ${ок ? '✓' : '✗'} ${что}`) }
const ждать = async (expr, что, мс = 30000) => {
  for (let t = 0; t < мс; t += 400) { if (await app.$(expr)) return true; await sleep(400) }
  throw new Error('не дождались: ' + что)
}
const перейти = async hash => { await app.$(`(location.hash = '${hash}', 1)`); await sleep(2000) }

п('\n═══ среда ═══')
const среда = await app.$(`({
  адрес: location.href, классы: document.documentElement.className,
  масштаб: document.documentElement.style.zoom || '(нет)', плотность: devicePixelRatio,
  окно: innerWidth + '×' + innerHeight,
  движок: (/ Chrome\\/([\\d.]+)/.exec(navigator.userAgent) || [])[1] || '?',
  меткаТВ: / kino-tv/.test(navigator.userAgent),
  умеет: ['inset:0', 'aspect-ratio:1'].filter(p => CSS.supports(p.split(':')[0], p.split(':')[1])).length
    + (CSS.supports('color', 'color-mix(in srgb, red 50%, transparent)') ? 1 : 0),
})`)
п(' ', JSON.stringify(среда))
проверить('режим телевизора включён', среда.классы.includes('tv'))
проверить('движок понимает inset, aspect-ratio и color-mix', среда.умеет === 3)

п('\n═══ меню ═══')
await перейти('#/')
const коротко = await app.$(`[...document.querySelectorAll('.side .nav')].map(n => n.textContent.trim())`)
проверить(`меню короткое (${коротко.length} пунктов)`, коротко.length <= 6 && коротко.includes('Ещё'))

await app.$(`(document.querySelector('.side .nav').focus(), 1)`)
await key('DPAD_DOWN', 8)
проверить('стрелка вниз не выбрасывает из меню', await app.$(`!!document.activeElement.closest('.side')`))
await ждать(`document.activeElement.textContent.trim() === 'Ещё'`, 'дошли до «Ещё»', 3000).catch(() => {})
await key('DPAD_CENTER')
await sleep(800)
проверить('после «Ещё» фокус принял раскрывшийся список',
  (await app.$(WHO)).includes('Буду смотреть'))

п('\n═══ поиск ═══')
await перейти('#/')
проверить('в шапке нет видимого поля ввода',
  0 === await app.$(`[...document.querySelectorAll('.topbar input')].filter(e => e.offsetParent !== null).length`))
await перейти('#/search/' + encodeURIComponent('масяня'))
проверить('с готовым запросом вместо поля — кнопка',
  0 === await app.$(`document.querySelectorAll('.tvsearch input').length`)
  && 1 === await app.$(`document.querySelectorAll('button.tvsearch').length`))

п('\n═══ настройки ═══')
await перейти('#/settings')
await app.$(`(document.querySelector('.setrow input').focus(), 1)`)
const строки = []
for (let i = 0; i < 3; i++) {
  строки.push(await app.$(`(() => { const r = document.activeElement.closest('.setrow'); return r ? r.querySelector('.setname').textContent : 'вне строк' })()`))
  await key('DPAD_DOWN')
}
проверить(`фокус проходит строки: ${строки.join(', ')}`, строки.includes('Режим телевизора'))
проверить('кольцо фокуса видно',
  'none' !== await app.$(`getComputedStyle(document.activeElement).outlineStyle`))
п(' ', (await app.$(`[...document.querySelectorAll('.content p.muted')].pop().innerText`)).replace(/\n/g, ' | '))

п('\n═══ плеер ═══')
await перейти('#/title/237')
await ждать(`!!document.querySelector('button.kp-center')`, 'кнопка «играть»')
проверить('кнопка «играть» накрывает кадр', await app.$(`(() => {
  const a = document.querySelector('.kp').getBoundingClientRect()
  const b = document.querySelector('button.kp-center').getBoundingClientRect()
  return Math.abs(a.width - b.width) < 1 && Math.abs(a.height - b.height) < 1
})()`))
await ждать(`document.activeElement && document.activeElement.classList.contains('kp-center')`, 'фокус на кнопке')
await key('DPAD_CENTER')
await ждать(`(() => { const v = document.querySelector('.kp video'); return v && !v.paused && v.currentTime > 0 })()`, 'воспроизведение', 40000)
проверить('старт разворачивает во весь экран', await app.$(`!!document.fullscreenElement`))
await ждать(`document.querySelector('.kp').classList.contains('idle')`, 'панель уснула')
const до = await app.$(`document.querySelector('.kp video').currentTime`)
await key('DPAD_RIGHT')
await sleep(800)
const после = await app.$(`document.querySelector('.kp video').currentTime`)
проверить(`вправо перематывает (${Math.round(до)}с → ${Math.round(после)}с)`, после - до >= 8)
await ждать(`document.querySelector('.kp').classList.contains('idle')`, 'панель уснула')
await key('DPAD_DOWN')
await sleep(500)
проверить('вниз открывает панель и ведёт на полосу времени',
  await app.$(`!document.querySelector('.kp').classList.contains('idle')
    && document.activeElement.classList.contains('kp-seek')`))
await ждать(`document.querySelector('.kp').classList.contains('idle')`, 'панель уснула')
await key('DPAD_CENTER')
await sleep(700)
проверить('OK ставит паузу', await app.$(`document.querySelector('.kp video').paused`))
await key('BACK')
await sleep(800)
проверить('«назад» выходит из полного экрана', !(await app.$(`!!document.fullscreenElement`)))

п('\n═══ полный экран через смену серии ═══')
// Серия из списка разворачивается в том же нажатии, до тикета: позже
// разрешения уже не будет.
await app.$(`(document.querySelectorAll('.epgrid .epcard')[1].focus(), 1)`)
await key('DPAD_CENTER')
await sleep(400)
проверить('серия из списка разворачивается сразу', await app.$(`!!document.fullscreenElement`))
await ждать(`(() => { const v = document.querySelector('.kp video'); return v && !v.paused && v.currentTime > 0 })()`,
  'воспроизведение второй серии', 40000)
проверить('и остаётся развёрнутой, когда пошло видео', await app.$(`!!document.fullscreenElement`))
// Следующая — без нажатия, как это делает обратный отсчёт в конце серии.
// Оценка здесь идёт по протоколу без жеста, развернуться заново нельзя.
await app.$(`(window.__fs = [], document.addEventListener('fullscreenchange',
  () => window.__fs.push(document.fullscreenElement ? 'вход' : 'выход')), 1)`)
await app.$(`(document.querySelector('.kp-btn[aria-label="Следующая серия"]').click(), 1)`)
await ждать(`(() => { const v = document.querySelector('.kp video'); return v && !v.paused && v.currentTime > 0 })()`,
  'воспроизведение третьей серии', 40000)
const смена = await app.$(`({ развёрнуто: !!document.fullscreenElement, события: window.__fs })`)
проверить(`автопереход не роняет полный экран (${JSON.stringify(смена.события)})`,
  смена.развёрнуто && смена.события.length === 0)
await key('BACK')
await sleep(800)
проверить('«назад» выходит и отсюда', !(await app.$(`!!document.fullscreenElement`)))

п('\n═══ плавность (каталог, 14 нажатий) ═══')
await перейти('#/browse//')
await ждать(`document.querySelectorAll('.tilelink').length > 20`, 'сетка каталога')
await app.$(`(document.querySelector('.tilelink').focus(), 1)`)
await app.$(`(() => { window.__f = []; let n = 0, last = performance.now()
  const tick = t => { window.__f.push(t - last); last = t; if (++n < 400) requestAnimationFrame(tick) }
  requestAnimationFrame(tick); return 1 })()`)
for (let i = 0; i < 14; i++) await key(i % 2 ? 'DPAD_RIGHT' : 'DPAD_DOWN')
await sleep(600)
const кадры = await app.$(`(() => { const f = window.__f.slice(2)
  return { среднее: +(f.reduce((s, x) => s + x, 0) / f.length).toFixed(1),
           худший: +Math.max(...f).toFixed(1), рывков: f.filter(x => x > 34).length } })()`)
п(' ', JSON.stringify(кадры))
// Порог не «идеально», а «не как было»: на эмуляторе с программной
// отрисовкой один тяжёлый кадр случается и без нашей вины. Ловим ту беду,
// с которой начинали, — кадры по 200 мс и рывки пачками.
проверить('рывков почти нет', кадры.рывков <= 2 && кадры.худший < 120)

await screenshot('/tmp/kino-tv-итог.png')
п(`\n${сбоев === 0 ? 'всё сошлось' : 'не сошлось: ' + сбоев}; снимок экрана — /tmp/kino-tv-итог.png\n`)
app.close()
process.exit(сбоев === 0 ? 0 : 1)
