package ru.sorewa.kino.ui.player

import androidx.activity.compose.BackHandler
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.displayCutout
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.focus.focusProperties
import androidx.compose.foundation.focusable
import androidx.compose.foundation.focusGroup
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathNode
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.ui.PlayerView
import ru.sorewa.kino.data.Episode
import ru.sorewa.kino.player.MenuGroup
import ru.sorewa.kino.player.MenuItem
import ru.sorewa.kino.player.PlaybackClient
import ru.sorewa.kino.player.PlaybackUiState
import ru.sorewa.kino.player.Skip
import ru.sorewa.kino.player.SkipKind
import ru.sorewa.kino.player.activeSkip
import kotlin.math.roundToInt

// Цвета и размеры — из web/packages/player/src/ui/controls.css (раскладка
// kinopub, класс .cine): нативный плеер повторяет её один в один.
private val KpGreen = Color(0xFF35C56A)
private val KpGreenDark = Color(0xFF1E8A47)      // отмеченный пункт на белой плашке фокуса
private val KpPlate = Color(0xBD000000)        // .kp.cine .kp-top/.kp-bar — rgba(0,0,0,.74)
private val KpScrim = Color(0x38000000)        // .kp.cine .kp-scrim — rgba(0,0,0,.22)
private val KpMenuBg = Color(0xFA101218)       // .kp-menu — rgba(16,18,24,.98)
private val KpMenuBorder = Color(0x24FFFFFF)
private val KpSeg = Color(0xD9FFD666)          // заставка/титры на полосе
private const val SKIP_SEC = 15                // CINE_SKIP
private val SPEEDS = listOf(0.5f, 0.75f, 1f, 1.25f, 1.5f, 2f)

private enum class Pop { VIDEO, AUDIO, SUBS, SPEED }

/** Куда встать фокусу пульта при открытии списка — на отмеченный пункт. */
private val LocalMenuSel = androidx.compose.runtime.staticCompositionLocalOf<FocusRequester?> { null }

/**
 * Плеер — раскладка kinopub, как у веб-плеера во весь экран у пальца и на
 * ТВ: сверху «←», название заглавными и надписи ВИДЕО / АУДИО / СУБТИТРЫ
 * (каждая открывает узкий список под собой), снизу время по краям полосы
 * и ряд по центру: полный экран, предыдущая, −15, пауза, +15, следующая,
 * скорость. Жесты: тап — панель, двойной у края — ±15 с, тяга — перемотка.
 * Пульт: стрелки при спрятанной панели — ±15 с, OK — пауза, вверх/вниз —
 * панель.
 */
@Composable
fun PlayerScreen(
    state: PlayerViewState,
    client: PlaybackClient?,
    isTv: Boolean,
    onBack: () -> Unit,
    onPip: () -> Unit,
    onQuality: (Int?) -> Unit,
    onSpeed: (Float) -> Unit,
    onSelectAudio: (String) -> Unit,
    onSelectText: (String) -> Unit,
    onSeek: (Long) -> Unit,
    onTogglePlay: () -> Unit,
    onNext: () -> Unit,
    onPrev: () -> Unit,
    onCreditsNext: () -> Unit,
    @Suppress("UNUSED_PARAMETER") onJumpToEpisode: (Episode) -> Unit,
) {
    val context = androidx.compose.ui.platform.LocalContext.current
    val touch = !isTv && context.packageManager.hasSystemFeature(android.content.pm.PackageManager.FEATURE_TOUCHSCREEN)
    val emptyState = remember { kotlinx.coroutines.flow.MutableStateFlow(PlaybackUiState()) }
    val cs by (client?.state ?: emptyState).collectAsStateWithLifecycle()
    val density = LocalDensity.current

    val settings by ru.sorewa.kino.KinoApp.instance.container.store.settingsFlow
        .collectAsStateWithLifecycle(initialValue = null)
    var controlsVisible by remember { mutableStateOf(true) }
    // Сводка о потоке (кнопка «i»), как у Kino.Watch; «всегда» — из настроек.
    var statsOn by remember { mutableStateOf(false) }
    val showStats = statsOn || settings?.statsAlways == true
    // Жест яркости/громкости: что показать поверх кадра (тип, доля 0..1).
    var gesture by remember { mutableStateOf<Pair<Boolean, Float>?>(null) }
    var pop by remember { mutableStateOf<Pop?>(null) }
    // Где стоят надписи и кнопка скорости — списки прижимаются к ним.
    val anchors = remember { mutableMapOf<Pop, androidx.compose.ui.geometry.Rect>() }
    var flash by remember { mutableStateOf<Pair<Boolean, Long>?>(null) } // (вперёд?, метка)
    var toast by remember { mutableStateOf<Skip?>(null) }
    val skippedAuto = remember { mutableStateOf(setOf<Double>()) }
    val playFocus = remember { FocusRequester() }
    // ТВ: пока панель спрятана, фокус держит сам плеер — иначе нажатия пульта
    // некому принять (кнопки панели уходят из дерева вместе с фокусом).
    val rootFocus = remember { FocusRequester() }
    val skipFocus = remember { FocusRequester() }
    val overlayFocus = remember { FocusRequester() }
    var skipFocused by remember { mutableStateOf(false) }
    // ТВ: списки (видео, аудио, субтитры, скорость). Открылся — фокус на
    // выбранный пункт (menuSel), нет такого — на первый (menuGroup); закрылся —
    // обратно на кнопку, которая его открыла (popFrom).
    val pillFocus = remember { Pop.entries.associateWith { FocusRequester() } }
    val menuSel = remember { FocusRequester() }
    val menuGroup = remember { FocusRequester() }
    var popFrom by remember { mutableStateOf<Pop?>(null) }
    // Метка последнего действия человека — перезапускает отсчёт скрытия панели.
    var lastInput by remember { mutableLongStateOf(0L) }
    var barScrubMs by remember { mutableLongStateOf(-1) }

    fun showControls() { controlsVisible = true }
    fun hideControls() { controlsVisible = false; pop = null }
    fun seekBy(sec: Int) {
        val target = state.positionMs + sec * 1000L
        val end = if (state.durationMs > 0) state.durationMs else Long.MAX_VALUE
        onSeek(target.coerceIn(0, end))
    }

    // ТВ: перемотка с пульта копится и применяется одним прыжком — когда
    // стрелку отпустили (пауза ~0,7 с) или нажали OK. Удержание разгоняет
    // шаг: 15 с, потом 30 с, минута, две — середина двухчасового фильма за
    // пару секунд, а не 240 нажатий, и поток не дёргается на каждом шаге.
    // Пока копится, видно, куда перематываешь.
    var keyScrubMs by remember { mutableLongStateOf(-1L) }
    var scrubTick by remember { mutableIntStateOf(0) }
    // Прыжок сделан, а позиция плеера ещё не доехала — полоса не скачет назад.
    var settleMs by remember { mutableLongStateOf(-1L) }
    fun keyScrub(fwd: Boolean, repeat: Int) {
        val dur = state.durationMs
        val step = when {
            repeat < 8 -> SKIP_SEC * 1000L
            repeat < 20 -> maxOf(30_000L, dur / 200)
            repeat < 40 -> maxOf(60_000L, dur / 100)
            else -> maxOf(120_000L, dur / 50)
        }
        val base = when {
            keyScrubMs >= 0 -> keyScrubMs
            settleMs >= 0 -> settleMs
            else -> state.positionMs
        }
        val end = if (dur > 0) (dur - 1_000).coerceAtLeast(0) else Long.MAX_VALUE
        keyScrubMs = (base + if (fwd) step else -step).coerceIn(0, end)
        scrubTick++
    }
    fun commitScrub() {
        val t = keyScrubMs
        if (t < 0) return
        keyScrubMs = -1
        settleMs = t
        onSeek(t)
    }
    LaunchedEffect(scrubTick) {
        if (keyScrubMs >= 0) { kotlinx.coroutines.delay(700); commitScrub() }
    }
    LaunchedEffect(settleMs) {
        if (settleMs >= 0) { kotlinx.coroutines.delay(2_000); settleMs = -1 }
    }

    // «Назад»: сначала закрывает список, потом прячет панель, потом выходит.
    BackHandler(enabled = pop != null) { pop = null }
    BackHandler(enabled = pop == null && controlsVisible && state.isPlaying) { hideControls() }
    // «Назад» во время перемотки с пульта — отменить её, а не выйти из плеера.
    BackHandler(enabled = keyScrubMs >= 0) { keyScrubMs = -1 }

    // Панель гаснет через N секунд игры (настройка, 0 — никогда); на паузе
    // и с открытым списком — нет.
    // Отсчёт идёт от последнего нажатия пульта/касания, а не от показа
    // панели: пока человек листает кнопки, панель не гаснет у него из-под рук.
    val hideAfter = settings?.hideAfterSec ?: 3
    LaunchedEffect(controlsVisible, state.isPlaying, pop, hideAfter, lastInput, barScrubMs >= 0, keyScrubMs >= 0) {
        if (controlsVisible && state.isPlaying && pop == null && hideAfter > 0 && barScrubMs < 0 && keyScrubMs < 0) {
            kotlinx.coroutines.delay(hideAfter * 1000L)
            hideControls()
        }
    }
    LaunchedEffect(gesture) { if (gesture != null) { kotlinx.coroutines.delay(900); gesture = null } }
    LaunchedEffect(flash) { if (flash != null) { kotlinx.coroutines.delay(700); flash = null } }
    LaunchedEffect(toast) { if (toast != null) { kotlinx.coroutines.delay(5000); toast = null } }

    // Автопропуск: только заставка и пересказ; титры и анонс — кнопкой.
    val active = activeSkip(cs.skips, state.positionMs / 1000.0)
    LaunchedEffect(active?.start, active?.end, cs.skipMode) {
        val a = active ?: return@LaunchedEffect
        if (cs.skipMode == "auto" && (a.kind == SkipKind.INTRO || a.kind == SkipKind.RECAP)) {
            if (!skippedAuto.value.contains(a.start)) {
                skippedAuto.value = skippedAuto.value + a.start
                onSeek((a.end * 1000).toLong())
                toast = a
            }
        }
    }

    // Соседние серии — для «предыдущая/следующая» и экрана конца серии.
    val curId = if (cs.episodeId > 0) cs.episodeId else state.currentEpisodeId
    val idx = state.episodes.indexOfFirst { it.id == curId }
    val nextEp = if (idx >= 0) state.episodes.getOrNull(idx + 1) else null
    val curEp = state.episodes.getOrNull(idx)
    val nextLabel = nextEp?.let { n ->
        if (curEp != null && n.seasonNo != curEp.seasonNo) "Сезон ${n.seasonNo}, эпизод ${n.episodeNo}"
        else "Эпизод ${n.episodeNo}"
    }

    // Тяга по кадру (телефон).
    var dragMs by remember { mutableLongStateOf(-1) }
    val dragSpanMs = remember(state.durationMs) {
        (state.durationMs / 4).coerceIn(2 * 60_000L, 15 * 60_000L).coerceAtMost(state.durationMs.coerceAtLeast(1))
    }
    val shownMs = when {
        dragMs >= 0 -> dragMs
        barScrubMs >= 0 -> barScrubMs
        keyScrubMs >= 0 -> keyScrubMs
        settleMs >= 0 && kotlin.math.abs(state.positionMs - settleMs) > 2_000 -> settleMs
        else -> state.positionMs
    }

    val fatalError = state.fatalError
    val ended = state.ended && !cs.preparing
    // Для человека «играет» и то, что грузится и сейчас пойдёт: значок —
    // пауза, OK — пауза.
    val playIntent = state.isPlaying || state.buffering ||
        (state.starting && !state.holdPaused)
    val error = fatalError ?: cs.error
    val endShown = (ended || cs.confirmNext) && error == null
    // Экран конца серии или ошибки: пульт ходит по его кнопкам, плеер не мешает.
    val overlay = endShown || error != null
    val skipShown = cs.skipMode != "off" && active != null && !cs.preparing && !ended

    // Куда на ТВ встать фокусу: экран поверх → его первая кнопка; панель →
    // пауза (центр ряда); «Пропустить заставку» → она (OK пропускает); иначе
    // сам плеер — стрелки перематывают, OK — пауза, вверх/вниз — панель.
    // Открытый список (озвучка, качество) — фокус в него, на выбранный пункт;
    // закрылся — на кнопку, которая его открыла, а не на паузу.
    LaunchedEffect(isTv, controlsVisible, overlay, skipShown, state.pip, pop) {
        if (!isTv || state.pip) return@LaunchedEffect
        androidx.compose.runtime.withFrameNanos { }
        if (!controlsVisible) popFrom = null
        try {
            when {
                overlay -> overlayFocus.requestFocus()
                controlsVisible && pop != null -> {
                    popFrom = pop
                    if (!menuSel.requestFocus(FocusDirection.Enter)) menuGroup.requestFocus(FocusDirection.Enter)
                }
                controlsVisible -> {
                    val back = popFrom?.let { pillFocus.getValue(it) }
                    popFrom = null
                    if (back == null || !back.requestFocus(FocusDirection.Enter)) playFocus.requestFocus()
                }
                skipShown -> skipFocus.requestFocus()
                else -> rootFocus.requestFocus()
            }
        } catch (_: Exception) { }
    }

    BoxWithConstraints(
        Modifier
            .fillMaxSize()
            .background(Color.Black)
            .onPreviewKeyEvent { event ->
                if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                lastInput = System.nanoTime()
                when (event.key) {
                    Key.MediaPlayPause -> { onTogglePlay(); return@onPreviewKeyEvent true }
                    Key.MediaPlay -> { if (!playIntent) onTogglePlay(); return@onPreviewKeyEvent true }
                    Key.MediaPause -> { if (playIntent) onTogglePlay(); return@onPreviewKeyEvent true }
                }
                if (pop != null || overlay) return@onPreviewKeyEvent false
                val repeat = event.nativeKeyEvent.repeatCount
                when {
                    event.key == Key.MediaFastForward -> { keyScrub(true, repeat); true }
                    event.key == Key.MediaRewind -> { keyScrub(false, repeat); true }
                    event.key == Key.DirectionLeft && !controlsVisible -> { keyScrub(false, repeat); true }
                    event.key == Key.DirectionRight && !controlsVisible -> { keyScrub(true, repeat); true }
                    // OK, пока копится перемотка, — прыгнуть сразу, а не пауза.
                    (event.key == Key.DirectionCenter || event.key == Key.Enter) && keyScrubMs >= 0 -> { commitScrub(); true }
                    event.key == Key.DirectionUp || event.key == Key.DirectionDown -> {
                        if (!controlsVisible) { showControls(); true } else false
                    }
                    // OK на «Пропустить заставку» — её нажатие, а не пауза.
                    (event.key == Key.DirectionCenter || event.key == Key.Enter || event.key == Key.Spacebar) && !controlsVisible && !(skipFocused && skipShown) -> {
                        onTogglePlay(); showControls(); true
                    }
                    else -> false
                }
            }
            .then(if (isTv) Modifier.focusRequester(rootFocus).focusable() else Modifier)
            // Касание где угодно (кнопки, списки, полоса) тоже продлевает панель;
            // проход Initial только подглядывает, событие идёт дальше как было.
            .pointerInput(Unit) {
                awaitPointerEventScope {
                    while (true) {
                        awaitPointerEvent(androidx.compose.ui.input.pointer.PointerEventPass.Initial)
                        lastInput = System.nanoTime()
                    }
                }
            },
    ) {
        val boxW = constraints.maxWidth.toFloat()

        AndroidView(
            factory = { ctx ->
                PlayerView(ctx).apply {
                    useController = false
                    resizeMode = androidx.media3.ui.AspectRatioFrameLayout.RESIZE_MODE_FIT
                    setShutterBackgroundColor(android.graphics.Color.BLACK)
                }
            },
            update = { it.player = state.player },
            modifier = Modifier
                .fillMaxSize()
                .then(
                    if (touch) Modifier.pointerInput(state.positionMs > 0) {
                        detectTapGestures(
                            onTap = { if (pop != null) pop = null else if (controlsVisible) hideControls() else showControls() },
                            onDoubleTap = { o ->
                                val w = size.width.toFloat()
                                when {
                                    o.x < w * 0.36f -> { seekBy(-SKIP_SEC); flash = false to System.nanoTime() }
                                    o.x > w * 0.64f -> { seekBy(SKIP_SEC); flash = true to System.nanoTime() }
                                    else -> onTogglePlay()
                                }
                            },
                        )
                    } else Modifier,
                )
                .then(
                    if (touch) Modifier.pointerInput(state.durationMs) {
                        var base = 0L
                        detectHorizontalDragGestures(
                            onDragStart = { base = state.positionMs; dragMs = base },
                            onDragEnd = { if (dragMs >= 0) onSeek(dragMs); dragMs = -1 },
                            onDragCancel = { dragMs = -1 },
                        ) { change, amount ->
                            change.consume()
                            val frac = amount / size.width.toFloat().coerceAtLeast(1f)
                            dragMs = (dragMs.coerceAtLeast(base) + (frac * dragSpanMs)).toLong()
                                .coerceIn(0, state.durationMs.coerceAtLeast(0))
                        }
                    } else Modifier,
                )
                .then(
                    // Свайп вверх-вниз у левого края — яркость, у правого — громкость.
                    if (touch && settings?.swipes != false) Modifier.pointerInput(Unit) {
                        var side = 0 // -1 яркость, 1 громкость, 0 середина — не трогаем
                        detectVerticalDragGestures(
                            onDragStart = { o ->
                                side = when {
                                    o.x < size.width * 0.4f -> -1
                                    o.x > size.width * 0.6f -> 1
                                    else -> 0
                                }
                            },
                        ) { change, amount ->
                            if (side == 0) return@detectVerticalDragGestures
                            change.consume()
                            val delta = -amount / (size.height * 0.8f)
                            gesture = if (side < 0) false to swipeBrightness(context, delta)
                            else true to swipeVolume(context, delta)
                        }
                    } else Modifier,
                ),
        )

        val panel = !state.pip && controlsVisible

        // Затемнение кадра, пока видна панель.
        if (panel) Box(Modifier.fillMaxSize().background(KpScrim))

        // Тяга по кадру: куда перематывает.
        if (dragMs >= 0 && state.durationMs > 0) {
            Row(
                Modifier
                    .align(Alignment.Center)
                    .background(Color(0x99000000), RoundedCornerShape(10.dp))
                    .padding(horizontal = 18.dp, vertical = 10.dp),
            ) {
                Text(fmt(dragMs), color = Color.White, fontSize = 20.sp, fontWeight = FontWeight.SemiBold)
                Text(if (dragMs > state.positionMs) " →" else " ←", color = KpGreen, fontSize = 20.sp, fontWeight = FontWeight.SemiBold)
            }
        }

        // Подсказка двойного тапа: «−15 с» / «+15 с» у края.
        flash?.let { (fwd, _) ->
            Box(
                Modifier
                    .fillMaxHeight()
                    .fillMaxWidth(0.36f)
                    .align(if (fwd) Alignment.CenterEnd else Alignment.CenterStart)
                    .background(
                        Brush.radialGradient(
                            listOf(Color(0x38FFFFFF), Color.Transparent),
                            center = Offset(if (fwd) boxW * 0.36f * 0.7f else boxW * 0.36f * 0.3f, constraints.maxHeight / 2f),
                            radius = boxW * 0.36f * 0.7f,
                        ),
                    ),
                contentAlignment = Alignment.Center,
            ) {
                Text(if (fwd) "+$SKIP_SEC с" else "−$SKIP_SEC с", color = Color.White, fontSize = 15.sp, fontWeight = FontWeight.SemiBold)
            }
        }

        // Круг «играть» на паузе — только на ТВ (цель пульта посреди экрана).
        if (isTv && panel && !playIntent && !cs.preparing && !ended) {
            KpButton(
                onClick = onTogglePlay,
                modifier = Modifier.align(Alignment.Center),
                size = 96.dp,
                circle = true,
                background = Color(0x80000000),
            ) { Icon(KpIcons.Play, "Воспроизвести", tint = Color.White, modifier = Modifier.size(52.dp)) }
        }

        // Ожидание: белое кольцо, как у kinopub.
        if ((cs.preparing || state.buffering || state.opening) && fatalError == null && cs.error == null) {
            Spinner(Modifier.align(Alignment.Center))
        }

        // Свайп яркости или громкости: столбик у своего края.
        gesture?.let { (vol, frac) ->
            Column(
                Modifier
                    .align(if (vol) Alignment.CenterEnd else Alignment.CenterStart)
                    .padding(horizontal = 40.dp)
                    .background(Color(0x99000000), RoundedCornerShape(14.dp))
                    .padding(horizontal = 14.dp, vertical = 12.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Icon(if (vol) KpIcons.Volume else KpIcons.Brightness, null, tint = Color.White, modifier = Modifier.size(24.dp))
                Spacer(Modifier.height(10.dp))
                Box(Modifier.width(6.dp).height(120.dp).clip(RoundedCornerShape(3.dp)).background(Color(0x40FFFFFF))) {
                    Box(Modifier.align(Alignment.BottomCenter).fillMaxWidth().fillMaxHeight(frac.coerceIn(0f, 1f)).background(Color.White))
                }
                Spacer(Modifier.height(8.dp))
                Text("${(frac * 100).roundToInt()}%", color = Color.White, fontSize = 13.sp)
            }
        }

        // ---------- шапка ----------
        // Как у Kino.Watch: название без оригинала, код серии и год одной
        // строкой, название серии — второй, обычным регистром; под ними
        // «таблетки». Часы и сводка — только по «i», в своей плашке.
        if (panel) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .background(KpPlate)
                    .windowInsetsPadding(WindowInsets.displayCutout)
                    .padding(start = 12.dp, end = 16.dp, top = 8.dp, bottom = 10.dp),
            ) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    KpButton(onClick = onBack) {
                        Icon(KpIcons.ArrowBack, "Назад", tint = Color.White, modifier = Modifier.size(26.dp))
                    }
                    Spacer(Modifier.width(10.dp))
                    Column(Modifier.weight(1f)) {
                        val name = ru.sorewa.kino.ui.splitTitle(state.titleName).first
                            .ifEmpty { state.episodeLabel }
                        val code = curEp?.takeIf { it.seasonNo > 0 }?.let { "S${it.seasonNo}E${it.episodeNo}" }
                        val head = listOfNotNull(
                            name.uppercase().takeIf { it.isNotEmpty() },
                            code,
                            state.year.takeIf { it > 0 }?.let { "($it)" },
                        ).joinToString(" ")
                        Text(
                            head,
                            color = Color(0xE6FFFFFF),
                            fontSize = 15.sp,
                            letterSpacing = 0.3.sp,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        val epName = curEp?.title?.takeIf { t ->
                            t.isNotBlank() && !t.matches(Regex("(?i)\\s*(серия|эпизод|episode)?\\s*\\d+\\s*(серия)?\\s*"))
                        }
                        if (epName != null) {
                            Text(
                                epName,
                                color = Color(0xB3FFFFFF),
                                fontSize = 14.sp,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                    if (!isTv) {
                        KpButton(onClick = onPip) {
                            Icon(KpIcons.PiP, "Картинка в картинке", tint = Color.White, modifier = Modifier.size(26.dp))
                        }
                    }
                }
                Row(
                    Modifier.padding(top = 6.dp, start = 52.dp),
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    val audioNow = cs.voiceGroup?.let { g -> g.items.firstOrNull { it.id == g.selected }?.label }
                        ?: state.audioTracks.indexOfFirst { it.first == state.selectedAudioFmt }
                            .takeIf { it >= 0 }?.let { trackName(state.audioTracks[it].second, it) }
                    val subsNow = state.textTracks.indexOfFirst { it.first == state.selectedTextFmt }
                        .takeIf { it >= 0 }?.let { trackName(state.textTracks[it].second, it) } ?: "Выкл"
                    listOf(
                        Triple(Pop.VIDEO, KpIcons.Video, qualityLabel(state.videoWidth, state.videoHeight, state.qualityHeight)),
                        Triple(Pop.AUDIO, KpIcons.Audio, audioNow ?: "Аудио"),
                        Triple(Pop.SUBS, KpIcons.Subs, subsNow),
                    ).forEach { (kind, icon, value) ->
                        KpPill(
                            icon, value,
                            on = pop == kind,
                            modifier = Modifier
                                .focusRequester(pillFocus.getValue(kind))
                                .onGloballyPositioned { anchors[kind] = it.boundsInRoot() },
                        ) { pop = if (pop == kind) null else kind }
                    }
                }
            }
        }
        // Сводка по «i» — своя плашка в правом верхнем углу, как у Kino.Watch:
        // «20:41 [21:03]», что в кадре и откуда, канал и буфер, почему не та площадка.
        // По «i» — вместе с панелью; «сводка всегда» из настроек — и без неё.
        if (!state.pip && ((statsOn && panel) || settings?.statsAlways == true)) {
            Column(
                Modifier
                    .align(Alignment.TopEnd)
                    .windowInsetsPadding(WindowInsets.displayCutout)
                    // Левее кнопки «картинка в картинке»: плашка её не закрывает.
                    .padding(end = if (isTv) 0.dp else 64.dp, top = 6.dp)
                    .background(Color(0xB3141414), RoundedCornerShape(10.dp))
                    .padding(horizontal = 14.dp, vertical = 8.dp),
                horizontalAlignment = Alignment.End,
            ) {
                var now by remember { mutableStateOf(java.time.LocalTime.now()) }
                LaunchedEffect(Unit) { while (true) { kotlinx.coroutines.delay(15_000); now = java.time.LocalTime.now() } }
                val left = (state.durationMs - state.positionMs).coerceAtLeast(0) / maxOf(state.speed, 0.1f)
                val end = now.plusSeconds((left / 1000).toLong())
                val f = java.time.format.DateTimeFormatter.ofPattern("H:mm")
                val lines = listOf(
                    if (state.durationMs > 0) "${now.format(f)}  [${end.format(f)}]" else now.format(f),
                ) + statsLines(state, cs) + listOfNotNull(cs.passedNote.takeIf { it.isNotEmpty() })
                lines.forEach { line ->
                    Text(line, color = Color(0xD9FFFFFF), fontSize = 14.sp, lineHeight = 19.sp, textAlign = TextAlign.End)
                }
            }
        }

        // Строка хозяина поверх кадра: переключение площадки и озвучки.
        // Пока открыт список — надпись не нужна: она ложилась на пункты.
        cs.notice?.takeIf { pop == null }?.let { n ->
            Row(
                Modifier
                    // Под кольцом ожидания: сверху надпись налезала на «таблетки».
                    .align(Alignment.Center)
                    .offset(y = 72.dp)
                    .padding(horizontal = 16.dp)
                    .background(Color(0xC7000000), RoundedCornerShape(10.dp))
                    .padding(horizontal = 14.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (cs.noticeBusy) {
                    Spinner(size = 16.dp, stroke = 2.dp)
                    Spacer(Modifier.width(10.dp))
                }
                Text(n, color = Color.White, fontSize = 14.sp, fontWeight = FontWeight.Medium, textAlign = TextAlign.Center)
            }
        }

        // ---------- конец серии ----------
        if (endShown) {
            Column(
                Modifier
                    .fillMaxSize()
                    .background(Color(0x9E000000))
                    .padding(16.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center,
            ) {
                if (nextEp != null || cs.confirmNext) {
                    Text(
                        (if (cs.confirmNext) "Вы ещё смотрите?" else "Дальше").uppercase(),
                        color = Color(0xA6FFFFFF), fontSize = 11.sp, letterSpacing = 1.3.sp,
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(nextLabel ?: "", color = Color.White, fontSize = 17.sp, fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center)
                    Spacer(Modifier.height(12.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        EndButton("Смотреть", KpIcons.Play, go = true, modifier = Modifier.focusRequester(overlayFocus)) {
                            if (cs.confirmNext) client?.confirmNextEpisode() else onNext()
                        }
                        if (cs.confirmNext) EndButton("Остаться", null, go = false) { client?.cancelConfirmNext() }
                        else EndButton("Заново", KpIcons.Replay, go = false) { onSeek(0); onTogglePlay() }
                    }
                } else {
                    EndButton("Смотреть заново", KpIcons.Replay, go = true, modifier = Modifier.focusRequester(overlayFocus)) { onSeek(0); onTogglePlay() }
                }
            }
        }

        // ---------- пропуск заставки и титров ----------
        val skipBottom = if (panel) 148.dp else if (keyScrubMs >= 0) 96.dp else 32.dp
        if (skipShown && active != null) {
            val offerNext = active.kind == SkipKind.OUTRO || active.kind == SkipKind.PREVIEW
            SkipButton(
                if (offerNext && nextEp != null) "Следующая серия" else active.label,
                Modifier
                    .align(Alignment.BottomEnd)
                    .padding(end = 16.dp, bottom = skipBottom)
                    .focusRequester(skipFocus)
                    .onFocusChanged { skipFocused = it.isFocused },
            ) {
                if (offerNext && nextEp != null) onCreditsNext() else {
                    onSeek((active.end * 1000).toLong())
                    toast = active
                }
            }
        } else if (toast != null) {
            Row(
                Modifier.align(Alignment.BottomEnd).padding(end = 16.dp, bottom = skipBottom),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(toast!!.skippedLabel, color = Color.White, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
                Spacer(Modifier.width(10.dp))
                SkipButton("Вернуть", small = true) {
                    onSeek((toast!!.start * 1000).toLong()); toast = null
                }
            }
        }

        // ---------- перемотка с пульта при спрятанной панели ----------
        // Только полоса со временем: куда прыгнем и на сколько. Кнопки не
        // появляются — стрелки и дальше перематывают, а не ходят по ряду.
        if (!panel && !state.pip && keyScrubMs >= 0) {
            Column(
                Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .background(KpPlate)
                    .padding(start = 16.dp, end = 16.dp, top = 8.dp, bottom = 18.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                ScrubDelta(keyScrubMs - state.positionMs)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(fmt(keyScrubMs), color = Color.White, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, textAlign = TextAlign.End, modifier = Modifier.widthIn(min = 56.dp))
                    Spacer(Modifier.width(10.dp))
                    SeekBar(
                        positionMs = keyScrubMs,
                        durationMs = state.durationMs,
                        bufferedMs = state.bufferedMs,
                        skips = if (cs.skipMode != "off") cs.skips else emptyList(),
                        tv = false,
                        onScrub = { },
                        onSeek = { },
                        modifier = Modifier.weight(1f),
                    )
                    Spacer(Modifier.width(10.dp))
                    Text(fmt(state.durationMs), color = Color(0xE6FFFFFF), fontSize = 15.sp, modifier = Modifier.widthIn(min = 56.dp))
                }
            }
        }

        // ---------- низ: время — полоса — длительность, ряд кнопок ----------
        if (panel) {
            Column(
                Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .background(KpPlate)
                    .windowInsetsPadding(WindowInsets.displayCutout)
                    .padding(start = 16.dp, end = 16.dp, top = 10.dp, bottom = 8.dp),
            ) {
                if (keyScrubMs >= 0) ScrubDelta(keyScrubMs - state.positionMs, Modifier.align(Alignment.CenterHorizontally))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        fmt(shownMs), color = Color(0xE6FFFFFF), fontSize = 15.sp,
                        textAlign = TextAlign.End, modifier = Modifier.widthIn(min = 56.dp),
                    )
                    Spacer(Modifier.width(10.dp))
                    SeekBar(
                        positionMs = shownMs,
                        durationMs = state.durationMs,
                        bufferedMs = state.bufferedMs,
                        skips = if (cs.skipMode != "off") cs.skips else emptyList(),
                        tv = isTv,
                        onScrub = { barScrubMs = it },
                        onSeek = { barScrubMs = -1; onSeek(it) },
                        onKey = { fwd, repeat -> keyScrub(fwd, repeat) },
                        onCommit = { commitScrub() },
                        modifier = Modifier.weight(1f),
                    )
                    Spacer(Modifier.width(10.dp))
                    Text(
                        fmt(state.durationMs), color = Color(0xE6FFFFFF), fontSize = 15.sp,
                        modifier = Modifier.widthIn(min = 56.dp),
                    )
                }
                val speedButton: @Composable () -> Unit = {
                    KpButton(
                        onClick = { pop = if (pop == Pop.SPEED) null else Pop.SPEED },
                        modifier = Modifier
                            .focusRequester(pillFocus.getValue(Pop.SPEED))
                            .onGloballyPositioned { anchors[Pop.SPEED] = it.boundsInRoot() },
                    ) {
                        Icon(KpIcons.Speed, "Скорость", tint = if (pop == Pop.SPEED) KpGreen else Color.White, modifier = Modifier.size(28.dp))
                    }
                }
                // Ряд — полоса шириной с полэкрана, кнопки поровну: пауза всегда в центре.
                Row(
                    Modifier
                        .align(Alignment.CenterHorizontally)
                        .widthIn(max = 560.dp)
                        .fillMaxWidth()
                        .padding(top = 2.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    // ТВ: «Свернуть» не нужна — это то же «Назад», а значок «выйти
                    // из полноэкранного» на телевизоре непонятен. Скорость встаёт на
                    // её место слева: по три кнопки с каждой стороны, пауза — ровно
                    // посередине.
                    if (isTv) speedButton()
                    else KpButton(onClick = onBack) { Icon(KpIcons.ExitFull, "Свернуть", tint = Color.White, modifier = Modifier.size(28.dp)) }
                    KpButton(onClick = { if (idx > 0) onPrev() else onSeek(0) }) {
                        Icon(KpIcons.Prev, if (idx > 0) "Предыдущая серия" else "С начала", tint = Color.White, modifier = Modifier.size(28.dp))
                    }
                    KpButton(onClick = { seekBy(-SKIP_SEC) }) { SkipIcon(SKIP_SEC, fwd = false) }
                    KpButton(onClick = onTogglePlay, modifier = Modifier.focusRequester(playFocus)) {
                        Icon(
                            if (playIntent) KpIcons.Pause else KpIcons.Play,
                            if (playIntent) "Пауза" else "Играть",
                            tint = Color.White, modifier = Modifier.size(32.dp),
                        )
                    }
                    KpButton(onClick = { seekBy(SKIP_SEC) }) { SkipIcon(SKIP_SEC, fwd = true) }
                    KpButton(onClick = onNext, enabled = nextEp != null) {
                        Icon(KpIcons.Next, "Следующая серия", tint = Color.White, modifier = Modifier.size(28.dp))
                    }
                    if (!isTv) speedButton()
                    KpButton(onClick = { statsOn = !statsOn }) {
                        Icon(KpIcons.Info, "Сводка о потоке", tint = if (showStats) KpGreen else Color.White, modifier = Modifier.size(28.dp))
                    }
                }
            }
        }

        // ---------- ошибка ----------
        if (error != null) {
            Column(
                Modifier.fillMaxSize().background(Color(0x9E000000)).padding(24.dp),
                verticalArrangement = Arrangement.Center,
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Text(error, color = Color.White, fontSize = 15.sp, textAlign = TextAlign.Center)
                Spacer(Modifier.height(16.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    EndButton("Повторить", KpIcons.Replay, go = true, modifier = Modifier.focusRequester(overlayFocus)) {
                        if (fatalError != null) state.fatalError = null else client?.retry()
                    }
                    EndButton("Назад", null, go = false, onClick = onBack)
                }
            }
        }

        // ---------- списки ----------
        val p = pop
        if (p != null && panel) {
            // Подложка: тап мимо списка закрывает его. Фокус пульта ей не нужен:
            // иначе стрелка вниз с «таблетки» уводила его на невидимую подложку.
            Box(
                Modifier
                    .fillMaxSize()
                    .focusProperties { canFocus = false }
                    .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { pop = null },
            )
            val a = anchors[p]
            val boxH = constraints.maxHeight.toFloat()
            val pad = with(density) { 8.dp.toPx() }
            val modifier = if (p == Pop.SPEED) {
                // Скорость — над кнопкой, прижата к её правому краю.
                val right = boxW - (a?.right ?: boxW)
                val bottom = boxH - (a?.top ?: boxH) + with(density) { 6.dp.toPx() }
                Modifier
                    .align(Alignment.BottomEnd)
                    .padding(
                        end = with(density) { right.coerceAtLeast(pad).toDp() },
                        bottom = with(density) { bottom.toDp() },
                    )
                    .heightIn(max = with(density) { (boxH - bottom - pad).toDp() })
            } else {
                val top = (a?.bottom ?: 0f) + with(density) { 4.dp.toPx() }
                Modifier
                    .offset { IntOffset((a?.left ?: pad).roundToInt().coerceAtLeast(pad.roundToInt()), top.roundToInt()) }
                    .heightIn(max = with(density) { (boxH - top - pad).toDp() })
            }
            androidx.compose.runtime.CompositionLocalProvider(LocalMenuSel provides menuSel) { PopMenu(modifier.focusRequester(menuGroup)) {
                when (p) {
                    Pop.VIDEO -> {
                        cs.sourceGroup?.takeIf { it.items.isNotEmpty() }?.let { g -> ChoiceItems(g) { pop = null } }
                        Header("Качество")
                        val heights = cs.ticket?.renditions?.map { it.height }?.distinct()?.sortedDescending() ?: emptyList()
                        PopItem("Авто", on = state.qualityHeight == null) { onQuality(null); pop = null }
                        heights.forEach { h -> PopItem("${h}p", on = state.qualityHeight == h) { onQuality(h); pop = null } }
                    }
                    Pop.AUDIO -> {
                        val g = cs.voiceGroup?.takeIf { it.items.isNotEmpty() }
                        if (g != null) ChoiceItems(g) { pop = null }
                        else if (state.audioTracks.isNotEmpty()) {
                            Header("Озвучка")
                            state.audioTracks.forEachIndexed { i, (id, label) ->
                                PopItem(trackName(label, i), on = state.selectedAudioFmt == id) { onSelectAudio(id); pop = null }
                            }
                        } else {
                            Text("Дорожка одна, выбирать не из чего", color = Color(0x99FFFFFF), fontSize = 15.sp, modifier = Modifier.padding(horizontal = 12.dp, vertical = 11.dp))
                        }
                    }
                    Pop.SUBS -> {
                        PopItem("Выключены", on = state.selectedTextFmt == null) { onSelectText(""); pop = null }
                        state.textTracks.forEachIndexed { i, (id, label) ->
                            PopItem(trackName(label, i), on = state.selectedTextFmt == id) { onSelectText(id); pop = null }
                        }
                    }
                    Pop.SPEED -> {
                        Header("Скорость")
                        SPEEDS.forEach { s ->
                            PopItem(
                                if (s == 1f) "Обычная" else fmtSpeed(s),
                                on = kotlin.math.abs(state.speed - s) < 0.01f,
                            ) { onSpeed(s); pop = null }
                        }
                    }
                }
            } }
        }
    }
}

// ─── элементы ───────────────────────────────────────────────────────────

/** Кнопка плеера (.kp-btn): прозрачная, на ТВ в фокусе — светлая подложка и рамка. */
@Composable
private fun KpButton(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    size: androidx.compose.ui.unit.Dp? = null,
    circle: Boolean = false,
    background: Color = Color.Transparent,
    content: @Composable () -> Unit,
) {
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    val shape = if (circle) CircleShape else RoundedCornerShape(7.dp)
    Box(
        modifier
            .then(if (size != null) Modifier.size(size) else Modifier)
            .clip(shape)
            .background(if (focused) Color(0x47FFFFFF) else background)
            .then(if (focused) Modifier.border(2.dp, Color.White, shape) else Modifier)
            .clickable(
                enabled = enabled,
                interactionSource = source,
                indication = androidx.compose.material3.ripple(color = Color.White),
                onClick = onClick,
            )
            .alpha(if (enabled) 1f else 0.35f)
            .padding(if (size != null) 0.dp else 10.dp),
        contentAlignment = Alignment.Center,
    ) { content() }
}

/** «Таблетка» шапки (как у Kino.Watch): рамка, значок и текущий выбор. */
@Composable
private fun KpPill(icon: ImageVector, value: String, on: Boolean, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    val shape = RoundedCornerShape(8.dp)
    Row(
        modifier
            .clip(shape)
            .background(if (focused) Color.White else if (on) Color(0x26FFFFFF) else Color.Transparent)
            .border(1.5.dp, if (on) KpGreen else Color(0x66FFFFFF), shape)
            .clickable(interactionSource = source, indication = androidx.compose.material3.ripple(color = Color.White), onClick = onClick)
            .widthIn(max = 300.dp)
            .padding(horizontal = 12.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, null, tint = if (focused) Color.Black else Color(0xCCFFFFFF), modifier = Modifier.size(18.dp))
        Spacer(Modifier.width(8.dp))
        Text(
            value.uppercase(),
            color = if (focused) Color.Black else KpGreen,
            fontSize = 14.sp,
            fontWeight = FontWeight.SemiBold,
            letterSpacing = 0.4.sp,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * Подпись качества по кадру: 1920×800 — это «1080p» (широкий кадр ниже
 * своей ступени), а выбранное руками — без «авто».
 */
private fun qualityLabel(w: Int, h: Int, fixed: Int?): String {
    val step = when {
        w >= 3800 || h >= 2000 -> 2160
        w >= 2500 || h >= 1400 -> 1440
        w >= 1880 || h >= 1000 -> 1080
        w >= 1260 || h >= 700 -> 720
        w >= 840 || h >= 460 -> 480
        h > 0 -> h
        else -> 0
    }
    return when {
        fixed != null -> "${fixed}p"
        step > 0 -> "${step}p (авто)"
        else -> "Авто"
    }
}

/** Сводка о потоке: что в кадре, откуда и как идёт канал. */
private fun statsLines(state: PlayerViewState, cs: PlaybackUiState): List<String> {
    fun mbit(bps: Long) = String.format(java.util.Locale("ru"), "%.1f Мбит/с", bps / 1_000_000.0)
    val top = listOfNotNull(
        state.videoCodec.takeIf { it.isNotEmpty() },
        if (state.videoWidth > 0) "${state.videoWidth}×${state.videoHeight}" else null,
        state.videoBitrate.takeIf { it > 0 }?.let { "(${mbit(it.toLong())})" },
        cs.ticket?.source?.name,
    ).joinToString(" ")
    val bufSec = ((state.bufferedMs - state.positionMs).coerceAtLeast(0) / 1000)
    val bottom = listOfNotNull(
        state.bandwidthBps.takeIf { it > 0 }?.let { "канал ${mbit(it)}" },
        "буфер $bufSec с",
    ).joinToString(" · ")
    return listOf(top, bottom).filter { it.isNotBlank() }
}

/** Яркость окна плеера (не системная): доля 0..1 после сдвига. */
private fun swipeBrightness(context: android.content.Context, delta: Float): Float {
    val activity = context as? android.app.Activity ?: return 0f
    val lp = activity.window.attributes
    val cur = if (lp.screenBrightness >= 0) lp.screenBrightness else try {
        android.provider.Settings.System.getInt(context.contentResolver, android.provider.Settings.System.SCREEN_BRIGHTNESS) / 255f
    } catch (_: Exception) { 0.5f }
    val next = (cur + delta).coerceIn(0.01f, 1f)
    lp.screenBrightness = next
    activity.window.attributes = lp
    return next
}

/** Громкость музыки системы — та же, что кнопками; доля 0..1 после сдвига. */
private val volAcc = java.util.concurrent.atomic.AtomicReference(0f)
private fun swipeVolume(context: android.content.Context, delta: Float): Float {
    val am = context.getSystemService(android.media.AudioManager::class.java) ?: return 0f
    val max = am.getStreamMaxVolume(android.media.AudioManager.STREAM_MUSIC).coerceAtLeast(1)
    // Копим дробные шаги: громкость у системы целыми делениями.
    val acc = volAcc.get() + delta * max
    val steps = acc.toInt()
    volAcc.set(acc - steps)
    val cur = am.getStreamVolume(android.media.AudioManager.STREAM_MUSIC)
    val next = (cur + steps).coerceIn(0, max)
    if (steps != 0) am.setStreamVolume(android.media.AudioManager.STREAM_MUSIC, next, 0)
    return next.toFloat() / max
}

/** Узкий список (.kp-menu.kp-pop): тёмный, с рамкой, строки под палец. */
@OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)
@Composable
private fun PopMenu(modifier: Modifier, content: @Composable androidx.compose.foundation.layout.ColumnScope.() -> Unit) {
    Column(
        modifier
            // Пульт ходит только по пунктам: стрелки не выводят из списка
            // (закрывает «Назад» или выбор), сам список фокус не берёт.
            .focusProperties {
                @Suppress("DEPRECATION")
                exit = { FocusRequester.Cancel }
            }
            .focusGroup()
            .width(androidx.compose.foundation.layout.IntrinsicSize.Max)
            .widthIn(min = 200.dp, max = 360.dp)
            .clip(RoundedCornerShape(8.dp))
            .background(KpMenuBg)
            .border(1.dp, KpMenuBorder, RoundedCornerShape(8.dp))
            .focusProperties { canFocus = false }
            .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { }
            .verticalScroll(rememberScrollState())
            .padding(6.dp),
        content = content,
    )
}

@Composable
private fun Header(text: String) {
    Text(
        text.uppercase(),
        color = Color(0x8CFFFFFF),
        fontSize = 12.sp,
        fontWeight = FontWeight.SemiBold,
        letterSpacing = 0.6.sp,
        modifier = Modifier.padding(start = 10.dp, end = 10.dp, top = 8.dp, bottom = 4.dp),
    )
}

@Composable
private fun PopItem(label: String, on: Boolean, hint: String? = null, onClick: () -> Unit) {
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    val sel = LocalMenuSel.current
    Row(
        Modifier
            .then(if (on && sel != null) Modifier.focusRequester(sel) else Modifier)
            .fillMaxWidth()
            .clip(RoundedCornerShape(7.dp))
            // Фокус пульта — белая плашка, как у «таблеток» шапки: видно с дивана.
            .background(if (focused) Color.White else Color.Transparent)
            .clickable(interactionSource = source, indication = androidx.compose.material3.ripple(color = Color.White), onClick = onClick)
            .padding(start = 10.dp, end = 12.dp, top = 11.dp, bottom = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.width(26.dp)) {
            if (on) Icon(KpIcons.Check, null, tint = if (focused) KpGreenDark else KpGreen, modifier = Modifier.size(18.dp))
        }
        // Название — главное: меряется первым и сжимается последним. Подсказка
        // (откуда, «не вышло») — что осталось, с многоточием. Наоборот нельзя:
        // на ТВ длинный список площадок съедает название студии до «AniT…»
        // или вовсе до пустоты.
        Text(label, color = when { focused && on -> KpGreenDark; focused -> Color(0xFF111111); on -> KpGreen; else -> Color.White }, fontSize = 15.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (hint != null) {
            Spacer(Modifier.width(14.dp))
            Text(hint, color = if (focused) Color(0x99111111) else Color(0x80FFFFFF), fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
        }
    }
}

/** Группа выбора хозяина (озвучки, площадки) — заголовок и пункты. */
@Composable
private fun ChoiceItems(g: MenuGroup, done: () -> Unit) {
    Header(g.title)
    g.items.forEach { it: MenuItem ->
        PopItem(it.label, on = it.id == g.selected, hint = it.hint) {
            if (it.id != g.selected) g.onSelect(it.id)
            done()
        }
    }
}

/** «Пропустить заставку» (.kp-skip-go): тёмная плашка с белой рамкой. */
@Composable
private fun SkipButton(text: String, modifier: Modifier = Modifier, small: Boolean = false, onClick: () -> Unit) {
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    Text(
        text,
        color = if (focused) Color(0xFF111111) else Color.White,
        fontSize = 14.sp,
        fontWeight = if (small) FontWeight.Medium else FontWeight.SemiBold,
        modifier = modifier
            .clip(RoundedCornerShape(6.dp))
            .background(if (focused) Color.White else Color(0xBD101010))
            .border(1.dp, Color(0x99FFFFFF), RoundedCornerShape(6.dp))
            .clickable(interactionSource = source, indication = androidx.compose.material3.ripple(), onClick = onClick)
            .padding(horizontal = if (small) 14.dp else 20.dp, vertical = if (small) 9.dp else 11.dp),
    )
}

/** Кнопки экрана конца серии (.kp-end-go / .kp-end-alt): капсулы. */
@Composable
private fun EndButton(text: String, icon: ImageVector?, go: Boolean, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    Row(
        modifier
            .clip(RoundedCornerShape(50))
            .background(if (go) KpGreen else Color(0x24FFFFFF))
            .then(if (focused) Modifier.border(3.dp, Color.White, RoundedCornerShape(50)) else Modifier)
            .clickable(interactionSource = source, indication = androidx.compose.material3.ripple(), onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 9.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val fg = if (go) Color(0xFF06130B) else Color.White
        if (icon != null) {
            Icon(icon, null, tint = fg, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(8.dp))
        }
        Text(text, color = fg, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
    }
}

/** Кольцо ожидания (.kp-spin): белая дуга на полупрозрачном круге. */
@Composable
private fun Spinner(modifier: Modifier = Modifier, size: androidx.compose.ui.unit.Dp = 64.dp, stroke: androidx.compose.ui.unit.Dp = 4.dp) {
    val t = rememberInfiniteTransition(label = "spin")
    val angle by t.animateFloat(0f, 360f, infiniteRepeatable(tween(900, easing = LinearEasing), RepeatMode.Restart), label = "a")
    Canvas(modifier.size(size)) {
        val w = stroke.toPx()
        val inset = w / 2
        val s = androidx.compose.ui.geometry.Size(this.size.width - w, this.size.height - w)
        drawArc(Color(0x40FFFFFF), 0f, 360f, false, Offset(inset, inset), s, style = Stroke(w))
        drawArc(Color.White, angle - 90f, 90f, false, Offset(inset, inset), s, style = Stroke(w))
    }
}

/** Полоса времени (.kp-seek): тонкая дорожка 3dp, бегунок 16dp, отрезки пропусков. */
@Composable
private fun SeekBar(
    positionMs: Long,
    durationMs: Long,
    bufferedMs: Long,
    skips: List<Skip>,
    tv: Boolean,
    onScrub: (Long) -> Unit,
    onSeek: (Long) -> Unit,
    onKey: (fwd: Boolean, repeat: Int) -> Unit = { _, _ -> },
    onCommit: () -> Unit = {},
    modifier: Modifier = Modifier,
) {
    var dragging by remember { mutableStateOf(false) }
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    Box(
        modifier
            .height(32.dp)
            .then(
                if (tv) Modifier
                    .onPreviewKeyEvent { e ->
                        if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                        when (e.key) {
                            Key.DirectionLeft -> { onKey(false, e.nativeKeyEvent.repeatCount); true }
                            Key.DirectionRight -> { onKey(true, e.nativeKeyEvent.repeatCount); true }
                            else -> false
                        }
                    }
                    // OK на полосе — прыгнуть туда, куда уже накопили.
                    .clickable(interactionSource = source, indication = null, onClick = onCommit)
                else Modifier,
            )
            .pointerInput(durationMs) {
                if (durationMs <= 0) return@pointerInput
                detectTapGestures { o -> onSeek((o.x / size.width * durationMs).toLong().coerceIn(0, durationMs)) }
            }
            .pointerInput(durationMs) {
                if (durationMs <= 0) return@pointerInput
                var last = 0L
                detectHorizontalDragGestures(
                    onDragStart = { o ->
                        dragging = true
                        last = (o.x / size.width * durationMs).toLong().coerceIn(0, durationMs)
                        onScrub(last)
                    },
                    onDragEnd = { dragging = false; onSeek(last) },
                    onDragCancel = { dragging = false; onScrub(-1) },
                ) { change, _ ->
                    change.consume()
                    last = (change.position.x / size.width * durationMs).toLong().coerceIn(0, durationMs)
                    onScrub(last)
                }
            },
    ) {
        Canvas(Modifier.fillMaxSize()) {
            val y = size.height / 2
            val h = (if (focused) 6.dp else 3.dp).toPx()
            val w = size.width
            val played = if (durationMs > 0) (positionMs.toFloat() / durationMs).coerceIn(0f, 1f) else 0f
            val buf = if (durationMs > 0) (bufferedMs.toFloat() / durationMs).coerceIn(0f, 1f) else 0f
            drawLine(Color(0x4DFFFFFF), Offset(0f, y), Offset(w, y), h, StrokeCap.Round)
            if (buf > 0) drawLine(Color(0x57FFFFFF), Offset(0f, y), Offset(w * buf, y), h, StrokeCap.Round)
            if (durationMs > 0) for (s in skips) {
                val x1 = (s.start * 1000 / durationMs).toFloat() * w
                val x2 = (s.end * 1000 / durationMs).toFloat() * w
                drawLine(KpSeg, Offset(x1, y), Offset(x2, y), h, StrokeCap.Round)
            }
            drawLine(KpGreen, Offset(0f, y), Offset(w * played, y), h, StrokeCap.Round)
            val r = (if (dragging || focused) 10.dp else 8.dp).toPx()
            if (focused) drawCircle(KpGreen.copy(alpha = 0.35f), r + 4.dp.toPx(), Offset(w * played, y))
            drawCircle(KpGreen, r, Offset(w * played, y))
        }
    }
}

/** На сколько перемотаем с пульта: «+2:15» / «−0:45». */
@Composable
private fun ScrubDelta(deltaMs: Long, modifier: Modifier = Modifier) {
    Text(
        (if (deltaMs >= 0) "+" else "−") + fmt(kotlin.math.abs(deltaMs)),
        color = KpGreen,
        fontSize = 17.sp,
        fontWeight = FontWeight.SemiBold,
        modifier = modifier.padding(bottom = 4.dp),
    )
}

/** Перемотка на sec секунд: круговая стрелка и число внутри (IconSkip). */
@Composable
private fun SkipIcon(sec: Int, fwd: Boolean) {
    Box(Modifier.size(28.dp), contentAlignment = Alignment.Center) {
        Icon(if (fwd) KpIcons.SkipFwd else KpIcons.SkipBack, if (fwd) "Вперёд $sec секунд" else "Назад $sec секунд", tint = Color.White, modifier = Modifier.fillMaxSize())
        Text(
            "$sec",
            color = Color.White,
            fontSize = 8.6.sp,
            fontWeight = FontWeight.Bold,
            modifier = Modifier.offset(y = 2.dp),
        )
    }
}

// ─── значки: те же контуры, что в web/packages/player/src/ui/icons.tsx ───

private object KpIcons {
    private fun icon(name: String, vararg d: String, stroke: Boolean = false) = ImageVector.Builder(
        name = name, defaultWidth = 24.dp, defaultHeight = 24.dp, viewportWidth = 24f, viewportHeight = 24f,
    ).apply {
        d.forEach { path ->
            val nodes = absolutePath(PathParser().parsePathString(path).toNodes())
            if (stroke) addPath(nodes, stroke = SolidColor(Color.White), strokeLineWidth = 2f, strokeLineCap = StrokeCap.Round)
            else addPath(nodes, fill = SolidColor(Color.White))
        }
    }.build()

    val Play = icon("play", "M8 5v14l11-7z")
    val Pause = icon("pause", "M6 5h4v14H6zm8 0h4v14h-4z")
    val Prev = icon("prev", "M6 6h2v12H6zm3.5 6 8.5 6V6z")
    val Next = icon("next", "M6 18l8.5-6L6 6v12zm10-12v12h2V6h-2z")
    val ArrowBack = icon("back", "M20 11H7.8l5.6-5.6L12 4l-8 8 8 8 1.4-1.4L7.8 13H20z")
    val ExitFull = icon("exitfull", "M5 16h3v3h2v-5H5v2zm3-8H5v2h5V5H8v3zm6 11h2v-3h3v-2h-5v5zm2-11V5h-2v5h5V8h-3z")
    val PiP = icon("pip", "M19 11h-8v6h8v-6zm4 8V5a2 2 0 0 0-2-2H3a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h18a2 2 0 0 0 2-2zm-2 0H3V5h18v14z")
    val Replay = icon("replay", "M12 5V1L7 6l5 5V7a5 5 0 1 1-5 5H5a7 7 0 1 0 7-7z")
    val Check = icon("check", "M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z")
    val SkipBack = icon("skipback", "M12 5V1L7 6l5 5V7a6 6 0 1 1-6 6H4a8 8 0 1 0 8-8z")
    val SkipFwd = icon("skipfwd", "M12 5V1l5 5-5 5V7a6 6 0 1 0 6 6h2a8 8 0 1 1-8-8z")
    val Video = icon("video", "M17 10.5V7a1 1 0 0 0-1-1H4a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-3.5l4 4v-11l-4 4zM15 16H5V8h10v8z")
    val Audio = icon("audio", "M3 9v6h4l5 5V4L7 9H3zm13.5 3a4.5 4.5 0 0 0-2.5-4v8a4.5 4.5 0 0 0 2.5-4z")
    val Volume = icon("volume", "M3 9v6h4l5 5V4L7 9H3zm13.5 3a4.5 4.5 0 0 0-2.5-4v8a4.5 4.5 0 0 0 2.5-4zM14 3.2v2.1a7 7 0 0 1 0 13.4v2.1a9 9 0 0 0 0-17.6z")
    val Subs = icon("subs", "M19 4H5a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2zm-8 7H9.5v-.5h-2v3h2V13H11v1a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1v-4a1 1 0 0 1 1-1h3a1 1 0 0 1 1 1v1zm7 0h-1.5v-.5h-2v3h2V13H18v1a1 1 0 0 1-1 1h-3a1 1 0 0 1-1-1v-4a1 1 0 0 1 1-1h3a1 1 0 0 1 1 1v1z")
    val Info = icon("info", "M11 7h2v2h-2zm0 4h2v6h-2zm1-9C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm0 18c-4.41 0-8-3.59-8-8s3.59-8 8-8 8 3.59 8 8-3.59 8-8 8z")
    val Brightness = icon("bright", "M20 8.69V4h-4.69L12 .69 8.69 4H4v4.69L.69 12 4 15.31V20h4.69L12 23.31 15.31 20H20v-4.69L23.31 12 20 8.69zM12 18c-3.31 0-6-2.69-6-6s2.69-6 6-6 6 2.69 6 6-2.69 6-6 6zm0-10c-2.21 0-4 1.79-4 4s1.79 4 4 4 4-1.79 4-4-1.79-4-4-4z")
    /** Скорость: часы с «ветром» слева, как у kinopub. */
    val Speed = icon(
        "speed",
        "M21.5 12a7 7 0 1 1-14 0a7 7 0 1 1 14 0", "M14.5 8.5V12l2.5 2", "M2 9h3.5M2 12h2.5M2 15h3.5",
        stroke = true,
    )
}

/**
 * Контур в абсолютных координатах. Compose отдаёт «m» после «z» прямо в
 * Path.rMoveTo, а Skia старых Android (Huawei TV) отсчитывает его от последней
 * точки, а не от начала закрытого контура: вторая полоска паузы уезжала вниз,
 * «i» в кружке рассыпалась. Абсолютные узлы рисуются одинаково везде.
 */
internal fun absolutePath(nodes: List<PathNode>): List<PathNode> {
    var cx = 0f; var cy = 0f; var sx = 0f; var sy = 0f
    return nodes.map { n ->
        val a: PathNode = when (n) {
            is PathNode.RelativeMoveTo -> PathNode.MoveTo(cx + n.dx, cy + n.dy)
            is PathNode.RelativeLineTo -> PathNode.LineTo(cx + n.dx, cy + n.dy)
            is PathNode.RelativeHorizontalTo -> PathNode.HorizontalTo(cx + n.dx)
            is PathNode.RelativeVerticalTo -> PathNode.VerticalTo(cy + n.dy)
            is PathNode.RelativeCurveTo -> PathNode.CurveTo(cx + n.dx1, cy + n.dy1, cx + n.dx2, cy + n.dy2, cx + n.dx3, cy + n.dy3)
            is PathNode.RelativeReflectiveCurveTo -> PathNode.ReflectiveCurveTo(cx + n.dx1, cy + n.dy1, cx + n.dx2, cy + n.dy2)
            is PathNode.RelativeQuadTo -> PathNode.QuadTo(cx + n.dx1, cy + n.dy1, cx + n.dx2, cy + n.dy2)
            is PathNode.RelativeReflectiveQuadTo -> PathNode.ReflectiveQuadTo(cx + n.dx, cy + n.dy)
            is PathNode.RelativeArcTo -> PathNode.ArcTo(
                n.horizontalEllipseRadius, n.verticalEllipseRadius, n.theta,
                n.isMoreThanHalf, n.isPositiveArc, cx + n.arcStartDx, cy + n.arcStartDy,
            )
            else -> n
        }
        when (a) {
            is PathNode.MoveTo -> { cx = a.x; cy = a.y; sx = cx; sy = cy }
            is PathNode.LineTo -> { cx = a.x; cy = a.y }
            is PathNode.HorizontalTo -> cx = a.x
            is PathNode.VerticalTo -> cy = a.y
            is PathNode.CurveTo -> { cx = a.x3; cy = a.y3 }
            is PathNode.ReflectiveCurveTo -> { cx = a.x2; cy = a.y2 }
            is PathNode.QuadTo -> { cx = a.x2; cy = a.y2 }
            is PathNode.ReflectiveQuadTo -> { cx = a.x; cy = a.y }
            is PathNode.ArcTo -> { cx = a.arcStartX; cy = a.arcStartY }
            is PathNode.Close -> { cx = sx; cy = sy }
            else -> {}
        }
        a
    }
}

/** Дорожка без подписи («und», пусто) — по номеру, а не служебным кодом. */
private fun trackName(label: String, i: Int): String =
    label.takeIf { it.isNotBlank() && it != "und" && it != "zxx" } ?: "Дорожка ${i + 1}"

private fun fmtSpeed(s: Float): String =
    java.lang.String.format(java.util.Locale.US, "%.2f", s).trimEnd('0').trimEnd('.') + "×"

/** Время как у веб-плеера: «0:51», «12:04», «1:02:03». */
private fun fmt(ms: Long): String {
    if (ms <= 0) return "0:00"
    val total = ms / 1000
    val h = total / 3600
    val m = (total % 3600) / 60
    val s = total % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, s) else "%d:%02d".format(m, s)
}
