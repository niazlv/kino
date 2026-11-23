package ru.sorewa.kino.ui

import androidx.compose.animation.core.tween
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.gestures.animateScrollBy
import androidx.compose.foundation.lazy.grid.LazyGridItemInfo
import androidx.compose.foundation.lazy.grid.LazyGridState
import androidx.compose.foundation.onFocusedBoundsChanged
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalFocusManager
import kotlinx.coroutines.launch

/**
 * Пульт в сетке постеров: «вниз» и «вверх» держат колонку.
 *
 * Ряд за краем экрана сетка не держит — она подкладывает его только в момент
 * нажатия, и Compose отдаёт фокус первой карточке нового ряда: с третьей
 * карточки «вниз» вело на первую. Поэтому, если соседнего ряда ещё нет,
 * сетка сначала сдвигается на ряд, и только потом фокус ищется обычным
 * способом — по геометрии, то есть в ту же колонку.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun Modifier.tvGridNav(state: LazyGridState): Modifier {
    if (!LocalTv.current) return this
    val focus = LocalFocusManager.current
    val scope = rememberCoroutineScope()
    val box = remember { NavBox() }
    return this
        .onGloballyPositioned { box.grid = it }
        .onFocusedBoundsChanged { box.focused = it }
        .onPreviewKeyEvent { e ->
            val down = e.key == Key.DirectionDown
            if (!down && e.key != Key.DirectionUp) return@onPreviewKeyEvent false
            // Пока сетка доезжает, повторы стрелки не пускаем в обычный поиск —
            // он бы снова прыгнул в первую колонку.
            if (box.busy) return@onPreviewKeyEvent true
            if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
            val info = state.layoutInfo
            val cur = box.focusedItem(info.visibleItemsInfo) ?: return@onPreviewKeyEvent false
            val nextRow = if (down) cur.row + 1 else cur.row - 1
            if (nextRow < 0 || info.visibleItemsInfo.any { it.row == nextRow }) return@onPreviewKeyEvent false
            if (down && !state.canScrollForward || !down && !state.canScrollBackward) return@onPreviewKeyEvent false
            val step = (cur.size.height + info.mainAxisItemSpacing).toFloat()
            box.busy = true
            scope.launch {
                try {
                    state.animateScrollBy(if (down) step else -step, tween(160))
                    withFrameNanos { }
                    focus.moveFocus(if (down) FocusDirection.Down else FocusDirection.Up)
                } finally {
                    box.busy = false
                }
            }
            true
        }
}

private class NavBox {
    var grid: LayoutCoordinates? = null
    var focused: LayoutCoordinates? = null
    var busy = false

    /** Карточка, в которой сейчас фокус: та, что накрывает центр фокуса. */
    fun focusedItem(items: List<LazyGridItemInfo>): LazyGridItemInfo? {
        val g = grid ?: return null
        val f = focused ?: return null
        if (!g.isAttached || !f.isAttached) return null
        val c = g.localBoundingBoxOf(f, clipBounds = false).center
        return items.firstOrNull {
            Rect(
                it.offset.x.toFloat(), it.offset.y.toFloat(),
                (it.offset.x + it.size.width).toFloat(), (it.offset.y + it.size.height).toFloat(),
            ).contains(c)
        }
    }
}
