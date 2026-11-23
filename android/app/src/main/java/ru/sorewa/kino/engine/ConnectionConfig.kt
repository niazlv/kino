package ru.sorewa.kino.engine

import android.content.Context
import android.content.SharedPreferences
import java.security.SecureRandom
import java.util.Base64

/** Откуда приложение берёт данные: свой сервер или движок на самом устройстве (ADR-0015). */
enum class ConnectionMode(val id: String) {
    REMOTE("remote"),
    LOCAL("local"),
    ;

    companion object {
        fun of(id: String?): ConnectionMode = entries.firstOrNull { it.id == id } ?: REMOTE
    }
}

/**
 * Режим подключения, адрес сервера и токен локального аккаунта. Живут в SharedPreferences, а
 * не в DataStore: режим нужен синхронно, на старте процесса, раньше любого
 * хранилища, ведь от него зависит, какие хранилища вообще открывать.
 *
 * Режим меняется только перезапуском процесса ([AppRestart]): у каждого режима
 * свои данные (идентификаторы серий движка и сервера не совпадают), и
 * переключать их под работающими экранами значило бы смешать их.
 */
class ConnectionConfig private constructor(
    private val prefs: SharedPreferences,
    /** Сервер, вшитый в сборку (-PkinoUrl); пусто — сборка без своего сервера. */
    val builtInServer: String,
) {

    val mode: ConnectionMode get() = ConnectionMode.of(prefs.getString(KEY_MODE, null))

    val isLocal: Boolean get() = mode == ConnectionMode.LOCAL

    /**
     * Сервер, к которому ходит приложение в режиме сервера: выбранный
     * человеком или вшитый в сборку. Пусто — сборка без сервера по умолчанию
     * (открытый исходник), и человек ещё ничего не выбрал: сначала выбор.
     */
    val serverUrl: String
        get() = prefs.getString(KEY_SERVER, null)?.let { ServerAddress.normalize(it) }
            ?: ServerAddress.normalize(builtInServer).orEmpty()

    /** Режим сервера, а сервера нет — экран выбора вместо входа. */
    val needsServer: Boolean get() = !isLocal && serverUrl.isEmpty()

    /**
     * Чьи данные открывать (см. [ModeContext], TokenStore, очередь загрузок):
     * null — вшитого сервера (прежнее хранилище, обновление ничего не теряет),
     * [LOCAL_PROFILE] — движка на устройстве, `s_…` — выбранного сервера.
     */
    val profile: String?
        get() = when {
            isLocal -> LOCAL_PROFILE
            else -> serverUrl.takeIf { it.isNotEmpty() }?.let { ServerAddress.profileOf(it, builtInServer) }
        }

    /** Серверы, где уже бывали, — свежие первыми. */
    fun knownServers(): List<KnownServer> {
        val list = KnownServers.decode(prefs.getString(KEY_SERVERS, null))
        // Вшитый сервер всегда под рукой: на него можно вернуться, даже если
        // человек никогда не выбирал его явно.
        val builtIn = ServerAddress.normalize(builtInServer)
        return if (builtIn == null || list.any { it.url == builtIn }) list else list + KnownServer(builtIn)
    }

    /** Имя сервера из его визитки, если оно было. */
    fun serverName(url: String = serverUrl): String =
        KnownServers.decode(prefs.getString(KEY_SERVERS, null)).firstOrNull { it.url == url }?.name.orEmpty()

    /**
     * Выбрать сервер: дальше приложение работает с ним (и с его данными).
     * Как и смена режима — только с перезапуском процесса ([AppRestart]):
     * клиент API, хранилища и очередь загрузок строятся под один сервер.
     * Сессия прежнего сервера остаётся в его профиле — вернуться можно без входа.
     * commit, а не apply: сразу за записью процесс перезапускается.
     */
    fun setServer(url: String, name: String = ""): Boolean {
        val norm = ServerAddress.normalize(url) ?: return false
        val list = KnownServers.remember(KnownServers.decode(prefs.getString(KEY_SERVERS, null)), norm, name)
        return prefs.edit()
            .putString(KEY_SERVER, norm)
            .putString(KEY_SERVERS, KnownServers.encode(list))
            .putString(KEY_MODE, ConnectionMode.REMOTE.id)
            .commit()
    }

    /** Запомнить имя текущего сервера, когда пришла его визитка. */
    fun rememberName(url: String, name: String) {
        if (name.isEmpty() || serverName(url) == name) return
        val list = KnownServers.remember(KnownServers.decode(prefs.getString(KEY_SERVERS, null)), url, name)
        prefs.edit().putString(KEY_SERVERS, KnownServers.encode(list)).apply()
    }

    /** Убрать сервер из списка (его данные на устройстве остаются). */
    fun forgetServer(url: String) {
        val list = KnownServers.forget(KnownServers.decode(prefs.getString(KEY_SERVERS, null)), url)
        prefs.edit().putString(KEY_SERVERS, KnownServers.encode(list)).apply()
    }

    /**
     * Токен владельца локального аккаунта: случайный, рождается при первом
     * обращении и живёт, пока стоит приложение. По нему движок узнаёт хозяина —
     * это единственный «пароль» локального режима.
     */
    @Synchronized
    fun engineToken(): String {
        prefs.getString(KEY_TOKEN, null)?.takeIf { it.length >= MIN_TOKEN_LEN }?.let { return it }
        val t = newToken()
        prefs.edit().putString(KEY_TOKEN, t).commit()
        return t
    }

    /** commit, а не apply: сразу за записью процесс перезапускается. */
    fun setMode(m: ConnectionMode): Boolean = prefs.edit().putString(KEY_MODE, m.id).commit()

    companion object {
        private const val KEY_MODE = "mode"
        private const val KEY_TOKEN = "engine_token"
        private const val KEY_SERVER = "server_url"
        private const val KEY_SERVERS = "servers"
        const val MIN_TOKEN_LEN = 32

        /** Профиль данных локального движка (каталог files/local, DataStore kino_local). */
        const val LOCAL_PROFILE = "local"

        fun of(context: Context): ConnectionConfig = ConnectionConfig(
            context.applicationContext.getSharedPreferences("connection", Context.MODE_PRIVATE),
            ru.sorewa.kino.BuildConfig.BASE_URL,
        )

        /** 32 случайных байта → 43 знака base64url: движок требует не короче 32. */
        fun newToken(random: SecureRandom = SecureRandom()): String {
            val b = ByteArray(32)
            random.nextBytes(b)
            return Base64.getUrlEncoder().withoutPadding().encodeToString(b)
        }
    }
}
