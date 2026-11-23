package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.EngineStatus
import ru.sorewa.kino.data.describeApiError
import ru.sorewa.kino.engine.AppRestart
import ru.sorewa.kino.engine.EngineLogFormat
import ru.sorewa.kino.engine.EngineSettings
import ru.sorewa.kino.engine.EngineSettingsStore
import ru.sorewa.kino.engine.EngineSources
import ru.sorewa.kino.engine.LocalEngine
import ru.sorewa.kino.engine.validateProxy
import ru.sorewa.kino.ui.Overline
import ru.sorewa.kino.ui.Panel
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.SecondaryButton
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Danger
import ru.sorewa.kino.ui.theme.Gold
import ru.sorewa.kino.ui.theme.OnAccent
import ru.sorewa.kino.ui.theme.Surface1
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain
import java.io.File

/**
 * Настройки «серверной» части — локального движка: какие источники включены,
 * ключи к ним, прокси, журнал, кэш и сброс данных. Изменения копятся в черновике
 * и вступают в силу кнопкой «Применить»: движок перезапускается и читает
 * настройки заново.
 */
@Composable
fun EngineSettingsScreen(nav: NavHostController) {
    val container = remember { KinoApp.instance.container }
    val engine = container.engine
    val context = LocalContext.current
    if (engine == null) {
        // Экран про движок, а движка нет (режим сервера): сюда не попадают, но на всякий случай.
        LaunchedEffect(Unit) { nav.popBackStack() }
        return
    }
    val scope = rememberCoroutineScope()
    val clipboard = LocalClipboardManager.current

    var saved by remember { mutableStateOf(EngineSettingsStore.load(context)) }
    var draft by remember { mutableStateOf(saved) }
    val state by engine.state.collectAsStateWithLifecycle()
    var status by remember { mutableStateOf<EngineStatus?>(null) }
    var statusErr by remember { mutableStateOf("") }
    var refresh by remember { mutableIntStateOf(0) }
    var log by remember { mutableStateOf<List<String>>(emptyList()) }
    var applying by remember { mutableStateOf(false) }
    var confirmReset by remember { mutableStateOf(false) }
    var kinopubLogin by remember { mutableStateOf(false) }
    var sourcesOpen by remember { mutableStateOf(false) }
    // Список источников — у движка: свои и присланные площадки появляются в нём сами.
    var sourcesRev by remember { mutableIntStateOf(0) }
    var busyNote by remember { mutableStateOf("") }

    val proxyError = validateProxy(draft.proxy)
    val dirty = draft != saved
    val canApply = dirty && proxyError == null && !userAgentHasError(draft) && !applying

    // Площадки подключаются в фоне и с повторами: пока какая-то «подключается»,
    // спрашиваем статус снова — иначе экран застыл бы на «подключается…».
    LaunchedEffect(state, refresh) {
        if (state != LocalEngine.State.READY) { status = null; return@LaunchedEffect }
        repeat(10) {
            try {
                val s = container.api.engineStatus()
                status = s; statusErr = ""
                if (s.providers.none { it.state == "starting" }) return@LaunchedEffect
            } catch (e: Exception) {
                statusErr = describeApiError(e)
            }
            delay(1500)
        }
    }
    LaunchedEffect(state, refresh) {
        log = withContext(Dispatchers.IO) { readLog(context) }
    }
    LaunchedEffect(state, refresh) {
        if (state != LocalEngine.State.READY) return@LaunchedEffect
        try {
            EngineSources.remember(context, container.api.engineSources().sources)
            sourcesRev++
        } catch (_: Exception) {
            // Старый движок без /engine/sources — остаётся вшитый список.
        }
    }

    fun apply() {
        if (!canApply) return
        scope.launch {
            applying = true
            withContext(Dispatchers.IO) { EngineSettingsStore.save(context, draft) }
            saved = draft
            engine.restart()
            engine.awaitReady(25_000)
            refresh++
            applying = false
        }
    }

    Column(Modifier.fillMaxSize().statusBarsPadding()) {
        Row(Modifier.padding(horizontal = 8.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Назад") }
            Text("Локальный движок", style = MaterialTheme.typography.headlineMedium)
        }

        Column(
            Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            // ---- состояние ----
            Panel {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text(stateTitle(state), style = MaterialTheme.typography.titleMedium, color = stateColor(state))
                        Text(
                            "127.0.0.1:${engine.port} · только это устройство",
                            style = MaterialTheme.typography.bodySmall, color = TextDim,
                        )
                    }
                    SecondaryButton("Перезапустить", enabled = !applying) {
                        scope.launch { engine.restart(); engine.awaitReady(25_000); refresh++ }
                    }
                }
                if (state == LocalEngine.State.FAILED) {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        "Движок не удалось удержать. Причина — в журнале ниже; чаще всего её подсказывает последняя строка.",
                        style = MaterialTheme.typography.bodySmall, color = Danger,
                    )
                }
            }

            // Настройки движка — для тех, кто понимает, что делает: по умолчанию всё работает.
            Text(
                "Настройки ниже чувствительные: неверное значение может сломать площадку, привести к блокировке " +
                    "вашего адреса или аккаунта. По умолчанию всё настроено бережно и менять ничего не нужно.",
                style = MaterialTheme.typography.bodySmall, color = TextDim, modifier = Modifier.padding(horizontal = 8.dp),
            )

            // ---- источники ----
            Overline("Источники", Modifier.padding(start = 8.dp, top = 6.dp))
            Text(
                "Каждый включённый источник — лишняя нагрузка на чужой сайт. Включайте то, чем пользуетесь.",
                style = MaterialTheme.typography.bodySmall, color = TextDim, modifier = Modifier.padding(horizontal = 8.dp),
            )
            Panel(padding = androidx.compose.foundation.layout.PaddingValues(vertical = 6.dp)) {
                // Перечитать список, когда движок ответил (свои площадки появляются в нём сами).
                val sourceList = remember(sourcesRev) { EngineSources.all }
                sourceList.forEach { src ->
                    val on = src.id in draft.providers
                    val lastOn = on && draft.providers.count { it in EngineSources.all.map { s -> s.id } } <= 1
                    Column(Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 8.dp)) {
                        Row(
                            Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp))
                                .clickable(enabled = !lastOn) {
                                    draft = draft.copy(providers = if (on) draft.providers - src.id else draft.providers + src.id)
                                },
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(src.title, style = MaterialTheme.typography.bodyLarge, color = TextMain)
                                Text(src.note, style = MaterialTheme.typography.bodySmall, color = TextDim)
                                if (src.origin == "local" || src.origin == "bundled") {
                                    Text(
                                        (if (src.origin == "local") "ваш скрипт" else "скрипт") +
                                            if (src.version.isNotEmpty()) " ${src.version}" else "",
                                        style = MaterialTheme.typography.bodySmall, color = TextFaint,
                                    )
                                }
                                SourceStatus(src.id, on, saved, status, state)
                            }
                            Switch(
                                checked = on,
                                enabled = !lastOn,
                                onCheckedChange = { draft = draft.copy(providers = if (it) draft.providers + src.id else draft.providers - src.id) },
                                colors = SwitchDefaults.colors(checkedTrackColor = Accent, checkedThumbColor = OnAccent),
                            )
                        }
                        if (on && src.id == "kinopub") {
                            Spacer(Modifier.height(8.dp))
                            KinopubAccess(
                                draft = draft, onDraft = { draft = it }, saved = saved, status = status,
                                engineReady = state == LocalEngine.State.READY,
                                onLogin = { kinopubLogin = true },
                                onLogout = {
                                    scope.launch {
                                        try { container.api.kinopubLogout() } catch (_: Exception) { }
                                        engine.restart(); engine.awaitReady(25_000); refresh++
                                    }
                                },
                            )
                        }
                        if (on && src.keyEnv != null) {
                            Spacer(Modifier.height(6.dp))
                            OutlinedTextField(
                                value = draft.keys[src.id].orEmpty(),
                                onValueChange = { draft = draft.copy(keys = draft.keys + (src.id to it)) },
                                label = { Text(src.keyLabel ?: "Ключ") },
                                singleLine = true,
                                visualTransformation = PasswordVisualTransformation(),
                                modifier = Modifier.fillMaxWidth(),
                            )
                        }
                    }
                }
            }
            if (draft.providers.size <= 1) {
                Text(
                    "Последний источник выключить нельзя: без источников искать нечего.",
                    style = MaterialTheme.typography.bodySmall, color = TextFaint, modifier = Modifier.padding(horizontal = 8.dp),
                )
            }
            SecondaryButton("Площадки: добавить, проверить, отправить нам", Modifier.fillMaxWidth(), enabled = !applying) {
                sourcesOpen = true
            }

            // ---- сеть ----
            Overline("Сеть и VPN", Modifier.padding(start = 8.dp, top = 6.dp))
            Panel {
                Text(
                    "Если у телефона включён VPN, движок пойдёт через него сам — тогда это поле пустое. " +
                        "Если нужной страны нет, укажите прокси: socks5://хост:порт или http://хост:порт. " +
                        "Он применяется ко всем источникам. Следить, чтобы он работал и стоял в нужной стране, — вам.",
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                )
                Spacer(Modifier.height(10.dp))
                OutlinedTextField(
                    value = draft.proxy,
                    onValueChange = { draft = draft.copy(proxy = it) },
                    label = { Text("Прокси (необязательно)") },
                    singleLine = true,
                    isError = proxyError != null,
                    supportingText = { if (proxyError != null) Text(proxyError) },
                    modifier = Modifier.fillMaxWidth(),
                )
            }

            // ---- запросы ----
            Overline("Запросы к площадкам", Modifier.padding(start = 8.dp, top = 6.dp))
            Panel {
                Text(
                    "Движок сам ничего не обходит и в фоне не ходит на площадки: они спрашиваются только когда " +
                        "вы ищете, открываете тайтл или смотрите. Всё найденное запоминается насовсем: повторный " +
                        "поиск и повторное открытие идут из памяти устройства, без запросов.",
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                )
                Spacer(Modifier.height(14.dp))
                Text("Обновлять тайтл не чаще, чем раз в", style = MaterialTheme.typography.bodyLarge)
                Text(
                    "Новые серии появятся, когда вы откроете тайтл и с прошлого раза пройдёт больше этого срока.",
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                )
                Spacer(Modifier.height(6.dp))
                ChipChoices(
                    EngineSettings.REFRESH_CHOICES.map { it to if (it >= 24) "${it / 24} дн" else "$it ч" },
                    draft.refreshHours,
                ) { draft = draft.copy(refreshHours = it) }
                Spacer(Modifier.height(14.dp))
                Text("Находок с одной площадки при поиске", style = MaterialTheme.typography.bodyLarge)
                Text(
                    "Меньше — меньше запросов. Спрашивается лучшая площадка, остальные — только если у неё нашлось мало.",
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                )
                Spacer(Modifier.height(6.dp))
                ChipChoices(
                    EngineSettings.SEARCH_PER_SOURCE_CHOICES.map { it to "$it" },
                    draft.searchPerSource,
                ) { draft = draft.copy(searchPerSource = it) }
                Spacer(Modifier.height(10.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text("Кадры серий", style = MaterialTheme.typography.bodyLarge)
                        Text(
                            "Красиво, но это отдельный запрос на каждую серию: на сериале в сотню серий — сотня запросов.",
                            style = MaterialTheme.typography.bodySmall, color = TextDim,
                        )
                    }
                    Switch(
                        checked = draft.thumbs,
                        onCheckedChange = { draft = draft.copy(thumbs = it) },
                        colors = SwitchDefaults.colors(checkedTrackColor = Accent, checkedThumbColor = OnAccent),
                    )
                }
                val req = status?.requests
                if (req != null) {
                    Spacer(Modifier.height(12.dp))
                    Text(
                        "Запросов на площадки с запуска движка: ${req.total}",
                        style = MaterialTheme.typography.bodyMedium, color = TextMain,
                    )
                    if (req.hosts.isNotEmpty()) {
                        Text(
                            req.hosts.take(5).joinToString(" · ") { "${it.host} ${it.count}" },
                            style = MaterialTheme.typography.bodySmall, color = TextDim,
                        )
                    }
                }
            }

            // ---- дополнительно ----
            Overline("Дополнительно", Modifier.padding(start = 8.dp, top = 6.dp))
            Panel {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text("Подробный журнал", style = MaterialTheme.typography.bodyLarge)
                        Text("Для разбора, почему источник молчит. Журнал растёт быстрее.", style = MaterialTheme.typography.bodySmall, color = TextDim)
                    }
                    Switch(
                        checked = draft.debugLog,
                        onCheckedChange = { draft = draft.copy(debugLog = it) },
                        colors = SwitchDefaults.colors(checkedTrackColor = Accent, checkedThumbColor = OnAccent),
                    )
                }
                Spacer(Modifier.height(14.dp))
                Text("Кэш постеров", style = MaterialTheme.typography.bodyLarge)
                Text(
                    "Один раз полученное не качается заново, пока помещается в кэш.",
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                )
                Spacer(Modifier.height(6.dp))
                ChipChoices(
                    EngineSettings.CACHE_CHOICES.map { it to if (it >= 1024) "${it / 1024} ГБ" else "$it МБ" },
                    draft.artCacheMb,
                ) { draft = draft.copy(artCacheMb = it) }
                Spacer(Modifier.height(10.dp))
                SecondaryButton("Очистить кэш постеров", enabled = !applying) {
                    scope.launch {
                        busyNote = "Очищаю кэш…"
                        engine.stop()
                        withContext(Dispatchers.IO) { File(context.cacheDir, "engine/art").deleteRecursively() }
                        engine.start()
                        engine.awaitReady(25_000)
                        busyNote = "Кэш очищен"
                    }
                }
                if (busyNote.isNotEmpty()) Text(busyNote, style = MaterialTheme.typography.bodySmall, color = TextDim, modifier = Modifier.padding(top = 6.dp))
            }

            // ---- для опытных: подпись (User-Agent) ----
            Overline("Для опытных: User-Agent", Modifier.padding(start = 8.dp, top = 6.dp))
            Panel { UserAgentSettings(draft = draft, onDraft = { draft = it }, status = status) }

            // ---- журнал ----
            Overline("Журнал движка", Modifier.padding(start = 8.dp, top = 6.dp))
            Panel {
                if (log.isEmpty()) {
                    Text("Пока пусто.", style = MaterialTheme.typography.bodySmall, color = TextDim)
                } else {
                    SelectionContainer {
                        Text(
                            log.joinToString("\n"),
                            fontFamily = FontFamily.Monospace, fontSize = 11.sp, lineHeight = 15.sp, color = TextDim,
                        )
                    }
                }
                Spacer(Modifier.height(10.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    SecondaryButton("Обновить", Modifier.weight(1f)) { refresh++ }
                    SecondaryButton("Скопировать", Modifier.weight(1f), enabled = log.isNotEmpty()) {
                        clipboard.setText(AnnotatedString(log.joinToString("\n")))
                    }
                }
            }

            // ---- что важно знать ----
            Overline("Что важно знать", Modifier.padding(start = 8.dp, top = 6.dp))
            Panel { LocalModeNoticeBody() }

            // ---- данные ----
            Overline("Данные", Modifier.padding(start = 8.dp, top = 6.dp))
            Panel {
                Text(
                    "Закладки, прогресс и история лежат только на этом устройстве. Сброс стирает их безвозвратно: " +
                        "резервной копии нет, и никто их не восстановит.",
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                )
                Spacer(Modifier.height(10.dp))
                SecondaryButton("Сбросить данные движка", enabled = !applying) { confirmReset = true }
            }
            Spacer(Modifier.height(24.dp))
        }

        if (dirty) {
            Column(Modifier.fillMaxWidth().background(Surface1).navigationBarsPadding().padding(16.dp)) {
                PrimaryButton(
                    if (applying) "Применяю…" else "Применить и перезапустить движок",
                    Modifier.fillMaxWidth(), busy = applying, enabled = canApply,
                ) { apply() }
                if (proxyError != null) Text(proxyError, color = Danger, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 6.dp))
            }
        }
    }

    if (sourcesOpen) {
        EngineSourcesDialog(
            api = container.api,
            engineReady = state == LocalEngine.State.READY,
            onRestart = { engine.restart(); engine.awaitReady(25_000); refresh++ },
            onDismiss = { sourcesOpen = false; refresh++ },
        )
    }

    if (kinopubLogin) {
        KinopubLoginDialog(container.api) { ok ->
            kinopubLogin = false
            // Сессия записана движком в файл; провайдеру нужен перезапуск, чтобы подняться с ней.
            if (ok) scope.launch { engine.restart(); engine.awaitReady(25_000); refresh++ }
        }
    }

    if (confirmReset) {
        AlertDialog(
            onDismissRequest = { confirmReset = false },
            title = { Text("Сбросить данные движка?") },
            text = {
                Text(
                    "Будут стёрты закладки, прогресс, история и найденные тайтлы на этом устройстве. " +
                        "Восстановить их нельзя: ни сервера, ни резервной копии нет. Приложение перезапустится.",
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    confirmReset = false
                    scope.launch {
                        withContext(Dispatchers.IO) {
                            engine.stop()
                            // База движка (и её журнал WAL) и всё, что в приложении ссылается на её id:
                            // очередь отметок, кэши карточек (профиль files/local), кэш постеров.
                            File(context.filesDir, "engine").listFiles()
                                ?.filter { it.name.startsWith("kino-engine.db") }?.forEach { it.delete() }
                            File(context.filesDir, "local").deleteRecursively()
                            File(context.cacheDir, "engine/art").deleteRecursively()
                        }
                        AppRestart.restart(context)
                    }
                }) { Text("Стереть", color = Danger) }
            },
            dismissButton = { TextButton(onClick = { confirmReset = false }) { Text("Отмена") } },
        )
    }
}

/** Ряд вариантов-«пилюль»: выбран один. */
@Composable
private fun ChipChoices(options: List<Pair<Int, String>>, selected: Int, onSelect: (Int) -> Unit) {
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        options.forEach { (value, label) ->
            val sel = value == selected
            Box(
                Modifier.clip(RoundedCornerShape(50))
                    .background(if (sel) Accent else Surface1)
                    .clickable { onSelect(value) }
                    .padding(horizontal = 14.dp, vertical = 8.dp),
            ) { Text(label, color = if (sel) OnAccent else TextMain, fontSize = 13.sp) }
        }
    }
}

@Composable
private fun SourceStatus(id: String, on: Boolean, saved: EngineSettings, status: EngineStatus?, engine: LocalEngine.State) {
    if (!on) return
    // Статус относится к тому, что применено: у только что включённого источника его ещё нет.
    if (id !in saved.providers) {
        Text("Включится после применения", style = MaterialTheme.typography.bodySmall, color = TextFaint)
        return
    }
    val st = status?.providers?.firstOrNull { it.id == id }
    val (text, color) = when {
        engine != LocalEngine.State.READY -> "Движок не работает" to TextFaint
        st == null -> "Проверяю…" to TextFaint
        st.state == "ready" -> "Работает" to Accent
        st.state == "starting" -> "Подключается…" to TextDim
        else -> "Отключён: ${st.reason.ifEmpty { "причина не названа" }}" to Gold
    }
    Text(text, style = MaterialTheme.typography.bodySmall, color = color, modifier = Modifier.padding(top = 2.dp))
}

private fun stateTitle(s: LocalEngine.State) = when (s) {
    LocalEngine.State.READY -> "Работает"
    LocalEngine.State.STARTING -> "Запускается…"
    LocalEngine.State.FAILED -> "Не запустился"
    LocalEngine.State.STOPPED -> "Остановлен"
}

private fun stateColor(s: LocalEngine.State) = when (s) {
    LocalEngine.State.READY -> Accent
    LocalEngine.State.FAILED -> Danger
    else -> TextMain
}

/** Хвост журнала движка: читаем не больше последних 64 КБ — файл может быть большим. */
private fun readLog(context: android.content.Context): List<String> {
    val f = File(context.filesDir, "engine/engine.log")
    if (!f.isFile) return emptyList()
    return try {
        val len = f.length()
        val text = java.io.RandomAccessFile(f, "r").use { raf ->
            val from = (len - 65_536).coerceAtLeast(0)
            raf.seek(from)
            val b = ByteArray((len - from).toInt())
            raf.readFully(b)
            // Начало могло упасть посреди строки — первую (обрезанную) выбрасываем.
            String(b, Charsets.UTF_8).let { if (from > 0) it.substringAfter('\n', "") else it }
        }
        EngineLogFormat.tail(text, 60)
    } catch (_: Exception) {
        emptyList()
    }
}
