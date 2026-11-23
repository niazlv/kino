import os

/// Хлебные крошки для проверки прогонов через системный лог.
/// Читать: xcrun simctl spawn booted log show --last 5m --predicate 'subsystem == "ru.sorewa.kino"'
enum LogUI {
    static let logger = Logger(subsystem: "ru.sorewa.kino", category: "ui")

    static func event(_ s: String) {
        logger.notice("\(s, privacy: .public)")
    }
}
