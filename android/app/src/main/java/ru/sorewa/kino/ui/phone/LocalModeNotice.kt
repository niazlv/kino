package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextMain

/**
 * Что человек должен знать, прежде чем работать «на этом устройстве». Текст один
 * на два места — диалог переключения (с обязательной галочкой) и экран настроек
 * движка, чтобы он всегда был под рукой и нигде не разошёлся.
 *
 * В режиме сервера эту работу делает чужой сервер; здесь сервером становится
 * сам телефон, и всё, что обычно лежит на сервере, лежит на человеке.
 */
object LocalModeNotice {

    data class Point(val title: String, val text: String)

    val points: List<Point> = listOf(
        Point(
            "Сеть и страны — на вас",
            "Приложение ходит на площадки напрямую с вашего устройства. Следить за VPN, " +
                "прокси и нужной страной вам придётся самим: часть источников открывается только " +
                "с определённых адресов. Если источник молчит, причина обычно в сети, а не в приложении.",
        ),
        Point(
            "Это нагрузка на чужие сервисы",
            "Каждый запрос идёт на сайты площадок. Движок по умолчанию бережный: ничего не обходит " +
                "в фоне, спрашивает площадки только когда вы ищете или открываете тайтл, помнит всё найденное. " +
                "Но нагрузку всё равно можно создать: не включайте источники без нужды, не частите " +
                "с обновлением и качайте только то, что действительно нужно: скачивание берёт видео " +
                "прямо у источника и стоит ему столько же, сколько просмотр. Лишняя нагрузка — повод " +
                "закрыть доступ и вам, и остальным.",
        ),
        Point(
            "Историю никто не восстановит",
            "Закладки, прогресс и история просмотра хранятся только на этом устройстве. Сервера, " +
                "который сохранил бы их, нет: удалили приложение, очистили данные или сбросили " +
                "движок — всё потеряно. Резервной копии не существует.",
        ),
        Point(
            "Доступ могут закрыть в любой момент",
            "Сервисы вправе в любой момент выпустить изменения, после которых просмотр через " +
                "сторонние клиенты перестанет работать. Источник может сломаться без " +
                "предупреждения и навсегда.",
        ),
        Point(
            "Как есть",
            "Режим предоставляется как есть, без гарантий работы источников и сохранности данных.",
        ),
    )
}

@Composable
fun LocalModeNoticeBody(modifier: Modifier = Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        LocalModeNotice.points.forEach { p ->
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(p.title, style = MaterialTheme.typography.titleSmall, color = TextMain)
                Text(p.text, style = MaterialTheme.typography.bodyMedium, color = TextDim)
            }
        }
    }
}
