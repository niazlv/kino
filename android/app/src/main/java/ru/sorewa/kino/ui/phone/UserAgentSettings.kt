package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import ru.sorewa.kino.data.EngineStatus
import ru.sorewa.kino.engine.EngineSettings
import ru.sorewa.kino.engine.EngineSources
import ru.sorewa.kino.engine.validateUserAgent
import ru.sorewa.kino.ui.SecondaryButton
import ru.sorewa.kino.ui.theme.Danger
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint

/** Хоть одна заданная человеком подпись негодна — «Применить» недоступно. */
fun userAgentHasError(s: EngineSettings): Boolean =
    validateUserAgent(s.userAgent) != null || s.userAgents.values.any { validateUserAgent(it) != null }

/** Подписи не по умолчанию заданы? Тогда блок раскрыт сразу: человек должен видеть, что они есть. */
private fun hasCustomUserAgent(s: EngineSettings): Boolean =
    s.userAgent.isNotBlank() || s.userAgents.values.any { it.isNotBlank() }

/**
 * «Для опытных»: подпись (User-Agent), с которой движок ходит на площадки.
 *
 * По умолчанию движок не выдаёт себя за чужой браузер: подпись браузероподобная (так нужно
 * части площадок), но кончается явной пометкой приложения и его версии. Менять её не
 * советуем, но можно — по каждой площадке отдельно и без пересборки приложения. Что задано,
 * уходит наружу как есть, на совести человека.
 */
@Composable
fun UserAgentSettings(draft: EngineSettings, onDraft: (EngineSettings) -> Unit, status: EngineStatus?) {
    var open by remember { mutableStateOf(hasCustomUserAgent(draft)) }
    val default = status?.userAgent?.default.orEmpty()
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(
            "По умолчанию движок не выдаёт себя за чужой браузер. Подпись похожа на браузерную (так нужно " +
                "части площадок), но в конце прямо сказано, что это приложение kino platform для Android, " +
                "и указана его версия.",
            style = MaterialTheme.typography.bodySmall, color = TextDim,
        )
        if (default.isNotEmpty()) {
            SelectionContainer {
                Text(
                    default,
                    fontFamily = FontFamily.Monospace, fontSize = 11.sp, lineHeight = 15.sp, color = TextDim,
                )
            }
        }
        Text(
            "Мы не советуем её менять. Что вы зададите ниже, уйдёт на площадку как есть: она может отказать, " +
                "отдавать другое или закрыть доступ — это на вашей совести. Но если подпись нужно подобрать " +
                "без пересборки приложения — можно.",
            style = MaterialTheme.typography.bodySmall, color = Danger,
        )
        val custom = status?.userAgent?.custom.orEmpty()
        if (custom.isNotEmpty()) {
            Text(
                "Сейчас задано вручную: " + custom.joinToString(", ") { id ->
                    if (id == "*") "все площадки" else EngineSources.of(id)?.title ?: id
                },
                style = MaterialTheme.typography.bodySmall, color = TextFaint,
            )
        }
        SecondaryButton(
            if (open) "Скрыть настройки подписи" else "Задать свою подпись…",
            Modifier.fillMaxWidth(),
        ) { open = !open }

        if (open) {
            UaField(
                label = "Для всех площадок", value = draft.userAgent, placeholder = "По умолчанию",
                onChange = { onDraft(draft.copy(userAgent = it)) },
            )
            Text(
                "Подпись площадки сильнее общей. Пустое поле — подпись по умолчанию.",
                style = MaterialTheme.typography.bodySmall, color = TextFaint,
            )
            // Только включённые площадки: подпись выключенной ни на что не влияет.
            for (id in draft.activeProviders()) {
                val src = EngineSources.of(id) ?: continue
                UaField(
                    label = src.title, value = draft.userAgents[id].orEmpty(), placeholder = "По умолчанию",
                    onChange = { v ->
                        onDraft(draft.copy(userAgents = if (v.isBlank()) draft.userAgents - id else draft.userAgents + (id to v)))
                    },
                )
            }
            SecondaryButton(
                "Вернуть подписи по умолчанию", Modifier.fillMaxWidth(),
                enabled = hasCustomUserAgent(draft),
            ) { onDraft(draft.copy(userAgent = "", userAgents = emptyMap())) }
        }
    }
}

@Composable
private fun UaField(label: String, value: String, placeholder: String, onChange: (String) -> Unit) {
    val error = validateUserAgent(value)
    OutlinedTextField(
        value = value, onValueChange = onChange,
        label = { Text(label) }, placeholder = { Text(placeholder) },
        singleLine = true, isError = error != null,
        supportingText = { if (error != null) Text(error) },
        modifier = Modifier.fillMaxWidth(),
    )
}
