import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { HoverPreview, type PreviewBackend, type PreviewFrame } from './preview'
import type { PlayerSource } from './types'

class FakeBackend implements PreviewBackend {
  seeks: number[] = []
  detached = 0
  private frameCb: ((f: PreviewFrame) => void) | null = null
  private failCb: (() => void) | null = null

  onFrame(cb: (f: PreviewFrame) => void) { this.frameCb = cb }
  onFail(cb: () => void) { this.failCb = cb }
  seek(t: number) { this.seeks.push(t) }
  detach() { this.detached++ }

  emit(t: number) { this.frameCb?.({ time: t, canvas: {} as HTMLCanvasElement }) }
  fail() { this.failCb?.() }
}

function setup() {
  const backends: FakeBackend[] = []
  const holder = { source: { url: 'hls://a' } as PlayerSource }
  const alive: boolean[] = []
  const frames: number[] = []
  const p = new HoverPreview({
    getSource: () => holder.source as unknown as PlayerSource,
    createBackend: () => { const b = new FakeBackend(); backends.push(b); return b },
    createVideo: () => ({}) as HTMLVideoElement,
  })
  p.onAlive(ok => alive.push(ok))
  p.onFrame(f => frames.push(f.time))
  return { p, backends, holder, alive, frames }
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('превью кадра под курсором', () => {
  it('заводит второй плеер по первому наведению и просит кадр после паузы', () => {
    const { p, backends } = setup()
    p.request(120)
    expect(backends).toHaveLength(1)
    expect(backends[0]!.seeks).toEqual([])
    vi.advanceTimersByTime(180)
    expect(backends[0]!.seeks).toEqual([120])
    // Пока курсор ходит, перемотки схлопываются в последнюю.
    p.request(125)
    p.request(130)
    vi.advanceTimersByTime(180)
    expect(backends[0]!.seeks).toEqual([120, 130])
  })

  it('cancel() отменяет ещё не отправленную перемотку', () => {
    const { p, backends } = setup()
    p.request(100)
    p.cancel()
    vi.advanceTimersByTime(500)
    expect(backends[0]!.seeks).toEqual([])
  })

  it('кадр хвоста старой перемотки до подписчиков не доходит', () => {
    const { p, backends, frames } = setup()
    p.request(100)
    vi.advanceTimersByTime(180)
    backends[0]!.emit(100)
    expect(frames).toEqual([100])
    // Ушли далеко: долетевший по дороге кадр со старого места — мусор.
    p.request(600)
    vi.advanceTimersByTime(180)
    backends[0]!.emit(300)
    expect(frames).toEqual([100])
    backends[0]!.emit(600)
    expect(frames).toEqual([100, 600])
  })

  it('после 15 секунд без наведений освобождает плеер и сеть', () => {
    const { p, backends } = setup()
    p.request(120)
    vi.advanceTimersByTime(15_000)
    expect(backends[0]!.detached).toBe(1)
    // Следующее наведение заводит новый.
    p.request(140)
    expect(backends).toHaveLength(2)
    vi.advanceTimersByTime(180)
    expect(backends[1]!.seeks).toEqual([140])
  })

  it('смену источника переживает пересозданием под новый адрес', () => {
    const { p, backends, holder } = setup()
    p.request(50)
    vi.advanceTimersByTime(180)
    holder.source = { url: 'hls://b' } as PlayerSource
    p.request(60)
    expect(backends[0]!.detached).toBe(1)
    expect(backends).toHaveLength(2)
    vi.advanceTimersByTime(180)
    expect(backends[1]!.seeks).toEqual([60])
  })

  it('отказавший поток больше не пытается, новый источник — снова пытается', () => {
    const { p, backends, holder, alive } = setup()
    p.request(100)
    backends[0]!.fail()
    expect(alive).toEqual([false])
    // Отложенная перемотка мёртвому плееру не отправляется.
    vi.advanceTimersByTime(180)
    expect(backends[0]!.seeks).toEqual([])
    p.request(120)
    expect(backends).toHaveLength(1)
    holder.source = { url: 'hls://b' } as PlayerSource
    p.request(60)
    expect(alive).toEqual([false, true])
    expect(backends).toHaveLength(2)
  })

  it('источник, которому превью не по зубам, помечается сразу', () => {
    const backends: FakeBackend[] = []
    const alive: boolean[] = []
    const p = new HoverPreview({
      getSource: () => ({ url: 'hls://a' }),
      createBackend: () => null,
      createVideo: () => ({}) as HTMLVideoElement,
    })
    p.onAlive(ok => alive.push(ok))
    p.request(10)
    expect(backends).toHaveLength(0)
    expect(alive).toEqual([false])
  })

  it('stop() гасит всё: ни таймеров, ни плеера', () => {
    const { p, backends } = setup()
    p.request(100)
    p.stop()
    vi.advanceTimersByTime(30_000)
    expect(backends[0]!.detached).toBe(1)
    expect(backends[0]!.seeks).toEqual([])
  })
})
