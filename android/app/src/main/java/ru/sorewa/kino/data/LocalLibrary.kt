package ru.sorewa.kino.data

import android.content.ContentUris
import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import androidx.documentfile.provider.DocumentFile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.io.File

/**
 * Библиотека скачанного — порт Library.java старой оболочки, но на Kotlin.
 *
 * Записи в одном JSON-файле с атомарной записью: записей десятки, база тут
 * избыточна. Ключ — titleId:season:episode; файлы могут лежать где угодно
 * (SAF-дерево, MediaStore, приватная папка) — uri хранится как есть.
 * Позиция/просмотренность переживают перекачку в другом качестве.
 */
class LocalLibrary(private val context: Context) {

    @Serializable
    data class Entry(
        val id: String,
        var titleId: Long = 0,
        val title: String = "",
        val season: Int = 0,
        val episode: Int = 0,
        val uri: String,
        val size: Long = 0,
        val addedAt: Long = 0,
        var positionMs: Long = 0,
        var durationMs: Long = 0,
        var watched: Boolean = false,
        var dirty: Boolean = false,
        /** Фильм, а не серия: у утилиты и фильм назван S01E01. */
        val movie: Boolean = false,
        /** Когда последний раз играла (мс) — «Продолжить» в загрузках. */
        var playedAt: Long = 0,
        // Транзитные адреса картинок (не хранятся): кадр и постер.
        var frame: String? = null,
        var poster: String? = null,
    )

    private val json = STORE_JSON
    private val file: File get() = File(context.filesDir, "library.json")
    private val lock = Any()
    private var cache: MutableList<Entry>? = null

    private fun load(): MutableList<Entry> {
        cache?.let { return it }
        val list = try {
            // Дубли одного id (гонка подбора в прежних версиях) — схлопываем.
            decodeStore(file.readText()).distinctBy { it.id }.toMutableList()
        } catch (_: Exception) {
            // Не разобрали — первая же запись затёрла бы опись, а с ней всё
            // скачанное (в том числе перешедшее от старой оболочки). Копия
            // остаётся рядом, чтобы было что чинить.
            try {
                if (file.isFile && file.length() > 2) {
                    file.copyTo(File(file.parentFile, "library.json.unreadable"), overwrite = true)
                }
            } catch (_: Exception) { }
            mutableListOf()
        }
        cache = list
        return list
    }

    private fun saveLocked(list: MutableList<Entry>) {
        val tmp = File(file.parentFile, file.name + ".tmp")
        tmp.writeText(json.encodeToString(list))
        if (!tmp.renameTo(file)) {
            file.delete()
            tmp.renameTo(file)
        }
        _changes.value = _changes.value + 1
    }

    /**
     * Счётчик записей описи: экраны перечитывают скачанное, когда он
     * сдвинулся. Плеер дописывает позицию в onStop — уже после onResume
     * экрана под ним, перечитывание по onResume её не видело.
     */
    private val _changes = kotlinx.coroutines.flow.MutableStateFlow(0L)
    val changes: kotlinx.coroutines.flow.StateFlow<Long> = _changes

    private fun keyOf(titleId: Long, season: Int, episode: Int) = "$titleId:$season:$episode"

    // ---- запись ----

    /** Новая скачанная серия. Прежние позиция/просмотренность сохраняются. */
    fun put(titleId: Long, title: String, season: Int, episode: Int, uri: Uri, size: Long, movie: Boolean = false) {
        synchronized(lock) {
            val list = load()
            val id = keyOf(titleId, season, episode)
            val old = list.find { it.id == id }
            val entry = Entry(
                id = id, titleId = titleId, title = title, season = season, episode = episode,
                uri = uri.toString(), size = size, addedAt = System.currentTimeMillis(),
                positionMs = old?.positionMs ?: 0, durationMs = old?.durationMs ?: 0,
                watched = old?.watched ?: false, dirty = old?.dirty ?: false,
                movie = movie || old?.movie == true, playedAt = old?.playedAt ?: 0,
            )
            list.removeAll { it.id == id }
            list.add(entry)
            saveLocked(list)
        }
    }

    fun progress(id: String, positionMs: Long, durationMs: Long, watched: Boolean) {
        synchronized(lock) {
            val list = load()
            val e = list.find { it.id == id } ?: return
            e.positionMs = positionMs.coerceAtLeast(0)
            if (durationMs > 0) e.durationMs = durationMs
            e.watched = e.watched || watched
            e.dirty = true
            e.playedAt = System.currentTimeMillis()
            saveLocked(list)
        }
    }

    fun setWatched(id: String, watched: Boolean) {
        synchronized(lock) {
            val list = load()
            val e = list.find { it.id == id } ?: return
            e.watched = watched
            e.dirty = true
            saveLocked(list)
        }
    }

    /** Прогресс доехал до сервера — не слать его дважды. */
    fun synced(id: String) {
        synchronized(lock) {
            val list = load()
            val e = list.find { it.id == id } ?: return
            e.dirty = false
            saveLocked(list)
        }
    }

    /** Скачанный файл связывается с тайтлом каталога (adopt-записи имеют 0). */
    fun setTitleId(id: String, titleId: Long) {
        synchronized(lock) {
            val list = load()
            val e = list.find { it.id == id } ?: return
            if (e.titleId == 0L && titleId > 0) {
                e.titleId = titleId
                saveLocked(list)
            }
        }
    }

    fun setDuration(id: String, ms: Long) {
        if (ms <= 0) return
        synchronized(lock) {
            val list = load()
            val e = list.find { it.id == id } ?: return
            if (e.durationMs == 0L) {
                e.durationMs = ms
                saveLocked(list)
            }
        }
    }

    /** Сначала удаляется файл; запись стирается, только если файл ушёл. */
    fun remove(id: String): Boolean {
        synchronized(lock) {
            val list = load()
            val e = list.find { it.id == id } ?: return false
            val ok = deleteFile(e.uri)
            if (ok) {
                list.removeAll { it.id == id }
                saveLocked(list)
            }
            return ok
        }
    }

    fun wipeWatched(): Pair<Int, Long> {
        synchronized(lock) {
            val list = load()
            val watched = list.filter { it.watched }
            var removed = 0
            var freed = 0L
            for (e in watched) {
                if (deleteFile(e.uri)) {
                    freed += e.size
                    list.removeAll { it.id == e.id }
                    removed++
                }
            }
            if (removed > 0) saveLocked(list)
            return removed to freed
        }
    }

    private fun deleteFile(uriString: String): Boolean = try {
        val uri = Uri.parse(uriString)
        when (uri.scheme) {
            null, "file" -> File(uri.path ?: return false).delete()
            "content" -> if (uri.host == "media") {
                context.contentResolver.delete(uri, null, null) > 0
            } else {
                DocumentFile.fromSingleUri(context, uri)?.delete() == true
            }
            else -> false
        }
    } catch (_: Exception) {
        false
    }

    // ---- чтение ----

    /** Только живые записи: файл мог стереть любой файловый менеджер. */
    fun list(): List<Entry> {
        synchronized(lock) {
            val list = load()
            val dead = list.filter { !fileExists(it.uri) }
            if (dead.isNotEmpty()) {
                list.removeAll { it.id in dead.map { d -> d.id } }
                saveLocked(list)
            }
            return list.toList()
        }
    }

    /** Список с адресами картинок (кадр/постер) — для витрины. */
    fun listWithArt(art: Art?): List<Entry> {
        val items = list()
        if (art == null) return items
        for (e in items) {
            e.frame = art.framePath(e.uri)?.let { it.toString() }
            e.poster = if (e.titleId > 0) art.posterPath(e.titleId)?.let { it.toString() } else null
        }
        return items
    }

    fun find(id: String): Entry? = list().find { it.id == id }

    /**
     * Опись без проверки файлов — для экранов (главный поток): проверка
     * существования ходит в MediaStore по каждой записи. Пропавшие файлы
     * вычистит ближайший [list].
     */
    fun cached(): List<Entry> = synchronized(lock) { load().map { it.copy() } }

    /** Записи одного тайтла по seasonNo/episodeNo — для «Серий» в плеере. */
    fun episodesOfTitle(titleId: Long): List<Entry> =
        list().filter { it.titleId == titleId }.sortedWith(compareBy({ it.season }, { it.episode }))

    fun bytes(): Long = list().sumOf { it.size }

    fun dirtyEntries(): List<Entry> = list().filter { it.dirty && it.titleId > 0 }

    // ---- существование файла ----

    private fun fileExists(uriString: String): Boolean = try {
        val uri = Uri.parse(uriString)
        when (uri.scheme) {
            null, "file" -> File(uri.path ?: return false).exists()
            "content" -> if (uri.host == "media") {
                context.contentResolver.query(
                    uri, arrayOf(MediaStore.MediaColumns._ID), null, null, null,
                )?.use { it.count > 0 } ?: false
            } else {
                DocumentFile.fromSingleUri(context, uri)?.exists() == true
            }
            else -> false
        }
    } catch (_: Exception) {
        false
    }

    // ---- adopt: собрать файлы, которых нет в инвентаре ----

    /**
     * Урожай «чужих» файлов: скачанных вручную, перенесённых, оставшихся от
     * старой оболочки. Смотрит Movies/kino/ в MediaStore и один уровень
     * SAF-дерева (<дерево>/<Тайтл>/SxxEyy.mkv). Возвращает число добавленных.
     */
    suspend fun adopt(treeUri: Uri?): Int = adoptMutex.withLock { adoptLocked(treeUri) }

    /**
     * Подбор зовут и офлайн-корень, и «Загрузки» — одновременно: без замка
     * оба видели файл новым и добавляли его дважды («2 серии» из одной).
     */
    private val adoptMutex = kotlinx.coroutines.sync.Mutex()

    private suspend fun adoptLocked(treeUri: Uri?): Int = withContext(Dispatchers.IO) {
        val known = list()
        val seenUris = known.map { it.uri }.toMutableSet()
        val signs = known.map { sign(it.title, it.season, it.episode, it.size) }.toMutableSet()
        var added = 0

        fun consider(uri: Uri, title: String, season: Int, episode: Int, size: Long, durationMs: Long) {
            if (season <= 0 && episode <= 0) return
            val uriKey = uri.toString()
            if (uriKey in seenUris) return
            val sign = sign(title, season, episode, size)
            if (sign in signs) return
            seenUris.add(uriKey)
            signs.add(sign)
            synchronized(lock) {
                val list = load()
                // id по uri — adopted-записи не конфликтуют с ключами качалки.
                val id = "f" + Integer.toHexString(uriKey.hashCode()) + ":" + season + ":" + episode
                if (list.any { it.id == id || it.uri == uriKey }) return
                list.add(
                    Entry(
                        id = id, titleId = 0, title = title, season = season, episode = episode,
                        uri = uriKey, size = size, addedAt = System.currentTimeMillis(),
                        durationMs = durationMs,
                    )
                )
                saveLocked(list)
            }
            added++
        }

        // MediaStore: Movies/kino/<Тайтл>/SxxEyy.mkv
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            try {
                val projection = arrayOf(
                    MediaStore.Video.Media._ID,
                    MediaStore.Video.Media.DISPLAY_NAME,
                    MediaStore.Video.Media.RELATIVE_PATH,
                    MediaStore.Video.Media.SIZE,
                    MediaStore.Video.Media.DURATION,
                )
                context.contentResolver.query(
                    MediaStore.Video.Media.EXTERNAL_CONTENT_URI,
                    projection,
                    "${MediaStore.Video.Media.RELATIVE_PATH} LIKE ?",
                    arrayOf("$MOVIES_ROOT%"),
                    null,
                )?.use { c ->
                    val iId = c.getColumnIndexOrThrow(MediaStore.Video.Media._ID)
                    val iName = c.getColumnIndexOrThrow(MediaStore.Video.Media.DISPLAY_NAME)
                    val iPath = c.getColumnIndexOrThrow(MediaStore.Video.Media.RELATIVE_PATH)
                    val iSize = c.getColumnIndexOrThrow(MediaStore.Video.Media.SIZE)
                    val iDur = c.getColumnIndexOrThrow(MediaStore.Video.Media.DURATION)
                    while (c.moveToNext()) {
                        val name = c.getString(iName) ?: continue
                        val se = seasonEpisode(name) ?: continue
                        val path = c.getString(iPath) ?: continue
                        val folder = path.removePrefix("$MOVIES_ROOT/").substringBefore("/").trim()
                        if (folder.isEmpty()) continue
                        val title = unescapeFolder(folder)
                        val uri = ContentUris.withAppendedId(MediaStore.Video.Media.EXTERNAL_CONTENT_URI, c.getLong(iId))
                        consider(uri, title, se[0].toInt(), se[1].toInt(), c.getLong(iSize), c.getLong(iDur))
                    }
                }
            } catch (_: Exception) {
                // Нет доступа к MediaStore — пропускаем.
            }
        }

        // SAF-дерево: один уровень — <дерево>/<Тайтл>/SxxEyy.mkv
        if (treeUri != null) {
            try {
                val root = DocumentFile.fromTreeUri(context, treeUri)
                for (dir in root?.listFiles().orEmpty()) {
                    if (!dir.isDirectory) continue
                    val title = unescapeFolder(dir.name ?: continue)
                    for (f in dir.listFiles()) {
                        if (f.isDirectory) continue
                        val name = f.name ?: continue
                        val se = seasonEpisode(name) ?: continue
                        if (!name.endsWith(".mkv", true) && !name.endsWith(".mp4", true)) continue
                        consider(f.uri, title, se[0].toInt(), se[1].toInt(), f.length(), 0)
                    }
                }
            } catch (_: Exception) {
                // Дерево отозвано — пропускаем.
            }
        }
        added
    }

    companion object {
        private const val MOVIES_ROOT = "Movies/kino"

        private val STORE_JSON = Json { ignoreUnknownKeys = true; encodeDefaults = true }

        /**
         * Разбор library.json. Тот же файл писала старая оболочка
         * (Library.java, org.json): опись скачанного переживает обновление
         * без переноса, поэтому формат обязан читаться как есть.
         */
        internal fun decodeStore(text: String): List<Entry> = STORE_JSON.decodeFromString(text)

        /** S01E05 из имени файла; null — не серия. */
        fun seasonEpisode(name: String): List<Int>? {
            val m = Regex("(?i)S(\\d{1,3})E(\\d{1,4})").find(name) ?: return null
            return listOf(m.groupValues[1].toInt(), m.groupValues[2].toInt())
        }

        /** titleId из ссылки страницы: .../#/title/53014/s2e3 → 53014. */
        fun titleIdOf(pageUrl: String): Long =
            Regex("/title/(\\d+)").find(pageUrl)?.groupValues?.get(1)?.toLongOrNull() ?: 0

        /** Отпечаток файла: одна и та же серия под разными uri — одна запись. */
        fun sign(title: String, season: Int, episode: Int, size: Long): String =
            title.lowercase().filter { it.isLetterOrDigit() } + "|$season|$episode|$size"

        /** safe() наоборот: «Тайтл   /   Сезон» из папки с пробелами вместо «/». */
        fun unescapeFolder(folder: String): String =
            folder.replace(Regex("\\s{2,}"), " / ").trim()

        /** Имя папки из названия тайтла — как safe() в старой оболочке. */
        fun safeFolder(title: String): String =
            title.replace(Regex("[\\\\/:*?\"<>|]+"), " ").trim().ifEmpty { "kino" }

        /** Имя файла серии: S02E05.mkv. */
        fun episodeFileName(season: Int, episode: Int, ext: String): String =
            "S%02dE%02d.%s".format(season, episode, ext)
    }
}
