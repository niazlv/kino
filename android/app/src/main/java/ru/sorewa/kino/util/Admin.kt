package ru.sorewa.kino.util

import android.content.ActivityNotFoundException
import android.content.Context
import android.net.Uri
import androidx.browser.customtabs.CustomTabsIntent
import ru.sorewa.kino.BuildConfig

/**
 * Админка — серверные страницы под /admin того же домена, переписывать их
 * под телефон незачем: вёрстка уже узкая. Открываются во вкладке браузера
 * поверх приложения (Custom Tab), а не в WebView:
 *  - в браузере работает passkey — вход отпечатком без пароля и кода;
 *    WebView без привязки приложения к домену его не умеет;
 *  - вход живёт в cookie браузера свои 12 часов и с приложением не связан:
 *    токен приложения админку не открывает, второй фактор остаётся в силе.
 * Chrome — первым: в нём passkey работает наверняка. Нет Chrome — браузер
 * по умолчанию.
 *
 * Адрес — страница входа с ?passkey=1: если в этом браузере ключ уже
 * работал, она сама спрашивает отпечаток (static/passkey.js админки).
 * Живая сессия со страницы входа уводит сразу в админку.
 */
object Admin {
    /** Админка того сервера, к которому подключено приложение (он мог быть выбран вручную). */
    val url: Uri get() = Uri.parse(ru.sorewa.kino.KinoApp.instance.container.serverUrl.trimEnd('/') + "/admin/login?passkey=1")

    fun open(context: Context) {
        val tab = CustomTabsIntent.Builder().setShowTitle(true).build()
        try {
            tab.intent.setPackage("com.android.chrome")
            tab.launchUrl(context, url)
        } catch (_: ActivityNotFoundException) {
            tab.intent.setPackage(null)
            try {
                tab.launchUrl(context, url)
            } catch (_: ActivityNotFoundException) {
                // Браузера нет вовсе — открывать нечем.
            }
        }
    }
}
