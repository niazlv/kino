import { describe, expect, it, vi } from 'vitest'
import { KinoPlayer } from './player'
import type { PlaybackEngine } from './engine/engine'

class Video extends EventTarget {
  currentTime = 0
  duration = 1800
  seeking = false
  paused = true
  removeAttribute() {}
}

function setup() {
  const video = new Video()
  const report = vi.fn(), flush = vi.fn()
  const engine: PlaybackEngine = {
    impl: 'native_hls',
    async attach() { video.currentTime = 0; video.dispatchEvent(new Event('pause')) },
    detach() { video.currentTime = 0; video.dispatchEvent(new Event('pause')) },
    audioTracks: () => [], textTracks: () => [], qualities: () => [],
    currentAudioId: () => null, currentTextId: () => null,
    selectAudio() {}, selectText() {}, selectQuality() {}, on() {},
  }
  const player = new KinoPlayer(video as unknown as HTMLVideoElement, {
    engineFactory: () => engine, progress: { report, flush },
  })
  return { player, video, report, flush }
}

describe('точная позиция просмотра', () => {
  it('при выходе сохраняет текущий кадр до сброса видео', async () => {
    const { player, video, report, flush } = setup()
    await player.load({ url: 'test', startPositionMs: 120_000 })
    expect(report).not.toHaveBeenCalled() // пауза во время attach ещё не просмотр
    video.currentTime = 127.456
    player.flushProgress()
    expect(report).toHaveBeenLastCalledWith(127_456, 1_800_000)
    expect(flush).toHaveBeenCalledOnce()
    video.currentTime = 128.5
    await player.unload('abandoned')
    expect(report).toHaveBeenLastCalledWith(128_500, 1_800_000)
    expect(video.currentTime).toBe(0)
  })

  it('сохраняет паузу и завершённую перемотку, включая начало', async () => {
    const { player, video, report } = setup()
    await player.load({ url: 'test', startPositionMs: 900_000 })
    video.currentTime = 120
    video.seeking = true
    video.dispatchEvent(new Event('pause'))
    expect(report).not.toHaveBeenCalled()
    video.seeking = false
    video.dispatchEvent(new Event('seeked'))
    expect(report).toHaveBeenLastCalledWith(120_000, 1_800_000)
    video.currentTime = 0
    video.dispatchEvent(new Event('pause'))
    expect(report).toHaveBeenLastCalledWith(0, 1_800_000)
    await player.unload('abandoned')
  })

  it('пауза не перезаписывает другой просмотр каждым тиком таймера', async () => {
    vi.useFakeTimers()
    const { player, video, report } = setup()
    try {
      await player.load({ url: 'test', startPositionMs: 120_000 })
      vi.advanceTimersByTime(30_000)
      expect(report).not.toHaveBeenCalled()
      video.paused = false
      video.dispatchEvent(new Event('playing'))
      vi.advanceTimersByTime(15_000)
      expect(report).toHaveBeenCalledOnce()
    } finally { await player.unload('abandoned'); vi.useRealTimers() }
  })

  it('повторная загрузка не умножает обработчики паузы', async () => {
    const { player, video, report } = setup()
    await player.load({ url: 'first' })
    await player.load({ url: 'second', startPositionMs: 200_000 })
    report.mockClear()
    video.currentTime = 210
    video.dispatchEvent(new Event('pause'))
    expect(report).toHaveBeenCalledOnce()
    await player.unload('abandoned')
  })

  it('открытый и не включённый плеер не отчитывается точкой загрузки', async () => {
    const { player, video, report } = setup()
    // Просмотренная серия грузится с нуля: такой отчёт стёр бы галочку.
    await player.load({ url: 'test', startPositionMs: 0 })
    player.flushProgress()
    await player.load({ url: 'test', startPositionMs: 120_000 })
    video.dispatchEvent(new Event('seeked'))
    player.flushProgress()
    await player.unload('abandoned')
    expect(report).not.toHaveBeenCalled()
  })

  it('включённый плеер отчитывается и с места загрузки', async () => {
    const { player, video, report } = setup()
    await player.load({ url: 'test', startPositionMs: 0 })
    video.dispatchEvent(new Event('playing'))
    video.currentTime = 0.4
    video.dispatchEvent(new Event('pause'))
    expect(report).toHaveBeenLastCalledWith(400, 1_800_000)
    await player.unload('abandoned')
  })
})
