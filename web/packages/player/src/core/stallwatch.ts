/**
 * Сторож зависаний: замечает, что источник не успевает за просмотром.
 *
 * Плееру видно только «жду данных» (waiting) и «пошло» (playing). Одно
 * ожидание после перемотки — норма. Но долгое ожидание или частые короткие
 * подряд значат, что сегменты приходят медленнее, чем играют: буфер не
 * копится, и серия будет вставать дальше. Так бывает у площадки за VPN:
 * три остановки на 26 секунд за две минуты, и это не лечится ожиданием.
 * Что делать, решает хозяин (платформа сменит площадку): сторож лишь
 * говорит «пора».
 */
export interface StallPolicy {
  /** Одно ожидание дольше этого — пора, не дожидаясь его конца. */
  longMs: number
  /** Сумма ожиданий за последние windowMs больше этого — пора. */
  totalMs: number
  windowMs: number
  /**
   * Старт без первого кадра дольше этого — поток не запустился. Отдельный, и
   * более долгий, срок: до первого кадра обычный сторож молчит (это время
   * старта), но ждать вечно нельзя. Больше, чем ожидание по ходу: первый
   * сегмент через узкий канал законно тянется дольше, а первый байт шлюз
   * отдаёт в пределах maxTimeToFirstByteMs (30 с у hls.js).
   */
  startupMs: number
}

/**
 * Замеры за 14 дней на рабочем сервере: у прямых площадок ожиданий около одного за сеанс,
 * у kinopub через VPN — 2,5 на ПК и 8 на ТВ, по 4–8 секунд каждое.
 * Десять секунд одним куском человек уже воспринимает как поломку.
 */
export const defaultStallPolicy: StallPolicy =
  { longMs: 10_000, totalMs: 15_000, windowMs: 180_000, startupMs: 45_000 }

export interface StallTrip {
  /**
   * long — одно ожидание затянулось, often — короткие, но подряд, startup —
   * первого кадра не было дольше срока старта (поток не запустился).
   */
  reason: 'long' | 'often' | 'startup'
  /** Сколько длится (или длилось) последнее ожидание. */
  waitedMs: number
  /** Сумма ожиданий за окно вместе с последним. */
  recentMs: number
}

export class StallWatch {
  // Ожидания до первого кадра — это время старта, его считает телеметрия.
  private started = false
  private since: number | null = null
  // Ожидание началось с перемотки: в сумму «часто» не идёт — человек сам
  // прыгнул туда, где ещё нет данных. Долгое — всё равно повод.
  private afterSeek = false
  private past: { at: number; ms: number }[] = []
  private timer: ReturnType<typeof setTimeout> | null = null
  // Отдельный сторож старта: пока первого кадра нет, обычный спит, а этот
  // ждёт, не затянулся ли сам запуск. Снимается первым кадром.
  private startupSince: number | null = null
  private startupTimer: ReturnType<typeof setTimeout> | null = null

  constructor(
    private readonly onTrip: (t: StallTrip) => void,
    private readonly policy: StallPolicy = defaultStallPolicy,
    private readonly now: () => number = () => Date.now(),
  ) {}

  playing(): void {
    this.started = true
    this.clearStartup()
    this.finish()
  }

  waiting(seeking: boolean): void {
    // Первого кадра ещё не было: обычный сторож молчит (это время старта),
    // но поток, разобравший манифест и не отдавший ни кадра за весь срок
    // старта, не запустится сам — сегменты идут тоньше, чем играются, и
    // фатальной ошибкой это не падает. Заводим сторож старта один раз;
    // первый кадр (playing) его снимет.
    if (!this.started) {
      if (this.startupSince !== null) return
      this.startupSince = this.now()
      this.startupTimer = setTimeout(() => {
        this.startupTimer = null
        if (this.started || this.startupSince === null) return
        this.onTrip({ reason: 'startup', waitedMs: this.now() - this.startupSince, recentMs: this.now() - this.startupSince })
      }, this.policy.startupMs)
      return
    }
    if (this.since !== null) return
    this.since = this.now()
    this.afterSeek = seeking
    this.timer = setTimeout(() => {
      this.timer = null
      if (this.since === null) return
      const waited = this.now() - this.since
      this.trip('long', waited, this.recent() + waited)
    }, this.policy.longMs)
  }

  /** Пауза кончает ожидание: дальше ждёт человек, а не плеер. Сторож старта
   *  тоже снимается — иначе пауза в ожидании первого кадра сорвала бы
   *  переключение площадки; на возобновлении он заведётся снова. */
  paused(): void { this.clearStartup(); this.finish() }

  stop(): void {
    this.clear()
    this.clearStartup()
    this.since = null
  }

  private clearStartup(): void {
    if (this.startupTimer) { clearTimeout(this.startupTimer); this.startupTimer = null }
    this.startupSince = null
  }

  private finish(): void {
    this.clear()
    if (this.since === null) return
    const at = this.now()
    const ms = at - this.since
    this.since = null
    if (this.afterSeek) return
    this.past.push({ at, ms })
    const recent = this.recent()
    if (recent >= this.policy.totalMs) this.trip('often', ms, recent)
  }

  private recent(): number {
    const from = this.now() - this.policy.windowMs
    this.past = this.past.filter((p) => p.at >= from)
    return this.past.reduce((s, p) => s + p.ms, 0)
  }

  // После сигнала счёт с нуля: следующий — только по новым ожиданиям, а не
  // на каждом коротком вслед за уже засчитанными.
  private trip(reason: StallTrip['reason'], waitedMs: number, recentMs: number): void {
    this.past = []
    this.onTrip({ reason, waitedMs, recentMs })
  }

  private clear(): void {
    if (this.timer) { clearTimeout(this.timer); this.timer = null }
  }
}
