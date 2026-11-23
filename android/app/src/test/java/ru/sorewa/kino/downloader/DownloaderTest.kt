package ru.sorewa.kino.downloader

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Контракт утилиты kinopub-downloader: аргументы запуска и NDJSON-протокол.
 * Правила — из DownloadService.java старой оболочки.
 */
class DownloaderTest {

    private val json = Json { ignoreUnknownKeys = true }

    // ---- argv ----

    @Test
    fun `baseArgs with all optional flags`() {
        val job = DownloadJob(
            pageUrl = "https://kino.example.org/#/title/53014/s2e3",
            title = "Тайтл",
            quality = "1080p",
            audio = listOf("Воротилин", "Одноголосый"),
            subs = listOf("Русские"),
            seasons = "2",
        )
        val args = Downloader.baseArgs(
            binPath = "/data/app/libkinopub.so",
            ffmpegPath = "/data/app/libffmpeg.so",
            workPath = "/storage/downloads",
            job = job,
            cookie = "kino_session=abc123",
            userAgent = "kino-android/0.2.0",
        )
        assertTrue(args.contains("--progress-json"))
        assertTrue(args.contains("--no-notify"))
        assertTrue(args.contains("--container"))
        assertTrue(args.contains("mkv"))
        assertEquals(listOf("-q", "1080p"), args.windowed(2).first { it[0] == "-q" })
        assertEquals("Воротилин,Одноголосый", args[args.indexOf("--audio") + 1])
        assertEquals("Русские", args[args.indexOf("--subs") + 1])
        assertEquals("2", args[args.indexOf("--seasons") + 1])
        assertEquals("https://kino.example.org/#/title/53014/s2e3", args.last())
    }

    @Test
    fun `baseArgs omits empty optional flags`() {
        val job = DownloadJob(pageUrl = "https://kino.example.org/#/title/53014", title = "Т")
        val args = Downloader.baseArgs(
            binPath = "b", ffmpegPath = "f", workPath = "w",
            job = job, cookie = "", userAgent = "",
        )
        org.junit.Assert.assertFalse(args.contains("--cookie"))
        org.junit.Assert.assertFalse(args.contains("--user-agent"))
        org.junit.Assert.assertFalse(args.contains("-q"))
        org.junit.Assert.assertFalse(args.contains("--audio"))
        org.junit.Assert.assertFalse(args.contains("--subs"))
        org.junit.Assert.assertFalse(args.contains("--seasons"))
        org.junit.Assert.assertFalse(args.contains("--voice"))
        org.junit.Assert.assertFalse(args.contains("--source"))
    }

    @Test
    fun `baseArgs passes the chosen voice and source to the utility`() {
        val job = DownloadJob(
            pageUrl = "https://kino.example.org/#/title/60129/s1e2", title = "Т",
            voice = "anilibria", voiceTitle = "AniLibria", source = "lordfilm",
        )
        val args = Downloader.baseArgs(binPath = "b", ffmpegPath = "f", workPath = "w", job = job, cookie = "", userAgent = "")
        assertEquals("anilibria", args[args.indexOf("--voice") + 1])
        assertEquals("lordfilm", args[args.indexOf("--source") + 1])
        // Подпись — для человека, утилите она не нужна.
        org.junit.Assert.assertFalse(args.contains("AniLibria"))
        assertEquals("https://kino.example.org/#/title/60129/s1e2", args.last())
    }

    @Test
    fun `old saved queue without voice decodes`() {
        val old = """{"pageUrl":"https://kino.example.org/#/title/1","title":"Т","quality":"","seasons":"2"}"""
        val job = json.decodeFromString(DownloadJob.serializer(), old)
        assertEquals("", job.voice)
        assertEquals("", job.source)
    }

    // ---- NDJSON ----

    @Test
    fun `parses start`() {
        val ev = parseDownloadEvent("""{"type":"start","title":"Фильм","total":8,"done":3}""", json)
        assertEquals(DownloadEvent.Start("Фильм", 8, 3), ev)
    }

    @Test
    fun `parses episode with error`() {
        val ev = parseDownloadEvent("""{"type":"episode","key":"S02E03","season":2,"episode":3,"state":"failed","error":"нет связи"}""", json)
        assertEquals(DownloadEvent.Episode("S02E03", "failed", "нет связи"), ev)
    }

    @Test
    fun `parses progress with approx bytes`() {
        val ev = parseDownloadEvent(
            """{"type":"progress","key":"S01E01","percent":42,"downloaded":1024,"totalBytes":2048,"approx":true}""",
            json,
        )
        assertEquals(DownloadEvent.Progress("S01E01", 42, 1024, 2048, true), ev)
    }

    @Test
    fun `parses result error`() {
        val ev = parseDownloadEvent("""{"type":"result","total":2,"succeeded":0,"failed":2,"error":"сессия истекла"}""", json)
        assertEquals(DownloadEvent.Result(0, 2, 0, "сессия истекла"), ev)
    }

    @Test
    fun `track and stop are recognized`() {
        assertTrue(parseDownloadEvent("""{"type":"track","key":"S01E01","track":"video","percent":10}""", json) is DownloadEvent.Track)
        assertTrue(parseDownloadEvent("""{"type":"stop"}""", json) is DownloadEvent.Stop)
    }

    @Test
    fun `garbage lines yield null`() {
        assertNull(parseDownloadEvent("plain log line", json))
        assertNull(parseDownloadEvent("""{"type":"unknown"}""", json))
        assertNull(parseDownloadEvent("""{broken json""", json))
        assertNull(parseDownloadEvent("", json))
    }

    // ---- прочее ----

    @Test
    fun `masks cookie in logs`() {
        val s = "--cookie kino_session=secret123 --user-agent x"
        assertEquals("--cookie kino_session=… --user-agent x", Downloader.maskCookie(s))
    }

    @Test
    fun `titleIdOf parses page url`() {
        assertEquals(53014L, Downloader.titleIdOf("https://kino.example.org/#/title/53014/s2e3"))
        assertEquals(0L, Downloader.titleIdOf("https://example.com/other"))
    }

    @Test
    fun `keyOf formats SxxEyy`() {
        assertEquals("S02E03", Downloader.keyOf(2, 3))
        assertEquals("S10E25", Downloader.keyOf(10, 25))
    }

    // ---- локальный режим: очередь и адрес страницы ----

    @Test
    fun `очередь локального режима лежит под своими ключами`() {
        // Очередь, пауза, «качается» и «сдалась» делят файл настроек с выбранной папкой.
        // Серверные ключи остаются прежними: у обновившихся очередь не пропадает.
        for (k in listOf("queue", "paused_job", "running_job", "failed_job", "failed_why")) {
            assertEquals(k, Downloader.scoped(k, profile = null))
            assertEquals("${k}_local", Downloader.scoped(k, profile = "local"))
            assertEquals("${k}_s_0123456789ab", Downloader.scoped(k, profile = "s_0123456789ab"))
        }
        // Папка (tree) — общая настройка и под этот механизм не подпадает: ключа в списке нет.
    }

    @Test
    fun `адрес страницы переписывается на актуальный порт движка`() {
        // Задание лежит в очереди с прошлого запуска приложения, а порт у движка новый.
        assertEquals(
            "http://127.0.0.1:41000/#/title/3/s1e2",
            Downloader.rebase("http://127.0.0.1:35385/#/title/3/s1e2", "http://127.0.0.1:41000"),
        )
        assertEquals(
            "http://127.0.0.1:41000/#/title/3",
            Downloader.rebase("http://127.0.0.1:35385/#/title/3", "http://127.0.0.1:41000/"),
        )
        // Серверный адрес не меняется (тот же origin).
        assertEquals(
            "https://kino.example.org/#/title/53014/s2e3",
            Downloader.rebase("https://kino.example.org/#/title/53014/s2e3", "https://kino.example.org"),
        )
        // Не ссылка на страницу тайтла — как есть.
        assertEquals("https://example.com/other", Downloader.rebase("https://example.com/other", "http://127.0.0.1:1"))
    }

    @Test
    fun `порт в адресе не принимается за номер тайтла`() {
        assertEquals(3L, Downloader.titleIdOf("http://127.0.0.1:35385/#/title/3/s1e2"))
        assertEquals(53014L, Downloader.titleIdOf(Downloader.rebase("https://kino.example.org/#/title/53014", "http://127.0.0.1:12345")))
    }

    @Test
    fun `утилита получает http-адрес локального движка как есть`() {
        val job = DownloadJob(pageUrl = Downloader.rebase("x/#/title/3/s1e2", "http://127.0.0.1:41000"), title = "t")
        val args = Downloader.baseArgs("/bin/k", "/bin/f", "/w", job, "kino_session=tok", "kino-android/1")
        assertEquals("http://127.0.0.1:41000/#/title/3/s1e2", args.last())
        assertTrue(args.contains("kino_session=tok"))
    }
}
