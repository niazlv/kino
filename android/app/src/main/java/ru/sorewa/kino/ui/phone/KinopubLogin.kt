package ru.sorewa.kino.ui.phone

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import ru.sorewa.kino.data.Api
import ru.sorewa.kino.data.ApiError
import ru.sorewa.kino.data.EngineStatus
import ru.sorewa.kino.data.KinopubLoginCode
import ru.sorewa.kino.data.describeApiError
import ru.sorewa.kino.data.parseIsoTime
import ru.sorewa.kino.engine.EngineSettings
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.SecondaryButton
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Danger
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain

/**
 * Доступ к kinopub в настройках движка: вход по коду (своя сессия, которую движок сам
 * продлевает) или выход. Ключ руками — отдельное поле под этим блоком.
 *
 * Вход по коду занимает отдельный слот устройства в аккаунте kino.pub и требует
 * секрета OAuth-клиента: публичного клиента у kino.pub нет, секрет берётся из
 * официального приложения. В это приложение он не вшит и не будет — его даёт человек.
 */
@Composable
fun KinopubAccess(
    draft: EngineSettings,
    onDraft: (EngineSettings) -> Unit,
    saved: EngineSettings,
    status: EngineStatus?,
    engineReady: Boolean,
    onLogin: () -> Unit,
    onLogout: () -> Unit,
) {
    val kp = status?.kinopub
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        if (kp?.session == true) {
            Text(
                "Вход выполнен: у движка своя сессия kinopub, он сам её продлевает.",
                style = MaterialTheme.typography.bodySmall, color = Accent,
            )
            SecondaryButton("Выйти из kinopub", Modifier.fillMaxWidth()) { onLogout() }
        } else {
            Text(
                "Вход по коду создаёт отдельную сессию вашего аккаунта kino.pub — это отдельный слот " +
                    "устройства (убрать его можно на kino.pub в списке устройств). Нужен секрет " +
                    "OAuth-клиента: публичного у kino.pub нет, он берётся из официального приложения, а в " +
                    "это приложение не вшит.",
                style = MaterialTheme.typography.bodySmall, color = TextDim,
            )
            OutlinedTextField(
                value = draft.keys[EngineSettings.KINOPUB_SECRET_KEY].orEmpty(),
                onValueChange = { onDraft(draft.copy(keys = draft.keys + (EngineSettings.KINOPUB_SECRET_KEY to it))) },
                label = { Text("Секрет OAuth-клиента") },
                singleLine = true,
                visualTransformation = PasswordVisualTransformation(),
                modifier = Modifier.fillMaxWidth(),
            )
            // Секрет доходит до движка только вместе с запуском: пока не применён, входить нечем.
            val ready = saved.keys[EngineSettings.KINOPUB_SECRET_KEY].orEmpty().isNotBlank() &&
                kp?.canLogin == true && engineReady
            PrimaryButton("Войти по коду", Modifier.fillMaxWidth(), enabled = ready) { onLogin() }
            if (!ready) {
                Text(
                    "Введите секрет и нажмите «Применить» — тогда вход станет доступен. Без секрета остаётся ключ руками.",
                    style = MaterialTheme.typography.bodySmall, color = TextFaint,
                )
            }
        }
    }
}

/** Что показывает диалог входа. */
internal sealed interface LoginUi {
    data object Loading : LoginUi
    data class Waiting(val code: KinopubLoginCode, val leftMin: Int) : LoginUi
    data class Failed(val message: String) : LoginUi
}

/**
 * Диалог входа: код для ввода на странице kino.pub и ожидание подтверждения. Движок
 * сам опрашивает kino.pub не чаще, чем тот разрешил, — диалог спрашивает движок раз в
 * несколько секунд. Закрылся диалог — начатый вход просто истечёт.
 *
 * [onClose] с true — вход выполнен: движок надо перезапустить, чтобы провайдер поднялся с сессией.
 */
@Composable
fun KinopubLoginDialog(api: Api, onClose: (ok: Boolean) -> Unit) {
    var ui by remember { mutableStateOf<LoginUi>(LoginUi.Loading) }
    var attempt by remember { mutableIntStateOf(0) }

    LaunchedEffect(attempt) {
        ui = LoginUi.Loading
        val code = try {
            api.kinopubLoginBegin()
        } catch (e: ApiError) {
            ui = LoginUi.Failed(
                when (e.code) {
                    "no_secret" -> "Движок не получил секрет. Введите его, нажмите «Применить» и повторите."
                    "client_rejected" -> "kino.pub не признал секрет. Проверьте его."
                    else -> describeApiError(e)
                },
            )
            return@LaunchedEffect
        } catch (e: Exception) {
            ui = LoginUi.Failed(describeApiError(e))
            return@LaunchedEffect
        }
        val until = parseIsoTime(code.expiresAt)
        while (true) {
            val left = if (until > 0) ((until - System.currentTimeMillis()) / 60_000).toInt().coerceAtLeast(0) else 0
            ui = LoginUi.Waiting(code, left)
            delay(3_000)
            val st = try { api.kinopubLoginPoll() } catch (_: Exception) { continue } // связь моргнула — спросим снова
            when (st.state) {
                "ok" -> { onClose(true); return@LaunchedEffect }
                "pending" -> Unit
                "none" -> { ui = LoginUi.Failed("Вход сброшен. Начните заново."); return@LaunchedEffect }
                else -> {
                    ui = LoginUi.Failed(st.message.ifEmpty { "Вход не удался." })
                    return@LaunchedEffect
                }
            }
        }
    }

    KinopubLoginView(ui, onRetry = { attempt++ }, onClose = { onClose(false) })
}

/** Вид диалога по состоянию: отдельно от логики, чтобы его можно было показать без сети. */
@Composable
internal fun KinopubLoginView(ui: LoginUi, onRetry: () -> Unit, onClose: () -> Unit) {
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("Вход в kinopub") },
        text = {
            when (val u = ui) {
                LoginUi.Loading -> Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp, color = Accent)
                    Spacer(Modifier.width(12.dp))
                    Text("Запрашиваю код у kino.pub…")
                }
                is LoginUi.Failed -> Text(u.message, color = Danger)
                is LoginUi.Waiting -> Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text("Откройте страницу kino.pub и введите код:")
                    Text(
                        u.code.userCode,
                        fontFamily = FontFamily.Monospace, fontWeight = FontWeight.Bold,
                        fontSize = 34.sp, color = TextMain,
                    )
                    Text(
                        u.code.verificationUri.ifEmpty { "kino.pub/device" },
                        style = MaterialTheme.typography.bodyMedium, color = TextDim,
                    )
                    // Друг под другом: в одну строку «Скопировать код» не помещается и обрезается.
                    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        SecondaryButton("Открыть страницу", Modifier.fillMaxWidth()) { openHttps(context, u.code.verificationUriComplete.ifEmpty { u.code.verificationUri }) }
                        SecondaryButton("Скопировать код", Modifier.fillMaxWidth()) { clipboard.setText(AnnotatedString(u.code.userCode)) }
                    }
                    Spacer(Modifier.height(2.dp))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp, color = Accent)
                        Spacer(Modifier.width(10.dp))
                        Text(
                            "Жду подтверждения" + if (u.leftMin > 0) " · код действует ещё ${u.leftMin} мин" else "",
                            style = MaterialTheme.typography.bodySmall, color = TextDim,
                        )
                    }
                    Text(
                        "Занимает отдельный слот устройства в вашем аккаунте kino.pub.",
                        style = MaterialTheme.typography.bodySmall, color = TextFaint,
                    )
                }
            }
        },
        confirmButton = {
            if (ui is LoginUi.Failed) TextButton(onClick = onRetry) { Text("Ещё раз") }
        },
        dismissButton = { TextButton(onClick = onClose) { Text("Закрыть") } },
    )
}

/** Только https: адрес пришёл из ответа авторизации, но открывать что попало из него не надо. */
private fun openHttps(context: android.content.Context, url: String) {
    if (!url.startsWith("https://")) return
    try {
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    } catch (_: Exception) {
        // Нет браузера — код всё равно на экране, ввести его можно с другого устройства.
    }
}
