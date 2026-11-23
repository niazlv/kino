package ru.sorewa.kino.engine

import java.io.File
import java.net.InetAddress
import java.net.ServerSocket

/**
 * Что нужно знать, чтобы запустить локальный движок подпроцессом (ADR-0015):
 * имя бинаря, окружение и адреса. Всё — чистые функции, без Android.
 *
 * Контракт с движком (cmd/engine): адрес — только петля, доступ — токеном
 * владельца в cookie `kino_session`, жизнь — на stdin: закрылся пайп
 * (приложение умерло) — движок выходит сам.
 */
object EngineLaunch {
    /** Движок лежит среди нативных библиотек: только оттуда Android даёт запускать файлы. */
    const val BINARY = "libkinoengine.so"

    /**
     * Источник по умолчанию: работает без ключей и VPN. Остальные включает
     * человек в настройках движка — осознанно: каждый лишний источник это
     * лишняя нагрузка на чужие сайты.
     */
    const val DEFAULT_PROVIDERS = "anilibria"

    const val HEALTH_PATH = "/api/v1/health"

    /** Файл сессии kinopub в каталоге данных движка. */
    const val KINOPUB_SESSION_FILE = "kinopub-session.json"

    /** Пометка приложения в User-Agent движка: «KinoPlatform-Android/0.2.14». */
    fun appUserAgent(version: String): String =
        "KinoPlatform-Android/" + version.trim().ifEmpty { "dev" }.filter { it.code in 0x21..0x7e }.ifEmpty { "dev" }

    fun baseUrl(port: Int): String = "http://127.0.0.1:$port"

    /**
     * Окружение движка: настройки человека ([EngineSettings]), поверх них —
     * то, что решает приложение и чего настройки перекрывать не вправе (адрес,
     * токен, база, жизнь на stdin). HOME и TMPDIR — свои: на Android у
     * приложения нет ни обычного домашнего каталога, ни записываемого /tmp.
     * Токен задаёт приложение, поэтому движок не пишет его в файл (ownerSession).
     */
    fun env(
        port: Int,
        token: String,
        dataDir: File,
        cacheDir: File,
        settings: EngineSettings = EngineSettings(),
        appVersion: String = "dev",
    ): Map<String, String> = linkedMapOf<String, String>().apply {
        putAll(settings.toEnv())
        // Движок представляется площадкам приложением, а не чужим браузером: подпись
        // браузероподобная, но кончается этой пометкой с версией (ADR-0015).
        put("ENGINE_APP_UA", appUserAgent(appVersion))
        put("ENGINE_ADDR", "127.0.0.1:$port")
        put("ENGINE_DB", File(dataDir, "kino-engine.db").path)
        put("ENGINE_TOKEN", token)
        put("ART_CACHE_DIR", File(cacheDir, "art").path)
        // Сюда движок пишет сессию kinopub после входа по коду и отсюда провайдер её
        // читает и продлевает; настройки путь не перекрывают.
        put("KINOPUB_TOKEN_FILE", File(dataDir, KINOPUB_SESSION_FILE).path)
        put("ENGINE_EXIT_ON_STDIN_EOF", "1")
        put("HOME", dataDir.path)
        put("TMPDIR", cacheDir.path)
    }

    /** Свободный порт на петле: движок поднимется на нём, адрес приложения строится от него. */
    fun pickFreePort(): Int =
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { it.localPort }
}
