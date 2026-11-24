package ru.sorewa.kino.ui

import android.os.Build
import android.view.HapticFeedbackConstants
import android.view.View
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import androidx.compose.ui.input.pointer.util.VelocityTracker
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.unit.dp
import kotlin.math.abs

/** С этой доли ширины отпущенный свайп срабатывает — и тут же отзывается вибрацией. */
private const val COMMIT_AT = 0.2f

/**
 * «Назад» свайпом вправо с любого места страницы. Ведёт тот же предпросмотр,
 * что и системный жест от края ([BackGesture]): страница сжимается в
 * карточку, под ней видно, куда вернёшься.
 *
 * Палец сперва достаётся детям: ленты листаются, списки прокручиваются, а
 * свайп начинается, только если жест горизонтальный, вправо и никем не забран.
 * Порог срабатывания отмечается вибрацией — как у системного жеста.
 */
@Composable
fun Modifier.swipeBack(enabled: Boolean): Modifier {
    val back = LocalBackGesture.current
    val view = LocalView.current
    if (!enabled || back == null) return this
    return pointerInput(back) {
        val slop = viewConfiguration.touchSlop
        // Отпущенный с такой скоростью вправо — срабатывает, даже если сдвинут чуть-чуть.
        val fling = 500.dp.toPx()
        awaitEachGesture {
            val down = awaitFirstDown(requireUnconsumed = false)
            if (back.active) return@awaitEachGesture
            val id = down.id
            val width = size.width.toFloat().coerceAtLeast(1f)
            var total = Offset.Zero
            // Ждём, чей это жест: тап, прокрутка ребёнка или наш свайп.
            while (true) {
                val ev = awaitPointerEvent(PointerEventPass.Main)
                if (ev.changes.size > 1) return@awaitEachGesture
                val ch = ev.changes.firstOrNull { it.id == id } ?: return@awaitEachGesture
                if (!ch.pressed || ch.isConsumed) return@awaitEachGesture
                total += ch.positionChange()
                if (abs(total.x) > slop || abs(total.y) > slop) {
                    if (total.x > 0 && total.x > abs(total.y) * 1.5f) break
                    return@awaitEachGesture
                }
            }
            val startX = total.x
            val vt = VelocityTracker()
            var y = down.position.y
            var frac = 0f
            var armed = false
            back.start(y)
            while (true) {
                val ev = awaitPointerEvent(PointerEventPass.Main)
                val ch = ev.changes.firstOrNull { it.id == id } ?: break
                // Сдвиг — до consume(): у поглощённого события positionChange() нулевой.
                total += ch.positionChange()
                ch.consume()
                if (!ch.pressed) break
                y = ch.position.y
                vt.addPosition(ch.uptimeMillis, ch.position)
                frac = ((total.x - startX) / width).coerceIn(0f, 1f)
                back.drag(frac, y)
                val over = frac >= COMMIT_AT
                if (over != armed) {
                    armed = over
                    threshold(view, over)
                }
            }
            val v = vt.calculateVelocity().x
            back.release(commit = v > fling || (armed && v > -fling / 2))
        }
    }
}

private fun threshold(view: View, on: Boolean) {
    val c = when {
        Build.VERSION.SDK_INT >= 34 ->
            if (on) HapticFeedbackConstants.GESTURE_THRESHOLD_ACTIVATE else HapticFeedbackConstants.GESTURE_THRESHOLD_DEACTIVATE
        on -> HapticFeedbackConstants.CLOCK_TICK
        else -> return
    }
    view.performHapticFeedback(c)
}
