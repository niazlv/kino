package ru.sorewa.kino.data

import android.content.Context
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.File
import java.util.concurrent.ConcurrentHashMap

/**
 * Карточки скачанных тайтлов для офлайна: без сети страница тайтла должна
 * выглядеть как онлайн — обложка, описание, сезоны, серии с названиями и
 * кадрами. Ответ /titles/<id> сохраняется в files/offline/t<id>.json, пока
 * сеть есть: при скачивании, на странице тайтла и при каждом запуске с сетью
 * (warmUp). Картинки — в [Art].
 *
 * Если карточку сохранить не успели, [detailFor] собирает её из самих
 * скачанных файлов: название, сезоны и серии есть всегда.
 */
class OfflineCatalog(
    context: Context,
    private val api: Api,
    private val library: LocalLibrary,
    private val art: Art,
    private val sync: Sync,
) {
    @Serializable
    data class Saved(
        val detail: TitleDetail,
        /** Студии озвучки, как в блоке «Озвучки» на странице тайтла. */
        val voices: List<String> = emptyList(),
        val savedAt: Long = 0,
        /** Когда последний раз обходили картинки: кадры, которых у сервера нет (502), не дёргать на каждом запуске. */
        val imagesAt: Long = 0,
    )

    /** Как идёт подготовка к офлайну — для строки в «Загрузках». */
    data class Status(
        val running: Boolean = false,
        val done: Int = 0,
        val total: Int = 0,
        /** Последний полный проход без сетевых сбоев (мс), 0 — не было. */
        val okAt: Long = 0,
        val failed: Int = 0,
    )

    private val dir: File by lazy { File(context.filesDir, "offline").apply { mkdirs() } }
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true; explicitNulls = false }
    private val memo = ConcurrentHashMap<Long, Saved>()
    private val missing = ConcurrentHashMap.newKeySet<Long>()
    private val mutex = Mutex()

    private val _status = MutableStateFlow(Status())
    val status: StateFlow<Status> = _status

    private fun file(titleId: Long) = File(dir, "t$titleId.json")

    /** Сохранённая карточка (чтение с диска один раз). */
    fun saved(titleId: Long): Saved? {
        if (titleId <= 0) return null
        memo[titleId]?.let { return it }
        if (titleId in missing) return null
        val s = try {
            json.decodeFromString(Saved.serializer(), file(titleId).readText())
        } catch (_: Exception) {
            null
        }
        if (s != null) memo[titleId] = s else missing.add(titleId)
        return s
    }

    /** Свежий ответ сервера — на диск. Озвучки null — оставить прежние. */
    fun save(titleId: Long, detail: TitleDetail, voices: List<String>? = null) {
        if (titleId <= 0) return
        val prev = saved(titleId)
        val s = Saved(detail, voices ?: prev?.voices.orEmpty(), System.currentTimeMillis(), prev?.imagesAt ?: 0)
        write(titleId, s)
    }

    private fun write(titleId: Long, s: Saved) {
        memo[titleId] = s
        missing.remove(titleId)
        try {
            val tmp = File(dir, "t$titleId.json.tmp")
            tmp.writeText(json.encodeToString(Saved.serializer(), s))
            if (!tmp.renameTo(file(titleId))) {
                file(titleId).delete()
                tmp.renameTo(file(titleId))
            }
        } catch (_: Exception) { }
    }

    /** Есть ли у тайтла хоть одна скачанная серия — тогда карточку стоит держать. */
    fun hasDownloads(titleId: Long): Boolean = library.cached().any { it.titleId == titleId }

    /**
     * Карточка для офлайна: сохранённая, иначе собранная из скачанного.
     * titleId 0 — тайтл, не найденный в каталоге (файлы, положенные руками):
     * собирается по названию папки.
     */
    fun detailFor(titleId: Long, name: String? = null): TitleDetail? {
        if (titleId > 0) saved(titleId)?.let { return it.detail }
        val entries = library.cached().filter {
            if (titleId > 0) it.titleId == titleId else it.titleId == 0L && it.title == name
        }
        if (entries.isEmpty()) return null
        return synthetic(titleId, entries)
    }

    /** Карточка из одних файлов: id серий отрицательные — у сервера их нет. */
    private fun synthetic(titleId: Long, entries: List<LocalLibrary.Entry>): TitleDetail {
        val sorted = entries.sortedWith(compareBy({ it.season }, { it.episode }))
        val movie = sorted.all { it.movie }
        return TitleDetail(
            title = Title(
                id = titleId,
                kind = if (movie) "movie" else "serial",
                title = sorted.first().title,
                hasArt = titleId > 0 && art.posterPath(titleId) != null,
            ),
            episodes = sorted.mapIndexed { i, e ->
                Episode(
                    id = -(i + 1L),
                    seasonNo = e.season,
                    episodeNo = e.episode,
                    durationSec = e.durationMs / 1000,
                )
            },
        )
    }

    /**
     * Карточка и картинки одного тайтла. force — перечитать с сервера, даже
     * если сохранённая свежая. Возвращает false при сетевом сбое.
     */
    suspend fun refreshTitle(titleId: Long, force: Boolean = false): Boolean = withContext(Dispatchers.IO) {
        if (titleId <= 0) return@withContext true
        val prev = saved(titleId)
        val now = System.currentTimeMillis()
        val stale = prev == null || now - prev.savedAt > FRESH_MS
        // Всё свежее и картинки уже обходили — ничего не делаем.
        if (!force && !stale && now - prev!!.imagesAt < FRESH_MS) return@withContext true
        val detail = if (force || stale) {
            try {
                api.title(titleId).also { save(titleId, it) }
            } catch (e: ApiError) {
                if (e.status == 404) return@withContext true // тайтла больше нет — не сбой сети
                prev?.detail ?: return@withContext false
            } catch (_: Exception) {
                prev?.detail ?: return@withContext false
            }
        } else prev!!.detail

        // Постер: у сервера он есть — пробуем, даже если однажды не дался.
        art.ensurePoster(titleId, retryMissing = detail.title.hasArt)

        // Кадры: сперва скачанные серии, потом остальные серии их сезонов —
        // список серий без сети выглядит как онлайн. Потолок — на «Блич».
        val local = library.list().filter { it.titleId == titleId }
        val have = local.map { it.season to it.episode }.toSet()
        val seasons = local.map { it.season }.toSet()
        val eps = detail.episodes.filter { it.hasArt && it.seasonNo in seasons }
        val first = eps.filter { (it.seasonNo to it.episodeNo) in have }
        val rest = eps.filter { (it.seasonNo to it.episodeNo) !in have }
        for (ep in (first + rest).take(MAX_SHOTS)) art.ensureShot(ep.id)
        saved(titleId)?.let { write(titleId, it.copy(imagesAt = System.currentTimeMillis())) }
        true
    }

    /**
     * Всё скачанное — к офлайну: прогресс серверу, связь безымянных файлов с
     * каталогом, кадры из файлов, карточки и картинки. Зовётся при запуске с
     * сетью и на «Загрузках»; второй вызов во время первого ничего не делает.
     */
    suspend fun warmUp(force: Boolean = false) = withContext(Dispatchers.IO) {
        if (!mutex.tryLock()) return@withContext
        try {
            try { sync.syncLocalProgress() } catch (_: Exception) { }
            // Очередь стейта: офлайн-позиции стрима, галочки, метки, озвучка.
            try { sync.syncPendingState() } catch (_: Exception) { }
            try { sync.resolveLocalTitles() } catch (_: Exception) { }
            try { art.refresh(library) } catch (_: Exception) { }
            val ids = library.list().map { it.titleId }.filter { it > 0 }.distinct()
            _status.value = _status.value.copy(running = true, done = 0, total = ids.size, failed = 0)
            var failed = 0
            for ((i, id) in ids.withIndex()) {
                if (!refreshTitle(id, force)) failed++
                _status.value = _status.value.copy(done = i + 1, failed = failed)
            }
            _status.value = _status.value.copy(
                running = false,
                okAt = if (failed == 0) System.currentTimeMillis() else _status.value.okAt,
            )
        } finally {
            if (_status.value.running) _status.value = _status.value.copy(running = false)
            mutex.unlock()
        }
    }

    /** Сколько скачанных тайтлов уже готовы к офлайну (есть карточка). */
    fun readiness(): Pair<Int, Int> {
        val ids = library.cached().map { it.titleId }.filter { it > 0 }.distinct()
        return ids.count { saved(it) != null } to ids.size
    }

    companion object {
        /** Карточка свежее шести часов — сервер не дёргаем, только картинки. */
        private const val FRESH_MS = 6 * 60 * 60 * 1000L
        private const val MAX_SHOTS = 300
    }
}
