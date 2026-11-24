package ru.sorewa.kino.data

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.time.OffsetDateTime
import java.util.concurrent.TimeUnit

/** Ошибка API: статус и машинный код ошибки сервера ({ "error": "..." }). */
class ApiError(val status: Int, val code: String, message: String) : Exception(message) {
    /** Сессия кончилась — повод показать вход, а не ошибку. */
    val unauthorized: Boolean get() = status == 401 || status == 403
    /**
     * Сессия не годится: 401/403 или 410 `expired` — так сервер отвечает на
     * истёкшую сессию (30 дней без продления на ходу). Сюда ответ доходит,
     * только если продление не помогло (см. [SessionRenewal]).
     */
    val sessionDead: Boolean get() = unauthorized || (status == 410 && code == "expired")
    /** Ответа не было вовсе: нет сети или сервер недоступен. */
    val offline: Boolean get() = status == 0

    companion object {
        fun offline(e: Exception) = ApiError(0, "offline", "нет связи с сервером: ${e.message ?: "кратко"}")
    }
}

/** Разбор времени сервера «2026-11-13T02:00:00.000Z» в миллисEpoch; не вышло — 0. */
fun parseIsoTime(s: String?): Long {
    if (s.isNullOrEmpty()) return 0
    return try {
        OffsetDateTime.parse(s).toInstant().toEpochMilli()
    } catch (_: Exception) {
        0
    }
}

/**
 * Клиент публичного API. Сессия носится в заголовке Cookie — тот же
 * kino_session, что и у витрины: requireUser на сервере читает только cookie,
 * device-flow выдаёт ровно такой же токен.
 *
 * 401 (отозвана) и 410 `expired` (истекла) — повод обновить пару по
 * refresh-токену (ротация) и повторить запрос один раз; не вышло — на вход.
 * Всё это — [SessionRenewal].
 */
class Api(
    private val store: TokenStore,
    private val isTv: Boolean,
    baseUrl: String,
    /** Локальный режим: запрос ждёт, пока поднимется движок (см. EngineGate). */
    private val gate: okhttp3.Interceptor? = null,
) {
    val json = Json { ignoreUnknownKeys = true; encodeDefaults = false; explicitNulls = false }
    private val jsonMedia = "application/json; charset=utf-8".toMediaType()
    private val base = baseUrl.trimEnd('/')

    /**
     * Адрес, на который смотрит клиент: сервер или локальный движок (порт у него
     * свой на каждый запуск приложения). Нужен тем, кто строит ссылки сам, — качалке.
     */
    val baseUrl: String get() = base

    /** Токен читается на сетевом потоке без корутин — volatile-поле. */
    @Volatile var currentToken: String = ""

    fun attachToken(token: String) { currentToken = token }

    /**
     * Сессия мертва и не продлевается (сервер отказал в refresh, /me это
     * подтвердил) — приложение уводит на вход. Зовётся с сетевого потока;
     * токен клиента к этому моменту уже пуст.
     */
    @Volatile var onSessionDead: (() -> Unit)? = null

    /**
     * Без сессии и без продления — для самого обновления, входа и выхода
     * чужой сессии. Через [client] ответ 401 на /auth/refresh снова звал бы
     * продление, а оно ждёт замка, который держит этот же refresh: все
     * запросы висели навсегда, приложение — вечные скелетоны на каждом
     * запуске (после «Выйти на всех», блокировки, истёкшего refresh).
     */
    private val bare: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS)
        .writeTimeout(20, TimeUnit.SECONDS)
        .apply { gate?.let { addInterceptor(it) } }
        .build()

    /** Cookie в каждом запросе и продление по 401 и 410 `expired` — см. [SessionRenewal]. */
    private val renewal = SessionRenewal(base, bare, json, object : SessionRenewal.Tokens {
        override val current: String get() = currentToken
        override fun refreshToken(): String? = runBlocking { store.session().refreshToken }
        override fun save(pair: TokenPair) {
            // Сначала в клиент: повтор запроса уйдёт уже с новым токеном.
            currentToken = pair.token
            runBlocking {
                store.saveSession(
                    pair.token, pair.refreshToken,
                    parseIsoTime(pair.expiresAt), parseIsoTime(pair.refreshExpiresAt),
                )
            }
        }
        override fun dead() {
            currentToken = ""
            onSessionDead?.invoke()
        }
    })

    private val client: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(40, TimeUnit.SECONDS)
        .writeTimeout(20, TimeUnit.SECONDS)
        // Ворота первыми: ждать движок надо до продления сессии и до самого запроса.
        .apply { gate?.let { addInterceptor(it) } }
        .addInterceptor(renewal)
        .build()

    /** Тот же клиент с сессией — для картинок (постеры и кадры только авторизованным). */
    val http: OkHttpClient get() = client

    /**
     * Выпуск тикета ждёт площадку дольше обычных вызовов: сервер сам ведёт
     * их до 25 с, потолок здесь 45 с — как в витрине (40 с + запас на дорогу).
     */
    private val clientTimed: OkHttpClient = client.newBuilder()
        .callTimeout(45, TimeUnit.SECONDS)
        .build()

    /**
     * Обновление пары токенов (например, перед запуском качалки: утилита
     * сама сессию не продлит). Возвращает true, если сессия продлена.
     * Обновление единовременное: параллельные запросы ждут и повторяются
     * уже с новым токеном.
     */
    suspend fun refreshSession(): Boolean = withContext(Dispatchers.IO) {
        val sent = currentToken
        sent.isNotEmpty() && renewal.renew(sent, probeFirst = false) == SessionRenewal.Renewal.RENEWED
    }

    // ---- низкоуровневые вызовы ----

    private suspend fun call(
        method: String,
        path: String,
        bodyJson: String? = null,
        timeoutMs: Long = 0,
    ): String = withContext(Dispatchers.IO) {
        try {
            // /engine/* — служебное пространство локального движка, вне /api/v1.
            val url = if (path.startsWith("/engine/")) "$base$path" else "$base/api/v1$path"
            val builder = Request.Builder().url(url)
            when (method) {
                "POST" -> builder.post((bodyJson ?: "").toRequestBody(jsonMedia))
                "PUT" -> builder.put((bodyJson ?: "").toRequestBody(jsonMedia))
                "DELETE" -> if (bodyJson != null) builder.delete(bodyJson.toRequestBody(jsonMedia)) else builder.delete()
                else -> builder.get()
            }
            val clientForCall = when {
                // Проверка площадки на устройстве идёт минутами: свой потолок, не общий.
                timeoutMs > 45_000 -> client.newBuilder()
                    .callTimeout(timeoutMs, TimeUnit.MILLISECONDS)
                    .readTimeout(timeoutMs, TimeUnit.MILLISECONDS)
                    .build()
                timeoutMs > 0 -> clientTimed
                else -> client
            }
            clientForCall.newCall(builder.build()).execute().use { resp ->
                if (resp.code == 204) return@use ""
                val text = resp.body?.string() ?: ""
                if (!resp.isSuccessful) {
                    val code = try { json.decodeFromString(ErrorBody.serializer(), text).error } catch (_: Exception) { "" }
                    throw ApiError(resp.code, code.ifEmpty { "http_${resp.code}" }, text.ifEmpty { "HTTP ${resp.code}" })
                }
                text
            }
        } catch (e: ApiError) {
            throw e
        } catch (e: IOException) {
            throw ApiError.offline(e)
        }
    }

    private suspend inline fun <reified T> get(path: String): T =
        json.decodeFromString<T>(call("GET", path))

    /**
     * B — тип тела, он должен остаться reified: kotlinx.serialization не
     * умеет сериализовать Any (сериализатор ищется в рантайме по типу).
     */
    private suspend inline fun <reified B, reified T> post(path: String, body: B? = null, timeoutMs: Long = 0): T {
        val raw = if (body == null) "{}" else json.encodeToString(body)
        return json.decodeFromString<T>(call("POST", path, raw, timeoutMs))
    }

    private suspend inline fun <reified B, reified T> put(path: String, body: B): T =
        json.decodeFromString<T>(call("PUT", path, json.encodeToString(body)))

    private suspend fun unit(method: String, path: String) {
        call(method, path)
    }

    private suspend inline fun <reified B> unit(method: String, path: String, body: B) {
        val raw = if (body is String) body else json.encodeToString(body)
        call(method, path, raw)
    }

    @Serializable
    private data class ErrorBody(val error: String = "")

    @Serializable
    data class TokenPair(
        val token: String,
        val refreshToken: String,
        val expiresAt: String = "",
        val refreshExpiresAt: String = "",
    )

    // ---- эндпоинты ----

    suspend fun me(): Me = get("/me")

    /** Состояние локального движка: какие площадки подключены, какие нет и почему. */
    suspend fun engineStatus(): EngineStatus = get("/engine/status")

    /** Начать вход в kinopub по коду: движок просит у kino.pub код, человек подтверждает его на сайте. */
    suspend fun kinopubLoginBegin(): KinopubLoginCode = post<Unit, KinopubLoginCode>("/engine/kinopub/login")

    /** Где вход. Движок сам не чаще интервала kino.pub ходит на авторизацию — спрашивать можно часто. */
    suspend fun kinopubLoginPoll(): KinopubLoginState = get("/engine/kinopub/login")

    /** Выйти из kinopub: сессия в движке стирается. */
    suspend fun kinopubLogout() = unit("DELETE", "/engine/kinopub/login")

    /** Все площадки движка: на Go, скрипты из сборки и свои, с состоянием (ADR-0017). */
    suspend fun engineSources(): EngineSourceList = get("/engine/sources")

    /** Текст площадки-скрипта: свой, иначе вшитый. У площадки на Go файла нет — ApiError 404. */
    suspend fun engineSourceFile(id: String): String = call("GET", "/engine/sources/${enc(id)}/file")

    /** Поставить площадку-скрипт (текст .js). Сломанный файл не ставится: ApiError с причиной. */
    suspend fun installEngineSource(text: String): EngineSourceInstalled =
        json.decodeFromString(call("POST", "/engine/sources", text))

    /** Удалить свою правку: вернётся вшитая площадка (если была). */
    suspend fun removeEngineSource(id: String) = unit("DELETE", "/engine/sources/${enc(id)}")

    /** Живые проверки площадки — те же, что у разработчиков (sourcekit check). Десяток запросов к площадке. */
    suspend fun checkEngineSource(req: EngineSourceCheckReq): EngineSourceCheck =
        json.decodeFromString(call("POST", "/engine/sources/check", json.encodeToString(req), timeoutMs = 200_000))

    private fun enc(s: String) = java.net.URLEncoder.encode(s, "UTF-8")

    suspend fun register(invite: String, login: String, password: String, displayName: String): Me =
        post("/auth/register", RegisterReq(invite, login, password, displayName))

    @Serializable
    private data class RegisterReq(val invite: String, val login: String, val password: String, val displayName: String)

    /**
     * Вход по паролю. Токен приходит в Set-Cookie (requireUser читает только
     * cookie, поэтому так и носим его дальше). Возвращает токен и время конца
     * сессии; refresh-токена у парольного входа нет — пара заводится сразу
     * после device-flow (см. AuthRepository).
     */
    suspend fun login(login: String, password: String): LoginRsp = withContext(Dispatchers.IO) {
        val raw = json.encodeToString(LoginReq(login, password, deviceKind()))
        val req = Request.Builder().url("$base/api/v1/auth/login")
            .post(raw.toRequestBody(jsonMedia)).build()
        try {
            bare.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) {
                    val text = resp.body?.string() ?: ""
                    val code = try { json.decodeFromString(ErrorBody.serializer(), text).error } catch (_: Exception) { "" }
                    throw ApiError(resp.code, code.ifEmpty { "http_${resp.code}" }, code.ifEmpty { "HTTP ${resp.code}" })
                }
                // Токен — из Set-Cookie (HttpOnly с сервера, но как транспорт
                // заголовок ответа нам виден).
                val token = resp.headers("Set-Cookie")
                    .firstOrNull { it.startsWith("kino_session=") && !it.contains("kino_session=;") }
                    ?.substringAfter("kino_session=")?.substringBefore(";") ?: ""
                if (token.isEmpty()) throw ApiError(500, "no_token", "сервер не выдал сессию")
                val body = resp.body?.string() ?: ""
                val expiresAt = try { json.decodeFromString(LoginBody.serializer(), body).expiresAt } catch (_: Exception) { "" }
                LoginRsp(token, expiresAt)
            }
        } catch (e: ApiError) { throw e }
        catch (e: IOException) { throw ApiError.offline(e) }
    }

    @Serializable
    private data class LoginBody(val expiresAt: String = "")

    data class LoginRsp(val token: String, val expiresAtIso: String)

    suspend fun logout() = unit("POST", "/auth/logout")

    /**
     * Выход ДРУГОЙ сессии этого же устройства — перенесённой cookie старого
     * приложения, когда её сменила своя пара. Мимо [SessionRenewal] (он
     * подставил бы текущий токен) и без продления: не вышло — та сама истечёт.
     */
    suspend fun logoutSession(token: String) = withContext(Dispatchers.IO) {
        try {
            val req = Request.Builder().url("$base/api/v1/auth/logout")
                .header("Cookie", "kino_session=$token")
                .post(ByteArray(0).toRequestBody(null))
                .build()
            bare.newCall(req).execute().close()
        } catch (_: Exception) { }
    }

    @Serializable
    private data class LoginReq(val login: String, val password: String, val device: String)

    @Serializable
    private data class DeviceReq(val device: String)

    @Serializable
    private data class DevicePollReq(val deviceCode: String, val cookie: Boolean = false)

    suspend fun deviceBegin(): DeviceBegin = post("/auth/device", DeviceReq(deviceKind()))

    /** Вернёт пару токенов или кинет ApiError с кодом authorization_pending. */
    suspend fun devicePoll(deviceCode: String): TokenPair =
        post("/auth/device/token", DevicePollReq(deviceCode))

    suspend fun deviceInfo(userCode: String): DeviceAuthInfo = get("/auth/device/${urlenc(userCode)}")
    suspend fun approveDevice(userCode: String) = unit("POST", "/auth/device/${urlenc(userCode)}/approve")
    suspend fun denyDevice(userCode: String) = unit("POST", "/auth/device/${urlenc(userCode)}/deny")

    suspend fun continueWatching(offset: Int = 0): Page<ContinueItem> =
        get(if (offset > 0) "/continue?offset=$offset" else "/continue")

    suspend fun search(q: String, offset: Int = 0): Page<Title> =
        get("/search?q=${urlenc(q)}${if (offset > 0) "&offset=$offset" else ""}")

    suspend fun suggest(q: String, limit: Int = 6): Page<Title> =
        get("/search/suggest?q=${urlenc(q)}&limit=$limit")

    suspend fun searchHistory(): SearchHistoryRsp = get("/search/history")
    suspend fun rememberSearch(q: String): SearchHistoryRsp =
        post("/search/history", RawQuery(q))
    suspend fun forgetSearch(q: String): SearchHistoryRsp =
        json.decodeFromString(call("DELETE", "/search/history?q=${urlenc(q)}"))
    suspend fun clearSearchHistory(): SearchHistoryRsp =
        json.decodeFromString(call("DELETE", "/search/history"))

    @Serializable
    private data class RawQuery(val query: String)

    suspend fun title(id: Long): TitleDetail = get("/titles/$id")

    /**
     * «Обновить»: спросить о тайтле включённые площадки, которых у него нет, и
     * перечитать серии всех его площадок. Есть только у локального движка (у сервера —
     * 404). Площадки медленные — потолок свой, как у проверки площадки.
     */
    suspend fun refreshTitle(id: Long): TitleRefresh =
        post<Unit, TitleRefresh>("/titles/$id/refresh", timeoutMs = 100_000)

    suspend fun setPrefs(titleId: Long, patch: PrefsPatch): Prefs =
        put("/titles/$titleId/prefs", patch)

    suspend fun marks(titleId: Long): Marks = get("/titles/$titleId/marks")
    suspend fun setMarks(titleId: Long, m: Marks): Marks = put("/titles/$titleId/marks", m)

    suspend fun list(kind: String, offset: Int = 0): Page<Title> =
        get("/lists/$kind${if (offset > 0) "?offset=$offset" else ""}")

    suspend fun browse(kind: String?, genre: String?, sort: String?, page: Int): Page<Title> {
        val q = buildList {
            kind?.let { add("kind=${urlenc(it)}") }
            genre?.let { add("genre=${urlenc(it)}") }
            sort?.let { add("sort=${urlenc(it)}") }
            if (page > 1) add("page=$page")
        }.joinToString("&")
        return get("/browse${if (q.isEmpty()) "" else "?$q"}")
    }

    suspend fun genres(): Page<String> = get("/genres")

    @Serializable
    private data class WatchedReq(val watched: Boolean)

    @Serializable
    data class WatchedRsp(val watched: Boolean = false, val episodes: Int = 0)

    suspend fun setWatched(episodeId: Long, watched: Boolean): WatchedRsp =
        put("/progress/$episodeId/watched", WatchedReq(watched))

    suspend fun setSeasonWatched(titleId: Long, season: Int, watched: Boolean): WatchedRsp =
        put("/titles/$titleId/seasons/$season/watched", WatchedReq(watched))

    suspend fun newEpisodes(onlyNew: Boolean, offset: Int = 0): Page<NewEpisodeItem> {
        val q = buildList {
            if (onlyNew) add("only=new")
            if (offset > 0) add("offset=$offset")
        }.joinToString("&")
        return get("/new-episodes${if (q.isEmpty()) "" else "?$q"}")
    }

    suspend fun history(page: Int, kind: String?, q: String?): Page<HistoryItem> {
        val parts = buildList {
            if (page > 1) add("page=$page")
            kind?.let { add("kind=${urlenc(it)}") }
            q?.let { add("q=${urlenc(it)}") }
        }
        return get("/history${if (parts.isEmpty()) "" else "?${parts.joinToString("&")}"}")
    }

    /**
     * Выпуск тикета. Сервер сам не ждёт площадку дольше 25 с; здесь потолок
     * 40 с — как в витрине: крутилка без конца хуже честной ошибки.
     */
    suspend fun playback(episodeId: Long, choice: PlaybackChoice, maxQuality: Int, codecs: List<String>): Playback {
        val body = json.encodeToString(
            PlaybackReq(
                codecs = codecs,
                maxHeight = maxQuality,
                supportsHls = true,
                source = choice.source,
                strict = choice.strict,
                variant = choice.variant,
                variantLabel = choice.variantLabel,
                voice = choice.voice,
                avoid = choice.avoid,
            )
        )
        return json.decodeFromString(call("POST", "/playback/$episodeId", body, timeoutMs = 45_000))
    }

    @Serializable
    private data class PlaybackReq(
        val codecs: List<String>,
        val maxHeight: Int,
        val supportsHls: Boolean,
        val source: String? = null,
        val strict: Boolean = false,
        val variant: String? = null,
        val variantLabel: String? = null,
        val voice: String? = null,
        val avoid: List<String> = emptyList(),
    )

    suspend fun voices(episodeId: Long): VoiceList = get("/episodes/$episodeId/voices")

    /** Ключи озвучек по подписям дорожек потока: правило склейки и
     *  псевдонимы студий — на сервере, приложение их не повторяет. */
    suspend fun voiceKeys(labels: List<String>): VoiceKeys =
        post<VoiceKeysRequest, VoiceKeys>("/voices/keys", VoiceKeysRequest(labels))

    /**
     * Манифест скачивания: качества и дорожки серии — для диалога качалки
     * (контракт api/openapi/download-manifest.md).
     */
    suspend fun manifest(episodeId: Long): DownloadManifest =
        get("/episodes/$episodeId/manifest")

    @Serializable
    data class DownloadManifest(
        val v: Int = 1,
        val title: String = "",
        val season: Int = 0,
        val episode: Int = 0,
        val filename: String = "",
        val protocol: String = "HLS",
        val url: String = "",
        val expiresAt: String = "",
        val renditions: List<Rendition> = emptyList(),
        val audios: List<Track> = emptyList(),
        val subtitles: List<Track> = emptyList(),
    )
    suspend fun skips(episodeId: Long, source: String?, variant: String?): List<SkipSegment> {
        val q = buildList {
            source?.let { add("source=${urlenc(it)}") }
            variant?.let { add("variant=${urlenc(it)}") }
        }.joinToString("&")
        val rsp = get<SkipList>(if (q.isEmpty()) "/episodes/$episodeId/skips" else "/episodes/$episodeId/skips?$q")
        return rsp.segments
    }

    @Serializable
    private data class SkipList(val segments: List<SkipSegment> = emptyList())

    suspend fun progress0(episodeId: Long): ProgressState = get("/progress/$episodeId")

    /** clientTs тот же, что встал в очередь StateStore, — ack вычистит своё. */
    suspend fun progress(episodeId: Long, positionMs: Long, durationMs: Long, clientTs: String = nowIso()) =
        unit("PUT", "/progress/$episodeId", ProgressReport(positionMs, durationMs, clientTs, deviceId()))

    /** Своё устройство для слияния прогресса; пусто — сервер возьмёт сессию. */
    private suspend fun deviceId(): String? =
        store.session().deviceId.takeIf { it.isNotEmpty() }

    suspend fun telemetry(payload: Map<String, kotlinx.serialization.json.JsonElement>) =
        unit("POST", "/telemetry/session", json.encodeToString(payload))

    suspend fun changePassword(old: String, new: String) {
        unit("PUT", "/me/password", PasswordReq(old, new))
    }

    @Serializable
    private data class PasswordReq(val oldPassword: String, val newPassword: String)

    // Контейнер один на оба входа; ТВ это или телефон — решает активность (KinoApp.deviceKind).
    fun deviceKind(): String = if (isTv || ru.sorewa.kino.KinoApp.deviceKind == "tv") "tv" else "android"

    fun artUrl(titleId: Long): String = "$base/api/v1/titles/$titleId/art"

    /**
     * Адреса потока и субтитров сервер отдаёт относительными («/stream/tkt_…»):
     * витрине их достраивает браузер, ExoPlayer же принимал такой путь за
     * файл на устройстве — и ни одна серия не играла.
     */
    fun absolute(url: String): String = when {
        url.startsWith("http://") || url.startsWith("https://") -> url
        url.startsWith("/") -> base + url
        else -> "$base/$url"
    }
    fun shotUrl(episodeId: Long): String = "$base/api/v1/episodes/$episodeId/art"

    private fun urlenc(s: String): String =
        java.net.URLEncoder.encode(s, "UTF-8").replace("+", "%20")

    fun nowIso(): String =
        java.time.Instant.now().toString()
}

/** Человекочитаемая ошибка для экранов вне плеера. */
fun describeApiError(e: Exception): String = when (e) {
    is ApiError -> when {
        e.offline -> "нет связи с сервером"
        e.unauthorized -> "неверный логин или пароль"
        e.code == "bad_invite" -> "приглашение не подходит"
        e.code == "taken" -> "такой логин уже занят"
        else -> e.message?.take(120) ?: "ошибка ${e.status}"
    }
    else -> e.message ?: "неизвестная ошибка"
}
