package ru.sorewa.kino.engine

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Свой сервер: адрес из ввода человека и раздельные данные серверов. */
class ServerAddressTest {

    @Test
    fun `ввод человека приводится к origin`() {
        val cases = mapOf(
            "kino.example.com" to "https://kino.example.com",
            "  Kino.Example.COM/  " to "https://kino.example.com",
            "https://kino.example.com/#/title/42/s1e2" to "https://kino.example.com",
            "http://10.0.2.2:18090/ #/title/5" to "http://10.0.2.2:18090",
            "https://kino.example.com/admin/login?passkey=1" to "https://kino.example.com",
            "https://kino.example.com:443" to "https://kino.example.com",
            "http://192.168.1.5:8080/" to "http://192.168.1.5:8080",
            "http://nas.local:80" to "http://nas.local",
            "kino.example.com:8443" to "https://kino.example.com:8443",
        )
        for ((input, want) in cases) assertEquals(input, want, ServerAddress.normalize(input))
    }

    @Test
    fun `кириллический домен — в punycode`() {
        val ascii = java.net.IDN.toASCII("кино.рф")
        assertTrue(ascii, ascii.startsWith("xn--"))
        assertEquals("https://$ascii", ServerAddress.normalize("Кино.РФ"))
        assertEquals("https://$ascii:8443", ServerAddress.normalize("https://кино.рф:8443/#/"))
    }

    @Test
    fun `не адрес — null`() {
        for (bad in listOf("", "   ", "ftp://kino.example.com", "javascript:alert(1)", "http://", "кино пример", "#/title/1")) {
            assertNull(bad, ServerAddress.normalize(bad))
        }
    }

    @Test
    fun `http виден человеку`() {
        assertTrue(ServerAddress.isInsecure("http://192.168.1.5:8080"))
        assertFalse(ServerAddress.isInsecure("https://kino.example.com"))
        assertEquals("kino.example.com", ServerAddress.display("https://kino.example.com"))
        assertEquals("http://192.168.1.5:8080", ServerAddress.display("http://192.168.1.5:8080"))
    }

    @Test
    fun `вшитый сервер живёт в прежнем хранилище`() {
        // Обновившееся приложение не должно потерять вход и очередь: у вшитого
        // сервера профиль null — те же DataStore, filesDir и ключи очереди.
        assertNull(ServerAddress.profileOf("https://kino.example.org", "https://kino.example.org"))
        assertNull(ServerAddress.profileOf("https://kino.example.org", "https://kino.example.org/"))
    }

    @Test
    fun `у каждого выбранного сервера свой профиль`() {
        val a = ServerAddress.profileOf("https://a.example", "https://kino.example.org")!!
        val b = ServerAddress.profileOf("https://b.example", "https://kino.example.org")!!
        assertTrue(a, Regex("s_[0-9a-f]{12}").matches(a))
        assertNotEquals(a, b)
        // Стабилен между запусками: иначе вход терялся бы при каждом старте.
        assertEquals(a, ServerAddress.profileOf("https://a.example", "https://kino.example.org"))
        // http и https одного хоста — разные серверы для данных (разные origin).
        assertNotEquals(a, ServerAddress.profileOf("http://a.example", "https://kino.example.org"))
        // Сборка без вшитого сервера: профиль есть у любого выбранного.
        assertEquals(a, ServerAddress.profileOf("https://a.example", ""))
    }

    @Test
    fun `список известных серверов — свежие первыми, без повторов`() {
        var list = emptyList<KnownServer>()
        list = KnownServers.remember(list, "https://a.example", "Кино А")
        list = KnownServers.remember(list, "https://b.example", "")
        list = KnownServers.remember(list, "https://a.example", "")
        assertEquals(listOf("https://a.example", "https://b.example"), list.map { it.url })
        // Старый сервер без визитки не стирает имя, узнанное раньше.
        assertEquals("Кино А", list.first().name)
        assertEquals(list, KnownServers.decode(KnownServers.encode(list)))
        assertEquals(listOf("https://b.example"), KnownServers.forget(list, "https://a.example").map { it.url })
        // Мусор в настройках — пустой список, а не падение на старте.
        assertEquals(emptyList<KnownServer>(), KnownServers.decode("{не json"))
        repeat(20) { list = KnownServers.remember(list, "https://s$it.example", "") }
        assertEquals(KnownServers.LIMIT, list.size)
    }

    @Test
    fun `http — только к серверу домашней сети`() {
        val local = listOf(
            "10.0.2.2", "10.255.255.255", "172.16.0.1", "172.31.255.254", "192.168.1.5", "127.0.0.1",
            "169.254.10.1", "100.64.0.1", "100.127.255.254", "[::1]", "[fd12:3456::1]", "[fe80::1]",
            "nas", "NAS", "kino.local", "kino.lan", "router.home", "kino.home.arpa", "kino.internal",
            "app.localhost", "kino.local.",
        )
        for (h in local) assertTrue(h, ServerAddress.isLocalHost(h))
        val public = listOf(
            "8.8.8.8", "172.15.0.1", "172.32.0.1", "100.63.255.255", "100.128.0.1", "192.169.1.1",
            "11.0.0.1", "[2001:db8::1]", "kino.example.org", "local.example.org", "lan.example.org",
            "kino.localhost.example.org", "1.2.3", "256.1.1.1",
        )
        for (h in public) assertFalse(h, ServerAddress.isLocalHost(h))

        assertTrue(ServerAddress.allowsCleartext("https://kino.example.org"))
        assertTrue(ServerAddress.allowsCleartext("http://192.168.1.5:8080"))
        assertTrue(ServerAddress.allowsCleartext("http://[fd00::5]:8080"))
        assertFalse(ServerAddress.allowsCleartext("http://kino.example.org"))
        assertFalse(ServerAddress.allowsCleartext("http://8.8.8.8:8080"))
    }
}
