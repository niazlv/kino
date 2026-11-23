import Foundation

/// Логика страницы тайтла — порт TitleScreen.kt (nextUp, сезоны).

struct NextUp {
    let episode: Episode
    let label: String
    let sub: String
}

/// Что включать по большой кнопке: начатая серия, иначе следующая за
/// последней досмотренной, иначе первая. Для фильма — единственная.
func nextUp(_ d: TitleDetail) -> NextUp? {
    let eps = d.episodes.sorted { ($0.seasonNo, $0.episodeNo) < ($1.seasonNo, $1.episodeNo) }
    if eps.isEmpty { return nil }
    let movie = isMovie(d)

    func se(_ e: Episode) -> String {
        e.seasonNo > 0 ? "\(e.seasonNo)×\(e.episodeNo)" : "серия \(e.episodeNo)"
    }
    func left(_ e: Episode) -> String {
        let total = e.durationSec * 1000
        return total > 0 && e.positionMs > 0 ? "осталось \(fmtMins((total - e.positionMs) / 60_000))" : ""
    }

    if let started = eps.first(where: { $0.positionMs > 0 && $0.status != "completed" }) {
        return NextUp(episode: started, label: movie ? "Продолжить" : "Продолжить \(se(started))", sub: left(started))
    }
    let lastDone = eps.lastIndex { $0.status == "completed" } ?? -1
    if lastDone < 0 {
        return NextUp(episode: eps[0], label: movie ? "Смотреть" : "Смотреть \(se(eps[0]))", sub: "")
    }
    if lastDone < eps.count - 1 {
        let n = eps[lastDone + 1]
        return NextUp(episode: n, label: "Смотреть \(se(n))", sub: "следующая серия")
    }
    return NextUp(episode: eps[0], label: "Пересмотреть", sub: "всё просмотрено")
}

func isMovie(_ d: TitleDetail) -> Bool {
    d.title.kind == "movie"
        || (d.episodes.count == 1 && d.episodes[0].seasonNo <= 1 && d.episodes[0].episodeNo <= 1)
}

/// Сезоны, досмотренные целиком — у них галочка в переключателе.
func watchedSeasons(_ d: TitleDetail) -> Set<Int> {
    var result: Set<Int> = []
    let bySeason = Dictionary(grouping: d.episodes, by: \.seasonNo)
    for (season, eps) in bySeason where eps.allSatisfy({ $0.status == "completed" }) {
        result.insert(season)
    }
    return result
}

func seasonsOf(_ d: TitleDetail) -> [Int] {
    Array(Set(d.episodes.map(\.seasonNo))).sorted()
}

func episodesOf(_ d: TitleDetail, _ season: Int) -> [Episode] {
    d.episodes
        .filter { $0.seasonNo == season }
        .sorted { ($0.episodeNo, $0.id) < ($1.episodeNo, $1.id) }
}

/// Отметка сезона применяется к экрану сразу, до ответа сервера.
/// Снятая отметка обнуляет и позицию — сервер делает так же.
func withSeasonWatched(_ d: TitleDetail, _ season: Int, watched: Bool) -> TitleDetail {
    var copy = d
    copy.episodes = d.episodes.map { ep in
        guard ep.seasonNo == season else { return ep }
        var e = ep
        e.status = watched ? "completed" : nil
        e.positionMs = 0
        return e
    }
    return copy
}
