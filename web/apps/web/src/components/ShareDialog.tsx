import { useEffect, useRef, useState } from 'react'
import { api, type Playback, type ShareKind, type ShareLink } from '../api'
import { describeError } from '../App'

/**
 * Ссылка-разрешение: «посмотреть» и «скачать».
 *
 * Виды разделены явно, потому что последствия у них разные. Ссылка на
 * просмотр открывает гостю плеер по ПОДПИСКЕ ПОДЕЛИВШЕГОСЯ; ссылка на
 * скачивание отдаёт утилите манифест с прямыми адресами. Свести их в одну
 * кнопку значило бы прятать это различие.
 *
 * Диалог нативный: <dialog> сам даёт ловушку фокуса, закрытие по Esc и
 * подложку — писать это руками только чтобы получить хуже.
 */

/** Русское склонение по числу: «1 качество», «2 качества», «5 качеств». */
function plural(n: number, one: string, few: string, many: string) {
  const a = Math.abs(n) % 100
  const b = a % 10
  if (a > 10 && a < 20) return many
  if (b > 1 && b < 5) return few
  if (b === 1) return one
  return many
}

/**
 * Подписи дорожек для показа. Площадка иногда отдаёт несколько дорожек с
 * одинаковым описанием; выбирать между двумя одинаковыми строками нельзя,
 * поэтому совпадающие нумеруем.
 */
export function labelsFor(tracks: { id: string; title?: string; language?: string }[]) {
  const raw = tracks.map((t, i) => t.title || t.language || `дорожка ${i + 1}`)
  const seen = new Map<string, number>()
  raw.forEach(l => seen.set(l, (seen.get(l) ?? 0) + 1))
  const used = new Map<string, number>()
  return raw.map(l => {
    if ((seen.get(l) ?? 0) < 2) return l
    const n = (used.get(l) ?? 0) + 1
    used.set(l, n)
    return `${l} · ${n}`
  })
}

export function ShareDialog(
  { episodeId, label, source, onClose }:
  { episodeId: number; label: string; source: Playback | null; onClose: () => void },
) {
  const ref = useRef<HTMLDialogElement>(null)
  const [kind, setKind] = useState<ShareKind>('watch')
  const [more, setMore] = useState(false)
  const [maxHeight, setMaxHeight] = useState(0)
  // Пусто = «всё». Именно поэтому по умолчанию ничего не перечисляем:
  // выбрано всё, и сужение — осознанное действие.
  const [audios, setAudios] = useState<string[]>([])
  const [subs, setSubs] = useState<string[]>([])
  const [link, setLink] = useState<ShareLink | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => { ref.current?.showModal() }, [])

  // Смена вида обесценивает уже выданную ссылку в окне: показывать ссылку
  // на просмотр под заголовком «скачать» — прямой путь скопировать не то.
  useEffect(() => { setLink(null); setErr('') }, [kind])

  const heights = [...new Set((source?.renditions ?? []).map(r => r.height))]
    .sort((a, b) => b - a)

  const toggle = (list: string[], set: (v: string[]) => void, all: string[], id: string) => {
    // Пустой список означает «все», поэтому первое снятие галочки нужно
    // сначала развернуть в полный набор — иначе снятие одной дорожки
    // прочиталось бы как «оставить только её».
    const cur = list.length === 0 ? all : list
    set(cur.includes(id) ? cur.filter(x => x !== id) : [...cur, id])
  }

  const create = async () => {
    setBusy(true); setErr('')
    try {
      setLink(await api.createShare(episodeId, kind,
        kind === 'download' ? { maxHeight: maxHeight || undefined, audios, subtitles: subs } : {}))
    } catch (e) {
      setErr(describeError(e))
    } finally {
      setBusy(false)
    }
  }

  const copy = (text: string) => {
    navigator.clipboard?.writeText(text).catch(() => {})
  }

  // Утилита берёт дорожки из плейлиста. Субтитров отдельными файлами (с
  // адресом) там нет — ни в выборе, ни в счёте их быть не должно.
  const subtitles = (source?.subtitles ?? []).filter(s => !s.url)
  const allAudioIds = (source?.audios ?? []).map(a => a.id)
  const allSubIds = subtitles.map(s => s.id)
  const audioLabels = labelsFor(source?.audios ?? [])
  const subLabels = labelsFor(subtitles)

  return (
    <dialog ref={ref} className="sharedlg" onClose={onClose}>
      <h3>Поделиться</h3>
      <p className="muted small">{label}</p>

      <div className="seasons" style={{ margin: '14px 0 4px' }}>
        <button className={kind === 'watch' ? 'on' : ''} onClick={() => setKind('watch')}>
          Смотреть
        </button>
        <button className={kind === 'download' ? 'on' : ''} onClick={() => setKind('download')}>
          Скачать
        </button>
      </div>

      {kind === 'watch' ? (
        <p className="muted small">
          Ссылка открывает плеер без входа, по вашей подписке. Прогресс
          гостя никуда не пишется — ваши позиции он не сдвинет. Живёт 48 часов,
          и её можно отозвать.
        </p>
      ) : (
        <>
          <p className="muted small">
            Кнопка «Скачать» на странице сохраняет файл сразу, в один клик —
            но в нём одна дорожка и одно качество, те, что запечены площадкой.
            Ссылка ниже нужна, когда дорожки и субтитры выбираете вы: собрать
            такой файл может только утилита, страница — нет. Живёт 2 часа,
            второй раз авторизоваться не нужно.
          </p>

          {!source && <p className="muted small">Состав серии подгружается…</p>}

          {source && (
            <>
              <p className="small" style={{ marginTop: 10 }}>
                Выбрано всё: {heights.length}{' '}
                {plural(heights.length, 'качество', 'качества', 'качеств')},{' '}
                {source.audios.length}{' '}
                {plural(source.audios.length, 'дорожка', 'дорожки', 'дорожек')},{' '}
                {subtitles.length === 0
                  ? 'субтитров нет'
                  : `${subtitles.length} ${plural(subtitles.length, 'дорожка субтитров', 'дорожки субтитров', 'дорожек субтитров')}`}.
              </p>
              <button className="linky" onClick={() => setMore(v => !v)}>
                {more ? 'Свернуть' : 'Дополнительно'}
              </button>
            </>
          )}

          {source && more && (
            <div className="sharesel">
              {heights.length > 0 && (
                <label className="row">
                  <span className="muted small" style={{ minWidth: 92 }}>Не выше</span>
                  <select value={maxHeight} onChange={e => setMaxHeight(Number(e.target.value))}>
                    <option value={0}>любого качества</option>
                    {heights.map(h => <option key={h} value={h}>{h}p</option>)}
                  </select>
                </label>
              )}

              {source.audios.length > 0 && (
                <fieldset>
                  <legend className="muted small">Аудиодорожки</legend>
                  {source.audios.map((a, i) => {
                    const on = audios.length === 0 || audios.includes(a.id)
                    return (
                      <label key={a.id} className="chk">
                        <input type="checkbox" checked={on}
                               onChange={() => toggle(audios, setAudios, allAudioIds, a.id)} />
                        <span>{audioLabels[i]}</span>
                      </label>
                    )
                  })}
                </fieldset>
              )}

              {subtitles.length > 0 && (
                <fieldset>
                  <legend className="muted small">Субтитры</legend>
                  {subtitles.map((s, i) => {
                    const on = subs.length === 0 || subs.includes(s.id)
                    return (
                      <label key={s.id} className="chk">
                        <input type="checkbox" checked={on}
                               onChange={() => toggle(subs, setSubs, allSubIds, s.id)} />
                        <span>{subLabels[i]}</span>
                      </label>
                    )
                  })}
                </fieldset>
              )}
            </div>
          )}
        </>
      )}

      {err && <p className="err small">{err}</p>}

      {link ? (
        <div className="sharelink">
          {link.command ? (
            <>
              <p className="muted small">Вставьте в терминал:</p>
              <code>{link.command}</code>
              <div className="row">
                <button onClick={() => copy(link.command!)}>Скопировать команду</button>
                <button className="ghost" onClick={() => copy(link.url)}>Только ссылку</button>
              </div>
            </>
          ) : (
            <>
              <code>{link.url}</code>
              <div className="row">
                <button onClick={() => copy(link.url)}>Скопировать ссылку</button>
              </div>
            </>
          )}
          <p className="muted small">
            Действует до {new Date(link.expiresAt).toLocaleString()}.{' '}
            <button className="linky" onClick={() => {
              void api.revokeShare(link.token).catch(() => {}); setLink(null)
            }}>Отозвать</button>
          </p>
        </div>
      ) : (
        <div className="row" style={{ marginTop: 14 }}>
          <button className="primary" disabled={busy} onClick={() => void create()}>
            {busy ? 'Создаём…' : 'Создать ссылку'}
          </button>
          <button className="ghost" onClick={() => ref.current?.close()}>Отмена</button>
        </div>
      )}
    </dialog>
  )
}
