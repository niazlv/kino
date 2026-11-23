import Foundation

/// Формы ответов сервера — зеркало data/Models.kt. Неизвестные поля
/// Codable игнорирует сам; необязательные поля с дефолтами декодируются
/// через decodeIfPresent — сервер расширяет ответы, клиент не должен падать.

struct Me: Codable, Equatable {
    var id: Int64
    var login: String
    var displayName: String
    var role: String?

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(Int64.self, forKey: .id)
        login = try c.decode(String.self, forKey: .login)
        displayName = try c.decode(String.self, forKey: .displayName)
        role = try c.decodeIfPresent(String.self, forKey: .role)
    }
}

struct DeviceBegin: Codable {
    var deviceCode: String
    var userCode: String
    var verificationUrl: String
    var expiresAt: String
    var interval: Int64 = 5

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        deviceCode = try c.decode(String.self, forKey: .deviceCode)
        userCode = try c.decode(String.self, forKey: .userCode)
        verificationUrl = try c.decode(String.self, forKey: .verificationUrl)
        expiresAt = try c.decode(String.self, forKey: .expiresAt)
        interval = (try? c.decodeIfPresent(Int64.self, forKey: .interval)) ?? 5
    }
}

struct DeviceAuthInfo: Codable, Equatable {
    var device: String
    var requestedAt: String
    var expiresAt: String
}

struct Page<T: Codable>: Codable {
    var items: [T]
    var total: Int = 0
    var nextOffset: Int = 0
    var hasMore: Bool = false
    var activeTotal: Int = 0
    var page: Int = 0
    var pages: Int = 0

    init(items: [T], total: Int = 0, nextOffset: Int = 0, hasMore: Bool = false) {
        self.items = items; self.total = total; self.nextOffset = nextOffset; self.hasMore = hasMore
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        items = try c.decodeIfPresent([T].self, forKey: .items) ?? []
        total = (try? c.decodeIfPresent(Int.self, forKey: .total)) ?? 0
        nextOffset = (try? c.decodeIfPresent(Int.self, forKey: .nextOffset)) ?? 0
        hasMore = (try? c.decodeIfPresent(Bool.self, forKey: .hasMore)) ?? false
        activeTotal = (try? c.decodeIfPresent(Int.self, forKey: .activeTotal)) ?? 0
        page = (try? c.decodeIfPresent(Int.self, forKey: .page)) ?? 0
        pages = (try? c.decodeIfPresent(Int.self, forKey: .pages)) ?? 0
    }
}

struct ContinueItem: Codable, Identifiable {
    var episodeId: Int64
    var titleId: Int64
    var title: String
    var kind: String = ""
    var year: Int = 0
    var hasArt: Bool = false
    var seasonNo: Int = 0
    var episodeNo: Int = 0
    var positionMs: Int64 = 0
    var durationMs: Int64 = 0

    var id: Int64 { episodeId }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        episodeId = try c.decode(Int64.self, forKey: .episodeId)
        titleId = try c.decode(Int64.self, forKey: .titleId)
        title = try c.decode(String.self, forKey: .title)
        kind = (try? c.decodeIfPresent(String.self, forKey: .kind)) ?? ""
        year = (try? c.decodeIfPresent(Int.self, forKey: .year)) ?? 0
        hasArt = (try? c.decodeIfPresent(Bool.self, forKey: .hasArt)) ?? false
        seasonNo = (try? c.decodeIfPresent(Int.self, forKey: .seasonNo)) ?? 0
        episodeNo = (try? c.decodeIfPresent(Int.self, forKey: .episodeNo)) ?? 0
        positionMs = (try? c.decodeIfPresent(Int64.self, forKey: .positionMs)) ?? 0
        durationMs = (try? c.decodeIfPresent(Int64.self, forKey: .durationMs)) ?? 0
    }
}

/// Площадка, с которой приходит видео.
struct SourceRef: Codable, Equatable, Hashable {
    var id: String
    var name: String
}

struct Title: Codable, Identifiable, Equatable {
    var id: Int64
    var kind: String = ""
    var title: String = ""
    var year: Int = 0
    /// Вид произведения: anime, cartoon, dorama, series, movie. Точнее kind.
    var species: String?
    var imdb: String?
    var plot: String?
    var genres: [String] = []
    var finished: Bool = false
    var hasArt: Bool = false
    /// Площадки тайтла по приоритету; только на странице тайтла.
    var sources: [SourceRef] = []

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(Int64.self, forKey: .id)
        kind = (try? c.decodeIfPresent(String.self, forKey: .kind)) ?? ""
        title = (try? c.decodeIfPresent(String.self, forKey: .title)) ?? ""
        year = (try? c.decodeIfPresent(Int.self, forKey: .year)) ?? 0
        species = try c.decodeIfPresent(String.self, forKey: .species)
        imdb = try c.decodeIfPresent(String.self, forKey: .imdb)
        plot = try c.decodeIfPresent(String.self, forKey: .plot)
        genres = (try? c.decodeIfPresent([String].self, forKey: .genres)) ?? []
        finished = (try? c.decodeIfPresent(Bool.self, forKey: .finished)) ?? false
        hasArt = (try? c.decodeIfPresent(Bool.self, forKey: .hasArt)) ?? false
        sources = (try? c.decodeIfPresent([SourceRef].self, forKey: .sources)) ?? []
    }

    init(
        id: Int64, kind: String = "", title: String = "", year: Int = 0,
        species: String? = nil, imdb: String? = nil, plot: String? = nil,
        genres: [String] = [], finished: Bool = false, hasArt: Bool = false,
        sources: [SourceRef] = []
    ) {
        self.id = id; self.kind = kind; self.title = title; self.year = year
        self.species = species; self.imdb = imdb; self.plot = plot
        self.genres = genres; self.finished = finished; self.hasArt = hasArt
        self.sources = sources
    }

    static func == (a: Title, b: Title) -> Bool { a.id == b.id && a.title == b.title && a.year == b.year }
}

struct Episode: Codable, Identifiable, Equatable {
    var id: Int64
    var seasonNo: Int = 0
    var episodeNo: Int = 0
    var title: String?
    var durationSec: Int64 = 0
    var positionMs: Int64 = 0
    var status: String?
    var hasArt: Bool = false

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(Int64.self, forKey: .id)
        seasonNo = (try? c.decodeIfPresent(Int.self, forKey: .seasonNo)) ?? 0
        episodeNo = (try? c.decodeIfPresent(Int.self, forKey: .episodeNo)) ?? 0
        title = try c.decodeIfPresent(String.self, forKey: .title)
        durationSec = (try? c.decodeIfPresent(Int64.self, forKey: .durationSec)) ?? 0
        positionMs = (try? c.decodeIfPresent(Int64.self, forKey: .positionMs)) ?? 0
        status = try c.decodeIfPresent(String.self, forKey: .status)
        hasArt = (try? c.decodeIfPresent(Bool.self, forKey: .hasArt)) ?? false
    }

    init(
        id: Int64, seasonNo: Int = 0, episodeNo: Int = 0, title: String? = nil,
        durationSec: Int64 = 0, positionMs: Int64 = 0, status: String? = nil, hasArt: Bool = false
    ) {
        self.id = id; self.seasonNo = seasonNo; self.episodeNo = episodeNo
        self.title = title; self.durationSec = durationSec; self.positionMs = positionMs
        self.status = status; self.hasArt = hasArt
    }
}

/// Выбор в плеере для тайтла. null — не выбирал; субтитры "" — выключены;
/// площадка "" — «автоматически».
struct Prefs: Codable, Equatable {
    var audio: String?
    var subtitle: String?
    var source: String?
}

struct NewEpisodeItem: Codable, Identifiable {
    var titleId: Int64
    var title: String
    var kind: String = ""
    var year: Int = 0
    var hasArt: Bool = false
    var unwatched: Int = 0
    var total: Int = 0

    var id: Int64 { titleId }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        titleId = try c.decode(Int64.self, forKey: .titleId)
        title = try c.decode(String.self, forKey: .title)
        kind = (try? c.decodeIfPresent(String.self, forKey: .kind)) ?? ""
        year = (try? c.decodeIfPresent(Int.self, forKey: .year)) ?? 0
        hasArt = (try? c.decodeIfPresent(Bool.self, forKey: .hasArt)) ?? false
        unwatched = (try? c.decodeIfPresent(Int.self, forKey: .unwatched)) ?? 0
        total = (try? c.decodeIfPresent(Int.self, forKey: .total)) ?? 0
    }
}

struct HistoryItem: Codable, Identifiable {
    var episodeId: Int64
    var titleId: Int64
    var title: String
    var kind: String = ""
    var seasonNo: Int = 0
    var episodeNo: Int = 0
    var positionMs: Int64 = 0
    var durationMs: Int64 = 0
    var status: String = ""
    var hasArt: Bool = false
    var hasShot: Bool = false
    var updatedAt: String = ""

    var id: Int64 { episodeId }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        episodeId = try c.decode(Int64.self, forKey: .episodeId)
        titleId = try c.decode(Int64.self, forKey: .titleId)
        title = try c.decode(String.self, forKey: .title)
        kind = (try? c.decodeIfPresent(String.self, forKey: .kind)) ?? ""
        seasonNo = (try? c.decodeIfPresent(Int.self, forKey: .seasonNo)) ?? 0
        episodeNo = (try? c.decodeIfPresent(Int.self, forKey: .episodeNo)) ?? 0
        positionMs = (try? c.decodeIfPresent(Int64.self, forKey: .positionMs)) ?? 0
        durationMs = (try? c.decodeIfPresent(Int64.self, forKey: .durationMs)) ?? 0
        status = (try? c.decodeIfPresent(String.self, forKey: .status)) ?? ""
        hasArt = (try? c.decodeIfPresent(Bool.self, forKey: .hasArt)) ?? false
        hasShot = (try? c.decodeIfPresent(Bool.self, forKey: .hasShot)) ?? false
        updatedAt = (try? c.decodeIfPresent(String.self, forKey: .updatedAt)) ?? ""
    }
}

struct Track: Codable, Identifiable, Equatable {
    var id: String
    var language: String = ""
    var title: String = ""
    /// Только у субтитров отдельным файлом: адрес на шлюзе.
    var url: String?
    /// Включить сразу: вариант «с субтитрами» (JSON-ключ "default").
    var isDefault: Bool = false

    enum CodingKeys: String, CodingKey {
        case id, language, title, url
        case isDefault = "default"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        language = (try? c.decodeIfPresent(String.self, forKey: .language)) ?? ""
        title = (try? c.decodeIfPresent(String.self, forKey: .title)) ?? ""
        url = try c.decodeIfPresent(String.self, forKey: .url)
        isDefault = (try? c.decodeIfPresent(Bool.self, forKey: .isDefault)) ?? false
    }
}

struct Rendition: Codable, Equatable {
    var height: Int
    var codec: String?

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        height = try c.decode(Int.self, forKey: .height)
        codec = try c.decodeIfPresent(String.self, forKey: .codec)
    }
}

/// Поток той же серии у той же площадки: у аниме-площадок так устроены озвучки.
struct Variant: Codable, Equatable, Identifiable {
    var id: String
    var title: String
    var player: String?
}

struct PassedSource: Codable, Equatable, Identifiable {
    var id: String
    var name: String
    var reason: String = ""

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        reason = (try? c.decodeIfPresent(String.self, forKey: .reason)) ?? ""
    }
}

struct Playback: Codable {
    var ticketId: String
    var url: String
    /// JSON-ключ "protocol" (в Swift это ключевое слово).
    var streamProtocol: String = "HLS"
    var expiresAt: String = ""
    var audios: [Track] = []
    var subtitles: [Track] = []
    var renditions: [Rendition] = []
    /// Площадка, выдавшая поток, и все, у кого эта серия есть.
    var source: SourceRef?
    var sources: [SourceRef] = []
    /// Площадки, стоявшие в очереди раньше выдавшей, и почему поток не с них.
    var passed: [PassedSource] = []
    var variants: [Variant] = []
    var variant: String?
    /// Разметка пропуска, пришедшая вместе с потоком. Остальная — skips().
    var segments: [SkipSegment] = []

    enum CodingKeys: String, CodingKey {
        case ticketId, url
        case streamProtocol = "protocol"
        case expiresAt, audios, subtitles, renditions, source, sources, passed
        case variants, variant, segments
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        ticketId = try c.decode(String.self, forKey: .ticketId)
        url = try c.decode(String.self, forKey: .url)
        streamProtocol = (try? c.decodeIfPresent(String.self, forKey: .streamProtocol)) ?? "HLS"
        expiresAt = (try? c.decodeIfPresent(String.self, forKey: .expiresAt)) ?? ""
        audios = (try? c.decodeIfPresent([Track].self, forKey: .audios)) ?? []
        subtitles = (try? c.decodeIfPresent([Track].self, forKey: .subtitles)) ?? []
        renditions = (try? c.decodeIfPresent([Rendition].self, forKey: .renditions)) ?? []
        source = try c.decodeIfPresent(SourceRef.self, forKey: .source)
        sources = (try? c.decodeIfPresent([SourceRef].self, forKey: .sources)) ?? []
        passed = (try? c.decodeIfPresent([PassedSource].self, forKey: .passed)) ?? []
        variants = (try? c.decodeIfPresent([Variant].self, forKey: .variants)) ?? []
        variant = try c.decodeIfPresent(String.self, forKey: .variant)
        segments = (try? c.decodeIfPresent([SkipSegment].self, forKey: .segments)) ?? []
    }
}

/// Что в серии можно пропустить. Время — секунды от начала файла.
struct SkipSegment: Codable, Equatable {
    var kind: String = "skip"
    var start: Double
    var end: Double
    /// Длина размеченного файла, если разметка чужая (AniSkip).
    var forDuration: Double?
    var origin: String?

    init(kind: String = "skip", start: Double, end: Double, forDuration: Double? = nil, origin: String? = nil) {
        self.kind = kind; self.start = start; self.end = end
        self.forDuration = forDuration; self.origin = origin
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = (try? c.decodeIfPresent(String.self, forKey: .kind)) ?? "skip"
        start = try c.decode(Double.self, forKey: .start)
        end = try c.decode(Double.self, forKey: .end)
        forDuration = try c.decodeIfPresent(Double.self, forKey: .forDuration)
        origin = try c.decodeIfPresent(String.self, forKey: .origin)
    }
}

struct VoiceOption: Codable, Identifiable {
    var key: String
    var title: String
    var ways: [VoiceWay] = []

    var id: String { key }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        key = try c.decode(String.self, forKey: .key)
        title = try c.decode(String.self, forKey: .title)
        ways = (try? c.decodeIfPresent([VoiceWay].self, forKey: .ways)) ?? []
    }
}

struct VoiceWay: Codable, Equatable {
    var source: String
    var variant: String?
    var player: String?
}

/// silent — площадки, не рассказавшие о своих озвучках: список неполон.
struct VoiceList: Codable {
    var voices: [VoiceOption] = []
    var silent: [PassedSource] = []

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        voices = (try? c.decodeIfPresent([VoiceOption].self, forKey: .voices)) ?? []
        silent = (try? c.decodeIfPresent([PassedSource].self, forKey: .silent)) ?? []
    }
}

/// Ключи озвучек по подписям дорожек — их считает сервер (POST /voices/keys).
struct VoiceKeys: Codable {
    var keys: [String: VoiceKeyInfo] = [:]

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        keys = (try? c.decodeIfPresent([String: VoiceKeyInfo].self, forKey: .keys)) ?? [:]
    }
}

struct VoiceKeyInfo: Codable {
    var key: String = ""
    var title: String = ""

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        key = (try? c.decodeIfPresent(String.self, forKey: .key)) ?? ""
        title = (try? c.decodeIfPresent(String.self, forKey: .title)) ?? ""
    }
}

/// Чего хочет человек от выпуска тикета — тело /playback.
struct PlaybackChoice: Codable {
    /// Площадка: запомненная или выбранная только что.
    var source: String?
    /// Выбрана только что руками — не подменять другой площадкой.
    var strict: Bool = false
    var variant: String?
    /// Запомненная студия озвучки — подходит к любой площадке.
    var variantLabel: String?
    /// Студия из общего списка (VoiceOption.key).
    var voice: String?
    /// Площадки, чей поток на этом устройстве только что тормозил.
    var avoid: [String] = []
}

struct Marks: Codable, Equatable {
    var following: Bool = false
    var watchlist: Bool = false
    var hidden: Bool = false

    init(following: Bool = false, watchlist: Bool = false, hidden: Bool = false) {
        self.following = following; self.watchlist = watchlist; self.hidden = hidden
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        following = (try? c.decodeIfPresent(Bool.self, forKey: .following)) ?? false
        watchlist = (try? c.decodeIfPresent(Bool.self, forKey: .watchlist)) ?? false
        hidden = (try? c.decodeIfPresent(Bool.self, forKey: .hidden)) ?? false
    }
}

struct TitleDetail: Codable {
    var title: Title
    var episodes: [Episode] = []
    var prefs: Prefs?

    init(title: Title, episodes: [Episode] = [], prefs: Prefs? = nil) {
        self.title = title; self.episodes = episodes; self.prefs = prefs
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        title = try c.decode(Title.self, forKey: .title)
        episodes = (try? c.decodeIfPresent([Episode].self, forKey: .episodes)) ?? []
        prefs = try c.decodeIfPresent(Prefs.self, forKey: .prefs)
    }
}

struct ProgressState: Codable {
    var positionMs: Int64 = 0
    var durationMs: Int64 = 0
    var status: String = ""

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        positionMs = (try? c.decodeIfPresent(Int64.self, forKey: .positionMs)) ?? 0
        durationMs = (try? c.decodeIfPresent(Int64.self, forKey: .durationMs)) ?? 0
        status = (try? c.decodeIfPresent(String.self, forKey: .status)) ?? ""
    }
}

struct ProgressReport: Codable {
    var positionMs: Int64
    var durationMs: Int64
    var clientTs: String
}

struct SearchHistoryRsp: Codable {
    var items: [String] = []

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        items = (try? c.decodeIfPresent([String].self, forKey: .items)) ?? []
    }
}

/// Одно переключение за раз: поле, которого нет (null), не меняется.
struct PrefsPatch: Codable {
    var audio: String?
    var subtitle: String?
    var source: String?
}

/// Тело /auth/login и /auth/refresh и т.п.
struct LoginBody: Codable { var login: String; var password: String; var device: String }
struct RegisterBody: Codable { var invite: String; var login: String; var password: String; var displayName: String }
struct RefreshBody: Codable { var refreshToken: String }
struct TokenPair: Codable {
    var token: String
    var refreshToken: String
    var expiresAt: String = ""
    var refreshExpiresAt: String = ""

    init(token: String, refreshToken: String, expiresAt: String = "", refreshExpiresAt: String = "") {
        self.token = token
        self.refreshToken = refreshToken
        self.expiresAt = expiresAt
        self.refreshExpiresAt = refreshExpiresAt
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        token = try c.decode(String.self, forKey: .token)
        refreshToken = try c.decode(String.self, forKey: .refreshToken)
        expiresAt = (try? c.decodeIfPresent(String.self, forKey: .expiresAt)) ?? ""
        refreshExpiresAt = (try? c.decodeIfPresent(String.self, forKey: .refreshExpiresAt)) ?? ""
    }
}
struct ExpiresRsp: Codable { var expiresAt: String = "" }
struct DeviceBeginBody: Codable { var device: String }
struct DevicePollBody: Codable { var deviceCode: String; var cookie: Bool = false }
struct WatchedBody: Codable { var watched: Bool }
struct WatchedRsp: Codable {
    var watched: Bool = false
    var episodes: Int = 0

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        watched = (try? c.decodeIfPresent(Bool.self, forKey: .watched)) ?? false
        episodes = (try? c.decodeIfPresent(Int.self, forKey: .episodes)) ?? 0
    }
}
struct PasswordBody: Codable { var oldPassword: String; var newPassword: String }
struct SearchHistoryBody: Codable { var query: String }
struct VoicesKeysBody: Codable { var labels: [String] }
