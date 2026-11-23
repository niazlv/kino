import Foundation

/// Все вызовы API — зеркало data/Api.kt. Пути относительные к базовому адресу.
extension Api {

    // MARK: Профиль

    func me() async throws -> Me { try await get("api/v1/me") }



    // MARK: Каталог

    func browse(kind: String?, genre: String?, sort: String, page: Int) async throws -> Page<Title> {
        var q = [URLQueryItem(name: "sort", value: sort), URLQueryItem(name: "page", value: String(page))]
        if let kind, !kind.isEmpty { q.append(URLQueryItem(name: "kind", value: kind)) }
        if let genre, !genre.isEmpty { q.append(URLQueryItem(name: "genre", value: genre)) }
        return try await get("api/v1/browse", query: q)
    }

    func genres() async throws -> Page<String> { try await get("api/v1/genres") }

    func title(_ id: Int64) async throws -> TitleDetail {
        try await get("api/v1/titles/\(id)")
    }

    func marks(_ id: Int64) async throws -> Marks { try await get("api/v1/titles/\(id)/marks") }

    func setMarks(_ id: Int64, _ marks: Marks) async throws -> Marks {
        try await putRsp("api/v1/titles/\(id)/marks", body: marks)
    }

    func setPrefs(_ id: Int64, _ patch: PrefsPatch) async throws -> Prefs {
        try await putRsp("api/v1/titles/\(id)/prefs", body: patch)
    }

    // MARK: Списки

    func list(_ kind: String, offset: Int) async throws -> Page<Title> {
        try await get("api/v1/lists/\(kind)", query: [URLQueryItem(name: "offset", value: String(offset))])
    }

    func continueItems(offset: Int) async throws -> Page<ContinueItem> {
        try await get("api/v1/continue", query: [URLQueryItem(name: "offset", value: String(offset))])
    }

    func newEpisodes(offset: Int, onlyNew: Bool) async throws -> Page<NewEpisodeItem> {
        var q = [URLQueryItem(name: "offset", value: String(offset))]
        if onlyNew { q.append(URLQueryItem(name: "only", value: "new")) }
        return try await get("api/v1/new-episodes", query: q)
    }

    func history(page: Int, kind: String? = nil, q: String? = nil) async throws -> Page<HistoryItem> {
        var query = [URLQueryItem(name: "page", value: String(page))]
        if let kind, !kind.isEmpty { query.append(URLQueryItem(name: "kind", value: kind)) }
        if let q, !q.isEmpty { query.append(URLQueryItem(name: "q", value: q)) }
        return try await get("api/v1/history", query: query)
    }

    // MARK: Поиск

    func search(_ q: String, offset: Int) async throws -> Page<Title> {
        try await get("api/v1/search", query: [
            URLQueryItem(name: "q", value: q),
            URLQueryItem(name: "offset", value: String(offset)),
        ])
    }

    func suggest(_ q: String, limit: Int = 6) async throws -> Page<Title> {
        try await get("api/v1/search/suggest", query: [
            URLQueryItem(name: "q", value: q),
            URLQueryItem(name: "limit", value: String(limit)),
        ])
    }

    func searchHistory() async throws -> [String] {
        let rsp: SearchHistoryRsp = try await get("api/v1/search/history")
        return rsp.items
    }

    func addSearchHistory(_ query: String) async throws -> [String] {
        let rsp: SearchHistoryRsp = try await postRsp("api/v1/search/history", body: SearchHistoryBody(query: query))
        return rsp.items
    }

    func removeSearchHistory(_ query: String) async throws -> [String] {
        let rsp: SearchHistoryRsp = try await deleteRsp("api/v1/search/history", query: [URLQueryItem(name: "q", value: query)])
        return rsp.items
    }

    func clearSearchHistory() async throws -> [String] {
        let rsp: SearchHistoryRsp = try await deleteRsp("api/v1/search/history")
        return rsp.items
    }

    // MARK: Плеер

    /// Выпуск тикета: профиль устройства добавляется из настроек (как Api.playback на Android).
    /// `maxHeight` — потолок из меню качества плеера, иначе из настроек.
    func playback(_ episodeId: Int64, choice: PlaybackChoice, maxHeight: Int? = nil, external: Bool = false) async throws -> Playback {
        let s = TokenStore.settings
        let body = PlaybackReq(
            codecs: Quality.claimedCodecs(hevc: s.hevc, av1: s.av1),
            maxHeight: Quality.maxQuality(maxHeight ?? s.maxQuality), supportsHls: true, external: external,
            source: choice.source, strict: choice.strict, variant: choice.variant,
            variantLabel: choice.variantLabel, voice: choice.voice, avoid: choice.avoid
        )
        // Выпуск тикета — долгий: 45 с.
        return try await post("api/v1/playback/\(episodeId)", body: body, timeout: 45)
    }

    func voices(_ episodeId: Int64) async throws -> VoiceList {
        try await get("api/v1/episodes/\(episodeId)/voices")
    }

    func voiceKeys(_ labels: [String]) async throws -> VoiceKeys {
        try await post("api/v1/voices/keys", body: VoicesKeysBody(labels: labels))
    }

    func skips(_ episodeId: Int64, source: String? = nil, variant: String? = nil) async throws -> [SkipSegment] {
        var q: [URLQueryItem] = []
        if let source, !source.isEmpty { q.append(URLQueryItem(name: "source", value: source)) }
        if let variant, !variant.isEmpty { q.append(URLQueryItem(name: "variant", value: variant)) }
        let rsp: SkipList = try await get("api/v1/episodes/\(episodeId)/skips", query: q)
        return rsp.segments
    }

    // MARK: Прогресс

    func progress(_ episodeId: Int64) async throws -> ProgressState {
        try await get("api/v1/progress/\(episodeId)")
    }

    func reportProgress(_ episodeId: Int64, _ r: ProgressReport) async throws {
        _ = try await putUnit("api/v1/progress/\(episodeId)", body: r)
    }

    func setWatched(_ episodeId: Int64, _ watched: Bool) async throws -> WatchedRsp {
        try await putRsp("api/v1/progress/\(episodeId)/watched", body: WatchedBody(watched: watched))
    }

    func setSeasonWatched(_ titleId: Int64, season: Int, watched: Bool) async throws -> WatchedRsp {
        try await putRsp("api/v1/titles/\(titleId)/seasons/\(season)/watched", body: WatchedBody(watched: watched))
    }

    // MARK: Общие PUT/POST с ответом

    func putRsp<B: Encodable, T: Decodable>(_ path: String, body: B) async throws -> T {
        let req = build("PUT", path, query: [], body: try jsonBody(body), timeout: nil, bareClient: false)
        let (data, _) = try await self.send(req, bareClient: false, retryAuth: true)
        return try JSONDecoder().decode(T.self, from: data)
    }

    func postRsp<B: Encodable, T: Decodable>(_ path: String, body: B) async throws -> T {
        let req = build("POST", path, query: [], body: try jsonBody(body), timeout: nil, bareClient: false)
        let (data, _) = try await self.send(req, bareClient: false, retryAuth: true)
        return try JSONDecoder().decode(T.self, from: data)
    }
}

/// Тело /playback — профиль устройства + выбор человека.
struct PlaybackReq: Codable {
    var codecs: [String]
    var maxHeight: Int
    var supportsHls: Bool
    var external: Bool
    var source: String?
    var strict: Bool
    var variant: String?
    var variantLabel: String?
    var voice: String?
    var avoid: [String]
}

struct SkipList: Codable {
    var segments: [SkipSegment] = []

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        segments = (try? c.decodeIfPresent([SkipSegment].self, forKey: .segments)) ?? []
    }
}
