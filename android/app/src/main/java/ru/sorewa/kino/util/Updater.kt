package ru.sorewa.kino.util

import android.app.Activity
import android.app.AlertDialog
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.SharedPreferences
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.widget.ProgressBar
import android.widget.Toast
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject
import ru.sorewa.kino.BuildConfig
import java.io.File
import java.io.FileOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest

/**
 * Самообновление с нашего же сервера — порт Updater.java.
 *
 * Приложение стоит в обход магазина, и без этого каждое обновление значило
 * бы «скачай и поставь заново» для всей семьи. При возврате в приложение и
 * не чаще раза в полчаса читается /app/version.json; если версия там
 * новее установленной, предлагается обновиться: APK качается в кеш,
 * сверяется по sha256 и отдаётся системному установщику. Подпись проверяет
 * сам Android: APK с чужим ключом поверх нашего он не поставит.
 *
 * Выкладывает файлы deploy/prod/publish-apk.sh.
 *
 * Источник обновлений — сервер, вшитый в сборку (-PkinoUrl), а не тот, что
 * человек выбрал в приложении: APK подписан ключом того, кто собрал сборку,
 * и чужой сервер раздаёт свой APK со своим ключом — Android поверх него не
 * поставит. Сборка без вшитого сервера обновляется вручную.
 */
class Updater(private val activity: Activity) {

    private val prefs: SharedPreferences = activity.getSharedPreferences("updater", Context.MODE_PRIVATE)

    fun onResume(force: Boolean) {
        // Ждущий разрешения APK помним и на диске: включив «неизвестные
        // приложения», Android часто перезапускает процесс, и поле в памяти
        // пропадало — человек возвращался, а установка так и не начиналась.
        val p = pending ?: prefs.getString(PENDING_KEY, null)?.let(::File)
        if (p != null && canInstall()) {
            pending = null
            prefs.edit().remove(PENDING_KEY).apply()
            if (p.exists() && apkCode(activity, p) > installedCode(activity)) {
                install(p)
                return
            }
        }
        val now = System.currentTimeMillis()
        if (!force && now - prefs.getLong("lastCheck", 0) < CHECK_EVERY_MS) return
        Thread {
            val result = checkInternal(activity)
            // Запоминаем только удавшуюся проверку: ТВ часто стартует раньше,
            // чем поднялся Wi-Fi, и неудача не должна откладывать её на 6 часов.
            if (result.remote > 0) prefs.edit().putLong("lastCheck", now).apply()
            result.update?.let { (code, name, file, sha) ->
                activity.runOnUiThread { offer(code, name, file, sha) }
            }
        }.start()
    }

    private fun offer(code: Long, name: String, file: String, sha: String) {
        if (activity.isFinishing) return
        AlertDialog.Builder(activity)
            .setTitle("Доступно обновление $name")
            .setMessage("Загрузим и поставим поверх текущей версии. Вход и настройки сохранятся.")
            .setPositiveButton("Обновить") { _, _ -> download(file, sha) }
            .setNegativeButton("Позже") { _, _ ->
                prefs.edit()
                    .putLong("snoozedCode", code)
                    .putLong("snoozedAt", System.currentTimeMillis()).apply()
            }
            .show()
    }

    private fun download(file: String, sha: String) {
        val progress = AlertDialog.Builder(activity)
            .setTitle("Загружаем обновление…")
            .setView(ProgressBar(activity))
            .setCancelable(false)
            .show()
        Thread {
            try {
                val out = File(dir(), "kino.apk")
                fetchApk(BuildConfig.BASE_URL, file, sha, out)
                val sameKey = sameSigner(activity, out)
                activity.runOnUiThread {
                    progress.dismiss()
                    if (sameKey == false) {
                        Toast.makeText(activity, FOREIGN_KEY, Toast.LENGTH_LONG).show()
                    } else install(out)
                }
            } catch (e: Exception) {
                activity.runOnUiThread {
                    progress.dismiss()
                    Toast.makeText(activity, "Не удалось скачать обновление", Toast.LENGTH_LONG).show()
                }
            }
        }.start()
    }

    /** Установка. С Android 8 нужно разовое разрешение «неизвестные приложения». */
    private fun install(apk: File) {
        if (!canInstall()) {
            pending = apk
            prefs.edit().putString(PENDING_KEY, apk.path).apply()
            AlertDialog.Builder(activity)
                .setTitle("Нужно разрешение")
                .setMessage("Разрешите kino ставить обновления и вернитесь в приложение.")
                .setPositiveButton("Открыть настройки") { _, _ ->
                    // На Android TV экрана «неизвестные приложения» часто нет:
                    // без проверки здесь было бы падение. Тогда — общие
                    // настройки безопасности, а если и их нет — подсказка.
                    val tries = listOf(
                        Intent(
                            Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                            Uri.parse("package:" + activity.packageName),
                        ),
                        Intent(Settings.ACTION_SECURITY_SETTINGS),
                        Intent(Settings.ACTION_SETTINGS),
                    )
                    val opened = tries.any { i ->
                        try { activity.startActivity(i); true } catch (_: Exception) { false }
                    }
                    if (!opened) Toast.makeText(
                        activity,
                        "Разрешите установку из неизвестных источников в настройках устройства",
                        Toast.LENGTH_LONG,
                    ).show()
                }
                .setNegativeButton("Позже", null)
                .show()
            return
        }
        val uri = FileProvider.getUriForFile(activity, activity.packageName + ".fileprovider", apk)
        try {
            activity.startActivity(
                Intent(Intent.ACTION_VIEW)
                    .setDataAndType(uri, "application/vnd.android.package-archive")
                    .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK),
            )
        } catch (_: Exception) {
            Toast.makeText(activity, "На устройстве не нашлось установщика приложений", Toast.LENGTH_LONG).show()
        }
    }

    private fun canInstall(): Boolean =
        Build.VERSION.SDK_INT < Build.VERSION_CODES.O ||
            activity.packageManager.canRequestPackageInstalls()

    private fun dir(): File =
        File(activity.cacheDir, "update").apply { mkdirs() }

    companion object {
        // version.json — пара сотен байт, проверять можно часто: раз в 6 часов
        // телефон не замечал бы вышедшее обновление до вечера.
        private const val CHECK_EVERY_MS = 30L * 60 * 1000
        private const val SNOOZE_MS = 24L * 60 * 60 * 1000
        private const val PENDING_KEY = "pendingApk"

        /** versionCode APK-файла; 0 — не прочитался. */
        @Suppress("DEPRECATION")
        private fun apkCode(context: Context, apk: File): Long = try {
            val pi = context.packageManager.getPackageArchiveInfo(apk.path, 0)
            when {
                pi == null -> 0
                Build.VERSION.SDK_INT >= Build.VERSION_CODES.P -> pi.longVersionCode
                else -> pi.versionCode.toLong()
            }
        } catch (_: Exception) {
            0
        }

        private const val FOREIGN_KEY =
            "Обновление подписано другим ключом и поверх этой сборки не встанет: " +
                "удалите приложение и поставьте его заново с сайта."

        /**
         * APK, ждущий разрешения на установку. Общий на процесс: проверка из
         * настроек и onResume активности — разные экземпляры Updater, а
         * вернувшись из системных настроек, ставить должна именно активность.
         */
        @Volatile private var pending: File? = null

        /**
         * Разовая проверка из настроек: возвращает заметку для человека.
         * Если есть новее — качает (или берёт уже скачанное с тем же sha256)
         * и сразу открывает установщик.
         *
         * Нужна активность, а не контекст приложения: с ним установщик не
         * открывается, строка пишет «Скачано… откройте APK», а следующее
         * нажатие качает те же 22 МБ заново.
         */
        suspend fun checkNow(context: Context): String = withContext(Dispatchers.IO) {
            if (BuildConfig.BASE_URL.isEmpty()) {
                return@withContext "Эта сборка не знает своего сервера обновлений — новую версию ставят вручную."
            }
            try {
                val v = fetchVersion(BuildConfig.BASE_URL)
                val remote = v.getLong("versionCode")
                val name = v.optString("versionName", remote.toString())
                val local = installedCode(context)
                if (remote <= local) {
                    return@withContext "Обновлений нет: стоит последняя версия ${BuildConfig.VERSION_NAME}."
                }
                val file = v.optString("file", "kino.apk")
                val sha = v.optString("sha256", "")
                val out = File(File(context.cacheDir, "update").apply { mkdirs() }, "kino.apk")
                fetchApk(BuildConfig.BASE_URL, file, sha, out)
                if (sameSigner(context, out) == false) return@withContext FOREIGN_KEY
                val activity = context.findActivity()
                    ?: return@withContext "Версия $name скачана, но открыть установщик отсюда нельзя."
                withContext(Dispatchers.Main) { Updater(activity).install(out) }
                "Версия $name скачана — открыт установщик. Нажмите «Установить»."
            } catch (e: Exception) {
                "Проверить не удалось: ${e.message?.take(80) ?: "ошибка сети"}"
            }
        }

        private tailrec fun Context.findActivity(): Activity? = when (this) {
            is Activity -> this
            is ContextWrapper -> baseContext.findActivity()
            else -> null
        }

        /** Качает APK, если в кеше нет уже скачанного с тем же sha256. */
        private fun fetchApk(base: String, file: String, sha: String, out: File) {
            if (sha.isNotEmpty() && out.exists() && sha.equals(sha256(out), ignoreCase = true)) return
            downloadApk(base, file, sha, out)
        }

        private fun sha256(f: File): String {
            val md = MessageDigest.getInstance("SHA-256")
            f.inputStream().use { input ->
                val b = ByteArray(64 * 1024)
                var n: Int
                while (input.read(b).also { n = it } > 0) md.update(b, 0, n)
            }
            return md.digest().joinToString("") { "%02x".format(it) }
        }

        /**
         * Подписан ли скачанный APK тем же ключом, что установленное
         * приложение. null — не удалось выяснить (тогда решает установщик).
         * false — например, стоит отладочная сборка: установщик сказал бы
         * лишь «Приложение не установлено».
         */
        @Suppress("DEPRECATION")
        private fun sameSigner(context: Context, apk: File): Boolean? = try {
            val pm = context.packageManager
            fun digests(sigs: Array<android.content.pm.Signature>?): Set<String> =
                sigs.orEmpty().map { s ->
                    MessageDigest.getInstance("SHA-256").digest(s.toByteArray())
                        .joinToString("") { "%02x".format(it) }
                }.toSet()
            val (mine, theirs) = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                val flag = PackageManager.GET_SIGNING_CERTIFICATES
                val a = pm.getPackageInfo(context.packageName, flag).signingInfo
                val b = pm.getPackageArchiveInfo(apk.path, flag)?.signingInfo
                fun all(i: android.content.pm.SigningInfo?): Set<String> =
                    if (i == null) emptySet()
                    else digests(i.apkContentsSigners) + digests(i.signingCertificateHistory)
                all(a) to all(b)
            } else {
                val flag = PackageManager.GET_SIGNATURES
                digests(pm.getPackageInfo(context.packageName, flag).signatures) to
                    digests(pm.getPackageArchiveInfo(apk.path, flag)?.signatures)
            }
            if (mine.isEmpty() || theirs.isEmpty()) null else mine.any { it in theirs }
        } catch (_: Exception) {
            null
        }

        /** Результат проверки: null — обновлений нет. */
        internal fun checkInternal(activity: Activity): CheckResult = try {
            val v = fetchVersion(BuildConfig.BASE_URL)
            val remote = v.getLong("versionCode")
            val local = installedCode(activity)
            if (remote <= local) CheckResult(null, local, remote)
            else {
                // «Позже» на сутки.
                val prefs = activity.getSharedPreferences("updater", Context.MODE_PRIVATE)
                if (prefs.getLong("snoozedCode", 0) == remote &&
                    System.currentTimeMillis() - prefs.getLong("snoozedAt", 0) < SNOOZE_MS
                ) CheckResult(null, local, remote)
                else CheckResult(
                    Update(remote, v.optString("versionName", remote.toString()),
                        v.optString("file", "kino.apk"), v.optString("sha256", "")),
                    local, remote,
                )
            }
        } catch (_: Exception) {
            CheckResult(null, 0, 0)
        }

        private fun fetchVersion(base: String): JSONObject {
            val c = open("$base/app/version.json")
            try {
                return JSONObject(c.inputStream!!.bufferedReader().use { it.readText() })
            } finally { c.disconnect() }
        }

        /** Скачивает APK и сверяет sha256. */
        internal fun downloadApk(base: String, file: String, sha: String, out: File): String {
            val c = open("$base/app/$file")
            val md = MessageDigest.getInstance("SHA-256")
            try {
                c.inputStream!!.use { input ->
                    FileOutputStream(out).use { fo ->
                        val b = ByteArray(64 * 1024)
                        var n: Int
                        while (input.read(b).also { n = it } > 0) {
                            fo.write(b, 0, n)
                            md.update(b, 0, n)
                        }
                    }
                }
            } finally { c.disconnect() }
            val got = md.digest().joinToString("") { "%02x".format(it) }
            if (sha.isNotEmpty() && !sha.equals(got, ignoreCase = true)) {
                out.delete()
                throw IllegalStateException("sha256 не совпал")
            }
            return got
        }

        private fun open(url: String): HttpURLConnection {
            val c = URL(url).openConnection() as HttpURLConnection
            c.connectTimeout = 8000
            c.readTimeout = 20000
            c.useCaches = false
            if (c.responseCode != 200) throw IllegalStateException("HTTP ${c.responseCode}")
            return c
        }

        fun installedCode(context: Context?): Long = try {
            val pm = context?.packageManager ?: return 0
            val pi = pm.getPackageInfo(context.packageName, 0)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) pi.longVersionCode else pi.versionCode.toLong()
        } catch (_: Exception) {
            0
        }
    }

    data class Update(val code: Long, val name: String, val file: String, val sha: String)
    data class CheckResult(val update: Update?, val local: Long, val remote: Long)
}
