package ru.sorewa.kino.ui.theme

import androidx.compose.animation.AnimatedContentScope
import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.EnterExitState
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.Easing
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateDp
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalDensity
import ru.sorewa.kino.ui.BackGesture
import ru.sorewa.kino.ui.BackMotion
import ru.sorewa.kino.ui.LocalBackGesture
import ru.sorewa.kino.ui.PagePose
import androidx.compose.ui.unit.dp
import androidx.navigation.NavBackStackEntry

/**
 * Движение по Material 3 (схема Expressive): пружины вместо кривых с
 * длительностью. «Пространственные» — для положения, размера и масштаба
 * (стандартные — без отскока, выразительные — с лёгким), «эффекты» — для
 * прозрачности и цвета, отскока у них не бывает.
 *
 * Значения — токены MotionScheme из Material 3 (standard / expressive):
 * затухание и жёсткость пружины.
 */
object KinoMotion {
    fun <T> spatial(): FiniteAnimationSpec<T> = spring(0.9f, 700f)
    fun <T> fastSpatial(): FiniteAnimationSpec<T> = spring(0.9f, 1400f)
    fun <T> slowSpatial(): FiniteAnimationSpec<T> = spring(0.9f, 300f)

    /** С отскоком — для того, что человек «тронул сам»: нажатие, раскрытие. */
    fun <T> bouncy(): FiniteAnimationSpec<T> = spring(0.6f, 800f)
    fun <T> expressive(): FiniteAnimationSpec<T> = spring(0.8f, 380f)

    fun <T> effects(): FiniteAnimationSpec<T> = spring(Spring.DampingRatioNoBouncy, 1600f)
    fun <T> fastEffects(): FiniteAnimationSpec<T> = spring(Spring.DampingRatioNoBouncy, 3800f)
    fun <T> slowEffects(): FiniteAnimationSpec<T> = spring(Spring.DampingRatioNoBouncy, 800f)

    /** Подчёркнутое замедление (emphasized decelerate) — вход на экран. */
    val EmphasizedDecelerate: Easing = CubicBezierEasing(0.05f, 0.7f, 0.1f, 1f)
    val EmphasizedAccelerate: Easing = CubicBezierEasing(0.3f, 0f, 0.8f, 0.15f)
    val Emphasized: Easing = CubicBezierEasing(0.2f, 0f, 0f, 1f)
}

// ─── Переходы между экранами ─────────────────────────────────────────────
//
// Как у самой системы Android между окнами (AOSP: activity_open_*/close_* и
// CrossActivityBackAnimation), а не своё изобретение — чтобы приложение
// ощущалось родным.
//
// Вглубь (тайтл, настройки, история): новый экран въезжает справа на 96dp и
// проявляется за 83 мс, прежний отъезжает влево на 96dp; 450 мс по кривой
// fast_out_extra_slow_in. Назад кнопкой — зеркально, уходящий гаснет сразу.
// Назад жестом (системным от края или своим свайпом) — предпросмотр, его
// рисует не NavHost, а BackGesture (ui/common/PredictiveBack.kt) прямо в слоях
// страниц: NavPage ниже. NavHost в это время только держит обе страницы.
// Разделы нижней панели — соседи, а не вложенность: «сквозное затухание».

/** Сдвиг страниц при переходе вглубь и назад — как у системы между окнами. */
val NavShift = 96.dp

private const val NAV_MS = 450

/** fast_out_extra_slow_in (emphasized) из AOSP: путь из двух кубических кривых. */
val EmphasizedPath: Easing = run {
    val first = CubicBezierEasing(0.3f, 0f, 0.8f, 0.15f)
    val second = CubicBezierEasing(0.05f, 0.7f, 0.1f, 1f)
    Easing { x ->
        if (x < 1f / 6f) 0.4f * first.transform(x * 6f)
        else 0.4f + 0.6f * second.transform((x - 1f / 6f) / (5f / 6f))
    }
}

private fun <T> nav(): FiniteAnimationSpec<T> = tween(NAV_MS, easing = EmphasizedPath)
private fun quickFadeIn() = fadeIn(tween(83, delayMillis = 50, easing = LinearEasing))
private fun quickFadeOut() = fadeOut(tween(83, delayMillis = 35, easing = LinearEasing))

private fun fadeOutFast() = fadeOut(tween(120, easing = KinoMotion.EmphasizedAccelerate))
private fun fadeThroughIn() =
    fadeIn(tween(300, delayMillis = 90, easing = KinoMotion.EmphasizedDecelerate)) +
        scaleIn(tween(300, delayMillis = 90, easing = KinoMotion.EmphasizedDecelerate), initialScale = 0.92f)

// Пока идёт жест «назад», NavHost держит обе страницы неподвижно (рисует их
// BackGesture), а после «назад» уходящая сразу невидима: её уже увела доводка.
private const val HOLD_MS = 32
private val Gone = Easing { f -> if (f > 0f) 1f else 0f }
private fun holdIn() = fadeIn(tween(HOLD_MS), initialAlpha = 1f)
private fun holdOut() = fadeOut(tween(HOLD_MS, easing = Gone))

typealias NavEnter = AnimatedContentTransitionScope<NavBackStackEntry>.() -> EnterTransition
typealias NavExit = AnimatedContentTransitionScope<NavBackStackEntry>.() -> ExitTransition

/**
 * Переходы NavHost. [isTop] — разделы нижней панели, [back] — жест «назад»
 * этого NavHost, [shift] — [NavShift] в пикселях.
 */
class NavMotion(
    private val isTop: (String?) -> Boolean,
    private val back: BackGesture?,
    private val shift: Int,
) {
    private val gesture get() = back?.active == true

    val enter: NavEnter = {
        if (!isTop(targetState.destination.route)) slideInHorizontally(nav()) { shift } + quickFadeIn()
        else fadeThroughIn()
    }

    val exit: NavExit = {
        if (!isTop(targetState.destination.route)) slideOutHorizontally(nav()) { -shift }
        else fadeOutFast()
    }

    val popEnter: NavEnter = {
        when {
            gesture -> holdIn()
            !isTop(initialState.destination.route) -> slideInHorizontally(nav()) { -shift }
            else -> fadeThroughIn()
        }
    }

    val popExit: NavExit = {
        when {
            gesture -> holdOut()
            !isTop(initialState.destination.route) -> slideOutHorizontally(nav()) { shift } + quickFadeOut()
            else -> fadeOutFast()
        }
    }
}

/**
 * Экран внутри NavHost: непрозрачный фон (иначе при сдвиге сквозь него виден
 * соседний) и предпросмотр жеста «назад» — уходящая страница сжимается в
 * карточку, под ней в тени лежит та, куда вернёшься (см. [BackMotion]).
 * Всё — в слое страницы: ход пальца не пересобирает экран, только двигает слой.
 */
@Composable
fun AnimatedContentScope.NavPage(content: @Composable () -> Unit) {
    val back = LocalBackGesture.current
    val closing = transition.targetState == EnterExitState.PostExit
    val entering = !closing && transition.currentState == EnterExitState.PreEnter
    val margin = with(LocalDensity.current) { 8.dp.toPx() }
    val shift = with(LocalDensity.current) { NavShift.toPx() }
    val radius = with(LocalDensity.current) { BackMotion.CORNER.toPx() }
    fun pose(w: Float): PagePose? {
        val b = back ?: return null
        if (!b.active || !(closing || entering)) return null
        return b.pose(closing, w, margin, shift)
    }
    Box(
        Modifier
            .fillMaxSize()
            .graphicsLayer {
                val p = pose(size.width) ?: return@graphicsLayer
                scaleX = p.scale
                scaleY = p.scale
                translationX = p.tx
                translationY = p.ty
                alpha = p.alpha
                if (p.corner > 0f) {
                    shape = RoundedCornerShape(radius * p.corner)
                    clip = true
                }
            }
            .background(Bg)
            .drawWithContent {
                drawContent()
                val p = pose(size.width) ?: return@drawWithContent
                if (p.scrim > 0f) drawRect(Color.Black.copy(alpha = p.scrim))
            },
    ) { content() }
}
