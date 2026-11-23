package ru.sorewa.kino.data

import android.content.Context
import android.webkit.CookieManager
import android.webkit.WebView
import android.webkit.WebViewClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import java.io.File
import kotlin.coroutines.resume

/**
 * Следы старого приложения — Capacitor-оболочки 0.1.x. Это приложение
 * ставится поверх неё обновлением (тот же пакет и ключ), данные пакета
 * остаются, и всё нужное забирается отсюда.
 *
 * Что откуда переезжает:
 *  - вход: cookie `kino_session` витрины в хранилище WebView (`app_webview`) —
 *    [AuthRepository.adoptWebViewSession];
 *  - опись скачанного, кадры и постеры, папка загрузок, канал уведомлений —
 *    те же файлы и имена (`files/library.json`, `files/art`, prefs
 *    `downloads`/`tree`, канал `downloads`): переносить нечего;
 *  - настройки устройства: «Качество», HEVC/AV1 — в prefs
 *    оболочки (`downloads`, ключи `settings/…`); пропуск заставок, «следующая
 *    серия», оценка канала по площадкам и режим телевизора — в localStorage
 *    витрины. Их один раз забирает [importSettings].
 */
object LegacyShell {

    /** Была ли здесь старая оболочка: только она заводила WebView. */
    fun present(context: Context): Boolean =
        File(context.applicationInfo.dataDir, "app_webview").isDirectory

    /**
     * Включённый в старой витрине «Режим телевизора» на устройстве без
     * leanback: такой приставке нужен ТВ-интерфейс, хотя лаунчер её
     * телевизором не считает. Читается синхронно из onCreate.
     */
    fun wantsTv(context: Context): Boolean =
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getBoolean(KEY_TV, false)

    /** Сессия витрины из cookie WebView; null — нет или истекла (истёкшую WebView не отдаёт). */
    suspend fun sessionCookie(baseUrl: String): String? = withContext(Dispatchers.Main) {
        try {
            CookieManager.getInstance().getCookie(baseUrl)
                ?.split(';')
                ?.map { it.trim() }
                ?.firstOrNull { it.startsWith("$COOKIE=") }
                ?.substringAfter('=')
                ?.takeIf { it.isNotBlank() }
        } catch (_: Throwable) {
            null // WebView на устройстве нет или не поднялся
        }
    }

    /**
     * Сессия перенесена (или сервер её больше не признаёт) — cookie в WebView
     * больше не нужна. Без этого «Выйти» в приложении не держалось: при
     * следующем запуске та же cookie входила бы обратно. Других cookie у
     * приложения нет — WebView им больше не пользуется.
     */
    suspend fun forgetCookies() = withContext(Dispatchers.Main) {
        try {
            val cm = CookieManager.getInstance()
            cm.removeAllCookies(null)
            cm.flush()
        } catch (_: Throwable) { }
    }

    /**
     * Настройки старой оболочки — один раз, при первом запуске после
     * обновления. Берётся только то, чего в этом приложении ещё не задавали:
     * собственный выбор человека здесь важнее старого.
     */
    suspend fun importSettings(context: Context, store: TokenStore, baseUrl: String) {
        store.resetStatsAlwaysOnce()
        if (store.legacyImported()) return
        if (!present(context)) {
            store.markLegacyImported()
            return
        }
        val shell = context.getSharedPreferences("downloads", Context.MODE_PRIVATE)
        fun setting(key: String): String = shell.getString("settings/$key", null).orEmpty()
        val page = readLocalStorage(context, baseUrl)

        val legacy = TokenStore.Legacy(
            maxQuality = setting("maxq").toIntOrNull()?.takeIf { it in setOf(1080, 1440, 2160) },
            hevcClaim = setting("hevc").takeIf { it == "on" || it == "off" },
            av1Claim = setting("av1").takeIf { it == "on" || it == "off" },
            skipMode = page["kp.skip"]?.takeIf { it == "off" || it == "button" || it == "auto" },
            autonext = page["kp.autonext"]?.let { it != "off" },
            bandwidth = page.filterKeys { it.startsWith("kino.bw.") }
                .mapNotNull { (k, v) ->
                    val bps = v.toLongOrNull()?.takeIf { it > 0 } ?: return@mapNotNull null
                    k.removePrefix("kino.bw.").takeIf { it.isNotEmpty() }?.let { it to bps }
                }.toMap(),
        )
        store.importLegacy(legacy)
        if (page["kino.tv"] == "1") {
            context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putBoolean(KEY_TV, true).apply()
        }
        // Своя сессия уже есть — cookie старой витрины больше не понадобится.
        if (store.session().authorized) forgetCookies()
        store.markLegacyImported()
    }

    /**
     * localStorage витрины: пустая страница с тем же происхождением, что
     * у старой оболочки, — в сеть не ходит (loadDataWithBaseURL и запрет
     * сетевых загрузок), хранилище у происхождения общее. Не успели —
     * пустой ответ: настройки по умолчанию, не повод тормозить запуск.
     */
    private suspend fun readLocalStorage(context: Context, baseUrl: String): Map<String, String> =
        withTimeoutOrNull(8_000) {
            withContext(Dispatchers.Main) {
                suspendCancellableCoroutine { cont ->
                    val wv = try {
                        WebView(context.applicationContext)
                    } catch (_: Throwable) {
                        cont.resume(emptyMap())
                        return@suspendCancellableCoroutine
                    }
                    wv.settings.javaScriptEnabled = true
                    wv.settings.domStorageEnabled = true
                    wv.settings.blockNetworkLoads = true
                    var done = false
                    fun finish(result: Map<String, String>) {
                        if (done) return
                        done = true
                        wv.destroy()
                        if (cont.isActive) cont.resume(result)
                    }
                    wv.webViewClient = object : WebViewClient() {
                        override fun onPageFinished(view: WebView, url: String?) {
                            view.evaluateJavascript(READ_JS) { raw -> finish(parse(raw)) }
                        }
                    }
                    cont.invokeOnCancellation { wv.post { finish(emptyMap()) } }
                    wv.loadDataWithBaseURL(
                        baseUrl.trimEnd('/') + "/", "<!doctype html><html></html>",
                        "text/html", "utf-8", null,
                    )
                }
            }
        } ?: emptyMap()

    private fun parse(raw: String?): Map<String, String> = try {
        val o = JSONObject(raw ?: "{}")
        o.keys().asSequence().associateWith { o.optString(it) }
    } catch (_: Exception) {
        emptyMap()
    }

    private const val COOKIE = "kino_session"
    private const val PREFS = "legacy"
    private const val KEY_TV = "tv"

    /** Только ключи витрины, которые здесь что-то значат. */
    private const val READ_JS = """(function(){var o={};try{for(var i=0;i<localStorage.length;i++){var k=localStorage.key(i);if(/^(kp\.skip|kp\.autonext|kino\.tv|kino\.bw\.)/.test(k))o[k]=localStorage.getItem(k);}}catch(e){}return o;})()"""
}
