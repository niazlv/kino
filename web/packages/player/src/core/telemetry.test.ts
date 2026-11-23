import { describe, expect, it } from 'vitest'
import { SessionRecorder } from './telemetry'

// Метрики плеера тестируются без DOM, без сети и без hls.js — ядро
// отделено от движков.
describe('SessionRecorder', () => {
  const clock = (t: { v: number }) => () => t.v

  it('меряет время до первого кадра', () => {
    const t = { v: 1000 }
    const r = new SessionRecorder('hlsjs', clock(t))
    r.start()
    t.v = 1420
    r.firstFrame()
    t.v = 9000
    r.firstFrame() // повторный вызов не сдвигает ttff
    expect(r.finish('completed').ttffMs).toBe(420)
  })

  it('суммирует ребуферизации и не считает незакрытую дважды', () => {
    const t = { v: 0 }
    const r = new SessionRecorder('native_hls', clock(t))
    r.start()
    r.rebufferStart()
    r.rebufferStart() // уже в ребуфере — не новый эпизод
    t.v = 800
    expect(r.rebufferEnd()).toBe(800)
    const s = r.finish('abandoned')
    expect(s.rebufferCount).toBe(1)
    expect(s.rebufferMsTotal).toBe(800)
    expect(s.impl).toBe('native_hls')
  })

  it('запоминает максимальную высоту по переключениям', () => {
    const r = new SessionRecorder('hlsjs', () => 0)
    r.start()
    r.levelSwitch(480)
    r.levelSwitch(1080)
    r.levelSwitch(720)
    const s = r.finish('completed')
    expect(s.maxHeight).toBe(1080)
    expect(s.bitrateSwitches).toBe(3)
  })
})
