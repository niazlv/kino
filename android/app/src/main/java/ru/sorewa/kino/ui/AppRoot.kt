package ru.sorewa.kino.ui

import androidx.compose.foundation.background
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.spring
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.selection.selectable
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.layout.layout
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.Constraints
import kotlinx.coroutines.launch
import ru.sorewa.kino.ui.theme.KinoMotion
import kotlin.math.roundToInt
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Bookmarks
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Explore
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.rounded.Bookmarks
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.Explore
import androidx.compose.material.icons.rounded.Home
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp
import androidx.navigation.NamedNavArgument
import androidx.navigation.NavBackStackEntry
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.ApiError
import ru.sorewa.kino.ui.phone.CatalogScreen
import ru.sorewa.kino.ui.phone.DownloadsScreen
import ru.sorewa.kino.ui.phone.HistoryScreen
import ru.sorewa.kino.ui.phone.HomeScreen
import ru.sorewa.kino.ui.phone.LinkDeviceScreen
import ru.sorewa.kino.ui.phone.LoginScreen
import ru.sorewa.kino.ui.phone.MineScreen
import ru.sorewa.kino.ui.phone.SearchScreen
import ru.sorewa.kino.ui.phone.SettingsScreen
import ru.sorewa.kino.ui.phone.TitleScreen
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.NavMotion
import ru.sorewa.kino.ui.theme.NavPage
import ru.sorewa.kino.ui.theme.NavShift
import ru.sorewa.kino.ui.theme.Surface1
import ru.sorewa.kino.ui.theme.Surface2
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextMain

private data class BottomItem(
    val route: String,
    val label: String,
    val icon: ImageVector,
    val iconOn: ImageVector,
)

private val bottomItems = listOf(
    BottomItem("home", "Главная", Icons.Outlined.Home, Icons.Rounded.Home),
    BottomItem("catalog", "Каталог", Icons.Outlined.Explore, Icons.Rounded.Explore),
    BottomItem("search", "Поиск", Icons.Outlined.Search, Icons.Rounded.Search),
    BottomItem("mine?tab={tab}", "Моё", Icons.Outlined.Bookmarks, Icons.Rounded.Bookmarks),
    BottomItem("downloads", "Загрузки", Icons.Outlined.Download, Icons.Rounded.Download),
)


/**
 * Корень приложения. Три состояния: офлайн (скачанное смотрится без сети
 * и без входа), вход, навигация. Сохранённая сессия проверяется одним /me:
 * сервер её не признал (и refresh не спас) — показываем вход.
 */
@Composable
fun AppRoot(authorized: Boolean) {
    val container = remember { KinoApp.instance.container }
    var validSession by remember(authorized) { mutableStateOf(authorized) }
    var offline by remember { mutableStateOf(false) }
    var checkKey by remember { mutableIntStateOf(0) }

    // Своей сессии нет — сперва пробуем перенести вход из старого
    // приложения (cookie WebView), и только потом показываем экран входа.
    // Сервер не ответил — офлайн-витрина, «Повторить» пробует снова.
    var adopting by remember { mutableStateOf(!authorized) }
    var adoptKey by remember { mutableIntStateOf(0) }
    LaunchedEffect(adoptKey) {
        // Старая оболочка жила только на вшитом сервере: у выбранного вручную
        // и у локального движка переносить нечего.
        if (!authorized && container.onBuiltInServer) {
            val r = try {
                container.auth.adoptWebViewSession(KinoApp.instance, ru.sorewa.kino.BuildConfig.BASE_URL)
            } catch (_: Exception) {
                ru.sorewa.kino.data.AuthRepository.Adopt.NONE
            }
            if (r == ru.sorewa.kino.data.AuthRepository.Adopt.OFFLINE) offline = true
        }
        adopting = false
    }
    val retry = {
        checkKey++
        // Без входа «Повторить» возвращает к переносу и экрану входа —
        // иначе из скачанного, открытого со входа, было не выбраться.
        if (!authorized) { offline = false; adopting = true; adoptKey++ }
    }

    LaunchedEffect(authorized, checkKey) {
        if (!authorized) {
            validSession = false
            // Без сессии офлайн-витрина открывается из входа («Смотреть
            // скачанное»), здесь ничего проверять нечего.
            return@LaunchedEffect
        }
        // Сети нет вовсе (режим полёта) — сразу скачанное, без ожидания сервера.
        if (!container.local && !ru.sorewa.kino.util.hasNetwork()) {
            offline = true
            validSession = true
            return@LaunchedEffect
        }
        try {
            Profile.me = container.api.me()
            validSession = true
            offline = false
            // Сеть есть — скачанное к офлайну: карточки, кадры, постеры.
            container.warmUpOffline()
            // Недокачанное (очередь, ждущий повтор, готовое от оборвавшегося
            // прогона) — служба доделает, не дожидаясь «Загрузок».
            kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                ru.sorewa.kino.downloader.DownloadService.restoreFailed(KinoApp.instance)
                ru.sorewa.kino.downloader.DownloadService.kickIfNeeded(KinoApp.instance)
            }
        } catch (e: ApiError) {
            when {
                e.offline -> { offline = true; validSession = true }
                // 401 или 410 `expired`, и продление не помогло — на вход.
                // Локальный аккаунт на вход не уводим: его сессия — токен движка,
                // и если движок её не признал, ставим её заново.
                e.sessionDead && container.local -> { container.reassertLocalSession(); validSession = true }
                e.sessionDead -> { container.auth.logout(); validSession = false }
                else -> validSession = true
            }
        } catch (_: Exception) {
            validSession = true
        }
    }

    when {
        // Сборка без вшитого сервера и сервер ещё не выбран — сначала он.
        container.connection.needsServer -> ru.sorewa.kino.ui.phone.ServerSetupScreen()
        // Нет сети: скачанное доступно всегда, вход не нужен.
        offline -> OfflineRoot(onRetry = retry)
        adopting && !authorized -> CenterSpinner()
        !validSession || !authorized -> LoginScreen(
            onDone = { validSession = true },
            onOfflineDownloads = { offline = true },
        )
        else -> NavRoot()
    }
}

/**
 * Офлайн-витрина: скачанное по тайтлам и страницы тайтлов из сохранённых
 * карточек — как онлайн, только играет с телефона. Плашка «нет связи» с
 * повтором — над списком; страница тайтла рисуется во весь экран, как онлайн.
 */
@Composable
fun OfflineRoot(onRetry: () -> Unit) {
    // Adopt — локальная работа, сети не требует: подбираем чужие файлы и офлайном.
    LaunchedEffect(Unit) { runAdopt() }
    val nav = rememberNavController()
    // Без нижней панели: системная полоска жестов не должна лечь на список.
    Box(Modifier.fillMaxSize().navigationBarsPadding()) {
    val isTop: (String?) -> Boolean = { it == "downloads" }
    val offRoute = nav.currentBackStackEntryAsState().value?.destination?.route
    val density = LocalDensity.current
    PredictiveBackHost(Modifier.fillMaxSize()) { back ->
    val shift = with(density) { NavShift.roundToPx() }
    val motion = remember(back, shift) { NavMotion(isTop, back, shift) }
    NavHost(
        navController = nav,
        startDestination = "downloads",
        modifier = Modifier.swipeBack(UiPrefs.swipeBack && offRoute != null && !isTop(offRoute)),
        enterTransition = motion.enter,
        exitTransition = motion.exit,
        popEnterTransition = motion.popEnter,
        popExitTransition = motion.popExit,
    ) {
        page("downloads") {
            Column(Modifier.fillMaxSize()) {
                OfflineBanner(onRetry, Modifier.statusBarsPadding())
                DownloadsScreen(nav, offline = true)
            }
        }
        page(
            "title/{id}",
            arguments = listOf(navArgument("id") { type = NavType.LongType }),
        ) { entry -> TitleScreen(nav, entry.arguments?.getLong("id") ?: 0, offlineOnly = true) }
        localTitleRoute(nav)
    }
    }
    }
}

/**
 * Экран телефона: непрозрачный фон и скругление углов на уходе (см. [NavPage]).
 * ТВ-маршруты (TvRoot) этим не пользуются — там свои переходы под пульт.
 */
fun NavGraphBuilder.page(
    route: String,
    arguments: List<NamedNavArgument> = emptyList(),
    content: @Composable (NavBackStackEntry) -> Unit,
) = composable(route, arguments) { entry -> NavPage { content(entry) } }

/** Скачанное без тайтла в каталоге: страница по названию папки. */
fun NavGraphBuilder.localTitleRoute(nav: NavHostController) {
    page(
        "local?name={name}",
        arguments = listOf(navArgument("name") { type = NavType.StringType; defaultValue = "" }),
    ) { entry -> TitleScreen(nav, 0, localName = entry.arguments?.getString("name").orEmpty()) }
}

@Composable
fun OfflineBanner(onRetry: () -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 10.dp)
            .background(Surface2, RoundedCornerShape(16.dp))
            .padding(start = 16.dp, end = 6.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Rounded.CloudOff, null, tint = TextDim, modifier = Modifier.size(22.dp))
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text("Нет связи с сервером", style = MaterialTheme.typography.titleSmall, color = TextMain)
            Text("Скачанное смотрится и так", style = MaterialTheme.typography.bodySmall, color = TextDim)
        }
        SecondaryButton("Повторить", onClick = onRetry)
    }
}

@Composable
private fun NavRoot() {
    val nav = rememberNavController()
    val backStack by nav.currentBackStackEntryAsState()
    val route = backStack?.destination?.route
    val glass = rememberGlassSource()
    val density = LocalDensity.current
    var barHeight by remember { mutableStateOf(0.dp) }
    val isTop: (String?) -> Boolean = { r -> bottomItems.any { it.route == r } }
    // Экраны — во всю высоту: нижняя панель стеклянная и лежит поверх них,
    // под ней просвечивает прокрутка. Списки сами отступают снизу на её
    // высоту (LocalBottomBarInset), чтобы последний ряд не прятался под ней.
    // Тайтл рисуется и под строкой состояния; панель внизу остаётся на всех
    // экранах — с тайтла можно сразу уйти в другой раздел.
    Box(Modifier.fillMaxSize()) {
        CompositionLocalProvider(LocalBottomBarInset provides barHeight) {
            PredictiveBackHost(Modifier.fillMaxSize()) { back ->
                val shift = with(density) { NavShift.roundToPx() }
                val motion = remember(back, shift) { NavMotion(isTop, back, shift) }
                NavHost(
                    navController = nav,
                    startDestination = "home",
                    modifier = Modifier
                        .fillMaxSize()
                        .glassSource(glass)
                        .swipeBack(UiPrefs.swipeBack && route != null && !isTop(route)),
                    enterTransition = motion.enter,
                    exitTransition = motion.exit,
                    popEnterTransition = motion.popEnter,
                    popExitTransition = motion.popExit,
                ) {
                    phoneGraph(nav)
                }
            }
        }
        val barModifier = Modifier
            .align(Alignment.BottomCenter)
            .onSizeChanged { barHeight = with(density) { it.height.toDp() } }
        if (UiPrefs.capsuleBar) CapsuleBar(nav, route, glass, barModifier)
        else BottomBar(nav, route, glass, barModifier)
    }
}

/** Маршруты телефона (у ТВ свой граф в TvRoot). */
fun NavGraphBuilder.phoneGraph(nav: NavHostController) {
    page("home") { HomeScreen(nav) }
    page("catalog") { CatalogScreen(nav) }
    page("search") { SearchScreen(nav) }
    page(
        "mine?tab={tab}",
        arguments = listOf(navArgument("tab") { type = NavType.IntType; defaultValue = 0 }),
    ) { entry -> MineScreen(nav, entry.arguments?.getInt("tab") ?: 0) }
    page("downloads") { DownloadsScreen(nav) }
    page("history") { HistoryScreen(nav) }
    page("settings") { SettingsScreen(nav) }
    page("link") { LinkDeviceScreen(nav) }
    page("engine") { ru.sorewa.kino.ui.phone.EngineSettingsScreen(nav) }
    page(
        "title/{id}",
        arguments = listOf(navArgument("id") { type = NavType.LongType }),
    ) { entry ->
        TitleScreen(nav, entry.arguments?.getLong("id") ?: 0)
    }
    localTitleRoute(nav)
}

/**
 * Подсвечен раздел, из которого открыт текущий экран (тайтл, история…):
 * ближайший к вершине стека пункт нижней панели. [current] — только чтобы
 * пересчитать при смене экрана.
 */
private fun currentSection(nav: NavHostController, @Suppress("UNUSED_PARAMETER") current: String?): String? =
    nav.currentBackStack.value
        .mapNotNull { it.destination.route }
        .lastOrNull { r -> bottomItems.any { it.route == r } }

private fun openSection(nav: NavHostController, item: BottomItem) {
    nav.navigate(item.route.substringBefore('?')) {
        popUpTo(nav.graph.findStartDestination().id) { saveState = true }
        launchSingleTop = true
        restoreState = true
    }
}

/** Панель во всю ширину, до низа экрана — как системные панели Android 17 (Material 3). */
@Composable
private fun BottomBar(nav: NavHostController, current: String?, glass: GlassSource, modifier: Modifier = Modifier) {
    val section = currentSection(nav, current)
    // Стекло и под полоской жестов. Вместо сплошной черты сверху — тонкий
    // светлый край стекла.
    Column(modifier.glass(glass, tint = Surface1.copy(alpha = 0.5f), fallback = Surface1, rim = false)) {
        HorizontalDivider(color = Color.White.copy(alpha = 0.07f), thickness = 1.dp)
        NavigationBar(containerColor = Color.Transparent, tonalElevation = 0.dp) {
            bottomItems.forEach { item ->
                val selected = section == item.route
                NavigationBarItem(
                    selected = selected,
                    onClick = { openSection(nav, item) },
                    icon = { Icon(if (selected) item.iconOn else item.icon, contentDescription = item.label) },
                    label = { Text(item.label, style = MaterialTheme.typography.labelSmall) },
                    colors = NavigationBarItemDefaults.colors(
                        selectedIconColor = Accent,
                        selectedTextColor = Accent,
                        indicatorColor = Accent.copy(alpha = 0.14f),
                        unselectedIconColor = TextDim,
                        unselectedTextColor = TextDim,
                    ),
                )
            }
        }
    }
}

/**
 * Нижняя панель плавающей капсулой, как в iOS 26: стекло с бликом по краю,
 * над полоской жестов и с отступами по бокам — прокрутка видна и вокруг неё.
 * Выбранный раздел — светлая «капля» под значком. При переходе она перетекает:
 * передний край бежит быстрее заднего, капля на ходу вытягивается и
 * собирается обратно на новом месте.
 */
@Composable
private fun CapsuleBar(nav: NavHostController, current: String?, glass: GlassSource, modifier: Modifier = Modifier) {
    val section = currentSection(nav, current)
    val index = bottomItems.indexOfFirst { it.route == section }
    val shape = RoundedCornerShape(percent = 50)
    Box(
        modifier
            .fillMaxWidth()
            .navigationBarsPadding()
            .padding(start = 16.dp, end = 16.dp, bottom = 10.dp),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            Modifier
                .widthIn(max = 520.dp)
                .fillMaxWidth()
                .height(64.dp)
                .shadow(20.dp, shape, ambientColor = Color.Black, spotColor = Color.Black)
                .glass(glass, shape, tint = Surface1.copy(alpha = 0.45f), fallback = Surface1, blur = 24.dp)
                .padding(4.dp),
        ) {
            Drop(index, bottomItems.size)
            Row(Modifier.fillMaxSize()) {
                bottomItems.forEachIndexed { i, item ->
                    CapsuleItem(item, i == index, Modifier.weight(1f)) { openSection(nav, item) }
                }
            }
        }
    }
}

/** «Капля» выбранного раздела: края — две пружины разной жёсткости. */
@Composable
private fun Drop(index: Int, count: Int) {
    val left = remember { Animatable(0f) }
    val right = remember { Animatable(0f) }
    var cell by remember { mutableStateOf(0f) }
    val placedCell = remember { floatArrayOf(0f) }
    LaunchedEffect(index, cell) {
        if (index < 0 || cell <= 0f) return@LaunchedEffect
        val l = index * cell
        val r = l + cell
        // Первое появление и смена ширины (поворот экрана) — сразу на место.
        if (placedCell[0] != cell) {
            placedCell[0] = cell
            left.snapTo(l)
            right.snapTo(r)
            return@LaunchedEffect
        }
        val toRight = l > left.value
        val lead = spring<Float>(dampingRatio = 0.7f, stiffness = 900f)
        val trail = spring<Float>(dampingRatio = 0.78f, stiffness = 300f)
        launch { left.animateTo(l, if (toRight) trail else lead) }
        launch { right.animateTo(r, if (toRight) lead else trail) }
    }
    if (index < 0) return
    val shape = RoundedCornerShape(percent = 50)
    Box(
        Modifier
            .fillMaxSize()
            .layout { m, c ->
                cell = c.maxWidth.toFloat() / count
                val l = left.value
                val w = (right.value - l).roundToInt().coerceIn(0, c.maxWidth)
                val p = m.measure(Constraints.fixed(w, c.maxHeight))
                layout(c.maxWidth, c.maxHeight) { p.place(l.roundToInt(), 0) }
            }
            .background(Color.White.copy(alpha = 0.10f), shape)
            .border(
                1.dp,
                Brush.verticalGradient(listOf(Color.White.copy(alpha = 0.16f), Color.White.copy(alpha = 0.03f))),
                shape,
            ),
    )
}

@Composable
private fun CapsuleItem(item: BottomItem, selected: Boolean, modifier: Modifier, onClick: () -> Unit) {
    val source = remember { MutableInteractionSource() }
    val tint by animateColorAsState(if (selected) Accent else TextDim, KinoMotion.effects(), label = "tab")
    Column(
        modifier
            .fillMaxHeight()
            .selectable(selected, source, indication = null, role = Role.Tab, onClick = onClick)
            .pressScale(source, 0.86f),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Icon(if (selected) item.iconOn else item.icon, contentDescription = null, tint = tint, modifier = Modifier.size(24.dp))
        Spacer(Modifier.height(2.dp))
        Text(item.label, style = MaterialTheme.typography.labelSmall, color = tint, maxLines = 1)
    }
}

suspend fun runAdopt() {
    val ctx = KinoApp.instance
    val perm = if (android.os.Build.VERSION.SDK_INT >= 33) {
        android.Manifest.permission.READ_MEDIA_VIDEO
    } else android.Manifest.permission.READ_EXTERNAL_STORAGE
    if (androidx.core.content.ContextCompat.checkSelfPermission(ctx, perm) !=
        android.content.pm.PackageManager.PERMISSION_GRANTED) return // запросит экран загрузок
    try {
        val treeUri = ctx.getSharedPreferences("downloads", android.content.Context.MODE_PRIVATE)
            .getString("tree", null)?.let { android.net.Uri.parse(it) }
        ctx.container.library.adopt(treeUri)
        // Идемпотентно: пропускает записи с кадрами и .none-маркерами.
        ctx.container.art.refresh(ctx.container.library)
    } catch (_: Exception) { }
}

