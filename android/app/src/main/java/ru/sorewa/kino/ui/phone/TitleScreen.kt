package ru.sorewa.kino.ui.phone

import android.os.Build
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
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
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Bookmark
import androidx.compose.material.icons.rounded.BookmarkBorder
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Done
import androidx.compose.material.icons.rounded.DownloadForOffline
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.DoneAll
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.MoreVert
import androidx.compose.material.icons.rounded.Notifications
import androidx.compose.material.icons.rounded.NotificationsNone
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.RemoveDone
import androidx.compose.material.icons.rounded.Replay
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.ViewAgenda
import androidx.compose.material.icons.automirrored.rounded.ViewList
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.blur
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import ru.sorewa.kino.data.LocalLibrary
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.Episode
import ru.sorewa.kino.data.Marks
import ru.sorewa.kino.data.TitleDetail
import ru.sorewa.kino.ui.CenterSpinner
import ru.sorewa.kino.ui.ErrorView
import ru.sorewa.kino.ui.IconAction
import ru.sorewa.kino.ui.PillRow
import ru.sorewa.kino.ui.PosterImage
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.ProgressLine
import ru.sorewa.kino.ui.ShotImage
import ru.sorewa.kino.ui.cardClick
import ru.sorewa.kino.ui.fmtMins
import ru.sorewa.kino.ui.playEpisode
import ru.sorewa.kino.ui.speciesWord
import ru.sorewa.kino.ui.splitTitle
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Bg
import ru.sorewa.kino.ui.theme.OnAccent
import ru.sorewa.kino.ui.theme.Stroke
import ru.sorewa.kino.ui.theme.Surface1
import ru.sorewa.kino.ui.theme.Surface2
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain

/**
 * Что включать по большой кнопке: начатая серия, иначе следующая за
 * последней досмотренной, иначе первая. Для фильма — единственная.
 */
data class NextUp(val episode: Episode, val label: String, val sub: String)

fun nextUp(d: TitleDetail): NextUp? {
    val eps = d.episodes.sortedWith(compareBy({ it.seasonNo }, { it.episodeNo }))
    if (eps.isEmpty()) return null
    val movie = d.title.kind == "movie" || (eps.size == 1 && eps[0].seasonNo <= 1)
    fun left(e: Episode): String {
        val total = e.durationSec * 1000
        return if (total > 0 && e.positionMs > 0) "осталось ${fmtMins((total - e.positionMs) / 60_000)}" else ""
    }
    fun se(e: Episode) = if (e.seasonNo > 0) "${e.seasonNo}×${e.episodeNo}" else "серия ${e.episodeNo}"

    val started = eps.firstOrNull { it.positionMs > 0 && it.status != "completed" }
    if (started != null) {
        return NextUp(started, if (movie) "Продолжить" else "Продолжить ${se(started)}", left(started))
    }
    val lastDone = eps.indexOfLast { it.status == "completed" }
    return when {
        lastDone < 0 -> NextUp(eps.first(), if (movie) "Смотреть" else "Смотреть ${se(eps.first())}", "")
        lastDone < eps.lastIndex -> {
            val n = eps[lastDone + 1]
            NextUp(n, "Смотреть ${se(n)}", "следующая серия")
        }
        else -> NextUp(eps.first(), "Пересмотреть", "всё просмотрено")
    }
}

fun isMovie(d: TitleDetail) = d.title.kind == "movie" ||
    (d.episodes.size == 1 && d.episodes[0].seasonNo <= 1 && d.episodes[0].episodeNo <= 1)

/**
 * Сезоны, досмотренные целиком: в переключателе сезонов у них галочка —
 * как ✓ на вкладках сезонов в витрине.
 */
fun watchedSeasons(d: TitleDetail): Set<Int> =
    d.episodes.groupBy { it.seasonNo }
        .filterValues { eps -> eps.all { it.status == "completed" } }
        .keys

/**
 * Отметка сезона применяется к экрану сразу, до ответа сервера, как на
 * витрине: задержка после нажатия читается как «кнопка не работает».
 * Снятая отметка обнуляет и позицию — сервер делает так же.
 */
fun withSeasonWatched(d: TitleDetail, season: Int, watched: Boolean) = d.copy(
    episodes = d.episodes.map {
        if (it.seasonNo != season) it
        else it.copy(status = if (watched) "completed" else null, positionMs = 0)
    },
)

/**
 * Страница тайтла: размытый постер фоном, постер и название, большая
 * кнопка «Смотреть/Продолжить», отметки иконками, описание, сезоны и
 * серии с кадрами. Серия открывает нативный плеер.
 */
@Composable
fun TitleScreen(
    nav: NavHostController,
    titleId: Long,
    /** Офлайн-витрина: сеть не трогаем, карточка — сохранённая. */
    offlineOnly: Boolean = false,
    /** Скачанное без тайтла в каталоге: страница собирается по названию папки. */
    localName: String? = null,
) {
    val container = remember { KinoApp.instance.container }
    val api = container.api
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    var detail by remember { mutableStateOf<TitleDetail?>(null) }
    var marks by remember { mutableStateOf<Marks?>(null) }
    var season by remember { mutableStateOf(-1) }
    var error by remember { mutableStateOf("") }
    var voices by remember { mutableStateOf<VoiceSummary?>(null) }
    var compact by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(false) }
    // Диалог скачивания: серия (id>0) или сезон (null + номер сезона).
    var downloadFor by remember { mutableStateOf<Pair<Episode?, Int>?>(null) }
    // Без сети: карточка из сохранённой, играют только скачанные серии.
    var offlineMode by remember { mutableStateOf(offlineOnly || localName != null) }
    // Скачанные серии тайтла по «сезон×серия».
    var local by remember { mutableStateOf<Map<Pair<Int, Int>, LocalLibrary.Entry>>(emptyMap()) }
    var removeLocal by remember { mutableStateOf<LocalLibrary.Entry?>(null) }

    suspend fun readLocal() {
        local = withContext(Dispatchers.IO) {
            container.library.list()
                .filter { if (localName != null) it.titleId == 0L && it.title == localName else titleId > 0 && it.titleId == titleId }
                .associateBy { it.season to it.episode }
        }
    }

    /** Метки для показа: ждущие > сервера > кэша (resolveMarks), чтение — вне главного потока. */
    suspend fun localMarks(server: Marks?): Marks? = withContext(Dispatchers.IO) {
        ru.sorewa.kino.data.resolveMarks(
            server,
            container.state.cachedMarks(titleId),
            container.state.pendingTitle(titleId),
        )
    }

    /** Сохранённая карточка (или собранная из файлов). false — показать нечего. */
    suspend fun fromCache(): Boolean {
        val raw = withContext(Dispatchers.IO) { container.offline.detailFor(titleId, localName) } ?: return false
        // Ждущий стейт поверх карточки: отмеченное в самолёте видно сразу.
        val d = withContext(Dispatchers.IO) {
            ru.sorewa.kino.data.overlayEpisodes(raw, container.state.pendingEpisodes())
        }
        detail = d
        offlineMode = true
        error = ""
        if (marks == null) marks = localMarks(null)
        container.offline.saved(titleId)?.voices?.takeIf { it.isNotEmpty() }?.let { voices = VoiceSummary(it) }
        if (season < 0) {
            val shown = withLocal(d, local, true)
            season = nextUp(downloadedOnly(shown, local))?.episode?.seasonNo
                ?: d.episodes.map { it.seasonNo }.distinct().minOrNull() ?: -1
        }
        return true
    }

    fun load() = scope.launch {
        readLocal()
        if (offlineOnly || localName != null) {
            if (!fromCache()) error = "Карточка не сохранена, а сети нет"
            return@launch
        }
        // Скачанное есть, а сети нет — сразу сохранённая карточка. Сеть есть,
        // но сервер молчит дольше 4 с (самолёт, плохой вайфай) — тоже она,
        // а ответ сервера, если дойдёт, заменит её.
        val canCache = local.isNotEmpty() || container.offline.saved(titleId) != null
        if (detail == null && canCache && !ru.sorewa.kino.util.hasNetwork()) {
            fromCache()
            return@launch
        }
        val fallback = if (detail == null && canCache) launch {
            kotlinx.coroutines.delay(4_000)
            if (detail == null) fromCache()
        } else null
        try {
            val d = api.title(titleId)
            fallback?.cancel()
            // Ждущий стейт (сорвавшийся PUT, офлайн-отметки) поверх сервера:
            // последним сделанное человеком не должно спрятаться.
            detail = ru.sorewa.kino.data.overlayEpisodes(
                d,
                withContext(Dispatchers.IO) { container.state.pendingEpisodes() },
            )
            error = ""
            offlineMode = false
            if (season < 0) season = nextUp(withLocal(d, local, false))?.episode?.seasonNo
                ?: d.episodes.map { it.seasonNo }.distinct().minOrNull() ?: -1
            // Скачано хоть что-то — карточка нужна и без сети: сохраняем свежую.
            if (local.isNotEmpty()) withContext(Dispatchers.IO) { container.offline.save(titleId, d) }
            val m = api.marks(titleId)
            marks = m
            withContext(Dispatchers.IO) { container.state.cacheMarks(titleId, m) }
        } catch (e: Exception) {
            fallback?.cancel()
            val noNet = e is ru.sorewa.kino.data.ApiError && e.offline
            when {
                detail == null -> if (!fromCache()) error = ru.sorewa.kino.data.describeApiError(e)
                noNet -> {
                    offlineMode = true
                    if (marks == null) marks = localMarks(null)
                }
            }
        }
    }

    LaunchedEffect(titleId, localName) { load() }

    // Опись изменилась (позиция из плеера, докачалась серия, стёрли файл) —
    // скачанные серии на странице перечитываются без похода на сервер.
    val libChanges by container.library.changes.collectAsStateWithLifecycle()
    LaunchedEffect(libChanges) { if (detail != null) readLocal() }

    // Качалка: что из этого тайтла качается и ждёт — на кнопке «Скачать»,
    // полосой под кнопками и у серий. Очередь лежит в prefs, перечитывается
    // при каждом изменении статуса службы.
    val dl by ru.sorewa.kino.downloader.DownloadService.status.collectAsStateWithLifecycle()
    val dlHere = remember(dl, titleId, detail?.episodes) {
        val ctx = KinoApp.instance
        titleDownload(
            dl,
            queue = ru.sorewa.kino.downloader.Downloader.loadQueue(ctx),
            paused = ru.sorewa.kino.downloader.Downloader.pausedJob(ctx),
            failed = ru.sorewa.kino.downloader.Downloader.failedJob(ctx),
            titleId = titleId,
            episodes = detail?.episodes.orEmpty(),
        )
    }
    val openDownloads = { nav.navigate("downloads") { launchSingleTop = true } }

    // Озвучки — по серии, что включится по большой кнопке: общий список
    // студий, без похода в плеер.
    val upId = detail?.takeIf { !offlineMode }?.let { nextUp(it)?.episode?.id }
    LaunchedEffect(upId) {
        val id = upId ?: return@LaunchedEffect
        try {
            val list = api.voices(id)
            val names = list.voices.map { it.title }.filter { it.isNotBlank() }.distinct()
            voices = VoiceSummary(names)
            if (local.isNotEmpty() && names.isNotEmpty()) {
                detail?.let { d -> withContext(Dispatchers.IO) { container.offline.save(titleId, d, names) } }
            }
        } catch (_: Exception) { }
    }

    // Вернулись из плеера — серия могла досмотреться: перечитываем. Позиции
    // скачанных серий сперва уезжают на сервер (плеер файла пишет их локально).
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        var first = true
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                if (!first) scope.launch {
                    // Очередь стейта (офлайн-отметки, сорвавшиеся PUT) — при
                    // каждом возврате; пустая выходит мгновенно.
                    try { container.sync.syncPendingState() } catch (_: Exception) { }
                    if (!offlineMode && local.isNotEmpty()) {
                        try { container.sync.syncLocalProgress() } catch (_: Exception) { }
                    }
                    load()
                }
                first = false
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    fun setMarks(next: Marks) {
        val prev = marks ?: Marks()
        marks = next
        scope.launch {
            // Сначала в очередь и кэш: сорвавшийся PUT не потеряет переключение,
            // офлайн-витрина покажет его из кэша. В очередь — только изменённые
            // поля, не снимок: отправка сперва читает серверные метки и кладёт
            // своё поле поверх, чтобы не затереть чужое. Сервер ответит —
            // вычистим своё.
            val ts = api.nowIso()
            withContext(Dispatchers.IO) {
                container.state.queueMarks(titleId, ru.sorewa.kino.data.marksDiff(prev, next), ts)
                container.state.cacheMarks(titleId, next)
            }
            try {
                val server = api.setMarks(titleId, next)
                marks = server
                withContext(Dispatchers.IO) {
                    container.state.ackMarks(titleId, ts)
                    container.state.cacheMarks(titleId, server)
                }
            } catch (_: Exception) {
                // Остаётся в очереди — уедет при следующем синке.
            }
        }
    }

    fun toggleWatched(ep: Episode, watched: Boolean) = scope.launch {
        val entry = local[ep.seasonNo to ep.episodeNo]
        if (offlineMode) {
            if (entry == null) {
                // Без сети и без описи: у серии из сохранённой карточки id
                // настоящий (у собранной из файлов — отрицательный, сервер его
                // не знает) — в очередь стейта, уедет при первой сети.
                if (ep.id > 0) {
                    withContext(Dispatchers.IO) {
                        container.state.queueWatched(ep.id, watched, ep.durationSec * 1000)
                    }
                    load()
                }
                return@launch
            }
            // Скачанное — в опись: уедет на сервер при первой сети.
            withContext(Dispatchers.IO) {
                if (watched) {
                    container.library.progress(entry.id, entry.durationMs, entry.durationMs, watched = true)
                } else {
                    container.library.progress(entry.id, 0, entry.durationMs, watched = false)
                    container.library.setWatched(entry.id, false)
                }
            }
            load()
            return@launch
        }
        try {
            // В очередь до PUT: сеть упала в середине — галочка не потеряется.
            val ts = api.nowIso()
            withContext(Dispatchers.IO) {
                container.state.queueWatched(ep.id, watched, ep.durationSec * 1000, ts)
            }
            api.setWatched(ep.id, watched)
            withContext(Dispatchers.IO) { container.state.ackEpisode(ep.id, ts) }
            // Скачанная копия — туда же: иначе её старая позиция вернулась бы на экран.
            if (entry != null) withContext(Dispatchers.IO) {
                container.library.setWatched(entry.id, watched)
                container.library.synced(entry.id)
            }
            load()
        } catch (_: Exception) {
            // Осталась в очереди — уедет при следующем синке.
        }
    }

    /** Скачанная серия — с телефона (и без сети), иначе — поток. */
    fun play(ep: Episode) {
        val entry = local[ep.seasonNo to ep.episodeNo]
        when {
            entry != null -> playLocal(entry)
            offlineMode -> android.widget.Toast.makeText(context, "Эта серия не скачана — без сети её не посмотреть", android.widget.Toast.LENGTH_SHORT).show()
            else -> playEpisode(context, ep.id, titleId)
        }
    }

    val raw = detail
    // Позиции скачанных серий: без сети — свои, с сетью — ещё не доехавшие.
    val d = remember(raw, local, offlineMode) { raw?.let { withLocal(it, local, offlineMode) } }
    val listStateHolder = remember { mutableStateOf<androidx.compose.foundation.lazy.LazyListState?>(null) }
    Box(Modifier.fillMaxSize()) {
        when {
            error.isNotEmpty() -> Box(Modifier.statusBarsPadding().padding(top = 48.dp)) { ErrorView(error) { error = ""; load() } }
            d == null -> CenterSpinner()
            else -> {
                val movie = isMovie(d)
                // Без сети большая кнопка включает только скачанное.
                val up = if (offlineMode) nextUp(downloadedOnly(d, local)) else nextUp(d)
                val seasons = d.episodes.map { it.seasonNo }.distinct().sorted()
                val eps = d.episodes.filter { it.seasonNo == season }.sortedBy { it.episodeNo }
                val localSeasons = local.keys.map { it.first }.toSet()
                val listState = rememberLazyListState()
                listStateHolder.value = listState
                LazyColumn(Modifier.fillMaxSize(), state = listState) {
                    item { TitleHeader(d, voices) }
                    item {
                        Column(Modifier.padding(horizontal = 20.dp)) {
                            if (up != null) {
                                PrimaryButton(
                                    text = up.label,
                                    icon = if (up.label == "Пересмотреть") Icons.Rounded.Replay else Icons.Rounded.PlayArrow,
                                    onClick = { play(up.episode) },
                                    modifier = Modifier.fillMaxWidth(),
                                )
                                if (up.sub.isNotEmpty()) {
                                    Text(
                                        up.sub,
                                        style = MaterialTheme.typography.bodySmall,
                                        color = TextDim,
                                        textAlign = TextAlign.Center,
                                        modifier = Modifier.fillMaxWidth().padding(top = 6.dp),
                                    )
                                }
                            }
                            if (local.isNotEmpty() || offlineMode) {
                                LocalNote(offlineMode, local.size, d.episodes.size, movie)
                            }
                            if (!offlineMode || marks != null) {
                                Spacer(Modifier.height(10.dp))
                                // Офлайн строка отметок жива (по кэшу и очереди),
                                // скачивание без сети не предлагается.
                                ActionsRow(
                                    marks = marks,
                                    movie = movie,
                                    download = if (offlineMode) null else dlHere,
                                    percent = if (dlHere.running) dl.percent else 0,
                                    onMarks = ::setMarks,
                                    onDownload = {
                                        // Фильм уже качается — шторке сказать нечего,
                                        // а пауза и отмена живут в «Загрузках».
                                        if (movie && dlHere.busy) openDownloads()
                                        else {
                                            val ep = if (movie) d.episodes.firstOrNull() else up?.episode
                                            if (ep != null) downloadFor = ep to ep.seasonNo
                                        }
                                    },
                                )
                                TitleDownloadStrip(
                                    state = dlHere,
                                    dl = dl,
                                    movie = movie,
                                    onOpen = openDownloads,
                                    onRetry = {
                                        val i = android.content.Intent(KinoApp.instance, ru.sorewa.kino.downloader.DownloadService::class.java)
                                            .setAction(ru.sorewa.kino.downloader.DownloadService.ACTION_RETRY)
                                        if (Build.VERSION.SDK_INT >= 26) KinoApp.instance.startForegroundService(i)
                                        else KinoApp.instance.startService(i)
                                    },
                                )
                            }
                            voices?.let { VoicesBlock(it) }
                            Plot(d)
                        }
                    }

                    if (!movie && eps.isNotEmpty()) {
                        item {
                            SeasonBar(
                                seasons = seasons,
                                watched = watchedSeasons(d),
                                downloaded = localSeasons,
                                season = season,
                                eps = eps,
                                offline = offlineMode,
                                onSeason = { season = it },
                                onDownloadSeason = { downloadFor = null to season },
                                compact = compact,
                                onCompact = { compact = !compact },
                                onMarkSeason = { watched ->
                                    val s = season
                                    detail = withSeasonWatched(d, s, watched)
                                    scope.launch {
                                        // Сезон — по сериям в очередь (сервер бы
                                        // пометил каждую): сорвавшийся PUT не
                                        // потеряет ни одной галочки.
                                        val ts = api.nowIso()
                                        withContext(Dispatchers.IO) {
                                            d.episodes.filter { it.seasonNo == s && it.id > 0 }.forEach {
                                                container.state.queueWatched(it.id, watched, it.durationSec * 1000, ts)
                                            }
                                        }
                                        try {
                                            api.setSeasonWatched(titleId, s, watched)
                                            withContext(Dispatchers.IO) {
                                                d.episodes.filter { it.seasonNo == s && it.id > 0 }.forEach {
                                                    container.state.ackEpisode(it.id, ts)
                                                }
                                            }
                                        } catch (_: Exception) { }
                                        load()
                                    }
                                },
                            )
                        }
                        items(eps, key = { it.id }) { ep ->
                            val entry = local[ep.seasonNo to ep.episodeNo]
                            val key = ep.seasonNo to ep.episodeNo
                            EpisodeRow(
                                dlCurrent = entry == null && dlHere.current == key,
                                dlWaiting = entry == null && key in dlHere.waiting,
                                dlPercent = if (dlHere.current == key) dl.percent else 0,
                                ep = ep,
                                titleId = titleId,
                                hasTitleArt = d.title.hasArt,
                                titleName = d.title.title,
                                current = ep.id == up?.episode?.id,
                                compact = compact,
                                local = entry,
                                offline = offlineMode,
                                onPlay = { play(ep) },
                                onStream = { playEpisode(context, ep.id, titleId) },
                                onDownload = { downloadFor = ep to ep.seasonNo },
                                onRemoveLocal = { removeLocal = entry },
                                onToggleWatched = { toggleWatched(ep, it) },
                            )
                        }
                    } else if (movie && local.isNotEmpty()) {
                        // Фильм: скачанный файл — отдельной строкой, чтобы было что стереть.
                        val entry = local.values.first()
                        item {
                            Spacer(Modifier.height(8.dp))
                            LocalFileRow(entry, onRemove = { removeLocal = entry })
                        }
                    }
                    item { Spacer(Modifier.height(32.dp)) }
                }
            }
        }

        // Пролистали обложку — под строкой состояния появляется подложка,
        // чтобы часы и значки не легли на текст.
        val scrolled = listStateHolder.value?.let { it.firstVisibleItemIndex > 0 || it.firstVisibleItemScrollOffset > 120 } ?: false
        val scrimAlpha by androidx.compose.animation.core.animateFloatAsState(if (scrolled) 1f else 0f, label = "scrim")
        Box(
            Modifier
                .fillMaxWidth()
                .alpha(scrimAlpha)
                .background(Brush.verticalGradient(listOf(Bg, Bg.copy(alpha = 0.85f), Color.Transparent)))
                .statusBarsPadding()
                .height(56.dp),
        )

        // Кнопка «назад» поверх обложки.
        IconButton(
            onClick = { nav.popBackStack() },
            modifier = Modifier
                .statusBarsPadding()
                .padding(8.dp)
                .size(44.dp)
                .background(Color(0x66000000), CircleShape),
        ) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Назад", tint = Color.White) }
    }

    // Диалог скачивания: для серии — манифест по её id, для сезона — по
    // первой серии сезона (качества у тайтла общие).
    downloadFor?.let { (ep, seasonNum) ->
        val dd = detail
        if (dd != null) {
            val manifestEp = ep ?: dd.episodes.firstOrNull { it.seasonNo == seasonNum }
            if (manifestEp != null) {
                val seasonsCount = dd.episodes.map { it.seasonNo }.distinct().size
                DownloadDialog(
                    titleId = titleId,
                    titleName = dd.title.title,
                    seasonNo = seasonNum,
                    episodeNo = manifestEp.episodeNo,
                    episodeId = manifestEp.id,
                    seasonsCount = seasonsCount,
                    episodes = dd.episodes,
                    downloaded = local.keys,
                    wholeSeason = ep == null,
                    prefs = dd.prefs,
                    onDismiss = { downloadFor = null },
                )
            } else downloadFor = null
        }
    }

    removeLocal?.let { e ->
        androidx.compose.material3.AlertDialog(
            onDismissRequest = { removeLocal = null },
            title = { Text("Стереть ${entryLabel(e).lowercase()} с телефона?") },
            text = { Text("Файл удаляется насовсем (${fmtBytes(e.size)}). Онлайн серия останется.") },
            confirmButton = {
                androidx.compose.material3.Button(onClick = {
                    scope.launch {
                        val ok = withContext(Dispatchers.IO) { container.library.remove(e.id) }
                        if (!ok) android.widget.Toast.makeText(context, "Файл не удалось стереть", android.widget.Toast.LENGTH_SHORT).show()
                        load()
                    }
                    removeLocal = null
                }) { Text("Стереть") }
            },
            dismissButton = { androidx.compose.material3.TextButton(onClick = { removeLocal = null }) { Text("Отмена") } },
        )
    }
}

/**
 * Скачанные серии поверх карточки. Без сети верим описи (там позиция,
 * набранная в самолёте), с сетью — только ещё не отправленному серверу.
 */
fun withLocal(d: TitleDetail, local: Map<Pair<Int, Int>, LocalLibrary.Entry>, offline: Boolean): TitleDetail {
    if (local.isEmpty()) return d
    return d.copy(
        episodes = d.episodes.map { ep ->
            val e = local[ep.seasonNo to ep.episodeNo] ?: return@map ep
            val dur = if (ep.durationSec > 0) ep.durationSec else e.durationMs / 1000
            val mine = e.dirty || (offline && (e.watched || e.positionMs > 0))
            if (!mine) ep.copy(durationSec = dur)
            else ep.copy(
                durationSec = dur,
                positionMs = if (e.watched) 0 else e.positionMs,
                status = when {
                    e.watched -> "completed"
                    e.positionMs > 0 -> "in_progress"
                    else -> null
                },
            )
        },
    )
}

/** Только скачанные серии — для большой кнопки без сети. */
fun downloadedOnly(d: TitleDetail, local: Map<Pair<Int, Int>, LocalLibrary.Entry>): TitleDetail =
    d.copy(episodes = d.episodes.filter { (it.seasonNo to it.episodeNo) in local })

/** Строка под большой кнопкой: что из тайтла лежит на телефоне. */
@Composable
private fun LocalNote(offline: Boolean, have: Int, total: Int, movie: Boolean) {
    Row(
        Modifier.fillMaxWidth().padding(top = 10.dp),
        horizontalArrangement = Arrangement.Center,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            if (offline) Icons.Rounded.CloudOff else Icons.Rounded.DownloadForOffline,
            null,
            tint = if (offline) TextDim else Accent,
            modifier = Modifier.size(16.dp),
        )
        Spacer(Modifier.width(6.dp))
        Text(
            when {
                movie && have > 0 -> if (offline) "Без сети · фильм на телефоне" else "Фильм на телефоне — включится без интернета"
                have == 0 -> "Без сети · скачанных серий нет"
                offline -> "Без сети · на телефоне $have из $total ${ru.sorewa.kino.ui.plural(total, "серии", "серий", "серий")}"
                else -> "На телефоне: $have ${ru.sorewa.kino.ui.plural(have, "серия", "серии", "серий")} · включаются без интернета"
            },
            style = MaterialTheme.typography.bodySmall,
            color = TextDim,
        )
    }
}

/** Скачанный фильм: размер и «стереть». */
@Composable
private fun LocalFileRow(e: LocalLibrary.Entry, onRemove: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().padding(start = 20.dp, end = 8.dp, top = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Rounded.DownloadForOffline, null, tint = Accent, modifier = Modifier.size(20.dp))
        Spacer(Modifier.width(10.dp))
        Text("На телефоне · ${fmtBytes(e.size)}", style = MaterialTheme.typography.bodyMedium, color = TextDim, modifier = Modifier.weight(1f))
        androidx.compose.material3.TextButton(onClick = onRemove) {
            Text("Стереть", color = ru.sorewa.kino.ui.theme.Danger)
        }
    }
}

/**
 * Обложка, как у Kino.Watch: размытый постер фоном, постер слева, справа
 * название, оригинал и сводка (вид, год, статус, сезоны или длительность),
 * плашки (озвучек, субтитры), жанры.
 */
@Composable
private fun TitleHeader(d: TitleDetail, voices: VoiceSummary?) {
    val t = d.title
    val (name, original) = splitTitle(t.title)
    val movie = isMovie(d)
    Box(Modifier.fillMaxWidth()) {
        // Фон — тот же постер, размытый и притушенный (blur — с Android 12).
        Box(Modifier.matchParentSize()) {
            PosterImage(
                t.id, t.hasArt, t.title,
                Modifier
                    .fillMaxSize()
                    .then(if (Build.VERSION.SDK_INT >= 31) Modifier.blur(36.dp) else Modifier)
                    .alpha(0.5f),
            )
            Box(
                Modifier
                    .fillMaxSize()
                    .background(Brush.verticalGradient(0f to Color(0x66000000), 0.6f to Color(0xB30A0C10), 1f to Bg)),
            )
        }
        Row(
            Modifier
                .fillMaxWidth()
                .statusBarsPadding()
                .padding(top = 64.dp, start = 20.dp, end = 20.dp, bottom = 18.dp),
            verticalAlignment = Alignment.Top,
        ) {
            PosterImage(
                t.id, t.hasArt, t.title,
                Modifier
                    .width(122.dp)
                    .aspectRatio(2f / 3f)
                    .shadow(18.dp, RoundedCornerShape(12.dp))
                    .clip(RoundedCornerShape(12.dp))
                    .border(1.dp, Stroke, RoundedCornerShape(12.dp)),
            )
            Spacer(Modifier.width(16.dp))
            Column(Modifier.weight(1f)) {
                Text(name, style = MaterialTheme.typography.titleLarge.copy(fontSize = 22.sp, lineHeight = 27.sp), color = TextMain, maxLines = 4, overflow = TextOverflow.Ellipsis)
                if (original.isNotEmpty()) {
                    Text(original, style = MaterialTheme.typography.bodyMedium, color = TextDim, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
                Spacer(Modifier.height(8.dp))
                val seasons = d.episodes.map { it.seasonNo }.filter { it > 0 }.distinct().size
                val meta = listOfNotNull(
                    speciesWord(t.species, t.kind).takeIf { it.isNotEmpty() },
                    t.year.takeIf { it > 0 }?.toString(),
                    if (!movie) (if (t.finished) "завершён" else "выходит") else null,
                )
                Text(meta.joinToString(" · "), style = MaterialTheme.typography.bodyMedium, color = Color(0xFFD5DAE2))
                val size = when {
                    movie -> d.episodes.firstOrNull()?.durationSec?.takeIf { it > 0 }?.let { fmtMins(it / 60) }
                    seasons > 0 -> "$seasons ${ru.sorewa.kino.ui.plural(seasons, "сезон", "сезона", "сезонов")} · " +
                        "${d.episodes.size} ${ru.sorewa.kino.ui.plural(d.episodes.size, "серия", "серии", "серий")}"
                    else -> null
                }
                if (size != null) Text(size, style = MaterialTheme.typography.bodyMedium, color = TextDim)
                // Плашки, как «1080p · AC3 · CC» у Kino.Watch: что есть у тайтла.
                val tags = listOfNotNull(
                    voices?.let { v -> if (v.names.size > 1) "${v.names.size} ${ru.sorewa.kino.ui.plural(v.names.size, "озвучка", "озвучки", "озвучек")}" else null },
                    t.sources.size.takeIf { it > 1 }?.let { "$it ${ru.sorewa.kino.ui.plural(it, "источник", "источника", "источников")}" },
                )
                if (tags.isNotEmpty()) {
                    Spacer(Modifier.height(8.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        tags.forEach { tag ->
                            Text(
                                tag,
                                style = MaterialTheme.typography.labelSmall,
                                color = TextMain,
                                modifier = Modifier
                                    .border(1.dp, Color(0x4DFFFFFF), RoundedCornerShape(6.dp))
                                    .padding(horizontal = 7.dp, vertical = 3.dp),
                            )
                        }
                    }
                }
                if (t.genres.isNotEmpty()) {
                    Spacer(Modifier.height(8.dp))
                    Text(t.genres.take(3).joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = TextFaint, maxLines = 2)
                }
            }
        }
    }
}

/** Озвучки серии: названия студий из общего списка (как «Дорожки» у Kino.Watch). */
data class VoiceSummary(val names: List<String>)

@Composable
private fun VoicesBlock(v: VoiceSummary) {
    if (v.names.isEmpty()) return
    var open by remember { mutableStateOf(false) }
    Column(
        Modifier
            .fillMaxWidth()
            .padding(top = 14.dp)
            .clip(RoundedCornerShape(14.dp))
            .background(Surface1)
            .clickable { open = !open }
            .animateContentSize()
            .padding(14.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Озвучки", style = MaterialTheme.typography.titleSmall, color = TextMain, modifier = Modifier.weight(1f))
            Text("${v.names.size}", style = MaterialTheme.typography.titleSmall, color = TextFaint)
        }
        Spacer(Modifier.height(6.dp))
        val shown = if (open) v.names else v.names.take(4)
        Text(
            shown.joinToString(" · ") + if (!open && v.names.size > 4) " · ещё ${v.names.size - 4}" else "",
            style = MaterialTheme.typography.bodyMedium,
            color = TextDim,
        )
    }
}

@Composable
private fun ActionsRow(
    marks: Marks?,
    movie: Boolean,
    /** null — без кнопки скачивания (офлайн-витрина). */
    download: TitleDownload?,
    percent: Int,
    onMarks: (Marks) -> Unit,
    onDownload: () -> Unit,
) {
    val m = marks ?: Marks()
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
        IconAction(
            if (m.watchlist) Icons.Rounded.Bookmark else Icons.Rounded.BookmarkBorder,
            "Буду смотреть",
            active = m.watchlist,
            modifier = Modifier.weight(1f),
        ) { if (marks != null) onMarks(m.copy(watchlist = !m.watchlist)) }
        if (!movie) {
            IconAction(
                if (m.following) Icons.Rounded.Notifications else Icons.Rounded.NotificationsNone,
                if (m.following) "Слежу" else "Следить",
                active = m.following,
                modifier = Modifier.weight(1f),
            ) { if (marks != null) onMarks(m.copy(following = !m.following)) }
        }
        if (download != null) {
            DownloadAction(download, percent, modifier = Modifier.weight(1f), onClick = onDownload)
        }
        IconAction(
            if (m.hidden) Icons.Rounded.Visibility else Icons.Rounded.VisibilityOff,
            if (m.hidden) "Вернуть" else "Скрыть",
            active = m.hidden,
            modifier = Modifier.weight(1f),
        ) { if (marks != null) onMarks(m.copy(hidden = !m.hidden)) }
    }
}

/** Описание: три строки, касание раскрывает целиком. */
@Composable
private fun Plot(d: TitleDetail) {
    val plot = d.title.plot?.trim().orEmpty()
    var open by remember { mutableStateOf(false) }
    if (plot.isNotEmpty()) {
        Spacer(Modifier.height(14.dp))
        Column(
            Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(14.dp))
                .clickable { open = !open }
                .animateContentSize()
                .padding(vertical = 4.dp),
        ) {
            Text(
                plot,
                style = MaterialTheme.typography.bodyMedium,
                color = Color(0xFFD5DAE2),
                maxLines = if (open) Int.MAX_VALUE else 3,
                overflow = TextOverflow.Ellipsis,
            )
            if (!open && plot.length > 140) {
                Text("Читать дальше", style = MaterialTheme.typography.labelMedium, color = Accent, modifier = Modifier.padding(top = 4.dp))
            }
        }
    }
    if (d.title.sources.isNotEmpty()) {
        Spacer(Modifier.height(10.dp))
        Text(
            "Источники: " + d.title.sources.joinToString(", ") { it.name },
            style = MaterialTheme.typography.bodySmall,
            color = TextFaint,
        )
    }
}

@Composable
private fun SeasonBar(
    seasons: List<Int>,
    watched: Set<Int>,
    downloaded: Set<Int>,
    season: Int,
    eps: List<Episode>,
    offline: Boolean,
    onSeason: (Int) -> Unit,
    onDownloadSeason: () -> Unit,
    onMarkSeason: (Boolean) -> Unit,
    compact: Boolean,
    onCompact: () -> Unit,
) {
    var menu by remember { mutableStateOf(false) }
    val allWatched = eps.all { it.status == "completed" }
    val done = eps.count { it.status == "completed" }
    Column(Modifier.padding(top = 18.dp)) {
        Row(
            Modifier.fillMaxWidth().padding(start = 20.dp, end = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("Серии", style = MaterialTheme.typography.titleLarge, color = TextMain)
            Spacer(Modifier.weight(1f))
            // «Компактно», как у Kino.Watch: без кадров, одним списком.
            IconButton(onClick = onCompact) {
                Icon(
                    if (compact) Icons.Rounded.ViewAgenda else Icons.AutoMirrored.Rounded.ViewList,
                    if (compact) "С кадрами" else "Компактно",
                    tint = TextDim,
                )
            }
            if (!offline) Box {
                IconButton(onClick = { menu = true }) { Icon(Icons.Rounded.MoreVert, "Действия с сезоном", tint = TextDim) }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }, containerColor = Surface2) {
                    DropdownMenuItem(
                        text = { Text(if (seasons.size > 1) "Скачать сезон" else "Скачать все серии") },
                        leadingIcon = { Icon(Icons.Rounded.Download, null) },
                        onClick = { menu = false; onDownloadSeason() },
                    )
                }
            }
        }
        if (seasons.size > 1) {
            Spacer(Modifier.height(6.dp))
            PillRow(
                options = seasons.map { it to (if (it > 0) "Сезон $it" else "Доп.") },
                selected = season,
                onSelect = onSeason,
                // ✓ — досмотрен целиком; стрелка — в сезоне есть скачанное.
                leadingOf = {
                    when (it) {
                        in watched -> Icons.Rounded.DoneAll
                        in downloaded -> Icons.Rounded.DownloadForOffline
                        else -> null
                    }
                },
            )
        }
        // Строка выбранного сезона — прямо под переключателем сезонов: слева
        // сколько в нём просмотрено, справа отметка. Подпись называет сезон
        // по номеру — «Отметить все» читалось как «весь сериал».
        val named = seasons.size > 1 && season > 0
        Row(
            Modifier.fillMaxWidth().padding(start = 20.dp, end = 12.dp, top = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (allWatched) {
                Icon(Icons.Rounded.DoneAll, null, Modifier.size(18.dp), tint = Accent)
                Spacer(Modifier.width(6.dp))
                Text(
                    if (named) "Сезон $season просмотрен" else "Всё просмотрено",
                    style = MaterialTheme.typography.bodyMedium,
                    color = Accent,
                )
            } else {
                Text(
                    (if (named) "Сезон $season · " else "") + "просмотрено $done из ${eps.size}",
                    style = MaterialTheme.typography.bodyMedium,
                    color = TextFaint,
                )
            }
            Spacer(Modifier.weight(1f))
            // Без сети сервер не отметить, а отметка одних скачанных серий
            // читалась бы как отметка сезона.
            if (!offline) Text(
                when {
                    allWatched -> "Снять отметки"
                    named -> "Отметить сезон"
                    else -> "Отметить все серии"
                },
                style = MaterialTheme.typography.labelLarge,
                color = if (allWatched) TextDim else Accent,
                modifier = Modifier
                    .clip(RoundedCornerShape(8.dp))
                    .clickable { onMarkSeason(!allWatched) }
                    .padding(horizontal = 8.dp, vertical = 10.dp),
            )
        }
        Spacer(Modifier.height(4.dp))
    }
}

@Composable
private fun EpisodeRow(
    ep: Episode,
    titleId: Long,
    hasTitleArt: Boolean,
    titleName: String,
    current: Boolean,
    compact: Boolean,
    local: LocalLibrary.Entry?,
    offline: Boolean,
    onPlay: () -> Unit,
    onStream: () -> Unit,
    onDownload: () -> Unit,
    onRemoveLocal: () -> Unit,
    onToggleWatched: (Boolean) -> Unit,
    /** Качается сейчас / ждёт в очереди загрузок; dlPercent — у качающейся. */
    dlCurrent: Boolean = false,
    dlWaiting: Boolean = false,
    dlPercent: Int = 0,
) {
    val watched = ep.status == "completed"
    val progress = if (ep.durationSec > 0 && ep.positionMs > 0) {
        (ep.positionMs.toFloat() / (ep.durationSec * 1000)).coerceIn(0f, 1f)
    } else 0f
    var menu by remember { mutableStateOf(false) }
    // Без сети нескачанная серия видна (список как онлайн), но приглушена.
    val unavailable = offline && local == null
    val frame = remember(local?.uri) { local?.let { KinoApp.instance.container.art.framePath(it.uri) } }
    Row(
        Modifier
            .fillMaxWidth()
            .alpha(if (unavailable) 0.42f else 1f)
            .clickable(onClick = onPlay)
            .background(if (current) Accent.copy(alpha = 0.06f) else Color.Transparent)
            .padding(start = 20.dp, end = 4.dp, top = 8.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (!compact) Box(
            Modifier
                .width(140.dp)
                .aspectRatio(16f / 9f)
                .clip(RoundedCornerShape(10.dp))
                .background(Surface1),
        ) {
            ShotImage(ep.id, titleId, hasTitleArt, titleName, Modifier.fillMaxSize(), tryShot = ep.hasArt, localFrame = frame)
            if (watched) Box(Modifier.fillMaxSize().background(Color(0x8C000000)))
            Box(
                Modifier
                    .align(Alignment.Center)
                    .size(32.dp)
                    .background(if (watched) Accent else Color(0x80000000), CircleShape),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    if (watched) Icons.Rounded.Done else Icons.Rounded.PlayArrow, null,
                    tint = if (watched) OnAccent else Color.White,
                    modifier = Modifier.size(20.dp),
                )
            }
            if (progress > 0f && !watched) {
                ProgressLine(progress, Modifier.align(Alignment.BottomCenter).padding(6.dp))
            }
        }
        else Box(
            Modifier
                .size(36.dp)
                .background(if (watched) Accent else Surface2, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            if (watched) Icon(Icons.Rounded.Done, null, tint = OnAccent, modifier = Modifier.size(20.dp))
            else Text("${ep.episodeNo}", style = MaterialTheme.typography.labelLarge, color = if (progress > 0f) Accent else TextMain)
        }
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f)) {
            val epTitle = ep.title?.takeIf { it.isNotBlank() && !it.matches(Regex("(?i)(серия|episode)?\\s*\\d+")) }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    "${ep.episodeNo} серия",
                    style = MaterialTheme.typography.titleSmall,
                    color = if (watched) TextDim else TextMain,
                )
                if (local != null) {
                    Spacer(Modifier.width(6.dp))
                    Icon(Icons.Rounded.DownloadForOffline, "Скачана", tint = Accent, modifier = Modifier.size(17.dp))
                } else if (dlCurrent || dlWaiting) {
                    Spacer(Modifier.width(6.dp))
                    EpisodeDownloadMark(dlCurrent, dlPercent)
                }
            }
            if (epTitle != null) {
                Text(
                    epTitle,
                    style = MaterialTheme.typography.bodySmall,
                    color = TextDim,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            val mins = ep.durationSec / 60
            val info = listOf(
                when {
                    watched -> "просмотрено"
                    progress > 0f -> "осталось ${fmtMins(((ep.durationSec * 1000 - ep.positionMs) / 60_000))}"
                    mins > 0 -> "$mins мин"
                    else -> ""
                },
                when {
                    local != null -> fmtBytes(local.size)
                    dlCurrent -> if (dlPercent > 0) "качается $dlPercent%" else "качается"
                    dlWaiting -> "в очереди загрузок"
                    offline -> "не скачана"
                    else -> ""
                },
            ).filter { it.isNotEmpty() }.joinToString(" · ")
            if (info.isNotEmpty()) {
                Spacer(Modifier.height(2.dp))
                Text(info, style = MaterialTheme.typography.bodySmall, color = if (watched) Accent.copy(alpha = 0.8f) else TextFaint, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        Box {
            IconButton(onClick = { menu = true }) { Icon(Icons.Rounded.MoreVert, "Ещё", tint = TextFaint) }
            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }, containerColor = Surface2) {
                if (!unavailable) DropdownMenuItem(
                    text = { Text(if (watched) "Не просмотрено" else "Просмотрено") },
                    leadingIcon = { Icon(if (watched) Icons.Rounded.RemoveDone else Icons.Rounded.CheckCircle, null) },
                    onClick = { menu = false; onToggleWatched(!watched) },
                )
                if (local != null && !offline) DropdownMenuItem(
                    text = { Text("Смотреть онлайн") },
                    leadingIcon = { Icon(Icons.Rounded.Cloud, null) },
                    onClick = { menu = false; onStream() },
                )
                if (local == null && !offline && !dlCurrent && !dlWaiting) DropdownMenuItem(
                    text = { Text("Скачать серию") },
                    leadingIcon = { Icon(Icons.Rounded.Download, null) },
                    onClick = { menu = false; onDownload() },
                )
                if (local != null) DropdownMenuItem(
                    text = { Text("Стереть с телефона", color = ru.sorewa.kino.ui.theme.Danger) },
                    leadingIcon = { Icon(Icons.Rounded.Delete, null, tint = ru.sorewa.kino.ui.theme.Danger) },
                    onClick = { menu = false; onRemoveLocal() },
                )
                if (unavailable) DropdownMenuItem(
                    text = { Text("Не скачана — нужна сеть", color = TextFaint) },
                    leadingIcon = { Icon(Icons.Rounded.CloudOff, null, tint = TextFaint) },
                    enabled = false,
                    onClick = { menu = false },
                )
            }
        }
    }
}

