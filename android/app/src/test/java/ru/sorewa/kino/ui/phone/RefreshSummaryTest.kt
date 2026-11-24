package ru.sorewa.kino.ui.phone

import org.junit.Assert.assertEquals
import org.junit.Test
import ru.sorewa.kino.data.SourceRef
import ru.sorewa.kino.data.TitleRefresh

class RefreshSummaryTest {
    private val ag = SourceRef("animego_me", "AnimeGO.me")
    private val ya = SourceRef("yummy", "YummyAnime")
    private val al = SourceRef("anilibria", "Анилибрия")

    @Test fun `нашлась новая площадка и серии`() {
        val r = TitleRefresh(asked = listOf(ag, ya), added = listOf(ag), refetched = listOf(al, ag),
            episodesBefore = 10, episodesAfter = 12)
        assertEquals("Добавлено: AnimeGO.me. Новых серий: 2", refreshSummary(r))
    }

    @Test fun `спросили, но тайтла у них нет`() {
        val r = TitleRefresh(asked = listOf(ya), refetched = listOf(al), episodesBefore = 10, episodesAfter = 10)
        assertEquals("У других площадок тайтл не нашёлся. Серии перечитаны", refreshSummary(r))
    }

    @Test fun `все включённые площадки уже у тайтла`() {
        val r = TitleRefresh(refetched = listOf(al), episodesBefore = 10, episodesAfter = 10)
        assertEquals("Других включённых площадок нет. Серии перечитаны", refreshSummary(r))
    }

    @Test fun `молчащие площадки названы`() {
        val r = TitleRefresh(failed = listOf(ag), refetchFailed = listOf(al))
        assertEquals("Не ответили: AnimeGO.me. Серии не отдали: Анилибрия", refreshSummary(r))
    }

    @Test fun `только что обновляли`() {
        assertEquals("Тайтл только что обновляли — повторить можно через 42 с", refreshSummary(TitleRefresh(waitSec = 42)))
    }
}
