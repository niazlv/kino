package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.rounded.Logout
import androidx.compose.material.icons.rounded.AdminPanelSettings
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.PhoneAndroid
import androidx.compose.material.icons.rounded.Folder
import androidx.compose.material.icons.rounded.HighQuality
import androidx.compose.material.icons.rounded.History
import androidx.compose.material.icons.rounded.Key
import androidx.compose.material.icons.rounded.Memory
import androidx.compose.material.icons.rounded.SkipNext
import androidx.compose.material.icons.rounded.SystemUpdate
import androidx.compose.material.icons.rounded.Tv
import androidx.compose.material.icons.rounded.FastForward
import androidx.compose.material.icons.rounded.Wifi
import androidx.compose.material.icons.rounded.Timer
import androidx.compose.material.icons.rounded.Swipe
import androidx.compose.material.icons.rounded.Insights
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
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import kotlinx.coroutines.launch
import ru.sorewa.kino.BuildConfig
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.TokenStore
import ru.sorewa.kino.player.Quality
import ru.sorewa.kino.ui.Avatar
import ru.sorewa.kino.ui.CenterSpinner
import ru.sorewa.kino.ui.LocalTv
import ru.sorewa.kino.ui.tvFocusRing
import ru.sorewa.kino.ui.Overline
import ru.sorewa.kino.ui.Panel
import ru.sorewa.kino.ui.Pill
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Danger
import ru.sorewa.kino.ui.theme.OnAccent
import ru.sorewa.kino.ui.theme.Surface3
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain

/**
 * Профиль и настройки: аккаунт, подключение устройств, плеер (качество,
 * кодеки, пропуски, автоследующая), загрузки, обновления, выход.
 */
@Composable
fun SettingsScreen(nav: NavHostController) {
    val container = remember { KinoApp.instance.container }
    val scope = rememberCoroutineScope()
    val context = androidx.compose.ui.platform.LocalContext.current
    val tv = LocalTv.current
    val session by container.store.sessionFlow.collectAsStateWithLifecycle(
        TokenStore.Session("", null, 0, 0, "", "", ""),
    )
    var settings by remember { mutableStateOf<TokenStore.Settings?>(null) }
    var changingPassword by remember { mutableStateOf(false) }
    var confirmLogout by remember { mutableStateOf(false) }
    var connectionDialog by remember { mutableStateOf(false) }
    var serverPicker by remember { mutableStateOf(false) }
    var updating by remember { mutableStateOf(false) }
    var updateNote by remember { mutableStateOf("") }

    LaunchedEffect(Unit) { settings = container.store.settings() }

    fun save(next: TokenStore.Settings) {
        settings = next
        scope.launch { container.store.saveSettings(next) }
    }

    val s = settings
    if (s == null) { CenterSpinner(); return }

    var folder by remember { mutableStateOf(folderLabel(context)) }
    val treeLauncher = androidx.activity.compose.rememberLauncherForActivityResult(
        androidx.activity.result.contract.ActivityResultContracts.OpenDocumentTree(),
    ) { uri ->
        if (uri != null) {
            context.contentResolver.takePersistableUriPermission(
                uri,
                android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION or
                    android.content.Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
            )
            context.getSharedPreferences("downloads", android.content.Context.MODE_PRIVATE)
                .edit().putString("tree", uri.toString()).apply()
            folder = folderLabel(context)
        }
    }

    Column(
        Modifier
            .fillMaxSize()
            .then(if (tv) Modifier else Modifier.statusBarsPadding())
            .verticalScroll(rememberScrollState()),
    ) {
        Row(Modifier.padding(horizontal = 8.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            if (!tv && nav.previousBackStackEntry != null) {
                IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Назад") }
            } else Spacer(Modifier.width(12.dp))
            Text("Профиль", style = MaterialTheme.typography.headlineMedium)
        }

        Column(Modifier.padding(horizontal = 16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            val name = ru.sorewa.kino.ui.Profile.name(session.displayName.ifEmpty { session.lastLogin.ifEmpty { "—" } })
            val login = ru.sorewa.kino.ui.Profile.me?.login ?: session.lastLogin
            Panel {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Avatar(name, size = 56.dp)
                    Spacer(Modifier.width(16.dp))
                    Column(Modifier.weight(1f)) {
                        Text(name, style = MaterialTheme.typography.titleLarge, color = TextMain)
                        Text(
                            listOf(login.takeIf { it.isNotEmpty() && it != name }?.let { "@$it" }, if (container.local) "на этом устройстве" else serverLabel(container.serverUrl, container.connection.serverName()))
                                .filterNotNull().joinToString(" · "),
                            style = MaterialTheme.typography.bodySmall, color = TextDim,
                        )
                    }
                }
            }

            Group {
                if (!tv && !container.local) NavRow(Icons.Rounded.Tv, "Подключить устройство", "Вход на телевизоре по коду") { nav.navigate("link") }
                NavRow(Icons.Rounded.History, "История просмотров") { nav.navigate("history") }
            }

            // Главный экран Android TV: «Продолжить просмотр» обновляется само,
            // канал «Новые серии» — только с согласия (системный диалог).
            if (tv && ru.sorewa.kino.tvhome.TvLauncher.supported(context)) {
                Overline("Главный экран", Modifier.padding(start = 8.dp, top = 6.dp))
                Group { HomeChannelRow() }
            }

            Overline("Плеер", Modifier.padding(start = 8.dp, top = 6.dp))
            Group {
                Choice(
                    Icons.Rounded.HighQuality, "Качество",
                    "Что заявлять площадкам. Телевизор тянет 4K, телефон в дороге — нет.",
                    listOf(1080 to "1080p", 1440 to "1440p", 2160 to "4K"), s.maxQuality,
                ) { save(s.copy(maxQuality = it)) }
                Choice(
                    Icons.Rounded.FastForward, "Заставки и титры", null,
                    listOf("off" to "Смотреть", "button" to "Кнопкой", "auto" to "Сами"), s.skipMode,
                ) { save(s.copy(skipMode = it)) }
                SwitchRow(Icons.Rounded.SkipNext, "Следующая серия сама", s.autonext) { save(s.copy(autonext = it)) }
                Choice(
                    Icons.Rounded.Timer, "Прятать панель через", null,
                    listOf(3 to "3 с", 5 to "5 с", 10 to "10 с", 0 to "Никогда"), s.hideAfterSec,
                ) { save(s.copy(hideAfterSec = it)) }
                if (!tv) SwitchRow(Icons.Rounded.Swipe, "Свайпы: яркость слева, громкость справа", s.swipes) { save(s.copy(swipes = it)) }
                SwitchRow(Icons.Rounded.Insights, "Сводка о потоке всегда", s.statsAlways) { save(s.copy(statsAlways = it)) }
            }

            Overline("Кодеки", Modifier.padding(start = 8.dp, top = 6.dp))
            Group {
                Choice(
                    Icons.Rounded.Memory, "HEVC",
                    if (Quality.detectHevc()) "Устройство умеет" else "Устройство не умеет",
                    listOf("" to "Сам", "on" to "Заявлять", "off" to "Нет"), s.hevcClaim,
                ) { save(s.copy(hevcClaim = it)) }
                Choice(
                    Icons.Rounded.Memory, "AV1",
                    if (Quality.detectAv1()) "Устройство умеет" else "Устройство не умеет",
                    listOf("" to "Сам", "on" to "Заявлять", "off" to "Нет"), s.av1Claim,
                ) { save(s.copy(av1Claim = it)) }
            }

            // На ТВ не скачивают, а выбора папки у Android TV нет вовсе
            // («нет приложения для этого») — раздел только на телефоне.
            if (!tv) {
                Overline("Загрузки", Modifier.padding(start = 8.dp, top = 6.dp))
                Group {
                    SwitchRow(Icons.Rounded.Wifi, "Только по Wi-Fi", s.wifiOnly) { save(s.copy(wifiOnly = it)) }
                    NavRow(Icons.Rounded.Folder, "Папка", folder) { treeLauncher.launch(null) }
                }
            }

            Overline(if (container.local) "Подключение" else "Аккаунт", Modifier.padding(start = 8.dp, top = 6.dp))
            Group {
                // Откуда приложение берёт данные: свой сервер или движок на этом
                // устройстве (ADR-0015). На телевизоре не переключается.
                if (!tv) NavRow(
                    if (container.local) Icons.Rounded.PhoneAndroid else Icons.Rounded.Cloud,
                    "Подключение",
                    if (container.local) "На этом устройстве, без сервера. Нажмите, чтобы вернуться к серверу"
                    else "Сервер. Нажмите, чтобы работать на этом устройстве",
                ) { connectionDialog = true }
                // «Серверная» часть локального режима настраивается здесь.
                if (!tv && container.local) NavRow(
                    Icons.Rounded.Dns, "Локальный движок",
                    "Источники, прокси и VPN, ключи, журнал, сброс данных",
                ) { nav.navigate("engine") }
                // Остальное — про аккаунт на сервере: у локального аккаунта нет ни пароля, ни выхода.
                if (!container.local) {
                    // Свой сервер: другой адрес — другой аккаунт и свои данные.
                    // На телевизоре тоже: он должен смотреть туда же, где телефон.
                    NavRow(
                        Icons.Rounded.Dns, "Сервер",
                        serverLabel(container.serverUrl, container.connection.serverName()).let { label ->
                            if (container.connection.serverName().isEmpty()) label else "$label · ${ru.sorewa.kino.engine.ServerAddress.display(container.serverUrl)}"
                        },
                    ) { serverPicker = true }
                    // Только админу и не на телевизоре: с пульта в браузерной
                    // админке делать нечего.
                    if (!tv && ru.sorewa.kino.ui.Profile.me?.role == "admin") {
                        NavRow(Icons.Rounded.AdminPanelSettings, "Админка", "В браузере, вход ключом или кодом") {
                            ru.sorewa.kino.util.Admin.open(context)
                        }
                    }
                    NavRow(Icons.Rounded.Key, "Сменить пароль") { changingPassword = true }
                }
                // Обновления — про само приложение, а не про аккаунт: идут с сервера выкладки (адрес зашит
                // при сборке) и в режиме «на этом устройстве» тоже. Сборке без адреса проверять нечего.
                if (BuildConfig.BASE_URL.isNotEmpty()) NavRow(
                    Icons.Rounded.SystemUpdate,
                    if (updating) "Проверяю…" else "Проверить обновления",
                    updateNote.ifEmpty { "Версия ${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})" },
                ) {
                    if (!updating) {
                        updating = true
                        updateNote = ""
                        scope.launch {
                            // Разовая проверка в обход 6-часового кеша.
                            updateNote = ru.sorewa.kino.util.Updater.checkNow(context)
                            updating = false
                        }
                    }
                }
                if (!container.local) {
                    NavRow(Icons.AutoMirrored.Rounded.Logout, "Выйти", tint = Danger, chevron = false) { confirmLogout = true }
                }
            }
            Spacer(Modifier.height(24.dp))
        }
    }

    if (connectionDialog) ConnectionDialog(toLocal = !container.local) { connectionDialog = false }
    if (serverPicker) ServerPickerDialog { serverPicker = false }

    if (confirmLogout) {
        AlertDialog(
            onDismissRequest = { confirmLogout = false },
            title = { Text("Выйти из аккаунта?") },
            text = { Text("Прогресс сохранится на сервере. Скачанное останется на устройстве.") },
            confirmButton = {
                TextButton(onClick = {
                    scope.launch {
                        container.auth.logout()
                        confirmLogout = false
                        restartToLogin()
                    }
                }, modifier = Modifier.tvFocusRing(RoundedCornerShape(50))) { Text("Выйти", color = Danger) }
            },
            dismissButton = { TextButton(onClick = { confirmLogout = false }, modifier = Modifier.tvFocusRing(RoundedCornerShape(50))) { Text("Отмена") } },
        )
    }

    if (changingPassword) {
        var old by remember { mutableStateOf("") }
        var new by remember { mutableStateOf("") }
        var err by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { changingPassword = false },
            title = { Text("Сменить пароль") },
            text = {
                Column {
                    OutlinedTextField(value = old, onValueChange = { old = it }, label = { Text("Старый") }, singleLine = true, visualTransformation = PasswordVisualTransformation())
                    Spacer(Modifier.height(8.dp))
                    OutlinedTextField(value = new, onValueChange = { new = it }, label = { Text("Новый") }, singleLine = true, visualTransformation = PasswordVisualTransformation())
                    if (err.isNotEmpty()) Text(err, color = MaterialTheme.colorScheme.error)
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    scope.launch {
                        try {
                            container.api.changePassword(old, new)
                            changingPassword = false
                        } catch (e: Exception) {
                            err = ru.sorewa.kino.data.describeApiError(e)
                        }
                    }
                }, modifier = Modifier.tvFocusRing(RoundedCornerShape(50))) { Text("Сменить") }
            },
            dismissButton = { TextButton(onClick = { changingPassword = false }, modifier = Modifier.tvFocusRing(RoundedCornerShape(50))) { Text("Отмена") } },
        )
    }
}

/** Канал «Новые серии» на главном экране ТВ: виден ли и как добавить. */
@Composable
private fun HomeChannelRow() {
    val context = androidx.compose.ui.platform.LocalContext.current
    val scope = rememberCoroutineScope()
    var state by remember { mutableStateOf<ru.sorewa.kino.tvhome.TvLauncher.ChannelState?>(null) }
    var busy by remember { mutableStateOf(false) }
    var checkKey by remember { androidx.compose.runtime.mutableIntStateOf(0) }
    LaunchedEffect(checkKey) { state = ru.sorewa.kino.tvhome.TvLauncher.channelState(context) }
    val ask = androidx.activity.compose.rememberLauncherForActivityResult(
        androidx.activity.result.contract.ActivityResultContracts.StartActivityForResult(),
    ) { checkKey++ }
    val shown = state == ru.sorewa.kino.tvhome.TvLauncher.ChannelState.SHOWN
    NavRow(
        Icons.Rounded.Tv,
        "Канал «Новые серии»",
        when {
            busy -> "Готовлю канал…"
            state == null -> null
            shown -> "На главном экране. Убрать — в настройках каналов Android TV"
            else -> "Нет на главном экране — нажмите, чтобы добавить"
        },
        chevron = !shown,
    ) {
        if (busy || shown || android.os.Build.VERSION.SDK_INT < android.os.Build.VERSION_CODES.O) return@NavRow
        busy = true
        scope.launch {
            // Канал заводится с карточками, чтобы на главном экране он сразу был не пустым.
            val synced = try {
                ru.sorewa.kino.tvhome.TvLauncher.sync(context, KinoApp.instance.container.api)
            } catch (_: Exception) { null }
            busy = false
            if (synced != null && synced.channelId > 0) {
                ru.sorewa.kino.tvhome.TvLauncher.askToShow(context, ask, synced.channelId)
            }
            checkKey++
        }
    }
}

@Composable
private fun Group(content: @Composable ColumnScope.() -> Unit) =
    Panel(padding = PaddingValues(vertical = 6.dp), content = content)

@Composable
private fun RowIcon(icon: ImageVector, tint: Color = TextMain) {
    Box(
        Modifier.size(36.dp).background(Surface3, RoundedCornerShape(10.dp)),
        contentAlignment = Alignment.Center,
    ) { Icon(icon, null, tint = tint, modifier = Modifier.size(20.dp)) }
}

@Composable
private fun NavRow(
    icon: ImageVector,
    title: String,
    sub: String? = null,
    tint: Color = TextMain,
    chevron: Boolean = true,
    onClick: () -> Unit,
) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .tvFocusRing()
            .clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RowIcon(icon, tint)
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge, color = tint)
            if (!sub.isNullOrEmpty()) Text(sub, style = MaterialTheme.typography.bodySmall, color = TextDim, maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
        if (chevron) Icon(Icons.AutoMirrored.Rounded.KeyboardArrowRight, null, tint = TextFaint)
    }
}

@Composable
private fun SwitchRow(icon: ImageVector, title: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .tvFocusRing()
            .clickable { onChange(!checked) }
            .padding(horizontal = 14.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RowIcon(icon)
        Spacer(Modifier.width(14.dp))
        Text(title, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
        // ТВ: переключает вся строка; сам Switch — не вторая цель пульта
        // (стрелка вправо вставала на него, а фокус на нём не виден).
        Switch(
            checked = checked,
            onCheckedChange = if (LocalTv.current) null else onChange,
            colors = SwitchDefaults.colors(checkedTrackColor = Accent, checkedThumbColor = OnAccent),
        )
    }
}

@Composable
private fun <T> Choice(
    icon: ImageVector,
    title: String,
    sub: String?,
    options: List<Pair<T, String>>,
    selected: T,
    onSelect: (T) -> Unit,
) {
    Column(Modifier.padding(horizontal = 14.dp, vertical = 10.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            RowIcon(icon)
            Spacer(Modifier.width(14.dp))
            Column {
                Text(title, style = MaterialTheme.typography.bodyLarge)
                if (!sub.isNullOrEmpty()) Text(sub, style = MaterialTheme.typography.bodySmall, color = TextDim)
            }
        }
        Spacer(Modifier.height(10.dp))
        Row(Modifier.padding(start = 50.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            options.forEach { (id, label) -> Pill(label, selected = id == selected) { onSelect(id) } }
        }
    }
}

private fun restartToLogin() {
    // Простой способ вернуть приложение на экран входа: перезапуск активности.
    val ctx = KinoApp.instance
    val intent = ctx.packageManager.getLaunchIntentForPackage(ctx.packageName)
    intent?.addFlags(
        android.content.Intent.FLAG_ACTIVITY_NEW_TASK or android.content.Intent.FLAG_ACTIVITY_CLEAR_TASK,
    )
    if (intent != null) ctx.startActivity(intent)
}
