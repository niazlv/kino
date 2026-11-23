package ru.sorewa.kino.player

import android.content.Intent
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import kotlinx.coroutines.flow.MutableStateFlow

/**
 * Служба воспроизведения: владеет ExoPlayer'ом и MediaSession.
 *
 * Отсюда фонное аудио, шторка уведомлений и локскрин — то, чего не могло
 * быть у WebView. Активность общается с ней через MediaController.
 */
@UnstableApi
class PlaybackService : MediaSessionService() {

    private var mediaSession: MediaSession? = null
    private var player: ExoPlayer? = null

    /** Оценка канала из анонсов ExoPlayer'а — для памяти «с какой площадки как тянет». */
    val bandwidthBps = MutableStateFlow(0L)

    override fun onCreate() {
        super.onCreate()
        player = ExoPlayer.Builder(this)
            // Фон: аудиофокус наш, наушники вынули — пауза, экран гаснет — играем.
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(C.USAGE_MEDIA)
                    .setContentType(C.AUDIO_CONTENT_TYPE_MOVIE)
                    .build(),
                true,
            )
            .setHandleAudioBecomingNoisy(true)
            .setWakeMode(C.WAKE_MODE_NETWORK)
            .build()
        player?.addAnalyticsListener(BandwidthListener())
        // Обложка в шторке и на локскрине: сервер отдаёт её только с сессией,
        // а загрузчик Media3 ходил без cookie — 401 и пустое уведомление.
        val api = (application as ru.sorewa.kino.KinoApp).container.api
        val withSession = androidx.media3.datasource.ResolvingDataSource.Factory(
            androidx.media3.datasource.DefaultHttpDataSource.Factory(),
        ) { spec ->
            val t = api.currentToken
            if (t.isEmpty()) spec else spec.withAdditionalHeaders(mapOf("Cookie" to "kino_session=$t"))
        }
        val bitmaps = androidx.media3.session.CacheBitmapLoader(
            androidx.media3.datasource.DataSourceBitmapLoader(
                androidx.media3.datasource.DataSourceBitmapLoader.DEFAULT_EXECUTOR_SERVICE.get(),
                withSession,
            ),
        )
        mediaSession = MediaSession.Builder(this, player!!).setBitmapLoader(bitmaps).build()
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = mediaSession

    override fun onTaskRemoved(rootIntent: Intent?) {
        val p = player
        if (p == null || !p.playWhenReady || p.mediaItemCount == 0) {
            stopSelf()
        } else {
            // Играет — остаёмся в уведомлении: кнопки там, аудио живёт.
            p.pause()
        }
    }

    override fun onDestroy() {
        mediaSession?.run {
            player.release()
            release()
            mediaSession = null
        }
        player = null
        super.onDestroy()
    }

    private inner class BandwidthListener : androidx.media3.exoplayer.analytics.AnalyticsListener {
        override fun onBandwidthEstimate(
            eventTime: androidx.media3.exoplayer.analytics.AnalyticsListener.EventTime,
            totalLoadTimeMs: Int,
            totalLoadBytes: Long,
            currentLoadBitrate: Long,
        ) {
            if (currentLoadBitrate > 0) {
                bandwidthBps.value = currentLoadBitrate
                PlaybackServiceHolder.publish(currentLoadBitrate)
            }
        }
    }
}
