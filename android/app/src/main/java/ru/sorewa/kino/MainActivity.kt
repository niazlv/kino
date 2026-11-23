package ru.sorewa.kino

import android.content.Intent
import android.content.pm.PackageManager
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.getValue
import androidx.core.view.WindowCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.launch
import ru.sorewa.kino.data.LegacyShell
import ru.sorewa.kino.ui.AppRoot
import ru.sorewa.kino.ui.theme.KinoTheme
import ru.sorewa.kino.util.Updater

/**
 * Телефонная витрина. ТВ — отдельная точка входа: [TvActivity].
 */
class MainActivity : ComponentActivity() {

    private val updater by lazy { Updater(this) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Старая оболочка на телевизоре запускалась этой же активностью (у неё
        // одна точка входа на оба лаунчера), и ярлык или «избранное» лаунчера
        // после обновления ведёт сюда. Телевизору — ТВ-интерфейс; телевизором
        // считается то же, что считала оболочка (leanback), плюс приставка,
        // где в старой витрине включали «Режим телевизора».
        if (isTv()) {
            startActivity(Intent(this, TvActivity::class.java))
            finish()
            return
        }
        WindowCompat.setDecorFitsSystemWindows(window, false)
        KinoApp.deviceKind = "android"
        val app = application as KinoApp
        lifecycleScope.launch {
            // Настройки старой оболочки переносятся в профиль вшитого сервера —
            // старая оболочка жила только на нём. У локального режима и у
            // выбранного вручную сервера их нет.
            if (app.container.onBuiltInServer) LegacyShell.importSettings(app, app.container.store, BuildConfig.BASE_URL)
            if (isTv()) {
                startActivity(Intent(this@MainActivity, TvActivity::class.java))
                finish()
            }
        }
        // Приложение всегда тёмное: светлые значки строки состояния при любой теме системы.
        enableEdgeToEdge(
            statusBarStyle = androidx.activity.SystemBarStyle.dark(android.graphics.Color.TRANSPARENT),
            navigationBarStyle = androidx.activity.SystemBarStyle.dark(android.graphics.Color.TRANSPARENT),
        )
        setContent {
            KinoTheme {
                // Пока сессия не прочитана с диска — пустой фон, а не мелькающий вход.
                val session by app.container.store.sessionFlow
                    .collectAsStateWithLifecycle(initialValue = null)
                session?.let { AppRoot(authorized = it.authorized) }
            }
        }
    }

    override fun onResume() {
        super.onResume()
        // Обновления приходят с сервера выкладки приложения (его адрес зашит при сборке), а не с
        // сервера с данными, поэтому проверяются и в режиме «на этом устройстве»: иначе человек,
        // переключившийся на него, застрял бы на своей версии навсегда. Сборка без адреса — вручную.
        updater.onResume(false)
    }

    private fun isTv(): Boolean =
        packageManager.hasSystemFeature(PackageManager.FEATURE_LEANBACK) || LegacyShell.wantsTv(this)
}
