package ru.sorewa.kino.data

import android.content.Context
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import androidx.datastore.preferences.preferencesDataStoreFile
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import java.util.UUID

private val Context.dataStore by preferencesDataStore(name = "kino")

/** Хранилище режима «на этом устройстве»: сессия и настройки не смешиваются с серверными. */
private val Context.dataStoreLocal by preferencesDataStore(name = "kino_local")

/**
 * Хранилища выбранных серверов (`kino_s_…`), по одному на профиль. DataStore
 * требует ровно один экземпляр на файл в процессе — отсюда общий реестр.
 */
private val serverStores = java.util.concurrent.ConcurrentHashMap<String, androidx.datastore.core.DataStore<Preferences>>()

private fun Context.dataStoreFor(profile: String?): androidx.datastore.core.DataStore<Preferences> = when (profile) {
    null -> dataStore
    ru.sorewa.kino.engine.ConnectionConfig.LOCAL_PROFILE -> dataStoreLocal
    else -> serverStores.computeIfAbsent(profile) {
        val app = applicationContext
        androidx.datastore.preferences.core.PreferenceDataStoreFactory.create {
            app.preferencesDataStoreFile("kino_$profile")
        }
    }
}

/**
 * Всё локальное состояние устройства: сессия (token + refresh), настройки
 * плеера этого входа, оценка канала по площадкам.
 *
 * Сессия живёт на сервере, токен здесь — просто ключ доступа, как cookie
 * в браузере. Refresh-токен продлевает её на 180 дней (ротация при каждом
 * обновлении), deviceId — свой для сквозного прогресса: с одного устройства
 * «кто последний, тот прав», с разных — «кто дальше досмотрел».
 */
class TokenStore(private val context: Context, private val profile: String? = null) {

    /**
     * Своё хранилище у каждого профиля (см. ConnectionConfig.profile): сессия
     * одного сервера не уходит на другой, а вернувшись, человек застаёт свой
     * вход и свои настройки плеера.
     */
    private val ds get() = context.dataStoreFor(profile)


    private object K {
        val token = stringPreferencesKey("token")
        val refreshToken = stringPreferencesKey("refresh_token")
        val tokenExpiresAt = longPreferencesKey("token_expires_at")
        val refreshExpiresAt = longPreferencesKey("refresh_expires_at")
        val deviceId = stringPreferencesKey("device_id")
        val displayName = stringPreferencesKey("display_name")
        val lastLogin = stringPreferencesKey("last_login")

        // Настройки плеера этого устройства: то же, что kp.skip / maxq /
        // hevc / av1 / autonext в localStorage витрины.
        val skipMode = stringPreferencesKey("skip_mode")
        val maxQuality = intPreferencesKey("max_quality")
        val hevcClaim = stringPreferencesKey("hevc_claim")
        val av1Claim = stringPreferencesKey("av1_claim")
        val autonext = booleanPreferencesKey("autonext")
        val wifiOnly = booleanPreferencesKey("wifi_only")
        val swipes = booleanPreferencesKey("player_swipes")
        val hideAfterSec = androidx.datastore.preferences.core.intPreferencesKey("player_hide_after")
        val statsAlways = booleanPreferencesKey("player_stats_always")

        /** Настройки старой оболочки уже забраны (см. LegacyShell). */
        val legacyImported = booleanPreferencesKey("legacy_imported")

        /** Разовый сброс «сводки всегда», перенесённой из 0.1.x (см. [resetStatsAlwaysOnce]). */
        val statsReset = booleanPreferencesKey("player_stats_reset_v1")
    }

    /** Настройки старой оболочки; null — там не задавали, остаётся своё. */
    data class Legacy(
        val maxQuality: Int? = null,
        val hevcClaim: String? = null,
        val av1Claim: String? = null,
        val skipMode: String? = null,
        val autonext: Boolean? = null,
        /** Оценка канала по площадкам, бит/с — как kino.bw.<площадка> витрины. */
        val bandwidth: Map<String, Long> = emptyMap(),
    )

    data class Session(
        val token: String,
        val refreshToken: String?,
        val tokenExpiresAt: Long,
        val refreshExpiresAt: Long,
        val deviceId: String,
        val displayName: String,
        val lastLogin: String,
    ) {
        val authorized: Boolean get() = token.isNotEmpty()
    }

    data class Settings(
        val skipMode: String,          // off | button | auto
        val maxQuality: Int,           // 1080 | 1440 | 2160
        val hevcClaim: String,         // '' | on | off
        val av1Claim: String,          // '' | on | off
        val autonext: Boolean,
        val wifiOnly: Boolean,
        /** Вертикальные свайпы у краёв кадра: слева яркость, справа громкость. */
        val swipes: Boolean = true,
        /** Через сколько секунд игры прячется панель плеера; 0 — не прятать. */
        val hideAfterSec: Int = 3,
        /** Сводка о потоке в углу кадра всегда, а не по кнопке «i». */
        val statsAlways: Boolean = false,
    )

    val sessionFlow: Flow<Session> = ds.data.map(::readSession)
    val settingsFlow: Flow<Settings> = ds.data.map(::readSettings)

    suspend fun session(): Session = readSession(ds.data.first())
    suspend fun settings(): Settings = readSettings(ds.data.first())

    private fun readSession(p: Preferences) = Session(
        token = p[K.token] ?: "",
        refreshToken = p[K.refreshToken],
        tokenExpiresAt = p[K.tokenExpiresAt] ?: 0,
        refreshExpiresAt = p[K.refreshExpiresAt] ?: 0,
        deviceId = p[K.deviceId] ?: "",
        displayName = p[K.displayName] ?: "",
        lastLogin = p[K.lastLogin] ?: "",
    )

    private fun readSettings(p: Preferences) = Settings(
        skipMode = p[K.skipMode] ?: "button",
        maxQuality = p[K.maxQuality]?.takeIf { it > 0 } ?: 1080,
        hevcClaim = p[K.hevcClaim] ?: "",
        av1Claim = p[K.av1Claim] ?: "",
        autonext = p[K.autonext] ?: true,
        wifiOnly = p[K.wifiOnly] ?: false,
        swipes = p[K.swipes] ?: true,
        hideAfterSec = p[K.hideAfterSec] ?: 3,
        statsAlways = p[K.statsAlways] ?: false,
    )

    /**
     * Пишет только переданное. Токен — тоже: вызов ради одного имени
     * (`saveSession(displayName = …)` после /me) иначе записал бы пустой
     * токен по умолчанию и разлогинил бы сразу после входа или переноса
     * сессии старого приложения. Стирает сессию только [clearSession].
     */
    suspend fun saveSession(
        token: String? = null,
        refreshToken: String? = null,
        tokenExpiresAt: Long = 0,
        refreshExpiresAt: Long = 0,
        displayName: String? = null,
        lastLogin: String? = null,
    ) {
        ds.edit { p ->
            token?.let { p[K.token] = it }
            refreshToken?.let { p[K.refreshToken] = it }
            if (tokenExpiresAt > 0) p[K.tokenExpiresAt] = tokenExpiresAt
            if (refreshExpiresAt > 0) p[K.refreshExpiresAt] = refreshExpiresAt
            displayName?.let { p[K.displayName] = it }
            lastLogin?.let { p[K.lastLogin] = it }
            if ((p[K.deviceId] ?: "").isEmpty()) p[K.deviceId] = UUID.randomUUID().toString()
        }
    }

    suspend fun saveSettings(s: Settings) {
        ds.edit { p ->
            p[K.skipMode] = s.skipMode
            p[K.maxQuality] = s.maxQuality
            p[K.hevcClaim] = s.hevcClaim
            p[K.av1Claim] = s.av1Claim
            p[K.autonext] = s.autonext
            p[K.wifiOnly] = s.wifiOnly
            p[K.swipes] = s.swipes
            p[K.hideAfterSec] = s.hideAfterSec
            p[K.statsAlways] = s.statsAlways
        }
    }

    suspend fun legacyImported(): Boolean = ds.data.first()[K.legacyImported] ?: false

    suspend fun markLegacyImported() {
        ds.edit { it[K.legacyImported] = true }
    }

    /**
     * Сводка о потоке по умолчанию выключена. 0.2.0 переносила её из старой
     * оболочки, и у обновившихся она горела всегда; один раз снимаем её
     * у всех — дальше держится только то, что человек включил сам.
     */
    suspend fun resetStatsAlwaysOnce() {
        ds.edit { p ->
            if (p[K.statsReset] == true) return@edit
            p.remove(K.statsAlways)
            p[K.statsReset] = true
        }
    }

    /** Переносит из старой оболочки только то, что здесь ещё не задано. */
    suspend fun importLegacy(l: Legacy) {
        ds.edit { p ->
            fun <T> put(key: Preferences.Key<T>, v: T?) { if (v != null && p[key] == null) p[key] = v }
            put(K.maxQuality, l.maxQuality)
            put(K.hevcClaim, l.hevcClaim)
            put(K.av1Claim, l.av1Claim)
            put(K.skipMode, l.skipMode)
            put(K.autonext, l.autonext)
            for ((source, bps) in l.bandwidth) put(longPreferencesKey("bw.$source"), bps)
        }
    }

    suspend fun clearSession() {
        ds.edit { p ->
            p.remove(K.token)
            p.remove(K.refreshToken)
            p.remove(K.tokenExpiresAt)
            p.remove(K.refreshExpiresAt)
            p.remove(K.displayName)
        }
    }

    // Оценка канала по площадке (бит/с): с неё плеер начнёт следующую серию.
    // Ключ «kino.bw.» + id площадки — как в витрине.
    suspend fun bandwidthHint(source: String): Long {
        if (source.isEmpty()) return 0
        return ds.data.first()[longPreferencesKey("bw.$source")] ?: 0
    }

    suspend fun rememberBandwidth(source: String?, kbps: Long) {
        if (source.isNullOrEmpty() || kbps <= 0) return
        ds.edit { it[longPreferencesKey("bw.$source")] = kbps * 1000 }
    }
}
