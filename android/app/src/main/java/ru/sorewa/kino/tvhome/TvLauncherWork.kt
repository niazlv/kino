package ru.sorewa.kino.tvhome

import android.content.Context
import android.os.Build
import android.util.Log
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.ApiError
import java.util.concurrent.TimeUnit

/**
 * Обновление главного экрана ТВ в фоне: раз в пару часов (новые серии
 * выходят, пока телевизор просто стоит) и вскоре после просмотра или ухода
 * из приложения. Только на телевизоре — на телефоне ничего не планируется.
 */
object TvLauncherWork {
    private const val PERIODIC = "tvhome-periodic"
    private const val SOON = "tvhome-soon"

    private val online = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()

    /** Периодическое обновление; уже стоящее не трогается. */
    fun schedule(context: Context) {
        if (!TvLauncher.supported(context)) return
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(
            PERIODIC,
            ExistingPeriodicWorkPolicy.KEEP,
            PeriodicWorkRequestBuilder<TvLauncherWorker>(2, TimeUnit.HOURS)
                .setConstraints(online)
                // Первый прогон периодической задачи — сразу при постановке, а
                // ставится она вместе с обновлением из приложения: без задержки
                // главный экран обновлялся бы дважды подряд.
                .setInitialDelay(2, TimeUnit.HOURS)
                .build(),
        )
    }

    /**
     * Обновить вскоре: задержка даёт плееру дослать позицию. Повторный
     * вызов заменяет ждущий — после череды событий обновление одно.
     */
    fun refreshSoon(context: Context, delayMs: Long = 0) {
        if (!TvLauncher.supported(context)) return
        WorkManager.getInstance(context).enqueueUniqueWork(
            SOON,
            ExistingWorkPolicy.REPLACE,
            OneTimeWorkRequestBuilder<TvLauncherWorker>()
                .setInitialDelay(delayMs, TimeUnit.MILLISECONDS)
                .setConstraints(online)
                .build(),
        )
    }

    /** Выход: ничего больше не публиковать. */
    fun cancel(context: Context) {
        if (!TvLauncher.supported(context)) return
        val wm = WorkManager.getInstance(context)
        wm.cancelUniqueWork(PERIODIC)
        wm.cancelUniqueWork(SOON)
    }
}

class TvLauncherWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val ctx = applicationContext
        if (!TvLauncher.supported(ctx) || Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return Result.success()
        val api = (ctx as KinoApp).container.api
        return try {
            TvLauncher.sync(ctx, api)
            Result.success()
        } catch (e: ApiError) {
            when {
                // Сессию погасили («Выйти на всех», истёк refresh): чужим
                // карточкам на общем телевизоре не место. Сам вход приложение
                // сбросит при следующем открытии. Переспрашиваем: 401 бывает
                // и от сорванного по сети продления — тогда ничего не стираем.
                e.sessionDead -> {
                    val gone = try { api.me(); false } catch (again: ApiError) { again.sessionDead }
                    if (gone) TvLauncher.clear(ctx)
                    Result.success()
                }
                e.offline && runAttemptCount < 3 -> Result.retry()
                else -> Result.success()
            }
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            Log.w("KinoTvHome", "обновление главного экрана", e)
            Result.success()
        }
    }
}
