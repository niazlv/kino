package ru.sorewa.kino.ui.phone

import org.junit.Assert.assertEquals
import org.junit.Test
import ru.sorewa.kino.data.Episode
import ru.sorewa.kino.data.VoiceOption
import ru.sorewa.kino.data.VoiceWay
import ru.sorewa.kino.downloader.DownloadJob

class DownloadPlanTest {
    private val base = "https://kino.example.org/#/title/42"
    private fun ep(id: Long, s: Int, n: Int) = Episode(id = id, seasonNo = s, episodeNo = n)
    private fun keys(s: Int, vararg n: Int) = n.map { s to it }

    private fun plan(scope: String, inScope: List<EpKey>, todo: List<EpKey>) =
        downloadJobs(base, "Тайтл", "720p", movie = false, scopeKind = scope, inScope = inScope, todo = todo)
            .map { it.pageUrl.removePrefix(base) + (if (it.seasons.isNotEmpty()) " s=${it.seasons}" else "") }

    @Test fun `сезон, где скачано 7 из 8, — только недостающая серия`() {
        val season = keys(1, 1, 2, 3, 4, 5, 6, 7, 8)
        assertEquals(listOf("/s1e5"), plan("season", season, listOf(1 to 5)))
    }

    @Test fun `нескачанный сезон — одним заданием, как раньше`() {
        val season = keys(2, 1, 2, 3)
        assertEquals(listOf(" s=2"), plan("season", season, season))
    }

    @Test fun `весь сериал без скачанного — одно задание на тайтл`() {
        val all = keys(1, 1, 2) + keys(2, 1, 2)
        assertEquals(listOf(""), plan("all", all, all))
    }

    @Test fun `весь сериал с частью скачанного — целые сезоны сезонами, остальное по серии`() {
        val all = keys(1, 1, 2, 3) + keys(2, 1, 2)
        val todo = listOf(2 to 2, 1 to 3, 2 to 1)
        assertEquals(listOf("/s1e3", " s=2"), plan("all", all, todo))
    }

    @Test fun `всё скачано — заданий нет`() {
        assertEquals(emptyList<String>(), plan("season", keys(1, 1, 2), emptyList()))
        assertEquals(emptyList<String>(), plan("episode", keys(1, 1), emptyList()))
    }

    @Test fun `выбор галочками — по серии в порядке номеров`() {
        assertEquals(listOf("/s1e2", "/s2e1"), plan("pick", listOf(2 to 1, 1 to 2), listOf(2 to 1, 1 to 2)))
    }

    @Test fun `фильм — ссылкой на тайтл, без s0e1`() {
        val jobs = downloadJobs(base, "Фильм", "", movie = true, scopeKind = "episode", inScope = listOf(0 to 1), todo = listOf(0 to 1))
        assertEquals(listOf(base), jobs.map { it.pageUrl })
        assertEquals(true, jobs.single().movie)
    }

    @Test fun `доп серии сериала — по id серии, а не сезоном 0`() {
        val ids = mapOf((0 to 1) to 501L, (0 to 2) to 502L)
        val urls = downloadJobs(base, "Т", "", movie = false, scopeKind = "season", inScope = keys(0, 1, 2), todo = keys(0, 1, 2), ids = ids)
            .map { it.pageUrl.removePrefix(base) to it.seasons }
        assertEquals(listOf("/501" to "", "/502" to ""), urls)
    }

    @Test fun `в загрузках — серия по id`() {
        val eps = listOf(ep(501, 0, 1), ep(502, 0, 2), ep(3, 1, 1))
        assertEquals(setOf(0 to 2), inFlightKeys(listOf(DownloadJob(pageUrl = "$base/502", title = "Т")), 42, eps))
    }

    @Test fun `в загрузках — серии из очереди, сезона и всего тайтла`() {
        val eps = listOf(ep(1, 1, 1), ep(2, 1, 2), ep(3, 2, 1), ep(4, 2, 2))
        val one = DownloadJob(pageUrl = "$base/s1e2", title = "Тайтл")
        val season = DownloadJob(pageUrl = base, title = "Тайтл", seasons = "2")
        val other = DownloadJob(pageUrl = "https://kino.example.org/#/title/7/s1e1", title = "Другой")
        assertEquals(setOf(1 to 2, 2 to 1, 2 to 2), inFlightKeys(listOf(one, season, other), 42, eps))
        assertEquals(eps.map { it.seasonNo to it.episodeNo }.toSet(), inFlightKeys(listOf(DownloadJob(pageUrl = base, title = "Тайтл")), 42, eps))
        assertEquals(emptySet<EpKey>(), inFlightKeys(listOf(other), 42, eps))
    }

    // ---- озвучка ----

    private val anilibria = VoiceOption("anilibria", "AniLibria", listOf(VoiceWay("animego_me", "2:1"), VoiceWay("anilibria", "anilibria")))
    private val dub = VoiceOption("dub", "Дубляж", listOf(VoiceWay("kinopub"), VoiceWay("lordfilm", "t215")))
    private val mvo = VoiceOption("mvo", "Многоголосый", listOf(VoiceWay("kinopub")))

    @Test fun `озвучка и площадка уходят во все задания`() {
        val all = keys(1, 1, 2) + keys(2, 1, 2)
        val jobs = downloadJobs(base, "Тайтл", "", movie = false, scopeKind = "all", inScope = all,
            todo = listOf(1 to 2, 2 to 1, 2 to 2), voice = anilibria, source = "animego_me")
        assertEquals(2, jobs.size)
        jobs.forEach {
            assertEquals("anilibria", it.voice)
            assertEquals("AniLibria", it.voiceTitle)
            assertEquals("animego_me", it.source)
        }
        // Без выбора — как раньше.
        downloadJobs(base, "Тайтл", "", movie = true, scopeKind = "episode", inScope = keys(0, 1), todo = keys(0, 1))
            .forEach { assertEquals("", it.voice); assertEquals("", it.source) }
    }

    @Test fun `сразу предлагается озвучка, которую человек слушает`() {
        val list = listOf(anilibria, dub, mvo)
        // Подпись площадки сводится к ключу тем же разбором, что у плеера.
        val keyOf = { s: String -> if (s == "Дублированный") "dub" else s.lowercase() }
        assertEquals(dub, rememberedVoice(list, "Дублированный", keyOf))
        assertEquals(anilibria, rememberedVoice(list, "AniLibria", keyOf))
        assertEquals(mvo, rememberedVoice(list, "Многоголосый", { "?" }))
        assertEquals(null, rememberedVoice(list, "SHIZA Project", keyOf))
        assertEquals(null, rememberedVoice(list, null, keyOf))
        assertEquals(null, rememberedVoice(emptyList(), "AniLibria", keyOf))
    }

    @Test fun `пояснение говорит, что окажется в файле`() {
        assertEquals("В файле будет эта озвучка", voiceHint(anilibria, manyEpisodes = false))
        assertEquals("Все дорожки потока, эта — среди них", voiceHint(mvo, manyEpisodes = false))
        assertEquals("Одна дорожка — эта; с kinopub — все его дорожки", voiceHint(dub, manyEpisodes = false))
        assertEquals("В файле будет эта озвучка. Где её нет, серия придёт в той, что есть", voiceHint(anilibria, manyEpisodes = true))
        assertEquals("Как выберет площадка; с kinopub — все дорожки", voiceHint(null, manyEpisodes = true))
    }
}
