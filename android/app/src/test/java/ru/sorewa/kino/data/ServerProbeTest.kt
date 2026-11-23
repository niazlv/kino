package ru.sorewa.kino.data

import kotlinx.coroutines.runBlocking
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.net.UnknownHostException

/**
 * Проверка адреса своего сервера. Сервер — интерсептор в самом OkHttp: в сеть
 * ничего не уходит.
 */
class ServerProbeTest {

    private fun client(handler: (Request) -> Pair<Int, String>): OkHttpClient =
        OkHttpClient.Builder().addInterceptor(Interceptor { chain ->
            val req = chain.request()
            val (code, body) = handler(req)
            Response.Builder()
                .request(req).protocol(Protocol.HTTP_1_1).code(code).message("x")
                .body(body.toResponseBody("application/json".toMediaType()))
                .build()
        }).build()

    @Test
    fun `визитка сервера`() = runBlocking {
        val seen = mutableListOf<String>()
        val r = ServerProbe.probe("Kino.Example.com/#/title/1", client { req ->
            seen += req.url.toString()
            200 to """{"kind":"kino-platform","api":1,"name":"Кино друзей","registration":"open"}"""
        })
        assertEquals(listOf("https://kino.example.com/api/v1/server"), seen)
        r as ServerProbe.Result.Ok
        assertEquals("https://kino.example.com", r.url)
        assertEquals("Кино друзей", r.info.name)
        assertTrue(r.info.openRegistration)
    }

    @Test
    fun `сервер старше визитки узнаётся по health`() = runBlocking {
        val r = ServerProbe.probe("https://old.example", client { req ->
            when (req.url.encodedPath) {
                "/api/v1/server" -> 404 to """{"error":"not found"}"""
                "/api/v1/health" -> 200 to """{"status":"ok"}"""
                else -> 500 to ""
            }
        })
        r as ServerProbe.Result.Ok
        assertEquals(ServerInfo.REG_INVITE, r.info.registration)
        assertTrue(r.info.canRegister)
    }

    @Test
    fun `чужой сайт — не сервер kino`() = runBlocking {
        // Любой сайт отвечает 200 на что угодно — без kind это не наш сервер.
        val html = ServerProbe.probe("example.com", client { 200 to "<html>hello</html>" })
        assertTrue(html is ServerProbe.Result.Fail)
        val json = ServerProbe.probe("example.com", client { 200 to """{"kind":"other"}""" })
        assertTrue(json is ServerProbe.Result.Fail)
        val none = ServerProbe.probe("example.com", client { 404 to "" })
        assertTrue(none is ServerProbe.Result.Fail)
    }

    @Test
    fun `понятные ошибки`() = runBlocking {
        val bad = ServerProbe.probe("не адрес", client { 200 to "" })
        assertTrue((bad as ServerProbe.Result.Fail).message.contains("адрес"))
        val dns = ServerProbe.probe("nowhere.example", OkHttpClient.Builder().addInterceptor(Interceptor {
            throw UnknownHostException("nowhere.example")
        }).build())
        assertTrue((dns as ServerProbe.Result.Fail).message.contains("не найден"))
    }

    @Test
    fun `закрытая регистрация`() {
        val info = ServerProbe.parse("""{"kind":"kino-platform","api":1,"registration":"closed"}""")!!
        assertEquals(false, info.canRegister)
        assertEquals(false, info.openRegistration)
    }

    @Test
    fun `http к серверу в интернете — отказ без запроса в сеть`() = runBlocking {
        var calls = 0
        val r = ServerProbe.probe("http://kino.example.org", client { calls++; 200 to "" })
        assertEquals(ServerProbe.CLEARTEXT_PUBLIC, (r as ServerProbe.Result.Fail).message)
        assertEquals(0, calls)
    }

    @Test
    fun `домашний адрес без схемы — https не ответил, берём http`() = runBlocking {
        val seen = mutableListOf<String>()
        val c = OkHttpClient.Builder().addInterceptor(Interceptor { chain ->
            val req = chain.request()
            seen += req.url.toString()
            if (req.url.scheme == "https") throw javax.net.ssl.SSLHandshakeException("plain http server")
            Response.Builder()
                .request(req).protocol(Protocol.HTTP_1_1).code(200).message("x")
                .body("""{"kind":"kino-platform","api":1,"registration":"invite"}""".toResponseBody("application/json".toMediaType()))
                .build()
        }).build()
        val r = ServerProbe.probe("192.168.1.5:8080", c)
        assertEquals("http://192.168.1.5:8080", (r as ServerProbe.Result.Ok).url)
        assertEquals(listOf("https://192.168.1.5:8080/api/v1/server", "http://192.168.1.5:8080/api/v1/server"), seen)

        // Сервер в интернете без схемы на http не откатывается.
        seen.clear()
        val pub = ServerProbe.probe("kino.example.org", c)
        assertTrue(pub is ServerProbe.Result.Fail)
        assertEquals(listOf("https://kino.example.org/api/v1/server"), seen)
    }
}
