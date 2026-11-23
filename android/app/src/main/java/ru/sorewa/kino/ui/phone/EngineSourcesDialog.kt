package ru.sorewa.kino.ui.phone

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import ru.sorewa.kino.data.Api
import ru.sorewa.kino.data.EngineSourceCheckReq
import ru.sorewa.kino.data.EngineSourceInfo
import ru.sorewa.kino.data.describeApiError
import ru.sorewa.kino.engine.EngineSources
import ru.sorewa.kino.ui.Overline
import ru.sorewa.kino.ui.Panel
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.SecondaryButton
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Bg
import ru.sorewa.kino.ui.theme.Danger
import ru.sorewa.kino.ui.theme.Gold
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain
import java.io.File

/**
 * Площадки движка (ADR-0017): все, что он знает, — на Go, скрипты из сборки и свои.
 * Здесь человек ставит площадку-скрипт (файлом или вставкой текста), проверяет
 * любую площадку тем же набором проверок, что и разработчики, и отправляет нам
 * файл площадки с отчётом проверки — мы проверяем его у себя (`sourcekit import`)
 * и включаем на сервере и в следующей сборке.
 *
 * Установка и удаление вступают в силу после перезапуска движка: [onRestart]
 * перезапускает его, как кнопка «Применить» в настройках.
 */
@Composable
fun EngineSourcesDialog(api: Api, engineReady: Boolean, onRestart: suspend () -> Unit, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var list by remember { mutableStateOf<List<EngineSourceInfo>>(emptyList()) }
    var err by remember { mutableStateOf("") }
    var refresh by remember { mutableIntStateOf(0) }
    var busy by remember { mutableStateOf("") }
    var checking by remember { mutableStateOf<EngineSourceInfo?>(null) }
    var report by remember { mutableStateOf<Pair<EngineSourceInfo, String>?>(null) }
    var pasting by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf<EngineSourceInfo?>(null) }

    LaunchedEffect(engineReady, refresh) {
        if (!engineReady) return@LaunchedEffect
        try {
            val got = api.engineSources().sources
            list = got
            err = ""
            EngineSources.remember(context, got)
        } catch (e: Exception) {
            err = describeApiError(e)
        }
    }

    fun install(text: String) {
        scope.launch {
            busy = "Ставлю площадку…"
            try {
                val r = api.installEngineSource(text)
                busy = "Поставлена «${r.name}» ${r.version}. Перезапускаю движок…"
                onRestart()
                refresh++
                busy = "Поставлена «${r.name}» ${r.version}. Включите её в списке источников."
            } catch (e: Exception) {
                busy = ""
                err = describeApiError(e)
            }
        }
    }

    val pickFile = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri: Uri? ->
        if (uri == null) return@rememberLauncherForActivityResult
        scope.launch {
            val text = withContext(Dispatchers.IO) {
                try {
                    context.contentResolver.openInputStream(uri)?.use { it.readBytes().toString(Charsets.UTF_8) }
                } catch (_: Exception) {
                    null
                }
            }
            if (text.isNullOrBlank()) err = "Файл не прочитан" else install(text)
        }
    }

    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize(), color = Bg) {
            Column(Modifier.fillMaxSize().statusBarsPadding()) {
                Row(Modifier.padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    IconButton(onClick = onDismiss) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Назад") }
                    Text("Площадки", style = MaterialTheme.typography.headlineMedium)
                }
                Column(
                    Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(horizontal = 16.dp),
                    verticalArrangement = Arrangement.spacedBy(14.dp),
                ) {
                    Text(
                        "Площадка — один файл .js: его можно поставить сюда, поправить, если площадка сломалась, " +
                            "и прислать нам — мы проверим и включим её у всех. Скрипт ходит только на сайты, " +
                            "перечисленные в нём самом, и не видит ваших данных.",
                        style = MaterialTheme.typography.bodySmall, color = TextDim,
                        modifier = Modifier.padding(horizontal = 8.dp),
                    )
                    if (!engineReady) Text("Движок не работает — список появится, когда он запустится.", color = Gold)
                    if (err.isNotEmpty()) Text(err, color = Danger, style = MaterialTheme.typography.bodySmall)
                    if (busy.isNotEmpty()) Text(busy, color = TextMain, style = MaterialTheme.typography.bodySmall)

                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        PrimaryButton("Из файла", Modifier.weight(1f), enabled = engineReady && busy.isEmpty()) {
                            err = ""
                            pickFile.launch(arrayOf("application/javascript", "text/javascript", "text/plain", "*/*"))
                        }
                        SecondaryButton("Вставить текст", Modifier.weight(1f), enabled = engineReady && busy.isEmpty()) {
                            err = ""
                            pasting = true
                        }
                    }

                    val scripts = list.filter { it.isScript || it.broken.isNotEmpty() }
                    val builtin = list.filter { !it.isScript && it.broken.isEmpty() }
                    if (scripts.isNotEmpty()) {
                        Overline("Площадки-скрипты", Modifier.padding(start = 8.dp, top = 6.dp))
                        scripts.forEach { s ->
                            SourceCard(s, onCheck = { checking = s }, onSend = {
                                scope.launch {
                                    try {
                                        val text = api.engineSourceFile(s.id)
                                        shareSource(context, s, text, report?.takeIf { it.first.id == s.id }?.second)
                                    } catch (e: Exception) {
                                        err = describeApiError(e)
                                    }
                                }
                            }, onRemove = { removing = s })
                        }
                    }
                    if (builtin.isNotEmpty()) {
                        Overline("Встроенные", Modifier.padding(start = 8.dp, top = 6.dp))
                        Text(
                            "Площадки на Go внутри приложения. Если такая сломалась, её можно заменить скриптом с тем же id — " +
                                "тогда работает скрипт, история просмотра сохраняется.",
                            style = MaterialTheme.typography.bodySmall, color = TextDim,
                            modifier = Modifier.padding(horizontal = 8.dp),
                        )
                        builtin.forEach { s -> SourceCard(s, onCheck = { checking = s }, onSend = null, onRemove = null) }
                    }
                    Spacer(Modifier.height(24.dp))
                }
            }
        }
    }

    checking?.let { src ->
        CheckDialog(src, onDismiss = { checking = null }) { query ->
            checking = null
            scope.launch {
                busy = "Проверяю «${src.name}»… это десяток запросов к площадке"
                try {
                    val r = api.checkEngineSource(EngineSourceCheckReq(id = src.id, query = query))
                    report = src to r.text
                } catch (e: Exception) {
                    err = describeApiError(e)
                }
                busy = ""
            }
        }
    }

    report?.let { (src, text) ->
        AlertDialog(
            onDismissRequest = { report = null },
            title = { Text("Проверка: ${src.name}") },
            text = {
                SelectionContainer {
                    Text(
                        text, fontFamily = FontFamily.Monospace, fontSize = 11.sp, lineHeight = 15.sp,
                        modifier = Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                    )
                }
            },
            confirmButton = {
                if (src.isScript) {
                    TextButton(onClick = {
                        scope.launch {
                            try {
                                shareSource(context, src, api.engineSourceFile(src.id), text)
                            } catch (e: Exception) {
                                err = describeApiError(e)
                            }
                        }
                    }) { Text("Отправить с отчётом") }
                } else {
                    TextButton(onClick = { shareReport(context, src, text) }) { Text("Отправить отчёт") }
                }
            },
            dismissButton = { TextButton(onClick = { report = null }) { Text("Закрыть") } },
        )
    }

    if (pasting) {
        var text by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { pasting = false },
            title = { Text("Текст площадки") },
            text = {
                OutlinedTextField(
                    value = text, onValueChange = { text = it },
                    label = { Text("source({ … })") },
                    textStyle = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                    modifier = Modifier.fillMaxWidth().heightIn(min = 160.dp, max = 360.dp),
                )
            },
            confirmButton = {
                TextButton(enabled = text.isNotBlank(), onClick = { pasting = false; install(text) }) { Text("Поставить") }
            },
            dismissButton = { TextButton(onClick = { pasting = false }) { Text("Отмена") } },
        )
    }

    removing?.let { src ->
        AlertDialog(
            onDismissRequest = { removing = null },
            title = { Text("Удалить «${src.name}»?") },
            text = {
                Text(
                    if (src.overrides.isNotEmpty()) "Ваша правка удалится, вернётся площадка из приложения."
                    else "Площадка исчезнет из списка источников. Просмотренное и закладки останутся.",
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    removing = null
                    scope.launch {
                        busy = "Удаляю…"
                        try {
                            api.removeEngineSource(src.id)
                            onRestart()
                            refresh++
                            busy = ""
                        } catch (e: Exception) {
                            busy = ""
                            err = describeApiError(e)
                        }
                    }
                }) { Text("Удалить", color = Danger) }
            },
            dismissButton = { TextButton(onClick = { removing = null }) { Text("Отмена") } },
        )
    }
}

@Composable
private fun SourceCard(s: EngineSourceInfo, onCheck: () -> Unit, onSend: (() -> Unit)?, onRemove: (() -> Unit)?) {
    Panel {
        Text(s.name.ifEmpty { s.id }, style = MaterialTheme.typography.bodyLarge, color = TextMain)
        Text(originLabel(s), style = MaterialTheme.typography.bodySmall, color = TextFaint)
        if (s.note.isNotEmpty()) Text(s.note, style = MaterialTheme.typography.bodySmall, color = TextDim)
        when {
            s.broken.isNotEmpty() -> Text("Файл не разобран: ${s.broken}", style = MaterialTheme.typography.bodySmall, color = Danger)
            s.pending -> Text("Изменится после перезапуска движка", style = MaterialTheme.typography.bodySmall, color = Gold)
            s.enabled && s.state == "ready" -> Text("Включена · работает", style = MaterialTheme.typography.bodySmall, color = Accent)
            s.enabled && s.state == "skipped" -> Text("Отключена: ${s.reason}", style = MaterialTheme.typography.bodySmall, color = Gold)
            s.enabled -> Text("Включена · подключается", style = MaterialTheme.typography.bodySmall, color = TextDim)
        }
        Spacer(Modifier.height(8.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (s.broken.isEmpty()) SecondaryButton("Проверить", Modifier.weight(1f)) { onCheck() }
            if (onSend != null && s.broken.isEmpty()) SecondaryButton("Отправить нам", Modifier.weight(1f)) { onSend() }
            if (onRemove != null && s.removable) SecondaryButton("Удалить", Modifier.weight(1f)) { onRemove() }
        }
    }
}

private fun originLabel(s: EngineSourceInfo): String {
    val v = if (s.version.isNotEmpty()) " ${s.version}" else ""
    return when {
        s.origin == "local" && s.overrides == "builtin" -> "ваш скрипт$v · заменил встроенную"
        s.origin == "local" && s.overrides == "bundled" -> "ваша правка$v"
        s.origin == "local" -> "ваш скрипт$v"
        s.origin == "bundled" -> "скрипт из приложения$v"
        else -> "встроенная · ${s.id}"
    }
}

@Composable
private fun CheckDialog(src: EngineSourceInfo, onDismiss: () -> Unit, onRun: (String) -> Unit) {
    var q by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Проверить «${src.name}»") },
        text = {
            Column {
                Text(
                    "Что точно есть на этой площадке? Проверка найдёт это, откроет тайтл, серии и начало потока — " +
                        "как у разработчиков.",
                    style = MaterialTheme.typography.bodySmall, color = TextDim,
                )
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(value = q, onValueChange = { q = it }, label = { Text("Название") }, singleLine = true)
            }
        },
        confirmButton = { TextButton(enabled = q.isNotBlank(), onClick = { onRun(q.trim()) }) { Text("Проверить") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Отмена") } },
    )
}

/**
 * Отправить файл площадки (и отчёт проверки, если был) через системное «Поделиться»:
 * в мессенджер, почту — куда человеку удобно. Файл кладётся в кеш приложения и
 * отдаётся через FileProvider (путь sources/ в file_paths.xml).
 */
private fun shareSource(context: Context, s: EngineSourceInfo, text: String, report: String?) {
    val dir = File(context.cacheDir, "sources").apply { mkdirs() }
    val file = File(dir, "${s.id}.js")
    file.writeText(text)
    val uri = FileProvider.getUriForFile(context, context.packageName + ".fileprovider", file)
    val body = buildString {
        append("Площадка «${s.name}» (${s.id}${if (s.version.isNotEmpty()) " " + s.version else ""}) — ")
        append(if (s.origin == "local") "моя правка или новая площадка." else "файл из приложения.")
        if (report != null) append("\n\n").append(report)
    }
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "application/javascript"
        putExtra(Intent.EXTRA_STREAM, uri)
        putExtra(Intent.EXTRA_SUBJECT, "Kino: площадка ${s.id}")
        putExtra(Intent.EXTRA_TEXT, body)
        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    }
    context.startActivity(Intent.createChooser(send, "Отправить площадку").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
}

private fun shareReport(context: Context, s: EngineSourceInfo, report: String) {
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_SUBJECT, "Kino: проверка площадки ${s.id}")
        putExtra(Intent.EXTRA_TEXT, report)
    }
    context.startActivity(Intent.createChooser(send, "Отправить отчёт").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
}
