import type { PlayerImpl, SessionStats } from './types'

/**
 * Копит метрики качества за сессию и отдаёт ОДНУ запись в конце.
 *
 * Плеер намеренно ничего не отправляет сам: решение «одна строка на просмотр,
 * а не на запрос» принимается платформой, а не плеером. Двухчасовой фильм —
 * это ~2400 запросов сегментов; посессионная запись отвечает на те же вопросы
 * в тысячи раз дешевле.
 */
export class SessionRecorder {
  private startedAt = 0
  private firstFrameAt = 0
  // null, а не 0: ребуфер, начавшийся в нулевой момент времени (часы теста
  // идут с нуля), не должен молча теряться в сентинеле.
  private rebufferStartedAt: number | null = null

  private rebufferCount = 0
  private rebufferMsTotal = 0
  private bitrateSwitches = 0
  private maxHeight = 0

  constructor(private readonly impl: PlayerImpl, private readonly now = () => Date.now()) {}

  start(): void { this.startedAt = this.now() }

  firstFrame(): void {
    if (this.firstFrameAt === 0) this.firstFrameAt = this.now()
  }

  rebufferStart(): void {
    if (this.rebufferStartedAt !== null) return
    this.rebufferStartedAt = this.now()
    this.rebufferCount++
  }

  rebufferEnd(): number {
    if (this.rebufferStartedAt === null) return 0
    const d = this.now() - this.rebufferStartedAt
    this.rebufferMsTotal += d
    this.rebufferStartedAt = null
    return d
  }

  levelSwitch(height?: number): void {
    this.bitrateSwitches++
    if (height && height > this.maxHeight) this.maxHeight = height
  }

  finish(endReason: SessionStats['endReason'], errorCode?: string): SessionStats {
    return {
      impl: this.impl,
      ttffMs: this.firstFrameAt ? this.firstFrameAt - this.startedAt : 0,
      rebufferCount: this.rebufferCount,
      rebufferMsTotal: this.rebufferMsTotal,
      bitrateSwitches: this.bitrateSwitches,
      maxHeight: this.maxHeight,
      endReason,
      errorCode,
    }
  }
}
