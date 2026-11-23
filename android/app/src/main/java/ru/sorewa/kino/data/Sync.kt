package ru.sorewa.kino.data

import kotlinx.coroutines.sync.Mutex

/**
 * Синк офлайн-стейта с сервером — порт syncLocalProgress и
 * resolveLocalTitles витрины, плюс слив очереди StateStore.
 *
 * Позиции, набранные офлайн, уезжают на сервер при появлении сети: dirty &&
 * titleId>0 → /progress/<episodeId>. Adopt-записи без тайтла связываются
 * с каталогом через suggest — «лучше без тайтла, чем не тот тайтл».
 *
 * Ждущий стейт стрима (позиция, галочки, метки, озвучка) — из StateStore:
 * туда всё попадает до PUT, сорвавшийся запрос не теряется молча.
 */
class Sync(
    private val api: Api,
    private val library: LocalLibrary,
    private val state: StateStore,
) {

    /** Прогресс: сколько записей доложено. Ошибки одной записи не рушат обход. */
    suspend fun syncLocalProgress(): Int {
        var done = 0
        val byTitle = library.dirtyEntries().groupBy { it.titleId }
        for ((titleId, entries) in byTitle) {
            val detail = try { api.title(titleId) } catch (_: Exception) { continue }
            for (e in entries) {
                val ep = detail.episodes.find {
                    it.seasonNo == e.season && it.episodeNo == e.episode
                } ?: continue
                try {
                    if (e.positionMs > 0 && e.durationMs > 0) {
                        api.progress(ep.id, e.positionMs, e.durationMs)
                    }
                    if (e.watched) api.setWatched(ep.id, true)
                    library.synced(e.id)
                    done++
                } catch (_: Exception) {
                    // Осталась dirty — уедет при следующем синке.
                }
            }
        }
        return done
    }

    private val drainMutex = Mutex()

    /**
     * Очередь StateStore — на сервер. Внутри серии: позиция, потом галочка
     * (галочка значит «досмотрена», позиция её не должна пережить). ts
     * записи уезжает как client_ts — слияние сервера само решает, чьё
     * новее. Частично доложенная серия остаётся в очереди: повтор тех же
     * значений с тем же ts идемпотентен для сервера.
     */
    suspend fun syncPendingState(): Int {
        if (api.currentToken.isEmpty()) return 0
        if (state.pendingEpisodes().isEmpty() && state.pendingTitles().isEmpty()) return 0
        if (!drainMutex.tryLock()) return 0
        try {
            var done = 0
            for (t in state.pendingTitles()) {
                try {
                    if (t.marksPatch != null) {
                        // Сначала серверные метки, своё поле поверх: PUT
                        // заменяет целиком, снимок из кэша затёр бы метки,
                        // поставленные с другого устройства, пока мы были
                        // офлайн. GET не удался — запись остаётся в очереди.
                        val server = api.marks(t.titleId)
                        api.setMarks(t.titleId, applyMarksPatch(server, t.marksPatch))
                        state.ackMarks(t.titleId, t.marksTs)
                        done++
                    }
                } catch (_: Exception) { }
                try {
                    if (t.prefs != null) {
                        api.setPrefs(t.titleId, t.prefs)
                        state.ackPrefs(t.titleId, t.prefsTs)
                        done++
                    }
                } catch (_: Exception) { }
            }
            for (e in state.pendingEpisodes()) {
                try {
                    if (e.positionMs > 0 && e.durationMs > 0) {
                        api.progress(e.episodeId, e.positionMs, e.durationMs, e.clientTs)
                    }
                    if (e.watched != null) api.setWatched(e.episodeId, e.watched)
                    state.ackEpisode(e.episodeId, e.clientTs)
                    done++
                } catch (_: Exception) {
                    // Осталась в очереди — уедет при следующем синке.
                }
            }
            return done
        } finally {
            drainMutex.unlock()
        }
    }

    /** Adopt-записи без тайтла: suggest по названию папки, точное совпадение. */
    suspend fun resolveLocalTitles(): Int {
        var resolved = 0
        val unknown = library.list().filter { it.titleId == 0L }.groupBy { it.title }
        for ((name, entries) in unknown) {
            val hits = try { api.suggest(name, 10).items } catch (_: Exception) { continue }
            // Папку называют и полным именем («Тьма / Dark» — так кладёт сама
            // качалка), и одним русским или оригинальным названием. Годится
            // любое, но только когда совпал ровно один тайтл.
            val want = normalize(name)
            val matched = hits.filter { h -> namesOf(h.title).any { it == want } }
            val hit = matched.singleOrNull() ?: continue
            for (e in entries) {
                library.setTitleId(e.id, hit.id)
                resolved++
            }
        }
        return resolved
    }

    private fun namesOf(full: String): List<String> =
        (listOf(full) + full.split(" / ")).map(::normalize).filter { it.isNotEmpty() }

    private fun normalize(s: String): String =
        s.lowercase().replace("ё", "е").filter { it.isLetterOrDigit() }
}
