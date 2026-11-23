package ru.sorewa.kino.downloader

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.nio.file.Files

/**
 * Продолжение загрузки: готовое берётся по состоянию утилиты при любом
 * исходе прогона, причина сбоя — по-человечески, а не случайной строкой INFO.
 */
class ResumeTest {

    private fun work(): File = Files.createTempDirectory("kino-work").toFile()

    /** Папка серии так, как её оставляет утилита: файлы + .kinopub-state.json. */
    private fun series(work: File, done: List<Int>, partial: Int? = null): File {
        val dir = File(work, "Даже у студсовета есть косяки! _ There Is Also a Hole")
        val season = File(dir, "Season 01").apply { mkdirs() }
        val recs = done.joinToString(",\n") { e ->
            val f = File(season, "S01E%02d.mkv".format(e)).apply { writeBytes(ByteArray(1024)) }
            """"S1E$e": {"season": 1, "episode": $e, "path": "${f.absolutePath}", "bytes": 1024}"""
        }
        // Недосшитая серия: файл есть, в состоянии её нет — забирать нельзя.
        if (partial != null) File(season, "S01E%02d.mkv".format(partial)).writeBytes(ByteArray(10))
        File(dir, ".kinopub-state.json").writeText(
            """
            {"series": "56975",
             "metadata": {"title": "Даже у студсовета есть косяки! / There Is Also a Hole", "input_url": "https://kino.example.org/#/title/56975"},
             "completed": {$recs}}
            """.trimIndent(),
        )
        return dir
    }

    @Test
    fun `готовые серии берутся по состоянию, недосшитая — нет`() {
        val w = work()
        series(w, done = (1..7).toList(), partial = 8)
        val ready = Exporter.readyFiles(w)
        assertEquals((1..7).toList(), ready.map { it.episode })
        assertTrue(ready.all { it.pageUrl.endsWith("/title/56975") })
        assertEquals("Даже у студсовета есть косяки! / There Is Also a Hole", ready.first().title)
        assertFalse(ready.any { it.file.name == "S01E08.mkv" })
    }

    @Test
    fun `уже перенесённую серию второй раз не берём`() {
        val w = work()
        series(w, done = listOf(1, 2))
        Exporter.readyFiles(w).first().file.delete() // экспорт уносит файл
        assertEquals(listOf(2), Exporter.readyFiles(w).map { it.episode })
    }

    @Test
    fun `id тайтла из состояния`() {
        val w = work()
        val dir = series(w, done = listOf(1))
        assertEquals("56975", Exporter.seriesOf(File(dir, ".kinopub-state.json")))
    }

    @Test
    fun `причина сбоя — серия и её ошибка, а не строка INFO`() {
        val why = Downloader.describeFailure(
            mapOf("S01E08" to "no variants found in master playlist"),
            "2026-11-21 23:47:10 [INFO] [downloader] muxing HLS streams episode=S01E07",
            done = 7,
        )
        assertTrue(why, why.startsWith("Скачано серий: 7. Не скачалась S01E08"))
        assertFalse(why, "[INFO]" in why)
    }

    @Test
    fun `оборванный процесс — так и говорим`() {
        val why = Downloader.describeFailure(
            emptyMap(),
            "2026-11-21 23:47:10 [INFO] [downloader] muxing HLS streams episode=S01E07",
            done = 0,
        )
        assertEquals("загрузка оборвалась (процесс остановлен системой)", why)
    }

    @Test
    fun `force уходит в аргументы утилиты`() {
        val args = Downloader.baseArgs("/bin", "/ff", "/w", DownloadJob(pageUrl = "u", title = "t", force = true), "", "")
        assertTrue("--force" in args)
        val plain = Downloader.baseArgs("/bin", "/ff", "/w", DownloadJob(pageUrl = "u", title = "t"), "", "")
        assertFalse("--force" in plain)
    }

    @Test
    fun `пауза перед автоповтором растёт`() {
        assertTrue(Downloader.backoffMs(0) < Downloader.backoffMs(1))
        assertTrue(Downloader.backoffMs(1) < Downloader.backoffMs(2))
    }
}
