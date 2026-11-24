package ru.sorewa.kino.ui.phone

import ru.sorewa.kino.ui.LocalBottomBarInset
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.focusable
import androidx.compose.foundation.border
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyGridState
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Mic
import androidx.compose.material.icons.rounded.History
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.SearchOff
import androidx.compose.material.icons.rounded.NorthWest
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.focus.FocusDirection
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.Title
import ru.sorewa.kino.ui.EmptyState
import ru.sorewa.kino.ui.ErrorView
import ru.sorewa.kino.ui.LoadMore
import ru.sorewa.kino.ui.LocalTv
import ru.sorewa.kino.ui.tvGridNav
import ru.sorewa.kino.ui.Pill
import ru.sorewa.kino.ui.PosterGridSkeleton
import ru.sorewa.kino.ui.PosterImage
import ru.sorewa.kino.ui.SectionHeader
import ru.sorewa.kino.ui.TitleCard
import ru.sorewa.kino.ui.kindSubtitle
import ru.sorewa.kino.ui.openTitle
import ru.sorewa.kino.ui.rememberPaged
import ru.sorewa.kino.ui.splitTitle
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Surface2
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextFaint
import ru.sorewa.kino.ui.theme.TextMain
import ru.sorewa.kino.ui.tvFocusMemory
import ru.sorewa.kino.ui.tvFocusKey
import ru.sorewa.kino.ui.tvFocusRing

/**
 * Поиск: поле сверху, до ввода — история (одна на все устройства), по мере
 * набора — подсказки из нашего каталога с постерами, по Enter — сетка.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun SearchScreen(nav: NavHostController) {
    val api = remember { KinoApp.instance.container.api }
    val scope = rememberCoroutineScope()
    val keyboard = LocalSoftwareKeyboardController.current
    val tv = LocalTv.current
    // Текст вместе с курсором: поле на строке теряло курсор при каждом
    // возвращении на экран (из меню, с тайтла) — он вставал в начало, и
    // стереть набранное было нельзя. Текст, подставленный приложением
    // (история, голос), тоже получает курсор в конце.
    var field by rememberSaveable(stateSaver = TextFieldValue.Saver) { mutableStateOf(TextFieldValue()) }
    val query = field.text
    fun setQuery(t: String) { field = TextFieldValue(t, TextRange(t.length)) }
    var submitted by rememberSaveable { mutableStateOf("") }
    var history by remember { mutableStateOf<List<String>>(emptyList()) }
    // Подсказки живут вместе с экраном в стеке: «Назад» с тайтла
    // возвращает тот же список, и пульт встаёт на открытую подсказку.
    var suggests by androidx.lifecycle.viewmodel.compose.viewModel<SearchSuggests>().items
    val focusMem = ru.sorewa.kino.ui.rememberTvFocusKey()
    ru.sorewa.kino.ui.TvFocusRestore(focusMem, ready = suggests.isNotEmpty() || submitted.isNotEmpty())
    val focus = remember { FocusRequester() }
    var okDownOnField by remember { mutableStateOf(false) }
    // ТВ: фокус истории — по таблетке на запрос, чтобы убранная долгим OK
    // передавала его соседке, а не терялась.
    val historyFocus = remember { mutableMapOf<String, FocusRequester>() }
    // Долгое OK убрало запрос: пока кнопку держат, пульт шлёт повторы — они
    // не должны нажать соседнюю таблетку, на которую перешёл фокус.
    var swallowOk by remember { mutableStateOf(false) }
    val context = LocalContext.current
    val focusManager = LocalFocusManager.current

    LaunchedEffect(Unit) {
        try { history = api.searchHistory().items } catch (_: Exception) { }
    }

    // Подсказки по мере набора: только наш каталог, без походов к площадкам.
    LaunchedEffect(query) {
        val q = query.trim()
        if (q.length < 2 || q == submitted) { suggests = emptyList(); return@LaunchedEffect }
        delay(250)
        try { suggests = api.suggest(q, 8).items } catch (_: Exception) { }
    }

    fun submit(q: String) {
        val t = q.trim()
        if (t.isEmpty()) return
        setQuery(t)
        submitted = t
        suggests = emptyList()
        keyboard?.hide()
        scope.launch { try { history = api.rememberSearch(t).items } catch (_: Exception) { } }
    }

    // Голосовой поиск (ТВ): системное распознавание, микрофон пульта.
    val voiceIntent = remember {
        val base = android.content.Intent(android.speech.RecognizerIntent.ACTION_RECOGNIZE_SPEECH)
            .putExtra(android.speech.RecognizerIntent.EXTRA_LANGUAGE_MODEL, android.speech.RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
            .putExtra(android.speech.RecognizerIntent.EXTRA_LANGUAGE, "ru-RU")
            .putExtra(android.speech.RecognizerIntent.EXTRA_PROMPT, "Что найти?")
        // На Android TV распознавателей два (ТВ-оболочка и движок речи) —
        // без явного выбора система спрашивает «чем открыть». Берём ТВ-шный.
        val tvVoice = android.content.Intent(base).setPackage("com.google.android.katniss")
        if (tvVoice.resolveActivity(context.packageManager) != null) tvVoice else base
    }
    val canVoice = remember { tv && voiceIntent.resolveActivity(context.packageManager) != null }
    val voice = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val said = r.data?.getStringArrayListExtra(android.speech.RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
        if (!said.isNullOrBlank()) submit(said)
    }

    // ТВ: поле ввода берёт фокус только в режиме набора. Иначе Android TV
    // открывал клавиатуру от одного попадания фокуса в поле — стрелкой,
    // после «Назад» с тайтла, после поиска с таблетки истории — и она
    // забирала себе пульт. Стрелки встают на рамку поля, OK на ней — набор.
    var editing by remember { mutableStateOf(false) }
    var boxFocused by remember { mutableStateOf(false) }
    var okDownOnBox by remember { mutableStateOf(false) }
    fun startEditing() {
        editing = true
        // Набор продолжается с конца текста — курсор на ТВ почти не виден.
        // Сюда же приходит OK по полю, когда клавиатуру закрыли «Назад»:
        // рамка видит фокус поля как свой. Куда бы ни увели курсор «◀ ▶»,
        // OK возвращает его в конец.
        field = field.copy(selection = TextRange(field.text.length))
        scope.launch {
            androidx.compose.runtime.withFrameNanos { }
            try { focus.requestFocus(); keyboard?.show() } catch (_: Exception) { }
        }
    }

    // ТВ: пришли в пустой поиск — сразу набор и клавиатура, как в Kino.Watch.
    LaunchedEffect(Unit) {
        if (tv && query.isEmpty()) startEditing()
    }

    Column(Modifier.fillMaxSize().then(if (tv) Modifier else Modifier.statusBarsPadding())) {
      Row(verticalAlignment = Alignment.CenterVertically) {
       val fieldShape = RoundedCornerShape(16.dp)
       Box(
            Modifier
                .weight(1f)
                .padding(horizontal = 16.dp, vertical = 10.dp)
                .then(
                    if (tv) Modifier
                        .then(if (boxFocused) Modifier.border(2.dp, androidx.compose.ui.graphics.Color.White, fieldShape) else Modifier)
                        .onFocusChanged { boxFocused = it.isFocused }
                        .onPreviewKeyEvent { e ->
                            if (!boxFocused) return@onPreviewKeyEvent false
                            val ok = e.key == Key.DirectionCenter || e.key == Key.Enter || e.key == Key.NumPadEnter
                            if (!ok) return@onPreviewKeyEvent false
                            if (e.type == KeyEventType.KeyDown && e.nativeKeyEvent.repeatCount == 0) okDownOnBox = true
                            if (e.type == KeyEventType.KeyUp && okDownOnBox) { okDownOnBox = false; startEditing() }
                            true
                        }
                        .focusable(enabled = !editing)
                    else Modifier,
                ),
        ) {
        TextField(
            value = field,
            onValueChange = {
                field = it
                if (it.text.isBlank()) submitted = ""
            },
            placeholder = { Text("Фильм, сериал, аниме…", color = TextFaint) },
            leadingIcon = { Icon(Icons.Rounded.Search, null, tint = TextDim) },
            trailingIcon = {
                if (query.isNotEmpty()) ru.sorewa.kino.ui.FocusIconButton(Icons.Rounded.Close, "Очистить", tint = TextDim) {
                    setQuery(""); submitted = ""
                    if (tv) startEditing() else focus.requestFocus()
                }
            },
            singleLine = true,
            shape = RoundedCornerShape(16.dp),
            colors = TextFieldDefaults.colors(
                focusedContainerColor = Surface2,
                unfocusedContainerColor = Surface2,
                focusedIndicatorColor = androidx.compose.ui.graphics.Color.Transparent,
                unfocusedIndicatorColor = androidx.compose.ui.graphics.Color.Transparent,
                cursorColor = Accent,
            ),
            // ТВ: клавиатура — только по OK на поле (и сама при входе в пустой
            // поиск). Иначе она выскакивала от одного фокуса — после «Назад»
            // с тайтла — и забирала себе пульт.
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search, showKeyboardOnFocus = if (tv) false else null),
            keyboardActions = KeyboardActions(onSearch = { submit(query) }),
            modifier = Modifier
                .fillMaxWidth()
                .then(
                    if (tv) Modifier
                        .focusProperties { canFocus = editing }
                        .onFocusChanged { if (!it.isFocused && !it.hasFocus && editing) editing = false }
                    else Modifier,
                )
                // ТВ: клавиатура — по ОТПУСКАНИЮ OK. По нажатию она успевала
                // открыться и принять отпускание той же кнопки — в поле
                // впечатывалась буква под курсором клавиатуры.
                // Стрелки пульта поле на ТВ не держит: иначе это ловушка —
                // курсор гуляет по тексту, а к истории, результатам и
                // микрофону с пульта не выйти. Текст правят на экранной
                // клавиатуре — её стрелки и Enter приходят сюда же и должны
                // достаться полю, а не увести, например, «←» в меню.
                .then(if (tv) Modifier.onPreviewKeyEvent { e ->
                    if (fromIme(e)) return@onPreviewKeyEvent false
                    val ok = e.key == Key.DirectionCenter || e.key == Key.Enter || e.key == Key.NumPadEnter
                    if (ok) {
                        // Только своё нажатие: отпускание OK, нажатого на другом
                        // элементе (долгое OK убрало таблетку истории, и фокус
                        // перескочил в поле), клавиатуру не открывает.
                        if (e.type == KeyEventType.KeyDown && e.nativeKeyEvent.repeatCount == 0) okDownOnField = true
                        if (e.type == KeyEventType.KeyUp && okDownOnField) { okDownOnField = false; keyboard?.show() }
                        return@onPreviewKeyEvent true
                    }
                    val dir = when (e.key) {
                        Key.DirectionDown -> FocusDirection.Down
                        Key.DirectionUp -> FocusDirection.Up
                        Key.DirectionLeft -> FocusDirection.Left
                        Key.DirectionRight -> FocusDirection.Right
                        else -> return@onPreviewKeyEvent false
                    }
                    if (e.type == KeyEventType.KeyDown) focusManager.moveFocus(dir)
                    true
                } else Modifier)
                .focusRequester(focus),
        )
       }
        if (canVoice) {
            VoiceButton(Modifier.padding(end = 16.dp)) {
                try { voice.launch(voiceIntent) } catch (_: Exception) { }
            }
        }
      }

        when {
            submitted.isNotEmpty() && query.trim() == submitted -> Results(nav, submitted, focusMem)
            suggests.isNotEmpty() -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = LocalBottomBarInset.current)) {
                items(suggests, key = { it.id }) { t ->
                    SuggestRow(t, Modifier.tvFocusKey(focusMem, "s${t.id}"), onOpen = { nav.openTitle(t) })
                }
                item {
                    ru.sorewa.kino.ui.FocusTextButton(
                        "Искать «${query.trim()}» везде",
                        modifier = Modifier.padding(horizontal = 12.dp, vertical = 4.dp),
                    ) { submit(query) }
                }
            }
            else -> Column(Modifier.padding(top = 4.dp)) {
                if (history.isNotEmpty()) {
                    Row(
                        Modifier.fillMaxWidth().padding(start = 20.dp, end = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text("Недавние", style = MaterialTheme.typography.titleMedium)
                        if (tv) {
                            Text(
                                "  ·  долгое нажатие OK — убрать запрос",
                                style = MaterialTheme.typography.bodySmall,
                                color = TextFaint,
                            )
                        }
                        Spacer(Modifier.weight(1f))
                        ru.sorewa.kino.ui.FocusTextButton("Очистить", color = TextDim) {
                            scope.launch { try { history = api.clearSearchHistory().items } catch (_: Exception) { } }
                        }
                    }
                    FlowRow(
                        Modifier
                            .padding(horizontal = 20.dp)
                            .onPreviewKeyEvent { e ->
                                val ok = e.key == Key.DirectionCenter || e.key == Key.Enter || e.key == Key.NumPadEnter
                                when {
                                    !ok || !swallowOk -> false
                                    // Новое нажатие — уже не хвост долгого.
                                    e.type == KeyEventType.KeyDown && e.nativeKeyEvent.repeatCount == 0 -> { swallowOk = false; false }
                                    else -> { if (e.type == KeyEventType.KeyUp) swallowOk = false; true }
                                }
                            },
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                        verticalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        history.forEach { h ->
                            fun forget() {
                                if (tv) {
                                    swallowOk = true
                                    val i = history.indexOf(h)
                                    val next = history.getOrNull(i + 1) ?: history.getOrNull(i - 1)
                                    next?.let { runCatching { historyFocus[it]?.requestFocus() } }
                                }
                                scope.launch { try { history = api.forgetSearch(h).items } catch (_: Exception) { } }
                            }
                            // ТВ: крестик внутри таблетки был отдельной невидимой целью
                            // фокуса — «вправо» вставало на него, OK стирал запрос.
                            // Там убирают долгим OK по самой таблетке.
                            Pill(
                                h,
                                selected = false,
                                leading = Icons.Rounded.History,
                                modifier = if (tv) Modifier.focusRequester(historyFocus.getOrPut(h) { FocusRequester() }) else Modifier,
                                onLongClick = if (tv) ::forget else null,
                                trailing = if (tv) null else ({
                                    Icon(
                                        Icons.Rounded.Close, "Убрать",
                                        tint = TextFaint,
                                        modifier = Modifier
                                            .size(18.dp)
                                            .clip(RoundedCornerShape(50))
                                            .clickable { forget() },
                                    )
                                }),
                            ) { submit(h) }
                        }
                    }
                } else {
                    EmptyState(
                        Icons.Rounded.Search,
                        "Что будем смотреть?",
                        "Ищите по названию на русском или в оригинале.",
                    )
                }
            }
        }
    }
}

/**
 * Нажатие пришло от экранной клавиатуры (её «◀»/«▶», Enter), а не с пульта.
 * Клавиатура шлёт их через поле ввода с пометкой FLAG_SOFT_KEYBOARD (Gboard
 * на ТВ — flags 0x6). У пульта, HDMI-CEC и adb её нет; deviceId и source по
 * ним не отличить: у adb и клавиатуры одинаковые -1 и KEYBOARD.
 */
private fun fromIme(e: androidx.compose.ui.input.key.KeyEvent): Boolean =
    e.nativeKeyEvent.flags and android.view.KeyEvent.FLAG_SOFT_KEYBOARD != 0

@Composable
private fun SuggestRow(t: Title, modifier: Modifier = Modifier, onOpen: () -> Unit) {
    val (name, original) = splitTitle(t.title)
    Row(
        modifier
            .fillMaxWidth()
            .tvFocusRing()
            .clickable(onClick = onOpen)
            .padding(horizontal = 20.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        PosterImage(
            t.id, t.hasArt, t.title,
            Modifier.width(44.dp).aspectRatio(2f / 3f).clip(RoundedCornerShape(8.dp)),
        )
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f)) {
            Text(name, style = MaterialTheme.typography.bodyLarge, color = TextMain, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val sub = listOf(original, kindSubtitle(t.kind, t.year, t.species)).filter { it.isNotEmpty() }.joinToString(" · ")
            Text(sub, style = MaterialTheme.typography.bodySmall, color = TextDim, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Icon(Icons.Rounded.NorthWest, null, tint = TextFaint, modifier = Modifier.size(18.dp))
    }
}

@Composable
private fun Results(nav: NavHostController, q: String, focusMem: ru.sorewa.kino.ui.TvFocusKey) {
    val api = remember { KinoApp.instance.container.api }
    val tv = LocalTv.current
    val paged = rememberPaged(q) { off -> api.search(q, off) }
    val items = paged.items
    // Прокрутка переживает уход на тайтл (вместе с кешем выдачи в rememberPaged).
    val gridState = androidx.compose.runtime.saveable.rememberSaveable(q, saver = LazyGridState.Saver) { LazyGridState() }
    when {
        paged.error.isNotEmpty() && items == null -> ErrorView(paged.error) { paged.reload() }
        items == null -> PosterGridSkeleton()
        items.isEmpty() -> EmptyState(Icons.Rounded.SearchOff, "Ничего не нашлось", "Попробуйте другое написание или оригинальное название.")
        else -> LazyVerticalGrid(
            state = gridState,
            columns = posterCells(),
            contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 4.dp, bottom = 24.dp + LocalBottomBarInset.current),
            verticalArrangement = Arrangement.spacedBy(18.dp),
            horizontalArrangement = Arrangement.spacedBy(if (tv) 20.dp else 12.dp),
            modifier = Modifier.fillMaxSize().tvFocusMemory().tvGridNav(gridState),
        ) {
            item(span = { GridItemSpan(maxLineSpan) }) {
                Box(Modifier.padding(bottom = 4.dp)) {
                    Text(
                        if (paged.total > 0) "Найдено: ${paged.total}" else "Результаты",
                        style = MaterialTheme.typography.bodyMedium,
                        color = TextDim,
                    )
                }
            }
            itemsIndexed(items, key = { _, t -> t.id }) { idx, t ->
                TitleCard(title = t, modifier = Modifier.tvFocusKey(focusMem, t.id), onClick = { nav.openTitle(t) })
                if (idx >= items.size - 6) LaunchedEffect(items.size) { paged.more() }
            }
            if (paged.loading) item(span = { GridItemSpan(maxLineSpan) }) { LoadMore(items.size, false, true) {} }
        }
    }
}


/** Кнопка голосового поиска на ТВ: круг, в фокусе — белый. */
@Composable
private fun VoiceButton(modifier: Modifier = Modifier, onClick: () -> Unit) {
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    Box(
        modifier
            .size(56.dp)
            .clip(CircleShape)
            .background(if (focused) androidx.compose.ui.graphics.Color.White else Surface2)
            .clickable(interactionSource = source, indication = null, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            Icons.Rounded.Mic, "Голосовой поиск",
            tint = if (focused) androidx.compose.ui.graphics.Color.Black else TextDim,
            modifier = Modifier.size(28.dp),
        )
    }
}

/** Подсказки поиска — в ViewModel экрана, чтобы пережить уход на тайтл. */
class SearchSuggests : androidx.lifecycle.ViewModel() {
    val items = mutableStateOf<List<Title>>(emptyList())
}
