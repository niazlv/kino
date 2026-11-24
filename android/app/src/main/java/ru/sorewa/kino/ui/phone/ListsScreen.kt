package ru.sorewa.kino.ui.phone

import kotlinx.coroutines.launch
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.pager.HorizontalPager
import ru.sorewa.kino.ui.LocalBottomBarInset
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.BookmarkBorder
import androidx.compose.material.icons.rounded.History
import androidx.compose.material.icons.rounded.NewReleases
import androidx.compose.material.icons.rounded.NotificationsNone
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.HistoryItem
import ru.sorewa.kino.data.TokenStore
import ru.sorewa.kino.ui.Avatar
import ru.sorewa.kino.ui.EmptyState
import ru.sorewa.kino.ui.ErrorView
import ru.sorewa.kino.ui.LoadMore
import ru.sorewa.kino.ui.LocalTv
import ru.sorewa.kino.ui.tvGridNav
import ru.sorewa.kino.ui.PillRow
import ru.sorewa.kino.ui.PosterCard
import ru.sorewa.kino.ui.PosterGridSkeleton
import ru.sorewa.kino.ui.ProgressLine
import ru.sorewa.kino.ui.ScreenHeader
import ru.sorewa.kino.ui.ShotImage
import ru.sorewa.kino.ui.kindSubtitle
import ru.sorewa.kino.ui.openTitle
import ru.sorewa.kino.ui.plural
import ru.sorewa.kino.ui.rememberPaged
import ru.sorewa.kino.ui.splitTitle
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain
import ru.sorewa.kino.ui.tvFocusMemory
import ru.sorewa.kino.ui.tvFocusKey
import ru.sorewa.kino.ui.tvFocusRing
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

private val TABS = listOf(0 to "Слежу", 1 to "Новые серии", 2 to "Буду смотреть", 3 to "История")

/**
 * «Моё»: всё личное в одном разделе — слежение со счётчиками, новые серии,
 * отложенное и история просмотров.
 */
@Composable
fun MineScreen(nav: NavHostController, initialTab: Int = 0) {
    var tab by rememberSaveable { mutableStateOf(initialTab) }
    val tv = LocalTv.current
    val store = remember { KinoApp.instance.container.store }
    val session by store.sessionFlow.collectAsStateWithLifecycle(TokenStore.Session("", null, 0, 0, "", "", ""))
    Column(Modifier.fillMaxSize().then(if (tv) Modifier else Modifier.statusBarsPadding())) {
        if (tv) {
            // ТВ: заголовок и вкладки одной строкой, как шапка каталога. Две
            // строки над сеткой съедали четверть экрана (540 dp), и второй
            // ряд постеров не помещался целиком.
            Row(
                Modifier.fillMaxWidth().padding(start = 20.dp, top = 8.dp, bottom = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text("Моё", style = MaterialTheme.typography.titleLarge, color = TextMain)
                Spacer(Modifier.width(24.dp))
                PillRow(TABS, tab, { tab = it }, contentPadding = PaddingValues(horizontal = 0.dp))
            }
        } else {
            ScreenHeader("Моё") {
                IconButton(onClick = { nav.navigate("settings") }) {
                    Avatar(ru.sorewa.kino.ui.Profile.name(session.displayName.ifEmpty { session.lastLogin }), size = 32.dp)
                }
            }
        }
        if (tv) {
            Spacer(Modifier.height(4.dp))
            MineTab(nav, tab)
        } else {
            // Телефон: вкладки листаются свайпом, как положено вкладкам Android;
            // таблетка следует за страницей, тап по таблетке листает к ней.
            val pager = rememberPagerState(initialPage = tab) { TABS.size }
            val scope = rememberCoroutineScope()
            LaunchedEffect(pager) { snapshotFlow { pager.currentPage }.collect { tab = it } }
            PillRow(TABS, tab, { scope.launch { pager.animateScrollToPage(it) } }, followSelected = true)
            Spacer(Modifier.height(8.dp))
            HorizontalPager(pager, Modifier.fillMaxSize(), key = { it }, verticalAlignment = Alignment.Top) { page -> MineTab(nav, page) }
        }
    }
}

@Composable
private fun MineTab(nav: NavHostController, tab: Int) {
    when (tab) {
        0 -> FollowGrid(nav, onlyNew = false)
        1 -> FollowGrid(nav, onlyNew = true)
        2 -> WatchlistGrid(nav)
        else -> HistoryList(nav)
    }
}

/** Старый маршрут «Списки» — теперь вкладки «Моё». */
@Composable
fun ListsScreen(nav: NavHostController) = MineScreen(nav)

@Composable
private fun FollowGrid(nav: NavHostController, onlyNew: Boolean) {
    val api = remember { KinoApp.instance.container.api }
    val paged = rememberPaged("follow", onlyNew, refresh = true) { api.newEpisodes(onlyNew, it) }
    val items = paged.items
    when {
        paged.error.isNotEmpty() && items == null -> ErrorView(paged.error) { paged.reload() }
        items == null -> PosterGridSkeleton()
        items.isEmpty() -> if (onlyNew) EmptyState(
            Icons.Rounded.NewReleases, "Новых серий нет", "Всё, за чем вы следите, досмотрено.",
        ) else EmptyState(
            Icons.Rounded.NotificationsNone, "Пока ни за чем не следите",
            "Нажмите «Следить» на странице сериала — новые серии появятся здесь.",
        )
        else -> PosterGrid(paged.loading, onMore = { paged.more() }, count = items.size) { mem ->
            itemsIndexed(items, key = { _, it -> it.titleId }) { idx, it ->
                PosterCard(
                    modifier = (if (LocalTv.current) Modifier else Modifier.animateItem()).tvFocusKey(mem, it.titleId),
                    titleId = it.titleId, name = it.title, hasArt = it.hasArt,
                    sub = if (it.unwatched > 0) "${it.unwatched} ${plural(it.unwatched, "новая", "новые", "новых")}" else "всё просмотрено",
                    badge = if (it.unwatched > 0) "+${it.unwatched}" else null,
                    dimmed = it.unwatched == 0 && !onlyNew,
                    onClick = { nav.openTitle(it.titleId) },
                )
                if (idx >= items.size - 6) LaunchedEffect(items.size) { paged.more() }
            }
        }
    }
}

@Composable
private fun WatchlistGrid(nav: NavHostController) {
    val api = remember { KinoApp.instance.container.api }
    val paged = rememberPaged("watchlist", refresh = true) { api.list("watchlist", it) }
    val items = paged.items
    when {
        paged.error.isNotEmpty() && items == null -> ErrorView(paged.error) { paged.reload() }
        items == null -> PosterGridSkeleton()
        items.isEmpty() -> EmptyState(
            Icons.Rounded.BookmarkBorder, "Список пуст",
            "Отмечайте «Буду смотреть» на странице тайтла, чтобы не потерять.",
        )
        else -> PosterGrid(paged.loading, onMore = { paged.more() }, count = items.size) { mem ->
            itemsIndexed(items, key = { _, t -> t.id }) { idx, t ->
                PosterCard(
                    modifier = (if (LocalTv.current) Modifier else Modifier.animateItem()).tvFocusKey(mem, t.id),
                    titleId = t.id, name = t.title, hasArt = t.hasArt,
                    sub = kindSubtitle(t.kind, t.year, t.species),
                    onClick = { nav.openTitle(t) },
                )
                if (idx >= items.size - 6) LaunchedEffect(items.size) { paged.more() }
            }
        }
    }
}

@Composable
private fun PosterGrid(
    loading: Boolean,
    onMore: () -> Unit,
    count: Int,
    content: androidx.compose.foundation.lazy.grid.LazyGridScope.(ru.sorewa.kino.ui.TvFocusKey) -> Unit,
) {
    val tv = LocalTv.current
    val state = rememberLazyGridState()
    // «Назад» с тайтла — пульт на ту же карточку.
    val mem = ru.sorewa.kino.ui.rememberTvFocusKey()
    ru.sorewa.kino.ui.TvFocusRestore(mem, ready = count > 0)
    LazyVerticalGrid(
        state = state,
        columns = posterCells(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp + LocalBottomBarInset.current),
        verticalArrangement = Arrangement.spacedBy(18.dp),
        horizontalArrangement = Arrangement.spacedBy(if (tv) 20.dp else 12.dp),
        modifier = Modifier.fillMaxSize().tvFocusMemory().tvGridNav(state),
    ) {
        content(mem)
        if (loading) item(span = { GridItemSpan(maxLineSpan) }) { LoadMore(count, false, true, onMore) }
    }
}

/** История: по дням, кадр серии, где остановились. */
@Composable
fun HistoryList(nav: NavHostController) {
    val api = remember { KinoApp.instance.container.api }
    val paged = rememberPaged<HistoryItem>("history", pageBased = true, refresh = true) { p -> api.history(p, null, null) }
    val items = paged.items
    when {
        paged.error.isNotEmpty() && items == null -> ErrorView(paged.error) { paged.reload() }
        items == null -> PosterGridSkeleton(columns = 2, rows = 3)
        items.isEmpty() -> EmptyState(Icons.Rounded.History, "История пуста", "Здесь будет всё, что вы смотрели.")
        else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp + LocalBottomBarInset.current)) {
            itemsIndexed(items, key = { _, it -> it.episodeId }) { idx, item ->
                val day = dayLabel(item.updatedAt)
                if (idx == items.indexOfFirst { dayLabel(it.updatedAt) == day }) {
                    Text(
                        day,
                        style = MaterialTheme.typography.titleSmall,
                        color = TextDim,
                        modifier = Modifier.padding(start = 20.dp, top = 14.dp, bottom = 4.dp),
                    )
                }
                HistoryRow(item) { nav.openTitle(item.titleId) }
                if (idx >= items.size - 6) LaunchedEffect(items.size) { paged.more() }
            }
            if (paged.loading) item { LoadMore(items.size, false, true) {} }
        }
    }
}

private val dayFmt = DateTimeFormatter.ofPattern("d MMMM", Locale.forLanguageTag("ru"))

private fun dayLabel(iso: String): String = try {
    val d = OffsetDateTime.parse(iso).atZoneSameInstant(ZoneId.systemDefault()).toLocalDate()
    val today = LocalDate.now()
    when (d) {
        today -> "Сегодня"
        today.minusDays(1) -> "Вчера"
        else -> d.format(dayFmt)
    }
} catch (_: Exception) { "" }

@Composable
private fun HistoryRow(item: HistoryItem, onClick: () -> Unit) {
    val watched = item.status == "completed"
    Row(
        Modifier
            .fillMaxWidth()
            .tvFocusRing()
            .clickable(onClick = onClick)
            .padding(horizontal = 20.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.width(132.dp)) {
            ShotImage(
                item.episodeId, item.titleId, item.hasArt, item.title,
                Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(10.dp)),
                tryShot = item.hasShot,
            )
            if (!watched && item.durationMs > 0 && item.positionMs > 0) {
                Spacer(Modifier.height(4.dp))
                ProgressLine(item.positionMs.toFloat() / item.durationMs)
            }
        }
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f)) {
            Text(splitTitle(item.title).first, style = MaterialTheme.typography.titleSmall, color = TextMain, maxLines = 2, overflow = TextOverflow.Ellipsis)
            val se = if (item.seasonNo > 0) "${item.seasonNo} сезон, ${item.episodeNo} серия" else ""
            Text(
                listOf(se, if (watched) "просмотрено" else "не досмотрено").filter { it.isNotEmpty() }.joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = TextFaint,
            )
        }
    }
}

