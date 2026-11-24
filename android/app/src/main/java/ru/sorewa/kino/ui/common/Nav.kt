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
 * Тайтл из списка: его карточка уже на руках — страница въезжает сразу с
 * обложкой и названием, а не пустой, пока сервер отвечает.
 */
fun NavHostController.openTitle(t: ru.sorewa.kino.data.Title) {
    TitlePeek.put(t)
    openTitle(t.id)
}

/** Последние открытые из списков карточки тайтлов — для шапки страницы до ответа сервера. */
object TitlePeek {
    private val map = object : LinkedHashMap<Long, ru.sorewa.kino.data.Title>(32, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<Long, ru.sorewa.kino.data.Title>?) = size > 64
    }

    fun put(t: ru.sorewa.kino.data.Title) = synchronized(map) { map[t.id] = t }
    fun get(id: Long): ru.sorewa.kino.data.Title? = synchronized(map) { map[id] }
}

/**
 * Кто вошёл: /me читается при старте (AppRoot/TvRoot) и кладётся сюда —
 * имя в аватаре и профиле, даже если в сохранённой сессии его нет.
 */
object Profile {
    var me by androidx.compose.runtime.mutableStateOf<ru.sorewa.kino.data.Me?>(null)

    fun name(fallback: String): String =
        me?.displayName?.takeIf { it.isNotBlank() } ?: me?.login ?: fallback
}
