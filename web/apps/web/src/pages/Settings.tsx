import { useState } from 'react'
import { readSkipMode, SKIP_KEY, type SkipMode } from '@kino/player/ui'
import { canPlayExternal, hasSettings, saveSetting, setting } from '../native'
import {
  av1Claim, detectAv1, detectHevc, hevcClaim, maxQuality, QUALITIES, saveClaim, saveMaxQuality,
  type Claim,
} from '../quality'
import { isTV } from '../tv'

/**
 * Настройки.
 *
 * Живут в витрине, а не в оболочке: витрина одна, и настройка, сделанная на
 * телефоне, должна выглядеть так же, как на компьютере. То, что имеет смысл
 * только внутри приложения (выбор плеера), там и показывается.
 *
 * Хранилище разное: в приложении настройки лежат у оболочки, потому что их
 * должны видеть ещё и встроенный плеер со страницей без сети. В браузере —
 * обычный localStorage.
 */

const AUTONEXT_KEY = 'kp.autonext'
const TV_KEY = 'kino.tv'

const read = (key: string, fallback: boolean): boolean => {
  try {
    const v = localStorage.getItem(key)
    return v === null ? fallback : v !== 'off'
  } catch {
    return fallback
  }
}

export function Settings() {
  const [player, setPlayer] = useState(() => setting('player') || 'internal')
  const [stats, setStats] = useState(() => setting('stats') === '1')
  const [autoNext, setAutoNext] = useState(() => read(AUTONEXT_KEY, true))
  const [skipMode, setSkipMode] = useState<SkipMode>(readSkipMode)
  const [tv, setTv] = useState(() => {
    try { return localStorage.getItem(TV_KEY) === '1' } catch { return false }
  })

  const pickPlayer = (v: string) => { setPlayer(v); saveSetting('player', v) }
  const toggleStats = (v: boolean) => { setStats(v); saveSetting('stats', v ? '1' : '') }

  const toggleAutoNext = (v: boolean) => {
    setAutoNext(v)
    try { localStorage.setItem(AUTONEXT_KEY, v ? 'on' : 'off') } catch { /* приватный режим */ }
  }

  const pickSkipMode = (v: SkipMode) => {
    setSkipMode(v)
    try { localStorage.setItem(SKIP_KEY, v) } catch { /* приватный режим */ }
  }

  const toggleTv = (v: boolean) => {
    setTv(v)
    try {
      if (v) localStorage.setItem(TV_KEY, '1')
      else localStorage.removeItem(TV_KEY)
    } catch { /* приватный режим */ }
  }

  return (
    <>
      <h1>Настройки</h1>

      <h2 className="dlhead">Плеер</h2>
      <div className="setlist">
        {canPlayExternal() && (
          <label className="setrow">
            <span className="setmain">
              <span className="setname">Чем открывать</span>
              <span className="sethint">
                Внешний плеер выбирается системой при каждом запуске — MX Player,
                VLC или любой другой. Он не сообщает, докуда вы досмотрели,
                поэтому прогресс по скачанному сохранит только встроенный.
              </span>
            </span>
            <select value={player} onChange={e => pickPlayer(e.target.value)}>
              <option value="internal">Встроенный</option>
              <option value="external">Внешний</option>
            </select>
          </label>
        )}

        <label className="setrow">
          <span className="setmain">
            <span className="setname">Показывать статистику</span>
            <span className="sethint">
              Кодек, разрешение, скорость канала и буфер в углу кадра. Гаснет
              вместе с панелью управления.
            </span>
          </span>
          <input type="checkbox" checked={stats} onChange={e => toggleStats(e.target.checked)} />
        </label>

        <label className="setrow">
          <span className="setmain">
            <span className="setname">Автозапуск следующей серии</span>
            <span className="sethint">По окончании серии следующая начнётся сама через несколько секунд.</span>
          </span>
          <input type="checkbox" checked={autoNext} onChange={e => toggleAutoNext(e.target.checked)} />
        </label>

        <label className="setrow">
          <span className="setmain">
            <span className="setname">Заставки и титры</span>
            <span className="sethint">
              Опенинг, пересказ и титры, если площадка или AniSkip их разметили.
              Титры сами никогда не пропускаются: на них кнопка «Следующая
              серия», а без нажатия серия переключится только по окончании.
            </span>
          </span>
          <select value={skipMode} onChange={e => pickSkipMode(e.target.value as SkipMode)}>
            <option value="button">Кнопкой</option>
            <option value="auto">Пропускать сами</option>
            <option value="off">Не предлагать</option>
          </select>
        </label>
      </div>

      <Quality />

      <h2 className="dlhead">Экран</h2>
      <div className="setlist">
        <label className="setrow">
          <span className="setmain">
            <span className="setname">Режим телевизора</span>
            <span className="sethint">
              Крупнее шрифт и элементы, управление стрелками пульта. Включается
              сам на телевизорах; здесь — чтобы проверить или поправить.
              Применится после перезагрузки страницы.
            </span>
          </span>
          <input type="checkbox" checked={tv} onChange={e => toggleTv(e.target.checked)} />
        </label>
      </div>

      {!hasSettings() && (
        <p className="muted" style={{ fontSize: 13, marginTop: 18 }}>
          В приложении здесь появится ещё выбор плеера. Настройки браузера
          и приложения хранятся отдельно.
        </p>
      )}

      <About />
    </>
  )
}

const QUALITY_NAMES: Record<number, string> = {
  1080: '1080p — Full HD',
  1440: '1440p — 2K',
  2160: '2160p — 4K',
}

/**
 * Что устройство заявляет площадкам — как «Поддержка 4K / HEVC» в
 * настройках устройства у kinopub. Своё у каждого устройства и браузера.
 */
function Quality() {
  const [maxq, setMaxq] = useState(maxQuality)
  const [hevc, setHevc] = useState<Claim>(hevcClaim)
  const [av1, setAv1] = useState<Claim>(av1Claim)
  const [hevcAuto] = useState(detectHevc)
  const [av1Auto] = useState(detectAv1)

  const pickMaxq = (n: number) => { setMaxq(n); saveMaxQuality(n) }
  const pickHevc = (v: Claim) => { setHevc(v); saveClaim('hevc', v) }
  const pickAv1 = (v: Claim) => { setAv1(v); saveClaim('av1', v) }
  const auto = (yes: boolean) => `Сам определять — ${yes ? 'есть' : 'нет'}`

  return (
    <>
      <h2 className="dlhead">Качество</h2>
      <div className="setlist">
        <label className="setrow">
          <span className="setmain">
            <span className="setname">Наибольшее качество</span>
            <span className="sethint">
              Выше 1080p площадки отдают не везде и в других кодеках: у kinopub
              это HEVC, у ЛордФильма — только AV1. Без нужного кодека придёт
              лучшее из того, что устройство умеет. На слабом телевизоре 4K
              может подтормаживать.
            </span>
          </span>
          <select value={maxq} onChange={e => pickMaxq(Number(e.target.value))}>
            {QUALITIES.map(q => <option key={q} value={q}>{QUALITY_NAMES[q]}</option>)}
          </select>
        </label>

        <label className="setrow">
          <span className="setmain">
            <span className="setname">Поддержка HEVC (H.265)</span>
            <span className="sethint">
              Нужна для 4K у kinopub. Если заявить кодек, которого устройство
              не умеет, будет звук без картинки.
            </span>
          </span>
          <select value={hevc} onChange={e => pickHevc(e.target.value as Claim)}>
            <option value="">{auto(hevcAuto)}</option>
            <option value="on">Заявлять</option>
            <option value="off">Не заявлять</option>
          </select>
        </label>

        <label className="setrow">
          <span className="setmain">
            <span className="setname">Поддержка AV1</span>
            <span className="sethint">
              Нужна для 1440p и 4K у ЛордФильма. Декодировать AV1 умеют не все
              устройства, а без аппаратной поддержки 4K идёт рывками.
            </span>
          </span>
          <select value={av1} onChange={e => pickAv1(e.target.value as Claim)}>
            <option value="">{auto(av1Auto)}</option>
            <option value="on">Заявлять</option>
            <option value="off">Не заявлять</option>
          </select>
        </label>
      </div>
    </>
  )
}

/**
 * Чем и на чём открыта витрина.
 *
 * Нужно ровно для разбора жалоб с телевизора: браузеры там разные и часто
 * старые, а посмотреть в них консоль нельзя — ни отладчика, ни адресной
 * строки. Строчка на экране заменяет и то, и другое: по ней сразу видно
 * и плотность экрана (от неё зависит масштаб раскладки), и чего движку
 * не хватает из того, на чём держится вёрстка.
 */
function About() {
  const dpr = window.devicePixelRatio || 1
  const zoom = document.documentElement.style.zoom || '1'
  // Chrome ищем первым намеренно: в строке WebView перед ним стоит
  // «Version/4.0», и по общему шаблону движком оказывалась именно она.
  const ua = navigator.userAgent
  const engine = / (Chrome|CriOS)\/([\d.]+)/.exec(ua) ?? / (Version)\/([\d.]+)/.exec(ua)
  const supports = (prop: string, value: string) =>
    (window.CSS?.supports?.(prop, value) ?? false) ? '' : ` нет ${prop}`
  // Селектор проверяется отдельной формой: движок, который её не знает,
  // не знает и :focus-visible — ответ «нет» верен в обе стороны.
  const selector = (sel: string) => {
    try { return window.CSS?.supports?.(`selector(${sel})`) ? '' : ` нет ${sel}` } catch { return ` нет ${sel}` }
  }
  const gaps = [
    supports('inset', '0'),
    supports('aspect-ratio', '1'),
    supports('color', 'color-mix(in srgb, red 50%, transparent)'),
    selector(':focus-visible'),
  ].join('')

  return (
    <>
      <h2 className="dlhead">Об устройстве</h2>
      <p className="muted small" style={{ lineHeight: 1.7 }}>
        Экран {screen.width}×{screen.height}, страница {innerWidth}×{innerHeight},
        плотность {dpr}, масштаб {zoom}
        <br />
        Движок {engine ? engine[1] + ' ' + engine[2] : 'неизвестен'}
        {gaps ? <>, <b>не хватает:</b>{gaps}</> : ', всё нужное поддерживает'}
        <br />
        Режим телевизора {isTV() ? 'включён' : 'выключен'}
      </p>
    </>
  )
}
