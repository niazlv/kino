package ru.sorewa.kino.downloader

import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.provider.MediaStore
import androidx.documentfile.provider.DocumentFile
import ru.sorewa.kino.data.LocalLibrary
import java.io.File

/**
 * Перенос готовых файлов из рабочей папки качалки туда, где их увидят:
 * SAF-папка на выбор или Movies/kino/<Тайтл>/ через MediaStore (с
 * IS_PENDING — недокопированное не видно другим приложениям).
 * Порт export()/newFiles() из DownloadService.java.
 */
object Exporter {

    private val VIDEO_EXT = setOf("mkv", "mp4")
    private const val MOVIES_ROOT = "Movies/kino"

    /** Файлы, появившиеся за время работы утилиты (запас 60 с на часы). */
    fun newFiles(work: File, started: Long): List<File> {
        val out = mutableListOf<File>()
        work.walkTopDown().forEach { f ->
            if (f.isFile && f.lastModified() >= started - 60_000) {
                val ext = f.extension.lowercase()
                if (ext in VIDEO_EXT || ext == "srt") out.add(f)
            }
        }
        return out
    }

    /**
     * Готовая серия в рабочей папке. Источник правды — состояние утилиты
     * (`<серия>/.kinopub-state.json`): серия попадает туда, только когда
     * сшита целиком, с путём к файлу, названием и ссылкой на тайтл. Так
     * готовое забирается при любом исходе прогона — и то, что осталось от
     * прошлого, оборвавшегося: сбой одной серии не теряет остальные.
     */
    data class Ready(
        val file: File,
        val subs: List<File>,
        val title: String,
        val pageUrl: String,
        val season: Int,
        val episode: Int,
    )

    private val stateJson = kotlinx.serialization.json.Json { ignoreUnknownKeys = true }

    @kotlinx.serialization.Serializable
    private data class UtilState(
        val series: String = "",
        val metadata: UtilMeta? = null,
        val completed: Map<String, UtilRec> = emptyMap(),
    )

    @kotlinx.serialization.Serializable
    private data class UtilMeta(
        val title: String = "",
        @kotlinx.serialization.SerialName("input_url") val inputUrl: String = "",
    )

    @kotlinx.serialization.Serializable
    private data class UtilRec(val season: Int = 0, val episode: Int = 0, val path: String = "")

    /** Файлы состояния утилиты: <work>/<серия>/.kinopub-state.json. */
    fun stateFiles(work: File): List<File> =
        work.listFiles().orEmpty().filter { it.isDirectory }
            .map { File(it, ".kinopub-state.json") }
            .filter { it.isFile }

    /** id тайтла, к которому относится состояние утилиты ("" — не разобрали). */
    fun seriesOf(stateFile: File): String = try {
        stateJson.decodeFromString(UtilState.serializer(), stateFile.readText()).series
    } catch (_: Exception) {
        ""
    }

    fun readyFiles(work: File): List<Ready> {
        val out = mutableListOf<Ready>()
        for (sf in stateFiles(work)) {
            val st = try { stateJson.decodeFromString(UtilState.serializer(), sf.readText()) } catch (_: Exception) { continue }
            val title = st.metadata?.title?.ifEmpty { null } ?: sf.parentFile?.name ?: "kino"
            val page = st.metadata?.inputUrl.orEmpty()
            for (rec in st.completed.values) {
                val f = File(rec.path)
                // Путь от прошлой установки мог указывать в другую папку —
                // ищем ту же серию внутри папки этой серии.
                val file = if (f.isFile) f else sf.parentFile?.walkTopDown()
                    ?.firstOrNull { it.isFile && it.extension.lowercase() in VIDEO_EXT && it.nameWithoutExtension == f.nameWithoutExtension }
                if (file == null || !file.isFile || file.length() == 0L) continue
                val base = file.nameWithoutExtension
                val subs = file.parentFile?.listFiles().orEmpty()
                    .filter { it.isFile && it.extension.equals("srt", true) && it.name.startsWith(base) }
                out += Ready(file, subs, title, page, rec.season, rec.episode)
            }
        }
        return out.sortedWith(compareBy({ it.title }, { it.season }, { it.episode }))
    }

    fun safe(s: String): String =
        s.replace(Regex("[\\\\/:*?\"<>|]+"), " ").trim().ifEmpty { "kino" }

    fun mimeOf(name: String): String =
        if (name.endsWith(".srt", true)) "application/x-subrip" else "video/x-matroska"

    /**
     * Экспорт одного файла. Возвращает uri готового файла; исходник удаляется.
     * Ошибка SAF — исключение («папка не даёт создать файл»).
     */
    fun export(ctx: Context, f: File, title: String, treeUri: Uri?): Uri {
        val mime = mimeOf(f.name)
        // 1. Выбранная папка (SAF).
        if (treeUri != null) {
            val root = DocumentFile.fromTreeUri(ctx, treeUri) ?: throw IllegalStateException("папка недоступна")
            val dir = root.findFile(safe(title))?.takeIf { it.isDirectory }
                ?: root.createDirectory(safe(title))
                ?: throw IllegalStateException("папка не даёт создать файл")
            dir.findFile(f.name)?.delete()
            val target = dir.createFile(mime, f.name) ?: throw IllegalStateException("папка не даёт создать файл")
            ctx.contentResolver.openOutputStream(target.uri)?.use { os ->
                f.inputStream().use { it.copyTo(os, 1 shl 20) }
            } ?: throw IllegalStateException("папка не даёт создать файл")
            f.delete()
            return target.uri
        }
        // 2. До Android 10 файл остаётся в приватной папке приложения.
        if (android.os.Build.VERSION.SDK_INT < android.os.Build.VERSION_CODES.Q) {
            return Uri.fromFile(f)
        }
        // 3. Галерея: Movies/kino/<Тайтл>/.
        val isSubtitle = mime == "application/x-subrip"
        val collection = if (isSubtitle) MediaStoreFiles else MediaStoreVideo
        val values = ContentValues().apply {
            put(MediaStore.MediaColumns.DISPLAY_NAME, f.name)
            put(MediaStore.MediaColumns.MIME_TYPE, mime)
            put(MediaStore.MediaColumns.RELATIVE_PATH, "$MOVIES_ROOT/${safe(title)}")
            put(MediaStore.MediaColumns.IS_PENDING, 1)
        }
        val uri = ctx.contentResolver.insert(collection, values)
            ?: throw IllegalStateException("галерея не приняла файл")
        try {
            ctx.contentResolver.openOutputStream(uri)?.use { os ->
                f.inputStream().use { it.copyTo(os, 1 shl 20) }
            } ?: throw IllegalStateException("галерея не открыла файл")
            values.clear()
            values.put(MediaStore.MediaColumns.IS_PENDING, 0)
            ctx.contentResolver.update(uri, values, null, null)
        } catch (e: Exception) {
            ctx.contentResolver.delete(uri, null, null)
            throw e
        }
        f.delete()
        return uri
    }

    // lazy: getContentUri(volume) существует с API 29 — трогаем только в ветке Q+.
    private val MediaStoreVideo: Uri by lazy {
        MediaStore.Video.Media.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
    }
    private val MediaStoreFiles: Uri by lazy {
        MediaStore.Files.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
    }

    /**
     * После успеха папка серии в workDir стирается целиком: остаточный файл
     * состояния (.kinopub-state.json) заставил бы утилиту молча пропустить
     * «уже скачанные» серии при перекачке.
     */
    fun wipeSeriesFolder(work: File, produced: List<File>) {
        val roots = produced.mapNotNull { f ->
            var dir = f.parentFile
            while (dir != null && dir.parentFile != null && dir.parentFile != work) {
                dir = dir.parentFile
            }
            if (dir != null && dir.parentFile == work && dir != work) dir else null
        }.toSet()
        for (dir in roots) dir.deleteRecursively()
    }

    /** Запись видео в библиотеку: только с распарсенным SxxEyy и titleId. */
    fun toLibraryEntry(ctx: Context, library: LocalLibrary, uri: Uri, title: String, pageUrl: String, size: Long, movie: Boolean = false) {
        val name = displayName(ctx, uri) ?: uri.lastPathSegment ?: return
        val se = LocalLibrary.seasonEpisode(name) ?: return
        val titleId = LocalLibrary.titleIdOf(pageUrl)
        if (titleId <= 0) return
        library.put(titleId, title, se[0], se[1], uri, size, movie)
    }

    private fun displayName(ctx: Context, uri: Uri): String? = try {
        ctx.contentResolver.query(
            uri, arrayOf(android.provider.MediaStore.MediaColumns.DISPLAY_NAME), null, null, null,
        )?.use { c -> if (c.moveToFirst()) c.getString(0) else null }
    } catch (_: Exception) {
        null
    }
}
