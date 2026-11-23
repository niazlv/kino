package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.engine.AppRestart
import ru.sorewa.kino.engine.ConnectionMode

private const val TO_LOCAL = "Приложение запустит собственный движок на этом телефоне. " +
    "Поиск и просмотр пойдут напрямую с площадок, закладки и прогресс будут храниться " +
    "только здесь, а сервер не понадобится.\n\n" +
    "Включена одна площадка — Анилибрия; остальные включаются в настройках движка. " +
    "Загрузки работают — прямо с источника, без сервера; входа по паролю и обновлений " +
    "через сервер в этом режиме нет. " +
    "Ваш вход на сервере сохранится — вернуться можно в любой момент. " +
    "Приложение перезапустится."

private const val TO_REMOTE = "Приложение вернётся к вашему серверу и войдёт под сохранённой сессией.\n\n" +
    "Данные этого режима (закладки, прогресс) останутся на устройстве и дождутся, " +
    "пока вы снова выберете «На этом устройстве».\n\n" +
    "Приложение перезапустится."

/**
 * Смена режима подключения (ADR-0015): сервер или движок на этом устройстве.
 * У режимов раздельные данные, поэтому переключение — перезапуск процесса, а
 * не подмена под работающими экранами.
 */
@Composable
fun ConnectionDialog(toLocal: Boolean, onDismiss: () -> Unit) {
    // Переход на движок — только осознанно: без галочки кнопка не нажимается.
    var understood by remember { mutableStateOf(!toLocal) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (toLocal) "Работать на этом устройстве?" else "Вернуться к серверу?") },
        text = {
            Column(Modifier.heightIn(max = 440.dp).verticalScroll(rememberScrollState())) {
                Text(if (toLocal) TO_LOCAL else TO_REMOTE)
                if (toLocal) {
                    Spacer(Modifier.height(16.dp))
                    LocalModeNoticeBody()
                    Spacer(Modifier.height(8.dp))
                    Row(
                        Modifier.fillMaxWidth().clickable { understood = !understood },
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Checkbox(checked = understood, onCheckedChange = { understood = it })
                        Text("Я прочитал и понимаю", style = MaterialTheme.typography.bodyMedium)
                    }
                }
            }
        },
        confirmButton = {
            TextButton(enabled = understood, onClick = {
                val app = KinoApp.instance
                // Запись режима — до перезапуска и синхронно: после него поздно.
                if (app.container.connection.setMode(if (toLocal) ConnectionMode.LOCAL else ConnectionMode.REMOTE)) {
                    AppRestart.restart(app)
                } else {
                    onDismiss()
                }
            }) { Text(if (toLocal) "Перейти" else "Вернуться") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Отмена") } },
    )
}
