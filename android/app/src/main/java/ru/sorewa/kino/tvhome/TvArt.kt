package ru.sorewa.kino.tvhome

import android.content.ContentProvider
import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import android.os.ParcelFileDescriptor
import androidx.tvprovider.media.tv.TvContractCompat.PreviewPrograms
import okhttp3.Request
import ru.sorewa.kino.data.Api
import java.io.File
import java.io.FileNotFoundException
import java.io.FileOutputStream
import java.io.IOException
import java.security.MessageDigest
import java.util.UUID

/**
 * Картинки карточек главного экрана ТВ.
 *
 * Лаунчер — чужой процесс без нашей cookie, а сервер отдаёт постеры и кадры
 * только с сессией. Поэтому приложение скачивает их само в files/tvhome и
 * отдаёт через [TvArtProvider]. Провайдер открыт всем (лаунчеры у прошивок
 * разные — выдавать права поштучно некому), поэтому имена файлов — хеш ключа
 * с солью этой установки: по угаданному «t123.jpg» чужое приложение не
 * узнает, что здесь смотрят. Сами адреса видит только система (TvProvider
 * отдаёт строки лишь их владельцу).
 */
internal class TvArt(private val context: Context, private val api: Api) {

    data class Pic(val uri: Uri, val aspect: Int)

    /** Имена, к которым обращались в этом обходе: остальное — мусор для [prune]. */
    private val used = HashSet<String>()

    private val dir: File get() = dir(context)

    /** Кадр серии 16:9 — для «Продолжить просмотр». */
    fun shot(episodeId: Long): Pic? = fetch("e$episodeId", api.shotUrl(episodeId), 640, PreviewPrograms.ASPECT_RATIO_16_9)

    /** Постер тайтла; без постера на сервере и не спрашиваем. */
    fun posterIf(titleId: Long, hasArt: Boolean): Pic? =
        if (!hasArt) null
        else fetch("t$titleId", api.artUrl(titleId), 360, PreviewPrograms.ASPECT_RATIO_MOVIE_POSTER)

    private fun fetch(key: String, url: String, width: Int, aspect: Int): Pic? {
        val name = nameOf(key)
        used += name
        val f = File(dir, name)
        if (f.isFile) return Pic(uriOf(context, name), aspect)
        // Нет картинки у сервера — не спрашивать при каждом обновлении, но
        // через сутки попробовать снова: кадр мог появиться.
        val none = File(dir, "$name.none")
        if (none.exists() && System.currentTimeMillis() - none.lastModified() < RETRY_MS) return null
        val ok = try {
            api.http.newCall(Request.Builder().url(url).build()).execute().use { resp ->
                // Только «нет такой картинки» отмечается; 5xx и прочее — попробуем в следующий раз.
                if (resp.code == 404) return@use false
                if (!resp.isSuccessful) return null
                val bmp = resp.body?.byteStream()?.let(BitmapFactory::decodeStream) ?: return@use false
                writeScaled(bmp, f, width)
                true
            }
        } catch (_: IOException) {
            return null // сеть: без отметки, попробуем в следующий раз
        }
        if (ok) {
            none.delete()
            return Pic(uriOf(context, name), aspect)
        }
        try { none.createNewFile(); none.setLastModified(System.currentTimeMillis()) } catch (_: IOException) { }
        return null
    }

    /** Убрать файлы карточек, которых больше нет. */
    fun prune() {
        dir.listFiles()?.forEach { f ->
            if (f.name.removeSuffix(".none") !in used) f.delete()
        }
    }

    private fun nameOf(key: String): String {
        val md = MessageDigest.getInstance("SHA-256")
        md.update(salt(context).toByteArray())
        md.update(key.toByteArray())
        return md.digest().take(16).joinToString("") { "%02x".format(it) } + ".jpg"
    }

    /** Запись через временный файл: лаунчер может читать картинку в ту же секунду. */
    private fun writeScaled(bmp: Bitmap, out: File, width: Int) {
        val scaled = if (bmp.width > width) {
            val h = (bmp.height.toLong() * width / bmp.width).toInt().coerceAtLeast(1)
            Bitmap.createScaledBitmap(bmp, width, h, true)
        } else bmp
        val tmp = File(out.parentFile, out.name + ".tmp")
        FileOutputStream(tmp).use { scaled.compress(Bitmap.CompressFormat.JPEG, 86, it) }
        if (!tmp.renameTo(out)) { tmp.delete(); throw IOException("rename ${out.name}") }
    }

    companion object {
        const val DIR = "tvhome"
        private const val RETRY_MS = 24 * 3600_000L
        private const val K_SALT = "art_salt"

        /** Только имена, которые выдаёт [nameOf]: 32 hex + .jpg. */
        internal val NAME = Regex("^[0-9a-f]{32}\\.jpg$")

        fun dir(context: Context): File = File(context.filesDir, DIR).apply { mkdirs() }

        fun uriOf(context: Context, name: String): Uri =
            Uri.Builder().scheme("content").authority(authority(context)).appendPath(name).build()

        fun authority(context: Context) = "${context.packageName}.tvart"

        private fun salt(context: Context): String {
            val p = context.getSharedPreferences("tvhome", Context.MODE_PRIVATE)
            p.getString(K_SALT, null)?.let { return it }
            val s = UUID.randomUUID().toString()
            p.edit().putString(K_SALT, s).apply()
            return s
        }

        /** После выхода: картинки и соль — новый вход получит новые адреса. */
        fun wipe(context: Context) {
            dir(context).listFiles()?.forEach { it.delete() }
            context.getSharedPreferences("tvhome", Context.MODE_PRIVATE).edit().remove(K_SALT).apply()
        }
    }
}

/**
 * Отдаёт лаунчеру картинки карточек из files/tvhome — только чтение и только
 * файлы с именами вида [TvArt.NAME]; ни запросов, ни записи.
 */
class TvArtProvider : ContentProvider() {
    override fun onCreate(): Boolean = true

    override fun getType(uri: Uri): String = "image/jpeg"

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        if (mode != "r") throw SecurityException("только чтение")
        val name = uri.lastPathSegment?.takeIf { TvArt.NAME.matches(it) }
            ?: throw FileNotFoundException(uri.toString())
        val f = File(TvArt.dir(context!!), name)
        if (!f.isFile) throw FileNotFoundException(uri.toString())
        return ParcelFileDescriptor.open(f, ParcelFileDescriptor.MODE_READ_ONLY)
    }

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = null
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = 0
}
