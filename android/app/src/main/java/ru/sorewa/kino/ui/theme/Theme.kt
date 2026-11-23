package ru.sorewa.kino.ui.theme

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Surface
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// Палитра: зелёный акцент витрины (#3ddc84) на глубоком тёмном фоне.
// Поверхности идут ступенями — карточка, приподнятое, обводка, — чтобы
// интерфейс читался слоями, а не одной плоской чернотой.
val Bg = Color(0xFF0A0C10)
val Surface1 = Color(0xFF12161D)
val Surface2 = Color(0xFF1A1F28)
val Surface3 = Color(0xFF242A35)
val Stroke = Color(0x1AFFFFFF)
val TextMain = Color(0xFFF1F4F8)
val TextDim = Color(0xFF9BA4B4)
val TextFaint = Color(0xFF6B7385)
val Accent = Color(0xFF3DDC84)
val AccentDeep = Color(0xFF1FA463)
val OnAccent = Color(0xFF04210F)
val Danger = Color(0xFFFF6B6B)
val Gold = Color(0xFFFFC857)

/** Совместимость со старыми экранами. */
val BgElevated = Surface1

private val DarkColors = darkColorScheme(
    primary = Accent,
    onPrimary = OnAccent,
    primaryContainer = Color(0xFF123D27),
    onPrimaryContainer = Color(0xFFB8F5D2),
    secondary = Accent,
    onSecondary = OnAccent,
    secondaryContainer = Surface2,
    onSecondaryContainer = TextMain,
    tertiary = Gold,
    background = Bg,
    onBackground = TextMain,
    surface = Bg,
    onSurface = TextMain,
    surfaceVariant = Surface2,
    onSurfaceVariant = TextDim,
    surfaceContainerLowest = Bg,
    surfaceContainerLow = Surface1,
    surfaceContainer = Surface1,
    surfaceContainerHigh = Surface2,
    surfaceContainerHighest = Surface3,
    surfaceBright = Surface3,
    outline = Color(0x33FFFFFF),
    outlineVariant = Stroke,
    error = Danger,
    onError = Color(0xFF2B0000),
    scrim = Color(0xCC000000),
)

private val base = Typography()

private val KinoType = Typography(
    displayLarge = base.displayLarge.copy(fontWeight = FontWeight.Bold, letterSpacing = (-1).sp),
    displayMedium = base.displayMedium.copy(fontWeight = FontWeight.Bold, letterSpacing = (-0.5).sp),
    displaySmall = base.displaySmall.copy(fontWeight = FontWeight.Bold, letterSpacing = (-0.5).sp),
    headlineLarge = base.headlineLarge.copy(fontWeight = FontWeight.Bold, letterSpacing = (-0.5).sp),
    headlineMedium = base.headlineMedium.copy(fontWeight = FontWeight.Bold, letterSpacing = (-0.3).sp),
    headlineSmall = base.headlineSmall.copy(fontWeight = FontWeight.Bold, fontSize = 22.sp, letterSpacing = (-0.2).sp),
    titleLarge = base.titleLarge.copy(fontWeight = FontWeight.SemiBold, fontSize = 20.sp),
    titleMedium = base.titleMedium.copy(fontWeight = FontWeight.SemiBold, fontSize = 17.sp),
    titleSmall = base.titleSmall.copy(fontWeight = FontWeight.SemiBold, fontSize = 15.sp),
    bodyLarge = base.bodyLarge.copy(fontSize = 16.sp, lineHeight = 23.sp),
    bodyMedium = base.bodyMedium.copy(fontSize = 14.sp, lineHeight = 20.sp),
    bodySmall = base.bodySmall.copy(fontSize = 12.5.sp, lineHeight = 17.sp),
    labelLarge = base.labelLarge.copy(fontWeight = FontWeight.SemiBold, fontSize = 15.sp),
    labelMedium = base.labelMedium.copy(fontWeight = FontWeight.SemiBold),
    labelSmall = base.labelSmall.copy(fontWeight = FontWeight.SemiBold, letterSpacing = 0.3.sp),
)

private val KinoShapes = Shapes(
    extraSmall = RoundedCornerShape(6.dp),
    small = RoundedCornerShape(10.dp),
    medium = RoundedCornerShape(14.dp),
    large = RoundedCornerShape(20.dp),
    extraLarge = RoundedCornerShape(28.dp),
)

/** Подпись мелким капсом над разделом. */
val Overline = TextStyle(fontSize = 12.sp, fontWeight = FontWeight.Bold, letterSpacing = 1.2.sp)

@Composable
fun KinoTheme(content: @Composable () -> Unit) {
    // Приложение всегда тёмное — видео-платформа. Surface под корнем задаёт
    // цвет текста по умолчанию: без него Text вне Scaffold рисовался чёрным.
    MaterialTheme(colorScheme = DarkColors, typography = KinoType, shapes = KinoShapes) {
        Surface(Modifier.fillMaxSize(), color = Bg, contentColor = TextMain, content = content)
    }
}
