package ru.sorewa.kino.ui

import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.tween
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil.compose.SubcomposeAsyncImage
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.ContinueItem
import ru.sorewa.kino.data.Title
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.OnAccent
import ru.sorewa.kino.ui.theme.Overline
import ru.sorewa.kino.ui.theme.Stroke
import ru.sorewa.kino.ui.theme.Surface1
import ru.sorewa.kino.ui.theme.Surface2
import ru.sorewa.kino.ui.theme.Surface3
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain

/** Экран рисуется для телевизора: крупнее, фокус пультом, без жестов. */
val LocalTv = staticCompositionLocalOf { false }

@Composable
fun ProvideTv(tv: Boolean, content: @Composable () -> Unit) =
    CompositionLocalProvider(LocalTv provides tv, content = content)

// ─── Подписи ────────────────────────────────────────────────────────────

/** «Сериал · 2024», «Фильм · 2023» — как subtitleOf в витрине. */
fun kindSubtitle(kind: String, year: Int, species: String?): String {
    val what = speciesWord(species, kind)
    return listOfNotNull(what.takeIf { it.isNotEmpty() }, year.takeIf { it > 0 }?.toString()).joinToString(" · ")
}

fun speciesWord(species: String?, kind: String): String = when (species ?: kind) {
    "anime" -> "Аниме"
    "cartoon" -> "Мультфильм"
    "dorama" -> "Дорама"
    "series" -> "Сериал"
    "movie" -> "Фильм"
    else -> if (kind == "movie") "Фильм" else if (kind == "series") "Сериал" else ""
}

/** «S01E05 · осталось 12 мин» — подпись карточки «Продолжить смотреть». */
fun describeContinue(item: ContinueItem): String {
    val left = item.durationMs - item.positionMs
    val tail = if (left > 0) "осталось ${fmtMins(left / 60_000)}" else ""
    val se = if (item.seasonNo > 0) "${item.seasonNo} сезон, ${item.episodeNo} серия" else ""
    return listOf(se, tail).filter { it.isNotEmpty() }.joinToString(" · ")
}

fun fmtMins(mins: Long): String = when {
    mins >= 60 -> "${mins / 60} ч ${mins % 60} мин"
    mins <= 0 -> "меньше минуты"
    else -> "$mins мин"
}

fun kindWord(kind: String): String = when (kind) {
    "movie" -> "Фильм"
    "series" -> "Сериал"
    "anime" -> "Аниме"
    "cartoon" -> "Мультфильм"
    "dorama" -> "Дорама"
    else -> "Тайтл"
}

/**
 * Названия приходят как «Тьма / Dark»: русское — главное, оригинал —
 * подпись. Делим по первому « / ».
 */
fun splitTitle(full: String): Pair<String, String> {
    val i = full.indexOf(" / ")
    return if (i <= 0) full to "" else full.substring(0, i).trim() to full.substring(i + 3).trim()
}

/** «серий: 1 / 2 / 5» по-русски. */
fun plural(n: Int, one: String, few: String, many: String): String {
    val m10 = n % 10
    val m100 = n % 100
    return when {
        m10 == 1 && m100 != 11 -> one
        m10 in 2..4 && m100 !in 12..14 -> few
        else -> many
    }
}

// ─── Фокус пультом ───────────────────────────────────────────────────────

/**
 * Кликабельная карточка: на ТВ в фокусе увеличивается и получает светлую
 * рамку — с дивана видно, где курсор. На телефоне — обычное нажатие и
 * долгое нажатие.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun Modifier.cardClick(
    shape: Shape,
    onLongClick: (() -> Unit)? = null,
    focusScale: Float = 1.07f,
    onClick: () -> Unit,
): Modifier {
    val tv = LocalTv.current
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    // Телефон: под пальцем карточка чуть вдавливается и отпружинивает обратно
    // (Material 3 Expressive). ТВ: увеличение в фокусе, как было.
    val pressed by source.collectIsPressedAsState()
    val scale by animateFloatAsState(
        when {
            tv -> if (focused) focusScale else 1f
            pressed -> 0.96f
            else -> 1f
        },
        when {
            tv -> tween(160)
            pressed -> ru.sorewa.kino.ui.theme.KinoMotion.fastSpatial()
            else -> ru.sorewa.kino.ui.theme.KinoMotion.bouncy()
        },
        label = "focus",
    )
    return this
        .scale(scale)
        .then(if (tv && focused) Modifier.shadow(18.dp, shape, ambientColor = Color.Black, spotColor = Color.Black) else Modifier)
        .clip(shape)
        .then(if (tv && focused) Modifier.border(3.dp, Color.White, shape) else Modifier)
        .combinedClickable(
            interactionSource = source,
            indication = if (tv) null else androidx.compose.foundation.LocalIndication.current,
            onLongClick = onLongClick,
            onClick = onClick,
        )
}

/**
 * Отклик на нажатие для кнопок и таблеток телефона: лёгкое сжатие под пальцем
 * и пружина обратно. На ТВ ничего не делает — там отклик даёт фокус.
 */
@Composable
fun Modifier.pressScale(source: androidx.compose.foundation.interaction.InteractionSource, pressedScale: Float = 0.95f): Modifier {
    if (LocalTv.current) return this
    val pressed by source.collectIsPressedAsState()
    val s by animateFloatAsState(
        if (pressed) pressedScale else 1f,
        if (pressed) ru.sorewa.kino.ui.theme.KinoMotion.fastSpatial() else ru.sorewa.kino.ui.theme.KinoMotion.bouncy(),
        label = "press",
    )
    return graphicsLayer { scaleX = s; scaleY = s }
}

// ─── Картинки ────────────────────────────────────────────────────────────

/** Заглушка без постера: мягкий градиент по названию + само название. */
@Composable
fun ArtPlaceholder(name: String, modifier: Modifier = Modifier, showName: Boolean = true) {
    val hue = remember(name) { ((name.hashCode() and 0x7fffffff) % 360).toFloat() }
    val c1 = Color.hsv(hue, 0.35f, 0.28f)
    val c2 = Color.hsv((hue + 40f) % 360f, 0.45f, 0.14f)
    Box(
        modifier.background(Brush.linearGradient(listOf(c1, c2))),
        contentAlignment = Alignment.Center,
    ) {
        if (showName) {
            Text(
                splitTitle(name).first,
                color = Color.White.copy(alpha = 0.85f),
                style = MaterialTheme.typography.titleSmall,
                textAlign = TextAlign.Center,
                maxLines = 4,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(10.dp),
            )
        }
    }
}

/**
 * Постер тайтла по id; пока грузится и при ошибке — заглушка. Постер,
 * сохранённый для скачанного (files/art), берётся с диска — без сети тоже.
 */
@Composable
fun PosterImage(titleId: Long, hasArt: Boolean, name: String, modifier: Modifier = Modifier) {
    val local = remember(titleId) { KinoApp.instance.container.art.posterPath(titleId) }
    if (local == null && (!hasArt || titleId <= 0)) {
        ArtPlaceholder(name, modifier)
        return
    }
    SubcomposeAsyncImage(
        model = local ?: KinoApp.instance.container.api.artUrl(titleId),
        contentDescription = name,
        contentScale = ContentScale.Crop,
        modifier = modifier,
        loading = { Shimmer(Modifier.fillMaxSize()) },
        error = { ArtPlaceholder(name, Modifier.fillMaxSize()) },
    )
}

/**
 * Кадр серии; нет кадра — постер тайтла, нет и его — заглушка. Сохранённый
 * кадр (files/art) — с диска; localFrame — кадр из самого скачанного файла,
 * если серверного нет.
 */
@Composable
fun ShotImage(
    episodeId: Long,
    titleId: Long,
    hasTitleArt: Boolean,
    name: String,
    modifier: Modifier = Modifier,
    tryShot: Boolean = true,
    localFrame: java.io.File? = null,
) {
    val local = remember(episodeId, localFrame) {
        KinoApp.instance.container.art.shotPath(episodeId) ?: localFrame
    }
    if (local == null && (!tryShot || episodeId <= 0)) {
        PosterImage(titleId, hasTitleArt, name, modifier)
        return
    }
    SubcomposeAsyncImage(
        model = local ?: KinoApp.instance.container.api.shotUrl(episodeId),
        contentDescription = null,
        contentScale = ContentScale.Crop,
        modifier = modifier,
        loading = { Shimmer(Modifier.fillMaxSize()) },
        error = { PosterImage(titleId, hasTitleArt, name, Modifier.fillMaxSize()) },
    )
}

/** Мерцающая заглушка загрузки. */
@Composable
fun Shimmer(modifier: Modifier = Modifier) {
    val t = androidx.compose.animation.core.rememberInfiniteTransition(label = "shimmer")
    val a by t.animateFloat(
        initialValue = 0.45f, targetValue = 0.9f,
        animationSpec = androidx.compose.animation.core.infiniteRepeatable(
            tween(900), androidx.compose.animation.core.RepeatMode.Reverse,
        ),
        label = "a",
    )
    Box(modifier.background(Surface2.copy(alpha = a)))
}

/** Полоска досмотренного поверх картинки или под строкой. */
@Composable
fun ProgressLine(frac: Float, modifier: Modifier = Modifier, height: Dp = 3.dp, track: Color = Color(0x40FFFFFF)) {
    Box(modifier.fillMaxWidth().height(height).clip(RoundedCornerShape(50)).background(track)) {
        Box(Modifier.fillMaxWidth(frac.coerceIn(0.02f, 1f)).fillMaxHeight().background(Accent))
    }
}

/** Плашка на картинке: «+3», «4K», «Новое». */
@Composable
fun Badge(text: String, modifier: Modifier = Modifier, color: Color = Accent, textColor: Color = OnAccent) {
    Text(
        text,
        color = textColor,
        fontSize = 11.sp,
        fontWeight = FontWeight.Bold,
        modifier = modifier
            .background(color, RoundedCornerShape(6.dp))
            .padding(horizontal = 6.dp, vertical = 2.dp),
    )
}

// ─── Карточки ────────────────────────────────────────────────────────────

/**
 * Постер 2:3 с названием и подписью. badge — плашка в углу (новые серии),
 * progress — полоска досмотренного по низу постера.
 */
@Composable
fun PosterCard(
    titleId: Long,
    name: String,
    hasArt: Boolean,
    modifier: Modifier = Modifier,
    sub: String? = null,
    badge: String? = null,
    progress: Float? = null,
    dimmed: Boolean = false,
    onLongClick: (() -> Unit)? = null,
    onClick: () -> Unit,
) {
    val tv = LocalTv.current
    val shape = RoundedCornerShape(if (tv) 12.dp else 12.dp)
    Column(modifier) {
        Box(
            Modifier
                .fillMaxWidth()
                .aspectRatio(2f / 3f)
                .cardClick(shape, onLongClick, onClick = onClick)
                .background(Surface1),
        ) {
            PosterImage(titleId, hasArt, name, Modifier.fillMaxSize())
            if (dimmed) Box(Modifier.fillMaxSize().background(Color(0x66000000)))
            if (badge != null) Badge(badge, Modifier.align(Alignment.TopStart).padding(6.dp))
            if (progress != null && progress > 0f) {
                Box(
                    Modifier
                        .align(Alignment.BottomCenter)
                        .fillMaxWidth()
                        .background(Brush.verticalGradient(listOf(Color.Transparent, Color(0xAA000000))))
                        .padding(start = 8.dp, end = 8.dp, top = 16.dp, bottom = 8.dp),
                ) { ProgressLine(progress) }
            }
        }
        Spacer(Modifier.height(8.dp))
        Text(
            splitTitle(name).first,
            style = if (tv) MaterialTheme.typography.titleSmall else MaterialTheme.typography.bodyMedium.copy(fontWeight = FontWeight.Medium),
            color = TextMain,
            maxLines = if (sub == null) 2 else 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (!sub.isNullOrEmpty()) {
            Text(sub, style = MaterialTheme.typography.bodySmall, color = TextDim, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

/** Карточка тайтла из каталога — обёртка над [PosterCard]. */
@Composable
fun TitleCard(
    title: Title,
    modifier: Modifier = Modifier,
    progress: Float? = null,
    subLine: String? = null,
    badge: String? = null,
    onLongClick: (() -> Unit)? = null,
    onClick: () -> Unit,
    tv: Boolean = false,
) = PosterCard(
    titleId = title.id,
    name = title.title,
    hasArt = title.hasArt,
    modifier = modifier,
    sub = subLine ?: kindSubtitle(title.kind, title.year, title.species),
    badge = badge,
    progress = progress,
    onLongClick = onLongClick,
    onClick = onClick,
)

/**
 * Широкая карточка 16:9 — кадр серии с названием поверх: «Продолжить
 * смотреть», серии на ТВ, скачанное.
 */
@Composable
fun WideCard(
    episodeId: Long,
    titleId: Long,
    hasArt: Boolean,
    name: String,
    sub: String,
    modifier: Modifier = Modifier,
    progress: Float? = null,
    tryShot: Boolean = true,
    showPlay: Boolean = true,
    onLongClick: (() -> Unit)? = null,
    onClick: () -> Unit,
) {
    val shape = RoundedCornerShape(14.dp)
    Box(
        modifier
            .aspectRatio(16f / 9f)
            .cardClick(shape, onLongClick, focusScale = 1.05f, onClick = onClick)
            .background(Surface1),
    ) {
        ShotImage(episodeId, titleId, hasArt, name, Modifier.fillMaxSize(), tryShot)
        Box(
            Modifier
                .fillMaxSize()
                .background(Brush.verticalGradient(0.35f to Color.Transparent, 1f to Color(0xE6000000))),
        )
        if (showPlay) {
            Box(
                Modifier
                    .align(Alignment.Center)
                    .size(44.dp)
                    .background(Color(0x66000000), CircleShape)
                    .border(1.dp, Color(0x55FFFFFF), CircleShape),
                contentAlignment = Alignment.Center,
            ) { Icon(Icons.Rounded.PlayArrow, null, tint = Color.White, modifier = Modifier.size(28.dp)) }
        }
        Column(
            Modifier
                .align(Alignment.BottomStart)
                .fillMaxWidth()
                .padding(horizontal = 12.dp, vertical = 10.dp),
        ) {
            Text(
                splitTitle(name).first,
                style = MaterialTheme.typography.titleSmall,
                color = Color.White,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (sub.isNotEmpty()) {
                Text(sub, style = MaterialTheme.typography.bodySmall, color = Color(0xCCFFFFFF), maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (progress != null && progress > 0f) {
                Spacer(Modifier.height(8.dp))
                ProgressLine(progress)
            }
        }
    }
}

// ─── Раскладка ───────────────────────────────────────────────────────────

/** Заголовок раздела с необязательной ссылкой «Все» справа. */
@Composable
fun SectionHeader(
    title: String,
    modifier: Modifier = Modifier,
    count: Int? = null,
    action: String? = null,
    onAction: (() -> Unit)? = null,
) {
    val tv = LocalTv.current
    Row(
        modifier
            .fillMaxWidth()
            .padding(start = if (tv) 0.dp else 20.dp, end = if (tv) 0.dp else 8.dp, top = 8.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            title,
            style = if (tv) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium,
            color = TextMain,
        )
        if (count != null && count > 0) {
            Spacer(Modifier.width(8.dp))
            Text("$count", style = MaterialTheme.typography.titleSmall, color = TextFaint)
        }
        Spacer(Modifier.weight(1f))
        if (action != null && onAction != null && !tv) {
            TextButton(onClick = onAction) { Text(action, color = Accent) }
        }
    }
}

/** Горизонтальная лента карточек раздела. */
@Composable
fun Rail(
    modifier: Modifier = Modifier,
    // Своя прокрутка ленты (серии на ТВ стартуют со следующей) и куда встать
    // пульту при первом входе в ленту, пока своей памяти у неё нет.
    state: androidx.compose.foundation.lazy.LazyListState? = null,
    focusStart: FocusRequester? = null,
    content: androidx.compose.foundation.lazy.LazyListScope.() -> Unit,
) {
    val tv = LocalTv.current
    val row: @Composable () -> Unit = {
        LazyRow(
            state = state ?: androidx.compose.foundation.lazy.rememberLazyListState(),
            modifier = modifier.fillMaxWidth().tvFocusMemory(focusStart),
            contentPadding = PaddingValues(horizontal = if (tv) 4.dp else 20.dp, vertical = if (tv) 14.dp else 8.dp),
            horizontalArrangement = Arrangement.spacedBy(if (tv) 20.dp else 12.dp),
            content = content,
        )
    }
    // На ТВ лента стоит, пока фокус не подошёл к краю, — не едет на каждом шаге.
    if (tv) EdgeScroll(72.dp, row) else row()
}

/** Крупный заголовок экрана с местом под кнопки справа. */
@Composable
fun ScreenHeader(
    title: String,
    modifier: Modifier = Modifier,
    subtitle: String? = null,
    actions: @Composable RowScope.() -> Unit = {},
) {
    Row(
        modifier
            .fillMaxWidth()
            .padding(start = 20.dp, end = 8.dp, top = 12.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.headlineMedium, color = TextMain)
            if (!subtitle.isNullOrEmpty()) {
                Text(subtitle, style = MaterialTheme.typography.bodyMedium, color = TextDim)
            }
        }
        actions()
    }
}

/**
 * Ряд «таблеток»-переключателей: вид в каталоге, сезоны, фильтры.
 * Листается вбок, выбранная — заливкой.
 *
 * Вправо с последней таблетки пульт упирается в конец ряда: иначе общий
 * поиск фокуса уводил его к ближайшему по геометрии — на соседний ряд
 * ниже или на кнопку в углу, и казалось, что ряд «перепрыгивает вбок».
 * Влево, вверх и вниз — как обычно (влево с первой — в меню).
 */
@OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)
@Composable
fun <T> PillRow(
    options: List<Pair<T, String>>,
    selected: T,
    onSelect: (T) -> Unit,
    modifier: Modifier = Modifier,
    contentPadding: PaddingValues = PaddingValues(horizontal = 20.dp),
    itemModifier: (T) -> Modifier = { Modifier },
    leadingOf: (T) -> ImageVector? = { null },
    followSelected: Boolean = false,
) {
    val rowState = androidx.compose.foundation.lazy.rememberLazyListState()
    // Выбор пришёл не от касания таблетки (свайп страниц) — ряд подкручивается
    // к ней, как вкладки Material. На ТВ ряд ведёт фокус, там это не нужно.
    if (followSelected) {
        LaunchedEffect(selected) {
            val i = options.indexOfFirst { it.first == selected }
            if (i >= 0) {
                val info = rowState.layoutInfo
                val item = info.visibleItemsInfo.firstOrNull { it.index == i }
                val visible = item != null && item.offset >= info.viewportStartOffset &&
                    item.offset + item.size <= info.viewportEndOffset
                if (!visible) rowState.animateScrollToItem(i)
            }
        }
    }
    LazyRow(
        state = rowState,
        modifier = modifier
            .fillMaxWidth()
            .focusProperties {
                @Suppress("DEPRECATION")
                exit = { dir -> if (dir == FocusDirection.Right) FocusRequester.Cancel else FocusRequester.Default }
            }
            .focusGroup(),
        contentPadding = contentPadding,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        items(options.size) { i ->
            val (id, label) = options[i]
            Pill(label, selected = id == selected, modifier = itemModifier(id), leading = leadingOf(id)) { onSelect(id) }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun Pill(
    label: String,
    selected: Boolean,
    modifier: Modifier = Modifier,
    leading: ImageVector? = null,
    trailing: (@Composable () -> Unit)? = null,
    onLongClick: (() -> Unit)? = null,
    onClick: () -> Unit,
) {
    val tv = LocalTv.current
    val shape = RoundedCornerShape(50)
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    val bg = when {
        tv && focused -> Color.White
        selected -> Accent
        else -> Surface2
    }
    val fg = when {
        tv && focused -> Color.Black
        selected -> OnAccent
        else -> TextMain
    }
    Row(
        modifier
            .pressScale(source)
            .clip(shape)
            .background(bg)
            .then(if (!selected && !(tv && focused)) Modifier.border(1.dp, Stroke, shape) else Modifier)
            .then(
                if (onLongClick == null) Modifier.clickable(interactionSource = source, indication = androidx.compose.foundation.LocalIndication.current, onClick = onClick)
                else Modifier.combinedClickable(interactionSource = source, indication = androidx.compose.foundation.LocalIndication.current, onLongClick = onLongClick, onClick = onClick),
            )
            .padding(horizontal = if (tv) 18.dp else 14.dp, vertical = if (tv) 10.dp else 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (leading != null) {
            Icon(leading, null, tint = fg, modifier = Modifier.size(16.dp))
            Spacer(Modifier.width(6.dp))
        }
        Text(label, color = fg, style = MaterialTheme.typography.labelLarge, maxLines = 1)
        if (trailing != null) {
            Spacer(Modifier.width(4.dp))
            trailing()
        }
    }
}

/** Кнопка-действие под заголовком тайтла: иконка над подписью. */
@Composable
fun IconAction(
    icon: ImageVector,
    label: String,
    modifier: Modifier = Modifier,
    active: Boolean = false,
    onClick: () -> Unit,
) {
    val tv = LocalTv.current
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    Column(
        modifier
            .pressScale(source, 0.92f)
            .clip(RoundedCornerShape(14.dp))
            .background(if (tv && focused) Color.White else Color.Transparent)
            .clickable(interactionSource = source, indication = androidx.compose.foundation.LocalIndication.current, onClick = onClick)
            .padding(vertical = 8.dp, horizontal = 4.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(
            icon, null,
            tint = when {
                tv && focused -> Color.Black
                active -> Accent
                else -> TextMain
            },
            modifier = Modifier.size(24.dp),
        )
        Spacer(Modifier.height(6.dp))
        Text(
            label,
            style = MaterialTheme.typography.labelMedium,
            color = when {
                tv && focused -> Color.Black
                active -> Accent
                else -> TextDim
            },
            maxLines = 2,
            textAlign = TextAlign.Center,
        )
    }
}

/** Главная кнопка: зелёная, крупная. */
@Composable
fun PrimaryButton(
    text: String,
    modifier: Modifier = Modifier,
    icon: ImageVector? = null,
    enabled: Boolean = true,
    busy: Boolean = false,
    onClick: () -> Unit,
) {
    val tv = LocalTv.current
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    Button(
        onClick = onClick,
        enabled = enabled && !busy,
        interactionSource = source,
        modifier = modifier.pressScale(source, 0.97f).height(52.dp),
        shape = RoundedCornerShape(14.dp),
        colors = ButtonDefaults.buttonColors(
            containerColor = if (tv && focused) Color.White else Accent,
            contentColor = if (tv && focused) Color.Black else OnAccent,
        ),
        contentPadding = PaddingValues(horizontal = 20.dp),
    ) {
        if (busy) {
            CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp, color = OnAccent)
        } else {
            if (icon != null) {
                Icon(icon, null, Modifier.size(24.dp))
                Spacer(Modifier.width(8.dp))
            }
            Text(text, style = MaterialTheme.typography.labelLarge.copy(fontSize = 16.sp), maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

/** Второстепенная кнопка: тёмная заливка. */
@Composable
fun SecondaryButton(
    text: String,
    modifier: Modifier = Modifier,
    icon: ImageVector? = null,
    enabled: Boolean = true,
    onClick: () -> Unit,
) {
    val tv = LocalTv.current
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    Button(
        onClick = onClick,
        enabled = enabled,
        interactionSource = source,
        modifier = modifier.pressScale(source, 0.97f).height(52.dp),
        shape = RoundedCornerShape(14.dp),
        colors = ButtonDefaults.buttonColors(
            containerColor = if (tv && focused) Color.White else Surface3,
            contentColor = if (tv && focused) Color.Black else TextMain,
        ),
        contentPadding = PaddingValues(horizontal = 18.dp),
    ) {
        if (icon != null) {
            Icon(icon, null, Modifier.size(22.dp))
            Spacer(Modifier.width(8.dp))
        }
        Text(text, style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

/** Карточка-подложка для групп настроек и статусов. */
@Composable
fun Panel(
    modifier: Modifier = Modifier,
    padding: PaddingValues = PaddingValues(16.dp),
    content: @Composable androidx.compose.foundation.layout.ColumnScope.() -> Unit,
) {
    Column(
        modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(18.dp))
            .background(Surface1)
            .border(1.dp, Stroke, RoundedCornerShape(18.dp))
            .padding(padding),
        content = content,
    )
}

/** Мелкая подпись над группой. */
@Composable
fun Overline(text: String, modifier: Modifier = Modifier) {
    Text(text.uppercase(), style = Overline, color = TextFaint, modifier = modifier)
}

// ─── Состояния ──────────────────────────────────────────────────────────

/** Догрузка списков: список листается, а не обрезается — уроки циклов подгрузки. */
@Composable
fun LoadMore(
    key: Any?,
    hasMore: Boolean,
    loading: Boolean,
    onLoadMore: () -> Unit,
) {
    LaunchedEffect(key, hasMore) {
        if (hasMore && !loading) onLoadMore()
    }
    if (loading) {
        Box(Modifier.fillMaxWidth().padding(20.dp), contentAlignment = Alignment.Center) {
            CircularProgressIndicator(Modifier.size(28.dp), strokeWidth = 2.5.dp, color = Accent)
        }
    }
}

@Composable
fun CenterSpinner(modifier: Modifier = Modifier) {
    Box(modifier.fillMaxSize().padding(bottom = LocalBottomBarInset.current), contentAlignment = Alignment.Center) {
        CircularProgressIndicator(Modifier.size(36.dp), strokeWidth = 3.dp, color = Accent)
    }
}

/** Пустое место с иконкой, заголовком и пояснением. */
@Composable
fun EmptyState(
    icon: ImageVector,
    title: String,
    text: String? = null,
    modifier: Modifier = Modifier,
    action: String? = null,
    onAction: (() -> Unit)? = null,
) {
    Column(
        modifier.fillMaxWidth().padding(horizontal = 32.dp, vertical = 48.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box(
            Modifier.size(72.dp).background(Surface2, CircleShape),
            contentAlignment = Alignment.Center,
        ) { Icon(icon, null, tint = TextDim, modifier = Modifier.size(34.dp)) }
        Spacer(Modifier.height(16.dp))
        Text(title, style = MaterialTheme.typography.titleMedium, color = TextMain, textAlign = TextAlign.Center)
        if (!text.isNullOrEmpty()) {
            Spacer(Modifier.height(6.dp))
            Text(text, style = MaterialTheme.typography.bodyMedium, color = TextDim, textAlign = TextAlign.Center)
        }
        if (action != null && onAction != null) {
            Spacer(Modifier.height(18.dp))
            SecondaryButton(action, onClick = onAction)
        }
    }
}

@Composable
fun ErrorView(text: String, onRetry: (() -> Unit)? = null) = EmptyState(
    icon = androidx.compose.material.icons.Icons.Rounded.Refresh,
    title = "Не получилось загрузить",
    text = text,
    action = if (onRetry != null) "Повторить" else null,
    onAction = onRetry,
)

@Composable
fun EmptyView(text: String) {
    Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) {
        Text(text, color = TextDim, style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center)
    }
}

@Composable
fun RowSpacer(w: Int = 12) = Spacer(Modifier.width(w.dp))

/** Аватар-кружок с первой буквой имени. */
@Composable
fun Avatar(name: String, size: Dp = 36.dp, modifier: Modifier = Modifier) {
    Box(
        modifier
            .size(size)
            .background(Brush.linearGradient(listOf(Accent, AccentAlt)), CircleShape),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            name.trim().take(1).uppercase().ifEmpty { "?" },
            color = OnAccent,
            fontWeight = FontWeight.Bold,
            fontSize = (size.value * 0.42f).sp,
        )
    }
}

private val AccentAlt = Color(0xFF2BB8C9)

/** Сетка-скелет на время первой загрузки. */
@Composable
fun PosterGridSkeleton(columns: Int = 3, rows: Int = 3) {
    Column(Modifier.padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        repeat(rows) {
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                repeat(columns) {
                    Column(Modifier.weight(1f)) {
                        Shimmer(Modifier.fillMaxWidth().aspectRatio(2f / 3f).clip(RoundedCornerShape(12.dp)))
                        Spacer(Modifier.height(8.dp))
                        Shimmer(Modifier.fillMaxWidth(0.8f).height(12.dp).clip(RoundedCornerShape(4.dp)))
                    }
                }
            }
        }
    }
}


/**
 * Прокрутка к фокусу «по необходимости»: на ТВ Compose по умолчанию тянет
 * фокус к трети экрана и уводит шапку страницы. Здесь — сдвиг ровно
 * настолько, чтобы фокус стал виден.
 */
@OptIn(ExperimentalFoundationApi::class)
val MinimalBringIntoView = object : androidx.compose.foundation.gestures.BringIntoViewSpec {
    override fun calculateScrollDistance(offset: Float, size: Float, containerSize: Float): Float {
        val end = offset + size
        return when {
            offset < 0 -> offset
            end > containerSize -> if (size > containerSize) offset else end - containerSize
            else -> 0f
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun MinimalScroll(content: @Composable () -> Unit) = CompositionLocalProvider(
    androidx.compose.foundation.gestures.LocalBringIntoViewSpec provides MinimalBringIntoView,
    content = content,
)

/**
 * Прокрутка к фокусу только у края: пока фокус дальше [margin] от краёв,
 * список стоит; у края сдвигается ровно настолько, чтобы до края осталось
 * [margin] (видно соседа). Без этого Compose на ТВ тянет фокус к трети
 * экрана, и всё едет на каждом нажатии — экран «раскачивается».
 */
@OptIn(ExperimentalFoundationApi::class)
fun edgeBringIntoView(marginPx: Float) = object : androidx.compose.foundation.gestures.BringIntoViewSpec {
    override fun calculateScrollDistance(offset: Float, size: Float, containerSize: Float): Float {
        val m = marginPx.coerceAtMost(((containerSize - size) / 2f).coerceAtLeast(0f))
        val end = offset + size
        return when {
            offset < m -> offset - m
            end > containerSize - m -> end - (containerSize - m)
            else -> 0f
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun EdgeScroll(margin: androidx.compose.ui.unit.Dp, content: @Composable () -> Unit) {
    val px = with(androidx.compose.ui.platform.LocalDensity.current) { margin.toPx() }
    val spec = remember(px) { edgeBringIntoView(px) }
    CompositionLocalProvider(androidx.compose.foundation.gestures.LocalBringIntoViewSpec provides spec, content = content)
}

/** Список сам не прокручивается за фокусом — прокрутку ведёт экран. */
@OptIn(ExperimentalFoundationApi::class)
val NoBringIntoView = object : androidx.compose.foundation.gestures.BringIntoViewSpec {
    override fun calculateScrollDistance(offset: Float, size: Float, containerSize: Float): Float = 0f
}

// ─── Фокус пульта у стандартных элементов ────────────────────────────────
// Material подсвечивает фокус полупрозрачной серой плёнкой — на телевизоре
// с дивана её не видно (сортировка каталога, подсказки поиска, строки
// профиля). Везде остальное фокус — белая подложка или белая рамка; эти
// помощники делают так же. На телефоне всё как было.

/**
 * Белая рамка и подсветка строки под пультом. Ставится ПЕРЕД clickable:
 * onFocusChanged видит фокус кнопки, что идёт за ним.
 */
@Composable
fun Modifier.tvFocusRing(shape: Shape = RoundedCornerShape(12.dp)): Modifier {
    if (!LocalTv.current) return this
    var focused by remember { mutableStateOf(false) }
    return this
        .onFocusChanged { focused = it.isFocused }
        .then(
            if (focused) Modifier.clip(shape).background(Color(0x24FFFFFF)).border(2.dp, Color.White, shape)
            else Modifier,
        )
}

/** Кнопка-значок: на ТВ в фокусе — белый круг с чёрным значком. */
@Composable
fun FocusIconButton(
    icon: ImageVector,
    contentDescription: String?,
    modifier: Modifier = Modifier,
    tint: Color = TextMain,
    onClick: () -> Unit,
) {
    if (!LocalTv.current) {
        androidx.compose.material3.IconButton(onClick = onClick, modifier = modifier) {
            Icon(icon, contentDescription, tint = tint)
        }
        return
    }
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    Box(
        modifier
            .size(48.dp)
            .clip(CircleShape)
            .background(if (focused) Color.White else Color.Transparent)
            .clickable(interactionSource = source, indication = null, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Icon(icon, contentDescription, tint = if (focused) Color.Black else tint, modifier = Modifier.size(24.dp))
    }
}

/** Текстовая кнопка: на ТВ в фокусе — белая плашка с тёмным текстом. */
@Composable
fun FocusTextButton(
    text: String,
    modifier: Modifier = Modifier,
    color: Color = Accent,
    onClick: () -> Unit,
) {
    if (!LocalTv.current) {
        TextButton(onClick = onClick, modifier = modifier) { Text(text, color = color) }
        return
    }
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    Box(
        modifier
            .clip(RoundedCornerShape(50))
            .background(if (focused) Color.White else Color.Transparent)
            .clickable(interactionSource = source, indication = null, onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 10.dp),
    ) {
        Text(text, color = if (focused) Color.Black else color, style = MaterialTheme.typography.labelLarge)
    }
}
