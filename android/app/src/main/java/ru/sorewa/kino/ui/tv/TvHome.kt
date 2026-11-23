package ru.sorewa.kino.ui.tv

import android.os.Build
import androidx.compose.animation.Crossfade
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.gestures.LocalBringIntoViewSpec
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import kotlinx.coroutines.launch
import ru.sorewa.kino.ui.NoBringIntoView
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.blur
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.ContinueItem
import ru.sorewa.kino.ui.PosterCard
import ru.sorewa.kino.ui.PosterImage
import ru.sorewa.kino.ui.PrimaryButton
import ru.sorewa.kino.ui.ProgressLine
import ru.sorewa.kino.ui.Rail
import ru.sorewa.kino.ui.SecondaryButton
import ru.sorewa.kino.ui.SectionHeader
import ru.sorewa.kino.ui.ShotImage
import ru.sorewa.kino.ui.WideCard
import ru.sorewa.kino.ui.describeContinue
import ru.sorewa.kino.ui.kindSubtitle
import ru.sorewa.kino.ui.openTitle
import ru.sorewa.kino.ui.phone.progressOf
import ru.sorewa.kino.ui.playEpisode
import ru.sorewa.kino.ui.rememberPaged
import ru.sorewa.kino.ui.splitTitle
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Bg
import ru.sorewa.kino.ui.theme.Overline
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.tvFocusMemory
import ru.sorewa.kino.ui.tvFocusKey
import ru.sorewa.kino.ui.rememberTvFocusKey

/** Что показывает афиша: то, на чём сейчас фокус. */
data class Feature(
    val titleId: Long,
    val name: String,
    val hasArt: Boolean,
    val kicker: String,
    val meta: String,
    val episodeId: Long = 0,
    val progress: Float = 0f,
    val playLabel: String = "",
)

private fun ContinueItem.feature() = Feature(
    titleId = titleId, name = title, hasArt = hasArt,
    kicker = "Продолжить",
    meta = listOf(kindSubtitle(kind, year, null), describeContinue(this)).filter { it.isNotEmpty() }.joinToString("  ·  "),
    episodeId = episodeId,
    progress = progressOf(this),
    playLabel = if (seasonNo > 0) "Смотреть $seasonNo×$episodeNo" else "Продолжить",
)

/**
 * Главная на ТВ: афиша сверху показывает выбранное (меняется вместе с
 * фокусом), ниже ленты. Фокус сразу на «Смотреть» — одно нажатие OK, и
 * серия идёт.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun TvHome(nav: NavHostController) {
    val api = remember { KinoApp.instance.container.api }
    val context = LocalContext.current
    // «Продолжить», новые серии и отложенное перечитываются при каждом
    // возвращении на главную — из плеера тоже: досмотрел 2×11 — афиша
    // зовёт на 2×12, а не на ту же серию.
    val cont = rememberPaged<ContinueItem>("continue", refresh = true) { api.continueWatching(it) }
    val fresh = rememberPaged("new", refresh = true) { api.newEpisodes(true, it) }
    val watchlist = rememberPaged("wl", refresh = true) { api.list("watchlist", it) }
    val movies = rememberPaged("movies", pageBased = true) { api.browse("movie", null, "new", it) }
    val series = rememberPaged("series", pageBased = true) { api.browse("series", null, "new", it) }

    var feature by remember { mutableStateOf<Feature?>(null) }
    // Афиша выбрана сама (не карточкой под пультом) — пересчитывается, пока
    // ленты догружаются: первой в афише встаёт первая лента, а не та, что
    // пришла раньше.
    var featureAuto by remember { mutableStateOf(true) }
    fun pick(f: Feature) { featureAuto = false; feature = f }
    val playFocus = remember { FocusRequester() }
    // Карточка в лентах, с которой ушли на тайтл: «Назад» — пульт на неё же.
    val focusMem = rememberTvFocusKey()

    val contItems = cont.items
    // Афиша по умолчанию: что продолжить, иначе новые серии, иначе первое из
    // лент — у нового человека верх главной не пустой, и пульту есть куда
    // встать сразу. «Продолжить» сверяется со свежим списком: серия в
    // афише сменяется следующей, как только сервер её отдал.
    LaunchedEffect(contItems, fresh.items, watchlist.items, movies.items, series.items) {
        val f = feature
        val fallback = contItems?.firstOrNull()?.feature()
            ?: fresh.items?.firstOrNull()?.let {
                Feature(it.titleId, it.title, it.hasArt, "Новые серии", "${it.unwatched} новых", playLabel = "")
            }
            ?: listOf("Буду смотреть" to watchlist, "Новые фильмы" to movies, "Новые сериалы" to series)
                .firstNotNullOfOrNull { (label, p) ->
                    p.items?.firstOrNull()?.let { t -> Feature(t.id, t.title, t.hasArt, label, kindSubtitle(t.kind, t.year, t.species)) }
                }
        feature = when {
            f == null || featureAuto -> fallback ?: f
            f.kicker == "Продолжить" && contItems != null ->
                contItems.firstOrNull { it.titleId == f.titleId }?.feature() ?: fallback
            else -> f
        }
    }
    // Фокус — на «Смотреть», как только кнопка появилась (кадр спустя).
    val hasFeature = feature != null
    LaunchedEffect(hasFeature) {
        if (hasFeature) {
            androidx.compose.runtime.withFrameNanos { }
            androidx.compose.runtime.withFrameNanos { }
            if (!focusMem.tryRestore()) {
                try { playFocus.requestFocus() } catch (_: Exception) { }
            }
        }
    }

    fun play(f: Feature) {
        if (f.episodeId > 0) playEpisode(context, f.episodeId, f.titleId) else nav.openTitle(f.titleId)
    }

    // Фокус в лентах: афиша ниже и без кнопок, чтобы ряд целиком влезал под неё.
    // Иначе лента (~310dp) не помещалась в оставшиеся ~244dp, и Compose
    // подтягивал её на каждом нажатии — экран раскачивался.
    var inRails by remember { mutableStateOf(false) }
    val heroHeight by animateDpAsState(if (inRails) 204.dp else 296.dp, tween(220), label = "hero")
    val listState = rememberLazyListState()
    val scope = rememberCoroutineScope()
    var focusedSection by remember { mutableIntStateOf(-1) }

    Column(Modifier.fillMaxSize()) {
        // ── Афиша ──
        Box(Modifier.fillMaxWidth().height(heroHeight)) {
            Crossfade(feature, animationSpec = tween(350), label = "hero") { f ->
                if (f != null) HeroBackdrop(f)
            }
            val f = feature
            if (f != null) {
                Column(
                    Modifier
                        .align(Alignment.BottomStart)
                        .padding(start = 32.dp, bottom = 20.dp)
                        .fillMaxWidth(0.55f),
                ) {
                    // В лентах надпись над названием повторяла заголовок ленты прямо
                    // под афишей («НОВЫЕ ФИЛЬМЫ» над «Новые фильмы») — там её нет.
                    // Колонка прижата к низу: строки ниже от этого не сдвигаются.
                    if (!inRails) {
                        Text(f.kicker.uppercase(), style = Overline, color = Accent)
                        Spacer(Modifier.height(6.dp))
                    }
                    val (name, original) = splitTitle(f.name)
                    // В лентах у афиши постоянная высота строк: название в одну
                    // строку, место под оригинал и полосу прогресса есть всегда —
                    // текст не прыгает от карточки к карточке.
                    if (inRails) {
                        OneLineTitle(name, MaterialTheme.typography.displaySmall, Color.White)
                    } else {
                        Text(
                            name,
                            style = MaterialTheme.typography.displaySmall,
                            color = Color.White,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    if (inRails || original.isNotEmpty()) {
                        Text(original.ifEmpty { " " }, style = MaterialTheme.typography.titleMedium, color = Color(0xB3FFFFFF), maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    Spacer(Modifier.height(8.dp))
                    Text(f.meta, style = MaterialTheme.typography.titleMedium, color = Color(0xE6FFFFFF), maxLines = 1, overflow = TextOverflow.Ellipsis)
                    if (inRails) {
                        Spacer(Modifier.height(10.dp))
                        if (f.progress > 0f) ProgressLine(f.progress, Modifier.width(320.dp), height = 4.dp)
                        else Spacer(Modifier.height(4.dp))
                    } else {
                        if (f.progress > 0f) {
                            Spacer(Modifier.height(10.dp))
                            ProgressLine(f.progress, Modifier.width(320.dp), height = 4.dp)
                        }
                        Spacer(Modifier.height(16.dp))
                        // Фокус на кнопках — ленты под афишей с начала.
                        Row(
                            Modifier.onFocusChanged {
                                if (it.hasFocus) {
                                    focusMem.forget()
                                    scope.launch { listState.scrollToItem(0) }
                                }
                            },
                            horizontalArrangement = Arrangement.spacedBy(14.dp),
                        ) {
                            PrimaryButton(
                                f.playLabel.ifEmpty { "Открыть" },
                                Modifier.focusRequester(playFocus),
                                icon = if (f.episodeId > 0) Icons.Rounded.PlayArrow else Icons.Rounded.Info,
                            ) { play(f) }
                            if (f.episodeId > 0) {
                                SecondaryButton("Подробнее", icon = Icons.Rounded.Info) { nav.openTitle(f.titleId) }
                            }
                        }
                    }
                }
            }
        }

        // ── Ленты ──
        // Секция = заголовок + лента. Вверх-вниз лист перелистывается целой
        // секцией (она встаёт к верху), вбок лента едет только у края.
        val sections = buildList<Pair<String, @Composable () -> Unit>> {
            contItems?.distinctBy { it.titleId }?.takeIf { it.isNotEmpty() }?.let { list ->
                add("Продолжить смотреть" to @Composable {
                    Rail {
                        // Ключ — тайтл, а не серия: после плеера карточка
                        // сменяет серию на месте, и фокус пульта остаётся на ней.
                        items(list, key = { it.titleId }) { c ->
                            WideCard(
                                episodeId = c.episodeId, titleId = c.titleId, hasArt = c.hasArt,
                                name = c.title, sub = describeContinue(c), progress = progressOf(c),
                                showPlay = false,
                                modifier = Modifier
                                    .width(272.dp)
                                    .tvFocusKey(focusMem, "cont:${c.titleId}")
                                    .onFocusChanged { if (it.hasFocus) pick(c.feature()) },
                                onClick = { playEpisode(context, c.episodeId, c.titleId) },
                            )
                        }
                    }
                })
            }
            fresh.items?.takeIf { it.isNotEmpty() }?.let { list ->
                add("Новые серии" to @Composable {
                    Rail {
                        items(list, key = { it.titleId }) { t ->
                            PosterCard(
                                titleId = t.titleId, name = t.title, hasArt = t.hasArt,
                                sub = kindSubtitle(t.kind, t.year, null), badge = "+${t.unwatched}",
                                modifier = Modifier.width(132.dp).tvFocusKey(focusMem, "new:${t.titleId}").onFocusChanged {
                                    if (it.hasFocus) pick(Feature(t.titleId, t.title, t.hasArt, "Новые серии", "${kindSubtitle(t.kind, t.year, null)}  ·  новых: ${t.unwatched}"))
                                },
                                onClick = { nav.openTitle(t.titleId) },
                            )
                        }
                    }
                })
            }
            listOf(
                "Буду смотреть" to watchlist,
                "Новые фильмы" to movies,
                "Новые сериалы" to series,
            ).forEach { (label, p) ->
                p.items?.takeIf { it.isNotEmpty() }?.let { list ->
                    add(label to @Composable {
                        Rail {
                            items(list, key = { it.id }) { t ->
                                PosterCard(
                                    titleId = t.id, name = t.title, hasArt = t.hasArt,
                                    sub = kindSubtitle(t.kind, t.year, t.species),
                                    modifier = Modifier.width(132.dp).tvFocusKey(focusMem, "$label:${t.id}").onFocusChanged {
                                        if (it.hasFocus) pick(Feature(t.id, t.title, t.hasArt, label, kindSubtitle(t.kind, t.year, t.species)))
                                    },
                                    onClick = { nav.openTitle(t.id) },
                                )
                            }
                        }
                    })
                }
            }
        }
        CompositionLocalProvider(LocalBringIntoViewSpec provides NoBringIntoView) {
            LazyColumn(
                Modifier
                    .fillMaxSize()
                    .tvFocusMemory()
                    .onFocusChanged { inRails = it.hasFocus }
                    // Вверх с первой ленты — к кнопкам афиши. Их нет, пока фокус
                    // в лентах, поэтому сначала вернуть афишу, потом фокус.
                    .onPreviewKeyEvent { e ->
                        if (e.type == KeyEventType.KeyDown && e.key == Key.DirectionUp && focusedSection == 0) {
                            inRails = false
                            scope.launch {
                                listState.scrollToItem(0)
                                withFrameNanos { }
                                try { playFocus.requestFocus() } catch (_: Exception) { }
                            }
                            true
                        } else false
                    },
                state = listState,
                contentPadding = PaddingValues(start = 28.dp, end = 40.dp, bottom = 40.dp),
            ) {
                sections.forEachIndexed { i, (label, rail) ->
                    item(key = label) {
                        Column(Modifier.onFocusChanged {
                            if (it.hasFocus) {
                                focusedSection = i
                                scope.launch { listState.animateScrollToItem(i) }
                            }
                        }) {
                            SectionHeader(label)
                            rail()
                        }
                    }
                }
            }
        }
    }
}

/** Фон афиши: кадр серии или размытый постер справа, затухающий в фон. */
@Composable
fun HeroBackdrop(f: Feature) {
    Box(Modifier.fillMaxSize()) {
        Box(
            Modifier
                .align(Alignment.TopEnd)
                .fillMaxHeight()
                .fillMaxWidth(0.72f),
        ) {
            if (f.episodeId > 0) {
                ShotImage(f.episodeId, f.titleId, f.hasArt, f.name, Modifier.fillMaxSize())
            } else {
                if (Build.VERSION.SDK_INT >= 31) {
                    PosterImage(
                        f.titleId, f.hasArt, f.name,
                        Modifier.fillMaxSize().blur(24.dp).alpha(0.7f),
                    )
                } else {
                    SoftPoster(f.titleId, f.hasArt, f.name, Modifier.fillMaxSize().alpha(0.7f))
                }
            }
            Box(Modifier.fillMaxSize().background(Brush.horizontalGradient(0f to Bg, 0.45f to Bg.copy(alpha = 0.55f), 1f to Color.Transparent)))
            Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.55f to Color.Transparent, 1f to Bg)))
        }
    }
}

/**
 * Размытый постер без blur(): тот работает только с Android 12, а на
 * телевизорах вроде Huawei (Android 10) афиша была резким увеличенным
 * куском постера. Здесь постер грузится крошечным (24×36) и растягивается
 * с мягкой фильтрацией — выходит то же пятно цвета, что и с размытием.
 */
@Composable
private fun SoftPoster(titleId: Long, hasArt: Boolean, name: String, modifier: Modifier) {
    if (!hasArt || titleId <= 0) {
        ru.sorewa.kino.ui.ArtPlaceholder(name, modifier, showName = false)
        return
    }
    val context = LocalContext.current
    val request = remember(titleId) {
        coil.request.ImageRequest.Builder(context)
            .data(KinoApp.instance.container.api.artUrl(titleId))
            .size(24, 36)
            // Свой ключ в памяти: иначе Coil отдаёт уже загруженный лентой
            // полноразмерный постер (больший размер «подходит» и меньшему).
            .memoryCacheKey("soft-poster-$titleId")
            .build()
    }
    coil.compose.AsyncImage(
        model = request,
        contentDescription = null,
        contentScale = androidx.compose.ui.layout.ContentScale.Crop,
        filterQuality = androidx.compose.ui.graphics.FilterQuality.High,
        modifier = modifier,
    )
}

/**
 * Название в одну строку, обрезанное по целому слову, без знака препинания
 * перед «…»: обычное многоточие давало «Маша и Медведь. Друзья.…» — четыре
 * точки подряд. Строка переносится по словам, остаётся первая; не влезло
 * и «…» — уходит ещё слово.
 */
@Composable
private fun OneLineTitle(text: String, style: androidx.compose.ui.text.TextStyle, color: Color) {
    var shown by remember(text) { mutableStateOf(text) }
    Text(
        shown,
        style = style,
        color = color,
        maxLines = 1,
        softWrap = true,
        overflow = TextOverflow.Clip,
        onTextLayout = { r ->
            if (r.hasVisualOverflow) {
                val base = shown.removeSuffix("…")
                var cut = shown.substring(0, minOf(r.getLineEnd(0), base.length)).trimEnd()
                if (shown != text && cut.length >= base.length) cut = base.substringBeforeLast(' ', "")
                cut = cut.trimEnd { it.isWhitespace() || it in ".,:;!?—–-" }
                if (cut.isNotEmpty() && "$cut…" != shown) shown = "$cut…"
            }
        },
    )
}
