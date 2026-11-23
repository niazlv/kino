import AVFoundation

/// Какие дорожки потока класть в загрузку и показывать в плеере скачанного.
///
/// kinopub объявляет каждую озвучку и субтитры по разу на семейство кодеков
/// (h264 и h265 делят один GROUP-ID): в группе оказываются неразличимые для
/// человека копии «01. Одноголосый (RUS)». Качать обе — лишние десятки
/// мегабайт на серию, показывать обе — путаница. Повтор — то же имя и тот же
/// язык; дорожки с разными именами («ENG #05» и «ENG #06») остаются обе.
enum TrackPlan {
    struct Track: Equatable {
        var name: String
        var language: String?
    }

    /// Индексы дорожек без повторов (побеждает первое объявление). Первой
    /// встаёт дорожка под первую подошедшую подпись из [preferred]: её
    /// система качает основной, и с неё начинает плеер.
    static func unique(_ tracks: [Track], preferred: [String?] = []) -> [Int] {
        var seen = Set<String>()
        var order: [Int] = []
        for (i, t) in tracks.enumerated() {
            let key = t.name.lowercased() + "\u{1}" + (t.language ?? "").lowercased()
            if seen.insert(key).inserted { order.append(i) }
        }
        for wanted in preferred.compactMap({ $0 }) where !wanted.isEmpty {
            guard let hit = order.firstIndex(where: { matches(tracks[$0].name, wanted) }) else { continue }
            order.insert(order.remove(at: hit), at: 0)
            break
        }
        return order
    }

    /// Языки субтитров, которые кладём в загрузку: русский, украинский,
    /// английский и языки скачиваемых озвучек (японские — к японской).
    /// Все подряд нельзя: у kinopub их бывает под сорок, и каждая — свой
    /// плейлист и кусок; на медленном канале эти запросы встают в очередь
    /// к шести соединениям, iOS через 10 с без ответа рвёт задание и
    /// начинает его заново — загрузка не двигается вовсе.
    static func subtitleLanguages(audio: [String?]) -> Set<String> {
        Set(["ru", "uk", "en"]).union(audio.compactMap { baseLanguage($0) })
    }

    /// Субтитры без языка — берём: не понять, лишние ли они.
    static func keepSubtitle(language: String?, wanted: Set<String>) -> Bool {
        guard let base = baseLanguage(language) else { return true }
        return wanted.contains(base)
    }

    /// «rus», «ru-RU», «RU» → «ru»; трёхбуквенные коды — к двухбуквенным.
    static func baseLanguage(_ tag: String?) -> String? {
        guard let tag, !tag.isEmpty else { return nil }
        let code = String(tag.split(separator: "-").first ?? Substring(tag)).lowercased()
        return Locale.LanguageCode(code).identifier(.alpha2) ?? code
    }

    /// Подходит ли дорожка под подпись студии: «LostFilm» ↔
    /// «01. Многоголосый. LostFilm (RUS)» — в обе стороны, без регистра.
    static func matches(_ name: String, _ wanted: String) -> Bool {
        guard !name.isEmpty, !wanted.isEmpty else { return false }
        return name.localizedCaseInsensitiveContains(wanted) || wanted.localizedCaseInsensitiveContains(name)
    }

    /// Варианты группы с именами из плейлиста, без повторов, [preferred] —
    /// первым. [available] — только эти (у скачанного — то, что лежит в файле).
    @MainActor
    static func options(in group: AVMediaSelectionGroup, among available: [AVMediaSelectionOption]? = nil,
                        preferred: [String?] = []) async -> [(option: AVMediaSelectionOption, name: String)] {
        let pool = available ?? group.options
        var named: [(AVMediaSelectionOption, String)] = []
        for o in pool { named.append((o, await o.trackName())) }
        let tracks = named.map { Track(name: $0.1, language: $0.0.extendedLanguageTag) }
        return unique(tracks, preferred: preferred).map { (named[$0].0, named[$0].1) }
    }

    /// Догрузка после основного задания: «a:<имя>» — озвучка, «s» — все
    /// субтитры нужных языков разом (они крошечные, по куску на дорожку).
    static let subtitlesExtra = "s"

    /// Что качать: основное задание — видео с выбранной озвучкой (без неё —
    /// с дорожкой по умолчанию), остальное — догрузкой в тот же пакет.
    ///
    /// Всё одним заданием нельзя: сервер отдаёт по HTTP/1.1 через канал
    /// ~1 МБ/с, а iOS шлёт десятки запросов разом на шесть соединений; кто
    /// за 10 с не получил ответа — рвёт всё задание, и уже идущие куски
    /// видео качаются заново: с десятками дорожек сразу задание почти не
    /// движется, и даже одна лишняя озвучка даёт обрывы.
    struct Plan {
        var primary: AVMediaSelection?
        var extras: [String]
    }

    @MainActor
    static func plan(asset: AVURLAsset, voiceTitle: String?) async throws -> Plan {
        let base = try await asset.load(.preferredMediaSelection)
        var plan = Plan(primary: nil, extras: [])
        var audioLanguages: [String?] = []
        if let audible = try await asset.loadMediaSelectionGroup(for: .audible) {
            let fallback = await audible.defaultOption?.trackName()
            for (i, item) in await options(in: audible, preferred: [voiceTitle, fallback]).enumerated() {
                audioLanguages.append(item.option.extendedLanguageTag)
                if i == 0 {
                    let s = base.mutableCopy() as! AVMutableMediaSelection
                    s.select(item.option, in: audible)
                    plan.primary = s
                } else {
                    plan.extras.append("a:" + item.name)
                }
            }
        }
        if let legible = try await asset.loadMediaSelectionGroup(for: .legible) {
            let wanted = subtitleLanguages(audio: audioLanguages)
            if await options(in: legible).contains(where: { keepSubtitle(language: $0.option.extendedLanguageTag, wanted: wanted) }) {
                plan.extras.append(subtitlesExtra)
            }
        }
        return plan
    }

    /// Выборы для догрузки [extra] в скачанный пакет [asset]: то, чего в нём
    /// ещё нет. Пусто — догружать нечего (уже лежит или дорожка пропала).
    @MainActor
    static func extraSelections(asset: AVURLAsset, extra: String) async throws -> [AVMediaSelection] {
        let base = try await asset.load(.preferredMediaSelection)
        let cache = asset.assetCache
        if extra == subtitlesExtra {
            guard let legible = try await asset.loadMediaSelectionGroup(for: .legible) else { return [] }
            var audioLanguages: [String?] = []
            if let audible = try await asset.loadMediaSelectionGroup(for: .audible) {
                audioLanguages = (cache?.mediaSelectionOptions(in: audible) ?? audible.options).map(\.extendedLanguageTag)
            }
            let wanted = subtitleLanguages(audio: audioLanguages)
            let have = cache?.mediaSelectionOptions(in: legible) ?? []
            return await options(in: legible).compactMap { item in
                guard keepSubtitle(language: item.option.extendedLanguageTag, wanted: wanted),
                      !have.contains(item.option) else { return nil }
                let s = base.mutableCopy() as! AVMutableMediaSelection
                s.select(item.option, in: legible)
                return s
            }
        }
        guard extra.hasPrefix("a:"), let audible = try await asset.loadMediaSelectionGroup(for: .audible) else { return [] }
        let name = String(extra.dropFirst(2))
        guard let hit = await options(in: audible).first(where: { $0.name == name }),
              cache?.mediaSelectionOptions(in: audible).contains(hit.option) != true else { return [] }
        let s = base.mutableCopy() as! AVMutableMediaSelection
        s.select(hit.option, in: audible)
        return [s]
    }
}
