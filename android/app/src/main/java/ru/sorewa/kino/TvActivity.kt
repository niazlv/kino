package ru.sorewa.kino

import android.os.Build
import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.getValue
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import ru.sorewa.kino.data.LegacyShell
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import ru.sorewa.kino.tvhome.TvLauncher
import ru.sorewa.kino.tvhome.TvLauncherWork
import ru.sorewa.kino.ui.theme.KinoTheme
import ru.sorewa.kino.ui.tv.TvRoot
import ru.sorewa.kino.util.Updater

/**
 * ТВ-точка входа: пульт, вход по коду с телефона. Приложение ставится
 * на приставки и телевизоры с Android TV/Google TV.
 */
class TvActivity : ComponentActivity() {

    private val updater by lazy { Updater(this) }

    /** Системный диалог «Добавить канал на главный экран?» — ответ знает сама система. */
    private val addChannel = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        KinoApp.deviceKind = "tv"
        val app = application as KinoApp
        // Старая оболочка жила только на вшитом сервере (см. MainActivity).
        if (app.container.onBuiltInServer) {
            lifecycleScope.launch { LegacyShell.importSettings(app, app.container.store, BuildConfig.BASE_URL) }
        }
        setContent {
            KinoTheme {
                // Пока сессия не прочитана с диска — пустой фон, а не мелькающий вход.
                val session by app.container.store.sessionFlow
                    .collectAsStateWithLifecycle(initialValue = null)
                session?.let { TvRoot(authorized = it.authorized) }
            }
        }
        // Главный экран Android TV: при каждом возвращении в приложение и
        // сразу после входа — «Продолжить просмотр» и канал «Новые серии».
        if (TvLauncher.supported(this)) {
            lifecycleScope.launch {
                repeatOnLifecycle(Lifecycle.State.RESUMED) {
                    app.container.store.sessionFlow.map { it.authorized }.distinctUntilChanged().collect { signedIn ->
                        if (signedIn) syncHomeScreen()
                    }
                }
            }
        }
    }

    private suspend fun syncHomeScreen() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        TvLauncherWork.schedule(this)
        val synced = try {
            TvLauncher.sync(this, (application as KinoApp).container.api)
        } catch (_: Exception) {
            // Сеть или сессия: вход и офлайн разбирает TvRoot, фоновое обновление повторит.
            return
        }
        // Канал на главный экран — только с согласия: один раз спрашиваем
        // сами, дальше — «Профиль → Главный экран».
        if (TvLauncher.shouldAskOnce(this, synced)) {
            TvLauncher.askToShow(this, addChannel, synced.channelId)
        }
    }

    override fun onResume() {
        super.onResume()
        updater.onResume(false)
    }

    override fun onStop() {
        super.onStop()
        // Ушли на главный экран: там должно быть то, что сейчас отмечали в приложении.
        if (TvLauncher.supported(this) && (application as KinoApp).container.api.currentToken.isNotEmpty()) {
            TvLauncherWork.refreshSoon(this, 1_500)
        }
    }
}
