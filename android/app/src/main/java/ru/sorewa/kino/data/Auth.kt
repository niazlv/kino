package ru.sorewa.kino.data

import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext

/**
 * Вход и продление сессии.
 *
 * Парольный вход выдаёт только cookie-сессию (30 дней, без refresh-токена).
 * Поэтому сразу после него заводится device-flow: приложение само одобряет
 * свой же запрос — и уходит с парой токен/refresh, где refresh живёт 180 дней
 * и ротируется при каждом обновлении. Перелогиниваться не придётся.
 *
 * На телевизоре пароля нет: там вход по коду — приложение показывает код,
 * человек одобряет с телефона (или на сайте), приложение дожидается пары.
 */
class AuthRepository(private val api: Api, private val store: TokenStore) {

    var me: Me? = null
        private set

    /** После выхода: убрать следы аккаунта вне приложения (главный экран ТВ). */
    var onSignedOut: (suspend () -> Unit)? = null

    /** Вход по логину и паролю (телефон). */
    suspend fun loginWithPassword(login: String, password: String) {
        val rsp = api.login(login, password)
        api.attachToken(rsp.token)
        // Сохранённый токен тут же уводит интерфейс со входа, и корутина
        // экрана входа отменяется — обмен на пару обрывался после
        // /auth/device, и вход жил 30 дней без продления. Поэтому дальше —
        // без отмены.
        withContext(NonCancellable) {
            store.saveSession(rsp.token, tokenExpiresAt = parseIsoTime(rsp.expiresAtIso))
            // Пара с refresh-токеном: самоодобрение device-flow своей же сессией.
            upgradeToRefreshable()
            // Пара получена — парольная сессия больше никому не нужна: без
            // этого она месяц висела бы в «Где вы вошли» вторым входом.
            val now = store.session()
            if (!now.refreshToken.isNullOrEmpty() && now.token != rsp.token) api.logoutSession(rsp.token)
            loadMe()
        }
    }

    /** Чем кончился перенос входа из старого приложения. */
    enum class Adopt {
        /** Сессия перенесена — вход не нужен. */
        ADOPTED,
        /** Переносить нечего (или сервер её не признал) — обычный вход. */
        NONE,
        /** Сервер не ответил — cookie остаётся, попробуем при «Повторить» или следующем запуске. */
        OFFLINE,
    }

    /**
     * Перенос входа из старого приложения (Capacitor-оболочки). Нативное
     * приложение ставится поверх неё тем же пакетом, и сессия витрины
     * остаётся в cookie WebView (app_webview) — её подхватываем, проверяем
     * через /me и меняем на пару токен/refresh, как после входа паролем.
     * Без этого обновление молча выкидывало всех на экран входа.
     *
     * Cookie переносится один раз: после переноса она стирается из WebView,
     * а сама cookie-сессия гасится на сервере, как только есть своя пара, —
     * иначе «Выйти» не держалось бы (следующий запуск входил бы ею снова),
     * а в «Где вы вошли» месяц висел бы лишний вход.
     */
    suspend fun adoptWebViewSession(context: android.content.Context, baseUrl: String): Adopt {
        if (!LegacyShell.present(context)) return Adopt.NONE
        val token = LegacyShell.sessionCookie(baseUrl) ?: return Adopt.NONE
        api.attachToken(token)
        val who = try {
            api.me()
        } catch (e: Exception) {
            api.attachToken("")
            // 401/403 — сессию погасили (выход на всех, блокировка), 410
            // `expired` — истекла: cookie больше ни на что не годна.
            // Остальное — сеть или сервер лежит.
            if (e is ApiError && e.sessionDead) {
                LegacyShell.forgetCookies()
                return Adopt.NONE
            }
            return Adopt.OFFLINE
        }
        // Дальше без отмены (поворот экрана, уход с корня): cookie из WebView
        // уже стёрта, и оборванный обмен оставил бы вход на месяц без продления.
        withContext(NonCancellable) {
            store.saveSession(token, displayName = who.displayName, lastLogin = who.login)
            LegacyShell.forgetCookies()
            me = who
            upgradeToRefreshable()
            val now = store.session()
            if (!now.refreshToken.isNullOrEmpty() && now.token != token) api.logoutSession(token)
            loadMe()
        }
        return Adopt.ADOPTED
    }

    /**
     * Показ кода на ТВ. Возвращает DeviceBegin (код и ссылку для подтверждения),
     * после чего вызывающий дожидается пары через [awaitDeviceApproval].
     */
    suspend fun beginDeviceLogin(): DeviceBegin = api.deviceBegin()

    /** Дожидаемся одобрения кода с телефона/сайта; интервал уважает slow_down. */
    suspend fun awaitDeviceApproval(begin: DeviceBegin): Boolean {
        val started = System.currentTimeMillis()
        val deadline = parseIsoTime(begin.expiresAt).takeIf { it > 0 }
            ?: started + 10 * 60_000
        var interval = (begin.interval.takeIf { it > 0 } ?: 5).coerceAtLeast(1) * 1000
        while (System.currentTimeMillis() < deadline) {
            delay(interval)
            try {
                val pair = api.devicePoll(begin.deviceCode)
                savePair(pair)
                loadMe()
                return true
            } catch (e: ApiError) {
                when (e.code) {
                    "authorization_pending" -> { /* ждём дальше */ }
                    "slow_down" -> interval = (interval * 3 / 2).coerceAtLeast(interval + 1000)
                    "access_denied", "expired_token", "invalid_grant" -> return false
                    else -> throw e
                }
            }
        }
        return false
    }

    private suspend fun upgradeToRefreshable() {
        try {
            val begin = api.deviceBegin()
            // Одобряем своим же токеном — пользователь только что вошёл.
            api.approveDevice(begin.userCode)
            val started = System.currentTimeMillis()
            var interval = (begin.interval.takeIf { it > 0 } ?: 5).coerceAtLeast(1) * 1000
            val deadline = started + 60_000
            while (System.currentTimeMillis() < deadline) {
                delay(interval)
                try {
                    val pair = api.devicePoll(begin.deviceCode)
                    savePair(pair)
                    return
                } catch (e: ApiError) {
                    when (e.code) {
                        "authorization_pending" -> { /* ждём дальше */ }
                        "slow_down" -> interval = (interval * 3 / 2).coerceAtLeast(interval + 1000)
                        else -> return // пара не получилась: остаёмся на cookie-сессии
                    }
                }
            }
        } catch (_: Exception) {
            // Без device-flow живём на парольной сессии: месяц, потом перелогин.
        }
    }

    private suspend fun savePair(pair: Api.TokenPair) {
        // Сначала в клиент, потом на диск: запись уводит интерфейс со входа,
        // и отмена после неё оставила бы запросы без токена.
        api.attachToken(pair.token)
        store.saveSession(
            pair.token, pair.refreshToken,
            parseIsoTime(pair.expiresAt), parseIsoTime(pair.refreshExpiresAt),
        )
    }

    private suspend fun loadMe() {
        try {
            me = api.me()
            me?.let { store.saveSession(displayName = it.displayName) }
        } catch (_: Exception) {
            // /me не отвечает — не мешает playback'у; имя подхватится позже.
        }
    }

    suspend fun logout() {
        try { api.logout() } catch (_: Exception) { /* сессия и так мертва */ }
        dropSession()
    }

    /**
     * Забыть сессию на устройстве, не спрашивая сервер: она уже мертва
     * (истекла, отозвана, refresh не продлевает). Стёртая сессия уводит
     * интерфейс на вход — телефон и ТВ слушают её поток.
     */
    suspend fun dropSession() {
        api.attachToken("")
        store.clearSession()
        me = null
        try { onSignedOut?.invoke() } catch (_: Exception) { }
    }

    companion object {
        fun parseIsoTime(s: String?): Long = ru.sorewa.kino.data.parseIsoTime(s)
    }
}
