package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.layout.statusBars
import androidx.compose.foundation.layout.WindowInsets
import ru.sorewa.kino.ui.rememberGlassSource
import ru.sorewa.kino.ui.glassSource
import ru.sorewa.kino.ui.glass
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.runtime.derivedStateOf
import androidx.compose.foundation.layout.windowInsetsTopHeight
import ru.sorewa.kino.ui.LocalBottomBarInset
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material.icons.rounded.Explore
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import kotlinx.coroutines.launch
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.ContinueItem
import ru.sorewa.kino.data.Page
import ru.sorewa.kino.data.TokenStore
import ru.sorewa.kino.ui.Avatar
import ru.sorewa.kino.ui.EmptyState
import ru.sorewa.kino.ui.ErrorView
import ru.sorewa.kino.ui.PosterCard
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.ProgressLine
import ru.sorewa.kino.ui.Rail
import ru.sorewa.kino.ui.SectionHeader
import ru.sorewa.kino.ui.ShotImage
import ru.sorewa.kino.ui.Shimmer
import ru.sorewa.kino.ui.WideCard
import ru.sorewa.kino.ui.describeContinue
import ru.sorewa.kino.ui.kindSubtitle
import ru.sorewa.kino.ui.openTitle
import ru.sorewa.kino.ui.playEpisode
import ru.sorewa.kino.ui.rememberPaged
import ru.sorewa.kino.ui.splitTitle
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Bg
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextMain
import ru.sorewa.kino.ui.cardClick
import ru.sorewa.kino.ui.Overline as OverlineText

/**
 * Главная: «Продолжить» крупно наверху (одно касание — и серия играет),
 * дальше ленты — начатое, новые серии, отложенное, новинки каталога.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(nav: NavHostController) {
    val api = remember { KinoApp.instance.container.api }
    val store = remember { KinoApp.instance.container.store }
    val stateStore = remember { KinoApp.instance.container.state }
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val session by store.sessionFlow.collectAsStateWithLifecycle(TokenStore.Session("", null, 0, 0, "", "", ""))

    var refreshKey by remember { mutableStateOf(0) }
    val cont = rememberPaged<ContinueItem>("continue", refreshKey, refresh = true) { offset ->
        try {
            api.continueWatching(offset).also { rsp ->
                // Последняя удачная страница — на случай офлайна.
                if (offset == 0) stateStore.cacheContinue(rsp.items)
            }
        } catch (e: ru.sorewa.kino.data.ApiError) {
            if (offset == 0 && e.offline) {
                val cached = stateStore.cachedContinue()
                if (cached.isEmpty()) throw e
                // Ждущие позиции поверх кэша: досмотренное в самолёте
                // выпадает, начатое показывает своё. Дальше офлайном
                // страниц нет (hasMore=false).
                Page(items = kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                    ru.sorewa.kino.data.overlayContinue(cached, stateStore.pendingEpisodes())
                })
            } else throw e
        }
    }
    val fresh = rememberPaged("new", refreshKey, refresh = true) { api.newEpisodes(true, it) }
    val watchlist = rememberPaged("wl", refreshKey, refresh = true) { api.list("watchlist", it) }
    val movies = rememberPaged("movies", refreshKey, pageBased = true) { api.browse("movie", null, "new", it) }
    val series = rememberPaged("series", refreshKey, pageBased = true) { api.browse("series", null, "new", it) }

    var menuFor by remember { mutableStateOf<ContinueItem?>(null) }

    val contItems = cont.items
    val refreshing = cont.loading && contItems != null

    val listState = androidx.compose.foundation.lazy.rememberLazyListState()
    val glass = rememberGlassSource()
    Box(Modifier.fillMaxSize()) {
    PullToRefreshBox(isRefreshing = refreshing, onRefresh = { refreshKey++ }, modifier = Modifier.fillMaxSize().glassSource(glass)) {
        LazyColumn(Modifier.fillMaxSize(), state = listState) {
            item {
                Row(
                    Modifier
                        .fillMaxWidth()
                        .statusBarsPadding()
                        .padding(start = 20.dp, end = 16.dp, top = 8.dp, bottom = 4.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    BrandMark()
                    Spacer(Modifier.weight(1f))
                    Avatar(
                        ru.sorewa.kino.ui.Profile.name(session.displayName.ifEmpty { session.lastLogin }),
                        modifier = Modifier.clip(CircleShape).clickable { nav.navigate("settings") },
                    )
                }
            }

            when {
                contItems == null && cont.error.isNotEmpty() -> item { ErrorView(cont.error) { refreshKey++ } }
                contItems == null -> item { HeroSkeleton() }
                contItems.isEmpty() -> item {
                    EmptyState(
                        icon = Icons.Rounded.Explore,
                        title = "Пока ничего не начато",
                        text = "Включите серию или фильм — здесь появится, где вы остановились.",
                        action = "Открыть каталог",
                        onAction = { nav.navigate("catalog") },
                    )
                }
                else -> {
                    val first = contItems.first()
                    item {
                        ContinueHero(
                            item = first,
                            onPlay = { playEpisode(context, first.episodeId, first.titleId) },
                            onInfo = { nav.openTitle(first.titleId) },
                        )
                    }
                    val rest = contItems.drop(1)
                    if (rest.isNotEmpty()) {
                        item { SectionHeader("Продолжить смотреть", count = cont.total.takeIf { it > 1 }) }
                        item {
                            Rail {
                                items(rest, key = { it.episodeId }) { it ->
                                    WideCard(
                                        episodeId = it.episodeId,
                                        titleId = it.titleId,
                                        hasArt = it.hasArt,
                                        name = it.title,
                                        sub = describeContinue(it),
                                        progress = progressOf(it),
                                        // Убранное из «Продолжить» уезжает, соседи плавно сдвигаются.
                                        modifier = Modifier.animateItem().width(264.dp),
                                        onLongClick = { menuFor = it },
                                        onClick = { playEpisode(context, it.episodeId, it.titleId) },
                                    )
                                }
                                if (cont.hasMore) item { androidx.compose.runtime.LaunchedEffect(rest.size) { cont.more() } }
                            }
                        }
                    }
                }
            }

            fresh.items?.takeIf { it.isNotEmpty() }?.let { list ->
                item { SectionHeader("Новые серии", count = fresh.total, action = "Все", onAction = { nav.navigate("mine?tab=1") }) }
                item {
                    Rail {
                        items(list, key = { it.titleId }) { t ->
                            PosterCard(
                                titleId = t.titleId, name = t.title, hasArt = t.hasArt,
                                sub = kindSubtitle(t.kind, t.year, null),
                                badge = "+${t.unwatched}",
                                modifier = Modifier.width(128.dp),
                                onClick = { nav.openTitle(t.titleId) },
                            )
                        }
                    }
                }
            }

            watchlist.items?.takeIf { it.isNotEmpty() }?.let { list ->
                item { SectionHeader("Буду смотреть", count = watchlist.total, action = "Все", onAction = { nav.navigate("mine?tab=2") }) }
                item {
                    Rail {
                        items(list, key = { it.id }) { t ->
                            PosterCard(
                                titleId = t.id, name = t.title, hasArt = t.hasArt,
                                sub = kindSubtitle(t.kind, t.year, t.species),
                                modifier = Modifier.width(128.dp),
                                onClick = { nav.openTitle(t) },
                            )
                        }
                    }
                }
            }

            listOf("Новые фильмы" to movies, "Новые сериалы" to series).forEach { (label, p) ->
                p.items?.takeIf { it.isNotEmpty() }?.let { list ->
                    item { SectionHeader(label, action = "Все", onAction = { nav.navigate("catalog") }) }
                    item {
                        Rail {
                            items(list, key = { it.id }) { t ->
                                PosterCard(
                                    titleId = t.id, name = t.title, hasArt = t.hasArt,
                                    sub = kindSubtitle(t.kind, t.year, t.species),
                                    modifier = Modifier.width(128.dp),
                                    onClick = { nav.openTitle(t) },
                                )
                            }
                        }
                    }
                }
            }

            item { Spacer(Modifier.height(24.dp + LocalBottomBarInset.current)) }
        }
    }
    // Лента уехала под строку состояния — под часами и значками стекло,
    // как у системных панелей: текст и постеры под ними не мешают читать.
    val under by remember { derivedStateOf { listState.firstVisibleItemIndex > 0 || listState.firstVisibleItemScrollOffset > 0 } }
    val stripAlpha by androidx.compose.animation.core.animateFloatAsState(
        if (under) 1f else 0f, ru.sorewa.kino.ui.theme.KinoMotion.effects(), label = "status",
    )
    if (stripAlpha > 0f) {
        Box(
            Modifier
                .fillMaxWidth()
                .windowInsetsTopHeight(WindowInsets.statusBars)
                .graphicsLayer { alpha = stripAlpha }
                .glass(glass, tint = Bg.copy(alpha = 0.5f), fallback = Bg.copy(alpha = 0.92f), rim = false),
        )
    }
    }

    menuFor?.let { it ->
        ModalBottomSheet(onDismissRequest = { menuFor = null }) {
            Column(Modifier.padding(bottom = 24.dp)) {
                Text(
                    splitTitle(it.title).first,
                    style = MaterialTheme.typography.titleMedium,
                    modifier = Modifier.padding(horizontal = 24.dp, vertical = 8.dp),
                )
                SheetRow(Icons.Rounded.PlayArrow, "Продолжить") {
                    menuFor = null; playEpisode(context, it.episodeId, it.titleId)
                }
                SheetRow(Icons.Rounded.Info, "О тайтле") { menuFor = null; nav.openTitle(it.titleId) }
                SheetRow(Icons.Rounded.VisibilityOff, "Убрать из «Продолжить»") {
                    menuFor = null
                    cont.drop { c -> c.titleId == it.titleId }
                    scope.launch {
                        // В очередь — только изменённое поле: отправка сперва
                        // читает серверные метки и кладёт hidden поверх, чужие
                        // метки не затираются (см. TitleScreen).
                        val ts = api.nowIso()
                        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                            stateStore.queueMarks(it.titleId, ru.sorewa.kino.data.MarksPatch(hidden = true), ts)
                        }
                        try {
                            val m = api.marks(it.titleId)
                            val next = m.copy(hidden = true)
                            kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                                stateStore.cacheMarks(it.titleId, next)
                            }
                            api.setMarks(it.titleId, next)
                            kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) { stateStore.ackMarks(it.titleId, ts) }
                        } catch (_: Exception) {
                            // Кэш для офлайн-показа: что знали, плюс hidden.
                            stateStore.cachedMarks(it.titleId)?.copy(hidden = true)?.let { s ->
                                kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                                    stateStore.cacheMarks(it.titleId, s)
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

fun progressOf(it: ContinueItem): Float =
    if (it.durationMs > 0) (it.positionMs.toFloat() / it.durationMs).coerceIn(0f, 1f) else 0f

@Composable
fun SheetRow(icon: ImageVector, label: String, tint: Color = TextMain, onClick: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, null, tint = tint)
        Spacer(Modifier.width(16.dp))
        Text(label, style = MaterialTheme.typography.bodyLarge, color = tint)
    }
}

/** Знак-билет и надпись kino — как в шапке витрины. */
@Composable
fun BrandMark(big: Boolean = false) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Box(
            Modifier
                .size(if (big) 56.dp else 32.dp)
                .background(Accent, RoundedCornerShape(if (big) 16.dp else 9.dp)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                androidx.compose.ui.res.painterResource(ru.sorewa.kino.R.drawable.ic_ticket),
                null,
                tint = ru.sorewa.kino.ui.theme.OnAccent,
                modifier = Modifier.size(if (big) 40.dp else 24.dp),
            )
        }
        Spacer(Modifier.width(if (big) 14.dp else 10.dp))
        Text(
            "kino",
            style = if (big) MaterialTheme.typography.displaySmall else MaterialTheme.typography.headlineSmall,
            color = TextMain,
        )
    }
}

/** Только знак-билет — для свёрнутого меню на ТВ. */
@Composable
fun BrandMarkSmall() {
    Box(
        Modifier.size(40.dp).background(Accent, RoundedCornerShape(11.dp)),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            androidx.compose.ui.res.painterResource(ru.sorewa.kino.R.drawable.ic_ticket),
            null,
            tint = ru.sorewa.kino.ui.theme.OnAccent,
            modifier = Modifier.size(28.dp),
        )
    }
}

/** Крупная карточка «Продолжить» — первое, что видно на главной. */
@Composable
private fun ContinueHero(item: ContinueItem, onPlay: () -> Unit, onInfo: () -> Unit) {
    val (name, original) = splitTitle(item.title)
    Column(Modifier.padding(horizontal = 20.dp, vertical = 12.dp)) {
        Box(
            Modifier
                .fillMaxWidth()
                .aspectRatio(16f / 10f)
                .cardClick(RoundedCornerShape(22.dp), onLongClick = onInfo, onClick = onPlay),
        ) {
            ShotImage(item.episodeId, item.titleId, item.hasArt, item.title, Modifier.fillMaxSize())
            Box(
                Modifier
                    .fillMaxSize()
                    .background(Brush.verticalGradient(0.3f to Color.Transparent, 1f to Color(0xF0000000))),
            )
            Column(
                Modifier
                    .align(Alignment.BottomStart)
                    .padding(18.dp),
            ) {
                Text("ПРОДОЛЖИТЬ", style = ru.sorewa.kino.ui.theme.Overline, color = Accent)
                Spacer(Modifier.height(4.dp))
                Text(
                    name,
                    style = MaterialTheme.typography.headlineSmall,
                    color = Color.White,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                if (original.isNotEmpty()) {
                    Text(original, style = MaterialTheme.typography.bodySmall, color = Color(0xB3FFFFFF), maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
                Spacer(Modifier.height(6.dp))
                Text(describeContinue(item), style = MaterialTheme.typography.bodyMedium, color = Color(0xE6FFFFFF))
                Spacer(Modifier.height(12.dp))
                ProgressLine(progressOf(item), height = 4.dp)
            }
        }
        Spacer(Modifier.height(12.dp))
        PrimaryButton(
            text = if (item.seasonNo > 0) "Смотреть ${item.seasonNo}×${item.episodeNo}" else "Продолжить",
            icon = Icons.Rounded.PlayArrow,
            onClick = onPlay,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun HeroSkeleton() {
    Column(Modifier.padding(horizontal = 20.dp, vertical = 12.dp)) {
        Shimmer(Modifier.fillMaxWidth().aspectRatio(16f / 10f).clip(RoundedCornerShape(22.dp)))
        Spacer(Modifier.height(12.dp))
        Shimmer(Modifier.fillMaxWidth().height(52.dp).clip(RoundedCornerShape(14.dp)))
        Spacer(Modifier.height(24.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            repeat(3) { Shimmer(Modifier.weight(1f).aspectRatio(2f / 3f).clip(RoundedCornerShape(12.dp))) }
        }
    }
}

