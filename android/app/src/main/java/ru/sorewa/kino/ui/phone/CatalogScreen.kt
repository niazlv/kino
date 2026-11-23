package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.background
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.DisposableEffect
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import ru.sorewa.kino.ui.Pill
import ru.sorewa.kino.ui.theme.Surface1
import ru.sorewa.kino.ui.theme.TextMain
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Sort
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.SearchOff
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.layout
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.ui.EmptyState
import ru.sorewa.kino.ui.ErrorView
import ru.sorewa.kino.ui.LoadMore
import ru.sorewa.kino.ui.LocalTv
import ru.sorewa.kino.ui.tvGridNav
import ru.sorewa.kino.ui.PillRow
import ru.sorewa.kino.ui.PosterGridSkeleton
import ru.sorewa.kino.ui.ScreenHeader
import ru.sorewa.kino.ui.TitleCard
import ru.sorewa.kino.ui.openTitle
import ru.sorewa.kino.ui.rememberPaged
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Surface2
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.tvFocusMemory
import ru.sorewa.kino.ui.tvFocusKey
import ru.sorewa.kino.ui.tvFocusRing

private val KINDS = listOf("" to "Всё", "movie" to "Фильмы", "series" to "Сериалы")
// «Популярное» — та же популярность, что ранжирует поиск (оценки КП,
// просмотры на площадке). Старый сервер его не знает и отдаёт «новое».
private val SORTS = listOf("new" to "Сначала новое", "popular" to "Популярное", "year" to "По году выхода", "title" to "По названию")

/**
 * Колонки сетки постеров: телефон и планшет — сколько влезет, ТВ — ровно 6,
 * как у kinopub: на экране 960×540 dp карточка ~125 dp, и два ряда
 * постеров с подписями помещаются по высоте целиком.
 */
@Composable
fun posterCells(): GridCells = if (LocalTv.current) GridCells.Fixed(6) else GridCells.Adaptive(104.dp)

/**
 * Каталог: вид и жанр — две разные оси (аниме приходит жанром, а не видом),
 * сортировка в меню справа. Сетка листается с догрузкой.
 */
@Composable
fun CatalogScreen(nav: NavHostController) {
    val api = remember { KinoApp.instance.container.api }
    var kind by rememberSaveable { mutableStateOf("") }
    var genre by rememberSaveable { mutableStateOf("") }
    var sort by rememberSaveable { mutableStateOf("new") }
    var genres by remember { mutableStateOf<List<String>>(emptyList()) }
    var sortMenu by remember { mutableStateOf(false) }
    var genrePicker by remember { mutableStateOf(false) }
    val moreFocus = remember { FocusRequester() }
    val tv = LocalTv.current

    LaunchedEffect(Unit) {
        try { genres = api.genres().items } catch (_: Exception) { /* без жанров тоже можно */ }
    }

    val paged = rememberPaged(kind, genre, sort, pageBased = true) { page ->
        api.browse(kind.ifEmpty { null }, genre.ifEmpty { null }, sort, page)
    }

    // Шапка и таблетки — первый элемент сетки, а не рамка над ней: на ТВ
    // (540 dp по высоте) прибитая шапка съедала треть экрана, и ни один
    // ряд постеров не помещался целиком. Сетка живёт во всех состояниях,
    // чтобы смена жанра не выкидывала фокус пульта из таблеток. Прокрутку
    // за фокусом на ТВ задаёт TvShell (EdgeScroll) — своей здесь нет.
    val state = rememberLazyGridState()
    val items = paged.items
    // «Назад» с тайтла — пульт на ту же карточку (список не перегружается,
    // см. rememberPaged, прокрутка восстанавливается сама).
    val focusMem = ru.sorewa.kino.ui.rememberTvFocusKey()
    ru.sorewa.kino.ui.TvFocusRestore(focusMem, ready = !items.isNullOrEmpty())
    LazyVerticalGrid(
        state = state,
        columns = posterCells(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(18.dp),
        horizontalArrangement = Arrangement.spacedBy(if (tv) 20.dp else 12.dp),
        modifier = Modifier.fillMaxSize().then(if (tv) Modifier else Modifier.statusBarsPadding()).tvFocusMemory().tvGridNav(state),
    ) {
        item(key = "header", span = { GridItemSpan(maxLineSpan) }) {
            val sortButton: @Composable () -> Unit = {
                Box {
                    ru.sorewa.kino.ui.FocusIconButton(Icons.AutoMirrored.Rounded.Sort, "Порядок") { sortMenu = true }
                    DropdownMenu(expanded = sortMenu, onDismissRequest = { sortMenu = false }, containerColor = Surface2) {
                        SORTS.forEach { (id, label) ->
                            DropdownMenuItem(
                                text = { Text(label) },
                                trailingIcon = { if (sort == id) Icon(Icons.Rounded.Check, null, tint = Accent) },
                                onClick = { sort = id; sortMenu = false },
                                modifier = Modifier.tvFocusRing(RoundedCornerShape(8.dp)),
                            )
                        }
                    }
                }
            }
            // Жанров с площадок ~140 (вместе с музыкальными у концертов
            // kinopub) — ряд из всех листался «бесконечно». В ряду самые
            // наполненные и выбранный, остальное — в «Ещё жанры».
            val genreRow: @Composable () -> Unit = {
                val top = genres.take(TOP_GENRES)
                val row = buildList {
                    add("" to "Все жанры")
                    top.forEach { add(it to it) }
                    if (genre.isNotEmpty() && genre !in top) add(genre to genre)
                    if (genres.size > top.size) add(MORE_GENRES to "Ещё жанры…")
                }
                PillRow(
                    row, genre,
                    { if (it == MORE_GENRES) genrePicker = true else genre = it },
                    itemModifier = { if (it == MORE_GENRES) Modifier.focusRequester(moreFocus) else Modifier },
                )
            }
            // У шапки и таблеток свои отступы по 20 dp — растягиваем на поля сетки.
            Column(Modifier.layout { m, c ->
                val extra = 40.dp.roundToPx()
                val w = c.maxWidth + extra
                val p = m.measure(c.copy(minWidth = w, maxWidth = w))
                layout(c.maxWidth, p.height) { p.place(-extra / 2, 0) }
            }) {
                if (tv) {
                    // ТВ: заголовок, счётчик, вид и порядок — одной строкой, жанры —
                    // второй. Крупный заголовок с подписью и два ряда таблеток
                    // съедали треть экрана над постерами.
                    Row(
                        Modifier.fillMaxWidth().padding(start = 20.dp, end = 8.dp, top = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text("Каталог", style = MaterialTheme.typography.titleLarge, color = TextMain)
                        if (paged.total > 0) {
                            Spacer(Modifier.width(10.dp))
                            Text("${paged.total}", style = MaterialTheme.typography.titleSmall, color = TextFaint)
                        }
                        Spacer(Modifier.width(24.dp))
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            KINDS.forEach { (id, label) -> Pill(label, selected = kind == id) { kind = id } }
                        }
                        Spacer(Modifier.weight(1f))
                        sortButton()
                    }
                    if (genres.isNotEmpty()) {
                        Spacer(Modifier.height(10.dp))
                        genreRow()
                    }
                    Spacer(Modifier.height(4.dp))
                } else {
                    ScreenHeader(
                        "Каталог",
                        subtitle = if (paged.total > 0) "${paged.total} в разделе" else null,
                    ) { sortButton() }
                    PillRow(KINDS, kind, { kind = it })
                    if (genres.isNotEmpty()) {
                        Spacer(Modifier.height(8.dp))
                        genreRow()
                    }
                }
            }
        }
        val full: androidx.compose.foundation.lazy.grid.LazyGridItemSpanScope.() -> GridItemSpan = { GridItemSpan(maxLineSpan) }
        when {
            paged.error.isNotEmpty() && items == null -> item(key = "state", span = full) {
                ErrorView(paged.error) { paged.reload() }
            }
            items == null -> item(key = "state", span = full) {
                PosterGridSkeleton(columns = if (tv) 6 else 3, rows = 2)
            }
            items.isEmpty() -> item(key = "state", span = full) {
                EmptyState(
                    Icons.Rounded.SearchOff,
                    "Здесь пока пусто",
                    "Каталог наполняется обходом площадок — возможно, до этого раздела ещё не дошло.",
                )
            }
            else -> {
                itemsIndexed(items, key = { _, t -> t.id }) { idx, t ->
                    TitleCard(title = t, modifier = Modifier.tvFocusKey(focusMem, t.id), onClick = { nav.openTitle(t.id) })
                    if (idx >= items.size - 6) LaunchedEffect(items.size) { paged.more() }
                }
                if (paged.loading) {
                    item(span = full) { LoadMore(key = items.size, hasMore = false, loading = true) {} }
                }
            }
        }
    }

    if (genrePicker) {
        GenrePicker(
            genres = genres,
            selected = genre,
            onPick = { genre = it; genrePicker = false },
            onDismiss = { genrePicker = false },
        )
        // Закрыли выбор — пульт возвращается на «Ещё жанры», а не теряется.
        DisposableEffect(Unit) { onDispose { runCatching { moreFocus.requestFocus() } } }
    }
}

private const val TOP_GENRES = 16
private const val MORE_GENRES = "\u0000more"

/**
 * Все жанры по алфавиту: русские, затем латиница. Пульт ходит по таблеткам
 * и из окна не выходит, «Назад» закрывает, фокус сразу на выбранном жанре.
 */
@OptIn(ExperimentalLayoutApi::class, androidx.compose.ui.ExperimentalComposeUiApi::class)
@Composable
private fun GenrePicker(
    genres: List<String>,
    selected: String,
    onPick: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    val tv = LocalTv.current
    val sorted = remember(genres) {
        val ru = java.text.Collator.getInstance(java.util.Locale("ru"))
        genres.sortedWith(compareBy<String> { !it.first().isCyrillic() }.thenComparator { a, b -> ru.compare(a, b) })
    }
    val first = remember { FocusRequester() }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Column(
            Modifier
                .fillMaxWidth(if (tv) 0.8f else 0.92f)
                .fillMaxHeight(0.85f)
                .clip(RoundedCornerShape(20.dp))
                .background(Surface1)
                .padding(20.dp),
        ) {
            Text("Жанры", style = MaterialTheme.typography.titleLarge, color = TextMain)
            Spacer(Modifier.height(14.dp))
            FlowRow(
                Modifier
                    .verticalScroll(rememberScrollState())
                    .focusProperties {
                        @Suppress("DEPRECATION")
                        exit = { FocusRequester.Cancel }
                    }
                    .focusGroup(),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                val focusId = if (selected in sorted) selected else ""
                (listOf("" to "Все жанры") + sorted.map { it to it }).forEach { (id, label) ->
                    Pill(
                        label,
                        selected = id == selected,
                        modifier = if (id == focusId) Modifier.focusRequester(first) else Modifier,
                    ) { onPick(id) }
                }
            }
        }
        LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
    }
}

private fun Char.isCyrillic() = Character.UnicodeBlock.of(this) == Character.UnicodeBlock.CYRILLIC
