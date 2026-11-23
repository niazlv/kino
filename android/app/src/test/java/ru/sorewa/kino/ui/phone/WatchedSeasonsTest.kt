package ru.sorewa.kino.ui.phone

import org.junit.Assert.assertEquals
import org.junit.Test
import ru.sorewa.kino.data.Episode
import ru.sorewa.kino.data.Title
import ru.sorewa.kino.data.TitleDetail

class WatchedSeasonsTest {
    private fun ep(id: Long, s: Int, n: Int, status: String? = null) =
        Episode(id = id, seasonNo = s, episodeNo = n, status = status)

    @Test fun `галочка только у сезона, досмотренного целиком`() {
        val d = TitleDetail(
            Title(id = 1, kind = "series"),
            listOf(
                ep(1, 1, 1, "completed"), ep(2, 1, 2, "completed"),
                ep(3, 2, 1, "completed"), ep(4, 2, 2, "in_progress"),
                ep(5, 3, 1),
            ),
        )
        assertEquals(setOf(1), watchedSeasons(d))
    }

    @Test fun `отметка сезона сразу ставит галочку`() {
        val d = TitleDetail(Title(id = 1, kind = "series"), listOf(ep(1, 5, 1), ep(2, 5, 2, "completed")))
        assertEquals(setOf(5), watchedSeasons(withSeasonWatched(d, 5, true)))
        assertEquals(emptySet<Int>(), watchedSeasons(withSeasonWatched(d, 5, false)))
    }
}
