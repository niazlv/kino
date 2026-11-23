package ru.sorewa.kino.data

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response

/**
 * Сессия в запросах и её продление.
 *
 * Сервер говорит «сессия кончилась» двумя способами: 401 — отозвана
 * («Выйти на всех», блокировка) или не узнана, 410 `expired` — истекла
 * (сессия устройства живёт 30 дней и на ходу не продлевается). На оба
 * пара обновляется по refresh-токену, запрос повторяется один раз со
 * свежим Cookie. Authenticator OkHttp для этого не годится: его зовут
 * только на 401, и через 30 дней после входа всё падало бы с 410 — ошибки
 * на каждом экране вместо продления или входа.
 *
 * 410 `expired` бывает не только про сессию: истёкший код устройства
 * (`/auth/device/{code}`), приглашение. Поэтому сначала /me с тем же
 * токеном: жив — ответ отдаётся как есть, без лишней ротации пары.
 *
 * Сервер отказал в продлении (401/403/410 на /auth/refresh, или refresh-
 * токена нет вовсе — парольная сессия) — [Tokens.dead], на экран входа,
 * но только когда /me подтверждает, что сессия мертва: 401 от неверного
 * старого пароля сессию не роняет.
 *
 * Продление единовременное: параллельные запросы ждут замка, и кто
 * дождался — видит, что токен уже сменили, и просто повторяет запрос.
 */
class SessionRenewal(
    baseUrl: String,
    private val bare: OkHttpClient,
    private val json: Json,
    private val tokens: Tokens,
) : Interceptor {

    interface Tokens {
        /** Токен, с которым уходят запросы сейчас; пусто — входа нет. */
        val current: String
        /** Refresh-токен с диска; пусто — продлевать нечем. */
        fun refreshToken(): String?
        /** Новая пара: сначала в клиент, потом на диск. */
        fun save(pair: Api.TokenPair)
        /** Сессия мертва и не продлевается — на вход. Токен клиента уже пуст. */
        fun dead()
    }

    enum class Renewal {
        /** Пара обновлена (или её уже обновил соседний запрос). */
        RENEWED,
        /** /me с тем же токеном жив: 410 был не про сессию. */
        ALIVE,
        /** Сервер отказал: продлевать нечем. */
        REJECTED,
        /** Сеть или сервер лежит — ничего не известно, сессию не трогаем. */
        FAILED,
    }

    private val base = baseUrl.trimEnd('/')
    private val api: HttpUrl = "$base/api/v1/".toHttpUrl()
    private val jsonMedia = "application/json; charset=utf-8".toMediaType()
    private val lock = Any()

    override fun intercept(chain: Interceptor.Chain): Response {
        val req = chain.request()
        val sent = tokens.current
        val first = chain.proceed(withSession(req, sent))
        if (sent.isEmpty() || !ours(req.url)) return first
        val expired = isExpired(first)
        if (first.code != 401 && !expired) return first
        return when (renew(sent, probeFirst = expired)) {
            Renewal.RENEWED -> {
                val now = tokens.current
                if (now.isEmpty()) return first
                first.close()
                chain.proceed(withSession(req, now))
            }
            else -> first
        }
    }

    /**
     * Продлить сессию, с которой ушёл запрос ([sent]). [probeFirst] — сначала
     * спросить /me, она ли мертва (для 410: он бывает и не про сессию).
     */
    fun renew(sent: String, probeFirst: Boolean): Renewal {
        synchronized(lock) {
            val now = tokens.current
            // Пока ждали замка, соседи уже продлили пару — или вышли.
            if (now != sent) return if (now.isEmpty()) Renewal.REJECTED else Renewal.RENEWED
            if (sent.isEmpty()) return Renewal.REJECTED
            var probe = Probe.UNKNOWN
            if (probeFirst) {
                probe = probe(sent)
                if (probe == Probe.ALIVE) return Renewal.ALIVE
            }
            val r = refresh()
            if (r == Renewal.REJECTED) {
                if (probe == Probe.UNKNOWN) probe = probe(sent)
                if (probe == Probe.DEAD) tokens.dead()
            }
            return r
        }
    }

    private enum class Probe { ALIVE, DEAD, UNKNOWN }

    /** /me с этим токеном, мимо продления: жива ли сессия. */
    private fun probe(token: String): Probe = try {
        val req = Request.Builder().url("$base/api/v1/me")
            .header("Cookie", "kino_session=$token").get().build()
        bare.newCall(req).execute().use { resp ->
            when {
                resp.isSuccessful -> Probe.ALIVE
                resp.code == 401 || resp.code == 403 || isExpired(resp) -> Probe.DEAD
                else -> Probe.UNKNOWN
            }
        }
    } catch (_: Exception) {
        Probe.UNKNOWN
    }

    private fun refresh(): Renewal {
        val rt = tokens.refreshToken()
        if (rt.isNullOrEmpty()) return Renewal.REJECTED
        return try {
            val body = json.encodeToString(RefreshReq.serializer(), RefreshReq(rt)).toRequestBody(jsonMedia)
            val req = Request.Builder().url("$base/api/v1/auth/refresh").post(body).build()
            bare.newCall(req).execute().use { resp ->
                when {
                    resp.isSuccessful -> {
                        val pair = json.decodeFromString(Api.TokenPair.serializer(), resp.body!!.string())
                        if (pair.token.isEmpty()) return Renewal.FAILED
                        tokens.save(pair)
                        Renewal.RENEWED
                    }
                    // 401 — отозвана, 403 — сессия без refresh, 410 — refresh истёк.
                    resp.code == 401 || resp.code == 403 || resp.code == 410 -> Renewal.REJECTED
                    else -> Renewal.FAILED
                }
            }
        } catch (_: Exception) {
            Renewal.FAILED
        }
    }

    /** Только свой API: картинки и вызовы приложения, не чужие адреса. */
    private fun ours(url: HttpUrl): Boolean =
        url.scheme == api.scheme && url.host == api.host && url.port == api.port &&
            url.encodedPath.startsWith(api.encodedPath)

    private fun withSession(req: Request, token: String): Request =
        if (token.isEmpty()) req else req.newBuilder().header("Cookie", "kino_session=$token").build()

    @Serializable
    private data class RefreshReq(val refreshToken: String)

    @Serializable
    private data class ErrorBody(val error: String = "")

    private fun isExpired(resp: Response): Boolean {
        if (resp.code != 410) return false
        return try {
            json.decodeFromString(ErrorBody.serializer(), resp.peekBody(4096).string()).error == "expired"
        } catch (_: Exception) {
            false
        }
    }
}
