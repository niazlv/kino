package ru.sorewa.kino.ui.phone

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import ru.sorewa.kino.data.Episode
import ru.sorewa.kino.downloader.DownloadJob
import ru.sorewa.kino.downloader.DownloadStatus

class TitleDownloadTest {
    private val base = "https://kino.example.org/#/title/42"
    private val season = (1..4).map { Episode(id = 100L + it, seasonNo = 1, episodeNo = it) }
    private val movie = listOf(Episode(id = 9, seasonNo = 0, episodeNo = 1))

    @Test fun `фильм качается — кнопка и полоса знают об этом`() {
        val job = DownloadJob(pageUrl = base, title = "Впритык", movie = true)
        val st = titleDownload(DownloadStatus(running = job, key = "S01E01", percent = 37), emptyList(), null, null, 42, movie)
        assertTrue(st.running)
        assertEquals(0 to 1, st.current) // S01E01 утилиты = сезон 0 карточки
        assertTrue(st.waiting.isEmpty())
        assertTrue(st.busy)
    }

    @Test fun `чужой тайтл не показывается`() {
        val job = DownloadJob(pageUrl = "https://kino.example.org/#/title/7", title = "Другое")
        val st = titleDownload(DownloadStatus(running = job, key = "S01E01"), listOf(job), null, null, 42, movie)
        assertFalse(st.shown)
    }

    @Test fun `сезон — текущая серия, готовые и очередь`() {
        val job = DownloadJob(pageUrl = base, title = "Сериал", seasons = "1")
        val dl = DownloadStatus(running = job, key = "S01E02", doneKeys = setOf("S01E01"))
        val st = titleDownload(dl, emptyList(), null, null, 42, season)
        assertEquals(1 to 2, st.current)
        assertEquals(setOf(1 to 3, 1 to 4), st.waiting)
    }

    @Test fun `в очереди, но ещё не пошло — ждёт`() {
        val st = titleDownload(DownloadStatus(queued = 1), listOf(DownloadJob(pageUrl = "$base/s1e3", title = "С")), null, null, 42, season)
        assertFalse(st.running)
        assertEquals(setOf(1 to 3), st.waiting)
        assertTrue(st.busy)
    }

    @Test fun `сдавшееся задание тайтла — причина и «Повторить»`() {
        val failed = DownloadJob(pageUrl = base, title = "Впритык", movie = true) to "качалка не приняла ссылку на тайтл"
        val st = titleDownload(DownloadStatus(lastError = "x", canRetry = true), emptyList(), null, failed, 42, movie)
        assertEquals("качалка не приняла ссылку на тайтл", st.failed)
        assertFalse(st.busy)
        assertTrue(st.shown)
    }

    @Test fun `строка загрузки`() {
        val dl = DownloadStatus(key = "S01E02", percent = 37, bytesDone = 512L shl 20, bytesTotal = 1536L shl 20, epTotal = 4, epDone = 1)
        assertEquals("S01E02 · 37% · 512 МБ из 1.5 ГБ · серия 2 из 4", downloadLine(dl).replace(',', '.'))
        assertEquals("готовлю загрузку…", downloadLine(DownloadStatus(), withKey = false))
    }
}
