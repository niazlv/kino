package ru.sorewa.kino.engine

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Test
import java.io.File
import java.net.HttpURLConnection
import java.net.Proxy
import java.net.URL
import java.nio.file.Files

/**
 * Настоящий бинарь движка под настоящим ProcessBuilder — тот же код запуска,
 * что в приложении, только бинарь хостовый (на Android он из jniLibs).
 * Включается переменной KINO_ENGINE_BIN=<путь к собранному cmd/engine>:
 *   go build -o /tmp/kino-engine ./cmd/engine
 *   KINO_ENGINE_BIN=/tmp/kino-engine ./gradlew :app:testDebugUnitTest --tests '*EngineProcessIntegrationTest*'
 */
class EngineProcessIntegrationTest {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private lateinit var tmp: File
    private lateinit var engine: LocalEngine
    private val token = ConnectionConfig.newToken()
    private var port = 0

    /** Копия бинаря с уникальным путём: убить «наш» процесс можно по пути, не задев чужие движки. */
    private lateinit var binCopy: File

    @Before fun setUp() {
        val bin = System.getenv("KINO_ENGINE_BIN")
        assumeTrue("нужен KINO_ENGINE_BIN — путь к собранному cmd/engine", bin != null && File(bin).canExecute())
        tmp = Files.createTempDirectory("kino-engine-it").toFile()
        binCopy = File(tmp, "kino-engine-" + System.nanoTime()).also {
            File(bin!!).copyTo(it); it.setExecutable(true)
        }
        val data = File(tmp, "data").apply { mkdirs() }
        val cache = File(tmp, "cache").apply { mkdirs() }
        port = EngineLaunch.pickFreePort()
        engine = LocalEngine(
            port = port,
            env = { EngineLaunch.env(port, token, data, cache) },
            launcher = ProcessEngineLauncher(binCopy, data, File(data, "engine.log")),
            scope = scope,
            backoffMs = { 50 },
        )
    }

    @After fun tearDown() {
        if (::engine.isInitialized) engine.stop()
        scope.cancel()
        if (::tmp.isInitialized) tmp.deleteRecursively()
    }

    private fun get(path: String, cookie: String? = null): Pair<Int, String> {
        val c = URL("http://127.0.0.1:$port$path").openConnection(Proxy.NO_PROXY) as HttpURLConnection
        c.connectTimeout = 1500; c.readTimeout = 3000
        if (cookie != null) c.setRequestProperty("Cookie", "kino_session=$cookie")
        return try {
            val code = c.responseCode
            val body = (if (code < 400) c.inputStream else c.errorStream)?.readBytes()?.toString(Charsets.UTF_8).orEmpty()
            code to body
        } finally { c.disconnect() }
    }

    private fun portOpen() = try { get("/api/v1/health").first == 200 } catch (_: Exception) { false }

    @Test fun `настоящий движок поднимается и признаёт токен приложения`() = runBlocking {
        engine.start()
        assertTrue("движок не поднялся", engine.awaitReady(20_000))

        val (noTok, _) = get("/api/v1/me")
        assertEquals("без токена — как на сервере", 401, noTok)

        val (code, body) = get("/api/v1/me", token)
        assertEquals(200, code)
        assertTrue("флаг локального режима в /me: $body", body.contains("\"local\":true"))

        // Чужой токен не годится: движок признаёт только того, кого назвало приложение.
        assertEquals(401, get("/api/v1/me", ConnectionConfig.newToken()).first)
        // Выход в локальном режиме запрещён — он убил бы единственную сессию.
        val c = URL("http://127.0.0.1:$port/api/v1/auth/logout").openConnection(Proxy.NO_PROXY) as HttpURLConnection
        c.requestMethod = "POST"; c.setRequestProperty("Cookie", "kino_session=$token")
        assertEquals(403, c.responseCode); c.disconnect()
        assertEquals("сессия после запрещённого выхода жива", 200, get("/api/v1/me", token).first)
    }

    @Test fun `движок убит — надзор поднимает его, токен и данные те же`() = runBlocking {
        engine.start()
        assertTrue(engine.awaitReady(20_000))
        // Закладка в базе: переживёт перезапуск, если база и токен прежние.
        val before = get("/api/v1/me", token).second

        // kill -9: движок не успевает ни закрыться, ни освободить порт по-хорошему.
        assertEquals("pkill нашёл процесс движка", 0,
            ProcessBuilder("pkill", "-9", "-f", binCopy.path).start().waitFor())

        assertTrue("надзор не заметил смерти", eventually(15_000) { engine.state.value != LocalEngine.State.READY || portOpen().not() })
        assertTrue("движок не вернулся", eventually(20_000) { engine.state.value == LocalEngine.State.READY && portOpen() })
        val after = get("/api/v1/me", token)
        assertEquals("тот же токен после перезапуска", 200, after.first)
        assertEquals("тот же аккаунт (база сохранилась)", before, after.second)
    }

    @Test fun `stop освобождает порт`() = runBlocking {
        engine.start()
        assertTrue(engine.awaitReady(20_000))
        engine.stop()
        assertTrue("порт не освободился", eventually(8_000) { !portOpen() })
        assertFalse(engine.state.value == LocalEngine.State.READY)
    }
}
