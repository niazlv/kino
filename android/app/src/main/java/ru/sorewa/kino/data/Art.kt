package ru.sorewa.kino.data

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.media.MediaMetadataRetriever
import android.net.Uri
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.Request
import java.io.File
import java.io.FileOutputStream
import java.util.concurrent.ConcurrentHashMap

/**
 * Офлайн-картинки — порт Previews.java, расширенный под офлайн «как онлайн».
 *
 * В files/art лежат: постер тайтла (t<id>.jpg), кадр серии с сервера
 * (s<episodeId>.jpg) и кадр, вынутый из самого скачанного файла
 * (e<hash uri>.jpg: 15% длины, но не дальше двух минут — интро в начале,
 * спойлеры в середине). Экраны берут картинку отсюда раньше сети: без сети
 * обложки и кадры те же, что онлайн.
 *
 * Пустой <имя>.none — «у сервера этой картинки нет» (или кадр из файла не
 * вынулся): не мучить при каждом запуске. Сетевой сбой .none не ставит —
 * иначе постер, не скачавшийся однажды без сети, не появлялся бы никогда.
 */
class Art(private val context: Context, private val api: Api) {

    private val dir: File by lazy { File(context.filesDir, "art").apply { mkdirs() } }

    /** Имена готовых файлов: экраны спрашивают на главном потоке, диск — не трогаем. */
    private val present: MutableSet<String> by lazy {
        ConcurrentHashMap.newKeySet<String>().apply { dir.list()?.let { addAll(it) } }
    }

    private fun has(name: String) = name in present
    private fun fileIf(name: String): File? = if (has(name)) File(dir, name) else null

    // ---- что есть на диске (дёшево, можно из композиции) ----

    fun posterPath(titleId: Long): File? = if (titleId > 0) fileIf(posterName(titleId)) else null
    fun shotPath(episodeId: Long): File? = if (episodeId > 0) fileIf(shotName(episodeId)) else null
    fun framePath(uriString: String): File? = fileIf(frameName(uriString))

    private fun posterName(titleId: Long) = "t$titleId.jpg"
    private fun shotName(episodeId: Long) = "s$episodeId.jpg"
    private fun frameName(uriString: String): String =
        "e" + Integer.toHexString(uriString.hashCode()) + ".jpg"

    // ---- добыча ----

    /**
     * Обход библиотеки: кадры из файлов, постеры, длительности. Однопоточно
     * и в фоне; возвращает true, если что-то изменилось.
     */
    suspend fun refresh(library: LocalLibrary): Boolean = withContext(Dispatchers.IO) {
        var changed = false
        for (e in library.list()) {
            val name = frameName(e.uri)
            if (!has(name) && !has("$name.none")) {
                val duration = extractFrame(e.uri, File(dir, name))
                if (duration > 0) {
                    present.add(name)
                    library.setDuration(e.id, duration)
                    changed = true
                } else {
                    mark("$name.none")
                }
            }
            if (e.titleId > 0 && ensurePoster(e.titleId)) changed = true
        }
        changed
    }

    /** Постер тайтла на диск; true — появился новый файл. */
    suspend fun ensurePoster(titleId: Long, retryMissing: Boolean = false): Boolean = withContext(Dispatchers.IO) {
        val name = posterName(titleId)
        if (has(name)) return@withContext false
        if (has("$name.none") && !retryMissing) return@withContext false
        fetch(api.artUrl(titleId), name, 480)
    }

    /** Кадр серии с сервера на диск; true — появился новый файл. */
    suspend fun ensureShot(episodeId: Long): Boolean = withContext(Dispatchers.IO) {
        val name = shotName(episodeId)
        if (has(name) || has("$name.none")) return@withContext false
        fetch(api.shotUrl(episodeId), name, 400)
    }

    /**
     * Картинка с сервера тем же клиентом, что API (сессия и её продление).
     * 404 и мусор вместо картинки — .none; обрыв сети — ничего, повторим.
     */
    private fun fetch(url: String, name: String, width: Int): Boolean = try {
        val req = Request.Builder().url(url).build()
        api.http.newCall(req).execute().use { resp ->
            when {
                resp.code == 404 || resp.code == 204 -> { mark("$name.none"); false }
                !resp.isSuccessful -> false
                else -> {
                    val bmp = BitmapFactory.decodeStream(resp.body?.byteStream())
                    if (bmp == null) {
                        mark("$name.none"); false
                    } else {
                        writeScaled(bmp, File(dir, name), width)
                        present.add(name)
                        present.remove("$name.none")
                        File(dir, "$name.none").delete()
                        true
                    }
                }
            }
        }
    } catch (_: Exception) {
        false
    }

    private fun mark(name: String) {
        try {
            File(dir, name).createNewFile()
            present.add(name)
        } catch (_: Exception) { }
    }

    /** Кадр из файла; возвращает длительность файла (0 — не вынул). */
    private fun extractFrame(uriString: String, out: File): Long = try {
        val retriever = MediaMetadataRetriever()
        try {
            retriever.setDataSource(context, Uri.parse(uriString))
            val duration = retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)
                ?.toLongOrNull() ?: 0
            val at = if (duration > 0) minOf(duration * 15 / 100, 120_000) else 30_000
            val bmp = retriever.getFrameAtTime(at * 1000, MediaMetadataRetriever.OPTION_CLOSEST_SYNC)
            if (bmp != null) {
                writeScaled(bmp, out, 480)
                duration
            } else 0
        } finally {
            retriever.release()
        }
    } catch (_: Exception) {
        0
    }

    private fun writeScaled(bmp: Bitmap, out: File, width: Int) {
        val scaled = if (bmp.width > width) {
            val h = (bmp.height.toLong() * width / bmp.width).toInt().coerceAtLeast(1)
            Bitmap.createScaledBitmap(bmp, width, h, true)
        } else bmp
        val tmp = File(out.parentFile, out.name + ".tmp")
        FileOutputStream(tmp).use { scaled.compress(Bitmap.CompressFormat.JPEG, 84, it) }
        if (!tmp.renameTo(out)) {
            out.delete()
            tmp.renameTo(out)
        }
        if (scaled !== bmp) scaled.recycle()
        bmp.recycle()
    }
}
