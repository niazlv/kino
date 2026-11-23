package ru.sorewa.kino.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
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
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
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
import ru.sorewa.kino.ui.theme.Surface1
import ru.sorewa.kino.ui.theme.Surface2
import ru.sorewa.kino.ui.theme.Stroke
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
    NavHost(
        navController = nav,
        startDestination = "downloads",
        enterTransition = { androidx.compose.animation.fadeIn(androidx.compose.animation.core.tween(180)) },
        exitTransition = { androidx.compose.animation.fadeOut(androidx.compose.animation.core.tween(120)) },
    ) {
        composable("downloads") {
            Column(Modifier.fillMaxSize()) {
                OfflineBanner(onRetry, Modifier.statusBarsPadding())
                DownloadsScreen(nav, offline = true)
            }
        }
        composable(
            "title/{id}",
            arguments = listOf(navArgument("id") { type = NavType.LongType }),
        ) { entry -> TitleScreen(nav, entry.arguments?.getLong("id") ?: 0, offlineOnly = true) }
        localTitleRoute(nav)
    }
    }
}

/** Скачанное без тайтла в каталоге: страница по названию папки. */
fun NavGraphBuilder.localTitleRoute(nav: NavHostController) {
    composable(
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
    // Тайтл рисуется во всю высоту под строкой состояния, но панель внизу
    // остаётся — с тайтла можно сразу уйти в другой раздел.
    Scaffold(
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
        bottomBar = { BottomBar(nav, route) },
    ) { padding ->
        Box(Modifier.padding(padding)) {
            NavHost(
                navController = nav,
                startDestination = "home",
                enterTransition = { androidx.compose.animation.fadeIn(androidx.compose.animation.core.tween(180)) },
                exitTransition = { androidx.compose.animation.fadeOut(androidx.compose.animation.core.tween(120)) },
            ) {
                phoneGraph(nav)
            }
        }
    }
}

/** Маршруты, общие для телефона и ТВ. */
fun NavGraphBuilder.phoneGraph(nav: NavHostController) {
    composable("home") { HomeScreen(nav) }
    composable("catalog") { CatalogScreen(nav) }
    composable("search") { SearchScreen(nav) }
    composable(
        "mine?tab={tab}",
        arguments = listOf(navArgument("tab") { type = NavType.IntType; defaultValue = 0 }),
    ) { entry -> MineScreen(nav, entry.arguments?.getInt("tab") ?: 0) }
    composable("downloads") { DownloadsScreen(nav) }
    composable("history") { HistoryScreen(nav) }
    composable("settings") { SettingsScreen(nav) }
    composable("link") { LinkDeviceScreen(nav) }
    composable("engine") { ru.sorewa.kino.ui.phone.EngineSettingsScreen(nav) }
    composable(
        "title/{id}",
        arguments = listOf(navArgument("id") { type = NavType.LongType }),
    ) { entry ->
        TitleScreen(nav, entry.arguments?.getLong("id") ?: 0)
    }
    localTitleRoute(nav)
}

@Composable
private fun BottomBar(nav: NavHostController, current: String?) {
    // Подсвечен раздел, из которого открыт текущий экран (тайтл, история…):
    // ближайший к вершине стека пункт нижней панели.
    @Suppress("UNUSED_VARIABLE") val tick = current
    val section = nav.currentBackStack.value
        .mapNotNull { it.destination.route }
        .lastOrNull { r -> bottomItems.any { it.route == r } }
    Column {
        HorizontalDivider(color = Stroke, thickness = 1.dp)
        NavigationBar(containerColor = Surface1, tonalElevation = 0.dp) {
            bottomItems.forEach { item ->
                val selected = section == item.route
                NavigationBarItem(
                    selected = selected,
                    onClick = {
                        val target = item.route.substringBefore('?')
                        nav.navigate(target) {
                            popUpTo(nav.graph.findStartDestination().id) { saveState = true }
                            launchSingleTop = true
                            restoreState = true
                        }
                    },
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

