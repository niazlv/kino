package ru.sorewa.kino.engine

import android.content.Context
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.net.URI

/**
 * Источник, который можно включить в локальном движке. Главный список — у самого
 * движка (`GET /engine/sources`, ADR-0017): там все площадки на Go, скрипты из
 * сборки и площадки, поставленные человеком. Здесь — его последний снимок
 * ([remember]) поверх вшитого запасного списка ([bundled]), чтобы экран и
 * [EngineSettings.toEnv] знали площадки и до запуска движка. Новая площадка на
 * сервере или в скрипте появляется здесь сама — править приложение не нужно.
 *
 * Пометки в [note] — то, что проверено запуском (поиск, тикет, настоящий
 * сегмент), и то, что известно про площадки из ограничений самих плееров;
 * приложению не принадлежит ни то, ни другое, и обещаний оно не даёт.
 */
object EngineSources {

    @Serializable
    data class Source(
        val id: String,
        val title: String,
        val note: String,
        /** Переменная окружения с ключом; null — источнику ключ не нужен. */
        val keyEnv: String? = null,
        val keyLabel: String? = null,
        /** builtin — площадка на Go, bundled — скрипт из сборки, local — поставлен на устройстве. */
        val origin: String = "builtin",
        val version: String = "",
    )

    /** Запасной список: на случай, если движок ещё ни разу не ответил. */
    val bundled: List<Source> = listOf(
        Source("anilibria", "Анилибрия", "Аниме. Играет без ключей и VPN."),
        Source("animego_me", "AnimeGo (.me)", "Аниме. Часть плееров открывается только с российского адреса."),
        Source("animego_fr", "AnimeGo (.fr)", "Аниме. Часть плееров открывается только с российского адреса."),
        Source("jutsu_net", "Jut.su (.net)", "Аниме. Плееры — только с российского адреса."),
        Source("jutsu_tv", "Jut.su (.tv)", "Аниме. Плееры — только с российского адреса."),
        Source("jutsu_org", "Jut.su (.org)", "Аниме. Оригинальная озвучка — только по подписке площадки."),
        Source("yummy", "YummyAnime", "Аниме."),
        Source("anidub", "AniDub", "Аниме. Нестабильно: на проверке нашёл тайтл, но не отдал серий."),
        Source(
            "kinopub", "kinopub",
            "Фильмы и сериалы. Нужен ваш ключ (access-токен); продлевать его приложение не умеет, " +
                "и обычно нужен VPN.",
            keyEnv = "KINOPUB_TOKEN", keyLabel = "Access-токен kinopub",
        ),
        Source(
            "filmix", "Filmix", "Фильмы и сериалы. Нужен ключ PRO+ для высокого качества.",
            keyEnv = "FILMIX_TOKEN", keyLabel = "Токен Filmix",
        ),
        Source(
            "lordfilm", "ЛордФильм", "Сериалы и фильмы через Alloha. Нужен ключ Alloha.",
            keyEnv = "LORDFILM_ALLOHA_TOKEN", keyLabel = "Токен Alloha",
        ),
    )

    /** Последний ответ движка, переведённый в [Source]. Пусто — движок ещё не отвечал. */
    @Volatile
    private var remote: List<Source> = emptyList()

    /**
     * Все источники: вшитые по своему порядку (пометки движка сильнее), затем новые —
     * скрипты и площадки, которых приложение не знало.
     */
    val all: List<Source>
        get() = merge(bundled, remote)

    fun of(id: String): Source? = all.firstOrNull { it.id == id }

    /** Слить вшитый список и ответ движка. Отдельно — ради теста. */
    fun merge(base: List<Source>, fromEngine: List<Source>): List<Source> {
        if (fromEngine.isEmpty()) return base
        val byId = fromEngine.associateBy { it.id }
        val known = base.map { b ->
            val r = byId[b.id] ?: return@map b
            // Имя и пометку для вшитых держим свои (они короче и проверены на телефоне),
            // ключ — свой, если движок о нём молчит; происхождение и версия — движка.
            b.copy(origin = r.origin, version = r.version, note = if (r.origin == "builtin") b.note else r.note,
                title = if (r.origin == "builtin") b.title else r.title,
                keyEnv = b.keyEnv ?: r.keyEnv, keyLabel = b.keyLabel ?: r.keyLabel)
        }
        val baseIds = base.map { it.id }.toSet()
        return known + fromEngine.filter { it.id !in baseIds }.sortedBy { it.title.lowercase() }
    }

    /** Ответ движка в источники: скрытые и сломанные — не для выбора. */
    fun fromEngine(list: List<ru.sorewa.kino.data.EngineSourceInfo>): List<Source> =
        list.filter { !it.hidden && it.broken.isEmpty() && it.id.isNotEmpty() }.map { info ->
            // Поле на экране одно: главный ключ — первая не «служебная» настройка, секретная первой.
            val main = info.settings.filter { !it.advanced }.let { s -> s.firstOrNull { it.secret } ?: s.firstOrNull() }
            Source(
                id = info.id, title = info.name.ifEmpty { info.id }, note = info.note,
                keyEnv = main?.env, keyLabel = main?.label, origin = info.origin, version = info.version,
            )
        }

    private const val PREFS = "engine_settings"
    private const val KEY = "sources"
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    /** Запомнить ответ движка: следующий запуск соберёт окружение и по своим площадкам. */
    fun remember(context: Context, list: List<ru.sorewa.kino.data.EngineSourceInfo>) {
        val next = fromEngine(list)
        remote = next
        context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit().putString(KEY, json.encodeToString(next)).apply()
    }

    /** Поднять снимок, сохранённый прошлым ответом движка. */
    fun restore(context: Context) {
        if (remote.isNotEmpty()) return
        val text = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY, null)
        if (text.isNullOrBlank()) return
        remote = try { json.decodeFromString<List<Source>>(text) } catch (_: Exception) { emptyList() }
    }
}

/**
 * Настройки «серверной» части — локального движка. Хранятся на устройстве,
 * превращаются в окружение процесса при каждом его запуске ([toEnv]); поэтому
 * чтобы изменение вступило в силу, движок перезапускается.
 *
 * Ключи площадок лежат в личном каталоге приложения открытым текстом — так же,
 * как токены сессии: защиты сильнее, чем у самого приложения, у них нет.
 */
@Serializable
data class EngineSettings(
    /** Включённые источники (идентификаторы из [EngineSources]). */
    val providers: List<String> = listOf(EngineLaunch.DEFAULT_PROVIDERS),
    /**
     * Прокси для всех обращений к площадкам: `socks5://хост:порт`, `http://…`.
     * Пусто — напрямую; VPN телефона работает и так, это для случая, когда его нет.
     */
    val proxy: String = "",
    /** Ключи по идентификатору источника. */
    val keys: Map<String, String> = emptyMap(),
    /** Подробный журнал движка (DEBUG): для разбора, почему источник молчит. */
    val debugLog: Boolean = false,
    /** Потолок кэша постеров, МБ. */
    val artCacheMb: Int = DEFAULT_CACHE_MB,
    /**
     * Не чаще, чем раз в столько часов перечитывать серии тайтла у площадки при
     * его открытии. Движок в фоне ничего не обходит: данные обновляются, только
     * когда вы сами открыли тайтл и с прошлого раза прошло больше этого срока.
     */
    val refreshHours: Int = DEFAULT_REFRESH_HOURS,
    /** Сколько находок просить у одной площадки за поиск. У части площадок это прямая цена поиска. */
    val searchPerSource: Int = DEFAULT_SEARCH_PER_SOURCE,
    /** Кадры серий по одному запросу на каждую: красиво, но на сериале в сотню серий — сотня запросов. */
    val thumbs: Boolean = false,
    /**
     * Подпись (User-Agent) для всех площадок. Пусто — по умолчанию: браузероподобная, с
     * явной пометкой приложения и его версии. Менять не советуем; что задано здесь —
     * уходит наружу как есть, на совести человека.
     */
    val userAgent: String = "",
    /** Подпись отдельной площадки по идентификатору источника; сильнее [userAgent]. */
    val userAgents: Map<String, String> = emptyMap(),
) {
    /** Источники, которые движок получит: только известные, по порядку списка, не пустой. */
    fun activeProviders(): List<String> {
        val on = providers.toSet()
        val known = EngineSources.all.map { it.id }.filter { it in on }
        return known.ifEmpty { listOf(EngineLaunch.DEFAULT_PROVIDERS) }
    }

    /**
     * Окружение движка по настройкам. Источник с ключом, но без ключа, всё равно
     * передаётся: движок отключит его сам и скажет причину в /engine/status —
     * человек увидит «нужен ключ», а не молчаливое отсутствие.
     */
    fun toEnv(): Map<String, String> {
        val env = linkedMapOf<String, String>()
        env["PROVIDERS"] = activeProviders().joinToString(",")
        if (validateProxy(proxy) == null && proxy.isNotBlank()) env["KINOPUB_PROXY"] = proxy.trim()
        for (id in activeProviders()) {
            val src = EngineSources.of(id) ?: continue
            val envName = src.keyEnv ?: continue
            keys[id]?.trim()?.takeIf { it.isNotEmpty() }?.let { env[envName] = it }
        }
        // Секрет OAuth-клиента kino.pub — для входа по коду. Его даёт человек: у kino.pub нет
        // публичного клиента, а чужой секрет в приложение не вшит. Только если kinopub включён.
        if ("kinopub" in activeProviders()) {
            keys[KINOPUB_SECRET_KEY]?.trim()?.takeIf { it.isNotEmpty() }?.let { env["KINOPUB_CLIENT_SECRET"] = it }
        }
        // Подписи, заданные человеком. Негодные (не ASCII, управляющие символы, слишком
        // длинные) не передаются — движок и сам их отбросил бы, но так причина видна на экране.
        userAgent.trim().takeIf { it.isNotEmpty() && validateUserAgent(it) == null }?.let { env["ENGINE_USER_AGENT"] = it }
        for (id in activeProviders()) {
            userAgents[id]?.trim()?.takeIf { it.isNotEmpty() && validateUserAgent(it) == null }
                ?.let { env[USER_AGENT_ENV_PREFIX + id.uppercase()] = it }
        }
        // Порядок опроса — порядок в списке источников: лучшая площадка первой, остальные
        // — только если у неё нашлось мало (так поиск стоит один запрос, а не по запросу
        // на каждую включённую площадку). Тот же порядок задаёт, чья карточка главнее.
        val active = activeProviders()
        env["PROVIDER_PRIORITY"] = active.withIndex().joinToString(",") { (i, id) -> "$id=${(100 - i).coerceAtLeast(1)}" }
        val per = searchPerSource.coerceIn(1, MAX_SEARCH_PER_SOURCE)
        env["ENGINE_SEARCH_PER_SOURCE"] = per.toString()
        env["ENGINE_SEARCH_MIN_HITS"] = minOf(per, SEARCH_ENOUGH_HITS).toString()
        env["ENGINE_REFRESH_HOURS"] = refreshHours.coerceIn(1, MAX_REFRESH_HOURS).toString()
        if (thumbs) env["ENGINE_THUMBS"] = "1"
        if (debugLog) env["LOG_LEVEL"] = "DEBUG"
        env["ART_CACHE_MB"] = artCacheMb.coerceIn(MIN_CACHE_MB, MAX_CACHE_MB).toString()
        return env
    }

    companion object {
        /** «Жирный» кэш: постеры, один раз полученные, не выкидываются и не качаются заново. */
        const val DEFAULT_CACHE_MB = 512
        const val MIN_CACHE_MB = 32
        const val MAX_CACHE_MB = 4096
        val CACHE_CHOICES = listOf(256, 512, 1024, 2048)

        /** Переменная окружения подписи одной площадки: ENGINE_UA_ANILIBRIA, ENGINE_UA_ANIMEGO_ME, … */
        const val USER_AGENT_ENV_PREFIX = "ENGINE_UA_"
        const val MAX_USER_AGENT_LEN = 512

        /** Ключ в [keys], под которым хранится секрет OAuth-клиента kino.pub (это не «источник»). */
        const val KINOPUB_SECRET_KEY = "kinopub_secret"

        const val DEFAULT_REFRESH_HOURS = 12
        const val MAX_REFRESH_HOURS = 24 * 30
        val REFRESH_CHOICES = listOf(6, 12, 24, 72)

        const val DEFAULT_SEARCH_PER_SOURCE = 4
        const val MAX_SEARCH_PER_SOURCE = 20
        val SEARCH_PER_SOURCE_CHOICES = listOf(2, 4, 6, 10)

        /** Сколько находок у площадки считается «достаточно» — к следующей идти не нужно. */
        const val SEARCH_ENOUGH_HITS = 3
    }
}

/**
 * Прокси-адрес годен для движка? null — да (или пусто), иначе — что не так.
 * Движок принимает http, https и socks5 с явным портом.
 */
fun validateProxy(raw: String): String? {
    val s = raw.trim()
    if (s.isEmpty()) return null
    val u = try { URI(s) } catch (_: Exception) { return "Адрес не разобран. Пример: socks5://192.168.1.5:1080" }
    if (u.scheme?.lowercase() !in setOf("http", "https", "socks5")) {
        return "Поддерживаются http://, https:// и socks5://"
    }
    if (u.host.isNullOrEmpty()) return "Не указан хост. Пример: socks5://192.168.1.5:1080"
    if (u.port !in 1..65535) return "Не указан порт. Пример: socks5://192.168.1.5:1080"
    return null
}

/**
 * Подпись годится для заголовка? null — да (или пусто), иначе — что не так. Печатный ASCII без
 * переводов строк и не длиннее [EngineSettings.MAX_USER_AGENT_LEN]: с другим заголовок
 * невалиден и каждый запрос площадки падал бы.
 */
fun validateUserAgent(raw: String): String? {
    val s = raw.trim()
    if (s.isEmpty()) return null
    if (s.length > EngineSettings.MAX_USER_AGENT_LEN) return "Длиннее ${EngineSettings.MAX_USER_AGENT_LEN} символов"
    if (s.any { it.code < 0x20 || it.code > 0x7e }) return "Только латиница, цифры и знаки, без переводов строк"
    return null
}

/** Хранение настроек: одна JSON-строка в SharedPreferences, читается синхронно при каждом запуске движка. */
object EngineSettingsStore {
    private const val PREFS = "engine_settings"
    private const val KEY = "settings"

    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    fun encode(s: EngineSettings): String = json.encodeToString(s)

    /** Битая или пустая запись — настройки по умолчанию: движок обязан подняться. */
    fun decode(text: String?): EngineSettings =
        if (text.isNullOrBlank()) EngineSettings() else try {
            json.decodeFromString(EngineSettings.serializer(), text)
        } catch (_: Exception) {
            EngineSettings()
        }

    fun load(context: Context): EngineSettings {
        // Источники, известные по прошлому ответу движка (свои скрипты), — до того, как
        // настройки превратятся в окружение: иначе свой источник отсеялся бы как чужой.
        EngineSources.restore(context)
        return decode(context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY, null))
    }

    /** commit: за записью сразу перезапуск движка, который прочтёт её заново. */
    fun save(context: Context, s: EngineSettings): Boolean =
        context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit().putString(KEY, encode(s)).commit()
}

/** Строка журнала движка (JSON slog) в читаемый вид: «06:44:48 INFO  сообщение». */
object EngineLogFormat {
    private val level = Regex("\"level\":\"([A-Z]+)\"")
    private val msg = Regex("\"msg\":\"((?:[^\"\\\\]|\\\\.)*)\"")
    private val time = Regex("\"time\":\"[^T\"]*T(\\d\\d:\\d\\d:\\d\\d)")
    private val extra = Regex("\"(err|причина|provider|id|code|path|status)\":(\"((?:[^\"\\\\]|\\\\.)*)\"|[0-9]+)")

    fun pretty(line: String): String {
        val m = msg.find(line) ?: return line
        val t = time.find(line)?.groupValues?.get(1).orEmpty()
        val l = level.find(line)?.groupValues?.get(1).orEmpty().padEnd(5)
        val text = m.groupValues[1].replace("\\\"", "\"").replace("\\n", " ")
        val tail = extra.findAll(line).joinToString(" ") { e ->
            "${e.groupValues[1]}=${e.groupValues[3].ifEmpty { e.groupValues[2] }}"
        }
        return listOf(t, l, text, tail).filter { it.isNotBlank() }.joinToString(" ")
    }

    /** Последние [n] строк журнала, читаемые. */
    fun tail(text: String, n: Int): List<String> =
        text.lineSequence().filter { it.isNotBlank() }.toList().takeLast(n).map(::pretty)
}
