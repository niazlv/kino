package ru.sorewa.kino.ui

import android.content.Context
import android.content.Intent
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.navigation.NavHostController
import ru.sorewa.kino.PlayerActivity

/** Открыть серию в плеере сразу с воспроизведением. */
fun playEpisode(context: Context, episodeId: Long, titleId: Long) {
    context.startActivity(
        Intent(context, PlayerActivity::class.java)
            .putExtra(PlayerActivity.EXTRA_EPISODE, episodeId)
            .putExtra(PlayerActivity.EXTRA_TITLE, titleId)
            .putExtra(PlayerActivity.EXTRA_AUTOPLAY, true),
    )
}

fun NavHostController.openTitle(id: Long) = navigate("title/$id")

/**
 * Кто вошёл: /me читается при старте (AppRoot/TvRoot) и кладётся сюда —
 * имя в аватаре и профиле, даже если в сохранённой сессии его нет.
 */
object Profile {
    var me by androidx.compose.runtime.mutableStateOf<ru.sorewa.kino.data.Me?>(null)

    fun name(fallback: String): String =
        me?.displayName?.takeIf { it.isNotBlank() } ?: me?.login ?: fallback
}
