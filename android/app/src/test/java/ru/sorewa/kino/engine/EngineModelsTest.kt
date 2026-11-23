package ru.sorewa.kino.engine

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import ru.sorewa.kino.data.EngineStatus
import ru.sorewa.kino.data.KinopubLoginCode
import ru.sorewa.kino.data.KinopubLoginState

/**
 * Контракт JSON между движком (Go) и приложением: примеры — ровно то, что отдают
 * /engine/status и /engine/kinopub/login. Сломается поле на одной стороне — тест на
 * другой скажет об этом раньше, чем человек увидит пустой экран.
 */
class EngineModelsTest {

    // Те же настройки разбора, что у Api.json.
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = false; explicitNulls = false }

    @Test fun `статус движка с входом в kinopub`() {
        val st = json.decodeFromString<EngineStatus>(
            """{"startedAt":"2026-11-23T03:34:43.3Z",
                "providers":[{"id":"anilibria","state":"ready"},
                             {"id":"kinopub","state":"skipped","reason":"нужен вход или ключ"}],
                "routes":["default","direct"],
                "requests":{"total":3,"since":"2026-11-23T03:34:43Z","hosts":[{"host":"api.anilibria.app","count":3}]},
                "kinopub":{"session":false,"canLogin":true}}""",
        )
        assertEquals("нужен вход или ключ", st.providers.first { it.id == "kinopub" }.reason)
        assertTrue(st.kinopub.canLogin)
        assertFalse(st.kinopub.session)
        assertEquals(3, st.requests.total)
    }

    @Test fun `статус движка с подписью — по умолчанию и где задана вручную`() {
        val st = json.decodeFromString<EngineStatus>(
            """{"providers":[],"routes":[],
                "userAgent":{"default":"Mozilla/5.0 (Linux; Android 10; K) Chrome/140.0.0.0 Mobile Safari/537.36 KinoPlatform-Android/0.2.14",
                             "custom":["*","anilibria"]}}""",
        )
        assertTrue(st.userAgent.default.endsWith("KinoPlatform-Android/0.2.14"))
        assertEquals(listOf("*", "anilibria"), st.userAgent.custom)
        // Старый движок поля не знает — значения по умолчанию, не падение.
        val old = json.decodeFromString<EngineStatus>("""{"providers":[],"routes":[]}""")
        assertEquals("", old.userAgent.default)
        assertTrue(old.userAgent.custom.isEmpty())
    }

    @Test fun `статус старого движка без поля kinopub читается`() {
        // Приложение новее движка (или наоборот): отсутствующее поле — значения по умолчанию, не падение.
        val st = json.decodeFromString<EngineStatus>("""{"providers":[],"routes":[]}""")
        assertFalse(st.kinopub.session)
        assertFalse(st.kinopub.canLogin)
    }

    @Test fun `код входа — что движок отдаёт на POST`() {
        val c = json.decodeFromString<KinopubLoginCode>(
            """{"userCode":"ABCD-1234","verificationUri":"https://kino.pub/device",
                "verificationUriComplete":"https://kino.pub/device?code=ABCD-1234",
                "expiresAt":"2026-11-23T07:56:23.298924+04:00","intervalSec":5}""",
        )
        assertEquals("ABCD-1234", c.userCode)
        assertEquals("https://kino.pub/device?code=ABCD-1234", c.verificationUriComplete)
        assertEquals(5, c.intervalSec)
        // Время со смещением и микросекундами разбирается так же, как остальные серверные.
        assertTrue(ru.sorewa.kino.data.parseIsoTime(c.expiresAt) > 0)
    }

    @Test fun `код входа без необязательных полей`() {
        val c = json.decodeFromString<KinopubLoginCode>("""{"userCode":"X","verificationUri":"https://kino.pub/device"}""")
        assertEquals("", c.verificationUriComplete)
        assertEquals(5, c.intervalSec)
    }

    @Test fun `состояния входа`() {
        for (state in listOf("none", "pending", "ok", "expired", "denied", "error")) {
            assertEquals(state, json.decodeFromString<KinopubLoginState>("""{"state":"$state"}""").state)
        }
        val e = json.decodeFromString<KinopubLoginState>("""{"state":"error","message":"kino.pub не признал клиента"}""")
        assertEquals("kino.pub не признал клиента", e.message)
        // Пустой ответ — «нет входа», а не исключение.
        assertEquals("none", json.decodeFromString<KinopubLoginState>("{}").state)
    }
}
