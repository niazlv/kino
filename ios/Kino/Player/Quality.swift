import Foundation
import VideoToolbox

/// Что устройство заявляет площадкам при запросе потока: кодеки и наибольшее
/// качество — порт player/Quality.kt.
///
/// Выше 1080p у площадок бывает в разных кодеках: у kinopub — HEVC, у
/// ЛордФильма — только AV1. Заявить кодек, которого устройство не умеет, —
/// получить чёрный экран или звук без картинки, поэтому по умолчанию кодеки
/// определяются по аппаратному декодеру, а качество — не выше 1080p.
enum Quality {
    static let qualities = [1080, 1440, 2160]

    /// HEVC умеет любой iPhone с A9; AV1 — только A17 Pro / M3 и новее.
    static let hevcSupported = VTIsHardwareDecodeSupported(kCMVideoCodecType_HEVC)
    static let av1Supported: Bool = {
        if #available(iOS 17.0, *) { return VTIsHardwareDecodeSupported(kCMVideoCodecType_AV1) }
        return false
    }()

    /// Настройка кодека: "self" — по декодеру, "declare" — заявлять, "off" — нет.
    private static func claimed(_ setting: String, _ detected: Bool) -> Bool {
        setting == "declare" || (setting == "self" && detected)
    }

    /// Кодеки для заявки: H.264 умеют все, остальное — по настройке.
    static func claimedCodecs(hevc: String, av1: String) -> [String] {
        var out = ["avc1"]
        if claimed(hevc, hevcSupported) { out.append("hvc1") }
        if claimed(av1, av1Supported) { out.append("av01") }
        return out
    }

    /// Наибольшее качество для площадок. 0 — не задано: 1080p.
    static func maxQuality(_ q: Int) -> Int {
        qualities.contains(q) ? q : 1080
    }
}
