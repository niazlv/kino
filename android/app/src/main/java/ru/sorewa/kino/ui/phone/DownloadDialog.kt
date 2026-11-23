package ru.sorewa.kino.ui.phone

import android.Manifest
import android.content.Intent
import android.net.Uri
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.DownloadForOffline
import androidx.compose.material.icons.rounded.Folder
import androidx.compose.material.icons.rounded.Schedule
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.documentfile.provider.DocumentFile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import ru.sorewa.kino.BuildConfig
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.Episode
import ru.sorewa.kino.data.LocalLibrary
import ru.sorewa.kino.data.Prefs
import ru.sorewa.kino.data.VoiceOption
import ru.sorewa.kino.player.Voice
import ru.sorewa.kino.downloader.DownloadJob
import ru.sorewa.kino.downloader.DownloadService
import ru.sorewa.kino.downloader.Downloader
import ru.sorewa.kino.ui.Overline
import ru.sorewa.kino.ui.Pill
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.plural
import ru.sorewa.kino.ui.splitTitle
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.OnAccent
import ru.sorewa.kino.ui.theme.Surface1
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain

/**
 * Скачивание: эта серия, сезон, весь сериал или серии на выбор галочками
 * (как у Kino.Watch), качество и озвучка. Озвучка — из общего списка серии
 * (/episodes/{id}/voices), сразу та, что человек слушает в тайтле ([prefs]):
 * у kinopub студии — дорожки одного потока и в файл попадают все, а у
 * остальных площадок студия — свой поток, и без выбора скачивалась бы
 * первая попавшаяся.
 *
 * Скачанное ([downloaded]) и уже стоящее в загрузках не качается второй
 * раз: шторка говорит, сколько уже есть, и считает только недостающее.
 */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun DownloadDialog(
    titleId: Long,
    titleName: String,
    seasonNo: Int,
    episodeNo: Int,
    episodeId: Long,
    seasonsCount: Int,
    episodes: List<Episode> = emptyList(),
    /** Уже на телефоне — «сезон × серия» из описи скачанного. */
    downloaded: Set<EpKey> = emptySet(),
    /** Открыли из меню сезона: сразу весь сезон, а не его первая серия. */
    wholeSeason: Boolean = false,
    /** Что человек выбрал в плеере для тайтла: озвучка и площадка. */
    prefs: Prefs? = null,
    onDismiss: () -> Unit,
) {
    val container = remember { KinoApp.instance.container }
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val movie = episodes.size <= 1

    // У фильма номер в описи («S01E01» от утилиты) может не совпасть с
    // номером серии в карточке — фильм скачан, если в описи есть хоть что-то.
    val have = remember(downloaded, movie) {
        if (movie && downloaded.isNotEmpty()) downloaded + (seasonNo to episodeNo) else downloaded
    }
    // Очередь, текущее задание и пауза — то, что и так скачается.
    val queued = remember(titleId, episodes) {
        val jobs = Downloader.loadQueue(context) +
            listOfNotNull(DownloadService.status.value.running, Downloader.pausedJob(context))
        inFlightKeys(jobs, titleId, episodes)
    }
    fun blocked(k: EpKey) = k in have || k in queued

    var scopeKind by remember { mutableStateOf(if (wholeSeason && !movie) "season" else "episode") } // episode | season | all | pick
    var quality by remember { mutableStateOf("") }          // "" = лучшее
    var renditions by remember { mutableStateOf<List<Int>>(emptyList()) }
    // Озвучки серии: null — список ещё едет (площадки отвечают до 8 с).
    var voices by remember { mutableStateOf<List<VoiceOption>?>(null) }
    var voice by remember { mutableStateOf<VoiceOption?>(null) }   // null — как выберет площадка
    var error by remember { mutableStateOf("") }
    var folder by remember { mutableStateOf(folderLabel(context)) }
    // Выбор галочками: по умолчанию — та серия, с которой открыли (если её
    // ещё нет на телефоне).
    var picked by remember {
        mutableStateOf(episodes.filter { it.id == episodeId && !blocked(it.seasonNo to it.episodeNo) }.map { it.id }.toSet())
    }

    val treeLauncher = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
        if (uri != null) {
            context.contentResolver.takePersistableUriPermission(
                uri,
                Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
            )
            context.getSharedPreferences("downloads", android.content.Context.MODE_PRIVATE)
                .edit().putString("tree", uri.toString()).apply()
            folder = folderLabel(context)
        }
    }

    // Озвучки — общим списком со всех площадок серии; сразу — запомненная.
    LaunchedEffect(episodeId) {
        val list = try {
            container.api.voices(episodeId).voices
        } catch (_: Exception) {
            emptyList() // без списка — как выберет площадка
        }
        voices = list
        if (voice == null) voice = rememberedVoice(list, prefs?.audio, Voice::key)
    }

    // Качества серии — из манифеста скачивания.
    LaunchedEffect(episodeId) {
        try {
            val m = container.api.manifest(episodeId)
            renditions = m.renditions.map { it.height }.distinct().sortedDescending()
        } catch (_: Exception) {
            // Без манифеста — качество «лучшее».
        }
    }

    // Серии выбранного охвата и то, что из них действительно качать.
    val inScope: List<EpKey> = when (scopeKind) {
        "episode" -> listOf(seasonNo to episodeNo)
        "season" -> episodes.filter { it.seasonNo == seasonNo }.map { it.seasonNo to it.episodeNo }
        "all" -> episodes.map { it.seasonNo to it.episodeNo }
        else -> episodes.filter { it.id in picked }.map { it.seasonNo to it.episodeNo }
    }
    val todo = inScope.filterNot { blocked(it) }
    val haveCount = inScope.count { it in have }
    val queuedCount = inScope.count { it !in have && it in queued }

    fun enqueue() {
        val list = downloadJobs(
            base = "${ru.sorewa.kino.KinoApp.instance.container.api.baseUrl}/#/title/$titleId",
            title = titleName, quality = quality, movie = movie,
            scopeKind = scopeKind, inScope = inScope, todo = todo,
            ids = episodes.associate { (it.seasonNo to it.episodeNo) to it.id } + ((seasonNo to episodeNo) to episodeId),
            voice = voice, source = prefs?.source.orEmpty(),
        )
        if (list.isEmpty()) { error = "Не выбрано ни одной серии"; return }
        scope.launch {
            val s = container.store.settings()
            val metered = context.getSystemService(android.net.ConnectivityManager::class.java)
                ?.isActiveNetworkMetered == true
            if (s.wifiOnly && metered) {
                error = "Включено «только Wi-Fi», а сеть лимитная"
                return@launch
            }
            withContext(Dispatchers.IO) {
                list.forEach { job ->
                    val intent = Intent(context, DownloadService::class.java)
                        .setAction(DownloadService.ACTION_ENQUEUE)
                        .putExtra(DownloadService.EXTRA_JOB, container.api.json.encodeToString(DownloadJob.serializer(), job))
                    if (Build.VERSION.SDK_INT >= 26) context.startForegroundService(intent) else context.startService(intent)
                }
            }
            onDismiss()
        }
    }

    val notifLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { _ ->
        enqueue()
    }

    val count = todo.size
    val buttonText = when {
        count > 0 -> if (movie) "Скачать" else "Скачать · $count ${plural(count, "серия", "серии", "серий")}"
        inScope.isEmpty() -> "Выберите серии"
        queuedCount > 0 -> "Уже в загрузках"
        movie -> "Фильм уже скачан"
        inScope.size == 1 -> "Серия уже скачана"
        else -> "Всё уже скачано"
    }
    // «Уже скачано 7 из 8» — у сезона и всего сериала; у одной серии и
    // фильма это говорит сама кнопка, у выбора — галочки.
    val haveNote = if (scopeKind != "season" && scopeKind != "all") "" else buildList {
        if (haveCount > 0) add("уже скачано $haveCount из ${inScope.size}")
        if (queuedCount > 0) add(if (haveCount > 0) "ещё $queuedCount в загрузках" else "$queuedCount из ${inScope.size} уже в загрузках")
    }.joinToString(", ").replaceFirstChar { it.uppercase() }

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = Surface1,
    ) {
        Column(
            Modifier
                .padding(horizontal = 20.dp)
                .verticalScroll(rememberScrollState()),
        ) {
            Text("Скачать", style = MaterialTheme.typography.titleLarge, color = TextMain)
            Text(splitTitle(titleName).first, style = MaterialTheme.typography.bodyMedium, color = TextDim, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Spacer(Modifier.height(16.dp))

            if (!movie) {
                Overline("Что скачать")
                Spacer(Modifier.height(8.dp))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Pill("Эту серию", scopeKind == "episode") { scopeKind = "episode" }
                    Pill(if (seasonsCount > 1) "Сезон $seasonNo" else "Все серии", scopeKind == "season") { scopeKind = "season" }
                    if (seasonsCount > 1) Pill("Весь сериал", scopeKind == "all") { scopeKind = "all" }
                    Pill("Выбрать серии", scopeKind == "pick") { scopeKind = "pick" }
                }
                if (haveNote.isNotEmpty()) {
                    Spacer(Modifier.height(10.dp))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        androidx.compose.material3.Icon(Icons.Rounded.DownloadForOffline, null, tint = Accent, modifier = Modifier.size(18.dp))
                        Spacer(Modifier.width(8.dp))
                        Text(haveNote, style = MaterialTheme.typography.bodyMedium, color = TextDim)
                    }
                }
                if (scopeKind == "pick") {
                    Spacer(Modifier.height(10.dp))
                    EpisodePicker(episodes, picked, have, queued) { picked = it }
                }
                Spacer(Modifier.height(16.dp))
            }

            Overline("Качество")
            Spacer(Modifier.height(8.dp))
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Pill("Лучшее" + (renditions.maxOrNull()?.let { " · ${it}p" } ?: ""), quality.isEmpty()) { quality = "" }
                renditions.filter { it > 0 }.drop(1).forEach { h ->
                    Pill("до ${h}p", quality == "${h}p") { quality = "${h}p" }
                }
            }

            val vs = voices
            if (vs == null || vs.isNotEmpty()) {
                Spacer(Modifier.height(16.dp))
                Overline("Озвучка")
                Spacer(Modifier.height(8.dp))
                if (vs == null) {
                    Text("Узнаём, какие есть…", style = MaterialTheme.typography.bodySmall, color = TextFaint)
                } else {
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Pill("Авто", voice == null) { voice = null }
                        vs.forEach { v -> Pill(v.title, voice?.key == v.key) { voice = v } }
                    }
                    Spacer(Modifier.height(6.dp))
                    Text(
                        voiceHint(voice, manyEpisodes = !movie && scopeKind != "episode"),
                        style = MaterialTheme.typography.bodySmall, color = TextFaint,
                    )
                }
            }

            Spacer(Modifier.height(12.dp))
            Row(
                Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(12.dp))
                    .clickable { treeLauncher.launch(null) }
                    .padding(vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                androidx.compose.material3.Icon(Icons.Rounded.Folder, null, tint = TextDim)
                Spacer(Modifier.width(12.dp))
                Column(Modifier.weight(1f)) {
                    Text("Папка", style = MaterialTheme.typography.bodyMedium, color = TextMain)
                    Text("$folder · файл .mkv", style = MaterialTheme.typography.bodySmall, color = TextFaint)
                }
                Text("Сменить", color = Accent, style = MaterialTheme.typography.labelLarge)
            }

            if (!Downloader.hasFFmpeg(context)) {
                Text("Утилита скачивания недоступна на этом устройстве", color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            if (error.isNotEmpty()) {
                Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }

            Spacer(Modifier.height(12.dp))
            PrimaryButton(
                text = buttonText,
                icon = Icons.Rounded.Download,
                enabled = Downloader.hasFFmpeg(context) && count > 0,
                modifier = Modifier.fillMaxWidth(),
            ) {
                error = ""
                if (Build.VERSION.SDK_INT >= 33 &&
                    ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS)
                        != android.content.pm.PackageManager.PERMISSION_GRANTED
                ) {
                    notifLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
                } else {
                    enqueue()
                }
            }
            Spacer(Modifier.height(24.dp))
        }
    }
}

/**
 * Серии по сезонам с галочками; у сезона — «все/ни одной» одним касанием.
 * Скачанные и стоящие в загрузках — без галочки, со значком: выбрать их
 * нельзя, «весь сезон» их не трогает.
 */
@Composable
private fun EpisodePicker(
    episodes: List<Episode>,
    picked: Set<Long>,
    downloaded: Set<EpKey>,
    queued: Set<EpKey>,
    onChange: (Set<Long>) -> Unit,
) {
    val bySeason = episodes.groupBy { it.seasonNo }.toSortedMap()
    LazyColumn(Modifier.fillMaxWidth().heightIn(max = 360.dp)) {
        bySeason.forEach { (season, eps) ->
            val free = eps.filter { (it.seasonNo to it.episodeNo).let { k -> k !in downloaded && k !in queued } }
            val ids = free.map { it.id }.toSet()
            val all = ids.isNotEmpty() && picked.containsAll(ids)
            val haveCount = eps.count { (it.seasonNo to it.episodeNo) in downloaded }
            item(key = "s$season") {
                Row(
                    Modifier
                        .fillMaxWidth()
                        .clickable(enabled = ids.isNotEmpty()) { onChange(if (all) picked - ids else picked + ids) }
                        .padding(vertical = 4.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Checkbox(
                        checked = all,
                        enabled = ids.isNotEmpty(),
                        onCheckedChange = { onChange(if (all) picked - ids else picked + ids) },
                        colors = CheckboxDefaults.colors(checkedColor = Accent, checkmarkColor = OnAccent),
                    )
                    Text(
                        (if (season > 0) "Сезон $season" else "Серии") +
                            "  (${eps.count { it.id in picked }}/${free.size})" +
                            (if (haveCount > 0) " · скачано $haveCount" else ""),
                        style = MaterialTheme.typography.titleSmall,
                        color = TextMain,
                    )
                }
            }
            items(eps.sortedBy { it.episodeNo }, key = { it.id }) { ep ->
                val k = ep.seasonNo to ep.episodeNo
                val isHave = k in downloaded
                val isQueued = !isHave && k in queued
                val on = ep.id in picked
                Row(
                    Modifier
                        .fillMaxWidth()
                        .clickable(enabled = !isHave && !isQueued) { onChange(if (on) picked - ep.id else picked + ep.id) }
                        .padding(start = 24.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    if (isHave || isQueued) {
                        // На месте галочки (та же ширина 48 dp) — значок.
                        Box(Modifier.size(48.dp), contentAlignment = Alignment.Center) {
                            androidx.compose.material3.Icon(
                                if (isHave) Icons.Rounded.DownloadForOffline else Icons.Rounded.Schedule,
                                if (isHave) "Скачана" else "В загрузках",
                                tint = if (isHave) Accent else TextDim,
                                modifier = Modifier.size(20.dp),
                            )
                        }
                    } else {
                        Checkbox(
                            checked = on,
                            onCheckedChange = { onChange(if (on) picked - ep.id else picked + ep.id) },
                            colors = CheckboxDefaults.colors(checkedColor = Accent, checkmarkColor = OnAccent),
                        )
                    }
                    Text(
                        "${ep.episodeNo} серия" + when {
                            isHave -> " · скачана"
                            isQueued -> " · в загрузках"
                            else -> ep.title?.takeIf { it.isNotBlank() }?.let { " · $it" } ?: ""
                        },
                        style = MaterialTheme.typography.bodyMedium,
                        color = if (isHave || isQueued || ep.status == "completed") TextDim else TextMain,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
    }
}

/** Серия по номерам: «сезон × серия» — так её знает опись скачанного. */
typealias EpKey = Pair<Int, Int>

/**
 * Серии тайтла, которые уже стоят в загрузках (очередь, текущее задание,
 * пауза). Задание на сезон или весь тайтл раскрывается по списку серий.
 */
internal fun inFlightKeys(jobs: List<DownloadJob>, titleId: Long, episodes: List<Episode>): Set<EpKey> {
    if (titleId <= 0) return emptySet()
    val out = mutableSetOf<EpKey>()
    for (j in jobs) {
        if (LocalLibrary.titleIdOf(j.pageUrl) != titleId) continue
        val se = Regex("/s(\\d+)e(\\d+)$").find(j.pageUrl)
        val byId = Regex("/title/\\d+/(\\d+)$").find(j.pageUrl)?.groupValues?.get(1)?.toLongOrNull()
        when {
            se != null -> out += se.groupValues[1].toInt() to se.groupValues[2].toInt()
            byId != null -> episodes.firstOrNull { it.id == byId }?.let { out += it.seasonNo to it.episodeNo }
            j.seasons.isNotEmpty() -> {
                val ss = j.seasons.split(',').mapNotNull { it.trim().toIntOrNull() }.toSet()
                episodes.filter { it.seasonNo in ss }.forEach { out += it.seasonNo to it.episodeNo }
            }
            else -> episodes.forEach { out += it.seasonNo to it.episodeNo }
        }
    }
    return out
}

/**
 * Задания на недостающее ([todo] — серии охвата без скачанных и стоящих в
 * загрузках). Скачанное второй раз не ставится: состояние утилиты после
 * удачной загрузки стирается, и «Все серии» перекачали бы сезон целиком,
 * а в галерее легли бы дубли «… (1).mkv». Сезон, где не скачано ничего, —
 * одним заданием на сезон, весь сериал без скачанного — одним на тайтл;
 * остальное — по серии.
 *
 * Сезона 0 утилита по «sXeY» не принимает («s0e1» — неверная ссылка, и
 * прогон кончался ничем): фильм — ссылкой на тайтл, как у витрины, а
 * доп. серии сериала — по id серии ([ids]).
 */
internal fun downloadJobs(
    base: String,
    title: String,
    quality: String,
    movie: Boolean,
    scopeKind: String,
    inScope: List<EpKey>,
    todo: List<EpKey>,
    ids: Map<EpKey, Long> = emptyMap(),
    /** Озвучка (ключ и подпись) и площадка-предпочтение — во все задания. */
    voice: VoiceOption? = null,
    source: String = "",
): List<DownloadJob> {
    if (todo.isEmpty()) return emptyList()
    fun job(page: String, seasons: String = "", mv: Boolean = false) = DownloadJob(
        pageUrl = page, title = title, quality = quality, seasons = seasons, movie = mv,
        voice = voice?.key.orEmpty(), voiceTitle = voice?.title.orEmpty(), source = source,
    )
    fun ep(k: EpKey): DownloadJob {
        val page = when {
            movie -> base
            k.first == 0 -> ids[k]?.let { "$base/$it" } ?: "$base/s0e${k.second}"
            else -> "$base/s${k.first}e${k.second}"
        }
        return job(page, mv = movie)
    }
    val sorted = todo.distinct().sortedWith(compareBy({ it.first }, { it.second }))
    if (movie || (scopeKind != "season" && scopeKind != "all")) return sorted.map(::ep).distinctBy { it.pageUrl }
    if (scopeKind == "all" && sorted.size == inScope.distinct().size) {
        return listOf(job(base))
    }
    return sorted.groupBy { it.first }.flatMap { (s, eps) ->
        if (s > 0 && eps.size == inScope.distinct().count { it.first == s }) {
            listOf(job(base, seasons = s.toString()))
        } else {
            eps.map(::ep)
        }
    }
}

/**
 * Озвучка, которую шторка предлагает сразу: та, что человек слушает в этом
 * тайтле (запомненная подпись [remembered]). Подпись площадки и ключ общего
 * списка сводит [keyOf] — тот же разбор, что у плеера. Не нашлась — null:
 * выберет площадка.
 */
internal fun rememberedVoice(voices: List<VoiceOption>, remembered: String?, keyOf: (String) -> String): VoiceOption? {
    val r = remembered?.trim().orEmpty()
    if (r.isEmpty()) return null
    val k = keyOf(r)
    return voices.firstOrNull { it.key == k || it.key == r } ?: voices.firstOrNull { it.title.equals(r, ignoreCase = true) }
}

/**
 * Пояснение под выбором озвучки. У площадок, где студии — дорожки одного
 * потока (kinopub), файл получит их все; у остальных студия — свой поток, и
 * в файле будет одна она.
 */
internal fun voiceHint(voice: VoiceOption?, manyEpisodes: Boolean): String {
    if (voice == null) return "Как выберет площадка; с kinopub — все дорожки"
    val shared = voice.ways.any { it.variant.isNullOrEmpty() }
    val single = voice.ways.any { !it.variant.isNullOrEmpty() }
    val what = when {
        shared && !single -> "Все дорожки потока, эта — среди них"
        shared -> "Одна дорожка — эта; с kinopub — все его дорожки"
        else -> "В файле будет эта озвучка"
    }
    return if (manyEpisodes) "$what. Где её нет, серия придёт в той, что есть" else what
}

fun folderLabel(context: android.content.Context): String {
    val raw = context.getSharedPreferences("downloads", android.content.Context.MODE_PRIVATE)
        .getString("tree", null) ?: return "Фильмы → kino"
    return try {
        DocumentFile.fromTreeUri(context, Uri.parse(raw))?.name ?: "выбранная папка"
    } catch (_: Exception) {
        "выбранная папка"
    }
}

