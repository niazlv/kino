package ru.sorewa.kino.ui.player

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.media3.common.C
import androidx.media3.common.Player

/**
 * Живое состояние плеера для вида: читается из MediaController каждые
 * четверть секунды и по событиям. Серверная часть (тикеты, меню озвучек) —
 * в PlaybackUiState, сюда только то, что видит Media3.
 */
class PlayerViewState {
    var player: Player? by mutableStateOf(null)

    /** Катастрофа до всякого воспроизведения: служба не поднялась. */
    var fatalError by mutableStateOf<String?>(null)

    var pip by mutableStateOf(false)

    var titleName by mutableStateOf("")
    var episodeLabel by mutableStateOf("")
    var episodes by mutableStateOf<List<ru.sorewa.kino.data.Episode>>(emptyList())

    // Live-значения из плеера.
    var positionMs by mutableLongStateOf(0)
    var durationMs by mutableLongStateOf(0)
    var bufferedMs by mutableLongStateOf(0)
    var isPlaying by mutableStateOf(false)
    var ended by mutableStateOf(false)
    /** Ждёт данных посреди игры — кольцо ожидания. */
    var buffering by mutableStateOf(false)
    /** Серия ещё не дала первого кадра; holdPaused — на это время нажата пауза. */
    var starting by mutableStateOf(false)
    var holdPaused by mutableStateOf(false)
    /** Экран ждёт тайтл, тикет ещё не выпускался: кольцо ожидания. */
    var opening by mutableStateOf(false)
    var speed by mutableStateOf(1f)
    var qualityHeight by mutableStateOf<Int?>(null)

    /** Что играет сейчас: для «таблетки» качества и сводки о потоке. */
    var videoWidth by mutableStateOf(0)
    var videoHeight by mutableStateOf(0)
    var videoCodec by mutableStateOf("")
    var videoBitrate by mutableStateOf(0)
    var bandwidthBps by mutableLongStateOf(0)

    /** Дорожки потока: id формата → подпись. */
    var audioTracks by mutableStateOf<List<Pair<String, String>>>(emptyList())
    var textTracks by mutableStateOf<List<Pair<String, String>>>(emptyList())
    var selectedAudioFmt by mutableStateOf<String?>(null)
    var selectedTextFmt by mutableStateOf<String?>(null)

    fun refresh(c: Player?) {
        val p = c ?: return
        positionMs = p.currentPosition.coerceAtLeast(0)
        durationMs = if (p.duration > 0) p.duration else 0
        bufferedMs = p.bufferedPosition.coerceAtLeast(0)
        isPlaying = p.isPlaying
        ended = p.playbackState == Player.STATE_ENDED
        buffering = p.playbackState == Player.STATE_BUFFERING && p.playWhenReady
        speed = p.playbackParameters.speed
        qualityHeight = p.trackSelectionParameters.maxVideoHeight
            .takeIf { it != Int.MAX_VALUE && it > 0 }

        bandwidthBps = ru.sorewa.kino.player.PlaybackServiceHolder.bandwidth()
        // Адаптивный поток держит выбранными все варианты сразу — какой из
        // них в кадре, видно по размеру картинки.
        val formats = mutableListOf<androidx.media3.common.Format>()
        for (g in p.currentTracks.groups) {
            if (g.type != C.TRACK_TYPE_VIDEO) continue
            for (i in 0 until g.length) if (g.isTrackSelected(i)) formats.add(g.getTrackFormat(i))
        }
        val vs = p.videoSize
        val cur = formats.firstOrNull { vs.height > 0 && it.height == vs.height }
            ?: formats.maxByOrNull { it.height }
        val vw = if (vs.width > 0) vs.width else cur?.width ?: 0
        val vh = if (vs.height > 0) vs.height else cur?.height ?: 0
        val vc = (cur?.codecs ?: cur?.sampleMimeType ?: "").split(',').first().trim().substringBefore('.').substringAfter("video/")
        val vb = cur?.let { f -> listOf(f.bitrate, f.averageBitrate, f.peakBitrate).firstOrNull { it > 0 } } ?: 0
        videoWidth = vw; videoHeight = vh; videoCodec = vc; videoBitrate = vb

        val audio = mutableListOf<Pair<String, String>>()
        val text = mutableListOf<Pair<String, String>>()
        var selAudio: String? = null
        var selText: String? = null
        for (g in p.currentTracks.groups) {
            if (!g.isSupported) continue
            for (i in 0 until g.length) {
                val f = g.getTrackFormat(i)
                val id = f.id ?: ""
                val label = f.label ?: f.language ?: ""
                if (g.type == C.TRACK_TYPE_AUDIO) {
                    if (label.isNotEmpty() || id.isNotEmpty()) audio.add(id to label)
                    if (g.isTrackSelected(i)) selAudio = id
                } else if (g.type == C.TRACK_TYPE_TEXT) {
                    if (label.isNotEmpty() || id.isNotEmpty()) text.add(id to label)
                    if (g.isTrackSelected(i)) selText = id
                }
            }
        }
        audioTracks = audio
        textTracks = text
        selectedAudioFmt = selAudio
        selectedTextFmt = selText
    }

    /** Год тайтла — в шапке плеера «НАЗВАНИЕ S1E1 (2015)». */
    var year by mutableStateOf(0)

    fun setEpisodes(title: String, eps: List<ru.sorewa.kino.data.Episode>, year: Int = 0) {
        this.year = year
        titleName = title
        episodes = eps
    }

    fun applyEpisodeLabel(l: String) { episodeLabel = l }

    /** Какая серия выбрана сейчас (в локальном режиме — синтетический id). */
    var currentEpisodeId by mutableLongStateOf(0)
    fun setEpisodeId(id: Long) { currentEpisodeId = id }
    fun applyPip(b: Boolean) { pip = b }

    fun fatal(msg: String) { fatalError = msg }
}
