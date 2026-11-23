import Foundation

/// Разметка пропусков — порт player/Skips.kt (pickSkips/activeSkip).
enum Skips {

    enum Kind: String {
        case intro, outro, recap, preview
        var label: String {
            switch self {
            case .intro: return "Пропустить заставку"
            case .outro: return "Пропустить титры"
            case .recap: return "Пропустить пересказ"
            case .preview: return "Пропустить анонс"
            }
        }
        var skippedLabel: String {
            switch self {
            case .intro: return "Заставка пропущена"
            case .outro: return "Титры пропущены"
            case .recap: return "Пересказ пропущен"
            case .preview: return "Анонс пропущен"
            }
        }
    }

    static func kind(_ raw: String) -> Kind? {
        Kind(rawValue: raw.lowercased())
    }

    /// Насколько длина потока может разойтись с длиной размеченного файла:
    /// поток Анилибрии короче релиза AniSkip на 6,5 с — такую разметку не брать.
    static let durationTolerance = 2.5
    /// Короче этого пропускать нечего: кнопка мелькнула бы и исчезла.
    private static let minLength = 3.0

    /// Какие отрезки показывать в потоке длиной `duration` (секунды).
    ///
    /// Разметка этого потока важнее чужой; чужая (forDuration) берётся, только
    /// когда длина совпала. Безымянный отрезок в первой половине — заставка,
    /// во второй — титры. Результат — с нормализованным kind.
    static func pick(_ raw: [SkipSegment], duration: Double) -> [SkipSegment] {
        guard duration > 0 else { return [] }
        var exact: [SkipSegment] = []
        var foreign: [SkipSegment] = []
        for s in raw {
            guard s.end > s.start, s.start >= 0, s.start < duration else { continue }
            if let fd = s.forDuration, fd > 0, abs(fd - duration) > durationTolerance { continue }
            let k: Kind = kind(s.kind) ?? (s.start < duration / 2 ? .intro : .outro)
            // Титры у самого конца — до конца: иначе после пропуска секунда
            // чёрного кадра перед «Дальше».
            var end = min(s.end, duration)
            if k == .outro && duration - end < 5 { end = duration }
            guard end - s.start >= minLength else { continue }
            let seg = SkipSegment(kind: k.rawValue, start: s.start, end: end, forDuration: s.forDuration, origin: s.origin)
            if s.forDuration != nil { foreign.append(seg) } else { exact.append(seg) }
        }
        let kinds = Set(exact.map(\.kind))
        let all = (exact + foreign.filter { !kinds.contains($0.kind) }).sorted { $0.start < $1.start }
        // Перекрытия: из двух налезающих остаётся первый.
        var out: [SkipSegment] = []
        for s in all {
            if let last = out.last, s.start < last.end { continue }
            out.append(s)
        }
        return out
    }

    /// Активное окно сейчас: pos ≥ start−0.25 и < end−1.
    static func active(_ segments: [SkipSegment], at pos: Double) -> SkipSegment? {
        segments.first { pos >= $0.start - 0.25 && pos < $0.end - 1 }
    }

    /// Автопропуск: только заставка и пересказ.
    static func autoSeekable(_ seg: SkipSegment) -> Bool {
        let k = Kind(rawValue: seg.kind)
        return k == .intro || k == .recap
    }
}
