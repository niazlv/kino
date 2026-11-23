package ru.sorewa.kino.data

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import ru.sorewa.kino.engine.ServerAddress
import java.io.IOException
import java.net.ConnectException
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLException

/**
 * Визитка сервера, `GET /api/v1/server` (без входа). По ней приложение
 * узнаёт, что по адресу живёт именно kino platform, как сервер себя
 * называет и какую регистрацию показывать.
 */
@Serializable
data class ServerInfo(
    val kind: String = "",
    val api: Int = 0,
    val name: String = "",
    /** invite — по коду приглашения, open — любой, closed — только админка. */
    val registration: String = REG_INVITE,
    val local: Boolean = false,
) {
    val openRegistration: Boolean get() = registration == REG_OPEN
    val canRegister: Boolean get() = registration != REG_CLOSED

    companion object {
        const val KIND = "kino-platform"
        const val REG_INVITE = "invite"
        const val REG_OPEN = "open"
        const val REG_CLOSED = "closed"

        /**
         * Сервер старше визитки (нет /api/v1/server, но жив /api/v1/health):
         * такие умели только регистрацию по приглашению.
         */
        val LEGACY = ServerInfo(kind = KIND, api = 1)
    }
}

/**
 * Проверка адреса, который человек ввёл как свой сервер, — до того как
 * приложение переключится на него и перезапустится.
 */
object ServerProbe {

    sealed class Result {
        /** Сервер kino platform. [url] — итоговый origin (после переадресации http→https). */
        data class Ok(val url: String, val info: ServerInfo) : Result()

        /** Не сервер или не отвечает; [message] — для человека, [network] — не дошли до сервера. */
        data class Fail(val message: String, val network: Boolean = false) : Result()
    }

    private val json = Json { ignoreUnknownKeys = true }

    /** Короткие сроки: человек ждёт на экране ввода, а не в фоне. */
    private val defaultClient: OkHttpClient by lazy {
        OkHttpClient.Builder()
            .connectTimeout(8, TimeUnit.SECONDS)
            .readTimeout(8, TimeUnit.SECONDS)
            .callTimeout(15, TimeUnit.SECONDS)
            .build()
    }

    /** Почему http к этому серверу нельзя (см. ServerAddress.allowsCleartext). */
    const val CLEARTEXT_PUBLIC = "По http — только сервер в домашней сети (192.168…, 10…, имя.local). " +
        "Для сервера в интернете нужен https."

    suspend fun probe(input: String, client: OkHttpClient = defaultClient): Result = withContext(Dispatchers.IO) {
        val url = ServerAddress.normalize(input)
            ?: return@withContext Result.Fail("Это не похоже на адрес. Пример: kino.example.com")
        if (!ServerAddress.allowsCleartext(url)) return@withContext Result.Fail(CLEARTEXT_PUBLIC)
        val first = probeUrl(url, client)
        // Схему не писали, а домашний сервер без сертификата: https не
        // ответил — пробуем тот же адрес по http (в домашней сети это можно).
        val bare = !input.contains("://")
        val plain = "http://" + url.removePrefix("https://")
        if (first is Result.Fail && first.network && bare && ServerAddress.allowsCleartext(plain)) {
            val second = probeUrl(plain, client)
            if (second is Result.Ok) return@withContext second
        }
        first
    }

    private fun probeUrl(url: String, client: OkHttpClient): Result =
        try {
            client.newCall(Request.Builder().url("$url/api/v1/server").build()).execute().use { rsp ->
                // Переадресация (обычно http → https) — запоминаем, куда привела:
                // дальше ходим сразу туда.
                val final = ServerAddress.normalize(rsp.request.url.toString().substringBefore("/api/v1/")) ?: url
                when {
                    rsp.isSuccessful -> parse(rsp.body?.string())
                        ?.let { Result.Ok(final, it) }
                        ?: notKino()
                    // Сервер старше визитки: узнаём его по /api/v1/health.
                    rsp.code == 404 -> legacy(final, client)
                    else -> Result.Fail("Сервер ответил ошибкой ${rsp.code}. Это точно адрес kino platform?")
                }.let { r ->
                    // Переадресовал с https на http в интернет — туда не идём.
                    if (r is Result.Ok && !ServerAddress.allowsCleartext(r.url)) Result.Fail(CLEARTEXT_PUBLIC) else r
                }
            }
        } catch (e: IOException) {
            Result.Fail(describe(e), network = true)
        } catch (e: IllegalArgumentException) {
            Result.Fail("Это не похоже на адрес. Пример: kino.example.com")
        }

    /** Ответ визитки → её содержимое; null — это не визитка kino platform. */
    fun parse(body: String?): ServerInfo? {
        if (body.isNullOrBlank()) return null
        val info = try { json.decodeFromString(ServerInfo.serializer(), body) } catch (_: Exception) { return null }
        return info.takeIf { it.kind == ServerInfo.KIND }
    }

    private fun legacy(url: String, client: OkHttpClient): Result =
        client.newCall(Request.Builder().url("$url/api/v1/health").build()).execute().use { rsp ->
            val body = rsp.body?.string().orEmpty()
            // {"status":"ok"} — так отвечает core любой версии (и движок).
            if (rsp.isSuccessful && body.contains("\"status\"")) Result.Ok(url, ServerInfo.LEGACY) else notKino()
        }

    private fun notKino() = Result.Fail("По этому адресу не сервер kino platform.")

    private fun describe(e: IOException): String = when (e) {
        is UnknownHostException -> "Сервер не найден. Проверьте адрес (и VPN, если сервер домашний)."
        is SSLException -> "Не удалось защищённое соединение: сертификат сервера не принят."
        is SocketTimeoutException -> "Сервер не отвечает."
        is ConnectException -> "Сервер не отвечает: соединение отклонено."
        else -> "Нет связи с сервером: ${e.message?.take(80) ?: e.javaClass.simpleName}"
    }
}
