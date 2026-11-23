package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.ChevronRight
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.Schedule
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import ru.sorewa.kino.data.Episode
import ru.sorewa.kino.data.LocalLibrary
import ru.sorewa.kino.downloader.DownloadJob
import ru.sorewa.kino.downloader.DownloadStatus
import ru.sorewa.kino.ui.ProgressLine
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Danger
import ru.sorewa.kino.ui.theme.Surface1
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain

/**
 * Что качалка делает с этим тайтлом — для страницы тайтла. Без этого после
 * «Скачать» шторка закрывается, на странице ничего не меняется, и человек
 * жмёт ещё и ещё.
 */
data class TitleDownload(
    /** Идёт задание этого тайтла (или стоит на паузе). */
    val running: Boolean = false,
    val paused: Boolean = false,
    /** Серия, которая качается сейчас («сезон × серия» карточки). */
    val current: Pair<Int, Int>? = null,
    /** Ждут своей очереди: очередь, остаток текущего задания, пауза. */
    val waiting: Set<Pair<Int, Int>> = emptySet(),
    /** Ждёт автоповтора (мс); 0 — нет. */
    val retryAt: Long = 0,
    /** Задание этого тайтла сдалось — причина; пусто — нет. */
    val failed: String = "",
) {
    val busy: Boolean get() = running || waiting.isNotEmpty() || retryAt > 0
    val shown: Boolean get() = busy || failed.isNotEmpty()
}

/**
 * Состояние качалки для тайтла. Чистая функция: очередь, задание с паузы и
 * сдавшееся задание передаются снаружи (они в prefs).
 */
fun titleDownload(
    dl: DownloadStatus,
    queue: List<DownloadJob>,
    paused: DownloadJob?,
    failed: Pair<DownloadJob, String>?,
    titleId: Long,
    episodes: List<Episode>,
): TitleDownload {
    if (titleId <= 0) return TitleDownload()
    fun mine(j: DownloadJob?) = j != null && LocalLibrary.titleIdOf(j.pageUrl) == titleId
    val run = dl.running?.takeIf(::mine)
    val current = run?.let { utilityKey(dl.key, episodes) }
    val done = dl.doneKeys.mapNotNull { utilityKey(it, episodes) }.toSet()
    val jobs = queue.filter(::mine) + listOfNotNull(paused?.takeIf(::mine), run)
    val waiting = inFlightKeys(jobs, titleId, episodes) - done - setOfNotNull(current)
    val retry = queue.filter(::mine).maxOfOrNull { it.notBefore } ?: 0
    return TitleDownload(
        running = run != null,
        paused = run != null && dl.paused,
        current = current,
        waiting = waiting,
        retryAt = if (run == null && retry > System.currentTimeMillis()) retry else 0,
        failed = if (run == null && failed != null && mine(failed.first)) {
            failed.second.ifEmpty { dl.lastError }.ifEmpty { "Загрузка не удалась" }
        } else "",
    )
}

/**
 * «S01E03» утилиты → серия карточки. Утилита нумерует как сериал: у фильма
 * и сезона 0 сезон 1, у серии 0 — серия 1; точное совпадение важнее.
 */
fun utilityKey(key: String, episodes: List<Episode>): Pair<Int, Int>? {
    val m = Regex("S(\\d+)E(\\d+)", RegexOption.IGNORE_CASE).find(key) ?: return null
    val s = m.groupValues[1].toInt()
    val e = m.groupValues[2].toInt()
    val ep = episodes.firstOrNull { it.seasonNo == s && it.episodeNo == e }
        ?: episodes.firstOrNull { it.seasonNo.coerceAtLeast(1) == s && it.episodeNo.coerceAtLeast(1) == e }
        ?: return null
    return ep.seasonNo to ep.episodeNo
}

/** «S01E03 · 37% · 512 МБ из 1,4 ГБ · серия 2 из 8» — строка текущей загрузки. */
fun downloadLine(dl: DownloadStatus, withKey: Boolean = true): String {
    val starting = dl.percent == 0 && dl.bytesDone == 0L
    val bytes = if (dl.bytesDone > 0) {
        "${fmtBytes(dl.bytesDone)}${if (dl.approx) "~" else ""}" +
            (if (dl.bytesTotal > 0) " из ${fmtBytes(dl.bytesTotal)}" else "")
    } else ""
    return listOf(
        if (withKey) dl.key else "",
        when {
            dl.paused -> "на паузе"
            starting -> "готовлю загрузку…"
            else -> "${dl.percent}%"
        },
        bytes,
        if (dl.epTotal > 1) "серия ${(dl.epDone + 1).coerceAtMost(dl.epTotal)} из ${dl.epTotal}" else "",
    ).filter { it.isNotEmpty() }.joinToString(" · ")
}

/** Полоса загрузки: проценты, а пока их нет (готовится, в очереди) — бегущая. */
@Composable
fun DownloadBar(dl: DownloadStatus?, modifier: Modifier = Modifier) {
    if (dl == null || (!dl.paused && dl.percent == 0 && dl.bytesDone == 0L)) {
        LinearProgressIndicator(
            modifier.fillMaxWidth().height(6.dp).clip(RoundedCornerShape(50)),
            color = Accent, trackColor = Color(0x40FFFFFF),
        )
    } else {
        ProgressLine(dl.percent / 100f, modifier, height = 6.dp)
    }
}

/** Кнопка «Скачать» под заголовком: пока качается — кольцо с процентами. */
@Composable
fun DownloadAction(state: TitleDownload, percent: Int, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val label = when {
        state.running && state.paused -> "На паузе"
        state.running -> if (percent > 0) "Качается $percent%" else "Качается"
        state.busy -> "В очереди"
        state.failed.isNotEmpty() -> "Не скачалось"
        else -> "Скачать"
    }
    val tint = when {
        state.failed.isNotEmpty() && !state.busy -> Danger
        state.busy -> Accent
        else -> TextMain
    }
    Column(
        modifier
            .clip(RoundedCornerShape(14.dp))
            .clickable(onClick = onClick)
            .padding(vertical = 8.dp, horizontal = 4.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box(Modifier.size(24.dp), contentAlignment = Alignment.Center) {
            when {
                state.running && state.paused -> Icon(Icons.Rounded.Pause, null, tint = tint, modifier = Modifier.size(24.dp))
                state.running && percent > 0 -> CircularProgressIndicator(
                    progress = { percent / 100f },
                    modifier = Modifier.size(22.dp), color = Accent, strokeWidth = 2.5.dp,
                    trackColor = Color(0x33FFFFFF),
                )
                state.running -> CircularProgressIndicator(Modifier.size(22.dp), color = Accent, strokeWidth = 2.5.dp)
                state.busy -> Icon(Icons.Rounded.Schedule, null, tint = tint, modifier = Modifier.size(24.dp))
                state.failed.isNotEmpty() -> Icon(Icons.Rounded.ErrorOutline, null, tint = tint, modifier = Modifier.size(24.dp))
                else -> Icon(Icons.Rounded.Download, null, tint = tint, modifier = Modifier.size(24.dp))
            }
        }
        Spacer(Modifier.height(6.dp))
        Text(
            label,
            style = MaterialTheme.typography.labelMedium,
            color = if (tint == TextMain) TextDim else tint,
            maxLines = 2,
            textAlign = TextAlign.Center,
        )
    }
}

/**
 * Полоса под кнопками страницы тайтла: что качается, сколько, что ждёт.
 * Касание — во вкладку «Загрузки» (там пауза и отмена).
 */
@Composable
fun TitleDownloadStrip(
    state: TitleDownload,
    dl: DownloadStatus,
    movie: Boolean,
    onOpen: () -> Unit,
    onRetry: () -> Unit,
) {
    if (!state.shown) return
    // Обратный отсчёт автоповтора — раз в секунду.
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    if (state.retryAt > 0) LaunchedEffect(state.retryAt) {
        while (true) { now = System.currentTimeMillis(); kotlinx.coroutines.delay(1000) }
    }
    val failedOnly = state.failed.isNotEmpty() && !state.busy
    val title = when {
        failedOnly -> "Не скачалось"
        state.running && state.paused -> "Загрузка на паузе"
        state.running -> if (movie) "Фильм качается" else "Качается"
        state.retryAt > now -> "Загрузка прервалась — повторю автоматически"
        else -> "В очереди загрузок"
    }
    val sub = when {
        failedOnly -> state.failed
        state.running -> downloadLine(dl, withKey = !movie)
        state.retryAt > now -> "Повтор через ${((state.retryAt - now) / 1000).coerceAtLeast(1)} с"
        !ru.sorewa.kino.util.hasNetwork() -> "Жду сеть, чтобы начать"
        else -> "Сейчас начнётся"
    }
    val more = if (!movie && state.waiting.isNotEmpty()) {
        "ещё ${state.waiting.size} ${ru.sorewa.kino.ui.plural(state.waiting.size, "серия", "серии", "серий")} в очереди"
    } else ""
    Spacer(Modifier.height(8.dp))
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(14.dp))
            .background(Surface1)
            .clickable(onClick = onOpen)
            .padding(horizontal = 14.dp, vertical = 12.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(
                    title,
                    style = MaterialTheme.typography.titleSmall,
                    color = if (failedOnly) MaterialTheme.colorScheme.error else TextMain,
                    maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
                Text(
                    listOf(sub, more).filter { it.isNotEmpty() }.joinToString(" · "),
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                    maxLines = if (failedOnly) 3 else 1, overflow = TextOverflow.Ellipsis,
                )
            }
            if (failedOnly) {
                TextButton(onClick = onRetry) { Text("Повторить", color = Accent) }
            } else {
                Icon(Icons.Rounded.ChevronRight, "Загрузки", tint = TextFaint)
            }
        }
        if (!failedOnly) {
            Spacer(Modifier.height(10.dp))
            DownloadBar(if (state.running) dl else null)
        }
    }
}

/** Отметка у серии: качается (кольцо с процентами) или ждёт в очереди. */
@Composable
fun EpisodeDownloadMark(current: Boolean, percent: Int) {
    if (current) {
        if (percent > 0) CircularProgressIndicator(
            progress = { percent / 100f },
            modifier = Modifier.size(15.dp), color = Accent, strokeWidth = 2.dp,
            trackColor = Color(0x33FFFFFF),
        ) else CircularProgressIndicator(Modifier.size(15.dp), color = Accent, strokeWidth = 2.dp)
    } else {
        Icon(Icons.Rounded.Schedule, "В очереди", tint = TextFaint, modifier = Modifier.size(16.dp))
    }
}

