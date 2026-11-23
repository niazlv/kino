/**
 * Озвучка так, как её узнаёт человек.
 *
 * Правило склейки живёт на сервере (provider.VoiceKey): разбор подписи и
 * псевдонимы студий, которые ведёт админка. Ключи дорожек потока сервер
 * отдаёт сам (POST /voices/keys, learnVoiceKeys); здесь — тот же разбор без
 * псевдонимов на время, пока ответа нет или нет сети. По нему дорожка
 * kinopub «01. Одноголосый. Воротилин (RUS)» сходится со студией
 * «Воротилин», «DUB» — с «Дублированный», а «Track 1 (RUS)» — с «Русская
 * дорожка».
 */

const LANGS: Record<string, string> = {
  rus: 'Русская дорожка', ru: 'Русская дорожка',
  eng: 'Английская дорожка', en: 'Английская дорожка',
  jpn: 'Японская дорожка', ja: 'Японская дорожка',
  ukr: 'Украинская дорожка', uk: 'Украинская дорожка',
  kor: 'Корейская дорожка', chi: 'Китайская дорожка', zho: 'Китайская дорожка',
}
// Вид дорожки у kinopub: «Одноголосый», «Многоголосый закадровый»…
// Префикс отрезается, только если он вид, — «А. Гаврилов» остаётся целым.
const KINDS = ['голос', 'дубл', 'закадр', 'автор', 'профес', 'любит', 'оригин']

/** Написание без регистра, «ё», пробелов, точек и хвоста «TV». */
function squash(s: string): string {
  const k = s.toLowerCase().replace(/ё/g, 'е').replace(/[^a-z0-9а-я]/g, '')
  return k.endsWith('tv') ? k.slice(0, -2) : k
}

// Вид озвучки без студии — по словам, как provider.parseKind на сервере.
// Старшинство: дубляж > авторский > многоголосый > двухголосый >
// одноголосый > оригинал. Ключ — как пишет kinopub.
const Kind = { None: 0, Original: 1, One: 2, Two: 3, Multi: 4, Author: 5, Dub: 6 } as const
type Kind = typeof Kind[keyof typeof Kind]
const KIND_NAMES: Record<number, [string, string]> = {
  [Kind.Dub]: ['dub', 'Дубляж'],
  [Kind.Author]: ['avo', 'Авторский'],
  [Kind.Multi]: ['mvo', 'Многоголосый'],
  [Kind.Two]: ['dvo', 'Двухголосый'],
  [Kind.One]: ['vo', 'Одноголосый'],
  [Kind.Original]: ['оригинал', 'Оригинал'],
}

function kindWord(w: string): { kind: Kind; amateur?: boolean; filler?: boolean } {
  const has = (...roots: string[]) => roots.some(r => w.startsWith(r))
  if (w === 'dub' || w === 'dubbing' || w === 'dubbed' || has('дубл')) return { kind: Kind.Dub }
  if (w === 'avo' || has('автор')) return { kind: Kind.Author }
  if (w === 'mvo' || has('многогол')) return { kind: Kind.Multi }
  if (w === 'dvo' || has('двухгол', 'двугол')) return { kind: Kind.Two }
  if (w === 'vo' || has('одногол')) return { kind: Kind.One }
  if (w === 'orig' || w === 'original' || has('оригин')) return { kind: Kind.Original }
  if (w === 'amateur' || has('любит')) return { kind: Kind.None, amateur: true }
  if (['проф', 'pro', 'voice', 'over', 'voiceover', 'track'].includes(w) ||
    has('закадр', 'профес', 'полн', 'официал', 'лиценз', 'перевод', 'озвуч', 'дорожк', 'русск')) {
    return { kind: Kind.None, filler: true }
  }
  return { kind: Kind.None }
}

/** Вид без студии: ключ и подпись; null — в подписи есть чужое слово. */
function parseKind(s: string): { key: string; title: string } | null {
  const words = s.toLowerCase().replace(/ё/g, 'е').split(/[^a-z0-9а-я]+/).filter(Boolean)
  if (!words.length) return null
  let best: number = Kind.None
  let amateur = false
  for (const w of words) {
    const k = kindWord(w)
    if (k.kind !== Kind.None) best = Math.max(best, k.kind)
    else if (k.amateur) amateur = true
    else if (!k.filler) return null
  }
  if (best === Kind.Original) { const [key, title] = KIND_NAMES[Kind.Original]!; return { key, title } }
  if (best === Kind.None) return amateur ? { key: 'любительский', title: 'Любительский' } : null
  const [key, title] = KIND_NAMES[best]!
  if (amateur) {
    const t = `Любительский ${title.toLowerCase()}`
    return { key: squash(t), title: t }
  }
  return { key, title }
}

function parse(raw: string): { key: string; title: string } {
  let s = raw.trim().replace(/^\s*\d+\s*\.\s*/, '')
  let lang = ''
  const m = /\s*\(([A-Za-z]{2,3})\)\s*$/.exec(s)
  if (m) { lang = m[1] ?? ''; s = s.slice(0, m.index) }
  const i = s.lastIndexOf('. ')
  if (i >= 0 && s.slice(i + 2).trim() && KINDS.some(w => s.slice(0, i).toLowerCase().includes(w))) {
    s = s.slice(i + 2)
  }
  s = s.trim()
  const kind = parseKind(s)
  if (kind) return kind
  if (!s || /^(track|дорожка)\s*\d*$/i.test(s)) {
    const l = LANGS[lang.toLowerCase()]
    if (l) return { key: squash(l), title: l }
  }
  const t = s || raw.trim()
  return { key: squash(t), title: t }
}

/** Ответы сервера: подпись дорожки → ключ и подпись с псевдонимами. */
const learned = new Map<string, { key: string; title: string }>()

/** Запомнить ключи, которые посчитал сервер. */
export function learnVoiceKeys(keys: Record<string, { key: string; title: string }>) {
  for (const [label, v] of Object.entries(keys)) {
    if (v?.key) learned.set(label, v)
  }
}

/** Подписи, ключей которых сервер ещё не называл. */
export function unknownVoiceLabels(labels: string[]): string[] {
  return [...new Set(labels.filter(l => l && !learned.has(l)))]
}

export function voiceTitle(raw: string): string {
  return learned.get(raw)?.title ?? parse(raw).title
}

/** Одна озвучка под разными написаниями — один ключ. */
export function voiceKey(raw: string): string {
  return learned.get(raw)?.key ?? parse(raw).key
}
