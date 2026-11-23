package ru.sorewa.kino.tvhome

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import kotlinx.coroutines.flow.MutableStateFlow
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.PlayerActivity
import ru.sorewa.kino.TvActivity

/**
 * Ссылки карточек главного экрана ТВ. Лаунчер запускает `intent:`-адрес
 * карточки; адрес ведёт в [TvLinkActivity] (с пакетом — без окна выбора
 * приложения), та решает, куда дальше:
 *
 *  - `play?title=&episode=` — сразу плеер с этой серией (продолжит с места);
 *  - `title?title=` — страница тайтла в ТВ-интерфейсе;
 *  - `home` — просто приложение (логотип канала).
 */
object TvLinks {
    private const val HOST_PLAY = "play"
    private const val HOST_TITLE = "title"
    private const val HOST_HOME = "home"

    /**
     * Тайтл, который ТВ-оболочка должна открыть, как только будет готова
     * (после входа, если его не было). Оболочка, уже открытая, отзывается сама.
     *
     * Поток, а не mutableStateOf: объект создаётся при первом обращении, и
     * после входа это было чтение из компоновки TvShell — состояние рождалось
     * внутри снимка Compose, и следующее чтение роняло приложение
     * («Reading a state that was created after the snapshot was taken»).
     */
    val openTitle = MutableStateFlow<Long?>(null)

    fun play(context: Context, titleId: Long, episodeId: Long): Uri =
        link(context, HOST_PLAY, "title" to titleId, "episode" to episodeId)

    fun title(context: Context, titleId: Long): Uri = link(context, HOST_TITLE, "title" to titleId)

    fun home(context: Context): Uri = link(context, HOST_HOME)

    private fun link(context: Context, host: String, vararg q: Pair<String, Long>): Uri {
        val data = Uri.Builder().scheme(context.packageName).authority(host).apply {
            q.forEach { (k, v) -> appendQueryParameter(k, v.toString()) }
        }.build()
        val intent = Intent(Intent.ACTION_VIEW, data).setPackage(context.packageName)
        return Uri.parse(intent.toUri(Intent.URI_INTENT_SCHEME))
    }

    internal fun route(activity: Activity, data: Uri?) {
        val app = activity.application as KinoApp
        val signedIn = app.container.api.currentToken.isNotEmpty()
        val title = data?.getQueryParameter("title")?.toLongOrNull() ?: 0
        val episode = data?.getQueryParameter("episode")?.toLongOrNull() ?: 0
        // Без входа — в приложение: там экран входа, карточки после выхода
        // и так стёрты, но лаунчер мог не успеть перерисоваться.
        if (signedIn && data?.host == HOST_PLAY && episode > 0) {
            activity.startActivity(
                Intent(activity, PlayerActivity::class.java)
                    .putExtra(PlayerActivity.EXTRA_EPISODE, episode)
                    .putExtra(PlayerActivity.EXTRA_TITLE, title)
                    .putExtra(PlayerActivity.EXTRA_AUTOPLAY, true)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
            return
        }
        if (signedIn && data?.host == HOST_TITLE && title > 0) openTitle.value = title
        activity.startActivity(Intent(activity, TvActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }
}

/** Батут без окна: разбирает ссылку карточки и сразу закрывается. */
class TvLinkActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        try {
            TvLinks.route(this, intent?.data)
        } finally {
            finish()
        }
    }
}
