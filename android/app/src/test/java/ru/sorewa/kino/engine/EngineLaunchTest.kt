package ru.sorewa.kino.engine

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.net.InetAddress
import java.net.ServerSocket
import java.security.SecureRandom

class EngineLaunchTest {

    @Test fun `окружение движка — контракт cmd-engine`() {
        val env = EngineLaunch.env(8123, "tok".repeat(15), File("/data/eng"), File("/cache/eng"))
        // Адрес — только петля: движок не должен слушать сеть.
        assertEquals("127.0.0.1:8123", env["ENGINE_ADDR"])
        assertEquals("/data/eng/kino-engine.db", env["ENGINE_DB"])
        assertEquals("tok".repeat(15), env["ENGINE_TOKEN"])
        assertEquals("/cache/eng/art", env["ART_CACHE_DIR"])
        // Умерло приложение — закрылся stdin — движок уходит сам.
        assertEquals("1", env["ENGINE_EXIT_ON_STDIN_EOF"])
        // У приложения нет ни домашнего каталога, ни записываемого /tmp.
        assertEquals("/data/eng", env["HOME"])
        assertEquals("/cache/eng", env["TMPDIR"])
        assertEquals(EngineLaunch.DEFAULT_PROVIDERS, env["PROVIDERS"])
    }

    @Test fun `по умолчанию только площадка без входа`() {
        // kinopub и прочим нужны ключи, входа в них из приложения пока нет:
        // движок со списком, где такая площадка есть, не поднялся бы вовсе.
        assertEquals("anilibria", EngineLaunch.DEFAULT_PROVIDERS)
    }

    @Test fun `адреса строятся от порта`() {
        assertEquals("http://127.0.0.1:9", EngineLaunch.baseUrl(9))
        assertEquals("/api/v1/health", EngineLaunch.HEALTH_PATH)
    }

    @Test fun `свободный порт действительно свободен и на петле`() {
        val p = EngineLaunch.pickFreePort()
        assertTrue(p in 1024..65535)
        ServerSocket(p, 1, InetAddress.getByName("127.0.0.1")).use { assertEquals(p, it.localPort) }
    }

    @Test fun `токен локального аккаунта проходит требование движка`() {
        val t = ConnectionConfig.newToken()
        // Движок отвергает ENGINE_TOKEN короче 32 знаков.
        assertTrue("длина ${t.length}", t.length >= ConnectionConfig.MIN_TOKEN_LEN)
        // base64url без «=»: безопасен в cookie и в окружении.
        assertTrue(t.all { it.isLetterOrDigit() || it == '-' || it == '_' })
        assertNotEquals(t, ConnectionConfig.newToken(SecureRandom()))
    }

    @Test fun `режим по умолчанию — сервер, мусор тоже сервер`() {
        assertEquals(ConnectionMode.REMOTE, ConnectionMode.of(null))
        assertEquals(ConnectionMode.REMOTE, ConnectionMode.of("что-то"))
        assertEquals(ConnectionMode.LOCAL, ConnectionMode.of("local"))
        assertEquals(ConnectionMode.REMOTE, ConnectionMode.of("remote"))
    }
}
