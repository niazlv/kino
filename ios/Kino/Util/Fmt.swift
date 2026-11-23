import Foundation

/// Текстовые хелперы — зеркало ui/common/Components.kt.

func speciesWord(_ species: String?, _ kind: String) -> String {
    switch species ?? kind {
    case "anime": return "Аниме"
    case "cartoon": return "Мультфильм"
    case "dorama": return "Дорама"
    case "series": return "Сериал"
    case "movie": return "Фильм"
    default: return kind == "movie" ? "Фильм" : (kind == "series" ? "Сериал" : "")
    }
}

func kindSubtitle(_ kind: String, _ year: Int, _ species: String?) -> String {
    let what = speciesWord(species, kind)
    var parts: [String] = []
    if !what.isEmpty { parts.append(what) }
    if year > 0 { parts.append(String(year)) }
    return parts.joined(separator: " · ")
}

func describeContinue(_ item: ContinueItem) -> String {
    let left = item.durationMs - item.positionMs
    let tail = left > 0 ? "осталось \(fmtMins(left / 60_000))" : ""
    let se = item.seasonNo > 0 ? "\(item.seasonNo) сезон, \(item.episodeNo) серия" : ""
    return [se, tail].filter { !$0.isEmpty }.joined(separator: " · ")
}

func fmtMins(_ mins: Int64) -> String {
    if mins >= 60 { return "\(mins / 60) ч \(mins % 60) мин" }
    if mins <= 0 { return "меньше минуты" }
    return "\(mins) мин"
}

/// «Рус / Original» → («Рус», «Original»).
func splitTitle(_ full: String) -> (String, String) {
    guard let i = full.range(of: " / "), i.lowerBound > full.startIndex else {
        return (full, "")
    }
    return (String(full[..<i.lowerBound]).trimmingCharacters(in: .whitespaces),
            String(full[i.upperBound...]).trimmingCharacters(in: .whitespaces))
}

func plural(_ n: Int, _ one: String, _ few: String, _ many: String) -> String {
    let m10 = n % 10, m100 = n % 100
    if m10 == 1 && m100 != 11 { return one }
    if (2...4).contains(m10) && !(12...14).contains(m100) { return few }
    return many
}

func fmtBytes(_ bytes: Int64) -> String {
    let kb = 1024.0, mb = kb * 1024, gb = mb * 1024
    let b = Double(bytes)
    if b >= gb { return String(format: "%.1f ГБ", b / gb) }
    if b >= mb { return String(format: "%.0f МБ", b / mb) }
    if b >= kb { return String(format: "%.0f КБ", b / kb) }
    return "\(bytes) Б"
}

/// Время плеера: h:mm:ss или mm:ss.
func fmtTime(_ sec: Double) -> String {
    let s = max(0, Int(sec.rounded()))
    let h = s / 3600, m = (s % 3600) / 60, ss = s % 60
    if h > 0 { return String(format: "%d:%02d:%02d", h, m, ss) }
    return String(format: "%d:%02d", m, ss)
}

/// Нормализация имени для сопоставления: lowercase, ё→е, только буквоцифры.
func normalizeName(_ s: String) -> String {
    let folded = s.lowercased()
        .applyingTransform(.fullwidthToHalfwidth, reverse: false) ?? s
    return String(folded.map { $0 == "ё" ? "е" : $0 }).filter { $0.isLetter || $0.isNumber }
}
