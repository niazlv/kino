package ru.sorewa.kino.engine

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class EngineSettingsTest {

    @Test fun `по умолчанию — один источник без ключей и без прокси`() {
        val env = EngineSettings().toEnv()
        assertEquals("anilibria", env["PROVIDERS"])
        assertFalse("прокси не задан", env.containsKey("KINOPUB_PROXY"))
        assertFalse(env.containsKey("LOG_LEVEL"))
        // «Жирный» кэш по умолчанию: один раз полученное не качается заново.
        assertEquals("512", env["ART_CACHE_MB"])
    }

    @Test fun `бережные умолчания — редкое обновление, мало находок, без кадров серий`() {
        val env = EngineSettings().toEnv()
        assertEquals("12", env["ENGINE_REFRESH_HOURS"])
        assertEquals("4", env["ENGINE_SEARCH_PER_SOURCE"])
        assertEquals("3", env["ENGINE_SEARCH_MIN_HITS"])
        assertFalse("кадры серий — по запросу на серию, по умолчанию выключены", env.containsKey("ENGINE_THUMBS"))
    }

    @Test fun `кадры серий включаются явно`() {
        assertEquals("1", EngineSettings(thumbs = true).toEnv()["ENGINE_THUMBS"])
    }

    @Test fun `порог достаточности не больше числа находок — иначе всегда спрашивали бы всех`() {
        // Просим у площадки две находки, а «достаточно» три — к следующей шли бы всегда.
        val env = EngineSettings(searchPerSource = 2).toEnv()
        assertEquals("2", env["ENGINE_SEARCH_PER_SOURCE"])
        assertEquals("2", env["ENGINE_SEARCH_MIN_HITS"])
        assertEquals("3", EngineSettings(searchPerSource = 10).toEnv()["ENGINE_SEARCH_MIN_HITS"])
    }

    @Test fun `срок обновления и число находок не выходят за разумные границы`() {
        assertEquals("1", EngineSettings(refreshHours = 0).toEnv()["ENGINE_REFRESH_HOURS"])
        assertEquals("720", EngineSettings(refreshHours = 999_999).toEnv()["ENGINE_REFRESH_HOURS"])
        assertEquals("1", EngineSettings(searchPerSource = 0).toEnv()["ENGINE_SEARCH_PER_SOURCE"])
        assertEquals("20", EngineSettings(searchPerSource = 500).toEnv()["ENGINE_SEARCH_PER_SOURCE"])
    }

    @Test fun `порядок опроса — порядок источников в приложении, а не порядок нажатий`() {
        // Лучшая площадка первой: её находок обычно хватает, и остальных не беспокоят.
        val env = EngineSettings(providers = listOf("yummy", "anilibria", "animego_me")).toEnv()
        assertEquals("anilibria=100,animego_me=99,yummy=98", env["PROVIDER_PRIORITY"])
    }

    @Test fun `источники идут в порядке списка приложения, чужие id отсеиваются`() {
        val s = EngineSettings(providers = listOf("yummy", "нет-такой", "anilibria", "animego_me"))
        // PROVIDERS собирается по EngineSources, а не по порядку, в котором человек нажимал.
        assertEquals(listOf("anilibria", "animego_me", "yummy"), s.activeProviders())
        assertEquals("anilibria,animego_me,yummy", s.toEnv()["PROVIDERS"])
    }

    @Test fun `пустой список не уходит в движок — он подставил бы kinopub`() {
        // PROVIDERS пустой движок читает как «kinopub» — а у него нет ключа.
        assertEquals("anilibria", EngineSettings(providers = emptyList()).toEnv()["PROVIDERS"])
        assertEquals("anilibria", EngineSettings(providers = listOf("мусор")).toEnv()["PROVIDERS"])
    }

    @Test fun `ключ уходит в свою переменную, пробелы срезаются, пустой не передаётся`() {
        val s = EngineSettings(
            providers = listOf("anilibria", "kinopub", "filmix", "lordfilm"),
            keys = mapOf("kinopub" to "  tok-kp  ", "filmix" to "   ", "lordfilm" to "alloha-1"),
        )
        val env = s.toEnv()
        assertEquals("tok-kp", env["KINOPUB_TOKEN"])
        assertEquals("alloha-1", env["LORDFILM_ALLOHA_TOKEN"])
        assertFalse("пустой ключ не передаётся", env.containsKey("FILMIX_TOKEN"))
    }

    @Test fun `ключ выключенного источника в движок не попадает`() {
        val env = EngineSettings(providers = listOf("anilibria"), keys = mapOf("kinopub" to "secret")).toEnv()
        // Ключ остаётся в настройках (включат снова — не вводить заново), но процессу его не отдаём.
        assertFalse(env.containsKey("KINOPUB_TOKEN"))
    }

    @Test fun `прокси идёт в KINOPUB_PROXY только если годен`() {
        assertEquals("socks5://10.0.0.5:1080", EngineSettings(proxy = " socks5://10.0.0.5:1080 ").toEnv()["KINOPUB_PROXY"])
        assertFalse("негодный не передаётся: движок на нём не поднялся бы",
            EngineSettings(proxy = "socks5://без-порта").toEnv().containsKey("KINOPUB_PROXY"))
    }

    @Test fun `проверка прокси`() {
        assertNull("пусто — напрямую", validateProxy(""))
        assertNull(validateProxy("   "))
        assertNull(validateProxy("socks5://192.168.1.5:1080"))
        assertNull(validateProxy("http://proxy.local:3128"))
        assertNull(validateProxy("https://proxy.local:443"))
        assertNotNull("без схемы", validateProxy("192.168.1.5:1080"))
        assertNotNull("чужая схема", validateProxy("ftp://h:21"))
        assertNotNull("без порта", validateProxy("socks5://h"))
        assertNotNull("порт вне диапазона", validateProxy("socks5://h:70000"))
        assertNotNull("мусор", validateProxy("socks5://ho st:1"))
    }

    @Test fun `отладочный журнал и потолок кэша`() {
        val env = EngineSettings(debugLog = true, artCacheMb = 512).toEnv()
        assertEquals("DEBUG", env["LOG_LEVEL"])
        assertEquals("512", env["ART_CACHE_MB"])
        // Абсурдные значения не доходят до движка.
        assertEquals("4096", EngineSettings(artCacheMb = 999_999).toEnv()["ART_CACHE_MB"])
        assertEquals("32", EngineSettings(artCacheMb = 0).toEnv()["ART_CACHE_MB"])
    }

    @Test fun `настройки не перекрывают то, что решает приложение`() {
        // Даже если в ключи как-нибудь попало имя служебной переменной.
        val env = EngineLaunch.env(
            9000, "app-token", File("/d"), File("/c"),
            EngineSettings(keys = mapOf("anilibria" to "ENGINE_TOKEN=evil")),
        )
        assertEquals("app-token", env["ENGINE_TOKEN"])
        assertEquals("127.0.0.1:9000", env["ENGINE_ADDR"])
        assertEquals("1", env["ENGINE_EXIT_ON_STDIN_EOF"])
        assertEquals("/d/kino-engine.db", env["ENGINE_DB"])
    }

    @Test fun `круг через JSON сохраняет всё, битая запись даёт умолчания`() {
        val s = EngineSettings(
            providers = listOf("anilibria", "kinopub"), proxy = "socks5://h:1",
            keys = mapOf("kinopub" to "k"), debugLog = true, artCacheMb = 1024,
            refreshHours = 72, searchPerSource = 6, thumbs = true,
        )
        assertEquals(s, EngineSettingsStore.decode(EngineSettingsStore.encode(s)))
        // Движок обязан подняться, что бы ни лежало в хранилище.
        assertEquals(EngineSettings(), EngineSettingsStore.decode(null))
        assertEquals(EngineSettings(), EngineSettingsStore.decode(""))
        assertEquals(EngineSettings(), EngineSettingsStore.decode("{не json"))
        // Запись из будущей версии с лишними полями читается.
        assertEquals(
            listOf("yummy"),
            EngineSettingsStore.decode("""{"providers":["yummy"],"новое":1}""").providers,
        )
    }

    @Test fun `у каждого источника с ключом есть переменная и подпись, у прочих — нет`() {
        for (src in EngineSources.all) {
            assertEquals(src.id, src.keyEnv == null, src.keyLabel == null)
        }
        assertEquals(setOf("KINOPUB_TOKEN", "FILMIX_TOKEN", "LORDFILM_ALLOHA_TOKEN"),
            EngineSources.all.mapNotNull { it.keyEnv }.toSet())
        assertTrue("идентификаторы уникальны", EngineSources.all.map { it.id }.let { it.size == it.toSet().size })
        assertEquals("anilibria", EngineSources.all.first().id)
    }

    @Test fun `журнал движка читается человеком`() {
        val line = """{"time":"2026-11-23T02:44:48.51Z","level":"WARN","msg":"площадка пропущена","service":"engine","id":"kinopub","причина":"нужен ключ"}"""
        val p = EngineLogFormat.pretty(line)
        assertTrue(p, p.startsWith("02:44:48 WARN  площадка пропущена"))
        assertTrue(p, p.contains("id=kinopub"))
        assertTrue(p, p.contains("причина=нужен ключ"))
        // Не JSON — как есть.
        assertEquals("просто текст", EngineLogFormat.pretty("просто текст"))
        val text = (1..100).joinToString("\n") { """{"time":"2026-11-23T02:44:48Z","level":"INFO","msg":"m$it"}""" }
        val tail = EngineLogFormat.tail(text, 3)
        assertEquals(3, tail.size)
        assertTrue(tail.last().endsWith("m100"))
    }

    // ---- вход в kinopub ----

    @Test fun `секрет клиента идёт в движок только при включённом kinopub`() {
        val keys = mapOf(EngineSettings.KINOPUB_SECRET_KEY to "  s3cret  ")
        assertEquals("s3cret", EngineSettings(providers = listOf("anilibria", "kinopub"), keys = keys).toEnv()["KINOPUB_CLIENT_SECRET"])
        // Выключен kinopub — секрет остаётся в настройках (включат снова — не вводить заново), но процессу не отдаётся.
        assertFalse(EngineSettings(providers = listOf("anilibria"), keys = keys).toEnv().containsKey("KINOPUB_CLIENT_SECRET"))
        // Пустой секрет не передаётся.
        assertFalse(
            EngineSettings(providers = listOf("kinopub"), keys = mapOf(EngineSettings.KINOPUB_SECRET_KEY to "  "))
                .toEnv().containsKey("KINOPUB_CLIENT_SECRET"),
        )
    }

    @Test fun `секрет клиента не путается с ключом-токеном`() {
        val env = EngineSettings(
            providers = listOf("kinopub"),
            keys = mapOf("kinopub" to "access-tok", EngineSettings.KINOPUB_SECRET_KEY to "sec"),
        ).toEnv()
        // Токен руками и секрет для входа — разные вещи и разные переменные.
        assertEquals("access-tok", env["KINOPUB_TOKEN"])
        assertEquals("sec", env["KINOPUB_CLIENT_SECRET"])
    }

    @Test fun `секрет не считается источником`() {
        // kinopub_secret лежит в keys рядом с ключами источников, но источником не является:
        // в список площадок движка он попасть не может.
        val s = EngineSettings(providers = listOf("anilibria", EngineSettings.KINOPUB_SECRET_KEY))
        assertEquals(listOf("anilibria"), s.activeProviders())
    }

    @Test fun `файл сессии kinopub лежит в данных движка и настройками не перекрывается`() {
        val env = EngineLaunch.env(
            9000, "tok", File("/d"), File("/c"),
            EngineSettings(providers = listOf("kinopub"), keys = mapOf("kinopub" to "x")),
        )
        assertEquals("/d/${EngineLaunch.KINOPUB_SESSION_FILE}", env["KINOPUB_TOKEN_FILE"])
        assertEquals("kinopub-session.json", EngineLaunch.KINOPUB_SESSION_FILE)
    }

    // ---- User-Agent: честный по умолчанию, настраиваемый человеком ----

    @Test fun `по умолчанию своих подписей нет — движок подставит честную`() {
        val env = EngineSettings().toEnv()
        assertFalse(env.containsKey("ENGINE_USER_AGENT"))
        assertFalse("ни одной подписи площадки", env.keys.any { it.startsWith(EngineSettings.USER_AGENT_ENV_PREFIX) })
    }

    @Test fun `подпись для всех и для отдельной площадки уходят в разные переменные`() {
        val env = EngineSettings(
            providers = listOf("anilibria", "animego_me"),
            userAgent = "  Common/1  ",
            userAgents = mapOf("animego_me" to "Mine/2"),
        ).toEnv()
        assertEquals("Common/1", env["ENGINE_USER_AGENT"])
        // Имя переменной — id площадки заглавными: так её читает движок (ENGINE_UA_ANIMEGO_ME → animego_me).
        assertEquals("Mine/2", env["ENGINE_UA_ANIMEGO_ME"])
        assertFalse(env.containsKey("ENGINE_UA_ANILIBRIA"))
    }

    @Test fun `подпись выключенной площадки не передаётся`() {
        val env = EngineSettings(
            providers = listOf("anilibria"),
            userAgents = mapOf("yummy" to "Mine/2", "anilibria" to "  "),
        ).toEnv()
        assertFalse(env.containsKey("ENGINE_UA_YUMMY"))
        assertFalse("пустая подпись — это «по умолчанию»", env.containsKey("ENGINE_UA_ANILIBRIA"))
    }

    @Test fun `негодная подпись в окружение не попадает`() {
        val env = EngineSettings(
            providers = listOf("anilibria"),
            userAgent = "Моя подпись",
            userAgents = mapOf("anilibria" to "line\nbreak"),
        ).toEnv()
        assertFalse(env.containsKey("ENGINE_USER_AGENT"))
        assertFalse(env.containsKey("ENGINE_UA_ANILIBRIA"))
    }

    @Test fun `проверка подписи`() {
        assertNull(validateUserAgent(""))
        assertNull(validateUserAgent("   "))
        assertNull(validateUserAgent("Mozilla/5.0 (Linux; Android 14) Chrome/140.0.0.0 KinoPlatform-Android/0.2.14"))
        assertNotNull("кириллица в заголовке недопустима", validateUserAgent("Моя"))
        assertNotNull("перевод строки — подмена заголовков", validateUserAgent("a\r\nX-Evil: 1"))
        assertNotNull(validateUserAgent("a".repeat(EngineSettings.MAX_USER_AGENT_LEN + 1)))
        assertNull(validateUserAgent("a".repeat(EngineSettings.MAX_USER_AGENT_LEN)))
    }

    @Test fun `пометка приложения несёт версию и не ломается от мусора`() {
        assertEquals("KinoPlatform-Android/0.2.14", EngineLaunch.appUserAgent("0.2.14"))
        assertEquals("KinoPlatform-Android/dev", EngineLaunch.appUserAgent(""))
        assertEquals("KinoPlatform-Android/dev", EngineLaunch.appUserAgent("  "))
        // Пробелы и управляющие символы из «версии» вычищаются: пометка — один токен.
        assertEquals("KinoPlatform-Android/0.2.14-debug", EngineLaunch.appUserAgent("0.2.14 -debug\n"))
    }

    @Test fun `окружение движка несёт пометку приложения с версией и подписи человека`() {
        val env = EngineLaunch.env(
            9000, "tok", File("/d"), File("/c"),
            EngineSettings(providers = listOf("anilibria"), userAgents = mapOf("anilibria" to "Mine/2")),
            appVersion = "0.2.14",
        )
        assertEquals("KinoPlatform-Android/0.2.14", env["ENGINE_APP_UA"])
        assertEquals("Mine/2", env["ENGINE_UA_ANILIBRIA"])
    }

    @Test fun `подписи переживают сохранение и чтение настроек`() {
        val s = EngineSettings(userAgent = "Common/1", userAgents = mapOf("yummy" to "Mine/2"))
        val back = EngineSettingsStore.decode(EngineSettingsStore.encode(s))
        assertEquals("Common/1", back.userAgent)
        assertEquals(mapOf("yummy" to "Mine/2"), back.userAgents)
        // Настройки, записанные до появления подписей, читаются: подписей просто нет.
        val old = EngineSettingsStore.decode("""{"providers":["anilibria"]}""")
        assertEquals("", old.userAgent)
        assertTrue(old.userAgents.isEmpty())
    }
}
