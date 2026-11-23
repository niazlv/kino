package ru.sorewa.kino.data

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

/**
 * Продление сессии: сервер на истёкшую сессию отвечает 410 `expired`, а не
 * 401, — и на неё клиент тоже должен обновить пару и повторить запрос.
 * Сервер здесь — интерсептор в самом OkHttp: в сеть ничего не уходит.
 */
class SessionRenewalTest {

    private val base = "http://kino.test"
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = false; explicitNulls = false }

    /** Маленький core: сессии, refresh-токены и ответы как у httpuser. */
    private class FakeCore : Interceptor {
        /** Живые сессии. */
        val alive: MutableSet<String> = Collections.synchronizedSet(mutableSetOf())
        /** Истёкшие — requireUser отвечает на них 410 `expired`. */
        val expired: MutableSet<String> = Collections.synchronizedSet(mutableSetOf())
        /** refresh-токен → код ответа /auth/refresh (200 — выдать новую пару). */
        val refresh: MutableMap<String, Int> = Collections.synchronizedMap(mutableMapOf())
        val refreshCalls = AtomicInteger()
        val meCalls = AtomicInteger()
        val log: MutableList<String> = Collections.synchronizedList(mutableListOf())
        private val seq = AtomicInteger()

        override fun intercept(chain: Interceptor.Chain): Response {
            val req = chain.request()
            val path = req.url.encodedPath
            val token = req.header("Cookie")?.removePrefix("kino_session=") ?: ""
            log += "${req.method} $path [$token]"
            if (path == "/api/v1/auth/refresh") {
                refreshCalls.incrementAndGet()
                val buf = okio.Buffer().also { req.body!!.writeTo(it) }
                val rt = Json.parseToJsonElement(buf.readUtf8()).jsonObject["refreshToken"]!!.jsonPrimitive.content
                val code = refresh.remove(rt) ?: 401
                if (code != 200) return reply(req, code, if (code == 410) "expired" else "unauthorized")
                val n = seq.incrementAndGet()
                alive += "tok$n"
                refresh["ref$n"] = 200
                return reply(req, 200, body = """{"token":"tok$n","refreshToken":"ref$n","expiresAt":"","refreshExpiresAt":""}""")
            }
            if (path == "/api/v1/me") meCalls.incrementAndGet()
            when {
                token in expired -> return reply(req, 410, "expired")
                token !in alive -> return reply(req, 401, "unauthorized")
            }
            return when (path) {
                // Код устройства истёк, сессия жива — тоже 410 `expired`.
                "/api/v1/auth/device/OLD-CODE" -> reply(req, 410, "expired")
                "/api/v1/me/password" -> reply(req, 401, "unauthorized")
                "/api/v1/odd" -> reply(req, 410, "gone")
                else -> reply(req, 200, body = """{"ok":true}""")
            }
        }

        private fun reply(req: Request, code: Int, error: String = "", body: String? = null): Response =
            Response.Builder().request(req).protocol(Protocol.HTTP_1_1).code(code).message("x")
                .body((body ?: """{"error":"$error","message":"$error"}""").toResponseBody("application/json".toMediaType()))
                .build()
    }

    /** Токены клиента — как в Api: память + «диск». */
    private class Tokens(token: String, var refreshTok: String?) : SessionRenewal.Tokens {
        @Volatile override var current: String = token
        val saved = mutableListOf<Api.TokenPair>()
        val deadCalls = AtomicInteger()
        override fun refreshToken(): String? = refreshTok
        override fun save(pair: Api.TokenPair) {
            current = pair.token
            refreshTok = pair.refreshToken
            saved += pair
        }
        override fun dead() {
            current = ""
            refreshTok = null
            deadCalls.incrementAndGet()
        }
    }

    private fun setup(core: FakeCore, tokens: Tokens): Pair<OkHttpClient, SessionRenewal> {
        val bare = OkHttpClient.Builder().addInterceptor(core).build()
        val renewal = SessionRenewal(base, bare, json, tokens)
        val client = OkHttpClient.Builder().addInterceptor(renewal).addInterceptor(core).build()
        return client to renewal
    }

    private fun OkHttpClient.get(path: String, url: String = "$base$path"): Pair<Int, String> =
        newCall(Request.Builder().url(url).build()).execute().use { it.code to it.body!!.string() }

    @Test
    fun `410 expired - refresh, then the request is repeated with the new cookie`() {
        val core = FakeCore().apply { expired += "old"; refresh["r0"] = 200 }
        val tokens = Tokens("old", "r0")
        val (client, _) = setup(core, tokens)

        val (code, _) = client.get("/api/v1/continue")

        assertEquals(200, code)
        assertEquals("tok1", tokens.current)
        assertEquals("ref1", tokens.refreshTok)
        assertEquals(1, tokens.saved.size)
        assertEquals(1, core.refreshCalls.get())
        assertEquals(0, tokens.deadCalls.get())
        assertEquals("GET /api/v1/continue [tok1]", core.log.last())
    }

    @Test
    fun `refresh answered 410 - session is dead, out to login`() {
        val core = FakeCore().apply { expired += "old"; refresh["r0"] = 410 }
        val tokens = Tokens("old", "r0")
        val (client, _) = setup(core, tokens)

        val (code, body) = client.get("/api/v1/continue")

        assertEquals(410, code)
        assertTrue(body.contains("expired"))
        assertEquals(1, tokens.deadCalls.get())
        assertEquals("", tokens.current)
        // Повтора не было: продлевать нечем.
        assertEquals(1, core.log.count { it.startsWith("GET /api/v1/continue") })
    }

    @Test
    fun `refresh revoked (401) after 401 - dead once the server confirms it`() {
        val core = FakeCore().apply { refresh["r0"] = 401 }
        val tokens = Tokens("revoked", "r0")
        val (client, _) = setup(core, tokens)

        assertEquals(401, client.get("/api/v1/titles/1").first)
        assertEquals(1, tokens.deadCalls.get())
        assertEquals(1, core.meCalls.get())
    }

    @Test
    fun `401 - refresh and repeat, as the old Authenticator did`() {
        val core = FakeCore().apply { refresh["r0"] = 200 }
        val tokens = Tokens("unknown", "r0")
        val (client, _) = setup(core, tokens)

        assertEquals(200, client.get("/api/v1/me").first)
        assertEquals("tok1", tokens.current)
        assertEquals(0, tokens.deadCalls.get())
    }

    @Test
    fun `410 for an expired device code is not about the session`() {
        val core = FakeCore().apply { alive += "live"; refresh["r0"] = 200 }
        val tokens = Tokens("live", "r0")
        val (client, _) = setup(core, tokens)

        val (code, _) = client.get("/api/v1/auth/device/OLD-CODE")

        assertEquals(410, code)
        // /me жив — ни ротации пары, ни выхода.
        assertEquals(0, core.refreshCalls.get())
        assertEquals(0, tokens.deadCalls.get())
        assertEquals("live", tokens.current)
        assertEquals(1, core.meCalls.get())
    }

    @Test
    fun `password session without refresh - expired means login`() {
        val core = FakeCore().apply { expired += "pwd" }
        val tokens = Tokens("pwd", null)
        val (client, _) = setup(core, tokens)

        assertEquals(410, client.get("/api/v1/continue").first)
        assertEquals(1, tokens.deadCalls.get())
        assertEquals(0, core.refreshCalls.get())
    }

    @Test
    fun `password session without refresh - 401 on a live session keeps it`() {
        // Неверный старый пароль отвечает 401, а сессия жива: не выкидывать.
        val core = FakeCore().apply { alive += "pwd" }
        val tokens = Tokens("pwd", null)
        val (client, _) = setup(core, tokens)

        assertEquals(401, client.get("/api/v1/me/password").first)
        assertEquals(0, tokens.deadCalls.get())
        assertEquals("pwd", tokens.current)
    }

    @Test
    fun `refresh failing with 5xx keeps the session`() {
        val core = FakeCore().apply { expired += "old"; refresh["r0"] = 502 }
        val tokens = Tokens("old", "r0")
        val (client, _) = setup(core, tokens)

        assertEquals(410, client.get("/api/v1/continue").first)
        assertEquals(0, tokens.deadCalls.get())
        assertEquals("old", tokens.current)
    }

    @Test
    fun `foreign hosts are left alone`() {
        val core = FakeCore().apply { expired += "old"; refresh["r0"] = 200 }
        val tokens = Tokens("old", "r0")
        val (client, _) = setup(core, tokens)

        assertEquals(410, client.get("", url = "http://cdn.test/api/v1/continue").first)
        assertEquals(0, core.refreshCalls.get())
        assertEquals(0, core.meCalls.get())
    }

    @Test
    fun `410 with another error code is not a session matter`() {
        val core = FakeCore().apply { alive += "live"; refresh["r0"] = 200 }
        val tokens = Tokens("live", "r0")
        val (client, _) = setup(core, tokens)

        assertEquals(410, client.get("/api/v1/odd").first)
        assertEquals(0, core.refreshCalls.get())
        assertEquals(0, core.meCalls.get())
    }

    @Test
    fun `no session - nothing to renew`() {
        val core = FakeCore().apply { refresh["r0"] = 200 }
        val tokens = Tokens("", "r0")
        val (client, _) = setup(core, tokens)

        assertEquals(401, client.get("/api/v1/me").first)
        assertEquals(0, core.refreshCalls.get())
        assertEquals("GET /api/v1/me []", core.log.single())
    }

    @Test
    fun `parallel 410s renew the pair once`() {
        val core = FakeCore().apply { expired += "old"; refresh["r0"] = 200 }
        val tokens = Tokens("old", "r0")
        val (client, _) = setup(core, tokens)

        val n = 8
        val pool = Executors.newFixedThreadPool(n)
        val start = CountDownLatch(1)
        val codes = Collections.synchronizedList(mutableListOf<Int>())
        repeat(n) { i ->
            pool.execute {
                start.await()
                codes += client.get("/api/v1/titles/$i").first
            }
        }
        start.countDown()
        pool.shutdown()
        assertTrue(pool.awaitTermination(10, TimeUnit.SECONDS))

        assertEquals(List(n) { 200 }, codes.toList())
        assertEquals(1, core.refreshCalls.get())
        assertEquals("tok1", tokens.current)
        assertEquals(0, tokens.deadCalls.get())
    }

    @Test
    fun `explicit renew after neighbours already renewed does not rotate again`() {
        val core = FakeCore().apply { expired += "old"; refresh["r0"] = 200 }
        val tokens = Tokens("old", "r0")
        val (client, renewal) = setup(core, tokens)

        assertEquals(200, client.get("/api/v1/continue").first)
        // Запрос ушёл со старым токеном и вернулся, когда пару уже сменили.
        assertEquals(SessionRenewal.Renewal.RENEWED, renewal.renew("old", probeFirst = true))
        assertEquals(1, core.refreshCalls.get())
        assertFalse(tokens.current == "old")
    }
}
