/**
 * Демо-стенд плеера. Никакого бэкенда, никакого @kino/*, никакой авторизации.
 *
 * Это не витрина, а рабочий инструмент: плеер разрабатывается и чинится без
 * поднятия платформы. И одновременно ежедневная проверка отделяемости —
 * если сюда потребуется что-то из приложения, значит связь завелась.
 */
import { KinoPlayer } from '../src'

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T

const video = $<HTMLVideoElement>('v')
let player: KinoPlayer | null = null
let rebuffers = 0

$('load').addEventListener('click', async () => {
  await player?.unload('abandoned')
  rebuffers = 0

  player = new KinoPlayer(video, {
    progressIntervalMs: 1000,
    // Прогресс "уезжает" в консоль: плеер не знает эндпоинтов.
    progress: {
      report: (pos, dur) => { $('pos').textContent = `${fmt(pos)} / ${fmt(dur)}` },
      flush: () => console.log('progress flushed'),
    },
    telemetry: { session: (s) => console.log('session stats', s) },
  })

  player.on('ready', (e) => { $('impl').textContent = `engine: ${e.impl}` })
  player.on('rebufferstart', () => { $('reb').textContent = `rebuffer: ${++rebuffers}` })
  player.on('tracks', (t) => {
    $('tracks').innerHTML = ''
    for (const a of t.audio) {
      const b = document.createElement('button')
      b.textContent = a.label
      b.onclick = () => player?.selectAudio(a.id)
      $('tracks').append(b)
    }
  })
  player.on('error', (e) => console.error('player error', e))

  await player.load({ url: $<HTMLInputElement>('url').value })
  await player.play()
})

const fmt = (ms: number) => {
  const s = Math.floor(ms / 1000)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}
