import Foundation

/// Синк офлайн-прогресса — порт data/Sync.kt: при открытии «Загрузок» и
/// при наличии сети; ошибка одной записи не рушит обход.
enum Sync {

    /// Сколько записей доложено. Запись чистится только когда ушло всё:
    /// и позиция, и «просмотрено».
    @MainActor
    @discardableResult
    static func pushLocalProgress() async -> Int {
        let library = LocalLibrary.shared
        // Записи описи — с id серий того сервера, при котором начали. Сменили
        // сервер посреди обхода — стоп: id прежнего у нового значат чужую серию.
        let generation = ServerConfig.generation
        await resolveLocalTitles()
        var done = 0
        guard ServerConfig.generation == generation else { return done }
        let byTitle = Dictionary(grouping: library.dirtyEntries.filter { $0.titleId > 0 }, by: \.titleId)
        for (titleId, entries) in byTitle {
            guard ServerConfig.generation == generation else { return done }
            guard let detail = try? await Api.shared.title(titleId) else { continue }
            for e in entries {
                guard ServerConfig.generation == generation else { return done }
                guard let ep = detail.episodes.first(where: { $0.seasonNo == e.season && $0.episodeNo == e.episode }) else { continue }
                do {
                    if e.positionMs > 0 && e.durationMs > 0 {
                        let report = ProgressReport(positionMs: e.positionMs, durationMs: e.durationMs,
                                                    clientTs: ISO8601DateFormatter().string(from: Date()))
                        try await Api.shared.reportProgress(ep.id, report)
                    }
                    guard ServerConfig.generation == generation else { return done }
                    if e.watched { _ = try await Api.shared.setWatched(ep.id, true) }
                    guard ServerConfig.generation == generation else { return done }
                    library.markSynced(e.id)
                    done += 1
                } catch {
                    // Осталась dirty — уедет при следующем синке.
                }
            }
        }
        return done
    }

    /// Записи без тайтла: suggest по названию, только однозначное совпадение
    /// («лучше без тайтла, чем не тот тайтл»).
    @MainActor
    private static func resolveLocalTitles() async {
        let library = LocalLibrary.shared
        let generation = ServerConfig.generation
        let unknown = Dictionary(grouping: library.entries.filter { $0.titleId == 0 }, by: \.title)
        for (name, entries) in unknown {
            guard let page = try? await Api.shared.suggest(name, limit: 10) else { continue }
            guard ServerConfig.generation == generation else { return }
            let want = normalizeName(name)
            let matched = page.items.filter { namesOf($0.title).contains(want) }
            guard matched.count == 1 else { continue }
            for e in entries { library.retitle(id: e.id, titleId: matched[0].id) }
        }
    }

    /// «Тьма / Dark» — годится и полное имя, и каждая половина.
    private static func namesOf(_ full: String) -> [String] {
        ([full] + full.components(separatedBy: " / ")).map(normalizeName).filter { !$0.isEmpty }
    }
}
