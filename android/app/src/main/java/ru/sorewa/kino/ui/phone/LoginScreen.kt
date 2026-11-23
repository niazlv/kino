package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.ui.draw.clip
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CloudDownload
import androidx.compose.material.icons.rounded.Key
import androidx.compose.material.icons.rounded.Lock
import androidx.compose.material.icons.rounded.Person
import androidx.compose.material.icons.rounded.QrCode2
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.ServerInfo
import ru.sorewa.kino.data.ServerProbe
import ru.sorewa.kino.data.describeApiError
import ru.sorewa.kino.ui.tvFocusRing
import ru.sorewa.kino.ui.DeviceCodeLogin
import ru.sorewa.kino.ui.DeviceCodeSteps
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.SecondaryButton
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Surface2
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain
import ru.sorewa.kino.util.isOnline

/**
 * Вход: логин и пароль, вход кодом (подтвердить на другом устройстве) и
 * регистрация — как велит сервер (визитка /api/v1/server: по приглашению,
 * открытая или никакой). Сервер выбирается здесь же, до входа. После
 * парольного входа приложение само одобряет себе device-flow и живёт на
 * паре токен/refresh.
 */
@Composable
fun LoginScreen(onDone: () -> Unit, onOfflineDownloads: () -> Unit = {}) {
    val container = remember { KinoApp.instance.container }
    val scope = rememberCoroutineScope()
    val focus = LocalFocusManager.current
    var mode by remember { mutableStateOf("password") } // password | code | register
    var login by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var invite by remember { mutableStateOf("") }
    var displayName by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf("") }
    var showLocalDialog by remember { mutableStateOf(false) }
    var showServerPicker by remember { mutableStateOf(false) }
    // Визитка сервера: как он называется и какую регистрацию показать. Пока
    // не пришла (или сервер старше её) — как раньше, по приглашению.
    var server by remember { mutableStateOf<ServerInfo?>(null) }
    LaunchedEffect(container.serverUrl) {
        val r = ServerProbe.probe(container.serverUrl)
        if (r is ServerProbe.Result.Ok) {
            server = r.info
            container.connection.rememberName(container.serverUrl, r.info.name)
        }
    }
    val openReg = server?.openRegistration == true
    val canRegister = server?.canRegister != false
    // Без сети кнопка скачанного видна сразу — не заставляем сначала
    // безуспешно «войти», чтобы её обнаружить.
    val offlineNow = remember { !isOnline() }

    fun submit() {
        if (busy) return
        busy = true; error = ""
        scope.launch {
            try {
                if (mode == "register") {
                    container.api.register(invite.trim(), login.trim(), password, displayName.trim())
                }
                container.auth.loginWithPassword(login.trim(), password)
                onDone()
            } catch (e: Exception) {
                error = describeApiError(e)
            } finally { busy = false }
        }
    }

    if (showLocalDialog) ConnectionDialog(toLocal = true) { showLocalDialog = false }
    if (showServerPicker) ServerPickerDialog { showServerPicker = false }

    Box(
        Modifier
            .fillMaxSize()
            .background(
                Brush.radialGradient(
                    listOf(Accent.copy(alpha = 0.16f), Color.Transparent),
                    center = androidx.compose.ui.geometry.Offset(200f, 0f),
                    radius = 1400f,
                ),
            ),
    ) {
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
            Spacer(Modifier.height(72.dp))
            BrandMark(big = true)
            Spacer(Modifier.height(10.dp))
            Text(
                "Домашний кинотеатр для своих",
                style = MaterialTheme.typography.bodyLarge,
                color = TextDim,
            )
            Spacer(Modifier.height(16.dp))
            // Куда входим: свой сервер выбирается здесь же, до входа.
            ServerChip(
                serverLabel(container.serverUrl, server?.name ?: container.connection.serverName()),
                onClick = { showServerPicker = true },
            )
            Spacer(Modifier.height(28.dp))

            Column(Modifier.widthIn(max = 420.dp).fillMaxWidth()) {
                when (mode) {
                    "code" -> {
                        DeviceCodeLogin(onDone = onDone, big = false, onOffline = onOfflineDownloads, modifier = Modifier.fillMaxWidth())
                        Spacer(Modifier.height(28.dp))
                        DeviceCodeSteps()
                        Spacer(Modifier.height(24.dp))
                        SecondaryButton("Войти паролем", Modifier.fillMaxWidth(), Icons.Rounded.Key) { mode = "password" }
                    }
                    else -> {
                        val reg = mode == "register"
                        if (reg) {
                            Field(invite, { invite = it }, if (openReg) "Код приглашения, если есть" else "Код приглашения", Icons.Rounded.Key)
                            Spacer(Modifier.height(10.dp))
                        }
                        Field(login, { login = it }, "Логин", Icons.Rounded.Person, keyboardType = KeyboardType.Ascii)
                        Spacer(Modifier.height(10.dp))
                        var shown by remember { mutableStateOf(false) }
                        Field(
                            password, { password = it }, "Пароль", Icons.Rounded.Lock,
                            keyboardType = KeyboardType.Password,
                            secret = !shown,
                            last = !reg,
                            onDone = { focus.clearFocus(); submit() },
                            trailing = {
                                IconButton(onClick = { shown = !shown }) {
                                    Icon(if (shown) Icons.Rounded.VisibilityOff else Icons.Rounded.Visibility, "Показать пароль", tint = TextFaint)
                                }
                            },
                        )
                        if (reg) {
                            Spacer(Modifier.height(10.dp))
                            Field(
                                displayName, { displayName = it }, "Как вас называть (необязательно)", Icons.Rounded.Person,
                                last = true, onDone = { focus.clearFocus(); submit() },
                            )
                        }
                        if (error.isNotEmpty()) {
                            Spacer(Modifier.height(12.dp))
                            Text(error, color = MaterialTheme.colorScheme.error, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth())
                        }
                        Spacer(Modifier.height(20.dp))
                        PrimaryButton(
                            if (reg) "Зарегистрироваться" else "Войти",
                            Modifier.fillMaxWidth(),
                            busy = busy,
                            enabled = login.isNotBlank() && password.isNotBlank() && (!reg || openReg || invite.isNotBlank()),
                        ) { focus.clearFocus(); submit() }
                        Spacer(Modifier.height(12.dp))
                        if (!reg) {
                            SecondaryButton("Войти кодом с другого устройства", Modifier.fillMaxWidth(), Icons.Rounded.QrCode2) {
                                error = ""; mode = "code"
                            }
                        }
                        // Регистрация — как велит сервер: по приглашению, без него или никак.
                        if (reg || canRegister) {
                            Spacer(Modifier.height(8.dp))
                            TextButton(onClick = { error = ""; mode = if (reg) "password" else "register" }, modifier = Modifier.fillMaxWidth()) {
                                Text(
                                    when {
                                        reg -> "У меня уже есть аккаунт"
                                        openReg -> "Нет аккаунта? Зарегистрироваться"
                                        else -> "Есть приглашение? Зарегистрироваться"
                                    },
                                    color = Accent,
                                )
                            }
                        }
                    }
                }
                if (offlineNow || error.startsWith("нет связи")) {
                    Spacer(Modifier.height(16.dp))
                    SecondaryButton("Смотреть скачанное", Modifier.fillMaxWidth(), Icons.Rounded.CloudDownload, onClick = onOfflineDownloads)
                }
                // Своего сервера нет — приложение работает само, на этом устройстве.
                Spacer(Modifier.height(8.dp))
                TextButton(onClick = { showLocalDialog = true }, modifier = Modifier.fillMaxWidth()) {
                    Text("Работать на этом устройстве, без сервера", color = TextDim)
                }
                Spacer(Modifier.height(32.dp))
            }
        }
    }
}

@Composable
private fun Field(
    value: String,
    onChange: (String) -> Unit,
    label: String,
    icon: ImageVector,
    keyboardType: KeyboardType = KeyboardType.Text,
    secret: Boolean = false,
    last: Boolean = false,
    onDone: () -> Unit = {},
    trailing: (@Composable () -> Unit)? = null,
) {
    val focus = LocalFocusManager.current
    TextField(
        value = value,
        onValueChange = onChange,
        label = { Text(label) },
        leadingIcon = { Icon(icon, null, tint = TextFaint) },
        trailingIcon = trailing,
        singleLine = true,
        visualTransformation = if (secret) PasswordVisualTransformation() else VisualTransformation.None,
        keyboardOptions = KeyboardOptions(keyboardType = keyboardType, imeAction = if (last) ImeAction.Go else ImeAction.Next),
        keyboardActions = KeyboardActions(
            onNext = { focus.moveFocus(FocusDirection.Down) },
            onGo = { onDone() },
        ),
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
}

/** «Сервер: … · Сменить» на экране входа. */
@Composable
internal fun ServerChip(label: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier
            .tvFocusRing(RoundedCornerShape(50))
            .clip(RoundedCornerShape(50))
            .background(Surface2)
            .clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Rounded.Dns, null, tint = TextFaint, modifier = Modifier.size(16.dp))
        Spacer(Modifier.width(8.dp))
        Text(label, style = MaterialTheme.typography.bodyMedium, color = TextMain, maxLines = 1, overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 240.dp))
        Spacer(Modifier.width(8.dp))
        Text("Сменить", style = MaterialTheme.typography.bodyMedium, color = Accent)
    }
}
