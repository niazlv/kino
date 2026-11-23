package ru.sorewa.kino.player

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import ru.sorewa.kino.data.Api
import ru.sorewa.kino.data.ApiError
import ru.sorewa.kino.data.Prefs
import ru.sorewa.kino.data.PrefsPatch
import ru.sorewa.kino.data.Playback
import ru.sorewa.kino.data.PlaybackChoice
import ru.sorewa.kino.data.ProgressState
import ru.sorewa.kino.data.SkipSegment
import ru.sorewa.kino.data.StateStore
import ru.sorewa.kino.data.TokenStore
import ru.sorewa.kino.data.Track
import ru.sorewa.kino.data.VoiceList
import ru.sorewa.kino.data.parseIsoTime

/** Что плеер умеет: клиенту не нужен весь Media3, только эти пять действий. */
interface PlayerHandle {
    fun positionMs(): Long
    fun durationMs(): Long
    fun isPlaying(): Boolean
    /** Текущая дорожка потока, как её назвала площадка (null — нет дорожек). */
    fun currentAudioLabel(): String?
    /**
     * Подмена источника. preferredAudioLabel — студия, по которой выбрать
     * дорожку потока (у kinopub озвучка и есть дорожка); preferredTextLabel —
     * субтитры: null — по умолчанию потока, "" — выключены, подпись — включить.
     */
    fun applyTicket(
        ticket: Playback, startMs: Long, autoPlay: Boolean,
        preferredAudioLabel: String?, preferredTextLabel: String?,
    )
    /** Выбор дорожки потока: "fmt:<formatId>" или "key:<voiceKey>". */
    fun selectInStreamAudio(selector: String)
    /** null — «авто». */
    fun setQuality(height: Int?)
    fun currentAudioFormatId(): String?
}

/** Пункт меню выбора. */
data class MenuItem(val id: String, val label: String, val hint: String? = null)
data class MenuGroup(
    val title: String,
    val items: List<MenuItem>,
    val selected: String?,
    val onSelect: (String) -> Unit,
)

data class PlaybackUiState(
    val episodeId: Long = 0,
    val ticket: Playback? = null,
    val startMs: Long = 0,
    val autoPlay: Boolean = false,
    /** Идёт первый выпуск или подмена потока: кольцо ожидания. */
    val preparing: Boolean = false,
    val error: String? = null,
    val notice: String? = null,
    val noticeBusy: Boolean = false,
    val voices: VoiceList? = null,
    val voiceFailed: Set<String> = emptySet(),
    /** Меню «Озвучка» и «Источник», собранные из тикета и общего списка. */
    val voiceGroup: MenuGroup? = null,
    val sourceGroup: MenuGroup? = null,
    val passedNote: String = "",
    /** Слитая разметка пропуска для этого потока, секунды. */
    val skips: List<Skip> = emptyList(),
    /** Подтверждение «Вы ещё смотрите?» — автоматических серий уже три. */
    val confirmNext: Boolean = false,
    /** Режим пропусков этого устройства: off | button | auto. */
    val skipMode: String = "button",
)

/**
 * Оркестрация воспроизведения — порт web/apps/web/src/pages/Player.tsx.
 *
 * Клиент не знает про Media3: он выпускает тикеты, держит путь назад при
 * подмене потока, поднимает упавший, докладывает прогресс и телеметрию.
 * Все обращения к плееру — через [PlayerHandle].
 */
class PlaybackClient(
    private val api: Api,
    private val store: TokenStore,
    /** Ждущий стейт: репорты встают в очередь до PUT и переживают сеть. */
    private val stateStore: StateStore,
    private val handle: PlayerHandle,
    private val deviceKind: String,
    private val appVersion: String,
    private val onPrefs: (PrefsPatch) -> Unit,
    /** Серия досмотрена: страница решает — автоследующая или остановка. */
    private val onEnded: () -> Unit,
    /** Автоследующая серия разрешена и есть куда идти. */
    private val onNextEpisode: () -> Unit,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)

    private val _state = MutableStateFlow(PlaybackUiState())
    val state: StateFlow<PlaybackUiState> = _state

    // Откуда играть: сначала запомненное для тайтла. Удачное переключение
    // в меню заменяет выбор на строгий.
    private var choice = PlaybackChoice()
    private var prefsRef: Prefs? = null

    private var attempt = 0
    private var switching: Job? = null
    private var startJob: Job? = null

    // Что играло до подмены — путь назад, пока у нового потока нет первого кадра.
    private data class Before(
        val pb: Playback, val positionMs: Long, val choice: PlaybackChoice,
        val remember: PrefsPatch?, val failed: String, val voice: String?,
    )
    private var before: Before? = null
    private var remember: PrefsPatch? = null
    // Студия, выбранная только что: по ней новый поток выберет дорожку.
    private var wantVoice: String? = null
    private var resumePlaying = false
    private var swapping = false

    // Площадки, чей поток тормозил, — до какого времени их обходить.
    private val slowUntil = mutableMapOf<String, Long>()
    private var recovered = mutableListOf<Long>()
    private var quietUntil = 0L
    private var refreshedLink = false

    // Скипы, пришедшие отдельно от тикета.
    private var moreSkips: List<SkipSegment> = emptyList()

    // Прогресс: последний репорт (для финального flush) и «досмотрено до
    // титров» — уход к следующей серии должен сообщить конец серии.
    private var lastReport: Triple<Long, Long, Long>? = null
    private var watchedTo = 0L

    // Сколько серий подряд autoplay сменил без человека.
    private var chain = 0

    private var noticeTimer: Job? = null

    // ---- телеметрия ----
    class SessionStats {
        var ttffMs: Long = 0
        var rebufferCount = 0
        var rebufferMsTotal = 0L
        var bitrateSwitches = 0
        var maxHeight = 0
        var endReason = ""
        var errorCode = ""
        private var bufferingStartedAt = 0L
        private var firstFrameAt = 0L
        private var sessionStartedAt = System.currentTimeMillis()

        fun onBuffering(now: Long) { bufferingStartedAt = now }
        fun onReady(now: Long) {
            if (bufferingStartedAt > 0) {
                rebufferCount++
                rebufferMsTotal += now - bufferingStartedAt
                bufferingStartedAt = 0
            }
        }
        fun onFirstFrame(now: Long) {
            if (firstFrameAt == 0L) { firstFrameAt = now; ttffMs = now - sessionStartedAt }
        }
        fun onVideoHeight(h: Int) { if (h > maxHeight) maxHeight = h }
        fun onBitrateSwitch() { bitrateSwitches++ }
        fun finish(reason: String, error: String) { endReason = reason; errorCode = error }
    }
    private val stats = SessionStats()
    /** Сколько раз подряд после обрыва сервер выдал ту же площадку. */
    private var sameSourceFails = 0

    // ---- запуск ----

    fun start(episodeId: Long, prefs: Prefs?, autoPlay: Boolean) {
        startJob?.cancel()
        switching?.cancel()
        _state.value = PlaybackUiState(episodeId = episodeId, preparing = true, autoPlay = autoPlay)
        prefsRef = prefs
        choice = PlaybackChoice(
            source = prefs?.source?.takeIf { it.isNotEmpty() },
            variantLabel = prefs?.audio?.takeIf { it.isNotEmpty() },
        )
        wantVoice = null
        before = null
        remember = null
        moreSkips = emptyList()
        watchedTo = 0
        lastReport = null
        attempt = 0
        sameSourceFails = 0

        startJob = scope.launch {
            try {
                // Позицию берём ДО выпуска тикета: если резолв сорвётся, мы
                // всё равно знаем, докуда человек досмотрел.
                val pos = try { restorePosition(episodeId) } catch (_: Exception) {
                    ProgressState()
                }
                val settings = store.settings()
                val ticket = api.playback(
                    episodeId, avoidSlow(choice),
                    Quality.maxQuality(settings.maxQuality, deviceKind == "tv"),
                    Quality.claimedCodecs(settings.hevcClaim, settings.av1Claim),
                )
                // Просмотренную серию открывают, чтобы пересмотреть: позиция
                // у неё стоит в конце — продолжение «с места» дало бы секунду
                // видео и сразу «дальше — следующая».
                val start = if (pos.status == "completed") 0L else pos.positionMs
                _state.value = _state.value.copy(
                    ticket = ticket, startMs = start, preparing = false, error = null,
                    skipMode = settings.skipMode,
                )
                handle.applyTicket(ticket, start, autoPlay, preferredAudio(), preferredText(ticket))
                refreshNotes()
                fetchVoices()
                fetchSkips(ticket)
            } catch (e: Exception) {
                _state.value = _state.value.copy(preparing = false, error = describeError(e))
            }
        }
    }

    /**
     * Позиция для старта: ждущее этого устройства сильнее сервера — его
     * сервер мог ещё не увидеть (PUT сорвался, сеть упала). Галочка
     * «смотрел» в очереди означает пересмотр: старт с нуля, как у
     * серверного «completed».
     */
    private suspend fun restorePosition(episodeId: Long): ProgressState {
        val p = withContext(Dispatchers.IO) { stateStore.pendingEpisode(episodeId) }
        if (p != null && (p.positionMs > 0 || p.watched != null)) {
            return ProgressState(
                positionMs = p.positionMs,
                durationMs = p.durationMs,
                status = when {
                    p.watched == true -> "completed"
                    p.watched == false -> ""
                    p.durationMs > 0 && p.positionMs >= p.durationMs * 9 / 10 -> "completed"
                    else -> ""
                },
            )
        }
        return api.progress0(episodeId)
    }

    /** «Повторить» после честной ошибки первого выпуска. */
    /** «Повторить» после ошибки: с чистого листа — без счётчика попыток и «тормозящих» площадок. */
    fun retry() {
        recovered.clear()
        slowUntil.clear()
        attempt++
        start(_state.value.episodeId, prefsRef, true)
    }

    fun stop() {
        flushProgress()
        sendTelemetry(reason = "stop", error = "")
        switching?.cancel()
        startJob?.cancel()
        scope.cancel()
    }

    // ---- переключение озвучки / источника (меню) ----

    private fun switchTo(next: PlaybackChoice, what: String, stays: String, prefsAfter: PrefsPatch?, voice: String?) {
        switching?.cancel()
        // Площадку нажали руками — значит, готовы ждать и тормозящую.
        if (next.strict) next.source?.let { slowUntil.remove(it) }
        showNotice("$what — переключаю…", busy = true)
        var job: Job? = null
        job = scope.launch {
            try {
                val settings = store.settings()
                val ticket = api.playback(
                    _state.value.episodeId, avoidSlow(next),
                    Quality.maxQuality(settings.maxQuality, deviceKind == "tv"),
                    Quality.claimedCodecs(settings.hevcClaim, settings.av1Claim),
                )
                // Прежний поток — путь назад: не запустится новый, вернёмся
                // на играющий с пометкой «не вышло», а не останемся ни с чем.
                swapTo(
                    ticket, next,
                    failed = switchFailed(what, Exception("поток не запустился"), stays),
                    prefsAfter = prefsAfter, said = null, voice = voice,
                )
            } catch (e: Exception) {
                wantVoice = null
                markFailed(voice)
                showNotice(switchFailed(what, e, stays), busy = false)
            } finally {
                if (switching === job) switching = null
            }
        }
        switching = job
    }

    /**
     * Подмена потока на выданный: тот же момент серии. Прежний остаётся
     * путём назад, пока у нового нет первого кадра.
     */
    private fun swapTo(ticket: Playback, next: PlaybackChoice, failed: String?, prefsAfter: PrefsPatch?, said: String?, voice: String?) {
        android.util.Log.w(TAG, "swapTo ${ticket.source?.id} failed=${failed != null}")
        // Место берётся в момент подмены, а не нажатия: пока площадка
        // отвечала, серия шла дальше.
        val pos = handle.positionMs()
        val positionMs = if (pos > 0) pos else _state.value.startMs
        before = if (_state.value.ticket != null && failed != null) Before(
            _state.value.ticket!!, positionMs, choice, remember, failed, voice,
        ) else null
        remember = prefsAfter
        resumePlaying = handle.isPlaying()
        swapping = true
        _state.value = _state.value.copy(
            ticket = ticket, startMs = positionMs, preparing = true,
            notice = null, noticeBusy = false,
        )
        // Итог — через showNotice: он гаснет сам, а не висит до конца серии.
        said?.let { showNotice(it, busy = false) }
        choice = next
        handle.applyTicket(ticket, positionMs, resumePlaying, preferredAudio(), preferredText(ticket))
        refreshNotes()
        fetchSkips(ticket)
    }

    /** Новый поток выдал первый кадр — выбор запоминается, путь назад не нужен. */
    fun onFirstFrame() {
        stats.onFirstFrame(System.currentTimeMillis())
        sameSourceFails = 0
        if (!swapping) return
        swapping = false
        before = null
        refreshedLink = false
        wantVoice = null
        remember?.let { onPrefs(it) }
        remember = null
        _state.value = _state.value.copy(preparing = false)
    }

    // ---- восстановление потока ----

    /** Фатальная ошибка плеера: путь назад или подъём без человека. */
    fun onFatalError(httpStatus: Int? = null, message: String = "") {
        android.util.Log.w(TAG, "fatal http=$httpStatus msg=$message src=${_state.value.ticket?.source?.id} before=${before != null} switching=${switching != null}")
        stats.errorCode = message.ifEmpty { "player_error" }
        val b = before
        before = null
        remember = b?.remember
        wantVoice = null
        if (b == null) {
            // Поток оборвался сам. 404 и 410 — ссылка протухла (оставили на
            // сутки), её выдают заново с той же площадки; иначе площадка не
            // отдаёт — другая с той же озвучкой.
            swapping = false
            val gone = (httpStatus == 404 || httpStatus == 410) && !refreshedLink
            recover(if (gone) "expired" else "broken")
            return
        }
        markFailed(b.voice)
        choice = b.choice
        _state.value = _state.value.copy(
            startMs = b.positionMs, ticket = b.pb, preparing = false,
            notice = null, noticeBusy = false,
        )
        showNotice(b.failed, busy = false)
        refreshedLink = false
        handle.applyTicket(b.pb, b.positionMs, true, preferredAudio(), preferredText(b.pb))
        refreshNotes()
    }

    /** Источник не успевает за просмотром — серия встаёт снова и снова. */
    fun onStall() = recover("slow")

    /**
     * Поток сам не поднимется: ссылка протухла, источник тормозит или
     * оборвался. Протухшая ссылка выдаётся заново с той же площадки, а
     * тормоз или обрыв — повод взять другую площадку с той же озвучкой.
     */
    private fun recover(why: String) {
        android.util.Log.w(TAG, "recover $why src=${_state.value.ticket?.source?.id} switching=${switching != null} before=${before != null} recovered=${recovered.size}")
        val pb = _state.value.ticket ?: return
        val cur = pb.source ?: return
        if (switching != null || before != null) return
        val now = System.currentTimeMillis()
        if (why == "slow" && now < quietUntil) return
        recovered.removeAll { now - it > RECOVER_WINDOW_MS }
        if (recovered.size >= RECOVER_MAX) {
            // Тормоз — не ошибка: серия идёт, пусть и с остановками.
            if (why != "slow") _state.value = _state.value.copy(
                error = "Поток обрывается: площадки не отдают серию",
            )
            return
        }
        recovered.add(now)
        val curVoice = currentVoice()
        val label = curVoice?.second ?: choice.variantLabel
        val next: PlaybackChoice = if (why == "expired") {
            PlaybackChoice(source = cur.id, strict = true, variant = pb.variant, variantLabel = label)
        } else {
            slowUntil[cur.id] = now + SLOW_FOR_MS
            // Студия из общего списка — тогда сервер возьмёт ровно её; иначе
            // подпись, по которой он поищет её сам.
            val key = curVoice?.first?.takeIf { k -> _state.value.voices?.voices?.any { it.key == k } == true }
            PlaybackChoice(source = choice.source, variantLabel = label, voice = key)
        }
        val trouble = "${cur.name} ${if (why == "slow") "тормозит" else "не отдаёт поток"}"
        if (why != "expired") showNotice("$trouble — ищу другую площадку…", busy = true)

        var job: Job? = null
        job = scope.launch {
            try {
                val settings = store.settings()
                val ticket = api.playback(
                    _state.value.episodeId, avoidSlow(next),
                    Quality.maxQuality(settings.maxQuality, deviceKind == "tv"),
                    Quality.claimedCodecs(settings.hevcClaim, settings.av1Claim),
                )
                val to = ticket.source
                if (why == "broken" && to?.id == cur.id) {
                    // Другой площадки с этой серией нет: та же ещё раз — свежей
                    // ссылкой, а оборвалась и она — говорим как есть, а не
                    // крутимся молча на чёрном экране.
                    sameSourceFails++
                    if (sameSourceFails >= 2) {
                        android.util.Log.w(TAG, "no other source for ${cur.id}")
                        _state.value = _state.value.copy(
                            preparing = false, notice = null,
                            error = "${cur.name} не отдаёт поток, а других площадок с этой серией сейчас нет",
                        )
                        return@launch
                    }
                }
                if (why == "slow" && to?.id == cur.id) {
                    // Той же озвучки больше нигде нет — остаёмся: медленный
                    // поток лучше чужой студии.
                    slowUntil.remove(cur.id)
                    quietUntil = System.currentTimeMillis() + QUIET_MS
                    showNotice(
                        "$trouble, а ${curVoice?.second?.let { "«$it»" } ?: "этой озвучки"} больше нигде нет",
                        busy = false,
                    )
                    return@launch
                }
                val moved = why != "expired" && to != null && to.id != cur.id
                // Прежняя ссылка протухла — возвращаться к ней некуда; не
                // пошла и свежая — значит, дело не в сроке, и это уже обрыв.
                refreshedLink = why == "expired"
                swapTo(
                    ticket, next,
                    // Путь назад — только от живого, но медленного потока: к
                    // оборвавшемуся возвращаться незачем, это и давало круг
                    // «свежая ссылка → откат на мёртвую».
                    failed = if (why != "slow") null else switchFailed(to?.name ?: "Площадка", Exception("поток не запустился"), "играет ${cur.name}"),
                    prefsAfter = if (moved && curVoice != null) PrefsPatch(audio = curVoice.second) else null,
                    said = if (moved) "$trouble — играет ${to?.name}" else null,
                    voice = null,
                )
            } catch (e: Exception) {
                if (why == "slow") {
                    // Других площадок нет или не ответили — серия идёт как шла.
                    quietUntil = System.currentTimeMillis() + QUIET_MS
                    showNotice("$trouble: другие площадки не ответили", busy = false)
                    return@launch
                }
                android.util.Log.w(TAG, "recover failed: $e")
                _state.value = _state.value.copy(error = describeError(e))
            } finally {
                if (switching === job) switching = null
            }
        }
        switching = job
    }

    /** Ссылку оставили на паузе дольше срока — при «играть» обновляем сразу. */
    fun onPlayPressed() {
        val pb = _state.value.ticket ?: return
        val exp = parseIsoTime(pb.expiresAt)
        if (exp in 1..System.currentTimeMillis()) recover("expired")
    }

    // ---- меню выбора ----

    /** Что играет сейчас — ключ студии: вариант площадки или дорожка потока. */
    private fun currentVoice(): Pair<String, String>? {
        val pb = _state.value.ticket ?: return null
        val v = pb.variants.find { it.id == pb.variant }
        if (v != null) return Voice.key(v.title) to Voice.title(v.title)
        val label = handle.currentAudioLabel() ?: return null
        return Voice.key(label) to Voice.title(label)
    }

    private fun buildGroups() {
        learnVoiceKeys(listOfNotNull(handle.currentAudioLabel()))
        val pb = _state.value.ticket ?: return
        val cur = pb.source ?: return
        val stays = currentVoice()?.second?.let { "осталась $it" } ?: ""
        val list = _state.value.voices?.voices ?: emptyList()
        val failed = _state.value.voiceFailed
        val curVoice = currentVoice()

        // Озвучка одним списком: студия один раз, сколько бы площадок её ни
        // отдавали, — откуда взять, решает сервер. Человек выбирает студию,
        // а не площадку.
        val voiceGroup: MenuGroup? = if (list.isNotEmpty()) {
            val known = mutableSetOf<String>()
            val items = mutableListOf<MenuItem>()
            val names = pb.sources.associate { it.id to it.name }
            list.forEach { v ->
                known.add(v.key)
                // Студии с других площадок — с подписью, откуда: выбрать можно
                // любую, сервер возьмёт ту площадку, где она есть. Подпись
                // короткая — первая площадка и «+N»: полный список на ТВ не
                // влезал и вытеснял само название студии.
                val here = v.ways.any { it.source == cur.id }
                val where = if (here) null else v.ways.mapNotNull { names[it.source] ?: it.source.takeIf { s -> s.isNotEmpty() } }
                    .distinct().let { w -> if (w.size > 1) "${w[0]} +${w.size - 1}" else w.firstOrNull() }
                items.add(MenuItem(v.key, v.title, if (failed.contains(v.key)) "не вышло" else where))
            }
            val cv = pb.variants.find { it.id == pb.variant }
            if (cv != null && !known.contains(Voice.key(cv.title))) {
                known.add(Voice.key(cv.title))
                items.add(0, MenuItem("variant:${cv.id}", cv.title))
            }
            val audio = handle.currentAudioLabel()
            if (cv == null && audio != null) {
                val k = Voice.key(audio)
                if (!known.contains(k)) {
                    known.add(k)
                    items.add(MenuItem("track:${handle.currentAudioFormatId()}", Voice.title(audio)))
                }
            }
            val selected = when {
                curVoice == null -> null
                list.any { it.key == curVoice.first } -> curVoice.first
                cv != null -> "variant:${cv.id}"
                else -> handle.currentAudioFormatId()?.let { "track:$it" }
            }
            MenuGroup("Озвучка", items, selected) { id ->
                if (id.startsWith("track:")) {
                    handle.selectInStreamAudio("fmt:${id.removePrefix("track:")}")
                    return@MenuGroup
                }
                val v = list.find { it.key == id } ?: return@MenuGroup
                // Дорожка того же потока (kinopub) — переключает сам плеер, сразу.
                val sameStream = v.ways.any { it.source == cur.id && it.variant == null }
                if (sameStream) {
                    handle.selectInStreamAudio("key:${v.key}")
                    onPrefs(PrefsPatch(audio = v.title))
                    return@MenuGroup
                }
                wantVoice = v.title
                // Есть на текущей площадке — остаёмся на ней; нет — идём туда,
                // где есть: иначе студии с других площадок было бы не выбрать.
                val way = v.ways.firstOrNull { it.source == cur.id } ?: v.ways.firstOrNull()
                switchTo(
                    PlaybackChoice(
                        voice = v.key,
                        source = way?.source?.takeIf { it.isNotEmpty() } ?: cur.id,
                        variant = way?.variant,
                        variantLabel = v.title,
                    ),
                    v.title, stays, PrefsPatch(audio = v.title), v.key,
                )
            }
        } else if (pb.variants.isNotEmpty()) {
            // Общего списка ещё нет (или сервер его не дал) — озвучки той
            // площадки, что играет, как раньше.
            val variants = pb.variants
            val dup = variants.groupBy { it.title }.filterValues { it.size > 1 }.keys
            MenuGroup("Озвучка", variants.map { v ->
                MenuItem(v.id, if (v.title in dup && v.player != null) "${v.title} · ${v.player}" else v.title)
            }, pb.variant) { id ->
                val v = variants.find { it.id == id }
                switchTo(
                    PlaybackChoice(source = cur.id, strict = true, variant = id),
                    v?.title ?: "Озвучка", stays,
                    PrefsPatch(source = cur.id, audio = v?.title), null,
                )
            }
        } else null

        // Источник — от него зависят картинка и качество, а не голос.
        val sources = pb.sources
        val sourceGroup: MenuGroup? = if (sources.size > 1) {
            val auto = prefsRef?.source == null
            MenuGroup(
                "Источник",
                listOf(MenuItem("", "Автоматически", if (auto) cur.name else null)) +
                    sources.map { x ->
                        MenuItem(
                            x.id, x.name,
                            if (!auto && x.id == cur.id && prefsRef?.source != cur.id) "сейчас" else null,
                        )
                    },
                if (auto) "" else prefsRef?.source ?: "",
            ) { id ->
                // «Автоматически» и уже играющая площадка — только память.
                if (id.isEmpty() || id == cur.id) {
                    onPrefs(PrefsPatch(source = id.ifEmpty { null }))
                    prefsRef = prefsRef?.copy(source = id.ifEmpty { null }) ?: Prefs(source = id.ifEmpty { null })
                    buildGroups()
                    return@MenuGroup
                }
                switchTo(
                    PlaybackChoice(source = id, strict = true, variantLabel = curVoice?.second ?: prefsRef?.audio),
                    sources.find { it.id == id }?.name ?: id, "играет ${cur.name}", PrefsPatch(source = id), null,
                )
            }
        } else null

        _state.value = _state.value.copy(voiceGroup = voiceGroup, sourceGroup = sourceGroup)
    }

    /** Дорожки потока сменились (в т.ч. наш выбор) — пересобрать меню. */
    fun onAudioTracksChanged(labels: List<String> = emptyList()) {
        learnVoiceKeys(labels)
        buildGroups()
    }

    // Подписи, ключи которых уже спрошены у сервера (или спрашиваются).
    private val keysAsked = mutableSetOf<String>()

    /**
     * Ключи дорожек потока и вариантов площадки считает сервер: у него
     * псевдонимы студий из админки, а новое правило склейки не требует
     * выпуска приложения. До ответа (и без сети) — свой разбор в [Voice];
     * ответ пересобирает меню.
     */
    private fun learnVoiceKeys(labels: List<String>) {
        val pb = _state.value.ticket
        val all = labels + pb?.variants.orEmpty().map { it.title } + listOfNotNull(prefsRef?.audio)
        val ask = Voice.unknown(all).filter { keysAsked.add(it) }.take(64)
        if (ask.isEmpty()) return
        scope.launch {
            runCatching { api.voiceKeys(ask) }
                .onSuccess { r ->
                    Voice.learn(r.keys.mapValues { it.value.key to it.value.title })
                    buildGroups()
                }
                .onFailure { keysAsked.removeAll(ask.toSet()) }
        }
    }

    fun markAudioTrackSelected(label: String) {
        // Выбор дорожки потока запоминается как студия — по общему правилу.
        onPrefs(PrefsPatch(audio = Voice.title(label)))
    }

    /** Выбор субтитров запоминается; "" — «выключены». */
    fun onSubtitlePref(label: String) {
        onPrefs(PrefsPatch(subtitle = label))
        prefsRef = prefsRef?.copy(subtitle = label) ?: Prefs(subtitle = label)
    }

    /** Студия, по которой выбрать дорожку потока при старте. */
    private fun preferredAudio(): String? = wantVoice ?: prefsRef?.audio

    /**
     * Субтитры при старте: вариант «Субтитры» (оригинал с подписями), выбранный
     * только что руками, сильнее выключенных когда-то субтитров.
     */
    private fun preferredText(ticket: Playback): String? {
        val s = prefsRef?.subtitle ?: return null
        return if (s.isEmpty() && ticket.subtitles.any { it.default }) null else s
    }

    // ---- вспомогательное ----

    private fun showNotice(text: String, busy: Boolean) {
        noticeTimer?.cancel()
        _state.value = _state.value.copy(notice = text, noticeBusy = busy)
        if (!busy) {
            // Итог переключения висит над кадром несколько секунд и гаснет сам.
            noticeTimer = scope.launch {
                kotlinx.coroutines.delay(6000)
                _state.value = _state.value.copy(notice = null)
            }
        }
    }

    private fun markFailed(key: String?) {
        if (key.isNullOrEmpty()) return
        _state.value = _state.value.copy(voiceFailed = _state.value.voiceFailed + key)
        buildGroups()
    }

    private fun refreshNotes() {
        val pb = _state.value.ticket ?: return
        val src = pb.source ?: return
        // Почему играет не та площадка: отказ площадки объясняется всегда,
        // «нет этой серии» — только у той, что человек выбрал сам.
        val why = (pb.passed).mapNotNull { p ->
            when (p.reason) {
                "down", "failed" -> "${p.name} не отвечает"
                "busy" -> "${p.name} просит подождать"
                "slow" -> "${p.name} тормозит"
                "reauth" -> "${p.name}: нужна переавторизация"
                "missing" -> if (p.id == choice.source) "у ${p.name} нет этой серии" else null
                "novoice" -> if (prefsRef?.audio != null) "у ${p.name} нет «${prefsRef?.audio}»" else "у ${p.name} нет этой озвучки"
                else -> null
            }
        }
        val note = if (why.isNotEmpty()) "${why.joinToString(", ")} — играет ${src.name}" else ""
        val changed = note != _state.value.passedNote
        _state.value = _state.value.copy(passedNote = note)
        // Почему играет не та площадка — один раз надписью поверх кадра
        // (дальше — в сводке по «i»): иначе отказ площадки, в том числе
        // протухший вход kinopub, никто не замечал.
        if (changed && note.isNotEmpty() && _state.value.notice == null) showNotice(note, busy = false)
        buildGroups()
    }

    private fun fetchVoices() {
        // Общий список озвучек просится, когда серия уже играет: поток не
        // ждёт, пока ответят все площадки.
        scope.launch {
            try {
                val l = api.voices(_state.value.episodeId)
                _state.value = _state.value.copy(voices = l)
                buildGroups()
            } catch (_: Exception) {
                // Не пришёл — меню остаётся прежним.
            }
        }
    }

    private fun fetchSkips(ticket: Playback) {
        scope.launch {
            try {
                val extra = api.skips(_state.value.episodeId, ticket.source?.id, ticket.variant)
                moreSkips = extra
                rebuildSkips()
            } catch (_: Exception) {
                // Без разметки серия смотрится как раньше.
            }
        }
        rebuildSkips()
    }

    /** Разметка: что пришло с потоком — сразу, остальное — вдогонку. */
    fun rebuildSkips(durationMs: Long = handle.durationMs()) {
        val duration = durationMs / 1000.0
        val merged = (_state.value.ticket?.segments ?: emptyList()) + moreSkips
        _state.value = _state.value.copy(skips = pickSkips(merged, duration))
    }

    /** Выбор с просьбой обойти тормозящие площадки. Строгий — как есть. */
    private fun avoidSlow(c: PlaybackChoice): PlaybackChoice {
        if (c.strict) return c
        val now = System.currentTimeMillis()
        val avoid = slowUntil.filterValues { it > now }.keys.toList()
        return if (avoid.isNotEmpty()) c.copy(avoid = avoid) else c
    }

    // ---- прогресс и телеметрия ----

    /** Раз в 15 с, пока плеер жив: доклад позиции + оценка канала. */
    fun tick(positionMs: Long, durationMs: Long, bandwidthBps: Long) {
        report(positionMs, durationMs)
        scope.launch { store.rememberBandwidth(_state.value.ticket?.source?.id, bandwidthBps / 1000) }
    }

    fun report(positionMs: Long, durationMs: Long) {
        var pos = positionMs
        if (watchedTo > pos) pos = watchedTo
        if (pos < 0 || durationMs <= 0) return
        lastReport = Triple(pos, durationMs, System.currentTimeMillis())
        pushProgress(pos, durationMs)
    }

    /** Финальный репорт: пауза, уход с экрана, конец серии. */
    fun flushProgress() {
        val r = lastReport ?: return
        pushProgress(r.first, r.second)
    }

    /**
     * Репорт уходит в два хода: сперва в очередь на устройстве, потом на
     * сервер. Сорвавшийся PUT больше не теряет позицию молча — слив дотащит
     * её при первой сети. ts один на очередь и на PUT: ack вычищает именно
     * своё, не трогая вставшее свежее, пока запрос летел.
     */
    private fun pushProgress(pos: Long, durationMs: Long) {
        val epId = _state.value.episodeId
        if (epId <= 0) return
        val ts = api.nowIso()
        scope.launch {
            withContext(Dispatchers.IO) { stateStore.queueProgress(epId, pos, durationMs, ts) }
            try {
                api.progress(epId, pos, durationMs, ts)
                withContext(Dispatchers.IO) { stateStore.ackEpisode(epId, ts) }
            } catch (_: Exception) {
                // Осталась в очереди — уедет при следующем синке.
            }
        }
    }

    /** С титров — к следующей серии: конца файла не было, но серия досмотрена. */
    fun creditsNext() {
        val d = handle.durationMs()
        if (d > 0) watchedTo = d
        stats.finish("credits_next", "")
        onEnded()
    }

    /** Конец серии: последний репорт сообщает конец, дальше — автоследующая. */
    fun onEndedInternal() {
        val d = handle.durationMs()
        if (d > 0) { watchedTo = d; report(d, d) }
        stats.finish("ended", "")
        chain++
        onEnded()
        // Автоследующая: после трёх подряд — спросить.
        if (chain >= MAX_CHAIN) {
            _state.value = _state.value.copy(confirmNext = true)
        } else {
            onNextEpisode()
        }
    }

    /** Человек подтвердил «ещё серию» после трёх автосмен. */
    fun confirmNextEpisode() {
        chain = 0
        _state.value = _state.value.copy(confirmNext = false)
        onNextEpisode()
    }

    fun cancelConfirmNext() {
        chain = 0
        _state.value = _state.value.copy(confirmNext = false)
    }

    /** Смена режима пропусков из плеера — сохраняется в настройках устройства. */
    fun setSkipMode(mode: String) {
        _state.value = _state.value.copy(skipMode = mode)
        scope.launch {
            try { store.saveSettings(store.settings().copy(skipMode = mode)) } catch (_: Exception) { }
        }
    }

    fun sendTelemetry(reason: String, error: String) {
        val pb = _state.value.ticket ?: return
        stats.finish(reason, error)
        val payload = mapOf(
            "impl" to kotlinx.serialization.json.JsonPrimitive("exoplayer"),
            "ttffMs" to kotlinx.serialization.json.JsonPrimitive(stats.ttffMs),
            "rebufferCount" to kotlinx.serialization.json.JsonPrimitive(stats.rebufferCount),
            "rebufferMsTotal" to kotlinx.serialization.json.JsonPrimitive(stats.rebufferMsTotal),
            "bitrateSwitches" to kotlinx.serialization.json.JsonPrimitive(stats.bitrateSwitches),
            "maxHeight" to kotlinx.serialization.json.JsonPrimitive(stats.maxHeight),
            "endReason" to kotlinx.serialization.json.JsonPrimitive(stats.endReason),
            "errorCode" to kotlinx.serialization.json.JsonPrimitive(stats.errorCode),
            "ticketId" to kotlinx.serialization.json.JsonPrimitive(pb.ticketId),
            "episodeId" to kotlinx.serialization.json.JsonPrimitive(_state.value.episodeId),
            "deviceKind" to kotlinx.serialization.json.JsonPrimitive(deviceKind),
            "appVersion" to kotlinx.serialization.json.JsonPrimitive(appVersion),
        )
        scope.launch {
            try { api.telemetry(payload) } catch (_: Exception) { }
        }
    }

    // ---- метрики из событий плеера ----
    fun onBufferingStart() = stats.onBuffering(System.currentTimeMillis())
    fun onBufferingEnd() = stats.onReady(System.currentTimeMillis())
    fun onVideoHeight(h: Int) = stats.onVideoHeight(h)
    fun onBitrateSwitch() = stats.onBitrateSwitch()

    companion object {
        // Сколько раз за окно поток можно поднимать без человека. Больше —
        // значит, не помогает, и честная ошибка лучше карусели площадок.
        const val TAG = "KinoPlayback"
        const val RECOVER_MAX = 3
        const val RECOVER_WINDOW_MS = 2 * 60_000L
        // Тормозит, а той же озвучки больше нигде нет — столько не дёргать.
        const val QUIET_MS = 5 * 60_000L
        const val SLOW_FOR_MS = 15 * 60_000L
        const val MAX_CHAIN = 3
        /** Сколько витрина ждёт выпуска тикета: серверный потолок 25 с + запас. */
        const val PLAYBACK_WAIT_MS = 40_000L
    }
}

/** Почему переключение не вышло — строкой поверх кадра. */
fun switchFailed(what: String, e: Exception, stays: String): String {
    val why = if (e is ApiError && e.status == 404) "нет этой серии" else describeError(e)
    return "$what: $why${if (stays.isNotEmpty()) " — $stays" else ""}"
}

fun describeError(e: Exception): String = when (e) {
    is ApiError -> when {
        e.offline -> "нет связи с сервером"
        e.status == 404 -> "нет этой серии"
        e.code == "timeout" -> "площадки не ответили"
        e.sessionDead -> "сессия кончилась — войдите заново"
        else -> e.message?.take(120) ?: "ошибка ${e.status}"
    }
    else -> e.message?.take(120) ?: "неизвестная ошибка"
}
