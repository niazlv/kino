import { describe, expect, it } from 'vitest'
import { activeSkip, pickSkips, type SkipSegment } from './skips'

describe('pickSkips', () => {
  // Kodik у «Ван-Пис»: голые интервалы, длина серии 24:40.
  const kodik: SkipSegment[] = [
    { kind: 'skip', start: 0, end: 111 },
    { kind: 'skip', start: 1373, end: 1477 },
  ]

  it('безымянный отрезок в начале — заставка, в конце — титры', () => {
    expect(pickSkips(kodik, 1480)).toEqual([
      { kind: 'intro', start: 0, end: 111 },
      { kind: 'outro', start: 1373, end: 1480 },
    ])
  })

  it('без длины потока не показывает ничего', () => {
    expect(pickSkips(kodik, 0)).toEqual([])
    expect(pickSkips(kodik, NaN)).toEqual([])
  })

  // «Фрирен», 2-я серия: AniSkip размечал релиз длиной 1566,6 с (опенинг)
  // и 1561 с (эндинг), поток Анилибрии — 1560 с.
  const aniskip: SkipSegment[] = [
    { kind: 'intro', start: 6.576, end: 96.576, forDuration: 1566.565 },
    { kind: 'outro', start: 1460, end: 1550, forDuration: 1561 },
  ]

  it('чужая разметка — только при совпавшей длине', () => {
    expect(pickSkips(aniskip, 1560)).toEqual([{ kind: 'outro', start: 1460, end: 1550 }])
    expect(pickSkips(aniskip, 1566)).toEqual([{ kind: 'intro', start: 6.576, end: 96.576 }])
    expect(pickSkips(aniskip, 1600)).toEqual([])
  })

  it('своя разметка важнее чужой того же вида', () => {
    // Длина 1566: опенинг AniSkip подошёл бы, но своя заставка уже есть.
    const got = pickSkips([
      { kind: 'skip', start: 1, end: 89 },
      ...aniskip,
    ], 1566)
    expect(got).toEqual([{ kind: 'intro', start: 1, end: 89 }])
  })

  it('налезающие отрезки не показываются оба', () => {
    expect(pickSkips([
      { kind: 'recap', start: 0, end: 60 },
      { kind: 'intro', start: 50, end: 140 },
    ], 1400)).toEqual([{ kind: 'recap', start: 0, end: 60 }])
  })

  it('мусор отбрасывается', () => {
    expect(pickSkips([
      { kind: 'intro', start: 10, end: 5 },
      { kind: 'intro', start: 10, end: 11 },
      { kind: 'outro', start: 2000, end: 2100 },
    ], 1400)).toEqual([])
  })
})

describe('activeSkip', () => {
  const skips = [{ kind: 'intro' as const, start: 10, end: 100 }]
  it('внутри отрезка и не у самого его конца', () => {
    expect(activeSkip(skips, 9)).toBeNull()
    expect(activeSkip(skips, 9.9)).toEqual(skips[0])
    expect(activeSkip(skips, 50)).toEqual(skips[0])
    expect(activeSkip(skips, 99.5)).toBeNull()
  })
})
