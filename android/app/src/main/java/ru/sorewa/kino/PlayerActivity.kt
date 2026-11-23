package ru.sorewa.kino

import android.app.PictureInPictureParams
import android.app.RemoteAction
import android.content.BroadcastReceiver
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.res.Configuration
import android.os.Build
import android.os.Bundle
import android.util.Rational
import android.view.WindowManager
import androidx.activity.OnBackPressedCallback
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.ComponentActivity
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.lifecycleScope
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.media3.common.MediaMetadata
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.common.VideoSize
import androidx.media3.datasource.HttpDataSource
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import com.google.common.util.concurrent.ListenableFuture
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import ru.sorewa.kino.data.Api
import ru.sorewa.kino.data.ApiError
import ru.sorewa.kino.data.Episode
import ru.sorewa.kino.data.PrefsPatch
import ru.sorewa.kino.data.TokenStore
import ru.sorewa.kino.player.PlaybackClient
import ru.sorewa.kino.player.PlaybackServiceHolder
import ru.sorewa.kino.player.PlaybackService
import ru.sorewa.kino.player.PlayerHandle
import ru.sorewa.kino.player.Voice
import ru.sorewa.kino.ui.player.PlayerScreen
import ru.sorewa.kino.ui.player.PlayerViewState
import ru.sorewa.kino.ui.theme.KinoTheme

/**
 * Экран плеера — общий для телефона и телевизора. Владеет MediaController'ом
 * и PlaybackClient'ом; весь вид — в ui/player/PlayerScreen.
 */
class PlayerActivity : ComponentActivity() {

    companion object {
        const val EXTRA_EPISODE = "episode"
        const val EXTRA_TITLE = "title"
        const val EXTRA_AUTOPLAY = "autoplay"
        /** Режим: MODE_STREAM (тикеты шлюза) или MODE_LOCAL (файл из библиотеки). */
        const val EXTRA_MODE = "mode"
        const val MODE_STREAM = "stream"
        const val MODE_LOCAL = "local"
        const val EXTRA_LIB_ID = "lib_id"

        /** Просмотренным файл считается с этой доли длины. */
        const val WATCHED_AT = 0.92

        const val ACTION_SEEK_BACK = "ru.sorewa.kino.pip.BACK"
        const val ACTION_PLAY_PAUSE = "ru.sorewa.kino.pip.TOGGLE"
        const val ACTION_SEEK_FWD = "ru.sorewa.kino.pip.FWD"
        private const val PIP_REQ_BACK = 1
        private const val PIP_REQ_TOGGLE = 2
        private const val PIP_REQ_FWD = 3

        /**
         * Попытки /titles/:id: сразу, повтор до старта — и, если стартовали
         * без него, ещё две в фоне (за списком серий для «следующей»).
         */
        private val DETAIL_RETRY_MS = longArrayOf(0, 1_500, 10_000, 30_000)
    }

    private lateinit var api: Api
    private lateinit var store: TokenStore
    private lateinit var state: ru.sorewa.kino.data.StateStore
    private var controller: MediaController? = null
    private var controllerFuture: ListenableFuture<MediaController>? = null
    // Состояние Compose: экран плеера рисуется раньше, чем подключается
    // служба и создаётся клиент. Обычная переменная оставляла экран без
    // клиента навсегда — ни ошибок, ни озвучек, ни кнопки пропуска.
    private var client: PlaybackClient? by androidx.compose.runtime.mutableStateOf<PlaybackClient?>(null)
    val ui = PlayerViewState()

    private var detail: ru.sorewa.kino.data.TitleDetail? = null
    /** /titles/:id не дался — стартуем без него (см. loadDetail). */
    private var detailFailed = false
    private var episodeId = 0L
    private var titleId = 0L
    private var autoStart = true
    private var started = false
    /**
     * Пауза, нажатая, пока серия ещё грузится (тикет в пути, первого кадра
     * не было): старт её не перебивает. Сбрасывается первым кадром и новой
     * серией. readyOnce — поток этого тикета уже был готов.
     */
    private var holdPaused = false
    private var readyOnce = false

    // Локальный режим: играем файл из библиотеки скачанного.
    private var localMode = false
    private var libId = ""
    private var localEntries: List<ru.sorewa.kino.data.LocalLibrary.Entry> = emptyList()
    private var localEntry: ru.sorewa.kino.data.LocalLibrary.Entry? = null

    private var ticker: Job? = null
    private var stallSince = 0L

    // Ожидаемые выборы дорожек: применяются, когда треки появятся (READY).
    private var pendingAudioLabel: String? = null
    private var pendingText: String? = null
    private var appliedForItem = ""

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        KinoApp.deviceKind = if (isTvDevice()) "tv" else "android"
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        episodeId = intent.getLongExtra(EXTRA_EPISODE, 0)
        titleId = intent.getLongExtra(EXTRA_TITLE, 0)
        autoStart = intent.getBooleanExtra(EXTRA_AUTOPLAY, true)
        ui.starting = autoStart
        localMode = intent.getStringExtra(EXTRA_MODE) == MODE_LOCAL
        libId = intent.getStringExtra(EXTRA_LIB_ID) ?: ""
        // До выпуска тикета (ждём тайтл) — тоже кольцо, а не пустой экран.
        ui.opening = !localMode

        val app = application as KinoApp
        api = app.container.api
        store = app.container.store
        state = app.container.state

        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() = finish()
        })

        setContent()
        connectController()
        if (localMode) {
            lifecycleScope.launch(Dispatchers.IO) { loadLocal() }
        } else {
            lifecycleScope.launch { loadDetail() }
        }
    }

    private suspend fun loadLocal() {
        val lib = (application as KinoApp).container.library
        localEntry = lib.find(libId)
        val entry = localEntry ?: run {
            withContext(Dispatchers.Main) { ui.fatal("запись не найдена в библиотеке") }
            return
        }
        titleId = entry.titleId
        localEntries = if (titleId > 0) lib.episodesOfTitle(titleId)
        else lib.list().filter { it.titleId == 0L && it.title == entry.title }
            .sortedWith(compareBy({ it.season }, { it.episode }))
        if (localEntries.none { it.id == entry.id }) localEntries = listOf(entry) + localEntries
        // Названия серий — из сохранённой карточки тайтла: без сети тоже.
        val saved = (application as KinoApp).container.offline.saved(titleId)?.detail
        withContext(Dispatchers.Main) {
            ui.setEpisodes((saved?.title?.title ?: entry.title).ifEmpty { "Скачанное" }, localEntries.mapIndexed { i, e ->
                val known = saved?.episodes?.find { it.seasonNo == e.season && it.episodeNo == e.episode }
                ru.sorewa.kino.data.Episode(
                    id = (i + 1).toLong(),
                    seasonNo = e.season, episodeNo = e.episode,
                    title = known?.title,
                    durationSec = (e.durationMs / 1000).takeIf { it > 0 } ?: known?.durationSec ?: 0,
                    positionMs = e.positionMs,
                    status = if (e.watched) "completed" else "in_progress",
                )
            })
            ui.applyEpisodeLabel(localLabel(entry))
            // Служба могла подключиться раньше — тогда старт за нами.
            maybeStart()
        }
    }

    private fun localLabel(entry: ru.sorewa.kino.data.LocalLibrary.Entry): String =
        listOfNotNull(
            entry.title.ifEmpty { "Скачанное" },
            "s${entry.season}e${entry.episode}"
                .takeIf { entry.season > 0 || entry.episode > 0 },
        ).joinToString(" · ")

    private fun setContent() = setContent {
        KinoTheme {
            PlayerScreen(
                state = ui,
                client = client,
                isTv = isTvDevice(),
                onBack = { finish() },
                onPip = { enterPip() },
                onQuality = { h -> controller?.let { setQuality(it, h) } },
                onSpeed = { s -> controller?.let { c ->
                    c.playbackParameters = c.playbackParameters.withSpeed(s)
                } },
                onSelectAudio = { sel -> selectAudio(sel) },
                onSelectText = { sel -> selectText(sel) },
                onSeek = { ms -> controller?.seekTo(ms) },
                onTogglePlay = { togglePlay() },
                onNext = { openEpisode(nextEpisode()) },
                onPrev = { openEpisode(prevEpisode()) },
                onCreditsNext = { client?.creditsNext() },
                onJumpToEpisode = { openEpisode(it) },
            )
        }
    }

    private fun connectController() {
        val token = SessionToken(this, ComponentName(this, PlaybackService::class.java))
        val future = MediaController.Builder(this, token).buildAsync()
        controllerFuture = future
        future.addListener({
            try {
                onControllerConnected(future.get())
            } catch (e: Exception) {
                // Служба не поднялась — без плеера дальше нечего показывать.
                ui.fatal("не удалось запустить плеер")
            }
        }, androidx.core.content.ContextCompat.getMainExecutor(this))
    }

    private fun onControllerConnected(c: MediaController) {
        controller = c
        // Служба общая и переживает экран: в ней ещё лежит поток прошлого
        // экрана (onDestroy ставит его на паузу, но не убирает). Новый экран
        // начинает с пустого плеера — иначе до первого кадра своей серии (а
        // при сорванном старте — навсегда) виден чужой кадр с 0:00/0:00.
        // Фоновую игру это не задевает: она живёт, пока жив тот же экран, —
        // он singleTask, а шторка его заново не создаёт (sessionActivity у
        // сессии нет, возврат — через задачу приложения). stop() — до
        // очистки: пустой список в READY дал бы STATE_ENDED.
        if (c.mediaItemCount > 0) {
            c.stop()
            c.clearMediaItems()
        }
        c.addListener(playerListener)
        client = PlaybackClient(
            api, store, state, PlayerHandleImpl(c),
            deviceKind = KinoApp.deviceKind,
            appVersion = BuildConfig.VERSION_NAME,
            onPrefs = { patch -> persistPrefs(patch) },
            onEnded = { /* окончание уже доложено клиентом */ },
            onNextEpisode = { openEpisode(nextEpisode()) },
        )
        ui.player = c
        maybeStart()
    }

    private fun persistPrefs(patch: PrefsPatch) {
        if (titleId <= 0) return
        val ts = api.nowIso()
        lifecycleScope.launch(Dispatchers.IO) {
            // Сначала в очередь: сорвавшийся PUT не потеряет выбор озвучки.
            state.queuePrefs(titleId, patch, ts)
            try {
                api.setPrefs(titleId, patch)
                state.ackPrefs(titleId, ts)
            } catch (_: Exception) { }
        }
    }

    /**
     * Тайтл: список серий и запомненные озвучка/площадка. Не приехал — плеер
     * всё равно стартует: иначе maybeStart ждал бы его вечно, и экран молча
     * стоял бы на кадре прошлого потока. Быстрый сбой (сеть, 5xx) — ещё одна
     * попытка через 1,5 с, затем старт без списка: без «следующей» и
     * запомненного выбора, ошибку выпуска покажет клиент («Повторить»).
     * Список потом добирается в фоне — «следующая» появится.
     */
    private suspend fun loadDetail() {
        if (titleId <= 0) return
        for ((attempt, wait) in DETAIL_RETRY_MS.withIndex()) {
            if (wait > 0) delay(wait)
            val t0 = android.os.SystemClock.elapsedRealtime()
            try {
                val d = withContext(Dispatchers.IO) {
                    debugFailTitle(attempt)
                    api.title(titleId)
                }
                // Выбор озвучки, ещё не доехавший до сервера (сорвавшийся
                // PUT), не теряется: ждущий патч ложится на prefs тайтла.
                val d2 = withContext(Dispatchers.IO) {
                    state.pendingTitle(titleId)?.prefs?.let { patch ->
                        d.copy(prefs = ru.sorewa.kino.data.applyPrefsPatch(d.prefs, patch))
                    } ?: d
                }
                detail = d2
                ui.setEpisodes(d.title.title, d.episodes, d.title.year)
                // Приехал после старта без него: шапка и «следующая».
                if (started) ui.applyEpisodeLabel(episodeLabel(currentEpisode(), d.title.title))
                maybeStart()
                return
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e // экран закрыт — ни старта, ни повторов
            } catch (e: Exception) {
                android.util.Log.w(PlaybackClient.TAG, "title $titleId, попытка ${attempt + 1}: ${e.message}")
                // ТВ: карточку главного экрана нажали, а сессию уже погасили
                // («Выйти на всех», истёк вход). Плеер без сессии стоял бы на
                // кадре прошлого потока — уводим в приложение, там вход.
                // Уже играющий (стартовал без тайтла) фоновый повтор не рвёт.
                if (e is ApiError && e.sessionDead && isTvDevice() && !started) {
                    startActivity(Intent(this, TvActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                    finish()
                    return
                }
                // Сессия кончилась или тайтла нет — повтор не поможет, а
                // выпуск тикета скажет это сам. Долгий сбой (таймаут) второй
                // раз не ждём.
                val hopeless = e is ApiError && (e.sessionDead || e.status == 404)
                val slow = android.os.SystemClock.elapsedRealtime() - t0 > 5_000
                if (attempt >= 1 || hopeless || slow) {
                    detailFailed = true
                    maybeStart()
                }
                if (hopeless) return
            }
        }
    }

    private fun maybeStart() {
        if (started || controller == null || isFinishing) return
        if (localMode) {
            // Служба могла подключиться раньше, чем прочиталась библиотека.
            if (localEntry == null) return
        } else if (client == null || (titleId > 0 && detail == null && !detailFailed)) {
            return
        }
        started = true
        if (localMode) {
            playLocalEntry(localEntry!!, fromStart = !autoStart)
        } else {
            ui.opening = false
            val label = episodeLabel(currentEpisode(), detail?.title?.title)
            ui.applyEpisodeLabel(label)
            // Первая серия: пауза, нажатая ещё до старта, сохраняется. Пустой
            // плеер заранее «собирается играть» — тогда пауза с пульта через
            // медиасессию меняет его состояние и не теряется.
            controller?.let { c -> if (autoStart && !holdPaused && c.mediaItemCount == 0) c.playWhenReady = true }
            client!!.start(episodeId, detail?.prefs, autoStart)
        }
        startTicker()
    }

    /** Файл из библиотеки: позиция — из записи, просмотренное — с начала. */
    private fun playLocalEntry(entry: ru.sorewa.kino.data.LocalLibrary.Entry, fromStart: Boolean) {
        val c = controller ?: return
        localEntry = entry
        libId = entry.id
        val idx = localEntries.indexOfFirst { it.id == entry.id }
        ui.setEpisodeId(if (idx >= 0) (idx + 1).toLong() else 1)
        ui.applyEpisodeLabel(localLabel(entry))
        val start = if (fromStart || entry.watched) 1L else entry.positionMs
        val item = MediaItem.Builder()
            .setUri(android.net.Uri.parse(entry.uri))
            .setMediaId(entry.id)
            .setMediaMetadata(
                MediaMetadata.Builder()
                    .setTitle(entry.title.ifEmpty { "Скачанное" })
                    .build(),
            )
            .build()
        c.setMediaItem(item, start.coerceAtLeast(0))
        c.prepare()
        c.playWhenReady = true
        ui.refresh(c)
    }

    private val playerListener = object : Player.Listener {
        override fun onPlaybackStateChanged(playbackState: Int) {
            when (playbackState) {
                Player.STATE_BUFFERING -> { client?.onBufferingStart(); stallSince = System.currentTimeMillis() }
                Player.STATE_READY -> {
                    client?.onBufferingEnd()
                    client?.onFirstFrame()
                    applyPendingTracks()
                    stallSince = 0
                    if (client?.state?.value?.preparing != true) resetHold(ready = true)
                }
                Player.STATE_ENDED -> when {
                    // Пустой плеер (прежний поток убран) — не конец серии.
                    (controller?.mediaItemCount ?: 0) == 0 -> Unit
                    localMode -> onLocalEnded()
                    else -> client?.onEndedInternal()
                }
            }
            ui.refresh(controller)
        }

        override fun onIsPlayingChanged(isPlaying: Boolean) {
            ui.refresh(controller)
            updatePipAutoEnter()
        }

        override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
            // Пауза/игра, пока серия грузится, — в том числе с пульта через
            // медиасессию, когда экран ещё не взял фокус: запоминаем намерение.
            if (!localMode && !readyOnce && reason == Player.PLAY_WHEN_READY_CHANGE_REASON_USER_REQUEST) {
                holdPaused = !playWhenReady
                ui.holdPaused = holdPaused
                if (playWhenReady) ui.starting = true
            }
            ui.refresh(controller)
        }

        override fun onRenderedFirstFrame() {
            client?.onFirstFrame()
        }

        override fun onVideoSizeChanged(videoSize: VideoSize) {
            client?.onVideoHeight(videoSize.height)
        }

        override fun onTracksChanged(tracks: Tracks) {
            ui.refresh(controller)
            applyPendingTracks()
            client?.onAudioTracksChanged(audioLabels(tracks))
        }

        override fun onPlayerError(error: PlaybackException) {
            val status = (error.cause as? HttpDataSource.InvalidResponseCodeException)?.responseCode
            client?.onFatalError(status, error.errorCodeName)
            ui.refresh(controller)
        }
    }

    private fun startTicker() {
        ticker?.cancel()
        // Живые значения для вида: позиция, буфер, дорожки.
        ticker = lifecycleScope.launch {
            while (true) {
                delay(250)
                val c = controller ?: continue
                ui.refresh(c)
                if (c.duration > 0) client?.rebuildSkips(c.duration)
            }
        }
        // Прогресс и телеметрия — реже: раз в 15 с, как в витрине.
        lifecycleScope.launch {
            while (true) {
                delay(15_000)
                val c = controller ?: continue
                if (localMode) {
                    if (c.playbackState == Player.STATE_READY && c.duration > 0) {
                        val lib = (application as KinoApp).container.library
                        lib.progress(libId, c.currentPosition, c.duration, c.currentPosition >= c.duration * WATCHED_AT)
                    }
                    continue
                }
                val cl = client ?: continue
                if (c.playbackState == Player.STATE_READY && c.duration > 0) {
                    cl.tick(c.currentPosition, c.duration, PlaybackServiceHolder.bandwidth())
                }
                // Долго буферизуется при готовом играть — площадка тормозит.
                if (c.playWhenReady && c.playbackState == Player.STATE_BUFFERING) {
                    if (stallSince == 0L) stallSince = System.currentTimeMillis()
                    if (System.currentTimeMillis() - stallSince > 10_000) {
                        stallSince = 0
                        cl.onStall()
                    }
                } else {
                    stallSince = 0
                }
            }
        }
    }

    /** Файл доигран: просмотренным, затем следующая скачанная — офлайн-марафон. */
    private fun onLocalEnded() {
        val lib = (application as KinoApp).container.library
        lib.setWatched(libId, true)
        val cur = localEntry
        val next = localEntries
            .filter { !it.watched && it.id != cur?.id }
            .sortedWith(compareBy({ it.season }, { it.episode }))
            .firstOrNull()
        if (next != null) {
            playLocalEntry(next, fromStart = false)
        } else {
            finish()
        }
    }

    // ---- серии: следующая / предыдущая ----

    private fun currentEpisode(): Episode? = detail?.episodes?.find { it.id == episodeId }

    private fun nextEpisode(): Episode? {
        val eps = detail?.episodes ?: return null
        val cur = currentEpisode() ?: return eps.firstOrNull()
        return eps.getOrNull(eps.indexOf(cur) + 1)
    }

    private fun prevEpisode(): Episode? {
        val eps = detail?.episodes ?: return null
        val cur = currentEpisode() ?: return null
        val idx = eps.indexOf(cur)
        return if (idx > 0) eps.getOrNull(idx - 1) else null
    }

    /**
     * Подпись в шапке плеера — как у витрины (Title.tsx, epLabel):
     * «Тьма / Dark · s1e2 · Название серии»; у фильма — только название.
     */
    /**
     * Рубильник для проверки переключения площадок — только в отладочной
     * сборке: `adb shell setprop debug.kino.failsource kinopub` портит адрес
     * потока этой площадки (шлюз ответит 404), «all» — всех. Снять:
     * `setprop debug.kino.failsource ""`.
     */
    private fun debugBreak(source: String?, url: String): String {
        if (!BuildConfig.DEBUG) return url
        val fail = debugProp("debug.kino.failsource")
        android.util.Log.w("KinoPlayback", "debug: источник $source, рубильник «$fail»")
        if (fail.isBlank() || (fail != "all" && fail != source)) return url
        android.util.Log.w("KinoPlayback", "debug: ломаю поток $source")
        return url.replace("master.m3u8", "broken-by-debug.m3u8")
    }

    /**
     * Рубильник для проверки старта без тайтла — только в отладочной сборке:
     * `adb shell setprop debug.kino.failtitle 503` — /titles/:id «отвечает»
     * этим кодом (0 — нет связи, 401 — сессия кончилась); `503x1` — ломается
     * только первая попытка (проверка повтора). Снять: `setprop … ""`.
     */
    private fun debugFailTitle(attempt: Int) {
        if (!BuildConfig.DEBUG) return
        val v = debugProp("debug.kino.failtitle")
        if (v.isBlank()) return
        val status = v.substringBefore('x').toIntOrNull() ?: 503
        val times = v.substringAfter('x', "").toIntOrNull()
        if (times != null && attempt >= times) return
        android.util.Log.w("KinoPlayback", "debug: ломаю /titles/$titleId → $status (попытка ${attempt + 1})")
        throw if (status == 0) ApiError(0, "offline", "нет связи с сервером: debug")
        else ApiError(status, "debug", "debug: HTTP $status")
    }

    // Скрытый SystemProperties новым Android закрыт — читаем через getprop.
    private fun debugProp(name: String): String = try {
        Runtime.getRuntime().exec(arrayOf("getprop", name))
            .inputStream.bufferedReader().readText().trim()
    } catch (_: Exception) { "" }

    private fun notifTitle(): String {
        val name = ru.sorewa.kino.ui.splitTitle(detail?.title?.title ?: ui.titleName).first
        val ep = currentEpisode()
        return if (ep != null && ep.seasonNo > 0) "$name S${ep.seasonNo}E${ep.episodeNo}" else name
    }

    private fun episodeLabel(ep: Episode?, title: String?): String {
        val t = title ?: ""
        if (ep == null || ep.seasonNo <= 0) return t
        return listOfNotNull(
            t.takeIf { it.isNotEmpty() },
            "s${ep.seasonNo}e${ep.episodeNo}",
            ep.title?.takeIf { it.isNotBlank() },
        ).joinToString(" · ")
    }

    private fun openEpisode(ep: Episode?, autoplay: Boolean = true) {
        if (localMode) {
            // Синтетический id = индекс+1 в localEntries.
            val entry = localEntries.getOrNull((ep?.id ?: 0).toInt() - 1) ?: return
            playLocalEntry(entry, fromStart = false)
            return
        }
        if (ep == null || ep.id == episodeId) return
        episodeId = ep.id
        appliedForItem = ""
        ui.applyEpisodeLabel(episodeLabel(ep, detail?.title?.title))
        resetHold()
        ui.starting = autoplay
        // Прежнюю серию убираем сразу: пока выпускается тикет, не виден её
        // кадр и не идёт её звук, а сорвётся выпуск — под ошибкой чёрный
        // экран, не прошлая серия. Только на виду (в том числе PiP): в фоне
        // (автоследующая под шторкой) пустой список снял бы уведомление и
        // foreground, а вернуть его из фона Android 12+ не даёт.
        if (lifecycle.currentState.isAtLeast(androidx.lifecycle.Lifecycle.State.STARTED)) {
            controller?.let { c -> c.stop(); c.clearMediaItems() }
        }
        client?.start(ep.id, detail?.prefs, autoplay)
    }

    // ---- дорожки ----

    private fun applyPendingTracks() {
        val c = controller ?: return
        val key = c.currentMediaItem?.mediaId ?: return
        if (key == appliedForItem) return
        appliedForItem = key
        val params = c.trackSelectionParameters.buildUpon()
        // Озвучка: подпись студии, которую выбрали/запомнили.
        pendingAudioLabel?.takeIf { it.isNotEmpty() }?.let { label ->
            val k = Voice.key(label)
            findAudioGroup(c) { Voice.key(it.second) == k }?.let { (group, idx) ->
                params.setOverrideForType(TrackSelectionOverride(group.mediaTrackGroup, idx))
            }
        }
        // Субтитры: "" — выключены; подпись — включить именно их.
        when {
            pendingText == "" -> params.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            pendingText != null -> {
                val want = pendingText!!
                val hit = findTextGroup(c) { Voice.key(it.second) == Voice.key(want) || it.second == want }
                if (hit != null) {
                    params.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
                    params.setOverrideForType(TrackSelectionOverride(hit.first.mediaTrackGroup, hit.second))
                }
            }
        }
        c.trackSelectionParameters = params.build()
    }

    private fun selectAudio(selector: String) {
        val c = controller ?: return
        val hit = when {
            selector.startsWith("fmt:") ->
                findAudioGroup(c) { it.first == selector.removePrefix("fmt:") }
            selector.startsWith("key:") ->
                findAudioGroup(c) { Voice.key(it.second) == selector.removePrefix("key:") }
            else -> null
        } ?: return
        c.trackSelectionParameters = c.trackSelectionParameters.buildUpon()
            .setOverrideForType(TrackSelectionOverride(hit.first.mediaTrackGroup, hit.second))
            .build()
        if (selector.startsWith("key:")) {
            val f = hit.first.getTrackFormat(hit.second)
            client?.markAudioTrackSelected(f.label ?: f.language ?: "")
        }
        ui.refresh(c)
    }

    private fun selectText(selector: String) {
        val c = controller ?: return
        val params = c.trackSelectionParameters.buildUpon()
        when (selector) {
            "" -> params.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            else -> {
                val hit = findTextGroup(c) { it.first == selector } ?: return
                params.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
                params.setOverrideForType(TrackSelectionOverride(hit.first.mediaTrackGroup, hit.second))
            }
        }
        c.trackSelectionParameters = params.build()
        ui.refresh(c)
        client?.onSubtitlePref(selector)
    }

    private fun setQuality(c: Player, height: Int?) {
        c.trackSelectionParameters = c.trackSelectionParameters.buildUpon()
            .setMaxVideoSize(Int.MAX_VALUE, height ?: Int.MAX_VALUE)
            .build()
        ui.refresh(c)
    }

    fun togglePlay() {
        val c = controller
        if (c == null) {
            // Плеер ещё подключается: запоминаем намерение, старт его исполнит.
            if (!localMode) {
                val intends = !holdPaused && ui.starting
                holdPaused = intends
                if (!intends) ui.starting = true
                ui.holdPaused = holdPaused
            }
            return
        }
        // Серия ещё грузится: нажатие — намерение, его исполнит старт потока.
        if (!localMode && !readyOnce) {
            // Сейчас «собирается играть» — тогда нажатие ставит паузу.
            val intends = !holdPaused && (ui.starting || c.playWhenReady)
            if (intends) { holdPaused = true; c.pause() }
            else { holdPaused = false; ui.starting = true; client?.onPlayPressed(); c.play() }
            ui.holdPaused = holdPaused
            ui.refresh(c)
            return
        }
        if (c.playbackState == Player.STATE_IDLE) c.prepare()
        // Ждёт данных, но собирается играть — для человека это «играет»:
        // OK ставит паузу, а не повторяет «играть».
        val playing = c.isPlaying || (c.playWhenReady && c.playbackState == Player.STATE_BUFFERING)
        if (playing) c.pause() else { client?.onPlayPressed(); c.play() }
        ui.refresh(c)
    }

    private fun resetHold(ready: Boolean = false) {
        holdPaused = false
        ui.holdPaused = false
        if (ready) ui.starting = false
        readyOnce = ready
    }

    /** Подписи всех дорожек звука потока — их ключи спросят у сервера. */
    private fun audioLabels(tracks: Tracks): List<String> {
        val out = mutableListOf<String>()
        for (g in tracks.groups) {
            if (g.type != C.TRACK_TYPE_AUDIO) continue
            for (i in 0 until g.length) {
                val f = g.getTrackFormat(i)
                (f.label ?: f.language)?.takeIf { it.isNotBlank() }?.let { out += it }
            }
        }
        return out
    }

    private fun findAudioGroup(c: Player, pred: (Pair<String, String>) -> Boolean): Pair<Tracks.Group, Int>? {
        for (g in c.currentTracks.groups) {
            if (g.type != C.TRACK_TYPE_AUDIO) continue
            for (i in 0 until g.length) {
                val f = g.getTrackFormat(i)
                val pair = (f.id ?: "") to (f.label ?: f.language ?: "")
                if (pred(pair)) return g to i
            }
        }
        return null
    }

    private fun findTextGroup(c: Player, pred: (Pair<String, String>) -> Boolean): Pair<Tracks.Group, Int>? {
        for (g in c.currentTracks.groups) {
            if (g.type != C.TRACK_TYPE_TEXT) continue
            for (i in 0 until g.length) {
                val f = g.getTrackFormat(i)
                val pair = (f.id ?: "") to (f.label ?: f.language ?: "")
                if (pred(pair)) return g to i
            }
        }
        return null
    }

    // ---- PiP ----

    private val pipReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            when (intent.action) {
                ACTION_SEEK_BACK -> controller?.seekTo((controller!!.currentPosition - 15_000).coerceAtLeast(0))
                ACTION_SEEK_FWD -> controller?.seekTo(controller!!.currentPosition + 15_000)
                ACTION_PLAY_PAUSE -> togglePlay()
            }
        }
    }

    private fun pipSupported(): Boolean =
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.O &&
            packageManager.hasSystemFeature(android.content.pm.PackageManager.FEATURE_PICTURE_IN_PICTURE) &&
            !isTvDevice()

    private fun enterPip() {
        if (!pipSupported()) return
        try {
            enterPictureInPictureMode(buildPipParams(controller?.isPlaying == true))
        } catch (_: Exception) { }
    }

    private fun buildPipParams(autoEnter: Boolean): PictureInPictureParams {
        val b = PictureInPictureParams.Builder().setAspectRatio(aspectFor())
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) b.setAutoEnterEnabled(autoEnter)
        // Кнопки PiP: −15 c, пауза/играть, +15 c — те же 15 секунд, что в плеере.
        fun pending(action: String, req: Int) = android.app.PendingIntent.getBroadcast(
            this, req, Intent(action).setPackage(packageName),
            android.app.PendingIntent.FLAG_IMMUTABLE,
        )
        b.setActions(listOf(
            RemoteAction(
                android.graphics.drawable.Icon.createWithResource(this, android.R.drawable.ic_media_rew),
                "Назад на 15 секунд", "", pending(ACTION_SEEK_BACK, PIP_REQ_BACK),
            ),
            RemoteAction(
                android.graphics.drawable.Icon.createWithResource(
                    this,
                    if (controller?.isPlaying == true) android.R.drawable.ic_media_pause else android.R.drawable.ic_media_play,
                ),
                "Пауза / играть", "", pending(ACTION_PLAY_PAUSE, PIP_REQ_TOGGLE),
            ),
            RemoteAction(
                android.graphics.drawable.Icon.createWithResource(this, android.R.drawable.ic_media_ff),
                "Вперёд на 15 секунд", "", pending(ACTION_SEEK_FWD, PIP_REQ_FWD),
            ),
        ))
        return b.build()
    }

    private fun updatePipAutoEnter() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S || !pipSupported()) return
        try { setPictureInPictureParams(buildPipParams(controller?.isPlaying == true)) } catch (_: Exception) { }
    }

    private fun aspectFor(): Rational {
        val c = controller ?: return Rational(16, 9)
        if (c.videoSize.width <= 0 || c.videoSize.height <= 0) return Rational(16, 9)
        val r = Rational(c.videoSize.width, c.videoSize.height)
        return when {
            r.toFloat() > 2.39f -> Rational(239, 100)
            1f / r.toFloat() > 2.39f -> Rational(100, 239)
            else -> r
        }
    }

    override fun onPictureInPictureModeChanged(isInPictureInPictureMode: Boolean, newConfig: Configuration) {
        super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
        ui.applyPip(isInPictureInPictureMode)
        if (!isInPictureInPictureMode && Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && isFinishing) {
            // PiP закрыли крестиком — воспроизведение останавливается.
            client?.stop()
            controller?.pause()
        }
    }

    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        // Уход с приложения на Android 8–11: авто-PiP, если смотрим.
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S && pipSupported() && controller?.isPlaying == true) {
            enterPip()
        }
    }

    // ---- системные панели, жизненный цикл ----

    private fun hideSystemBars() {
        val w = window
        WindowCompat.setDecorFitsSystemWindows(w, false)
        val ctl = WindowInsetsControllerCompat(w, w.decorView)
        ctl.hide(WindowInsetsCompat.Type.systemBars())
        ctl.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus && !ui.pip) hideSystemBars()
    }

    override fun onStart() {
        super.onStart()
        val filter = IntentFilter().apply {
            addAction(ACTION_SEEK_BACK); addAction(ACTION_PLAY_PAUSE); addAction(ACTION_SEEK_FWD)
        }
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(pipReceiver, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            registerReceiver(pipReceiver, filter)
        }
    }

    /**
     * Телевизор: карточка главного экрана («Продолжить просмотр») нажата,
     * пока плеер жив в фоне. Та же серия — просто возвращаемся; другая —
     * экран начинается заново с новой. На телефоне — как было.
     */
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        if (!isTvDevice()) return
        val same = if (intent.getStringExtra(EXTRA_MODE) == MODE_LOCAL) {
            localMode && intent.getStringExtra(EXTRA_LIB_ID) == libId
        } else {
            !localMode && intent.getLongExtra(EXTRA_EPISODE, 0) == episodeId
        }
        if (same) return
        setIntent(intent)
        recreate()
    }

    override fun onStop() {
        super.onStop()
        // ТВ: досмотренное и позиция — на главный экран, когда плеер дошлёт прогресс.
        if (!localMode) ru.sorewa.kino.tvhome.TvLauncherWork.refreshSoon(this, 5_000)
        unregisterReceiver(pipReceiver)
        // Ушли с экрана (не закрыли): финальный репорт — 15-секундный тикер
        // к этому моменту мог не дотикать.
        if (localMode) {
            controller?.let { c ->
                if (c.playbackState == Player.STATE_READY && c.duration > 0) {
                    (application as KinoApp).container.library.progress(
                        libId, c.currentPosition, c.duration,
                        c.currentPosition >= c.duration * WATCHED_AT,
                    )
                }
            }
        } else {
            client?.flushProgress()
            if (!ui.pip) client?.sendTelemetry("background", "")
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        if (!ui.pip) {
            if (!localMode) client?.stop()
            // Закрыли экран — воспроизведение останавливается. Фонно играет
            // только тот, кто ушёл в шторку или PiP.
            controller?.pause()
        }
        ticker?.cancel()
        controller?.removeListener(playerListener)
        controller?.release()
        controllerFuture?.let { MediaController.releaseFuture(it) }
        controllerFuture = null
        controller = null
    }

    private fun isTvDevice(): Boolean =
        packageManager.hasSystemFeature(android.content.pm.PackageManager.FEATURE_LEANBACK) ||
            (getSystemService(android.app.UiModeManager::class.java))?.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION

    /** Реализация пяти действий плеера через MediaController. */
    private inner class PlayerHandleImpl(val c: Player) : PlayerHandle {
        override fun positionMs(): Long = c.currentPosition
        override fun durationMs(): Long = c.duration
        // «Хочет играть», а не «играет сейчас»: поток, упавший до первого
        // кадра, не играл ни секунды — и подмена на другую площадку вставала
        // на паузу вместо того, чтобы продолжить.
        override fun isPlaying(): Boolean =
            c.isPlaying || (c.playWhenReady && c.playbackState != Player.STATE_ENDED)

        override fun currentAudioLabel(): String? {
            for (g in c.currentTracks.groups) {
                if (g.type != C.TRACK_TYPE_AUDIO || !g.isSelected) continue
                for (i in 0 until g.length) {
                    if (g.isTrackSelected(i)) {
                        val f = g.getTrackFormat(i)
                        return (f.label ?: f.language)?.takeIf { it.isNotEmpty() } ?: f.label
                    }
                }
            }
            return null
        }

        override fun currentAudioFormatId(): String? {
            for (g in c.currentTracks.groups) {
                if (g.type != C.TRACK_TYPE_AUDIO || !g.isSelected) continue
                for (i in 0 until g.length) {
                    if (g.isTrackSelected(i)) return g.getTrackFormat(i).id
                }
            }
            return null
        }

        override fun applyTicket(
            ticket: ru.sorewa.kino.data.Playback,
            startMs: Long,
            autoPlay: Boolean,
            preferredAudioLabel: String?,
            preferredTextLabel: String?,
        ) {
            pendingAudioLabel = preferredAudioLabel
            pendingText = preferredTextLabel
            val subs = ticket.subtitles.filter { it.url != null }.map { s ->
                MediaItem.SubtitleConfiguration.Builder(android.net.Uri.parse(api.absolute(s.url!!)))
                    .setMimeType(MimeTypes.TEXT_VTT)
                    .setLabel(s.title.ifEmpty { s.language })
                    .setLanguage(s.language.ifEmpty { null })
                    .setSelectionFlags(if (s.default) C.SELECTION_FLAG_DEFAULT else 0)
                    .build()
            }
            val item = MediaItem.Builder()
                .setUri(debugBreak(ticket.source?.id, api.absolute(ticket.url)))
                .setMediaId("ep-${ticket.ticketId}")
                .setSubtitleConfigurations(subs)
                .setMediaMetadata(
                    MediaMetadata.Builder()
                        // Шторка — как шапка плеера: «Название S1E1», ниже название серии.
                        .setTitle(notifTitle())
                        .setArtist(currentEpisode()?.title?.takeIf { it.isNotBlank() } ?: "")
                        .setArtworkUri(detail?.title?.hasArt?.let {
                            android.net.Uri.parse(api.artUrl(detail!!.title.id))
                        })
                        .build(),
                )
                .build()
            c.setMediaItem(item, startMs.coerceAtLeast(0))
            c.prepare()
            readyOnce = false
            // Старт серии: намерение человека (ui.starting) важнее флага
            // автозапуска; подмена потока посреди серии — как было.
            c.playWhenReady = (autoPlay || ui.starting) && !holdPaused
        }

        override fun selectInStreamAudio(selector: String) = this@PlayerActivity.selectAudio(selector)

        override fun setQuality(height: Int?) {
            controller?.let { this@PlayerActivity.setQuality(it, height) }
        }
    }
}
