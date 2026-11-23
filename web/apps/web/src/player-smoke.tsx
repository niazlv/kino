/**
 * Дымовая страница панели плеера: отдельный вход vite (`player-smoke.html`),
 * бэкенда не требует — играет публичный тестовый поток. В прод-сборку не
 * попадает (вход только index.html), в dev открывается по адресу
 * /player-smoke.html, с ?touch — панель считает основной ввод пальцем,
 * с ?notice=текст — строка хозяина поверх кадра («переключаю…»).
 */
import { useEffect, useState } from 'react'
import type { ChoiceGroup } from '@kino/player/ui'
import type { SkipSegment } from '@kino/player'
import { createRoot } from 'react-dom/client'
import { Controls, type NextItem } from '@kino/player/ui'
import { usePlayer } from '@kino/player/react'

// Тач-режим: ?touch / ?notouch — явно, иначе по умолчанию на узком окне
// (мобильная раскладка плеера включается по coarse-указателю).
const params = new URLSearchParams(location.search)
const smokeTouch = params.has('touch')
  || (!params.has('notouch') && typeof matchMedia === 'function' && matchMedia('(max-width: 640px)').matches)
if (smokeTouch) {
  const orig = window.matchMedia.bind(window)
  window.matchMedia = (q: string) =>
    q.includes('pointer: coarse') ? { matches: true, media: q } as MediaQueryList : orig(q)
}

// ?fakefull — полный экран понарошку: встроенный браузер среды проверки
// настоящий не разрешает, а раскладку во весь экран посмотреть нужно.
// Плеер растягивается на окно и получает то же событие, что и от браузера.
if (params.has('fakefull')) {
  let fsEl: Element | null = null
  Object.defineProperty(document, 'fullscreenElement', { get: () => fsEl, configurable: true })
  const change = () => document.dispatchEvent(new Event('fullscreenchange'))
  Element.prototype.requestFullscreen = function (this: Element) {
    fsEl = this; this.setAttribute('data-fakefull', ''); change(); return Promise.resolve()
  }
  document.exitFullscreen = () => {
    fsEl?.removeAttribute('data-fakefull'); fsEl = null; change(); return Promise.resolve()
  }
}

const STREAM = 'https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8'
// Разметка пропуска на тестовом потоке (10:34): голые «от-до», как у
// Kodik, — заставка в начале, титры у конца. ?tv — панель телевизора.
// ?tail — титры кончаются раньше файла (дальше «анонс»): кнопка должна
// перематывать к нему, а не уводить к следующей серии.
const SEGMENTS: SkipSegment[] = [
  { kind: 'skip', start: 0, end: 40 },
  { kind: 'skip', start: 570, end: params.has('tail') ? 600 : 634 },
]
const smokeTV = params.has('tv')
// Звук по умолчанию выключен: страницу гоняют проверки, и играть вслух
// у человека в фоне ей незачем. ?sound — со звуком.
const smokeMuted = !params.has('sound')
const next: NextItem = { label: 'Серия 2 (демо)', onSelect: () => console.log('next!') }
const prev: NextItem = { label: 'Серия 0 (демо)', onSelect: () => console.log('prev!') }

function Smoke() {
  const [go, setGo] = useState(false)
  const [src, setSrc] = useState('kinopub')
  // Группа хозяина, как «Площадка» на странице тайтла: видно её место в меню.
  const choices: ChoiceGroup[] = [{
    title: 'Площадка', selected: src, onSelect: setSrc,
    items: [{ id: 'kinopub', label: 'kinopub' }, { id: 'animego', label: 'AnimeGO.me' }],
  }]
  const { videoRef, player, audio, qualities, text } = usePlayer(go ? {
    url: STREAM, type: 'hls', autoplay: true, title: 'Дымовой поток',
  } : null, {})
  useEffect(() => {
    const v = videoRef.current
    if (v && smokeMuted) v.muted = true
  })
  return (
    <main style={{ maxWidth: 900, margin: '0 auto', padding: 24 }}>
      <h1>Панель плеера — дым</h1>
      <p style={{ opacity: .7 }}>
        Раскладка: {smokeTouch ? 'палец (во весь экран — кинопаб)' : 'мышь'} —{' '}
        <a href={smokeTouch ? '?notouch' : '?touch'} style={{ color: '#8cf' }}>
          переключить на {smokeTouch ? 'мышь' : 'телефон'}
        </a>
      </p>
      {!go && <button onClick={() => setGo(true)}>Загрузить поток</button>}
      <div style={{ aspectRatio: '16/9', position: 'relative', background: '#000' }}>
        {go && <Controls videoRef={videoRef} player={player} audio={audio} qualities={qualities}
                         subtitles={text} title="Дымовой поток · s1e2" next={next} prev={prev}
                         stats choices={choices} tv={smokeTV} segments={SEGMENTS}
                         notice={params.get('notice') ?? undefined}
                         onCreditsNext={() => console.log('credits next!')} />}
      </div>
    </main>
  )
}

createRoot(document.getElementById('root')!).render(<Smoke />)
