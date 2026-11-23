import SwiftUI

/// Палитра — зеркало ui/theme/Theme.kt. Тема всегда тёмная.
enum Pal {
    static let bg = Color(hex: 0x0A0C10)
    static let surface1 = Color(hex: 0x12161D)
    static let surface2 = Color(hex: 0x1A1F28)
    static let surface3 = Color(hex: 0x242A35)
    static let stroke = Color(hex: 0xFFFFFF, alpha: 0x1A)

    static let textMain = Color(hex: 0xF1F4F8)
    static let textDim = Color(hex: 0x9BA4B4)
    static let textFaint = Color(hex: 0x6B7385)

    static let accent = Color(hex: 0x3DDC84)
    static let accentDeep = Color(hex: 0x1FA463)
    static let onAccent = Color(hex: 0x04210F)
    static let danger = Color(hex: 0xFF6B6B)
    static let gold = Color(hex: 0xFFC857)
    static let accentAlt = Color(hex: 0x2BB8C9)

    /// Радиусы фигур: xs…xl (6/10/14/20/28).
    static let rXs: CGFloat = 6
    static let rS: CGFloat = 10
    static let rM: CGFloat = 14
    static let rL: CGFloat = 20
    static let rXl: CGFloat = 28
}

extension Color {
    init(hex: UInt32, alpha: UInt32 = 0xFF) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255,
            opacity: Double(alpha) / 255
        )
    }
}

/// Типографика — размеры/веса из Theme.kt (sp → pt 1:1).
extension Font {
    static let kinoDisplay = Font.system(size: 34, weight: .bold)
    static let kinoHeadline = Font.system(size: 22, weight: .bold)
    static let kinoTitle = Font.system(size: 20, weight: .semibold)
    static let kinoTitleMedium = Font.system(size: 17, weight: .semibold)
    static let kinoTitleSmall = Font.system(size: 15, weight: .semibold)
    static let kinoBody = Font.system(size: 16)
    static let kinoBodyMedium = Font.system(size: 14)
    static let kinoBodySmall = Font.system(size: 12.5)
    static let kinoLabel = Font.system(size: 15, weight: .semibold)
    static let kinoLabelSmall = Font.system(size: 11, weight: .semibold)
    /// Секционные надзаголовки: капс + трекинг.
    static let kinoOverline = Font.system(size: 12, weight: .bold)
}

extension Text {
    /// Капс-надзаголовок «ПРОДОЛЖИТЬ», «ПЛЕЕР» и т.п.
    func overlineStyle(_ color: Color = Pal.textFaint) -> some View {
        font(.kinoOverline).tracking(1.2).textCase(.uppercase).foregroundStyle(color)
    }
}
