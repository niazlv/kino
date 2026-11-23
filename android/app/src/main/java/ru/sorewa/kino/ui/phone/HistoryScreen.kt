package ru.sorewa.kino.ui.phone

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.navigation.NavHostController
import ru.sorewa.kino.ui.LocalTv
import ru.sorewa.kino.ui.ScreenHeader

/** История просмотров отдельным экраном (ТВ-рельс, старые ссылки). */
@Composable
fun HistoryScreen(nav: NavHostController) {
    val tv = LocalTv.current
    Column(Modifier.fillMaxSize().then(if (tv) Modifier else Modifier.statusBarsPadding())) {
        ScreenHeader("История")
        HistoryList(nav)
    }
}
