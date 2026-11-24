package ru.sorewa.kino.ui

import androidx.activity.BackEventCompat
import androidx.activity.OnBackPressedCallback
import androidx.activity.OnBackPressedDispatcher
import androidx.activity.OnBackPressedDispatcherOwner
import androidx.activity.compose.LocalOnBackPressedDispatcherOwner
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animate
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import ru.sorewa.kino.ui.theme.EmphasizedPath
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.min
import kotlin.math.sign

/** Положение страницы в жесте «назад»: масштаб, сдвиги, прозрачность, доля скругления, тень поверх. */
data class PagePose(
    val scale: Float,
    val tx: Float,
    val ty: Float,
    val alpha: Float = 1f,
    val corner: Float = 1f,
    val scrim: Float = 0f,
)

/**
 * Геометрия предпросмотра «назад» — та же, что у системы между окнами
 * (AOSP, CrossActivityBackAnimation + DefaultCrossActivityBackAnimation):
 *
 * - пока палец ведёт, уходящая страница сжимается до 90 % и прижимается к
 *   правому краю (8dp от края), прежняя лежит под ней в тени, сдвинутая на
 *   96dp влево, и сжимается вместе с ней; обе чуть идут за пальцем по
 *   вертикали;
 * - отпустил — 450 мс: уходящая гаснет за первую пятую и уплывает вправо,
 *   прежняя расправляется на весь экран, тень сходит.
 *
 * Только арифметика — без Compose, проверяется тестами.
 */
object BackMotion {
    const val MAX_SCALE = 0.9f
    const val POST_MS = 450

    /** Тень над прежней страницей. У системы в тёмной теме 0.8 — у нас светлее: куда вернёшься, должно быть видно. */
    const val SCRIM = 0.6f
    val CORNER = 32.dp

    /** BACK_GESTURE из AOSP: как ход пальца переходит в сжатие — быстро в начале. */
    private val gesture = CubicBezierEasing(0.1f, 0.1f, 0f, 1f)

    fun shrink(progress: Float): Float = gesture.transform(progress.coerceIn(0f, 1f))

    private fun scale(shrink: Float) = 1f - (1f - MAX_SCALE) * shrink

    /** Сдвиг сжатой уходящей страницы вправо: её правый край — в [margin] от края экрана. */
    private fun closingX(shrink: Float, width: Float, margin: Float) =
        shrink * (width * (1f - MAX_SCALE) / 2f - margin)

    fun closing(shrink: Float, width: Float, margin: Float, ty: Float) =
        PagePose(scale(shrink), closingX(shrink, width, margin), ty)

    fun entering(shrink: Float, shift: Float, ty: Float) =
        PagePose(scale(shrink), -shift, ty, scrim = SCRIM)

    /** Уходящая после «назад»: [t] — доля времени доводки 0..1. */
    fun closingAfter(shrink: Float, width: Float, margin: Float, shift: Float, ty: Float, t: Float): PagePose {
        val e = EmphasizedPath.transform(t)
        val s0 = scale(shrink)
        val x0 = closingX(shrink, width, margin)
        val left0 = width * (1f - s0) / 2f + x0
        return PagePose(
            scale = s0 + (1f - s0) * e,
            tx = x0 + e * (left0 + shift - x0),
            ty = ty * (1f - e),
            alpha = max(0f, 1f - 5f * t),
        )
    }

    fun enteringAfter(shrink: Float, shift: Float, ty: Float, t: Float): PagePose {
        val e = EmphasizedPath.transform(t)
        val s0 = scale(shrink)
        return PagePose(
            scale = s0 + (1f - s0) * e,
            tx = -shift * (1f - e),
            ty = ty * (1f - e),
            corner = 1f - e,
            scrim = SCRIM * (1f - t),
        )
    }

    /**
     * Сдвиг по вертикали за пальцем: до половины высоты экрана — с
     * замедлением, но не дальше 8dp от края.
     */
    fun followY(delta: Float, height: Float, shrink: Float, margin: Float): Float {
        if (height <= 0f) return 0f
        val half = height / 2f
        val r = min(half, abs(delta)) / half
        val eased = 1f - (1f - r) * (1f - r)
        val room = max(0f, (height - height * scale(shrink)) / 2f - margin)
        return room * eased * sign(delta)
    }
}

/**
 * Жест «назад» с предпросмотром для одного NavHost.
 *
 * Системный жест от края (система присылает его ход — Android 14+, и
 * HyperOS тоже) и свой свайп с любого места ([swipeBack]) ведут одно и то
 * же: ход пальца двигает слои страниц ([ru.sorewa.kino.ui.theme.NavPage]) без
 * пересборки экрана, а после отпускания доводку ведёт этот класс — по
 * времени и кривым системы, а не NavHost.
 *
 * NavHost при этом сидит на своём диспетчере «назад» ([inner]): ему
 * передаётся только «начали» (он рисует под страницей прежнюю), «назад»
 * (снимает страницу со стека, когда доводка кончилась) или «отмена».
 */
@Stable
class BackGesture internal constructor(
    private val scope: CoroutineScope,
    private val px: (androidx.compose.ui.unit.Dp) -> Float,
) {
    enum class Phase { Idle, Dragging, Settling }

    var phase by mutableStateOf(Phase.Idle)
        private set
    val active: Boolean get() = phase != Phase.Idle

    private var progress by mutableFloatStateOf(0f)
    private var after by mutableFloatStateOf(-1f) // доля доводки после «назад»; <0 — ещё не отпустили
    private var ty by mutableFloatStateOf(0f)
    private var startY = 0f
    private var shrinkAtRelease = 0f
    private var tyAtRelease = 0f
    private var job: Job? = null
    internal var height = 0f

    internal val outer = object : OnBackPressedCallback(false) {
        override fun handleOnBackStarted(backEvent: BackEventCompat) = start(backEvent.touchY)
        override fun handleOnBackProgressed(backEvent: BackEventCompat) = drag(backEvent.progress, backEvent.touchY)
        override fun handleOnBackPressed() = release(commit = true)
        override fun handleOnBackCancelled() = release(commit = false)
    }

    internal val inner = OnBackPressedDispatcher(null) { enabled -> outer.isEnabled = enabled }

    /** Положение страницы ([closing] — уходящая, иначе — та, куда вернёмся); вызывать из слоя. */
    fun pose(closing: Boolean, width: Float, margin: Float, shift: Float): PagePose {
        val t = after
        return if (t < 0f) {
            val s = BackMotion.shrink(progress)
            if (closing) BackMotion.closing(s, width, margin, ty) else BackMotion.entering(s, shift, ty)
        } else {
            if (closing) BackMotion.closingAfter(shrinkAtRelease, width, margin, shift, tyAtRelease, t)
            else BackMotion.enteringAfter(shrinkAtRelease, shift, tyAtRelease, t)
        }
    }

    fun start(touchY: Float) {
        if (phase != Phase.Idle) return
        phase = Phase.Dragging
        progress = 0f
        after = -1f
        ty = 0f
        startY = touchY
        val e = BackEventCompat(0f, touchY, 0f, BackEventCompat.EDGE_LEFT)
        inner.dispatchOnBackStarted(e)
        inner.dispatchOnBackProgressed(e)
    }

    fun drag(p: Float, touchY: Float) {
        if (phase != Phase.Dragging) return
        progress = p.coerceIn(0f, 1f)
        ty = BackMotion.followY(touchY - startY, height, BackMotion.shrink(progress), px(8.dp))
    }

    fun release(commit: Boolean) {
        when (phase) {
            // «Назад» кнопкой или без хода жеста — обычный переход NavHost.
            Phase.Idle -> if (commit) inner.onBackPressed()
            Phase.Dragging -> if (commit) settleBack() else settleCancel()
            Phase.Settling -> Unit
        }
    }

    private fun settleBack() {
        phase = Phase.Settling
        shrinkAtRelease = BackMotion.shrink(progress)
        tyAtRelease = ty
        job?.cancel()
        job = scope.launch {
            after = 0f
            animate(0f, 1f, animationSpec = tween(BackMotion.POST_MS, easing = LinearEasing)) { v, _ -> after = v }
            inner.onBackPressed()
            // NavHost снимает страницу за пару кадров (она уже невидима) —
            // до тех пор слои держат конечное положение.
            repeat(4) { withFrameNanos { } }
            reset()
        }
    }

    private fun settleCancel() {
        phase = Phase.Settling
        job?.cancel()
        job = scope.launch {
            val from = progress
            val y0 = ty
            if (from > 0f) {
                animate(from, 0f, animationSpec = spring(Spring.DampingRatioNoBouncy, Spring.StiffnessMedium)) { v, _ ->
                    progress = v
                    ty = y0 * (v / from)
                }
            }
            inner.dispatchOnBackCancelled()
            reset()
        }
    }

    private fun reset() {
        progress = 0f
        after = -1f
        ty = 0f
        phase = Phase.Idle
    }
}

val LocalBackGesture = staticCompositionLocalOf<BackGesture?> { null }

/**
 * Обёртка над NavHost: свой диспетчер «назад» для него и жест с
 * предпросмотром поверх. Системный «назад» приходит сюда, только когда у
 * NavHost есть куда возвращаться — иначе система сама уводит на рабочий стол.
 */
@Composable
fun PredictiveBackHost(modifier: Modifier = Modifier, content: @Composable (BackGesture) -> Unit) {
    val scope = rememberCoroutineScope()
    val density = androidx.compose.ui.platform.LocalDensity.current
    val back = remember { BackGesture(scope) { with(density) { it.toPx() } } }
    val activity = LocalOnBackPressedDispatcherOwner.current
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(activity, lifecycleOwner) {
        activity?.onBackPressedDispatcher?.addCallback(lifecycleOwner, back.outer)
        onDispose { back.outer.remove() }
    }
    val owner = remember(lifecycleOwner) {
        object : OnBackPressedDispatcherOwner {
            override val onBackPressedDispatcher: OnBackPressedDispatcher get() = back.inner
            override val lifecycle: Lifecycle get() = lifecycleOwner.lifecycle
        }
    }
    Box(modifier.onSizeChanged { back.height = it.height.toFloat() }) {
        CompositionLocalProvider(
            LocalOnBackPressedDispatcherOwner provides owner,
            LocalBackGesture provides back,
        ) { content(back) }
    }
}
