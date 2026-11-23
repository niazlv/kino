import { useEffect, useRef, useState } from 'react'
import type { Playback } from '../api'
import type { AudioTrack, TextTrack } from '@kino/player'
import { labelsFor } from './ShareDialog'

// Мост живёт в native.ts: им пользуется не только это окно, но и раздел
// со скачанным. Здесь он переэкспортирован ради прежних импортов.
export { hasApp } from '../native'
export type { KinoApp } from '../native'

/**
 * Скачивание внутри приложения.
 *
 * Оболочка Android кладёт в окно объект kinoApp; когда он есть, «Скачать»
 * открывает этот диалог, а не прямую ссылку. Состав серии страница уже
 * знает из тикета плеера, поэтому окно открывается сразу, без похода на
 * площадку. Выбор уходит оболочке одним вызовом, и дальше качает встроенная
 * утилита: все отмеченные озвучки и субтитры в одном файле.
 *
 * Дорожки берутся у плеера, а не из тикета: их имена — из плейлиста, и
 * именно по ним утилита отбирает дорожки (подписи площадки в тикете с
 * плейлистом не совпадают). Оболочке уходят эти же имена.
 */

export function DownloadDialog(
  { title, episodeLabel, season, seasons, source, audio, text, onClose }:
  { title: string; episodeLabel: string; season: number; seasons: number[]
    source: Playback | null; audio: AudioTrack[]; text: TextTrack[]; onClose: () => void },
) {
  const ref = useRef<HTMLDialogElement>(null)
  const [scope, setScope] = useState<'episode' | 'season' | 'all'>('episode')
  const [maxHeight, setMaxHeight] = useState(0)
  // Пусто = «всё», как в ссылке-разрешении: сужение — осознанное действие.
  const [audios, setAudios] = useState<string[]>([])
  const [subs, setSubs] = useState<string[]>([])
  const [folder, setFolder] = useState(() => window.kinoApp?.folderName() ?? '')

  useEffect(() => { ref.current?.showModal() }, [])
  // Выбор папки уводит в системное окно; по возвращении подпись обновляется.
  useEffect(() => {
    const on = () => { if (document.visibilityState === 'visible') setFolder(window.kinoApp?.folderName() ?? '') }
    document.addEventListener('visibilitychange', on)
    return () => document.removeEventListener('visibilitychange', on)
  }, [])

  const heights = [...new Set((source?.renditions ?? []).map(r => r.height))].sort((a, b) => b - a)
  // Плеер отдаёт дорожки по каждой группе качества — одни и те же имена по
  // три раза. Утилита выбирает группу сама, здесь нужны только имена.
  const uniq = <T extends { label: string; language: string }>(list: T[]) => {
    const seen = new Set<string>()
    return list.filter(t => { const k = t.label || t.language; if (seen.has(k)) return false; seen.add(k); return true })
  }
  const audioList = uniq(audio)
  // Субтитры отдельными файлами (с адресом) плеер добавляет сам, в плейлисте
  // их нет — утилита их не найдёт, и предлагать их здесь было бы обманом.
  const subList = uniq(text.filter(t => !t.url))
  const audioLabels = labelsFor(audioList.map(a => ({ id: a.id, title: a.label, language: a.language })))
  const subLabels = labelsFor(subList.map(s => ({ id: s.id, title: s.label, language: s.language })))
  const allAudio = audioList.map(a => a.id)
  const allSubs = subList.map(s => s.id)
  const ready = source !== null && (audio.length > 0 || source.audios.length === 0)

  const toggle = (list: string[], set: (v: string[]) => void, all: string[], id: string) => {
    const cur = list.length === 0 ? all : list
    set(cur.includes(id) ? cur.filter(x => x !== id) : [...cur, id])
  }

  // Что утилите передать как имя дорожки: подпись площадки без нашей
  // нумерации совпадений и без запятых — запятая в списке разделяет имена.
  const names = (tracks: { id: string; label: string; language: string }[], chosen: string[]) =>
    chosen.length === 0 || chosen.length === tracks.length ? []
      : tracks.filter(t => chosen.includes(t.id)).map(t => (t.label || t.language).replace(/,/g, ' '))

  const start = () => {
    const app = window.kinoApp
    if (!app) return
    const here = location.href
    const link = scope === 'episode' ? here : here.replace(/\/(s\d+(e\d+)?|\d+)\/?$/i, '')
    app.download(JSON.stringify({
      link, title,
      quality: maxHeight ? `${maxHeight}p` : '',
      audio: names(audioList, audios),
      subtitles: names(subList, subs),
      seasons: scope === 'season' ? String(season) : '',
    }))
    ref.current?.close()
  }

  return (
    <dialog ref={ref} className="sharedlg sheet" onClose={onClose}>
      <h3>Скачать</h3>
      <p className="muted small">{episodeLabel}</p>

      {season > 0 && (
        <div className="seasons" style={{ margin: '12px 0 4px' }}>
          <button className={scope === 'episode' ? 'on' : ''} onClick={() => setScope('episode')}>Эту серию</button>
          <button className={scope === 'season' ? 'on' : ''} onClick={() => setScope('season')}>
            {seasons.length > 1 ? `Сезон ${season}` : 'Весь сезон'}
          </button>
          {seasons.length > 1 && (
            <button className={scope === 'all' ? 'on' : ''} onClick={() => setScope('all')}>Весь сериал</button>
          )}
        </div>
      )}

      {!ready && <p className="muted small">Состав серии подгружается…</p>}

      {ready && source && (
        <div className="sharesel">
          {heights.length > 0 && (
            <label className="row">
              <span className="muted small" style={{ minWidth: 92 }}>Качество</span>
              <select value={maxHeight} onChange={e => setMaxHeight(Number(e.target.value))}>
                <option value={0}>лучшее ({heights[0]}p)</option>
                {heights.slice(1).map(h => <option key={h} value={h}>не выше {h}p</option>)}
              </select>
            </label>
          )}
          {audioList.length > 0 && (
            <fieldset>
              <legend className="muted small">Озвучки</legend>
              {audioList.map((a, i) => {
                const on = audios.length === 0 || audios.includes(a.id)
                return (
                  <label key={a.id} className="chk">
                    <input type="checkbox" checked={on} onChange={() => toggle(audios, setAudios, allAudio, a.id)} />
                    <span>{audioLabels[i]}</span>
                  </label>
                )
              })}
            </fieldset>
          )}
          {subList.length > 0 && (
            <fieldset>
              <legend className="muted small">Субтитры</legend>
              {subList.map((s, i) => {
                const on = subs.length === 0 || subs.includes(s.id)
                return (
                  <label key={s.id} className="chk">
                    <input type="checkbox" checked={on} onChange={() => toggle(subs, setSubs, allSubs, s.id)} />
                    <span>{subLabels[i]}</span>
                  </label>
                )
              })}
            </fieldset>
          )}
        </div>
      )}

      <div className="row" style={{ marginTop: 12 }}>
        <span className="muted small">Куда</span>
        <span className="small" style={{ flex: 1 }}>{folder || 'Фильмы → kino'}</span>
        <button className="sm" onClick={() => window.kinoApp?.chooseFolder()}>Выбрать папку…</button>
      </div>

      <div className="row" style={{ marginTop: 14 }}>
        <button className="primary" disabled={!ready} onClick={start} data-autofocus>Скачать</button>
        <button className="ghost" onClick={() => ref.current?.close()}>Отмена</button>
      </div>
    </dialog>
  )
}
