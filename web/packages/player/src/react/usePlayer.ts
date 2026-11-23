import { useEffect, useRef, useState } from 'react'
import { KinoPlayer, type PlayerOptions } from '../core/player'
import type { AudioTrack, PlayerImpl, PlayerSource, Quality, TextTrack } from '../core/types'

/**
 * Тонкая обвязка: вся логика в ядре. React здесь опциональная peer-зависимость,
 * так что пакет остаётся пригодным и без него.
 */
export function usePlayer(source: PlayerSource | null, opts: PlayerOptions = {}) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const playerRef = useRef<KinoPlayer | null>(null)
  const [impl, setImpl] = useState<PlayerImpl | null>(null)
  const [audio, setAudio] = useState<AudioTrack[]>([])
  const [qualities, setQualities] = useState<Quality[]>([])
  const [text, setText] = useState<TextTrack[]>([])
  const [buffering, setBuffering] = useState(false)
  // Что выбрано сейчас — с учётом выбора по предпочтению при загрузке.
  const [audioId, setAudioId] = useState<string | null>(null)
  const [textId, setTextId] = useState<string | null>(null)

  useEffect(() => {
    const video = videoRef.current
    if (!video || !source) return

    const p = new KinoPlayer(video, opts)
    playerRef.current = p
    setAudioId(null); setTextId(null)

    const off = [
      p.on('ready', (e) => setImpl(e.impl)),
      p.on('tracks', (t) => { setAudio(t.audio); setQualities(t.qualities); setText(t.text) }),
      p.on('audiochange', (e) => setAudioId(e.id)),
      p.on('textchange', (e) => setTextId(e.id)),
      p.on('rebufferstart', () => setBuffering(true)),
      p.on('rebufferend', () => setBuffering(false)),
    ]
    void p.load(source)

    return () => {
      off.forEach((f) => f())
      void p.unload('abandoned')
      playerRef.current = null
    }
  }, [source?.url])

  return { videoRef, player: playerRef, impl, audio, qualities, text, buffering, audioId, textId }
}
