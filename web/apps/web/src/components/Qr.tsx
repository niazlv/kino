import { useMemo } from 'react'

/**
 * QR-код своими руками.
 *
 * Нужен ровно для одного экрана — входа по коду — и ровно затем, чтобы
 * ссылку подтверждения не набирали руками там, где это мучение: пультом на
 * телевизоре, пальцем по стеклу в приложении. Ради одной картинки
 * зависимость не нужна: здесь минимальный кодировщик — побайтовый режим,
 * уровень коррекции M, версии 1–10. Это до 213 знаков, а ссылка вида
 * `https://kino.example/#/link/KJ7Q-3MTR` втрое короче.
 *
 * Ниже — прямое следование ISO/IEC 18004. Порядок шагов и все числа задаёт
 * стандарт, поэтому комментарии говорят, ЧТО делает шаг; «почему именно
 * так» — вопрос не к этому файлу.
 */

// Уровень M по версиям 1–10: проверочных слов на блок, блоков покороче и
// данных в каждом, блоков подлиннее и данных в каждом.
type Spec = readonly [ec: number, blocks1: number, data1: number, blocks2: number, data2: number]
const BLOCKS: readonly Spec[] = [
  [0, 0, 0, 0, 0], [10, 1, 16, 0, 0], [16, 1, 28, 0, 0], [26, 1, 44, 0, 0], [18, 2, 32, 0, 0],
  [24, 2, 43, 0, 0], [16, 4, 27, 0, 0], [18, 4, 31, 0, 0], [22, 2, 38, 2, 39],
  [22, 3, 36, 2, 37], [26, 4, 43, 1, 44],
]
// Центры выравнивающих узоров.
const ALIGN: readonly (readonly number[])[] = [
  [], [], [6, 18], [6, 22], [6, 26], [6, 30], [6, 34],
  [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50],
]
const MAX_VERSION = 10

// ── арифметика Галуа для кода Рида — Соломона ──────────────────────────

const EXP = new Uint8Array(512)
const LOG = new Uint8Array(256)
for (let i = 0, x = 1; i < 255; i++) {
  EXP[i] = x
  LOG[x] = i
  x = (x << 1) ^ (x & 0x80 ? 0x11d : 0)
}
for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255] as number

const exp = (i: number) => EXP[i] as number
const log = (v: number) => LOG[v] as number
const mul = (a: number, b: number) => (a && b ? exp(log(a) + log(b)) : 0)

/** Проверочные слова блока — остаток от деления на порождающий многочлен. */
function checkWords(data: Uint8Array, n: number): Uint8Array {
  let gen = Uint8Array.of(1)
  for (let i = 0; i < n; i++) {
    const next = new Uint8Array(gen.length + 1)
    for (let j = 0; j < gen.length; j++) {
      const g = gen[j] as number
      next[j] = (next[j] as number) ^ g
      next[j + 1] = (next[j + 1] as number) ^ mul(g, exp(i))
    }
    gen = next
  }
  const rem = new Uint8Array(n)
  for (const b of data) {
    const factor = b ^ (rem[0] as number)
    rem.copyWithin(0, 1)
    rem[n - 1] = 0
    for (let i = 0; i < n; i++) rem[i] = (rem[i] as number) ^ mul(gen[i + 1] as number, factor)
  }
  return rem
}

/**
 * Матрица QR построчно: единица — тёмный модуль. Бросает, если строка
 * длиннее версии 10, — звать с чем-то длиннее ссылки здесь неоткуда.
 */
export function qrMatrix(text: string): Uint8Array[] {
  const data = new TextEncoder().encode(text)

  // Версия — наименьшая, куда влезает. С десятой счётчик длины занимает
  // 16 бит вместо 8, поэтому запас считается для каждой версии заново.
  let version = 1
  for (; version <= MAX_VERSION; version++) {
    const [, b1, d1, b2, d2] = BLOCKS[version] as Spec
    if (4 + (version < 10 ? 8 : 16) + data.length * 8 <= (b1 * d1 + b2 * d2) * 8) break
  }
  if (version > MAX_VERSION) throw new Error('строка длиннее, чем вмещает QR версии 10')

  const [ecLen, blocks1, data1, blocks2, data2] = BLOCKS[version] as Spec
  const dataWords = blocks1 * data1 + blocks2 * data2
  const size = 17 + 4 * version

  // ── поток данных ──
  const bits: number[] = []
  const push = (value: number, len: number) => {
    for (let i = len - 1; i >= 0; i--) bits.push((value >> i) & 1)
  }
  push(0b0100, 4) // побайтовый режим
  push(data.length, version < 10 ? 8 : 16)
  for (const b of data) push(b, 8)
  for (let i = 0; i < 4 && bits.length < dataWords * 8; i++) bits.push(0) // ограничитель
  while (bits.length % 8) bits.push(0)

  const words: number[] = []
  for (let i = 0; i < bits.length; i += 8) {
    let b = 0
    for (let j = 0; j < 8; j++) b = (b << 1) | (bits[i + j] as number)
    words.push(b)
  }
  for (let i = 0; words.length < dataWords; i++) words.push(i % 2 === 0 ? 0xec : 0x11)

  // Данные и проверочные слова идут вперемежку по блокам: так царапина
  // портит по нескольку слов в каждом блоке, а не один блок целиком.
  const chunks: Uint8Array[] = []
  const checks: Uint8Array[] = []
  for (let i = 0, off = 0; i < blocks1 + blocks2; i++) {
    const len = i < blocks1 ? data1 : data2
    const chunk = Uint8Array.from(words.slice(off, off + len))
    off += len
    chunks.push(chunk)
    checks.push(checkWords(chunk, ecLen))
  }
  const stream: number[] = []
  for (let i = 0; i < Math.max(data1, data2); i++) {
    for (const c of chunks) if (i < c.length) stream.push(c[i] as number)
  }
  for (let i = 0; i < ecLen; i++) for (const c of checks) stream.push(c[i] as number)

  // ── служебные узоры ──
  // Матрица плоская: строк до 57, и возня с массивом массивов тут ничего
  // не проясняет. Рядом такая же карта «модуль служебный, не трогать».
  const m = new Uint8Array(size * size)
  const fixed = new Uint8Array(size * size)
  const at = (r: number, c: number) => m[r * size + c] as number
  const put = (r: number, c: number, v: number) => {
    if (r < 0 || c < 0 || r >= size || c >= size) return
    m[r * size + c] = v
    fixed[r * size + c] = 1
  }

  // Три угловых квадрата с белой каймой.
  for (const [r0, c0] of [[0, 0], [0, size - 7], [size - 7, 0]] as const) {
    for (let dr = -1; dr <= 7; dr++) {
      for (let dc = -1; dc <= 7; dc++) {
        const ring = Math.max(Math.abs(dr - 3), Math.abs(dc - 3))
        put(r0 + dr, c0 + dc, ring === 2 || ring > 3 ? 0 : 1)
      }
    }
  }
  // Синхродорожки.
  for (let i = 8; i < size - 8; i++) {
    put(6, i, i % 2 === 0 ? 1 : 0)
    put(i, 6, i % 2 === 0 ? 1 : 0)
  }
  // Выравнивающие узоры, кроме тех, что попали бы на угловые квадраты.
  const centers = ALIGN[version] as readonly number[]
  for (const r of centers) {
    for (const c of centers) {
      if ((r === 6 && c === 6) || (r === 6 && c === size - 7) || (r === size - 7 && c === 6)) continue
      for (let dr = -2; dr <= 2; dr++) {
        for (let dc = -2; dc <= 2; dc++) {
          put(r + dr, c + dc, Math.max(Math.abs(dr), Math.abs(dc)) === 1 ? 0 : 1)
        }
      }
    }
  }
  // Места под сведения о формате: значение зависит от маски и ляжет позже.
  // Шестая строка и шестой столбец — синхродорожки: они идут насквозь и
  // полю формата не принадлежат, затереть их значило бы сломать код.
  for (let i = 0; i < 9; i++) {
    if (i === 6) continue
    put(8, i, 0)
    put(i, 8, 0)
  }
  for (let i = 0; i < 8; i++) {
    put(8, size - 1 - i, 0)
    put(size - 1 - i, 8, 0)
  }
  // Номер версии — с седьмой, двумя копиями.
  if (version >= 7) {
    let rem = version
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25)
    const info = (version << 12) | rem
    for (let i = 0; i < 18; i++) {
      const bit = (info >> i) & 1
      put(size - 11 + (i % 3), Math.floor(i / 3), bit)
      put(Math.floor(i / 3), size - 11 + (i % 3), bit)
    }
  }

  // ── данные змейкой снизу справа вверх, по два столбца ──
  let bit = 0
  let up = true
  for (let col = size - 1; col > 0; col -= 2) {
    if (col === 6) col-- // вертикальная синхродорожка столбцом не считается
    for (let i = 0; i < size; i++) {
      const row = up ? size - 1 - i : i
      for (const c of [col, col - 1]) {
        if (fixed[row * size + c]) continue
        // Хвост за концом потока — незанятые биты, они нулевые.
        const word = stream[bit >> 3]
        m[row * size + c] = word === undefined ? 0 : (word >> (7 - (bit & 7))) & 1
        bit++
      }
    }
    up = !up
  }

  // ── маска ──
  const maskBit = (mask: number, r: number, c: number): boolean => {
    switch (mask) {
      case 0: return (r + c) % 2 === 0
      case 1: return r % 2 === 0
      case 2: return c % 3 === 0
      case 3: return (r + c) % 3 === 0
      case 4: return (Math.floor(r / 2) + Math.floor(c / 3)) % 2 === 0
      case 5: return ((r * c) % 2) + ((r * c) % 3) === 0
      case 6: return (((r * c) % 2) + ((r * c) % 3)) % 2 === 0
      default: return (((r + c) % 2) + ((r * c) % 3)) % 2 === 0
    }
  }

  const masked = (mask: number): Uint8Array => {
    const g = Uint8Array.from(m)
    for (let r = 0; r < size; r++) {
      for (let c = 0; c < size; c++) {
        if (!fixed[r * size + c] && maskBit(mask, r, c)) g[r * size + c] = at(r, c) ^ 1
      }
    }
    // Сведения о формате: уровень коррекции (M — нули) и номер маски,
    // защищённые кодом БЧХ, двумя копиями по разным углам.
    let rem = mask
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537)
    const info = ((mask << 10) | rem) ^ 0x5412
    const set = (r: number, c: number, i: number) => { g[r * size + c] = (info >> i) & 1 }
    for (let i = 0; i <= 5; i++) set(i, 8, i)
    set(7, 8, 6)
    set(8, 8, 7)
    set(8, 7, 8)
    for (let i = 9; i < 15; i++) set(8, 14 - i, i)
    for (let i = 0; i < 8; i++) set(8, size - 1 - i, i)
    for (let i = 8; i < 15; i++) set(size - 15 + i, 8, i)
    g[(size - 8) * size + 8] = 1 // всегда тёмный модуль
    return g
  }

  // Штраф: чем однороднее картинка и чем больше в ней подобий углового
  // квадрата, тем труднее сканеру. Берём маску с наименьшим.
  const penalty = (g: Uint8Array): number => {
    const v = (r: number, c: number) => g[r * size + c] as number
    let score = 0
    for (let i = 0; i < size; i++) {
      const lines = [
        Array.from({ length: size }, (_, j) => v(i, j)),
        Array.from({ length: size }, (_, j) => v(j, i)),
      ]
      for (const line of lines) {
        let run = 1
        for (let j = 1; j <= size; j++) {
          if (j < size && line[j] === line[j - 1]) {
            run++
            continue
          }
          if (run >= 5) score += run - 2
          run = 1
        }
        const s = line.join('')
        for (const pattern of ['10111010000', '00001011101']) {
          for (let k = s.indexOf(pattern); k >= 0; k = s.indexOf(pattern, k + 1)) score += 40
        }
      }
    }
    for (let r = 0; r < size - 1; r++) {
      for (let c = 0; c < size - 1; c++) {
        const x = v(r, c)
        if (x === v(r, c + 1) && x === v(r + 1, c) && x === v(r + 1, c + 1)) score += 3
      }
    }
    let dark = 0
    for (const x of g) dark += x
    score += Math.floor(Math.abs((dark * 100) / (size * size) - 50) / 5) * 10
    return score
  }

  let best = masked(0)
  let bestScore = penalty(best)
  for (let mask = 1; mask < 8; mask++) {
    const g = masked(mask)
    const score = penalty(g)
    if (score < bestScore) {
      best = g
      bestScore = score
    }
  }
  return Array.from({ length: size }, (_, r) => best.slice(r * size, r * size + size))
}

/**
 * Картинка кода. Всегда тёмное на белом, какой бы ни была тема витрины:
 * сканеру нужен контраст, а не наше оформление. Поля вокруг — часть кода,
 * без них его не находят, поэтому они внутри картинки, а не в вёрстке.
 */
export function Qr({ text, className = '' }: { text: string; className?: string }) {
  const matrix = useMemo(() => {
    try {
      return qrMatrix(text)
    } catch {
      return null
    }
  }, [text])
  if (!matrix) return null

  const quiet = 4
  const side = matrix.length + quiet * 2
  // Один путь на все модули: тысяча отдельных прямоугольников заметно
  // тормозит отрисовку на телевизоре.
  let d = ''
  matrix.forEach((row, r) => row.forEach((v, c) => {
    if (v) d += `M${c + quiet} ${r + quiet}h1v1h-1z`
  }))
  return (
    <svg className={`qr ${className}`.trim()} viewBox={`0 0 ${side} ${side}`}
         shapeRendering="crispEdges" role="img" aria-label="QR-код ссылки для входа">
      <rect width={side} height={side} fill="#fff" />
      <path d={d} fill="#000" />
    </svg>
  )
}
