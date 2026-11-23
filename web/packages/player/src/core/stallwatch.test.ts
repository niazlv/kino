import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { StallWatch, type StallTrip } from './stallwatch'

describe('StallWatch', () => {
  let trips: StallTrip[]
  let w: StallWatch
  beforeEach(() => {
    vi.useFakeTimers()
    trips = []
    w = new StallWatch((t) => trips.push(t))
  })
  afterEach(() => { w.stop(); vi.useRealTimers() })

  const wait = (ms: number, seeking = false) => {
    w.waiting(seeking)
    vi.advanceTimersByTime(ms)
    w.playing()
  }

  it('до первого кадра не считает: это старт, а не зависание', () => {
    w.waiting(false)
    vi.advanceTimersByTime(20_000)
    expect(trips).toEqual([])
  })

  // Манифест разобран, но ни кадра: поток тянется тоньше, чем играется,
  // и без сторожа плеер крутит кольцо без конца.
  it('старт без первого кадра дольше срока — сигнал о незапуске', () => {
    w.waiting(false)
    vi.advanceTimersByTime(44_999)
    expect(trips).toEqual([])
    vi.advanceTimersByTime(1)
    expect(trips).toEqual([{ reason: 'startup', waitedMs: 45_000, recentMs: 45_000 }])
  })

  it('первый кадр в срок снимает сторож старта', () => {
    w.waiting(false)
    vi.advanceTimersByTime(44_999)
    w.playing()
    vi.advanceTimersByTime(60_000)
    expect(trips).toEqual([])
  })

  it('сторож старта заводится один раз, лишние waiting его не сдвигают', () => {
    w.waiting(false)
    vi.advanceTimersByTime(30_000)
    w.waiting(false)
    vi.advanceTimersByTime(15_000)
    expect(trips).toEqual([{ reason: 'startup', waitedMs: 45_000, recentMs: 45_000 }])
  })

  it('пауза в ожидании первого кадра снимает сторож старта, возобновление заводит снова', () => {
    w.waiting(false)
    vi.advanceTimersByTime(30_000)
    w.paused()
    vi.advanceTimersByTime(60_000)
    expect(trips).toEqual([])
    w.waiting(false)
    vi.advanceTimersByTime(45_000)
    expect(trips).toEqual([{ reason: 'startup', waitedMs: 45_000, recentMs: 45_000 }])
  })

  it('одно долгое ожидание — сигнал, не дожидаясь его конца', () => {
    w.playing()
    w.waiting(false)
    vi.advanceTimersByTime(9_999)
    expect(trips).toEqual([])
    vi.advanceTimersByTime(1)
    expect(trips).toEqual([{ reason: 'long', waitedMs: 10_000, recentMs: 10_000 }])
  })

  // Живой замер площадки за VPN: три остановки на 26 секунд за две минуты.
  it('короткие подряд — сигнал по сумме за окно', () => {
    w.playing()
    wait(8_000)
    vi.advanceTimersByTime(40_000)
    wait(5_000)
    expect(trips).toEqual([])
    vi.advanceTimersByTime(40_000)
    wait(3_000)
    expect(trips).toEqual([{ reason: 'often', waitedMs: 3_000, recentMs: 16_000 }])
  })

  it('старые ожидания выпадают из окна', () => {
    w.playing()
    wait(8_000)
    vi.advanceTimersByTime(200_000)
    wait(8_000)
    expect(trips).toEqual([])
  })

  it('короткое ожидание после перемотки в сумму не идёт', () => {
    w.playing()
    wait(8_000, true)
    wait(8_000, true)
    expect(trips).toEqual([])
    w.waiting(true)
    vi.advanceTimersByTime(10_000)
    expect(trips.map((t) => t.reason)).toEqual(['long'])
  })

  it('пауза кончает ожидание', () => {
    w.playing()
    w.waiting(false)
    vi.advanceTimersByTime(3_000)
    w.paused()
    vi.advanceTimersByTime(20_000)
    expect(trips).toEqual([])
  })

  it('после сигнала счёт с нуля', () => {
    w.playing()
    wait(9_000)
    wait(6_000)
    expect(trips.length).toBe(1)
    wait(2_000)
    expect(trips.length).toBe(1)
  })
})
