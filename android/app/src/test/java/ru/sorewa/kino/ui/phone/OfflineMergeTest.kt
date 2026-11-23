package ru.sorewa.kino.ui.phone

import org.junit.Assert.assertEquals
import org.junit.Test
import ru.sorewa.kino.data.Episode
import ru.sorewa.kino.data.LocalLibrary
import ru.sorewa.kino.data.Title
import ru.sorewa.kino.data.TitleDetail

class OfflineMergeTest {
    private fun ep(id: Long, s: Int, n: Int, status: String? = null, pos: Long = 0) =
        Episode(id = id, seasonNo = s, episodeNo = n, status = status, positionMs = pos, durationSec = 1200)

    private fun entry(s: Int, n: Int, pos: Long = 0, watched: Boolean = false, dirty: Boolean = false) =
        LocalLibrary.Entry(
            id = "7:$s:$n", titleId = 7, title = "Тьма", season = s, episode = n, uri = "content://x/$s/$n",
            positionMs = pos, durationMs = 1_200_000, watched = watched, dirty = dirty,
        )

    private val detail = TitleDetail(
        Title(id = 7, kind = "serial", title = "Тьма"),
        listOf(ep(1, 1, 1, "completed"), ep(2, 1, 2), ep(3, 1, 3), ep(4, 2, 1)),
    )

    @Test fun `без сети позиция из самолёта видна на странице`() {
        val local = mapOf((1 to 2) to entry(1, 2, pos = 300_000))
        val d = withLocal(detail, local, offline = true)
        val e = d.episodes.first { it.id == 2L }
        assertEquals(300_000L, e.positionMs)
        assertEquals("in_progress", e.status)
    }

    @Test fun `с сетью верим серверу, пока своё уже отправлено`() {
        val local = mapOf((1 to 2) to entry(1, 2, pos = 300_000))
        val d = withLocal(detail, local, offline = false)
        assertEquals(0L, d.episodes.first { it.id == 2L }.positionMs)
        // Не отправленное (dirty) — поверх сервера и с сетью.
        val dirty = mapOf((1 to 2) to entry(1, 2, pos = 300_000, dirty = true))
        assertEquals(300_000L, withLocal(detail, dirty, offline = false).episodes.first { it.id == 2L }.positionMs)
    }

    @Test fun `большая кнопка без сети включает только скачанное`() {
        // Скачаны 1×3 и 2×1; 1×2 не скачана — её пропускаем.
        val local = mapOf((1 to 3) to entry(1, 3), (2 to 1) to entry(2, 1))
        val up = nextUp(downloadedOnly(withLocal(detail, local, offline = true), local))
        assertEquals(3L, up?.episode?.id)
    }

    @Test fun `досмотренная в самолёте серия — просмотрена`() {
        val local = mapOf((1 to 3) to entry(1, 3, pos = 1_190_000, watched = true))
        val d = withLocal(detail, local, offline = true)
        assertEquals("completed", d.episodes.first { it.id == 3L }.status)
    }
}
