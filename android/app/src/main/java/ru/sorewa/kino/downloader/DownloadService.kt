package ru.sorewa.kino.downloader

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.net.Uri
import android.os.IBinder
import androidx.core.app.NotificationCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.longOrNull
import ru.sorewa.kino.BuildConfig
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.MainActivity
import ru.sorewa.kino.R
import ru.sorewa.kino.data.LocalLibrary
import ru.sorewa.kino.data.TokenStore
import java.io.BufferedReader
import java.io.InputStreamReader
import java.util.concurrent.atomic.AtomicReference

/**
 * Качалка: foreground-служба с очередью заданий. Движок — утилита
 * kinopub-downloader (libkinopub.so) + ffmpeg, как в старой оболочке; обвязка
 * своя: очередь переживает смерть службы (prefs), пауза/отмена с reap'ом,
 * прогресс в уведомлении (не чаще раза в секунду) и в статусе для UI.
 *
 * Протокол stdout утилиты — NDJSON: start/episode/track/progress/stop и
 * финальный result. track/stop игнорируются.
 */
/** Живой статус качалки для UI (экран «Загрузки»). */
data class DownloadStatus(
    val running: DownloadJob? = null,
    val key: String = "",
    val state: String = "",      // started/completed/failed/deferred/…
    val percent: Int = 0,
    val bytesDone: Long = 0,
    val bytesTotal: Long = 0,
    val approx: Boolean = false,
    val queued: Int = 0,
    /** Серий в текущем задании (по событию start утилиты) и сколько готово. */
    val epTotal: Int = 0,
    val epDone: Int = 0,
    /**
     * Серии текущего задания, которые утилита уже докачала («S01E03»): в
     * опись они попадут в конце задания, а страница тайтла не должна до тех
     * пор показывать их «в очереди».
     */
    val doneKeys: Set<String> = emptySet(),
    val paused: Boolean = false,
    val lastError: String = "",
    /** Есть что повторить: задание сдалось или ждёт автоповтора. */
    val canRetry: Boolean = false,
    /** Когда сработает автоповтор (мс); 0 — не ждём. */
    val retryAt: Long = 0,
)

class DownloadService : Service() {

    companion object {
        const val ACTION_ENQUEUE = "ru.sorewa.kino.ENQUEUE"
        const val ACTION_PAUSE = "ru.sorewa.kino.PAUSE"
        const val ACTION_RESUME = "ru.sorewa.kino.RESUME"
        const val ACTION_CANCEL = "ru.sorewa.kino.CANCEL"
        /** «Повторить»: сдавшееся задание или ждущий автоповтор — сейчас. */
        const val ACTION_RETRY = "ru.sorewa.kino.RETRY"
        /** Подтолкнуть: доделать очередь и забрать готовое от прошлых прогонов. */
        const val ACTION_KICK = "ru.sorewa.kino.KICK"

        /**
         * С экрана приложения: есть очередь или готовое в рабочей папке —
         * служба доделает. Без дела не стартует (уведомление ни к чему).
         */
        fun kickIfNeeded(ctx: Context) {
            val busy = Downloader.loadQueue(ctx).isNotEmpty() ||
                Downloader.runningJob(ctx) != null ||
                Exporter.readyFiles(Downloader.workDir(ctx)).isNotEmpty()
            if (!busy || status.value.running != null) return
            val i = Intent(ctx, DownloadService::class.java).setAction(ACTION_KICK)
            try {
                if (Build.VERSION.SDK_INT >= 26) ctx.startForegroundService(i) else ctx.startService(i)
            } catch (_: Exception) { }
        }

        /** Восстановить «Повторить» после перезапуска приложения. */
        fun restoreFailed(ctx: Context) {
            if (status.value.lastError.isNotEmpty() || status.value.running != null) return
            Downloader.failedJob(ctx)?.let { (_, why) ->
                status.value = status.value.copy(lastError = why.ifEmpty { "Загрузка не удалась" }, canRetry = true)
            }
        }
        const val EXTRA_JOB = "job" // DownloadJob в JSON
        const val CHANNEL = "downloads"
        private const val NOTE_ID = 1001
        private const val NO_NET_GIVE_UP_MS = 30 * 60_000L

        /** Живой статус для UI (экран «Загрузки»). */
        val status = kotlinx.coroutines.flow.MutableStateFlow(DownloadStatus())
    }

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val json = Json { ignoreUnknownKeys = true }
    private var current: Process? = null
    private val currentJob = AtomicReference<DownloadJob?>(null)
    @Volatile private var pausing = false
    /** Рабочий цикл уже идёт (в том числе ждёт автоповтора) — второй не нужен. */
    @Volatile private var working = false
    @Volatile private var cancelled = false
    private val cancelFinished = java.util.concurrent.atomic.AtomicBoolean(false)
    private var done = 0
    private var count = 0
    private var lastEpisodeError = ""
    /** Серии, не давшиеся в этом прогоне: ключ → причина от утилиты. */
    private val failedKeys = LinkedHashMap<String, String>()
    private var lastResult: DownloadEvent.Result? = null
    private var lastNoteAt = 0L

    // Опись и картинки — общие с экранами и плеером: свой экземпляр описи
    // держал бы свою копию списка, и первая же запись позиции из плеера
    // затирала бы серии, скачанные службой.
    private val library get() = (application as KinoApp).container.library
    private val art get() = (application as KinoApp).container.art

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL, getString(R.string.downloads_channel), NotificationManager.IMPORTANCE_LOW),
        )
        // Очередь прошлой жизни службы лежит в prefs и подхватывается рабочим
        // циклом сама. Добавлять её здесь ещё раз нельзя: каждое задание
        // качалось бы дважды. Задание с паузы — в голову очереди.
        Downloader.pausedJob(this)?.let { requeueFront(it) }
        Downloader.savePaused(this, null)
        // Процесс убили посреди задания (служба создаётся заново только в
        // новом процессе или после stopSelf, когда записи уже нет) — задание
        // снова в голову очереди; утилита пропустит докачанное.
        Downloader.runningJob(this)?.let { requeueFront(it.copy(notBefore = 0)) }
        Downloader.saveRunning(this, null)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_ENQUEUE -> intent.getStringExtra(EXTRA_JOB)?.let { raw ->
                runCatching { json.decodeFromString(DownloadJob.serializer(), raw) }
                    .getOrNull()?.let { scope.launch { enqueue(it) } }
            }
            ACTION_PAUSE -> pause()
            ACTION_RESUME -> resume()
            ACTION_CANCEL -> cancel()
            ACTION_RETRY -> retryNow()
            ACTION_KICK -> kick()
        }
        return START_NOT_STICKY
    }

    private fun enqueue(job: DownloadJob) {
        synchronized(this) {
            val queue = Downloader.loadQueue(this)
            queue.add(job)
            Downloader.saveQueue(this, queue)
            publish(status.value.copy(queued = queue.size))
        }
        if (current == null && !pausing) startForegroundAndWork() else ensureForeground()
    }

    private fun startForegroundAndWork() {
        val n = note("В очереди", null)
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(NOTE_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            startForeground(NOTE_ID, n)
        }
        if (working) return
        working = true
        scope.launch {
            try { work() } finally { working = false }
        }
    }

    /** «Повторить»: сдавшееся — снова в очередь, отложенные — без ожидания. */
    private fun retryNow() {
        synchronized(this) {
            val q = Downloader.loadQueue(this).map { it.copy(notBefore = 0) }.toMutableList()
            Downloader.failedJob(this)?.let { (job, _) -> q.add(0, job.copy(attempt = 0, notBefore = 0)) }
            Downloader.saveFailed(this, null)
            Downloader.saveQueue(this, q)
            publish(status.value.copy(lastError = "", canRetry = false, retryAt = 0, queued = q.size))
        }
        if (current == null && !pausing && !working) startForegroundAndWork() else ensureForeground()
    }

    private fun kick() {
        if (current == null && !pausing && !working) startForegroundAndWork() else ensureForeground()
    }

    /** startForegroundService требует startForeground, даже если работа уже идёт. */
    private fun ensureForeground() {
        val n = note(if (current != null) "Качается" else "В очереди", null)
        if (Build.VERSION.SDK_INT >= 29) startForeground(NOTE_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        else startForeground(NOTE_ID, n)
    }

    private fun pause() {
        pausing = true
        current?.destroy()
    }

    private fun resume() {
        pausing = false
        // Задание с паузы — снова в голову очереди. Новая служба делает это в
        // onCreate, а если прежняя ещё жива, кроме этого места некому.
        synchronized(this) {
            Downloader.pausedJob(this)?.let {
                requeueFront(it)
                Downloader.savePaused(this, null)
            }
        }
        publish(status.value.copy(paused = false))
        if (current == null) startForegroundAndWork()
    }

    private fun cancel() {
        cancelled = true
        synchronized(this) {
            Downloader.saveQueue(this, emptyList())
            Downloader.savePaused(this, null)
        }
        Downloader.saveFailed(this, null)
        val proc = current
        if (proc == null) {
            scope.launch { finishCancel(null) }
        } else {
            proc.destroy() // читатель цикла увидит обрыв и зайдёт в ветку cancelled
        }
    }

    /**
     * Отмена доведена до конца: служба остановлена, экран и уведомление
     * сброшены. Готовые серии отмена не трогает — они скачаны целиком.
     * Вызывается и из cancel(), и из рабочего цикла — второй вызов ничего не делает.
     */
    private suspend fun finishCancel(job: DownloadJob?) {
        if (!cancelFinished.compareAndSet(false, true)) return
        runCatching { exportReady(job) }
        Downloader.workDir(this).deleteRecursively()
        finishNote("Отменено")
        publish(DownloadStatus())
        stopForeground(STOP_FOREGROUND_REMOVE)
        // «На паузе» могло остаться от прежней службы (parkPaused) — его
        // stopForeground этой службы не снимает.
        getSystemService(NotificationManager::class.java).cancel(NOTE_ID)
        stopSelf()
    }

    /**
     * Пауза: готовые серии забираем сразу, остальное продолжит утилита —
     * по своему состоянию она пропустит скачанное.
     */
    private suspend fun finishPause(job: DownloadJob) {
        runCatching { exportReady(job) }
        Downloader.savePaused(this, job)
        publish(status.value.copy(paused = true, running = job))
        note("На паузе · ${job.title}", actions = NoteActions.RESUME)
    }

    /**
     * На паузе служба не нужна: уведомление «На паузе» с «Продолжить» и
     * статус для экранов остаются, «Продолжить» поднимет её снова. Висеть
     * foreground-службой нельзя — у dataSync суточный лимит времени.
     */
    private fun parkPaused() {
        stopForeground(STOP_FOREGROUND_DETACH)
        stopSelf()
    }

    // ---- рабочий цикл ----

    private suspend fun work() {
        // Готовое от прошлых прогонов (оборвался, сдался, служба умерла) —
        // сперва наружу: оно скачано, его должно быть видно в «Загрузках».
        runCatching { exportReady(null) }
        while (!cancelled) {
            val job = nextJob() ?: break

            if (pausing) {
                Downloader.savePaused(this, job)
                publish(status.value.copy(paused = true, running = job))
                note("На паузе · ${job.title}", actions = NoteActions.RESUME)
                parkPaused()
                return
            }

            runJob(job)
            // Задание встало на паузу. Брать следующее нельзя: оно затёрло бы
            // запись о паузе, а сброс статуса ниже спрятал бы паузу с экранов.
            if (pausing && Downloader.pausedJob(this) != null) {
                parkPaused()
                return
            }
        }
        if (!cancelled) {
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
            // Ошибка последнего задания остаётся на экране загрузок: обнули
            // статус — и неудачная загрузка пропадёт бесследно.
            publish(DownloadStatus(lastError = status.value.lastError, canRetry = status.value.canRetry))
        }
    }

    /**
     * Следующее задание очереди. Отложенное (автоповтор) ждёт своего часа,
     * а без сети — саму сеть: попытки в самолёте не сгорают.
     */
    private suspend fun nextJob(): DownloadJob? {
        var noNetSince = 0L
        while (!cancelled) {
            val queue = synchronized(this) { Downloader.loadQueue(this) }
            if (queue.isEmpty()) return null
            val now = System.currentTimeMillis()
            if (ru.sorewa.kino.util.hasNetwork()) noNetSince = 0
            else if (noNetSince == 0L) noNetSince = now
            else if (now - noNetSince > NO_NET_GIVE_UP_MS) {
                // Самолёт: часами держать службу Android не даст (у dataSync
                // 6 ч в сутки). Очередь остаётся — продолжим, как только
                // приложение откроют с сетью.
                publish(status.value.copy(
                    running = null, retryAt = 0, canRetry = true,
                    lastError = "Нет сети — докачаю, когда откроете приложение с интернетом.",
                ))
                return null
            }
            val ready = queue.indexOfFirst { it.notBefore <= now }
            if (ready >= 0 && ru.sorewa.kino.util.hasNetwork()) {
                return synchronized(this) {
                    val q = Downloader.loadQueue(this)
                    val i = q.indexOfFirst { it.notBefore <= System.currentTimeMillis() }
                    if (i < 0) null else q.removeAt(i).also {
                        Downloader.saveQueue(this, q)
                        publish(status.value.copy(queued = q.size, retryAt = 0))
                    }
                } ?: continue
            }
            val wait = queue.minOf { it.notBefore }
            val first = queue.minByOrNull { it.notBefore }!!
            val text = if (!ru.sorewa.kino.util.hasNetwork()) "Жду сеть, чтобы продолжить · ${first.title.substringBefore(" / ")}"
            else "Повторю через ${((wait - now) / 1000).coerceAtLeast(1)} с · ${first.title.substringBefore(" / ")}"
            // Свежее задание без сети — не «прервалась», а «ждёт сеть»:
            // retryAt только у настоящего автоповтора (иначе экран рисовал
            // красное «Загрузка не удалась» у задания, которое и не начиналось).
            publish(status.value.copy(running = null, queued = queue.size, retryAt = if (wait > now) wait else 0))
            note(text, actions = NoteActions.RETRY_NOW)
            kotlinx.coroutines.delay(if (wait > now) minOf(wait - now, 5_000) else 5_000)
        }
        return null
    }

    private suspend fun runJob(job: DownloadJob) {
        currentJob.set(job)
        Downloader.saveRunning(this, job)
        // Прошлая ошибка уходит: под полосой новой попытки её красный текст
        // читался как «опять не качается».
        publish(status.value.copy(
            running = job, key = "", percent = 0, state = "started",
            queued = Downloader.loadQueue(this).size, lastError = "", canRetry = false, retryAt = 0,
            bytesDone = 0, bytesTotal = 0, epTotal = 0, epDone = 0, doneKeys = emptySet(),
        ))
        note("${job.title} — качается", actions = NoteActions.PAUSE_CANCEL)

        val container = (application as KinoApp).container
        val api = container.api
        // Сессия должна быть свежей: утилита сама токен не обновит. У локального
        // аккаунта сессия — токен движка, он не истекает и продлевать его нечем.
        if (!container.local) runCatching { api.refreshSession() }
        val cookie = "kino_session=${api.currentToken}"
        val ua = "kino-android/${BuildConfig.VERSION_NAME}"

        // Адрес страницы — на актуальный origin: у локального движка порт новый на
        // каждый запуск приложения, а задание могло пролежать в очереди с прошлого.
        val args = Downloader.baseArgs(this, job.copy(pageUrl = Downloader.rebase(job.pageUrl, api.baseUrl)), cookie, ua)
        android.util.Log.i("kino", "качалка: ${Downloader.maskCookie(args.joinToString(" "))}")

        // Хвост stderr, а не голова: за сезон утилита пишет десятки строк INFO,
        // и в 8000 символов с головы влезут только первые серии — причина
        // сбоя в конце прогона потеряется, а на экран пойдёт случайная строка.
        val stderr = ArrayDeque<String>()
        var stderrLen = 0
        failedKeys.clear()
        lastResult = null
        lastEpisodeError = ""

        try {
            // Утилита ходит за манифестами к движку этого устройства: он должен
            // слушать, прежде чем её запускать (холодный старт, перезапуск движка).
            if (container.local && container.engine?.let { it.start(); it.awaitReady(25_000) } != true) {
                throw java.io.IOException("локальный движок не отвечает")
            }
            val proc = Downloader.process(this, args)
            current = proc
            // Пауза или отмена пришли, пока ждали движок (current был пуст и
            // гасить было нечего): утилиту сразу назад, дальше — обычным путём.
            if (cancelled || pausing) proc.destroy()

            Thread {
                try {
                    proc.errorStream.bufferedReader().forEachLine {
                        synchronized(stderr) {
                            stderr.addLast(it)
                            stderrLen += it.length + 1
                            while (stderrLen > 12_000 && stderr.size > 1) stderrLen -= stderr.removeFirst().length + 1
                        }
                    }
                } catch (_: Exception) { }
            }.start()

            try {
                proc.inputStream.let { stream ->
                    BufferedReader(InputStreamReader(stream)).forEachLine { line ->
                        if (!line.startsWith("{")) return@forEachLine
                        parseEvent(line, job)
                    }
                }
            } catch (e: java.io.IOException) {
                // destroy() закрывает потоки процесса, и чтение обычно рвётся
                // исключением, а не концом потока. При паузе и отмене это наш же
                // сигнал: дальше — как при обычном выходе утилиты.
                if (!pausing && !cancelled) throw e
            }

            val code = proc.waitFor()
            reap(proc)
            current = null
            val tail = synchronized(stderr) { stderr.joinToString("\n") }

            if (cancelled) {
                finishCancel(job)
                return
            }
            if (pausing) {
                finishPause(job)
                return
            }

            // Готовое — наружу при любом исходе: сбой одной серии больше не
            // хоронит остальные (было: 7 скачанных серий остались в служебной
            // папке, потому что не далась 8-я).
            var moved = 0
            var exportError = ""
            try {
                moved = exportReady(job)
            } catch (e: Exception) {
                exportError = e.message ?: "файл не сохранился"
            }

            val result = lastResult
            val outcome = Downloader.outcome(code, result, failedKeys, moved, job, tail, exportError)
            android.util.Log.i("kino", "качалка: код $code, перенесено $moved, не далось ${failedKeys.keys}, result=$result → $outcome")

            when (outcome) {
                RunOutcome.Force -> {
                    requeueFront(job.copy(force = true, notBefore = 0))
                    return
                }
                is RunOutcome.Done -> {
                    // Всё скачано: состояние утилиты больше не нужно — иначе она
                    // молча пропустила бы эти серии при будущей перекачке.
                    wipeStates(job)
                    Downloader.saveFailed(this, null)
                    publish(status.value.copy(lastError = "", canRetry = false))
                    val name = job.title.substringBefore(" / ")
                    if (outcome.moved > 0) finishNote("Готово · $name · ${outcome.moved}")
                    else finishNote("Уже скачано · $name")
                    return
                }
                is RunOutcome.Failed -> Unit
            }

            val why = (outcome as RunOutcome.Failed).why
            android.util.Log.w("kino", "качалка: $why; код $code; stderr:\n${tail.takeLast(3000)}")

            if (!outcome.permanent && job.attempt + 1 < Downloader.MAX_ATTEMPTS) {
                // Автоповтор: то же задание, утилита пропустит готовое и
                // докачает только недостающее.
                val at = System.currentTimeMillis() + Downloader.backoffMs(job.attempt)
                synchronized(this) {
                    val q = Downloader.loadQueue(this)
                    q.add(job.copy(attempt = job.attempt + 1, notBefore = at))
                    Downloader.saveQueue(this, q)
                }
                publish(status.value.copy(
                    lastError = "$why. Повторю сам (попытка ${job.attempt + 2} из ${Downloader.MAX_ATTEMPTS}).",
                    canRetry = true, retryAt = at,
                ))
            } else {
                // Сдались: ждём «Повторить». Задание помнится и после
                // перезапуска приложения.
                Downloader.saveFailed(this, job.copy(attempt = 0, notBefore = 0, force = false), why)
                failNote(job, why)
                publish(status.value.copy(lastError = why, canRetry = true, retryAt = 0))
            }
        } catch (e: Exception) {
            reap(current)
            current = null
            // Пауза и отмена, оборвавшие задание на любом шаге, — не ошибка, но
            // их надо довести: отмена останавливает службу и сбрасывает экран,
            // пауза сохраняет задание для «Продолжить».
            if (cancelled) finishCancel(job)
            else if (pausing) finishPause(job)
            else {
                val why = e.message ?: "неизвестная ошибка"
                android.util.Log.w("kino", "качалка: исключение", e)
                runCatching { exportReady(job) }
                Downloader.saveFailed(this, job.copy(attempt = 0, notBefore = 0), why)
                failNote(job, why)
                publish(status.value.copy(lastError = why, canRetry = true))
            }
        } finally {
            currentJob.set(null)
            // Здесь задание уже разобрано: готово, в очереди на повтор, на
            // паузе или сдалось. Запись нужна только на случай смерти процесса.
            Downloader.saveRunning(this, null)
        }
    }

    /**
     * Готовые серии из рабочей папки — в «Фильмы» (или выбранную папку) и в
     * опись. job — текущее задание (его название и фильм ли это); для
     * остатков прошлых прогонов то же берётся из состояния утилиты.
     * Возвращает, сколько серий перенесено.
     */
    private suspend fun exportReady(job: DownloadJob?): Int {
        val work = Downloader.workDir(this)
        val ready = Exporter.readyFiles(work)
        if (ready.isEmpty()) return 0
        val tree = treeUri()
        var moved = 0
        val titles = mutableSetOf<Long>()
        for (r in ready) {
            val sameJob = job != null && LocalLibrary.titleIdOf(job.pageUrl) == LocalLibrary.titleIdOf(r.pageUrl)
            val title = if (sameJob) job!!.title else r.title
            val page = r.pageUrl.ifEmpty { job?.pageUrl.orEmpty() }
            val size = r.file.length()
            if (job == null) note("Переношу скачанное · ${title.substringBefore(" / ")} · S%02dE%02d".format(r.season, r.episode))
            val uri = Exporter.export(this, r.file, title, tree)
            Exporter.toLibraryEntry(this, library, uri, title, page, size, sameJob && job!!.movie)
            for (sub in r.subs) runCatching { Exporter.export(this, sub, title, tree) }
            moved++
            LocalLibrary.titleIdOf(page).takeIf { it > 0 }?.let { titles += it }
        }
        art.refresh(library)
        // Карточка тайтла и кадры серий — сразу, пока сеть есть:
        // скачанное должно открываться без сети как онлайн.
        for (id in titles) runCatching {
            (application as KinoApp).container.offline.refreshTitle(id, force = true)
        }
        if (job == null && moved > 0) finishNote("Перенесено в «Загрузки»: $moved")
        return moved
    }

    /** Задание скачано целиком — стираем состояние утилиты по его тайтлу. */
    private fun wipeStates(job: DownloadJob) {
        val id = LocalLibrary.titleIdOf(job.pageUrl)
        val work = Downloader.workDir(this)
        for (sf in Exporter.stateFiles(work)) {
            val dir = sf.parentFile ?: continue
            val same = Exporter.seriesOf(sf) == id.toString()
            // Ничего не осталось, кроме служебного, — тоже прочь.
            val empty = dir.walkTopDown().none { it.isFile && it.extension.lowercase() in setOf("mkv", "mp4") }
            if (same && empty) dir.deleteRecursively()
        }
    }

    private fun requeueFront(job: DownloadJob) {
        synchronized(this) {
            val q = Downloader.loadQueue(this)
            q.add(0, job)
            Downloader.saveQueue(this, q)
        }
    }

    /** destroy() ломает потоки — процессу даём умереть до троганья файлов. */
    private fun reap(proc: Process?) {
        val p = proc ?: return
        try {
            for (i in 0 until 100) {
                try {
                    p.exitValue()
                    return
                } catch (_: IllegalThreadStateException) {
                    if (i == 20) p.destroy()
                    Thread.sleep(100)
                }
            }
            android.util.Log.w("kino", "качалка: процесс не умер за 10 с")
        } catch (_: Exception) { }
    }

    // ---- NDJSON ----

    private fun parseEvent(line: String, job: DownloadJob) {
        when (val ev = parseDownloadEvent(line, json)) {
            is DownloadEvent.Start -> {
                count = (ev.total - ev.done).coerceAtLeast(1)
                done = 0
                publish(status.value.copy(epTotal = count, epDone = 0))
                note("${job.title} · серий: $count", actions = NoteActions.PAUSE_CANCEL)
            }
            is DownloadEvent.Episode -> {
                if (ev.state == "completed") done++
                if (ev.state == "failed") {
                    ev.error?.let { lastEpisodeError = it }
                    failedKeys[ev.key] = ev.error ?: "ошибка"
                }
                if (ev.state == "completed") failedKeys.remove(ev.key)
                val stateText = when (ev.state) {
                    "started" -> "качается"
                    "completed" -> "готово"
                    "failed" -> "ошибка"
                    "deferred" -> "повторим позже"
                    else -> ev.state
                }
                publish(status.value.copy(
                    key = ev.key, state = ev.state, epDone = done,
                    doneKeys = if (ev.state == "completed") status.value.doneKeys + ev.key else status.value.doneKeys,
                    // Новая серия — проценты с нуля, а не хвост прошлой.
                    percent = if (ev.state == "started") 0 else status.value.percent,
                    bytesDone = if (ev.state == "started") 0 else status.value.bytesDone,
                    bytesTotal = if (ev.state == "started") 0 else status.value.bytesTotal,
                ))
                val tail = if (count > 1) " · $done из $count" else ""
                note("${job.title} · ${ev.key} · $stateText$tail", actions = NoteActions.PAUSE_CANCEL)
            }
            is DownloadEvent.Progress -> {
                val key = ev.key ?: status.value.key
                publish(
                    status.value.copy(
                        key = key, percent = ev.percent,
                        bytesDone = ev.downloaded, bytesTotal = ev.totalBytes, approx = ev.approx,
                    )
                )
                // Уведомление не чаще раза в секунду.
                val now = System.currentTimeMillis()
                if (now - lastNoteAt >= 1000) {
                    lastNoteAt = now
                    val bytes = if (ev.downloaded > 0) {
                        " · ${fmtBytes(ev.downloaded)}${if (ev.approx) "~" else ""}" +
                            (if (ev.totalBytes > 0) "/${fmtBytes(ev.totalBytes)}" else "")
                    } else ""
                    note("${job.title} · $key · ${ev.percent}%$bytes", progress = ev.percent, actions = NoteActions.PAUSE_CANCEL)
                }
            }
            is DownloadEvent.Result -> {
                lastResult = ev
                // Финальная строка утилиты: честная ошибка всего прогона.
                if (!ev.error.isNullOrEmpty()) {
                    lastEpisodeError = ev.error
                } else if (ev.succeeded == 0 && ev.failed > 0) {
                    lastEpisodeError = lastEpisodeError.ifEmpty { "ни одна серия не скачалась" }
                }
            }
            // track / stop — игнорируются; null — не-JSON строка.
            else -> {}
        }
    }

    // ---- уведомления ----

    private enum class NoteActions { NONE, PAUSE_CANCEL, RESUME, RETRY_NOW }

    private fun note(text: String, progress: Int? = null, actions: NoteActions = NoteActions.NONE): Notification {
        val open = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java).putExtra("open", currentJob.get()?.pageUrl ?: ""),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val b = NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setContentTitle("kino")
            .setContentText(text)
            .setContentIntent(open)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .setOngoing(true)
        if (progress != null) {
            b.setProgress(100, progress, false)
        }
        when (actions) {
            NoteActions.PAUSE_CANCEL -> {
                b.addAction(0, "Пауза", serviceIntent(ACTION_PAUSE, 1))
                b.addAction(0, "Отменить", serviceIntent(ACTION_CANCEL, 2))
            }
            NoteActions.RESUME -> {
                b.addAction(0, "Продолжить", serviceIntent(ACTION_RESUME, 4))
                b.addAction(0, "Отменить", serviceIntent(ACTION_CANCEL, 2))
            }
            NoteActions.RETRY_NOW -> {
                b.addAction(0, "Повторить сейчас", serviceIntent(ACTION_RETRY, 5))
                b.addAction(0, "Отменить", serviceIntent(ACTION_CANCEL, 2))
            }
            NoteActions.NONE -> {}
        }
        val n = b.build()
        getSystemService(NotificationManager::class.java).notify(NOTE_ID, n)
        return n
    }

    private fun serviceIntent(action: String, req: Int): PendingIntent =
        PendingIntent.getService(this, req, Intent(this, DownloadService::class.java).setAction(action), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)

    /**
     * Ошибка — отдельным уведомлением, которое переживёт остановку службы:
     * «текущее» уведомление уходит вместе с foreground, и ошибку никто не видел.
     */
    private fun failNote(job: DownloadJob, why: String) {
        val n = NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_notify_error)
            .setContentTitle("Не скачалось: ${job.title.substringBefore(" / ")}")
            .setContentText(why.take(120))
            .setStyle(NotificationCompat.BigTextStyle().bigText(why.take(400)))
            .setAutoCancel(true)
            .addAction(0, "Повторить", serviceIntent(ACTION_RETRY, 6))
            .build()
        getSystemService(NotificationManager::class.java).notify(3000 + (System.currentTimeMillis() % 1000).toInt(), n)
    }

    private fun finishNote(text: String) {
        val n = NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_sys_download_done)
            .setContentTitle("kino")
            .setContentText(text)
            .setAutoCancel(true)
            .build()
        getSystemService(NotificationManager::class.java).notify(2000 + (System.currentTimeMillis() % 1000).toInt(), n)
    }

    private fun treeUri(): Uri? {
        val raw = getSharedPreferences("downloads", Context.MODE_PRIVATE).getString("tree", null)
        return raw?.let { runCatching { Uri.parse(it) }.getOrNull() }
    }

    private fun publish(s: DownloadStatus) { status.value = s }

    private fun fmtBytes(b: Long): String = when {
        b >= 1 shl 30 -> "%.1f ГБ".format(b.toDouble() / (1 shl 30))
        b >= 1 shl 20 -> "%.0f МБ".format(b.toDouble() / (1 shl 20))
        else -> "${b / (1 shl 10)} КБ"
    }

    override fun onDestroy() {
        scope.cancel()
        current?.destroy()
        super.onDestroy()
    }
}
