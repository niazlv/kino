package ru.sorewa.kino.player

import java.util.concurrent.ConcurrentHashMap

/**
 * Озвучка так, как её узнаёт человек.
 *
 * Правило склейки живёт на сервере (provider.VoiceKey): разбор подписи и
 * псевдонимы студий, которые ведёт админка. Ключи дорожек потока сервер
 * отдаёт сам ([learn], POST /voices/keys); здесь — тот же разбор без
 * псевдонимов на время, пока ответа нет или нет сети. Порт
 * web/apps/web/src/voices.ts.
 */
object Voice {

    private val LANGS = mapOf(
        "rus" to "Русская дорожка", "ru" to "Русская дорожка",
        "eng" to "Английская дорожка", "en" to "Английская дорожка",
        "jpn" to "Японская дорожка", "ja" to "Японская дорожка",
        "ukr" to "Украинская дорожка", "uk" to "Украинская дорожка",
        "kor" to "Корейская дорожка",
        "chi" to "Китайская дорожка", "zho" to "Китайская дорожка",
    )

    // Вид дорожки у kinopub: «Одноголосый», «Многоголосый закадровый»…
    // Префикс отрезается, только если он вид, — «А. Гаврилов» остаётся целым.
    private val KINDS = listOf("голос", "дубл", "закадр", "автор", "профес", "любит", "оригин")

    private fun isKeyChar(c: Char) = c in 'a'..'z' || c in 'а'..'я' || c in '0'..'9'

    /** Написание без регистра, «ё», пробелов, точек и хвоста «TV». */
    private fun squash(s: String): String {
        val k = s.lowercase().replace("ё", "е").filter(::isKeyChar)
        return if (k.endsWith("tv")) k.dropLast(2) else k
    }

    // Вид озвучки без студии — по словам, как provider.parseKind на сервере.
    // Старшинство по номеру: дубляж > авторский > многоголосый >
    // двухголосый > одноголосый > оригинал. Ключ — как пишет kinopub.
    private const val NONE = 0
    private const val ORIGINAL = 1
    private const val ONE = 2
    private const val TWO = 3
    private const val MULTI = 4
    private const val AUTHOR = 5
    private const val DUB = 6
    private val KIND_NAMES = mapOf(
        DUB to ("dub" to "Дубляж"),
        AUTHOR to ("avo" to "Авторский"),
        MULTI to ("mvo" to "Многоголосый"),
        TWO to ("dvo" to "Двухголосый"),
        ONE to ("vo" to "Одноголосый"),
        ORIGINAL to ("оригинал" to "Оригинал"),
    )
    private val FILLER_WORDS = setOf("проф", "pro", "voice", "over", "voiceover", "track")
    private val FILLER_ROOTS = listOf("закадр", "профес", "полн", "официал", "лиценз", "перевод", "озвуч", "дорожк", "русск")

    /** Что слово значит для вида: вид, -1 — «любительский», -2 — служебное, NONE — чужое. */
    private fun kindWord(w: String): Int {
        fun has(vararg roots: String) = roots.any { w.startsWith(it) }
        return when {
            w == "dub" || w == "dubbing" || w == "dubbed" || has("дубл") -> DUB
            w == "avo" || has("автор") -> AUTHOR
            w == "mvo" || has("многогол") -> MULTI
            w == "dvo" || has("двухгол", "двугол") -> TWO
            w == "vo" || has("одногол") -> ONE
            w == "orig" || w == "original" || has("оригин") -> ORIGINAL
            w == "amateur" || has("любит") -> -1
            w in FILLER_WORDS || FILLER_ROOTS.any { w.startsWith(it) } -> -2
            else -> NONE
        }
    }

    /** Вид без студии: ключ и подпись; null — в подписи есть чужое слово. */
    private fun parseKind(s: String): Pair<String, String>? {
        val words = s.lowercase().replace("ё", "е").split(Regex("[^a-z0-9а-я]+")).filter { it.isNotEmpty() }
        if (words.isEmpty()) return null
        var best = NONE
        var amateur = false
        for (w in words) {
            when (val k = kindWord(w)) {
                -1 -> amateur = true
                -2 -> {}
                NONE -> return null
                else -> if (k > best) best = k
            }
        }
        if (best == ORIGINAL) return KIND_NAMES.getValue(ORIGINAL)
        if (best == NONE) return if (amateur) "любительский" to "Любительский" else null
        val (key, title) = KIND_NAMES.getValue(best)
        if (amateur) {
            val t = "Любительский ${title.lowercase()}"
            return squash(t) to t
        }
        return key to title
    }

    /** Ключ и подпись по одной подписи, без псевдонимов. */
    private fun parse(raw: String): Pair<String, String> {
        var s = raw.trim().replace(Regex("^\\s*\\d+\\s*\\.\\s*"), "")
        var lang = ""
        val m = Regex("\\s*\\(([A-Za-z]{2,3})\\)\\s*$").find(s)
        if (m != null) {
            lang = m.groupValues[1]
            s = s.substring(0, m.range.first)
        }
        val i = s.lastIndexOf(". ")
        if (i >= 0 && s.substring(i + 2).trim().isNotEmpty() &&
            KINDS.any { w -> s.substring(0, i).lowercase().contains(w) }
        ) {
            s = s.substring(i + 2)
        }
        s = s.trim()
        parseKind(s)?.let { return it }
        if (s.isEmpty() || Regex("^(track|дорожка)\\s*\\d*$", RegexOption.IGNORE_CASE).matches(s)) {
            LANGS[lang.lowercase()]?.let { return squash(it) to it }
        }
        val t = s.ifEmpty { raw.trim() }
        return squash(t) to t
    }

    /** Ответы сервера: подпись → ключ и подпись с псевдонимами студий. */
    private val learned = ConcurrentHashMap<String, Pair<String, String>>()

    /** Запомнить ключи, которые посчитал сервер. */
    fun learn(keys: Map<String, Pair<String, String>>) {
        for ((label, v) in keys) if (v.first.isNotEmpty()) learned[label] = v
    }

    /** Подписи, ключей которых сервер ещё не называл. */
    fun unknown(labels: List<String>): List<String> =
        labels.filter { it.isNotBlank() && !learned.containsKey(it) }.distinct()

    fun title(raw: String): String = learned[raw]?.second ?: parse(raw).second

    /** Одна озвучка под разными написаниями — один ключ. */
    fun key(raw: String): String = learned[raw]?.first ?: parse(raw).first
}
