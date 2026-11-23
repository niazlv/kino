package ru.sorewa.kino.downloader

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Итог прогона утилиты: ошибка всего прогона — не «Готово». Фильм со
 * ссылкой …/s0e1, который не качается вовсе, не должен кончаться
 * уведомлением «Готово» при пустых экранах.
 */
class OutcomeTest {
    private val job = DownloadJob(pageUrl = "https://kino.example.org/#/title/7/s0e1", title = "Впритык", movie = true)
    private fun result(ok: Int = 0, failed: Int = 0, skipped: Int = 0, error: String? = null) =
        DownloadEvent.Result(succeeded = ok, failed = failed, skipped = skipped, error = error)

    @Test fun `ошибка всего прогона — сбой, а не «Готово»`() {
        // Дословно строка утилиты на ссылку фильма с сезоном 0.
        val line = """{"type":"result","total":0,"succeeded":0,"failed":0,"skipped":0,"error":"input URL is invalid or unclassified"}"""
        val r = parseDownloadEvent(line, kotlinx.serialization.json.Json) as DownloadEvent.Result
        val o = Downloader.outcome(code = 1, result = r, failedKeys = emptyMap(), moved = 0, job = job, stderrTail = "")
        assertTrue(o is RunOutcome.Failed)
        o as RunOutcome.Failed
        assertEquals("качалка не приняла ссылку на тайтл", o.why)
        assertTrue("повтор ссылку не исправит", o.permanent)
    }

    @Test fun `сервер не принял сессию — сбой с повтором`() {
        val o = Downloader.outcome(
            1, result(error = "page scrape: title 1: platform page needs that site's session (kino.example.org answered 401 Unauthorized)"),
            emptyMap(), 0, job, "",
        ) as RunOutcome.Failed
        assertTrue(o.why.startsWith("сервер не принял вход"))
        assertEquals(false, o.permanent)
    }

    @Test fun `ноль серий без причины — сбой`() {
        val o = Downloader.outcome(0, result(), emptyMap(), 0, job, "")
        assertEquals(RunOutcome.Failed("качалка ничего не скачала"), o)
    }

    @Test fun `скачано и перенесено — готово`() {
        assertEquals(RunOutcome.Done(1), Downloader.outcome(0, result(ok = 1), emptyMap(), 1, job, ""))
    }

    @Test fun `всё пропущено по просьбе человека — перекачать с force`() {
        assertEquals(RunOutcome.Force, Downloader.outcome(1, result(skipped = 1), emptyMap(), 0, job, ""))
    }

    @Test fun `с force всё снова пропущено — площадка не отдала поток`() {
        val o = Downloader.outcome(1, result(skipped = 1), emptyMap(), 0, job.copy(force = true), "")
        assertTrue(o is RunOutcome.Failed)
    }

    @Test fun `автоповтор, где всё уже было скачано, — готово без переноса`() {
        assertEquals(RunOutcome.Done(0), Downloader.outcome(1, result(skipped = 3), emptyMap(), 0, job.copy(attempt = 1), ""))
    }

    @Test fun `упавшая серия — сбой с повтором, готовые посчитаны`() {
        val o = Downloader.outcome(
            1, result(ok = 7, failed = 1), mapOf("S01E08" to "no variants found in master playlist"), 7, job, "",
        ) as RunOutcome.Failed
        assertTrue(o.why.startsWith("Скачано серий: 7. Не скачалась S01E08"))
        assertEquals(false, o.permanent)
    }

    @Test fun `процесс убит без итога — сбой`() {
        assertTrue(Downloader.outcome(137, null, emptyMap(), 0, job, "") is RunOutcome.Failed)
    }
}
