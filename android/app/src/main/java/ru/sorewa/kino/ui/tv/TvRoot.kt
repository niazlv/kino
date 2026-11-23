package ru.sorewa.kino.ui.tv

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Bookmarks
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.Explore
import androidx.compose.material.icons.rounded.Home
import androidx.compose.material.icons.rounded.Person
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.withFrameNanos
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.ui.DeviceCodeLogin
import ru.sorewa.kino.ui.DeviceCodeSteps
import ru.sorewa.kino.ui.OfflineBanner
import ru.sorewa.kino.ui.Profile
import ru.sorewa.kino.ui.ProvideTv
import ru.sorewa.kino.ui.phone.BrandMark
import ru.sorewa.kino.ui.phone.CatalogScreen
import ru.sorewa.kino.ui.phone.DownloadsScreen
import ru.sorewa.kino.ui.phone.HistoryScreen
import ru.sorewa.kino.ui.phone.MineScreen
import ru.sorewa.kino.ui.phone.SearchScreen
import ru.sorewa.kino.ui.phone.SettingsScreen
import ru.sorewa.kino.ui.runAdopt
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Bg
import ru.sorewa.kino.ui.theme.Surface1
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextMain

private data class TvItem(val route: String, val label: String, val icon: ImageVector)

private val tvItems = listOf(
    TvItem("search", "Поиск", Icons.Rounded.Search),
    TvItem("home", "Главная", Icons.Rounded.Home),
    TvItem("catalog", "Каталог", Icons.Rounded.Explore),
    TvItem("mine", "Моё", Icons.Rounded.Bookmarks),
    TvItem("downloads", "Загрузки", Icons.Rounded.Download),
    TvItem("settings", "Профиль", Icons.Rounded.Person),
)

/**
 * ТВ-корень: меню слева, которое раскрывается, когда на него приходит
 * фокус, и экраны во всю ширину. Вход — по коду с телефона.
 */
@Composable
fun TvRoot(authorized: Boolean) = ProvideTv(true) {
    val container = remember { KinoApp.instance.container }
    var sessionOk by remember { mutableStateOf(authorized) }
    var offline by remember { mutableStateOf(false) }
    var checkKey by remember { mutableIntStateOf(0) }
    LaunchedEffect(authorized) { sessionOk = authorized }
    // Своей сессии нет — переносим вход старого приложения (cookie WebView).
    // Сервер не ответил — офлайн-витрина, «Повторить» пробует снова.
    var adopting by remember { mutableStateOf(!authorized) }
    var adoptKey by remember { mutableIntStateOf(0) }
    LaunchedEffect(adoptKey) {
        if (!sessionOk && container.onBuiltInServer) {
            val r = try {
                container.auth.adoptWebViewSession(KinoApp.instance, ru.sorewa.kino.BuildConfig.BASE_URL)
            } catch (_: Exception) {
                ru.sorewa.kino.data.AuthRepository.Adopt.NONE
            }
            when (r) {
                ru.sorewa.kino.data.AuthRepository.Adopt.ADOPTED -> sessionOk = true
                ru.sorewa.kino.data.AuthRepository.Adopt.OFFLINE -> offline = true
                ru.sorewa.kino.data.AuthRepository.Adopt.NONE -> {}
            }
        }
        adopting = false
    }
    val retry = {
        checkKey++
        // Без входа «Повторить» возвращает к переносу и экрану входа.
        if (!sessionOk) { offline = false; adopting = true; adoptKey++ }
    }

    // Сессия есть, но сервер не отвечает — офлайн-витрина скачанного.
    LaunchedEffect(sessionOk, checkKey) {
        if (!sessionOk) return@LaunchedEffect
        try {
            Profile.me = container.api.me()
            offline = false
        } catch (e: ru.sorewa.kino.data.ApiError) {
            if (e.offline) offline = true
            else if (e.sessionDead) { container.auth.logout(); sessionOk = false }
        } catch (_: Exception) { }
    }

    when {
        // Сборка без вшитого сервера и сервер ещё не выбран — сначала он.
        container.connection.needsServer -> ru.sorewa.kino.ui.phone.ServerSetupScreen()
        offline -> {
            LaunchedEffect(Unit) { runAdopt() }
            Column(Modifier.fillMaxSize().padding(horizontal = 32.dp, vertical = 16.dp)) {
                OfflineBanner(onRetry = retry)
                DownloadsScreen(null, offline = true)
            }
        }
        adopting && !sessionOk -> ru.sorewa.kino.ui.CenterSpinner()
        !sessionOk -> TvLoginScreen(onDone = { sessionOk = true; checkKey++ }, onOffline = { offline = true })
        else -> TvShell()
    }
}

@OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)
@Composable
private fun TvShell() {
    val nav = rememberNavController()
    val entry by nav.currentBackStackEntryAsState()
    val route = entry?.destination?.route
    var navFocused by remember { mutableStateOf(false) }
    val requesters = remember { tvItems.associate { it.route to androidx.compose.ui.focus.FocusRequester() } }
    val selectedRoute = remember { mutableStateOf("home") }
    // «Загрузки» — только если на телевизоре что-то скачано: скачать с ТВ
    // нельзя (на странице тайтла нет «Скачать»), и пустой раздел был
    // тупиком с системным запросом доступа к видео.
    var hasDownloads by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        hasDownloads = kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
            try { KinoApp.instance.container.library.list().isNotEmpty() } catch (_: Exception) { false }
        }
    }
    val menu = tvItems.filter { it.route != "downloads" || hasDownloads }
    val width by animateDpAsState(if (navFocused) 236.dp else 84.dp, tween(200), label = "rail")

    // Фокус живёт в экране; в меню — только стрелкой влево с края экрана.
    // Когда фокус пропадает (переход, загрузка, назад), Compose отдаёт его
    // первому, что попадётся, — меню; оттуда возвращаем в экран, как только
    // в нём появится, на что встать.
    val contentFocus = remember { androidx.compose.ui.focus.FocusRequester() }
    var contentFocused by remember { mutableStateOf(false) }
    var keySeq by remember { mutableIntStateOf(0) }
    var lastKey by remember { mutableStateOf<Key?>(null) }
    val scope = rememberCoroutineScope()
    fun focusContent(afterNav: Boolean) {
        scope.launch {
            val seq = keySeq
            if (afterNav) delay(250) // старый экран ещё гаснет — не встать на него
            repeat(100) { // до ~10 с: экран может грузиться с сервера
                if (keySeq != seq) return@launch // человек сам ходит по меню
                withFrameNanos { }
                // Экран уже сам поставил фокус (вернул на карточку, «Смотреть») —
                // не перебивать его первым элементом.
                if (contentFocused) return@launch
                val ok = try { contentFocus.requestFocus(FocusDirection.Enter) } catch (_: Exception) { false }
                if (ok && contentFocused) return@launch
                delay(100)
            }
        }
    }

    // Карточка канала «Новые серии» на главном экране Android TV открывает тайтл.
    val linkedTitle by ru.sorewa.kino.tvhome.TvLinks.openTitle.collectAsState()
    LaunchedEffect(linkedTitle) {
        val id = linkedTitle ?: return@LaunchedEffect
        ru.sorewa.kino.tvhome.TvLinks.openTitle.value = null
        if (id > 0) nav.navigate("title/$id") { launchSingleTop = true }
    }

    // «Назад» на главной не закрывает приложение сразу: сначала меню (на
    // «Главной»), из меню — выход: одно лишнее нажатие не должно выкидывать
    // на экран телевизора.
    val backToMenu = remember { booleanArrayOf(false) }
    androidx.activity.compose.BackHandler(enabled = route == "home" && !navFocused) {
        backToMenu[0] = true
        val ok = try { requesters.getValue("home").requestFocus() } catch (_: Exception) { false }
        if (!ok) backToMenu[0] = false
    }

    Box(
        Modifier
            .fillMaxSize()
            .background(Bg)
            .onPreviewKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                keySeq++
                lastKey = e.key
                // Фокус потерян совсем — стрелка возвращает его в экран, а не в меню.
                if (!navFocused && !contentFocused && e.key in arrows) {
                    try { contentFocus.requestFocus(FocusDirection.Enter) } catch (_: Exception) { false }
                } else false
            },
    ) {
        // Экран — под меню: свёрнутое меню занимает узкую полосу слева.
        Box(
            Modifier
                .fillMaxSize()
                .padding(start = 84.dp)
                .onFocusChanged { contentFocused = it.hasFocus }
                .focusRequester(contentFocus)
                // Из экрана наружу — только влево, в меню. Вверх с первого ряда
                // и прочие края фокус не уводят.
                .focusProperties {
                    @Suppress("DEPRECATION")
                    exit = { dir ->
                        when {
                            // «Назад» на главной сам переводит фокус в меню — пропустить.
                            backToMenu[0] -> androidx.compose.ui.focus.FocusRequester.Default
                            // Влево — сразу на текущий раздел меню, а не на ближайший по высоте пункт.
                            dir == FocusDirection.Left -> requesters[selectedRoute.value] ?: androidx.compose.ui.focus.FocusRequester.Default
                            else -> androidx.compose.ui.focus.FocusRequester.Cancel
                        }
                    }
                }
                .focusGroup(),
        ) {
            ProvideEdgeScroll {
            NavHost(
                navController = nav,
                startDestination = "home",
                enterTransition = { fadeIn(tween(200)) },
                exitTransition = { fadeOut(tween(120)) },
            ) {
                composable("home") { TvHome(nav) }
                composable("catalog") { TvPage { CatalogScreen(nav) } }
                composable("search") { TvPage { SearchScreen(nav) } }
                composable(
                    "mine?tab={tab}",
                    arguments = listOf(navArgument("tab") { type = NavType.IntType; defaultValue = 0 }),
                ) { e -> TvPage { MineScreen(nav, e.arguments?.getInt("tab") ?: 0) } }
                composable("downloads") { TvPage { DownloadsScreen(nav) } }
                composable("history") { TvPage { HistoryScreen(nav) } }
                composable("settings") { TvPage { SettingsScreen(nav) } }
                composable(
                    "title/{id}",
                    arguments = listOf(navArgument("id") { type = NavType.LongType }),
                ) { e -> TvTitle(nav, e.arguments?.getLong("id") ?: 0) }
            }
            }
        }

        // Меню поверх: раскрывается с затемнением, пока в нём фокус.
        Column(
            Modifier
                .fillMaxHeight()
                .width(width)
                .background(
                    if (navFocused) Brush.horizontalGradient(listOf(Color(0xF20A0C10), Color(0xE60A0C10)))
                    else Brush.horizontalGradient(listOf(Bg, Bg)),
                )
                .onFocusChanged {
                    val was = navFocused
                    navFocused = it.hasFocus
                    // В меню попали не стрелкой влево — фокус потерялся, вернуть в экран.
                    // «Назад» на главной открывает меню намеренно — его не возвращаем.
                    if (it.hasFocus && !was && lastKey != Key.DirectionLeft && !backToMenu[0]) focusContent(afterNav = false)
                    if (it.hasFocus) backToMenu[0] = false
                }
                // Вход в меню с экрана — на текущий раздел, а не на ближайший по геометрии пункт.
                .focusProperties {
                    @Suppress("DEPRECATION")
                    enter = { requesters[selectedRoute.value] ?: androidx.compose.ui.focus.FocusRequester.Default }
                }
                .focusGroup()
                .padding(vertical = 28.dp, horizontal = 14.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Box(Modifier.padding(start = 6.dp, bottom = 26.dp).height(40.dp), contentAlignment = Alignment.CenterStart) {
                if (navFocused) BrandMark() else ru.sorewa.kino.ui.phone.BrandMarkSmall()
            }
            val section = nav.currentBackStack.value.mapNotNull { it.destination.route }
                .lastOrNull { r -> tvItems.any { it.route == r.substringBefore('?') } }?.substringBefore('?')
                ?: route?.substringBefore('?')
            selectedRoute.value = menu.firstOrNull { it.route == section }?.route ?: "home"
            menu.forEach { item ->
                TvNavItem(item, selected = section == item.route, expanded = navFocused, modifier = Modifier.focusRequester(requesters.getValue(item.route))) {
                    nav.navigate(item.route) {
                        popUpTo(nav.graph.findStartDestination().id) { saveState = true }
                        launchSingleTop = true
                        restoreState = true
                    }
                    // Раздел выбран — меню закрывается, фокус уходит в экран.
                    focusContent(afterNav = true)
                }
            }
        }
    }
}

private val arrows = setOf(Key.DirectionUp, Key.DirectionDown, Key.DirectionLeft, Key.DirectionRight)

/** Списки экранов едут за фокусом только у края — см. [ru.sorewa.kino.ui.edgeBringIntoView]. */
@Composable
private fun ProvideEdgeScroll(content: @Composable () -> Unit) = ru.sorewa.kino.ui.EdgeScroll(56.dp, content)

/** Отступы от краёв экрана: телевизоры обрезают края (overscan). */
@Composable
fun TvPage(content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize().padding(start = 24.dp, end = 40.dp, top = 20.dp)) { content() }
}

@Composable
private fun TvNavItem(item: TvItem, selected: Boolean, expanded: Boolean, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val source = remember { MutableInteractionSource() }
    val focused by source.collectIsFocusedAsState()
    val shape = RoundedCornerShape(14.dp)
    Row(
        modifier
            .fillMaxWidth()
            .height(52.dp)
            .clip(shape)
            .background(
                when {
                    focused -> Color.White
                    selected && expanded -> Surface1
                    else -> Color.Transparent
                },
            )
            .clickable(interactionSource = source, indication = null, onClick = onClick)
            .padding(horizontal = 16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            item.icon, item.label,
            tint = when {
                focused -> Color.Black
                selected -> Accent
                else -> TextDim
            },
            modifier = Modifier.size(26.dp),
        )
        AnimatedVisibility(expanded, enter = fadeIn(tween(150)), exit = fadeOut(tween(80))) {
            Text(
                item.label,
                style = MaterialTheme.typography.titleMedium,
                color = if (focused) Color.Black else TextMain,
                maxLines = 1,
                modifier = Modifier.padding(start = 18.dp),
            )
        }
    }
}

/**
 * Вход на ТВ: приложение просит код у сервера и показывает его вместе с QR.
 * Одобряется с телефона («Профиль → Подключить устройство») или на сайте —
 * на пульте набирать пароль не надо.
 */
@Composable
fun TvLoginScreen(onDone: () -> Unit, onOffline: () -> Unit = {}) {
    val container = remember { KinoApp.instance.container }
    var picker by remember { mutableStateOf(false) }
    if (picker) ru.sorewa.kino.ui.phone.ServerPickerDialog { picker = false }
    Box(
        Modifier
            .fillMaxSize()
            .background(
                Brush.radialGradient(
                    listOf(Accent.copy(alpha = 0.16f), Color.Transparent),
                    center = androidx.compose.ui.geometry.Offset(0f, 0f),
                    radius = 1600f,
                ),
            )
            .padding(horizontal = 72.dp, vertical = 48.dp),
    ) {
        Row(Modifier.fillMaxSize(), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                BrandMark(big = true)
                Spacer(Modifier.height(36.dp))
                Text("Вход на телевизоре", style = MaterialTheme.typography.displaySmall, color = TextMain)
                Spacer(Modifier.height(12.dp))
                Text(
                    "Пароль на пульте набирать не нужно — подтвердите вход с телефона.",
                    style = MaterialTheme.typography.titleMedium,
                    color = TextDim,
                )
                Spacer(Modifier.height(32.dp))
                DeviceCodeSteps()
            }
            Spacer(Modifier.width(48.dp))
            DeviceCodeLogin(onDone = onDone, big = true, onOffline = onOffline, modifier = Modifier.weight(0.9f))
        }
        // Телевизор и телефон должны смотреть на один сервер: код подтверждают
        // там, где выдали. В углу, а не под шагами: в левой колонке на экране
        // 960×540 dp места уже нет.
        ru.sorewa.kino.ui.phone.ServerChip(
            "Сервер: " + ru.sorewa.kino.ui.phone.serverLabel(container.serverUrl, container.connection.serverName()),
            onClick = { picker = true },
            modifier = Modifier.align(Alignment.TopEnd),
        )
    }
}
