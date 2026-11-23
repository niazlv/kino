import XCTest
@testable import Kino

/// Декодирование ответов сервера — клиент не должен падать на новых полях.
final class ModelsTests: XCTestCase {

    func testPageDecoding() throws {
        let json = """
        {"items":[{"id":1,"kind":"series","title":"Тьма / Dark","year":2017,"hasArt":true}],
         "total":50,"nextOffset":1,"hasMore":true,"unknownField":123}
        """.data(using: .utf8)!
        let page = try JSONDecoder().decode(Page<Title>.self, from: json)
        XCTAssertEqual(page.items.count, 1)
        XCTAssertEqual(page.items[0].id, 1)
        XCTAssertEqual(page.items[0].title, "Тьма / Dark")
        XCTAssertTrue(page.hasMore)
        XCTAssertEqual(page.activeTotal, 0) // отсутствует → дефолт
    }

    func testPlaybackDecoding() throws {
        let json = """
        {"ticketId":"t1","url":"/stream/t1/master.m3u8","protocol":"HLS",
         "audios":[{"id":"a1","title":"AniLibria"}],
         "subtitles":[{"id":"s1","title":"Рус","url":"/stream/t1/sub/0.vtt","default":true}],
         "renditions":[{"height":1080,"codec":"hvc1"},{"height":720}],
         "source":{"id":"animego_fr","name":"AnimeGo"},
         "passed":[{"id":"x","name":"X","reason":"медленно"}],
         "segments":[{"kind":"intro","start":10,"end":90}],
         "extra":42}
        """.data(using: .utf8)!
        let pb = try JSONDecoder().decode(Playback.self, from: json)
        XCTAssertEqual(pb.streamProtocol, "HLS")
        XCTAssertEqual(pb.audios.first?.title, "AniLibria")
        XCTAssertTrue(pb.subtitles.first?.isDefault ?? false)
        XCTAssertEqual(pb.renditions.count, 2)
        XCTAssertEqual(pb.segments.first?.kind, "intro")
    }

    func testTrackDefaultKey() throws {
        let json = """
        {"id":"a1","title":"Дорожка 2","default":true}
        """.data(using: .utf8)!
        let t = try JSONDecoder().decode(Track.self, from: json)
        XCTAssertTrue(t.isDefault)
    }

    func testTitleDetailDefaults() throws {
        let json = """
        {"title":{"id":53014,"title":"Kuragehime"},"episodes":[{"id":9,"seasonNo":1,"episodeNo":2}]}
        """.data(using: .utf8)!
        let d = try JSONDecoder().decode(TitleDetail.self, from: json)
        XCTAssertEqual(d.episodes.count, 1)
        XCTAssertNil(d.prefs)
        XCTAssertEqual(d.episodes[0].durationSec, 0)
    }

    func testVoiceList() throws {
        let json = """
        {"voices":[{"key":"anidub","title":"AniDUB","ways":[{"source":"animego_fr"}]}],
         "silent":[{"id":"kp","name":"KinoPub"}]}
        """.data(using: .utf8)!
        let v = try JSONDecoder().decode(VoiceList.self, from: json)
        XCTAssertEqual(v.voices.first?.key, "anidub")
        XCTAssertEqual(v.silent.first?.name, "KinoPub")
    }
}

/// Логика тайтла — nextUp/сезоны (порт тестов TitleScreen.kt).
final class TitleLogicTests: XCTestCase {

    private func detail(kind: String, episodes: [(Int, Int, Int64, String?)]) -> TitleDetail {
        // (season, episode, positionMs, status)
        let title = Title(id: 1, kind: kind, title: "Т", year: 2020)
        var t = title
        t.species = nil
        let eps = episodes.map { tuple in
            var e = Episode(id: Int64(100 + tuple.1), seasonNo: tuple.0, episodeNo: tuple.1)
            e.positionMs = tuple.2
            e.status = tuple.3
            e.durationSec = 1200
            return e
        }
        return TitleDetail(title: t, episodes: eps, prefs: nil)
    }

    func testNextUpStarted() {
        let d = detail(kind: "series", episodes: [(1, 1, 0, "completed"), (1, 2, 300_000, nil), (1, 3, 0, nil)])
        let up = nextUp(d)
        XCTAssertEqual(up?.episode.episodeNo, 2)
        XCTAssertEqual(up?.label, "Продолжить 1×2")
    }

    func testNextUpNextAfterDone() {
        let d = detail(kind: "series", episodes: [(1, 1, 0, "completed"), (1, 2, 0, nil)])
        let up = nextUp(d)
        XCTAssertEqual(up?.episode.episodeNo, 2)
        XCTAssertEqual(up?.sub, "следующая серия")
    }

    func testNextUpRewatch() {
        let d = detail(kind: "series", episodes: [(1, 1, 0, "completed")])
        XCTAssertEqual(nextUp(d)?.label, "Пересмотреть")
    }

    func testNextUpMovie() {
        let d = detail(kind: "movie", episodes: [(1, 1, 0, nil)])
        XCTAssertEqual(nextUp(d)?.label, "Смотреть")
        XCTAssertTrue(isMovie(d))
    }

    func testWatchedSeasons() {
        let d = detail(kind: "series", episodes: [(1, 1, 0, "completed"), (1, 2, 0, "completed"), (2, 1, 0, nil)])
        XCTAssertEqual(watchedSeasons(d), [1])
    }

    func testWithSeasonWatchedResets() {
        let d = detail(kind: "series", episodes: [(1, 1, 500_000, nil)])
        let updated = withSeasonWatched(d, 1, watched: false)
        XCTAssertEqual(updated.episodes[0].positionMs, 0)
        XCTAssertNil(updated.episodes[0].status)
    }
}

/// Хелперы текста.
final class FmtTests: XCTestCase {

    func testPlural() {
        XCTAssertEqual(plural(1, "серия", "серии", "серий"), "серия")
        XCTAssertEqual(plural(2, "серия", "серии", "серий"), "серии")
        XCTAssertEqual(plural(5, "серия", "серии", "серий"), "серий")
        XCTAssertEqual(plural(11, "серия", "серии", "серий"), "серий")
        XCTAssertEqual(plural(21, "серия", "серии", "серий"), "серия")
    }

    func testFmtMins() {
        XCTAssertEqual(fmtMins(0), "меньше минуты")
        XCTAssertEqual(fmtMins(5), "5 мин")
        XCTAssertEqual(fmtMins(65), "1 ч 5 мин")
    }

    func testSplitTitle() {
        XCTAssertEqual(splitTitle("Тьма / Dark").0, "Тьма")
        XCTAssertEqual(splitTitle("Тьма / Dark").1, "Dark")
        XCTAssertEqual(splitTitle("Просто").1, "")
    }

    func testFmtBytes() {
        XCTAssertEqual(fmtBytes(500), "500 Б")
        XCTAssertEqual(fmtBytes(2048), "2 КБ")
        XCTAssertEqual(fmtBytes(5 * 1024 * 1024), "5 МБ")
        XCTAssertEqual(fmtBytes(Int64(1.5 * 1024 * 1024 * 1024)), "1.5 ГБ")
    }

    func testSpeciesWord() {
        XCTAssertEqual(speciesWord("anime", "series"), "Аниме")
        XCTAssertEqual(speciesWord(nil, "movie"), "Фильм")
        XCTAssertEqual(speciesWord("dorama", "series"), "Дорама")
    }
}

/// Разметка пропусков.
final class SkipsTests: XCTestCase {

    func testPickFiltersShort() {
        let segs = [
            SkipSegment(kind: "intro", start: 0, end: 2),      // короче 3 с — долой
            SkipSegment(kind: "intro", start: 10, end: 90),
        ]
        let picked = Skips.pick(segs, duration: 1400)
        XCTAssertEqual(picked.count, 1)
        XCTAssertEqual(picked.first?.start ?? -1, 10)
    }

    func testForeignMarkupOnlyWhenDurationMatches() {
        // Чужая разметка с другой длиной файла — не брать (Android: допуск 2.5 с).
        let foreign = [SkipSegment(kind: "intro", start: 10, end: 90, forDuration: 1000)]
        XCTAssertTrue(Skips.pick(foreign, duration: 2000).isEmpty)
        // Совпавшая в допуске — берётся как есть, без пересчёта.
        let close = [SkipSegment(kind: "intro", start: 10, end: 90, forDuration: 2001)]
        let picked = Skips.pick(close, duration: 2000)
        XCTAssertEqual(picked.count, 1)
        XCTAssertEqual(picked.first?.start ?? -1, 10, accuracy: 0.01)
        // Своя разметка важнее чужой того же вида.
        let mixed = [
            SkipSegment(kind: "intro", start: 5, end: 40),
            SkipSegment(kind: "intro", start: 10, end: 90, forDuration: 2001),
        ]
        XCTAssertEqual(Skips.pick(mixed, duration: 2000).first?.start ?? -1, 5, accuracy: 0.01)
    }

    func testActiveWindow() {
        let segs = [SkipSegment(kind: "intro", start: 10, end: 90)]
        XCTAssertNil(Skips.active(segs, at: 5))
        XCTAssertNotNil(Skips.active(segs, at: 10))
        XCTAssertNil(Skips.active(segs, at: 90))
    }

    func testAutoSeekable() {
        XCTAssertTrue(Skips.autoSeekable(SkipSegment(kind: "intro", start: 0, end: 10)))
        XCTAssertTrue(Skips.autoSeekable(SkipSegment(kind: "recap", start: 0, end: 10)))
        XCTAssertFalse(Skips.autoSeekable(SkipSegment(kind: "outro", start: 0, end: 10)))
    }
}

/// Пагинация: свой курсор страниц и тихое обновление «сколько показано».
@MainActor
final class PagingTests: XCTestCase {

    private func titles(_ ids: Range<Int>) -> [Title] {
        ids.map { i in
            try! JSONDecoder().decode(Title.self, from: Data(#"{"id":\#(i),"kind":"movie","title":"t\#(i)"}"#.utf8))
        }
    }

    /// /browse отвечает {items,total} без номера страницы — вторая страница
    /// должна быть 2, а не снова 1.
    func testPageBasedCursorWithoutPageField() async {
        var asked: [Int] = []
        let p = Paged<Title>(pageBased: true) { n in
            asked.append(n)
            return Page(items: self.titles((n - 1) * 3 ..< n * 3), total: 9)
        }
        await p.initial()
        await p.more()
        await p.more()
        XCTAssertEqual(asked, [1, 2, 3])
        XCTAssertEqual(p.items.count, 9)
        XCTAssertFalse(p.hasMore)
        XCTAssertEqual(p.total, 9)
    }

    /// Обновление перечитывает показанное, а не 10 страниц подряд.
    func testRefreshReadsOnlyShown() async {
        var asked: [Int] = []
        let p = Paged<Title>(pageBased: false) { off in
            asked.append(off)
            return Page(items: self.titles(off ..< off + 5), total: 100, nextOffset: off + 5, hasMore: true)
        }
        await p.initial()
        asked = []
        await p.refresh()
        XCTAssertEqual(asked, [0])
        XCTAssertEqual(p.items.count, 5)
    }
}

final class SessionAndProfileTests: XCTestCase {

    /// Сервер отвечает на истёкший токен 410 {"error":"expired"} — это повод
    /// для refresh, как 401.
    func testSessionDead() {
        XCTAssertTrue(ApiError(status: 401, code: "unauthorized", message: "").sessionDead)
        XCTAssertTrue(ApiError(status: 410, code: "expired", message: "").sessionDead)
        XCTAssertFalse(ApiError(status: 410, code: "gone", message: "").sessionDead)
        XCTAssertFalse(ApiError(status: 404, code: "not_found", message: "").sessionDead)
        XCTAssertFalse(ApiError.offline.sessionDead)
    }

    /// H.264 заявляется всегда; «Нет» убирает кодек, «Заявлять» добавляет.
    func testClaimedCodecs() {
        XCTAssertEqual(Quality.claimedCodecs(hevc: "off", av1: "off"), ["avc1"])
        XCTAssertEqual(Quality.claimedCodecs(hevc: "declare", av1: "declare"), ["avc1", "hvc1", "av01"])
        XCTAssertEqual(Quality.claimedCodecs(hevc: "off", av1: "declare").first, "avc1")
        XCTAssertEqual(Quality.maxQuality(0), 1080)
        XCTAssertEqual(Quality.maxQuality(2160), 2160)
        XCTAssertEqual(Quality.maxQuality(777), 1080)
    }

    /// Загрузки хранятся относительно домашней папки; абсолютный путь из
    /// прежнего контейнера перевешивается на текущий.
    func testStoredPathsSurviveContainerMove() {
        let home = NSHomeDirectory()
        let file = URL(fileURLWithPath: home).appendingPathComponent("Library/x/1.movpkg")
        XCTAssertEqual(LocalLibrary.storedPath(file), "Library/x/1.movpkg")
        XCTAssertEqual(LocalLibrary.url(for: "Library/x/1.movpkg").standardizedFileURL.path, file.standardizedFileURL.path)
        let legacy = "/private/var/mobile/Containers/Data/Application/OLD-UUID/Library/x/1.movpkg"
        XCTAssertEqual(LocalLibrary.url(for: legacy).standardizedFileURL.path, file.standardizedFileURL.path)
    }
}
