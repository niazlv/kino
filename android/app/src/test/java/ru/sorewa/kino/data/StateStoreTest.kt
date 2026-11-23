package ru.sorewa.kino.data

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class StateStoreTest {

    // ---- очередь: позиция ----

    @Test fun `позиция — последний пишущий побеждает`() {
        val first = mergeProgress(7, null, 1_000, 60_000, "t1")
        assertEquals(1_000L, first.positionMs)
        assertEquals(60_000L, first.durationMs)
        assertNull(first.watched)

        val next = mergeProgress(7, first, 5_000, 60_000, "t2")
        assertEquals(5_000L, next.positionMs)
        assertEquals("t2", next.clientTs)
    }

    @Test fun `репорт без хронометража хранит прежний`() {
        val first = mergeProgress(7, null, 1_000, 60_000, "t1")
        val next = mergeProgress(7, first, 2_000, 0, "t2")
        assertEquals(60_000L, next.durationMs)
    }

    @Test fun `отрицательная позиция не сохраняется`() {
        assertEquals(0L, mergeProgress(7, null, -5, 60_000, "t1").positionMs)
    }

    // ---- очередь: галочка ----

    @Test fun `галочка «смотрел» ставит позицию в конец`() {
        val next = mergeWatched(7, null, watched = true, durationMs = 120_000, clientTs = "t1")
        assertEquals(120_000L, next.positionMs)
        assertEquals(true, next.watched)
    }

    @Test fun `галочка «не смотрел» обнуляет позицию`() {
        val cur = mergeProgress(7, null, 100_000, 120_000, "t1")
        val next = mergeWatched(7, cur, watched = false, durationMs = 120_000, clientTs = "t2")
        assertEquals(0L, next.positionMs)
        assertEquals(false, next.watched)
    }

    // ---- очередь: патчи озвучки ----

    @Test fun `патч озвучки null-полем не перекрывает ждущее`() {
        val merged = mergePrefs(PrefsPatch(audio = "AniDUB"), PrefsPatch(source = ""))
        assertEquals("AniDUB", merged.audio)
        assertEquals("", merged.source)
        assertNull(merged.subtitle)
    }

    @Test fun `новое непустое поле патча перекрывает ждущее`() {
        val merged = mergePrefs(PrefsPatch(audio = "AniDUB", source = ""), PrefsPatch(audio = "AnimeLib"))
        assertEquals("AnimeLib", merged.audio)
        assertEquals("", merged.source)
    }

    @Test fun `патч ложится на серверные prefs`() {
        val out = applyPrefsPatch(Prefs(audio = "AniDUB", subtitle = "rus"), PrefsPatch(source = "animego"))
        assertEquals("AniDUB", out.audio)
        assertEquals("rus", out.subtitle)
        assertEquals("animego", out.source)
    }

    @Test fun `патч без серверных prefs становится ими`() {
        val out = applyPrefsPatch(null, PrefsPatch(audio = "AniDUB"))
        assertEquals("AniDUB", out.audio)
        assertNull(out.subtitle)
        assertNull(out.source)
    }

    // ---- оверлей на страницу тайтла ----

    private fun detail(vararg eps: Episode) =
        TitleDetail(Title(id = 7, kind = "serial", title = "Тьма"), eps.toList())

    @Test fun `ждущий прогресс ложится поверх серий сервера`() {
        val d = detail(
            Episode(id = 10, seasonNo = 1, episodeNo = 1),
            Episode(id = 11, seasonNo = 1, episodeNo = 2, positionMs = 5_000, status = "in_progress"),
        )
        val pending = listOf(
            PendingEp(10, positionMs = 60_000, durationMs = 120_000, clientTs = "t1"),
            PendingEp(11, positionMs = 120_000, durationMs = 120_000, watched = true, clientTs = "t1"),
        )
        val out = overlayEpisodes(d, pending)
        assertEquals(60_000L, out.episodes[0].positionMs)
        assertEquals("in_progress", out.episodes[0].status)
        assertEquals(120_000L, out.episodes[1].positionMs)
        assertEquals("completed", out.episodes[1].status)
    }

    @Test fun `галочка «не смотрел» на странице — серия чистая`() {
        val d = detail(Episode(id = 10, seasonNo = 1, episodeNo = 1, positionMs = 90_000, status = "in_progress"))
        val out = overlayEpisodes(d, listOf(PendingEp(10, positionMs = 0, watched = false, clientTs = "t1")))
        assertEquals(0L, out.episodes[0].positionMs)
        assertEquals("", out.episodes[0].status)
    }

    @Test fun `досмотренный до порога репорт на странице — просмотрено`() {
        val d = detail(Episode(id = 10, seasonNo = 1, episodeNo = 1))
        val out = overlayEpisodes(d, listOf(PendingEp(10, positionMs = 115_000, durationMs = 120_000, clientTs = "t1")))
        assertEquals("completed", out.episodes[0].status)
    }

    @Test fun `чужие серии оверлей не трогает`() {
        val d = detail(Episode(id = 10, seasonNo = 1, episodeNo = 1))
        val out = overlayEpisodes(d, listOf(PendingEp(99, positionMs = 60_000, durationMs = 120_000, clientTs = "t1")))
        assertEquals(d, out)
    }

    @Test fun `пустой оверлей возвращает как было`() {
        val d = detail(Episode(id = 10, seasonNo = 1, episodeNo = 1))
        assertEquals(d, overlayEpisodes(d, emptyList()))
    }

    // ---- метки: патчи вместо снимков ----

    @Test fun `дифф меток несёт только переключённое поле`() {
        assertEquals(MarksPatch(watchlist = true), marksDiff(Marks(), Marks(watchlist = true)))
        assertEquals(MarksPatch(), marksDiff(Marks(following = true), Marks(following = true)))
    }

    @Test fun `патч меток ложится на базу, не трогая остальное`() {
        val out = applyMarksPatch(Marks(following = true, watchlist = true), MarksPatch(hidden = true))
        assertEquals(Marks(following = true, watchlist = true, hidden = true), out)
    }

    @Test fun `метки — база сервер или кэш, ждущий патч поверх`() {
        val pending = PendingTitle(1, marksPatch = MarksPatch(watchlist = true), marksTs = "t1")
        assertEquals(Marks(following = true, watchlist = true), resolveMarks(Marks(following = true), Marks(hidden = true), pending))
        assertEquals(Marks(following = true), resolveMarks(Marks(following = true), Marks(hidden = true), null))
        assertEquals(Marks(hidden = true), resolveMarks(null, Marks(hidden = true), null))
        // Ни сервера, ни кэша — показать нечего: патч без базы не выдумывает.
        assertNull(resolveMarks(null, null, pending))
        assertNull(resolveMarks(null, null, null))
    }

    // ---- «Продолжить» из кэша ----

    @Test fun `ждущая позиция ложится на кэш продолжения`() {
        val items = listOf(ContinueItem(episodeId = 10, titleId = 1, title = "Тьма", positionMs = 1_000, durationMs = 120_000))
        val out = overlayContinue(items, listOf(PendingEp(10, positionMs = 60_000, durationMs = 120_000, clientTs = "t1")))
        assertEquals(60_000L, out[0].positionMs)
        assertEquals(120_000L, out[0].durationMs)
    }

    @Test fun `досмотренное в очереди выпадает из продолжения`() {
        val items = listOf(
            ContinueItem(episodeId = 10, titleId = 1, title = "Тьма", positionMs = 1_000, durationMs = 120_000),
            ContinueItem(episodeId = 11, titleId = 1, title = "Тьма", positionMs = 30_000, durationMs = 120_000),
        )
        val out = overlayContinue(items, listOf(PendingEp(10, positionMs = 120_000, durationMs = 120_000, watched = true, clientTs = "t1")))
        assertEquals(listOf(11L), out.map { it.episodeId })
    }

    @Test fun `галочка «не смотрел» обнуляет позицию в продолжении`() {
        val items = listOf(ContinueItem(episodeId = 10, titleId = 1, title = "Тьма", positionMs = 60_000, durationMs = 120_000))
        val out = overlayContinue(items, listOf(PendingEp(10, positionMs = 0, watched = false, clientTs = "t1")))
        assertEquals(0L, out[0].positionMs)
    }

    // ---- файл ----

    @Test fun `пустой и чужой файл — пустой стейт`() {
        assertEquals(StateFile(), StateStore.decodeState("{}"))
        assertEquals(StateFile(), StateStore.decodeState("""{"episodes":[],"x":1}"""))
    }

    @Test fun `файл переживает кругосветку`() {
        val f = StateFile(
            episodes = listOf(PendingEp(10, positionMs = 60_000, durationMs = 120_000, watched = true, clientTs = "t1")),
            titles = listOf(PendingTitle(1, marksPatch = MarksPatch(following = true), marksTs = "t1", prefs = PrefsPatch(audio = "AniDUB"), prefsTs = "t2")),
            continueCache = listOf(ContinueItem(episodeId = 10, titleId = 1, title = "Тьма", positionMs = 60_000, durationMs = 120_000)),
        )
        val text = Json { encodeDefaults = true }.encodeToString(f)
        assertEquals(f, StateStore.decodeState(text))
    }
}
