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
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.LockOpen
import androidx.compose.material.icons.rounded.PhoneAndroid
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.launch
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.ServerInfo
import ru.sorewa.kino.data.ServerProbe
import ru.sorewa.kino.engine.AppRestart
import ru.sorewa.kino.engine.KnownServer
import ru.sorewa.kino.engine.ServerAddress
import ru.sorewa.kino.ui.LocalTv
import ru.sorewa.kino.ui.Panel
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.SecondaryButton
import ru.sorewa.kino.ui.tvFocusRing
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Danger
import ru.sorewa.kino.ui.theme.Gold
import ru.sorewa.kino.ui.theme.Surface2
import ru.sorewa.kino.ui.theme.Surface3
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain

/** Как назвать сервер человеку: его имя из визитки, иначе адрес. */
fun serverLabel(url: String, name: String = ""): String =
    if (url.isEmpty()) "не выбран" else name.ifEmpty { ServerAddress.display(url) }

/**
 * Выбор своего сервера в окне — из профиля и с экрана входа. Смена сервера
 * перезапускает приложение (как смена режима): у каждого сервера свои id
 * серий, своя сессия и свои данные на устройстве.
 */
@Composable
fun ServerPickerDialog(onDismiss: () -> Unit) {
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Box(Modifier.fillMaxSize().imePadding().padding(16.dp), contentAlignment = Alignment.Center) {
            Panel(Modifier.widthIn(max = 520.dp).heightIn(max = 720.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("Сервер", style = MaterialTheme.typography.titleLarge, color = TextMain, modifier = Modifier.weight(1f))
                    // На ТВ закрывают кнопкой «Назад»: лишняя цель для фокуса ни к чему.
                    if (!LocalTv.current) IconButton(onClick = onDismiss) { Icon(Icons.Rounded.Close, "Закрыть", tint = TextDim) }
                }
                Spacer(Modifier.height(8.dp))
                Column(Modifier.verticalScroll(rememberScrollState())) {
                    ServerPickerContent()
                }
            }
        }
    }
}

/**
 * Первый запуск сборки без вшитого сервера (открытый исходник): сначала
 * выбрать сервер — или работать на этом устройстве.
 */
@Composable
fun ServerSetupScreen() {
    var showLocal by remember { mutableStateOf(false) }
    val tv = LocalTv.current
    if (showLocal) ConnectionDialog(toLocal = true) { showLocal = false }
    Column(
        Modifier
            .fillMaxSize()
            .statusBarsPadding()
            .navigationBarsPadding()
            .imePadding()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Spacer(Modifier.height(if (tv) 32.dp else 72.dp))
        BrandMark(big = true)
        Spacer(Modifier.height(10.dp))
        Text("Укажите сервер kino platform", style = MaterialTheme.typography.bodyLarge, color = TextDim)
        Spacer(Modifier.height(28.dp))
        Column(Modifier.widthIn(max = 460.dp).fillMaxWidth()) {
            ServerPickerContent()
            // На телевизоре движка нет (см. настройки): только сервер.
            if (!tv) {
                Spacer(Modifier.height(12.dp))
                TextButton(onClick = { showLocal = true }, modifier = Modifier.fillMaxWidth()) {
                    Icon(Icons.Rounded.PhoneAndroid, null, tint = TextDim, modifier = Modifier.size(18.dp))
                    Spacer(Modifier.width(8.dp))
                    Text("Работать на этом устройстве, без сервера", color = TextDim)
                }
            }
            Spacer(Modifier.height(32.dp))
        }
    }
}

/**
 * Поле адреса, проверка визитки (`/api/v1/server`) и переход. Проверка
 * обязательна: приложение перезапустится на новом сервере, и опечатка в
 * адресе стоила бы экрана «нет связи» вместо входа.
 */
@Composable
fun ServerPickerContent() {
    val app = KinoApp.instance
    val connection = app.container.connection
    val current = if (app.container.local) "" else connection.serverUrl
    val scope = rememberCoroutineScope()
    val focus = LocalFocusManager.current
    var input by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf("") }
    var found by remember { mutableStateOf<ServerProbe.Result.Ok?>(null) }
    var known by remember { mutableStateOf(connection.knownServers()) }
    // Недавний сервер не ответил: он уже проверялся, когда его выбирали, —
    // можно подключиться и так (дома без VPN, сервер перезапускается, а
    // скачанное с него смотрится и без связи).
    var unreachableKnown by remember { mutableStateOf<KnownServer?>(null) }

    fun check(value: String) {
        if (busy) return
        busy = true; error = ""; found = null; unreachableKnown = null
        scope.launch {
            when (val r = ServerProbe.probe(value)) {
                is ServerProbe.Result.Ok -> found = r
                is ServerProbe.Result.Fail -> {
                    error = r.message
                    val url = ServerAddress.normalize(value)
                    unreachableKnown = known.firstOrNull { it.url == url && it.url != current }
                }
            }
            busy = false
        }
    }

    fun switchTo(url: String, name: String) {
        // Запись — синхронно и до перезапуска: после него поздно.
        if (connection.setServer(url, name)) AppRestart.restart(app)
        else error = "Не удалось сохранить адрес."
    }

    fun connect(ok: ServerProbe.Result.Ok) {
        if (ok.url == current) {
            // Тот же сервер: перезапускать незачем, только имя освежим.
            connection.rememberName(ok.url, ok.info.name)
            found = null; input = ""
            known = connection.knownServers()
            return
        }
        switchTo(ok.url, ok.info.name)
    }

    if (current.isNotEmpty()) {
        Text("Сейчас", style = MaterialTheme.typography.labelMedium, color = TextFaint)
        Spacer(Modifier.height(4.dp))
        Text(
            serverLabel(current, connection.serverName(current)),
            style = MaterialTheme.typography.titleMedium, color = TextMain,
        )
        if (connection.serverName(current).isNotEmpty()) {
            Text(ServerAddress.display(current), style = MaterialTheme.typography.bodySmall, color = TextDim)
        }
        Spacer(Modifier.height(16.dp))
    }

    TextField(
        value = input,
        onValueChange = { input = it; error = ""; found = null; unreachableKnown = null },
        label = { Text("Адрес сервера") },
        placeholder = { Text("kino.example.com") },
        leadingIcon = { Icon(Icons.Rounded.Dns, null, tint = TextFaint) },
        singleLine = true,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go, autoCorrectEnabled = false),
        keyboardActions = KeyboardActions(onGo = { focus.clearFocus(); if (input.isNotBlank()) check(input) }),
        shape = RoundedCornerShape(16.dp),
        colors = TextFieldDefaults.colors(
            focusedContainerColor = Surface2,
            unfocusedContainerColor = Surface2,
            focusedIndicatorColor = Color.Transparent,
            unfocusedIndicatorColor = Color.Transparent,
            focusedLabelColor = Accent,
            unfocusedLabelColor = TextDim,
            cursorColor = Accent,
        ),
        modifier = Modifier.fillMaxWidth(),
    )
    Spacer(Modifier.height(10.dp))
    SecondaryButton(
        if (busy) "Проверяю…" else "Проверить",
        Modifier.fillMaxWidth(),
        Icons.Rounded.Search,
        enabled = input.isNotBlank() && !busy,
    ) { focus.clearFocus(); check(input) }

    if (error.isNotEmpty()) {
        Spacer(Modifier.height(10.dp))
        Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
    }

    unreachableKnown?.let { k ->
        Spacer(Modifier.height(10.dp))
        SecondaryButton("Всё равно подключиться", Modifier.fillMaxWidth()) { switchTo(k.url, k.name) }
        Spacer(Modifier.height(6.dp))
        Text(
            "Вы уже работали с этим сервером. Пока он недоступен, будет видно скачанное с него.",
            style = MaterialTheme.typography.bodySmall, color = TextFaint,
        )
    }

    found?.let { ok ->
        Spacer(Modifier.height(14.dp))
        FoundServer(ok)
        Spacer(Modifier.height(12.dp))
        PrimaryButton(
            if (ok.url == current) "Уже подключено" else "Подключиться",
            Modifier.fillMaxWidth(),
            enabled = ok.url != current,
        ) { connect(ok) }
        if (ok.url != current) {
            Spacer(Modifier.height(6.dp))
            Text(
                "Приложение перезапустится. Вход и загрузки прежнего сервера сохранятся на устройстве — к нему можно вернуться.",
                style = MaterialTheme.typography.bodySmall, color = TextFaint,
            )
        }
    }

    val others = known.filter { it.url != current }
    if (others.isNotEmpty()) {
        Spacer(Modifier.height(20.dp))
        Text("Недавние", style = MaterialTheme.typography.labelMedium, color = TextFaint)
        Spacer(Modifier.height(6.dp))
        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
            for (k in others) KnownServerRow(
                k,
                builtIn = k.url == ServerAddress.normalize(connection.builtInServer),
                onPick = { input = ServerAddress.display(k.url); check(k.url) },
                onForget = { connection.forgetServer(k.url); known = connection.knownServers() },
            )
        }
    }
}

@Composable
private fun FoundServer(ok: ServerProbe.Result.Ok) {
    val info = ok.info
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(14.dp))
            .background(Surface3)
            .padding(14.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Rounded.CheckCircle, null, tint = Accent, modifier = Modifier.size(20.dp))
            Spacer(Modifier.width(8.dp))
            Text(serverLabel(ok.url, info.name), style = MaterialTheme.typography.titleMedium, color = TextMain)
        }
        if (info.name.isNotEmpty()) {
            Text(ServerAddress.display(ok.url), style = MaterialTheme.typography.bodySmall, color = TextDim)
        }
        Spacer(Modifier.height(6.dp))
        Text(
            when (info.registration) {
                ServerInfo.REG_OPEN -> "Регистрация открыта: аккаунт можно завести прямо в приложении."
                ServerInfo.REG_CLOSED -> "Регистрации нет: аккаунт выдаёт владелец сервера."
                else -> "Регистрация по коду приглашения от владельца сервера."
            },
            style = MaterialTheme.typography.bodySmall, color = TextDim,
        )
        if (info.local) {
            Spacer(Modifier.height(4.dp))
            Text("Это движок на чьём-то устройстве, а не сервер — войти в него нельзя.", style = MaterialTheme.typography.bodySmall, color = Gold)
        }
        if (ServerAddress.isInsecure(ok.url)) {
            Spacer(Modifier.height(8.dp))
            Row(verticalAlignment = Alignment.Top) {
                Icon(Icons.Rounded.LockOpen, null, tint = Danger, modifier = Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp))
                Text(
                    "Соединение не шифруется (http): пароль и история просмотров видны всем в вашей сети. Годится только для домашней сети.",
                    style = MaterialTheme.typography.bodySmall, color = Danger,
                )
            }
        }
    }
}

@Composable
private fun KnownServerRow(k: KnownServer, builtIn: Boolean, onPick: () -> Unit, onForget: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .tvFocusRing(RoundedCornerShape(12.dp))
            .clip(RoundedCornerShape(12.dp))
            .background(Surface2)
            .clickable(onClick = onPick)
            .padding(start = 14.dp, top = 10.dp, bottom = 10.dp, end = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(serverLabel(k.url, k.name), style = MaterialTheme.typography.bodyLarge, color = TextMain, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                listOfNotNull(ServerAddress.display(k.url).takeIf { k.name.isNotEmpty() }, "по умолчанию".takeIf { builtIn })
                    .joinToString(" · ").ifEmpty { " " },
                style = MaterialTheme.typography.bodySmall, color = TextDim, maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
        }
        // Вшитый сервер из списка не убирается (он вернётся сам); на ТВ
        // крестик — лишняя цель фокуса.
        if (!builtIn && !LocalTv.current) {
            IconButton(onClick = onForget) { Icon(Icons.Rounded.Close, "Убрать из списка", tint = TextFaint) }
        }
    }
}
