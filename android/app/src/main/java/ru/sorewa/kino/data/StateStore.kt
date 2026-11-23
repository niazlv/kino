package ru.sorewa.kino.data

import android.content.Context
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.io.File

/**
 * Ждущий и кэшированный пользовательский стейт — источник истины до
 * подтверждения сервера.
 *
 * Мутации (позиция, «просмотрено», метки, озвучка/площадка) сначала встают
 * в очередь этого файла, потом best-effort уходят на сервер: сорвавшийся
 * PUT больше не теряется молча — сливает Sync.syncPendingState при первой
 * сети. Сервер сам решает конфликты: PUT /progress несёт deviceId+client_ts
 * (тот же девайс — «кто последний», разные — «кто дальше досмотрел»), метки
 * — полный replace, prefs — частичный патч.
 *
 * Рядом кэш чтений: метки тайтлов и «Продолжить», чтобы офлайн-страница
 * тайтла имела переключатели, а дом — список продолжения.
 *
 * Файл один, записей десятки — как в LocalLibrary, база тут избыточна.
 */

/** Ждущее отправки состояние одной серии. */
@Serializable
data class PendingEp(
    val episodeId: Long,
    val positionMs: Long = 0,
    val durationMs: Long = 0,
    /** null — флаг не меняли; true/false — ждёт отправки. */
    val watched: Boolean? = null,
    /** Момент мутации на этом устройстве; с ним же уезжает на сервер. */
    val clientTs: String = "",
)

/** Ждущее изменение меток: поле null — не меняли. Снимка в очереди нет
    нарочно: PUT меток заменяет целиком, и снимок из кэша затёр бы метки,
    поставленные с другого устройства, пока мы были офлайн. */
@Serializable
data class MarksPatch(
    val following: Boolean? = null,
    val watchlist: Boolean? = null,
    val hidden: Boolean? = null,
)

/** Ждущее отправки состояние тайтла: изменение меток и/или выбор озвучки. */
@Serializable
data class PendingTitle(
    val titleId: Long,
    val marksPatch: MarksPatch? = null,
    val marksTs: String = "",
    val prefs: PrefsPatch? = null,
    val prefsTs: String = "",
)

@Serializable
internal data class CachedMarks(val titleId: Long, val marks: Marks, val at: Long = 0)

@Serializable
internal data class StateFile(
    val episodes: List<PendingEp> = emptyList(),
    val titles: List<PendingTitle> = emptyList(),
    val marksCache: List<CachedMarks> = emptyList(),
    /** Последняя удачная страница «Продолжить» — для офлайн-дома. */
    val continueCache: List<ContinueItem> = emptyList(),
)

internal fun isoNow(): String = java.time.Instant.now().toString()

/**
 * Позиция: последний пишущий побеждает — ждущая запись заменяется целиком.
 * Хронометраж, если новый репорт его не знал, сохраняется прежний.
 */
internal fun mergeProgress(
    episodeId: Long,
    existing: PendingEp?,
    positionMs: Long,
    durationMs: Long,
    clientTs: String,
): PendingEp = PendingEp(
    episodeId = episodeId,
    positionMs = positionMs.coerceAtLeast(0),
    durationMs = when {
        durationMs > 0 -> durationMs
        existing != null -> existing.durationMs
        else -> 0
    },
    watched = existing?.watched,
    clientTs = clientTs,
)

/**
 * Галочка: «смотрел» ставит позицию в полный хронометраж — как серверная
 * (серия доложена до конца); «не смотрел» обнуляет её — сервер удаляет
 * строку, и воскрешать старую позицию после галочки нельзя.
 */
internal fun mergeWatched(
    episodeId: Long,
    existing: PendingEp?,
    watched: Boolean,
    durationMs: Long,
    clientTs: String,
): PendingEp = PendingEp(
    episodeId = episodeId,
    positionMs = when {
        !watched -> 0
        durationMs > 0 -> durationMs
        existing != null -> existing.positionMs
        else -> 0
    },
    durationMs = when {
        durationMs > 0 -> durationMs
        existing != null -> existing.durationMs
        else -> 0
    },
    watched = watched,
    clientTs = clientTs,
)

/** Патчи озвучки сливаются: новое непустое поле перекрывает, null не трогает. */
internal fun mergePrefs(existing: PrefsPatch?, patch: PrefsPatch): PrefsPatch = PrefsPatch(
    audio = patch.audio ?: existing?.audio,
    subtitle = patch.subtitle ?: existing?.subtitle,
    source = patch.source ?: existing?.source,
)

/** Prefs тайтла с ждущим патчем — для показа на странице и старта плеера. */
internal fun applyPrefsPatch(prefs: Prefs?, patch: PrefsPatch): Prefs {
    val merged = mergePrefs(prefs?.let { PrefsPatch(it.audio, it.subtitle, it.source) }, patch)
    return Prefs(merged.audio, merged.subtitle, merged.source)
}

/**
 * Ждущий стейт поверх серий сервера или сохранённой карточки: пока PUT не
 * подтверждён, страница показывает своё — как со скачанным (withLocal).
 */
internal fun overlayEpisodes(detail: TitleDetail, pending: List<PendingEp>): TitleDetail {
    if (pending.isEmpty()) return detail
    val byId = pending.associateBy { it.episodeId }
    var changed = false
    val eps = detail.episodes.map { ep ->
        val p = byId[ep.id] ?: return@map ep
        changed = true
        ep.copy(
            positionMs = if (p.watched == false) 0 else p.positionMs,
            status = when {
                p.watched == true -> "completed"
                p.watched == false -> ""
                p.durationMs > 0 && p.positionMs >= p.durationMs * 9 / 10 -> "completed"
                p.positionMs > 0 -> "in_progress"
                else -> ""
            },
        )
    }
    return if (changed) detail.copy(episodes = eps) else detail
}

/** Разница меток: только поля, что человек переключил сейчас. */
internal fun marksDiff(prev: Marks, next: Marks): MarksPatch = MarksPatch(
    following = if (next.following != prev.following) next.following else null,
    watchlist = if (next.watchlist != prev.watchlist) next.watchlist else null,
    hidden = if (next.hidden != prev.hidden) next.hidden else null,
)

/** Своё поле поверх базовых меток — для отправки и для показа. */
internal fun applyMarksPatch(base: Marks, patch: MarksPatch): Marks = Marks(
    following = patch.following ?: base.following,
    watchlist = patch.watchlist ?: base.watchlist,
    hidden = patch.hidden ?: base.hidden,
)

internal fun hasMarksPatch(patch: MarksPatch?): Boolean =
    patch != null && (patch.following != null || patch.watchlist != null || patch.hidden != null)

/** Метки для показа: база — сервер или кэш, ждущий патч ложится сверху.
    Ни сервера, ни кэша — показать нечего (переключатели скрыты). */
internal fun resolveMarks(server: Marks?, cached: Marks?, pending: PendingTitle?): Marks? {
    val base = server ?: cached ?: return null
    val patch = pending?.marksPatch ?: return base
    return applyMarksPatch(base, patch)
}

/**
 * «Продолжить» из кэша с ждущими позициями поверх — для офлайн-дома.
 * Досмотренное до конца в очереди выпадает: продолжать там нечего, как и
 * на сервере.
 */
internal fun overlayContinue(items: List<ContinueItem>, pending: List<PendingEp>): List<ContinueItem> {
    if (items.isEmpty() || pending.isEmpty()) return items
    val byId = pending.associateBy { it.episodeId }
    return items.mapNotNull { c ->
        val p = byId[c.episodeId] ?: return@mapNotNull c
        val done = p.watched == true ||
            (p.durationMs > 0 && p.positionMs >= p.durationMs * 9 / 10)
        if (done) return@mapNotNull null
        c.copy(
            positionMs = if (p.watched == false) 0 else p.positionMs,
            durationMs = if (p.durationMs > 0) p.durationMs else c.durationMs,
        )
    }
}

class StateStore(private val context: Context) {

    private val json = STORE_JSON
    private val file: File get() = File(context.filesDir, "state.json")
    private val lock = Any()
    private var cache: StateFile? = null

    private val _changes = MutableStateFlow(0L)

    /** Счётчик изменений: экраны перечитывают ждущее/кэш, когда он сдвинулся. */
    val changes: StateFlow<Long> = _changes

    private fun load(): StateFile {
        cache?.let { return it }
        cache = try {
            decodeState(file.readText())
        } catch (_: Exception) {
            // Не разобрали — первая же запись затёрла бы очередь; копия рядом,
            // чтобы было что чинить (как в LocalLibrary).
            try {
                if (file.isFile && file.length() > 2) {
                    file.copyTo(File(file.parentFile, "state.json.unreadable"), overwrite = true)
                }
            } catch (_: Exception) { }
            StateFile()
        }
        return cache!!
    }

    private fun saveLocked(next: StateFile) {
        val tmp = File(file.parentFile, file.name + ".tmp")
        tmp.writeText(json.encodeToString(next))
        if (!tmp.renameTo(file)) {
            file.delete()
            tmp.renameTo(file)
        }
        cache = next
        _changes.value = _changes.value + 1
    }

    private fun update(block: (StateFile) -> StateFile) {
        synchronized(lock) { saveLocked(block(load())) }
    }

    // ---- очередь мутаций ----

    /** Позиция стрима: встаёт в очередь до похода в сеть. */
    fun queueProgress(episodeId: Long, positionMs: Long, durationMs: Long, clientTs: String = isoNow()) {
        if (episodeId <= 0) return
        update { f ->
            val cur = f.episodes.find { it.episodeId == episodeId }
            val next = mergeProgress(episodeId, cur, positionMs.coerceAtLeast(0), durationMs, clientTs)
            f.copy(episodes = replaced(f.episodes, next) { it.episodeId == episodeId })
        }
    }

    /** «Просмотрено»: тоже в очередь до похода в сеть. */
    fun queueWatched(episodeId: Long, watched: Boolean, durationMs: Long = 0, clientTs: String = isoNow()) {
        if (episodeId <= 0) return
        update { f ->
            val cur = f.episodes.find { it.episodeId == episodeId }
            val next = mergeWatched(episodeId, cur, watched, durationMs, clientTs)
            f.copy(episodes = replaced(f.episodes, next) { it.episodeId == episodeId })
        }
    }

    /** Метки: ждут изменённые поля, не снимок — при отправке сперва GET
        серверных меток, своё поле ложится сверху (PUT заменяет целиком). */
    fun queueMarks(titleId: Long, patch: MarksPatch, clientTs: String = isoNow()) {
        if (titleId <= 0) return
        if (!hasMarksPatch(patch)) return
        update { f ->
            val cur = f.titles.find { it.titleId == titleId } ?: PendingTitle(titleId)
            val merged = MarksPatch(
                following = patch.following ?: cur.marksPatch?.following,
                watchlist = patch.watchlist ?: cur.marksPatch?.watchlist,
                hidden = patch.hidden ?: cur.marksPatch?.hidden,
            )
            val next = cur.copy(marksPatch = merged, marksTs = clientTs)
            f.copy(titles = replaced(f.titles, next) { it.titleId == titleId })
        }
    }

    /** Озвучка/площадка/субтитры: патчи сливаются, null-поле не перекрывает. */
    fun queuePrefs(titleId: Long, patch: PrefsPatch, clientTs: String = isoNow()) {
        if (titleId <= 0) return
        update { f ->
            val cur = f.titles.find { it.titleId == titleId } ?: PendingTitle(titleId)
            val next = cur.copy(prefs = mergePrefs(cur.prefs, patch), prefsTs = clientTs)
            f.copy(titles = replaced(f.titles, next) { it.titleId == titleId })
        }
    }

    /**
     * PUT удался: ждущее с тем же ts уехало — вычистить. Записи, вставшие в
     * очередь свежее, пока запрос летел (тик плеера раз в 15 с), не трогаем.
     */
    fun ackEpisode(episodeId: Long, clientTs: String) {
        if (episodeId <= 0) return
        update { f ->
            f.copy(episodes = f.episodes.filterNot { it.episodeId == episodeId && it.clientTs == clientTs })
        }
    }

    fun ackMarks(titleId: Long, clientTs: String) {
        if (titleId <= 0) return
        update { f ->
            val cur = f.titles.find { it.titleId == titleId } ?: return@update f
            if (cur.marksTs != clientTs) return@update f
            val next = cur.copy(marksPatch = null, marksTs = "")
            f.copy(titles = dropTitleIfEmpty(f.titles, next))
        }
    }

    fun ackPrefs(titleId: Long, clientTs: String) {
        if (titleId <= 0) return
        update { f ->
            val cur = f.titles.find { it.titleId == titleId } ?: return@update f
            if (cur.prefsTs != clientTs) return@update f
            val next = cur.copy(prefs = null, prefsTs = "")
            f.copy(titles = dropTitleIfEmpty(f.titles, next))
        }
    }

    // ---- чтение очереди ----

    fun pendingEpisode(episodeId: Long): PendingEp? =
        synchronized(lock) { load().episodes.find { it.episodeId == episodeId } }

    fun pendingTitle(titleId: Long): PendingTitle? =
        synchronized(lock) { load().titles.find { it.titleId == titleId } }

    fun pendingEpisodes(): List<PendingEp> =
        synchronized(lock) { load().episodes.toList() }

    fun pendingTitles(): List<PendingTitle> =
        synchronized(lock) { load().titles.toList() }

    // ---- кэш чтений ----

    fun cacheMarks(titleId: Long, marks: Marks) {
        if (titleId <= 0) return
        update { f ->
            val rest = f.marksCache.filter { it.titleId != titleId }
            f.copy(marksCache = (rest + CachedMarks(titleId, marks, System.currentTimeMillis())).takeLast(CACHE_MARKS))
        }
    }

    fun cachedMarks(titleId: Long): Marks? =
        synchronized(lock) { load().marksCache.find { it.titleId == titleId }?.marks }

    /** Страница 0 «Продолжить» — сохраняется при удачной загрузке дома. */
    fun cacheContinue(items: List<ContinueItem>) {
        update { f -> f.copy(continueCache = items.take(CACHE_CONTINUE)) }
    }

    fun cachedContinue(): List<ContinueItem> =
        synchronized(lock) { load().continueCache.toList() }

    private fun <T> replaced(list: List<T>, next: T, same: (T) -> Boolean): List<T> {
        val idx = list.indexOfFirst(same)
        if (idx < 0) return list + next
        return list.toMutableList().also { it[idx] = next }
    }

    private fun dropTitleIfEmpty(list: List<PendingTitle>, next: PendingTitle): List<PendingTitle> =
        if (next.marksPatch == null && next.prefs == null) {
            list.filter { it.titleId != next.titleId }
        } else {
            replaced(list, next) { it.titleId == next.titleId }
        }

    companion object {
        private val STORE_JSON = Json { ignoreUnknownKeys = true; encodeDefaults = true }
        private const val CACHE_MARKS = 200
        private const val CACHE_CONTINUE = 60

        internal fun decodeState(text: String): StateFile = STORE_JSON.decodeFromString(text)
    }
}
