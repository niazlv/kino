package ru.sorewa.kino.player

import ru.sorewa.kino.data.SkipSegment

/**
 * Разметка пропуска: какой разметке верить для этого потока. Порт
 * web/packages/player/src/core/skips.ts.
 */

enum class SkipKind { INTRO, OUTRO, RECAP, PREVIEW }

data class Skip(val kind: SkipKind, val start: Double, val end: Double) {
    val label: String get() = when (kind) {
        SkipKind.INTRO -> "Пропустить заставку"
        SkipKind.OUTRO -> "Пропустить титры"
        SkipKind.RECAP -> "Пропустить пересказ"
        SkipKind.PREVIEW -> "Пропустить анонс"
    }
    val name: String get() = when (kind) {
        SkipKind.INTRO -> "Заставка"
        SkipKind.OUTRO -> "Титры"
        SkipKind.RECAP -> "Пересказ"
        SkipKind.PREVIEW -> "Анонс"
    }
    val skippedLabel: String get() = when (kind) {
        SkipKind.INTRO -> "Заставка пропущена"
        SkipKind.OUTRO -> "Титры пропущены"
        SkipKind.RECAP -> "Пересказ пропущен"
        SkipKind.PREVIEW -> "Анонс пропущен"
    }
}

/**
 * Насколько длина потока может разойтись с длиной размеченного файла.
 * Сверено на «Фрирен» (2-я серия): поток Анилибрии короче релиза из AniSkip
 * на 6,5 с — такую разметку брать нельзя. Секунда-другая — округление.
 */
const val DURATION_TOLERANCE = 2.5

/** Короче этого пропускать нечего: кнопка мелькнула бы и исчезла. */
private const val MIN_LENGTH = 3.0

/**
 * Какие отрезки показывать в потоке длиной [duration] (секунды).
 *
 * Разметка с этого потока важнее чужой. Безымянный отрезок (Kodik и Alloha
 * пишут голые «от-до») в первой половине серии — заставка, во второй — титры.
 */
fun pickSkips(raw: List<SkipSegment>, duration: Double): List<Skip> {
    if (duration <= 0.0) return emptyList()
    val exact = mutableListOf<Skip>()
    val foreign = mutableListOf<Skip>()
    for (s in raw) {
        if (!(s.end > s.start) || s.start < 0 || s.start >= duration) continue
        if (s.forDuration != null && s.forDuration > 0 &&
            kotlin.math.abs(s.forDuration - duration) > DURATION_TOLERANCE
        ) continue
        val kind = when (s.kind) {
            "intro" -> SkipKind.INTRO
            "outro" -> SkipKind.OUTRO
            "recap" -> SkipKind.RECAP
            "preview" -> SkipKind.PREVIEW
            else -> if (s.start < duration / 2) SkipKind.INTRO else SkipKind.OUTRO
        }
        // Титры, кончающиеся у самого конца, — до конца: иначе после пропуска
        // оставалась бы секунда чёрного кадра перед «дальше».
        var end = minOf(s.end, duration)
        if (kind == SkipKind.OUTRO && duration - end < 5) end = duration
        if (end - s.start < MIN_LENGTH) continue
        (if (s.forDuration != null) foreign else exact).add(Skip(kind, s.start, end))
    }
    val kinds = exact.map { it.kind }.toSet()
    val all = (exact + foreign.filter { it.kind !in kinds }).sortedBy { it.start }
    // Перекрытия: из двух налезающих остаётся первый.
    val out = mutableListOf<Skip>()
    for (s in all) {
        if (out.isNotEmpty() && s.start < out.last().end) continue
        out.add(s)
    }
    return out
}

/** Отрезок, внутри которого сейчас позиция (секунды). */
fun activeSkip(skips: List<Skip>, pos: Double): Skip? =
    skips.firstOrNull { pos >= it.start - 0.25 && pos < it.end - 1 }
