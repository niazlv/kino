package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Tv
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavHostController
import kotlinx.coroutines.launch
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.DeviceAuthInfo
import ru.sorewa.kino.data.describeApiError
import ru.sorewa.kino.ui.Panel
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.SecondaryButton
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Surface2
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextMain

/**
 * «Подключить устройство»: телевизор (или другой телефон) показывает код —
 * здесь его вводят и подтверждают. Без этого экрана вход на ТВ упирался
 * в сайт.
 */
@Composable
fun LinkDeviceScreen(nav: NavHostController) {
    val api = remember { KinoApp.instance.container.api }
    val scope = rememberCoroutineScope()
    var code by remember { mutableStateOf("") }
    var info by remember { mutableStateOf<DeviceAuthInfo?>(null) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf("") }
    var done by remember { mutableStateOf(false) }

    fun normalized() = code.uppercase().filter { it.isLetterOrDigit() }.let {
        if (it.length == 8) it.take(4) + "-" + it.drop(4) else it
    }

    fun check() {
        busy = true; error = ""
        scope.launch {
            try { info = api.deviceInfo(normalized()) } catch (e: Exception) {
                // 410 `expired` здесь — истёкший код, а не сессия (её продлевает клиент).
                error = if (e is ru.sorewa.kino.data.ApiError && (e.status == 404 || e.status == 410)) "Код не найден или истёк — проверьте на экране устройства" else describeApiError(e)
            } finally { busy = false }
        }
    }

    Column(
        Modifier
            .fillMaxSize()
            .statusBarsPadding()
            .imePadding()
            .verticalScroll(rememberScrollState()),
    ) {
        Row(Modifier.padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Назад") }
            Text("Подключить устройство", style = MaterialTheme.typography.titleLarge)
        }
        Column(Modifier.padding(horizontal = 24.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Spacer(Modifier.height(24.dp))
            Box(Modifier.size(88.dp).background(Accent.copy(alpha = 0.14f), CircleShape), contentAlignment = Alignment.Center) {
                Icon(if (done) Icons.Rounded.CheckCircle else Icons.Rounded.Tv, null, tint = Accent, modifier = Modifier.size(44.dp))
            }
            Spacer(Modifier.height(20.dp))
            when {
                done -> {
                    Text("Готово!", style = MaterialTheme.typography.headlineSmall)
                    Spacer(Modifier.height(8.dp))
                    Text("Устройство вошло под вашим именем. Можно смотреть.", color = TextDim, textAlign = TextAlign.Center)
                    Spacer(Modifier.height(28.dp))
                    PrimaryButton("Хорошо", Modifier.fillMaxWidth()) { nav.popBackStack() }
                }
                info != null -> {
                    val i = info!!
                    Text("Впустить устройство?", style = MaterialTheme.typography.headlineSmall)
                    Spacer(Modifier.height(16.dp))
                    Panel {
                        Text("Код", color = TextDim, style = MaterialTheme.typography.bodySmall)
                        Text(normalized(), color = TextMain, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.Bold, fontSize = 22.sp)
                        Spacer(Modifier.height(10.dp))
                        Text("Устройство", color = TextDim, style = MaterialTheme.typography.bodySmall)
                        Text(deviceName(i.device), color = TextMain, style = MaterialTheme.typography.bodyLarge)
                    }
                    Spacer(Modifier.height(12.dp))
                    Text(
                        "Подтверждайте, только если код на экране вашего телевизора или телефона.",
                        color = TextDim, style = MaterialTheme.typography.bodySmall, textAlign = TextAlign.Center,
                    )
                    if (error.isNotEmpty()) {
                        Spacer(Modifier.height(8.dp))
                        Text(error, color = MaterialTheme.colorScheme.error, textAlign = TextAlign.Center)
                    }
                    Spacer(Modifier.height(24.dp))
                    PrimaryButton("Впустить", Modifier.fillMaxWidth(), busy = busy) {
                        busy = true
                        scope.launch {
                            try { api.approveDevice(normalized()); done = true } catch (e: Exception) {
                                error = if (e is ru.sorewa.kino.data.ApiError && e.status == 410) "Код истёк — на экране устройства уже новый" else describeApiError(e)
                            } finally { busy = false }
                        }
                    }
                    Spacer(Modifier.height(10.dp))
                    SecondaryButton("Не впускать", Modifier.fillMaxWidth()) {
                        scope.launch { try { api.denyDevice(normalized()) } catch (_: Exception) { } ; nav.popBackStack() }
                    }
                }
                else -> {
                    Text("Введите код с экрана", style = MaterialTheme.typography.headlineSmall)
                    Spacer(Modifier.height(8.dp))
                    Text(
                        "На телевизоре откройте kino — он покажет код из восьми знаков.",
                        color = TextDim, textAlign = TextAlign.Center,
                    )
                    Spacer(Modifier.height(24.dp))
                    TextField(
                        value = code,
                        onValueChange = { v -> code = v.uppercase().filter { it.isLetterOrDigit() || it == '-' }.take(9) },
                        placeholder = { Text("ABCD-EFGH", color = TextDim.copy(alpha = 0.5f), modifier = Modifier.fillMaxWidth(), textAlign = TextAlign.Center, fontFamily = FontFamily.Monospace, fontSize = 28.sp) },
                        singleLine = true,
                        textStyle = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 28.sp, fontWeight = FontWeight.Bold, textAlign = TextAlign.Center, letterSpacing = 3.sp, color = TextMain),
                        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Characters, imeAction = ImeAction.Done),
                        keyboardActions = KeyboardActions(onDone = { if (code.filter { it.isLetterOrDigit() }.length >= 8) check() }),
                        shape = RoundedCornerShape(18.dp),
                        colors = TextFieldDefaults.colors(
                            focusedContainerColor = Surface2,
                            unfocusedContainerColor = Surface2,
                            focusedIndicatorColor = Color.Transparent,
                            unfocusedIndicatorColor = Color.Transparent,
                            cursorColor = Accent,
                        ),
                        modifier = Modifier.fillMaxWidth(),
                    )
                    if (error.isNotEmpty()) {
                        Spacer(Modifier.height(10.dp))
                        Text(error, color = MaterialTheme.colorScheme.error, textAlign = TextAlign.Center)
                    }
                    Spacer(Modifier.height(20.dp))
                    PrimaryButton(
                        "Дальше",
                        Modifier.fillMaxWidth(),
                        busy = busy,
                        enabled = code.filter { it.isLetterOrDigit() }.length >= 8,
                    ) { check() }
                }
            }
            Spacer(Modifier.height(32.dp))
        }
    }
}

private fun deviceName(d: String): String = when (d.lowercase()) {
    "tv" -> "Телевизор"
    "android" -> "Телефон на Android"
    "web" -> "Браузер"
    else -> d.ifEmpty { "Неизвестное устройство" }
}

