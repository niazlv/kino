package ru.sorewa.kino.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Хелперы библиотеки скачанного: ключи, разбор имён, отпечатки, папки. */
class LocalLibraryTest {

    /**
     * library.json старой оболочки (Library.java пишет через org.json: «/»
     * экранирован, у подобранных файлов нет addedAt, title может не быть
     * вовсе). Обновление поверх неё не переносит опись — читает её как есть.
     */
    @Test
    fun `reads library json written by the old shell`() {
        val old = """[{"id":"53014:1:2","titleId":53014,"title":"Тьма \/ Dark","season":1,"episode":2,""" +
            """"uri":"content:\/\/media\/external\/video\/media\/1234","size":734003200,"addedAt":1761900000000,""" +
            """"positionMs":61000,"durationMs":3200000,"watched":false,"dirty":true},""" +
            """{"id":"f1a2b3c:2:5","titleId":0,"season":2,"episode":5,""" +
            """"uri":"content:\/\/com.android.externalstorage.documents\/tree\/primary%3AMovies\/document\/primary%3AMovies%2Fkino%2FS02E05.mkv",""" +
            """"size":1000,"positionMs":0,"durationMs":0,"watched":true,"dirty":false}]"""
        val list = LocalLibrary.decodeStore(old)
        assertEquals(2, list.size)
        val a = list[0]
        assertEquals("53014:1:2", a.id)
        assertEquals(53014L, a.titleId)
        assertEquals("Тьма / Dark", a.title)
        assertEquals("content://media/external/video/media/1234", a.uri)
        assertEquals(734003200L, a.size)
        assertEquals(61000L, a.positionMs)
        assertEquals(3200000L, a.durationMs)
        assertEquals(true, a.dirty)
        val b = list[1]
        assertEquals("", b.title)
        assertEquals(0L, b.addedAt)
        assertEquals(true, b.watched)
        assertEquals(
            "content://com.android.externalstorage.documents/tree/primary%3AMovies/document/primary%3AMovies%2Fkino%2FS02E05.mkv",
            b.uri,
        )
    }

    @Test
    fun `seasonEpisode parses SxxEyy`() {
        assertEquals(listOf(2, 3), LocalLibrary.seasonEpisode("Тайтл - S02E03.mkv"))
        assertEquals(listOf(1, 105), LocalLibrary.seasonEpisode("film S1E105.mp4"))
        assertNull(LocalLibrary.seasonEpisode("movie.mkv"))
        // Расширение не проверяется — как в старой оболочке: парсится имя,
        // фильтр по .mkv/.mp4 делает вызывающий.
        assertEquals(listOf(1, 2), LocalLibrary.seasonEpisode("s1e2.txt"))
    }

    @Test
    fun `titleIdOf reads page url`() {
        assertEquals(53014L, LocalLibrary.titleIdOf("https://kino.example.org/#/title/53014/s2e3"))
        assertEquals(53014L, LocalLibrary.titleIdOf("https://kino.example.org/#/title/53014"))
        assertEquals(0L, LocalLibrary.titleIdOf("https://example.com/#/search/abc"))
    }

    @Test
    fun `sign dedupes differently-encoded files`() {
        val a = LocalLibrary.sign("Тайтл / Сезон", 2, 3, 1_000_000)
        val b = LocalLibrary.sign("тайтл / сезон", 2, 3, 1_000_000)
        assertEquals(a, b)
        org.junit.Assert.assertNotEquals(a, LocalLibrary.sign("Тайтл / Сезон", 2, 4, 1_000_000))
        org.junit.Assert.assertNotEquals(a, LocalLibrary.sign("Тайтл / Сезон", 2, 3, 2_000_000))
    }

    @Test
    fun `safeFolder strips forbidden chars`() {
        // «/» тоже запрещённый символ — превращается в пробел (adopt потом
        // разворачивает два+ пробела обратно в « / »).
        assertEquals("Тайтл  Сезон", LocalLibrary.safeFolder("Тайтл: Сезон?"))
        assertEquals("Тайтл   Сезон", LocalLibrary.safeFolder("Тайтл / Сезон"))
        assertEquals("kino", LocalLibrary.safeFolder("///"))
    }

    @Test
    fun `unescapeFolder restores slash`() {
        // Слэш в папке — это «Тайтл / Сезон», где safe() заменил «/» пробелом:
        // вокруг остаются обычные пробелы, разворот — только для 3+ подряд.
        assertEquals("Тайтл / Сезон", LocalLibrary.unescapeFolder("Тайтл / Сезон"))
        assertEquals("Просто тайтл", LocalLibrary.unescapeFolder("Просто тайтл"))
    }

    @Test
    fun `episodeFileName pads numbers`() {
        assertEquals("S02E03.mkv", LocalLibrary.episodeFileName(2, 3, "mkv"))
        assertEquals("S01E01.mkv", LocalLibrary.episodeFileName(1, 1, "mkv"))
    }
}
