package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.background
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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Schedule
import androidx.compose.material.icons.rounded.CloudDone
import androidx.compose.material.icons.rounded.DownloadForOffline
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.PlayerActivity
import ru.sorewa.kino.data.LocalLibrary
import ru.sorewa.kino.downloader.DownloadService
import ru.sorewa.kino.ui.EmptyState
import ru.sorewa.kino.ui.LocalTv
import ru.sorewa.kino.ui.Panel
import ru.sorewa.kino.ui.PosterCard
import ru.sorewa.kino.ui.ProgressLine
import ru.sorewa.kino.ui.Rail
import ru.sorewa.kino.ui.ScreenHeader
import ru.sorewa.kino.ui.SectionHeader
import ru.sorewa.kino.ui.ShotImage
import ru.sorewa.kino.ui.cardClick
import ru.sorewa.kino.ui.fmtMins
import ru.sorewa.kino.ui.plural
import ru.sorewa.kino.ui.splitTitle
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Danger
import ru.sorewa.kino.ui.theme.Surface1
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint

/**
 * Скачанный тайтл: все его серии на телефоне. key — titleId, а у файлов,
 * не найденных в каталоге, — название папки.
 */
data class LocalTitle(
    val key: String,
    val titleId: Long,
    val name: String,
    val hasArt: Boolean,
    val movie: Boolean,
    val entries: List<LocalLibrary.Entry>,
) {
    val size: Long get() = entries.sumOf { it.size }
    val unwatched: Int get() = entries.count { !it.watched }
    val lastTouched: Long get() = entries.maxOf { maxOf(it.playedAt, it.addedAt) }

    /** Что включать по касанию: начатая, иначе первая непросмотренная, иначе первая. */
    fun next(): LocalLibrary.Entry {
        val sorted = entries.sortedWith(compareBy({ it.season }, { it.episode }))
        return sorted.firstOrNull { !it.watched && it.positionMs > 0 }
            ?: sorted.firstOrNull { !it.watched }
            ?: sorted.first()
    }
}

/** Опись → тайтлы; название и постер — из сохранённой карточки, если есть. */
fun groupLocal(entries: List<LocalLibrary.Entry>): List<LocalTitle> {
    val offline = KinoApp.instance.container.offline
    val art = KinoApp.instance.container.art
    return entries
        .groupBy { if (it.titleId > 0) "t${it.titleId}" else "n${it.title}" }
        .map { (key, list) ->
            val id = list.first().titleId
            val saved = offline.saved(id)?.detail
            LocalTitle(
                key = key,
                titleId = id,
                name = saved?.title?.title ?: list.first().title.ifEmpty { "Без названия" },
                hasArt = (saved?.title?.hasArt ?: false) || art.posterPath(id) != null,
                movie = saved?.let { isMovie(it) } ?: list.all { it.movie },
                entries = list,
            )
        }
        .sortedByDescending { it.lastTouched }
}

/** Включить скачанную серию: просмотренную — с начала, начатую — с места. */
fun playLocal(e: LocalLibrary.Entry) {
    val ctx = KinoApp.instance
    ctx.startActivity(
        android.content.Intent(ctx, PlayerActivity::class.java)
            .putExtra(PlayerActivity.EXTRA_MODE, PlayerActivity.MODE_LOCAL)
            .putExtra(PlayerActivity.EXTRA_LIB_ID, e.id)
            .putExtra(PlayerActivity.EXTRA_TITLE, e.titleId)
            .putExtra(PlayerActivity.EXTRA_AUTOPLAY, !e.watched)
            .addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK),
    )
}

/** Страница скачанного тайтла: по id каталога или по названию папки. */
fun NavHostController.openLocalTitle(t: LocalTitle) {
    if (t.titleId > 0) navigate("title/${t.titleId}")
    else navigate("local?name=" + android.net.Uri.encode(t.name))
}

/**
 * Загрузки — и офлайн-витрина: без сети это главный экран приложения.
 * Скачанное собрано по тайтлам, как в каталоге: постер, название, сколько
 * серий; касание открывает страницу тайтла (без сети — из сохранённой
 * карточки). Сверху — что досматривается, внизу — место на телефоне.
 */
@Composable
fun DownloadsScreen(nav: NavHostController?, offline: Boolean = false) {
    val container = remember { KinoApp.instance.container }
    val scope = rememberCoroutineScope()
    val tv = LocalTv.current
    var entries by remember { mutableStateOf<List<LocalLibrary.Entry>>(emptyList()) }
    var loaded by remember { mutableStateOf(false) }
    var confirmWipe by remember { mutableStateOf(false) }
    var confirmRemove by remember { mutableStateOf<LocalTitle?>(null) }
    var note by remember { mutableStateOf("") }
    val dl by DownloadService.status.collectAsStateWithLifecycle()
    val prep by container.offline.status.collectAsStateWithLifecycle()

    fun reload() {
        scope.launch {
            entries = withContext(Dispatchers.IO) { container.library.listWithArt(container.art) }
            loaded = true
        }
    }

    // Разрешение на чтение медиа (adopt) — запрашивается лениво, один раз.
    // На ТВ не спрашиваем: файлы туда подбрасывать некому, а системное окно
    // «доступ к фото и видео» при входе в раздел только пугало.
    fun adoptNow() {
        scope.launch {
            ru.sorewa.kino.ui.runAdopt()
            reload()
        }
    }
    val adoptPerm = if (android.os.Build.VERSION.SDK_INT >= 33) {
        android.Manifest.permission.READ_MEDIA_VIDEO
    } else {
        android.Manifest.permission.READ_EXTERNAL_STORAGE
    }
    val adoptPermLauncher = androidx.activity.compose.rememberLauncherForActivityResult(
        androidx.activity.result.contract.ActivityResultContracts.RequestPermission(),
    ) { _ -> adoptNow() }

    LaunchedEffect(Unit) {
        reload()
        // Очередь, ждущий повтор или готовые серии от оборвавшегося прогона —
        // служба доделает; сдавшееся задание снова показывает «Повторить».
        if (!tv) withContext(Dispatchers.IO) {
            DownloadService.restoreFailed(KinoApp.instance)
            DownloadService.kickIfNeeded(KinoApp.instance)
        }
        val granted = androidx.core.content.ContextCompat.checkSelfPermission(KinoApp.instance, adoptPerm) ==
            android.content.pm.PackageManager.PERMISSION_GRANTED
        if (granted) adoptNow() else if (!tv) adoptPermLauncher.launch(adoptPerm)
        // С сетью — прогресс серверу и карточки к офлайну (в фоне, не держит экран).
        if (!offline) container.warmUpOffline()
    }
    // Подготовка закончилась — названия и постеры могли появиться.
    LaunchedEffect(prep.running) { if (!prep.running) reload() }
    // Опись изменилась: позиция из плеера, докачанная или стёртая серия.
    val libChanges by container.library.changes.collectAsStateWithLifecycle()
    LaunchedEffect(libChanges) { if (loaded) reload() }

    // Возврат из плеера — позиция обновилась.
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        var first = true
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) { if (!first) reload(); first = false }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    val groups = remember(entries, prep.running) { groupLocal(entries) }
    val resume = entries
        .filter { !it.watched && it.positionMs > 0 }
        .sortedByDescending { it.playedAt }
    val open: (LocalTitle) -> Unit = { t ->
        // ТВ без сети ходит без навигации, а «local?» есть только у телефона.
        if (nav == null || (tv && t.titleId <= 0)) playLocal(t.next()) else nav.openLocalTitle(t)
    }

    androidx.compose.foundation.layout.BoxWithConstraints(
        Modifier.fillMaxSize().then(if (nav != null && !offline && !tv) Modifier.statusBarsPadding() else Modifier),
    ) {
    // Постеры — рядами, как сетка каталога: на телефоне от трёх в ряд.
    val cols = if (tv) 6 else ((maxWidth.value - 40f) / 112f).toInt().coerceIn(3, 6)
    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(bottom = 24.dp),
    ) {
        item(key = "head") {
            ScreenHeader(
                "Загрузки",
                subtitle = when {
                    offline -> "Без сети — всё скачанное здесь"
                    note.isNotEmpty() -> note
                    else -> null
                },
            )
        }

        // Идущая качалка — карточка статуса с паузой/отменой.
        if (dl.running != null) {
            item(key = "dl") { Box(Modifier.padding(horizontal = 16.dp, vertical = 6.dp)) { DownloadStatusCard(dl) } }
        } else if (dl.lastError.isNotEmpty() || dl.retryAt > 0) {
            item(key = "dlerr") {
                Box(Modifier.padding(horizontal = 16.dp, vertical = 6.dp)) { DownloadFailCard(dl) }
            }
        } else if (dl.queued > 0) {
            // Поставлено, но ещё не пошло (служба переносит прошлое скачанное
            // или ждёт сеть): без этой карточки экран был пуст, и казалось,
            // что нажатие «Скачать» не сработало.
            item(key = "dlq") {
                Box(Modifier.padding(horizontal = 16.dp, vertical = 6.dp)) { DownloadQueuedCard(dl) }
            }
        }

        if (loaded && entries.isEmpty() && dl.running == null && dl.queued == 0) {
            item(key = "empty") {
                EmptyState(
                    Icons.Rounded.DownloadForOffline,
                    "Скачанного пока нет",
                    when {
                        tv -> "Скачивают серии на телефоне, на телевизоре смотрят онлайн."
                        offline -> "Когда появится сеть, скачайте серии на странице тайтла."
                        else -> "Нажмите «Скачать» на странице тайтла — серии будут доступны без интернета."
                    },
                )
            }
        }

        // Досматриваемое — как «Продолжить смотреть» на главной.
        if (resume.isNotEmpty()) {
            item(key = "resume-h") { SectionHeader("Продолжить", count = resume.size.takeIf { it > 1 }) }
            item(key = "resume") {
                Rail {
                    items(resume, key = { it.id }) { e ->
                        LocalWideCard(e, Modifier.width(if (tv) 300.dp else 248.dp)) { playLocal(e) }
                    }
                }
            }
        }

        if (groups.isNotEmpty()) {
            item(key = "titles-h") {
                SectionHeader(if (groups.all { it.movie }) "Фильмы" else "Сериалы и фильмы", count = groups.size)
            }
            items(groups.chunked(cols), key = { row -> row.first().key }) { row ->
                Row(
                    Modifier.fillMaxWidth().padding(start = if (tv) 0.dp else 20.dp, end = if (tv) 0.dp else 20.dp, top = 6.dp, bottom = 8.dp),
                    horizontalArrangement = Arrangement.spacedBy(if (tv) 20.dp else 12.dp),
                ) {
                    row.forEach { t ->
                        PosterCard(
                            titleId = t.titleId,
                            name = t.name,
                            hasArt = t.hasArt,
                            modifier = Modifier.weight(1f),
                            sub = localSub(t),
                            badge = if (!t.movie && t.unwatched > 0) "${t.unwatched}" else null,
                            progress = if (t.movie) t.entries.first().let { e ->
                                if (e.durationMs > 0 && !e.watched) e.positionMs.toFloat() / e.durationMs else null
                            } else (t.entries.size - t.unwatched).toFloat() / t.entries.size,
                            dimmed = t.unwatched == 0,
                            onLongClick = if (tv) null else ({ confirmRemove = t }),
                            onClick = { open(t) },
                        )
                    }
                    repeat(cols - row.size) { Spacer(Modifier.weight(1f)) }
                }
            }
        }

        if (entries.isNotEmpty()) {
            item(key = "storage") {
                Box(Modifier.padding(horizontal = 16.dp, vertical = 10.dp)) {
                    StorageCard(
                        entries = entries,
                        prep = if (offline || tv) null else prep,
                        onWipe = { confirmWipe = true },
                        onPrepare = { container.warmUpOffline(force = true) },
                    )
                }
            }
        }
    }
    }

    if (confirmWipe) {
        val watched = entries.filter { it.watched }
        AlertDialog(
            onDismissRequest = { confirmWipe = false },
            title = { Text("Стереть просмотренное (${watched.size})?") },
            text = { Text("Освободится ${fmtBytes(watched.sumOf { it.size })}. Файлы удаляются насовсем.") },
            confirmButton = {
                Button(onClick = {
                    scope.launch(Dispatchers.IO) {
                        val (n, _) = container.library.wipeWatched()
                        note = "Стерто: $n"
                        reload()
                    }
                    confirmWipe = false
                }) { Text("Стереть") }
            },
            dismissButton = { TextButton(onClick = { confirmWipe = false }) { Text("Отмена") } },
        )
    }
    confirmRemove?.let { t ->
        AlertDialog(
            onDismissRequest = { confirmRemove = null },
            title = { Text("Стереть «${splitTitle(t.name).first}»?") },
            text = {
                Text(
                    "${t.entries.size} ${plural(t.entries.size, "файл", "файла", "файлов")}, " +
                        "${fmtBytes(t.size)}. Файлы удаляются насовсем.",
                )
            },
            confirmButton = {
                Button(onClick = {
                    scope.launch(Dispatchers.IO) {
                        val failed = t.entries.count { !container.library.remove(it.id) }
                        note = if (failed > 0) "Не удалось стереть: $failed" else "Стерто: ${splitTitle(t.name).first}"
                        reload()
                    }
                    confirmRemove = null
                }) { Text("Стереть") }
            },
            dismissButton = { TextButton(onClick = { confirmRemove = null }) { Text("Отмена") } },
        )
    }
}

/** «5 серий · 2,1 ГБ» / «Фильм · 1,4 ГБ». */
private fun localSub(t: LocalTitle): String {
    val n = t.entries.size
    val what = if (t.movie) "Фильм" else "$n ${plural(n, "серия", "серии", "серий")}"
    return "$what · ${fmtBytes(t.size)}"
}

/** Широкая карточка начатой серии: кадр, название, «1×3 · осталось 12 мин». */
@Composable
private fun LocalWideCard(e: LocalLibrary.Entry, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val container = KinoApp.instance.container
    val saved = remember(e.titleId) { container.offline.saved(e.titleId)?.detail }
    val ep = saved?.episodes?.find { it.seasonNo == e.season && it.episodeNo == e.episode }
    val name = saved?.title?.title ?: e.title
    val frame = remember(e.uri) { container.art.framePath(e.uri) }
    val shape = RoundedCornerShape(14.dp)
    Box(
        modifier
            .aspectRatio(16f / 9f)
            .cardClick(shape, null, focusScale = 1.05f, onClick = onClick)
            .background(Surface1),
    ) {
        ShotImage(
            ep?.id ?: 0, e.titleId, saved?.title?.hasArt ?: true, name, Modifier.fillMaxSize(),
            tryShot = ep?.hasArt == true, localFrame = frame,
        )
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.35f to Color.Transparent, 1f to Color(0xE6000000))))
        Box(
            Modifier.align(Alignment.Center).size(44.dp).background(Color(0x66000000), CircleShape),
            contentAlignment = Alignment.Center,
        ) { Icon(Icons.Rounded.PlayArrow, null, tint = Color.White, modifier = Modifier.size(28.dp)) }
        Column(Modifier.align(Alignment.BottomStart).fillMaxWidth().padding(horizontal = 12.dp, vertical = 10.dp)) {
            Text(splitTitle(name).first, style = MaterialTheme.typography.titleSmall, color = Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val left = if (e.durationMs > 0) "осталось ${fmtMins((e.durationMs - e.positionMs).coerceAtLeast(0) / 60_000)}" else ""
            Text(
                listOf(if (e.movie) "" else "${e.season}×${e.episode}", left).filter { it.isNotEmpty() }.joinToString(" · "),
                style = MaterialTheme.typography.bodySmall, color = Color(0xCCFFFFFF), maxLines = 1,
            )
            if (e.durationMs > 0) {
                Spacer(Modifier.height(6.dp))
                ProgressLine((e.positionMs.toFloat() / e.durationMs).coerceIn(0f, 1f))
            }
        }
    }
}

/**
 * Сколько занято, «стереть просмотренное» и готовность к офлайну: карточки
 * и картинки сохраняются, пока сеть есть, — видно, можно ли улетать.
 */
@Composable
private fun StorageCard(
    entries: List<LocalLibrary.Entry>,
    prep: ru.sorewa.kino.data.OfflineCatalog.Status?,
    onWipe: () -> Unit,
    onPrepare: () -> Unit,
) {
    val total = entries.sumOf { it.size }
    val watched = entries.filter { it.watched }
    val free = remember { try { android.os.Environment.getExternalStorageDirectory().usableSpace } catch (_: Exception) { 0L } }
    Panel {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(fmtBytes(total), style = MaterialTheme.typography.titleLarge)
                Text(
                    "${entries.size} ${plural(entries.size, "файл", "файла", "файлов")}" +
                        if (free > 0) " · свободно ${fmtBytes(free)}" else "",
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                )
            }
            if (watched.isNotEmpty()) {
                TextButton(onClick = onWipe) { Text("Стереть\nпросмотренное", color = Danger, textAlign = TextAlign.End) }
            }
        }
        if (free > 0) {
            Spacer(Modifier.height(12.dp))
            ProgressLine(total.toFloat() / (total + free).coerceAtLeast(1), height = 6.dp)
        }
        if (prep != null) {
            Spacer(Modifier.height(14.dp))
            val (ready, all) = remember(entries, prep.running) { KinoApp.instance.container.offline.readiness() }
            Row(verticalAlignment = Alignment.CenterVertically) {
                when {
                    prep.running -> CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = Accent)
                    ready >= all -> Icon(Icons.Rounded.CloudDone, null, tint = Accent, modifier = Modifier.size(20.dp))
                    else -> Icon(Icons.Rounded.DownloadForOffline, null, tint = TextDim, modifier = Modifier.size(20.dp))
                }
                Spacer(Modifier.width(10.dp))
                Text(
                    when {
                        prep.running -> "Готовлю к просмотру без сети: ${prep.done} из ${prep.total}"
                        ready >= all -> "Готово к просмотру без сети: обложки, описания и серии сохранены"
                        else -> "Без сети откроется не всё: карточки сохранены у $ready из $all"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = if (!prep.running && ready >= all) TextDim else TextFaint,
                    modifier = Modifier.weight(1f),
                )
                if (!prep.running && ready < all) {
                    TextButton(onClick = onPrepare) { Text("Сохранить", color = Accent) }
                }
            }
        }
    }
}

/**
 * Загрузка не удалась или ждёт автоповтора: что именно не далось, когда
 * повтор, кнопки «Повторить» (сейчас) и «Скрыть». Уже скачанные серии
 * повтор не перекачивает — утилита помнит их.
 */
@Composable
private fun DownloadFailCard(dl: ru.sorewa.kino.downloader.DownloadStatus) {
    fun send(action: String) {
        val i = android.content.Intent(KinoApp.instance, DownloadService::class.java).setAction(action)
        if (android.os.Build.VERSION.SDK_INT >= 26) KinoApp.instance.startForegroundService(i) else KinoApp.instance.startService(i)
    }
    // Обратный отсчёт до автоповтора — раз в секунду.
    var now by remember { mutableStateOf(System.currentTimeMillis()) }
    if (dl.retryAt > 0) LaunchedEffect(dl.retryAt) {
        while (true) { now = System.currentTimeMillis(); kotlinx.coroutines.delay(1000) }
    }
    val waiting = dl.retryAt > now
    Panel {
        Text(
            if (waiting) "Загрузка прервалась — повторю автоматически" else "Загрузка не удалась",
            style = MaterialTheme.typography.titleSmall,
            color = if (waiting) ru.sorewa.kino.ui.theme.TextMain else MaterialTheme.colorScheme.error,
        )
        if (dl.lastError.isNotEmpty()) {
            Spacer(Modifier.height(4.dp))
            Text(dl.lastError, style = MaterialTheme.typography.bodySmall, color = TextDim, maxLines = 4, overflow = TextOverflow.Ellipsis)
        }
        Spacer(Modifier.height(4.dp))
        Text(
            when {
                waiting && !ru.sorewa.kino.util.hasNetwork() -> "Жду сеть. Уже скачанное не пропадёт и не перекачается."
                waiting -> "Повтор через ${((dl.retryAt - now) / 1000).coerceAtLeast(1)} с. Уже скачанное не перекачается."
                else -> "Скачанные серии уже в «Загрузках»; повтор докачает только недостающие."
            },
            style = MaterialTheme.typography.bodySmall, color = TextFaint,
        )
        if (dl.canRetry || waiting) {
            Spacer(Modifier.height(10.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                Button(onClick = { send(DownloadService.ACTION_RETRY) }) {
                    Icon(Icons.Rounded.Refresh, null, modifier = Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text(if (waiting) "Повторить сейчас" else "Повторить")
                }
                if (!waiting) TextButton(onClick = {
                    ru.sorewa.kino.downloader.Downloader.saveFailed(KinoApp.instance, null)
                    DownloadService.status.value = DownloadService.status.value.copy(lastError = "", canRetry = false)
                }) { Text("Скрыть", color = TextDim) }
            }
        }
    }
}

/** Задания стоят, а качалка ещё не начала: что ждёт и почему. */
@Composable
private fun DownloadQueuedCard(dl: ru.sorewa.kino.downloader.DownloadStatus) {
    val queue = remember(dl) { ru.sorewa.kino.downloader.Downloader.loadQueue(KinoApp.instance) }
    val first = queue.firstOrNull()
    Panel {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Rounded.Schedule, null, tint = Accent, modifier = Modifier.size(22.dp))
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(
                    first?.let { splitTitle(it.title).first } ?: "Загрузка в очереди",
                    style = MaterialTheme.typography.titleSmall,
                    maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
                Text(
                    listOf(
                        if (ru.sorewa.kino.util.hasNetwork()) "сейчас начнётся" else "жду сеть, чтобы начать",
                        if (dl.queued > 1) "в очереди: ${dl.queued}" else "",
                    ).filter { it.isNotEmpty() }.joinToString(" · "),
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                    maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            IconButton(onClick = {
                KinoApp.instance.startService(
                    android.content.Intent(KinoApp.instance, DownloadService::class.java).setAction(DownloadService.ACTION_CANCEL),
                )
            }) {
                Icon(Icons.Rounded.Close, "Отменить", tint = Danger)
            }
        }
        Spacer(Modifier.height(10.dp))
        DownloadBar(null)
    }
}

@Composable
private fun DownloadStatusCard(dl: ru.sorewa.kino.downloader.DownloadStatus) {
    fun send(action: String) = KinoApp.instance.startService(
        android.content.Intent(KinoApp.instance, DownloadService::class.java).setAction(action),
    )
    Panel {
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (dl.paused) {
                Icon(Icons.Rounded.Pause, null, tint = TextDim, modifier = Modifier.size(22.dp))
            } else {
                CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp, color = Accent)
            }
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(
                    splitTitle(dl.running?.title ?: "").first,
                    style = MaterialTheme.typography.titleSmall,
                    maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
                Text(
                    (listOf(downloadLine(dl)) + (if (dl.queued > 0) listOf("ещё в очереди: ${dl.queued}") else emptyList()))
                        .filter { it.isNotEmpty() }.joinToString(" · "),
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                    maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            IconButton(onClick = {
                send(if (dl.paused) DownloadService.ACTION_RESUME else DownloadService.ACTION_PAUSE)
            }) {
                Icon(
                    if (dl.paused) Icons.Rounded.PlayArrow else Icons.Rounded.Pause,
                    if (dl.paused) "Продолжить" else "Пауза",
                )
            }
            IconButton(onClick = { send(DownloadService.ACTION_CANCEL) }) {
                Icon(Icons.Rounded.Close, "Отменить", tint = Danger)
            }
        }
        Spacer(Modifier.height(10.dp))
        DownloadBar(dl)
        if (dl.lastError.isNotEmpty()) {
            Spacer(Modifier.height(8.dp))
            Text(dl.lastError, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
        }
    }
}

fun entryLabel(e: LocalLibrary.Entry): String =
    if (e.movie) "Фильм" else "${e.season} сезон, ${e.episode} серия"

fun fmtBytes(b: Long): String = when {
    b >= 1L shl 30 -> "%.1f ГБ".format(b.toDouble() / (1L shl 30))
    b >= 1L shl 20 -> "%.0f МБ".format(b.toDouble() / (1L shl 20))
    b >= 1L shl 10 -> "${b / (1L shl 10)} КБ"
    else -> "$b Б"
}
