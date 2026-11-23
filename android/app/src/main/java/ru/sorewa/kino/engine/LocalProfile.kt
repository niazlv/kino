package ru.sorewa.kino.engine

import ru.sorewa.kino.BuildConfig
import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.os.Bundle
import kotlinx.coroutines.CoroutineScope
import java.io.File

/**
 * Контекст «профиля» данных: filesDir переехал в подкаталог.
 *
 * Идентификаторы серий у движка и у каждого сервера свои, и всё, что на них
 * ссылается — очередь неотправленных отметок, опись скачанного, кэши карточек —
 * обязано лежать отдельно. Иначе очередь, набранная для одного сервера, при
 * переключении ушла бы в движок или другой сервер к чужим сериям и испортила
 * чужую историю просмотров.
 *
 * Профили (см. ConnectionConfig.profile): null — сервер, вшитый в сборку,
 * получает обычный контекст (его данные остаются где были); `local` — движок,
 * `files/local`; выбранный сервер `s_…` — `files/servers/s_…`.
 */
class ModeContext(base: Context, private val profile: String?) : ContextWrapper(base) {

    private val dir: File? by lazy {
        when (profile) {
            null -> null
            ConnectionConfig.LOCAL_PROFILE -> File(base.filesDir, "local")
            else -> File(File(base.filesDir, "servers"), profile)
        }?.apply { mkdirs() }
    }

    override fun getFilesDir(): File = dir ?: super.getFilesDir()
}

/** Сборка движка под это приложение: пути, порт, окружение. */
object LocalEngineFactory {

    fun create(context: Context, token: String, scope: CoroutineScope): LocalEngine {
        val dataDir = File(context.filesDir, "engine").apply { mkdirs() }
        val cacheDir = File(context.cacheDir, "engine").apply { mkdirs() }
        val port = EngineLaunch.pickFreePort()
        val binary = File(context.applicationInfo.nativeLibraryDir, EngineLaunch.BINARY)
        return LocalEngine(
            port = port,
            env = { EngineLaunch.env(port, token, dataDir, cacheDir, EngineSettingsStore.load(context), BuildConfig.VERSION_NAME) },
            launcher = ProcessEngineLauncher(binary, dataDir, File(dataDir, "engine.log")),
            scope = scope,
            log = { android.util.Log.i("KinoEngine", it) },
        )
    }
}

/**
 * Перезапуск процесса приложения (приём ProcessPhoenix): сначала поднимается
 * активность в отдельном процессе, она запускает приложение заново и убивает
 * себя. Так основной процесс гарантированно умирает целиком — а с ним, через
 * закрытый stdin, и движок, — и контейнер строится заново уже под новый режим.
 */
object AppRestart {
    const val EXTRA_NEXT = "next"

    /**
     * Мы в процессе `:restart`? Application.onCreate выполняется в каждом процессе
     * приложения, и без этой проверки процесс-перезапускатель строил бы свой
     * контейнер: поднимал второй движок на той же базе и открывал общие
     * хранилища, пока основной процесс ещё умирает. Имя процесса читается из
     * /proc: Application.getProcessName() есть только с API 28, а minSdk — 24.
     */
    fun isRestartProcess(): Boolean = try {
        File("/proc/self/cmdline").readText().trim('\u0000').endsWith(":restart")
    } catch (_: Exception) {
        false
    }

    fun restart(context: Context) {
        val launch = context.packageManager.getLaunchIntentForPackage(context.packageName)
            ?.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
            ?: return
        context.startActivity(
            Intent(context, RestartActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                .putExtra(EXTRA_NEXT, launch),
        )
        Runtime.getRuntime().exit(0)
    }
}

/** Живёт в процессе `:restart` (см. манифест), поэтому переживает смерть основного. */
class RestartActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        @Suppress("DEPRECATION")
        val next = intent.getParcelableExtra<Intent>(AppRestart.EXTRA_NEXT)
        if (next != null) startActivity(next)
        finish()
        Runtime.getRuntime().exit(0)
    }
}
