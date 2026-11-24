package ru.sorewa.kino.ui

import android.graphics.ColorMatrix
import android.graphics.ColorMatrixColorFilter
import android.os.Build
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.asComposeRenderEffect
import androidx.compose.ui.graphics.drawOutline
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.layer.GraphicsLayer
import androidx.compose.ui.graphics.layer.drawLayer
import androidx.compose.ui.graphics.rememberGraphicsLayer
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.addOutline
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInRoot
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import kotlin.math.ceil
import kotlin.math.roundToInt

/**
 * «Стекло», как у системных панелей Android 17 и у iOS 26: то, что
 * прокручивается под панелью, просвечивает сквозь неё размытым и чуть более
 * насыщенным, поверх — полупрозрачная подкраска в цвет темы и тонкий блик
 * по краю.
 *
 * Устроено как у библиотеки Haze, только без неё (её текущие версии тянут
 * Compose новее нашего): источник ([glassSource]) рисует своё содержимое в
 * слой и показывает его, а панель ([glass]) рисует тот же слой у себя под
 * ногами через размытие. Панель должна лежать НЕ внутри источника, а поверх
 * него, соседом: внутри источника она рисовала бы сама себя.
 *
 * Размытие RenderEffect есть с Android 12; на старых панель просто плотнее
 * подкрашена — без просвета, но и без мутной каши.
 */
@Stable
class GlassSource internal constructor(internal val layer: GraphicsLayer) {
    internal var origin by mutableStateOf(Offset.Zero)
    internal var size by mutableStateOf(IntSize.Zero)

    /** Растёт при каждой перерисовке источника: панель перерисуется следом. */
    internal var version by mutableIntStateOf(0)
}

@Composable
fun rememberGlassSource(): GlassSource {
    val layer = rememberGraphicsLayer()
    return remember(layer) { GlassSource(layer) }
}

/** Сколько снизу закрыто стеклянной нижней панелью: списки добавляют это к отступу снизу. */
val LocalBottomBarInset = compositionLocalOf { 0.dp }

val glassSupported: Boolean get() = Build.VERSION.SDK_INT >= 31

fun Modifier.glassSource(src: GlassSource): Modifier = this
    .onGloballyPositioned {
        src.origin = it.positionInRoot()
        src.size = it.size
    }
    .drawWithContent {
        if (!glassSupported) {
            drawContent()
            return@drawWithContent
        }
        src.layer.record { this@drawWithContent.drawContent() }
        drawLayer(src.layer)
        // Без подписки на version: иначе источник перерисовывал бы сам себя по кругу.
        Snapshot.withoutReadObservation { src.version++ }
    }

/**
 * Подложка-стекло под содержимым модификатора.
 *
 * [tint] — подкраска поверх размытого (с альфой: чем плотнее, тем меньше
 * просвечивает), [fallback] — сплошной цвет, где размытия нет, [backdrop] —
 * фон под копией: где у источника пусто, размытие смешивается с ним, а не
 * с прозрачностью (иначе сквозь стекло проступал бы чёткий оригинал).
 */
@Composable
fun Modifier.glass(
    src: GlassSource,
    shape: Shape = RectangleShape,
    tint: Color,
    fallback: Color = tint.copy(alpha = 0.96f),
    blur: Dp = 28.dp,
    rim: Boolean = true,
    backdrop: Color = ru.sorewa.kino.ui.theme.Bg,
): Modifier {
    if (!glassSupported) {
        return this.drawWithContent {
            drawOutline(shape.createOutline(size, layoutDirection, this), fallback)
            if (rim) drawRim(shape)
            drawContent()
        }
    }
    val blurLayer = rememberGraphicsLayer()
    val pos = remember { mutableStateOf(Offset.Zero) }
    val effect = remember { EffectCache() }
    return this
        .onGloballyPositioned { pos.value = it.positionInRoot() }
        .drawWithContent {
            @Suppress("UNUSED_VARIABLE") val v = src.version
            val outline = shape.createOutline(size, layoutDirection, this)
            val r = blur.toPx()
            // Что размывать: панель с запасом на радиус размытия (у верхнего края
            // в размытие должно попадать то, что над панелью), но не дальше краёв
            // источника — за ними пусто. У края экрана размытие дотягивает
            // крайние пиксели (CLAMP), а не тянет внутрь прозрачность.
            val pad = ceil(r * 1.5f)
            val o = pos.value
            val left = maxOf(o.x - pad, src.origin.x)
            val top = maxOf(o.y - pad, src.origin.y)
            val right = minOf(o.x + size.width + pad, src.origin.x + src.size.width)
            val bottom = minOf(o.y + size.height + pad, src.origin.y + src.size.height)
            val w = (right - left).roundToInt()
            val h = (bottom - top).roundToInt()
            if (w > 0 && h > 0) {
                blurLayer.renderEffect = effect.get(r)
                blurLayer.topLeft = IntOffset((left - o.x).roundToInt(), (top - o.y).roundToInt())
                blurLayer.record(IntSize(w, h)) {
                    drawRect(backdrop)
                    translate(src.origin.x - left, src.origin.y - top) { drawLayer(src.layer) }
                }
                val clip = Path().apply { addOutline(outline) }
                clipPath(clip) { drawLayer(blurLayer) }
            }
            drawOutline(outline, tint)
            if (rim) drawRim(shape)
            drawContent()
        }
}

/** Блик по краю: сверху светлее, к низу сходит на нет — объём, как у стекла. */
private fun androidx.compose.ui.graphics.drawscope.DrawScope.drawRim(shape: Shape) {
    val stroke = 1.dp.toPx()
    drawOutline(
        shape.createOutline(size, layoutDirection, this),
        Brush.verticalGradient(
            0f to Color.White.copy(alpha = 0.16f),
            0.5f to Color.White.copy(alpha = 0.05f),
            1f to Color.White.copy(alpha = 0.02f),
        ),
        style = Stroke(stroke),
    )
}

private val saturate = ColorMatrix().apply { setSaturation(1.5f) }

/** Эффект пересоздаётся только при смене радиуса, а не на каждом кадре прокрутки. */
private class EffectCache {
    private var radius = -1f
    private var effect: androidx.compose.ui.graphics.RenderEffect? = null
    fun get(r: Float): androidx.compose.ui.graphics.RenderEffect? {
        if (r != radius) { radius = r; effect = glassEffect(r) }
        return effect
    }
}

private fun glassEffect(radiusPx: Float): androidx.compose.ui.graphics.RenderEffect? {
    if (Build.VERSION.SDK_INT < 31 || radiusPx <= 0f) return null
    val blur = android.graphics.RenderEffect.createBlurEffect(radiusPx, radiusPx, android.graphics.Shader.TileMode.CLAMP)
    val color = android.graphics.RenderEffect.createColorFilterEffect(ColorMatrixColorFilter(saturate))
    return android.graphics.RenderEffect.createChainEffect(color, blur).asComposeRenderEffect()
}

