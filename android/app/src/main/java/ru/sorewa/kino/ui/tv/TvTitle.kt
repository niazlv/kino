package ru.sorewa.kino.ui.tv

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Bookmark
import androidx.compose.material.icons.rounded.BookmarkBorder
import androidx.compose.material.icons.rounded.DoneAll
import androidx.compose.material.icons.rounded.RemoveDone
import androidx.compose.material.icons.rounded.Notifications
import androidx.compose.material.icons.rounded.NotificationsNone
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Replay
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.navigation.NavHostController
import kotlinx.coroutines.launch
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.Marks
import ru.sorewa.kino.data.TitleDetail
import ru.sorewa.kino.ui.CenterSpinner
import ru.sorewa.kino.ui.ErrorView
import ru.sorewa.kino.ui.Pill
import ru.sorewa.kino.ui.PillRow
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.Rail
import ru.sorewa.kino.ui.SecondaryButton
import ru.sorewa.kino.ui.SectionHeader
import ru.sorewa.kino.ui.WideCard
import ru.sorewa.kino.ui.fmtMins
import ru.sorewa.kino.ui.phone.isMovie
import ru.sorewa.kino.ui.phone.nextUp
import ru.sorewa.kino.ui.phone.watchedSeasons
import ru.sorewa.kino.ui.phone.withSeasonWatched
import ru.sorewa.kino.ui.playEpisode
import ru.sorewa.kino.ui.plural
import ru.sorewa.kino.ui.speciesWord
import ru.sorewa.kino.ui.splitTitle
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextMain
import ru.sorewa.kino.ui.theme.TextFaint

/**
 * Тайтл на ТВ: афиша во весь экран, крупная кнопка «Смотреть» в фокусе
 * сразу, отметки рядом, ниже сезоны и лента серий с кадрами. Долгое
 * нажатие OK на серии — отметить просмотренной.
 */
@Composable
fun TvTitle(nav: NavHostController, titleId: Long) {
    val api = remember { KinoApp.instance.container.api }
    val state = remember { KinoApp.instance.container.state }
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    var detail by remember { mutableStateOf<TitleDetail?>(null) }
    var marks by remember { mutableStateOf<Marks?>(null) }
    var season by remember { mutableStateOf(-1) }
    var error by remember { mutableStateOf("") }
    val playFocus = remember { FocusRequester() }
    val listState = androidx.compose.foundation.lazy.rememberLazyListState()

    fun load() = scope.launch {
        try {
            val d = api.title(titleId)
            detail = ru.sorewa.kino.data.overlayEpisodes(
                d,
                kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) { state.pendingEpisodes() },
            )
            if (season < 0) season = nextUp(d)?.episode?.seasonNo ?: d.episodes.map { it.seasonNo }.minOrNull() ?: -1
            val m = api.marks(titleId)
            marks = m
            kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) { state.cacheMarks(titleId, m) }
        } catch (e: Exception) {
            if (detail == null) error = ru.sorewa.kino.data.describeApiError(e)
        }
    }
    LaunchedEffect(titleId) { load() }

    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        var first = true
        val observer = LifecycleEventObserver { _, e ->
            if (e == Lifecycle.Event.ON_RESUME) { if (!first) load(); first = false }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    fun setMarks(next: Marks) {
        val prev = marks ?: Marks()
        marks = next
        scope.launch {
            // В очередь до PUT — и только изменённые поля (см. TitleScreen).
            val ts = api.nowIso()
            kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                state.queueMarks(titleId, ru.sorewa.kino.data.marksDiff(prev, next), ts)
                state.cacheMarks(titleId, next)
            }
            try {
                val server = api.setMarks(titleId, next)
                marks = server
                kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                    state.ackMarks(titleId, ts)
                    state.cacheMarks(titleId, server)
                }
            } catch (_: Exception) { }
        }
    }

    val d = detail
    when {
        error.isNotEmpty() -> ErrorView(error) { error = ""; load() }
        d == null -> CenterSpinner()
        else -> {
            val t = d.title
            val movie = isMovie(d)
            val up = nextUp(d)
            val seasons = d.episodes.map { it.seasonNo }.distinct().sorted()
            val doneSeasons = watchedSeasons(d)
            val eps = d.episodes.filter { it.seasonNo == season }.sortedBy { it.episodeNo }
            LaunchedEffect(Unit) {
                androidx.compose.runtime.withFrameNanos { }
                try { playFocus.requestFocus() } catch (_: Exception) { }
            }

            ru.sorewa.kino.ui.MinimalScroll { Box(Modifier.fillMaxSize()) {
                // Фон — кадр той серии, что включится по «Смотреть»; нет кадра — постер.
                HeroBackdrop(Feature(t.id, t.title, t.hasArt, "", "", episodeId = up?.episode?.takeIf { it.hasArt }?.id ?: 0))
                LazyColumn(
                    Modifier.fillMaxSize(),
                    state = listState,
                    contentPadding = PaddingValues(start = 32.dp, end = 40.dp, top = 40.dp, bottom = 40.dp),
                ) {
                    item {
                        Column(Modifier.fillMaxWidth(0.6f)) {
                            val (name, original) = splitTitle(t.title)
                            Text(name, style = MaterialTheme.typography.displaySmall, color = Color.White, maxLines = 2, overflow = TextOverflow.Ellipsis)
                            if (original.isNotEmpty()) Text(original, style = MaterialTheme.typography.titleMedium, color = Color(0xB3FFFFFF))
                            Spacer(Modifier.height(10.dp))
                            val nSeasons = seasons.count { it > 0 }
                            val meta = listOfNotNull(
                                speciesWord(t.species, t.kind).takeIf { it.isNotEmpty() },
                                t.year.takeIf { it > 0 }?.toString(),
                                if (!movie && nSeasons > 0) "$nSeasons ${plural(nSeasons, "сезон", "сезона", "сезонов")}" else null,
                                if (movie) d.episodes.firstOrNull()?.durationSec?.takeIf { it > 0 }?.let { fmtMins(it / 60) } else null,
                                t.genres.take(3).joinToString(", ").takeIf { it.isNotEmpty() },
                            )
                            Text(meta.joinToString("  ·  "), style = MaterialTheme.typography.titleMedium, color = TextDim)
                            t.plot?.takeIf { it.isNotBlank() }?.let {
                                Spacer(Modifier.height(14.dp))
                                Text(it, style = MaterialTheme.typography.bodyLarge, color = Color(0xFFD5DAE2), maxLines = 3, overflow = TextOverflow.Ellipsis)
                            }
                        }
                    }
                    item {
                        // Вернулись к кнопкам с серий — шапка (название, описание)
                        // не фокусируется сама, поэтому прокручиваем к ней явно.
                        Column(Modifier.onFocusChanged { f ->
                            if (f.hasFocus && listState.firstVisibleItemIndex + listState.firstVisibleItemScrollOffset > 0) {
                                scope.launch { listState.animateScrollToItem(0) }
                            }
                        }) {
                            Spacer(Modifier.height(24.dp))
                            val m = marks ?: Marks()
                            Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                                if (up != null) {
                                    PrimaryButton(
                                        up.label,
                                        Modifier.focusRequester(playFocus),
                                        icon = if (up.label == "Пересмотреть") Icons.Rounded.Replay else Icons.Rounded.PlayArrow,
                                    ) { playEpisode(context, up.episode.id, titleId) }
                                }
                                SecondaryButton(
                                    if (m.watchlist) "В списке" else "Буду смотреть",
                                    icon = if (m.watchlist) Icons.Rounded.Bookmark else Icons.Rounded.BookmarkBorder,
                                ) { if (marks != null) setMarks(m.copy(watchlist = !m.watchlist)) }
                                if (!movie) {
                                    SecondaryButton(
                                        if (m.following) "Слежу" else "Следить",
                                        icon = if (m.following) Icons.Rounded.Notifications else Icons.Rounded.NotificationsNone,
                                    ) { if (marks != null) setMarks(m.copy(following = !m.following)) }
                                }
                                SecondaryButton(
                                    if (m.hidden) "Вернуть" else "Скрыть",
                                    icon = if (m.hidden) Icons.Rounded.Visibility else Icons.Rounded.VisibilityOff,
                                ) { if (marks != null) setMarks(m.copy(hidden = !m.hidden)) }
                            }
                            if (up != null && up.sub.isNotEmpty()) {
                                Spacer(Modifier.height(8.dp))
                                Text(up.sub, style = MaterialTheme.typography.bodyMedium, color = TextFaint)
                            }
                        }
                    }

                    if (!movie && eps.isNotEmpty()) {
                        item { Spacer(Modifier.height(28.dp)) }
                        item {
                            val done = eps.count { it.status == "completed" }
                            val all = done == eps.size
                            fun markSeason(watched: Boolean) {
                                val s = season
                                detail = withSeasonWatched(d, s, watched)
                                scope.launch {
                                    // Сезон — по сериям в очередь, как на телефоне.
                                    val ts = api.nowIso()
                                    kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                                        d.episodes.filter { it.seasonNo == s && it.id > 0 }.forEach {
                                            state.queueWatched(it.id, watched, it.durationSec * 1000, ts)
                                        }
                                    }
                                    try {
                                        api.setSeasonWatched(titleId, s, watched)
                                        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                                            d.episodes.filter { it.seasonNo == s && it.id > 0 }.forEach {
                                                state.ackEpisode(it.id, ts)
                                            }
                                        }
                                    } catch (_: Exception) { }
                                    load()
                                }
                            }
                            // Отметка сезона — таблеткой в строке заголовка «Серии», над
                            // сезонами, как на телефоне. Под переключателем сезонов «вниз»
                            // проскакивал её прямо в ленту; здесь путь пульта прямой:
                            // «Смотреть» → «Отметить все» → сезоны → серии. Подпись
                            // меняется на месте — фокус не теряется.
                            Row(
                                Modifier.padding(bottom = 6.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Text("Серии", style = MaterialTheme.typography.titleLarge, color = TextMain)
                                Spacer(Modifier.width(20.dp))
                                // Номер сезона — в подписи: «Отметить все» читалось как
                                // «весь сериал».
                                val named = seasons.size > 1 && season > 0
                                Pill(
                                    when {
                                        all && named -> "Снять отметки сезона $season"
                                        all -> "Снять отметки"
                                        named -> "Отметить сезон $season"
                                        else -> "Отметить все серии"
                                    },
                                    selected = false,
                                    leading = if (all) Icons.Rounded.RemoveDone else Icons.Rounded.DoneAll,
                                ) { markSeason(!all) }
                                Spacer(Modifier.width(20.dp))
                                Text(
                                    when {
                                        all && named -> "сезон $season просмотрен"
                                        all -> "всё просмотрено"
                                        else -> "просмотрено $done из ${eps.size}"
                                    },
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = if (all) Accent else TextFaint,
                                )
                                Text(
                                    " · долгое нажатие OK — отметить серию",
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = TextFaint,
                                )
                            }
                        }
                        if (seasons.size > 1) {
                            item {
                                PillRow(
                                    seasons.map { it to (if (it > 0) "Сезон $it" else "Доп.") },
                                    season, { season = it },
                                    contentPadding = PaddingValues(horizontal = 4.dp, vertical = 6.dp),
                                    leadingOf = { if (it in doneSeasons) Icons.Rounded.DoneAll else null },
                                )
                            }
                        }
                        // Свой ключ на сезон: лента серий заводится заново — прокрутка
                        // и память фокуса не переезжают из прошлого сезона (иначе после
                        // 16-й серии 1-го сезона «Сезон 2» открывался тоже на 16-й).
                        item(key = "episodes-$season") {
                            // Лента открывается на серии, с которой продолжать, и пульт
                            // встаёт на неё же, а не на 1-ю серию, откуда до 10-й девять
                            // нажатий вправо. В другом сезоне — с начала.
                            val startIdx = eps.indexOfFirst { it.id == up?.episode?.id }.coerceAtLeast(0)
                            val railState = remember { androidx.compose.foundation.lazy.LazyListState(firstVisibleItemIndex = startIdx) }
                            val startFocus = remember { FocusRequester() }
                            val startId = eps.getOrNull(startIdx)?.id
                            Rail(state = railState, focusStart = startFocus) {
                                items(eps, key = { it.id }) { ep ->
                                    val watched = ep.status == "completed"
                                    val progress = if (ep.durationSec > 0 && ep.positionMs > 0)
                                        (ep.positionMs.toFloat() / (ep.durationSec * 1000)).coerceIn(0f, 1f) else 0f
                                    val sub = listOfNotNull(
                                        ep.title?.takeIf { it.isNotBlank() },
                                        if (watched) "просмотрено" else (ep.durationSec / 60).takeIf { it > 0 }?.let { "$it мин" },
                                    ).joinToString(" · ")
                                    WideCard(
                                        episodeId = ep.id, titleId = titleId, hasArt = t.hasArt,
                                        name = "${ep.episodeNo} серия",
                                        sub = sub,
                                        progress = if (watched) 1f else progress,
                                        tryShot = ep.hasArt,
                                        showPlay = false,
                                        modifier = Modifier
                                            .width(280.dp)
                                            .then(if (ep.id == startId) Modifier.focusRequester(startFocus) else Modifier),
                                        onLongClick = {
                                            scope.launch {
                                                // Галочка — в очередь до PUT (см. TitleScreen).
                                                val ts = api.nowIso()
                                                kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                                                    state.queueWatched(ep.id, !watched, ep.durationSec * 1000, ts)
                                                }
                                                try {
                                                    api.setWatched(ep.id, !watched)
                                                    kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                                                        state.ackEpisode(ep.id, ts)
                                                    }
                                                } catch (_: Exception) { }
                                                load()
                                            }
                                        },
                                        onClick = { playEpisode(context, ep.id, titleId) },
                                    )
                                }
                            }
                        }
                    }
                }
            } }
        }
    }
}

