import { describe, expect, it } from 'vitest'
import { KinoPlayer } from './player'
import type { EngineEvent, EnginePayload, PlaybackEngine } from './engine/engine'
import type { AudioTrack, PlayerEvents, PlayerImpl, TextTrack } from './types'

// Ядро отделено от движков: движок подменяется фабрикой, видео — пустышкой.
// Проверяется контракт «панель знает, что играет, с самого старта».
describe('KinoPlayer: объявление текущих дорожек', () => {
  const track = (id: string, label: string): AudioTrack => ({ id, label, language: 'rus' })

  class FakeEngine implements PlaybackEngine {
    readonly impl: PlayerImpl = 'hlsjs'
    private listeners = new Map<EngineEvent, ((p: EnginePayload) => void)[]>()

    constructor(
      private readonly audio: AudioTrack[],
      private readonly text: TextTrack[] = [],
      private readonly current: { audio: string | null; text: string | null } = { audio: null, text: null },
    ) {}

    async attach(): Promise<void> {}
    detach(): void {}
    audioTracks() { return this.audio }
    textTracks() { return this.text }
    qualities() { return [] }
    currentAudioId() { return this.current.audio }
    currentTextId() { return this.current.text }
    selectAudio(id: string) { this.current.audio = id }
    selectQuality(): void {}
    selectText(id: string | null) { this.current.text = id }
    on(event: EngineEvent, cb: (p: EnginePayload) => void): void {
      const list = this.listeners.get(event) ?? []
      list.push(cb)
      this.listeners.set(event, list)
    }

    // Подмена события движка в тесте: так тестируется реакция ядра
    // на фолбэк hls.js без самого hls.js.
    emitEngine(event: EngineEvent, payload: EnginePayload): void {
      for (const cb of this.listeners.get(event) ?? []) cb(payload)
    }
  }

  // Плееру от видео нужен только набор событий и пара полей — все no-op.
  const fakeVideo = () => ({
    poster: '',
    removeAttribute() {},
    addEventListener() {},
    removeEventListener() {},
    currentTime: 0,
    duration: NaN,
  }) as unknown as HTMLVideoElement

  it('объявляет дорожку, играющую по умолчанию', async () => {
    const engine = new FakeEngine([track('0', 'AniLibria'), track('1', 'StudioBand')], [], { audio: '1', text: null })
    const changes: PlayerEvents['audiochange'][] = []
    const p = new KinoPlayer(fakeVideo(), {
      engineFactory: () => engine,
      on: { audiochange: e => changes.push(e) },
    })
    await p.load({ url: 'http://x/h.m3u8' })
    await p.unload('abandoned')
    // Одна дорожка уже играет, хотя клика не было — и она объявлена.
    expect(changes).toEqual([{ id: '1', label: 'StudioBand', language: 'rus', auto: true }])
  })

  it('после выбора по предпочтению объявляется оно же', async () => {
    const engine = new FakeEngine([track('0', 'AniLibria'), track('1', 'StudioBand')], [], { audio: '1', text: null })
    const changes: PlayerEvents['audiochange'][] = []
    const p = new KinoPlayer(fakeVideo(), {
      engineFactory: () => engine,
      on: { audiochange: e => changes.push(e) },
    })
    await p.load({ url: 'http://x/h.m3u8', preferredAudioLabel: 'AniLibria' })
    await p.unload('abandoned')
    expect(changes.at(-1)).toMatchObject({ id: '0', label: 'AniLibria', auto: true })
  })

  it('подписи дорожек сравниваются правилом хозяина', async () => {
    // Плейлист kinopub пишет студию с номером, видом и языком, а запомнена
    // она как «Воротилин»: без правила хозяина совпадения нет.
    const engine = new FakeEngine(
      [track('0', '01. Многоголосый. AniLibria (RUS)'), track('1', '02. Одноголосый. Воротилин (RUS)')],
      [], { audio: '0', text: null })
    const changes: PlayerEvents['audiochange'][] = []
    const p = new KinoPlayer(fakeVideo(), {
      engineFactory: () => engine,
      on: { audiochange: e => changes.push(e) },
      audioLabelKey: s => s.replace(/^\d+\.\s*|\s*\(\w+\)$/g, '').split('. ').at(-1)!.toLowerCase(),
    })
    await p.load({ url: 'http://x/h.m3u8', preferredAudioLabel: 'Воротилин' })
    await p.unload('abandoned')
    expect(changes.at(-1)).toMatchObject({ id: '1', auto: true })
  })

  it('выключенные субтитры — тоже состояние, и оно объявляется', async () => {
    const engine = new FakeEngine([track('0', 'AniLibria')], [{ id: '0', label: 'ru', language: 'ru' }], { audio: '0', text: null })
    const changes: PlayerEvents['textchange'][] = []
    const p = new KinoPlayer(fakeVideo(), {
      engineFactory: () => engine,
      on: { textchange: e => changes.push(e) },
    })
    await p.load({ url: 'http://x/h.m3u8' })
    await p.unload('abandoned')
    expect(changes).toEqual([{ id: null, label: '', language: '', auto: true }])
  })

  it('субтитры с default включаются, если человек не выбрал другое', async () => {
    const text: TextTrack[] = [
      { id: 'x0', label: '(Russian) Русские', language: 'rus', url: '/sub/0.vtt', default: true },
      { id: 'x1', label: '(English) English', language: 'eng', url: '/sub/1.vtt' },
    ]
    const load = async (preferredTextLabel?: string) => {
      const engine = new FakeEngine([track('0', 'Оригинал')], text, { audio: '0', text: null })
      const changes: PlayerEvents['textchange'][] = []
      const p = new KinoPlayer(fakeVideo(), { engineFactory: () => engine, on: { textchange: e => changes.push(e) } })
      await p.load({ url: 'http://x/h.m3u8', preferredTextLabel })
      await p.unload('abandoned')
      return changes.at(-1)
    }
    // Без предпочтения и с подписью, которой здесь нет, — те, что по умолчанию.
    expect(await load()).toMatchObject({ id: 'x0', auto: true })
    expect(await load('Forced (ENG)')).toMatchObject({ id: 'x0', auto: true })
    // Выбранные раньше — они.
    expect(await load('(English) English')).toMatchObject({ id: 'x1', auto: true })
    // Выключенные намеренно — выключены.
    expect(await load('')).toMatchObject({ id: null, auto: true })
  })

  it('без дорожек объявлять нечего — события нет', async () => {
    const engine = new FakeEngine([], [], { audio: null, text: null })
    const changes: PlayerEvents['audiochange'][] = []
    const p = new KinoPlayer(fakeVideo(), {
      engineFactory: () => engine,
      on: { audiochange: e => changes.push(e) },
    })
    await p.load({ url: 'http://x/h.m3u8' })
    await p.unload('abandoned')
    expect(changes).toEqual([])
  })

  it('переключение дорожки самим движком объявляется как автовыбор', async () => {
    const engine = new FakeEngine([track('0', 'AniLibria'), track('1', 'StudioBand')], [], { audio: '0', text: null })
    const changes: PlayerEvents['audiochange'][] = []
    const p = new KinoPlayer(fakeVideo(), {
      engineFactory: () => engine,
      on: { audiochange: e => changes.push(e) },
    })
    await p.load({ url: 'http://x/h.m3u8' })
    // Фолбэк hls.js: движок молча переехал на другую дорожку.
    engine.emitEngine('audioswitch', { id: '1' })
    await p.unload('abandoned')
    expect(changes.at(-1)).toMatchObject({ id: '1', label: 'StudioBand', auto: true })
  })
})
