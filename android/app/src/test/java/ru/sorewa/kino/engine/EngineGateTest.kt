package ru.sorewa.kino.engine

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.IOException
import java.net.InetAddress
import java.net.ServerSocket
import java.util.concurrent.atomic.AtomicBoolean

class EngineGateTest {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    /** Мини-HTTP на голом сокете: android.jar не знает com.sun.net.httpserver. */
    private val server = ServerSocket(0, 50, InetAddress.getByName("127.0.0.1")).also { ss ->
        Thread {
            while (!ss.isClosed) {
                try {
                    ss.accept().use { sock ->
                        val r = sock.getInputStream().bufferedReader()
                        while (r.readLine().orEmpty().isNotEmpty()) { /* заголовки запроса */ }
                        sock.getOutputStream().write(
                            "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok".toByteArray(),
                        )
                    }
                } catch (_: Exception) { }
            }
        }.apply { isDaemon = true }.start()
    }
    private val port = server.localPort

    @After fun tearDown() { server.close(); scope.cancel() }

    private fun client(engine: LocalEngine, waitMs: Long) =
        OkHttpClient.Builder().addInterceptor(EngineGate(engine, waitMs)).build()

    private fun engine(launcher: FakeLauncher, up: AtomicBoolean, maxRestarts: Int = 3) = LocalEngine(
        port = port, env = { emptyMap() }, launcher = launcher, scope = scope,
        probe = { up.get() }, readyTimeoutMs = 400, maxRestarts = maxRestarts, backoffMs = { 10 },
    )

    @Test fun `первый запрос сам запускает движок и ждёт, пока он поднимется`() {
        val launcher = FakeLauncher()
        val up = AtomicBoolean(false)
        val e = engine(launcher, up)
        // Движок «открывает порт» через 300 мс после запроса.
        Thread { Thread.sleep(300); up.set(true) }.start()

        val t0 = System.nanoTime()
        client(e, 3000).newCall(Request.Builder().url("http://127.0.0.1:$port/x").build()).execute().use {
            assertEquals(200, it.code)
        }
        val waited = (System.nanoTime() - t0) / 1_000_000
        assertTrue("запрос дождался движка ($waited мс), а не упал отказом в соединении", waited >= 250)
        assertEquals(1, launcher.launches.get())
    }

    @Test fun `движок готов — запрос идёт без ожидания`() {
        val launcher = FakeLauncher()
        val e = engine(launcher, AtomicBoolean(true))
        e.start()
        client(e, 3000).newCall(Request.Builder().url("http://127.0.0.1:$port/x").build()).execute().use {
            assertEquals(200, it.code)
        }
    }

    @Test fun `движок не поднялся — IOException, то есть «нет связи», а не вечный спиннер`() {
        val launcher = FakeLauncher().apply { bornDead = true }
        val e = engine(launcher, AtomicBoolean(false), maxRestarts = 1)
        try {
            client(e, 2000).newCall(Request.Builder().url("http://127.0.0.1:$port/x").build()).execute().close()
            fail("запрос должен был упасть")
        } catch (ex: IOException) {
            assertTrue(ex.message.orEmpty().contains("движок"))
        }
    }
}
