package ru.sorewa.kino.downloader

import android.content.Context
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.LocalLibrary
import java.io.File

/**
 * Задание качалки и мост к утилите kinopub-downloader (libkinopub.so) и
 * ffmpeg (libffmpeg.so). Бинарники лежат в APK как «библиотеки» и
 * распаковываются при установке — единственный способ исполнять их на Android.
 *
 * Контракт stdout — NDJSON от --progress-json; парсится в [DownloadService].
 */
@Serializable
data class DownloadJob(
    /** Ссылка страницы тайтла: https://kino.example.org/#/title/53014[/s2e3]. */
    val pageUrl: String,
    val title: String,
    /** "1080p" или "" — лучшее. */
    val quality: String = "",
    /** Подписи дорожек из плейлиста; запятые внутри вырезаны. Пусто = все. */
    val audio: List<String> = emptyList(),
    val subs: List<String> = emptyList(),
    /** "2" — один сезон; "" — весь тайтл. */
    val seasons: String = "",
    /** Фильм — подпись в «Загрузках» «Фильм», а не «Сезон 1, эпизод 1». */
    val movie: Boolean = false,
    /** Номер автоповтора: 0 — первый запуск (или «Повторить» руками). */
    val attempt: Int = 0,
    /** Не раньше этого времени (мс): пауза перед автоповтором. */
    val notBefore: Long = 0,
    /**
     * Перекачать и то, что утилита считает скачанным (--force): серию
     * стёрли с телефона, а состояние утилиты помнит её готовой.
     */
    val force: Boolean = false,
    /**
     * Студия из общего списка озвучек (ключ из /episodes/{id}/voices). У
     * kinopub все студии — дорожки одного потока, а у остальных площадок
     * каждая — свой поток: без выбора скачивалась та, что у площадки первая.
     * Пусто — как выберет площадка.
     */
    val voice: String = "",
    /** Подпись этой студии — для человека (шторка, «Загрузки»). */
    val voiceTitle: String = "",
    /** Площадка, с которой человек смотрит тайтл, — предпочтение; пусто — по приоритету. */
    val source: String = "",
)

object Downloader {

    private val json = Json { ignoreUnknownKeys = true }

    fun binary(ctx: Context): File = File(ctx.applicationInfo.nativeLibraryDir, "libkinopub.so")
    fun ffmpeg(ctx: Context): File = File(ctx.applicationInfo.nativeLibraryDir, "libffmpeg.so")
    fun hasFFmpeg(ctx: Context): Boolean = ffmpeg(ctx).canExecute()

    /**
     * Рабочая папка: сегменты и недокачанное; экспорт уносит готовое. У режима
     * «на этом устройстве» своя: недокачанное одного режима не должно
     * подхватиться заданием другого с тем же названием папки.
     */
    fun workDir(ctx: Context): File =
        File(ctx.getExternalFilesDir(null), scopedDir("downloads", profile())).apply { mkdirs() }

    /** Чьи данные открыты (движок, вшитый или выбранный сервер): от этого зависят очередь и рабочая папка. */
    private fun profile(): String? = KinoApp.instance.container.profile

    /**
     * Ключ записи очереди. Очередь, пауза, «качается сейчас» и «сдалась» лежат в
     * одном файле настроек рядом с выбранной папкой (она общая), но у каждого
     * профиля (локальный движок, выбранный сервер) свои ключи: id серий у них не
     * совпадают, и задание одного, поднятое в другом, качало бы чужую серию.
     * У вшитого сервера (null) ключи прежние — очередь переживает обновление.
     */
    fun scoped(key: String, profile: String?): String = if (profile == null) key else "${key}_$profile"

    /** Рабочая папка профиля: `downloads`, `downloads-local`, `downloads-s_…`. */
    fun scopedDir(name: String, profile: String?): String = if (profile == null) name else "$name-$profile"

    private fun key(base: String) = scoped(base, profile())

    /**
     * Тот же адрес страницы, но на актуальном origin. Задание хранится вместе с
     * ссылкой, а у локального движка порт новый на каждый запуск приложения:
     * отложенное или недокачанное задание после перезапуска смотрело бы на
     * мёртвый порт. Часть после origin («/#/title/3/s1e1») остаётся как есть.
     */
    fun rebase(pageUrl: String, base: String): String {
        val i = pageUrl.indexOf("/#/title/")
        return if (i < 0) pageUrl else base.trimEnd('/') + pageUrl.substring(i)
    }

    /** Аргументы запуска утилиты — дословно как в старой оболочке. */
    fun baseArgs(ctx: Context, job: DownloadJob, cookie: String, userAgent: String): List<String> =
        baseArgs(binary(ctx).path, ffmpeg(ctx).path, workDir(ctx).path, job, cookie, userAgent)

    /** Чистая форма — для тестов и логов; порядок флагов тот же. */
    fun baseArgs(
        binPath: String,
        ffmpegPath: String,
        workPath: String,
        job: DownloadJob,
        cookie: String,
        userAgent: String,
    ): List<String> {
        val args = mutableListOf(
            binPath,
            "--progress-json",
            "--no-notify",
            "--ffmpeg", ffmpegPath,
        )
        if (cookie.isNotEmpty()) args += listOf("--cookie", cookie)
        if (userAgent.isNotEmpty()) args += listOf("--user-agent", userAgent)
        args += listOf("-o", workPath)
        args += listOf("--container", "mkv")
        if (job.quality.isNotEmpty()) args += listOf("-q", job.quality)
        if (job.audio.isNotEmpty()) args += listOf("--audio", job.audio.joinToString(","))
        if (job.subs.isNotEmpty()) args += listOf("--subs", job.subs.joinToString(","))
        if (job.seasons.isNotEmpty()) args += listOf("--seasons", job.seasons)
        if (job.voice.isNotEmpty()) args += listOf("--voice", job.voice)
        if (job.source.isNotEmpty()) args += listOf("--source", job.source)
        if (job.force) args += "--force"
        args += job.pageUrl
        return args
    }

    /** Процесс с окружением: HOME — состояние утилиты (докачка), TMPDIR — сегменты. */
    fun process(ctx: Context, args: List<String>): Process = ProcessBuilder(args)
        .directory(workDir(ctx))
        .apply {
            environment()["HOME"] = ctx.filesDir.path
            environment()["TMPDIR"] = ctx.cacheDir.path
            // Имена — системным резолвером Android (бинарник собран с cgo):
            // свой резолвер Go ищет /etc/resolv.conf, которого на Android нет,
            // и спрашивает 8.8.8.8 — а он у части сетей и провайдеров закрыт.
            environment()["GODEBUG"] = "netdns=cgo"
        }
        .start()

    /**
     * Причина падения по-человечески. Утилита пишет «Error: …» с переносами,
     * и последняя строка бывала одним словом «timeout».
     */
    fun lastLine(stderr: StringBuilder): String {
        val text = stderr.toString()
        // «episode failed … error=master playlist returned HTTP 403» — суть;
        // «Finished with errors: 0 succeeded, 1 failed» её не говорит.
        val failed = Regex("episode failed.*?error=(.+)").findAll(text).lastOrNull()?.groupValues?.get(1)?.trim()
        if (!failed.isNullOrEmpty()) {
            return if ("403" in failed) "площадка отказала в доступе (403): ${failed.take(160)}" else failed.take(240)
        }
        val err = text.substringAfterLast("Error:", "").lines().joinToString(" ") { it.trim() }.trim()
        return when {
            "lookup" in err || "no such host" in err -> "не нашёлся адрес сервера (DNS): ${err.takeLast(120)}"
            "connection refused" in err || "dial tcp" in err -> "нет связи с сервером: ${err.takeLast(120)}"
            err.isNotEmpty() -> err.take(240)
            else -> text.trim().lines().lastOrNull { it.isNotBlank() }?.trim() ?: ""
        }
    }

    // ---- персистентная очередь: переживает смерть службы ----

    private const val PREFS = "downloads"
    private const val KEY_QUEUE = "queue"
    private const val KEY_PAUSED = "paused_job"
    private const val KEY_RUNNING = "running_job"
    private const val KEY_FAILED = "failed_job"
    private const val KEY_FAILED_WHY = "failed_why"

    /** Сколько раз задание запускается само, прежде чем ждать «Повторить». */
    const val MAX_ATTEMPTS = 3

    /** Пауза перед автоповтором: сеть моргнула — полминуты, дальше дольше. */
    fun backoffMs(attempt: Int): Long = when (attempt) {
        0 -> 30_000L
        1 -> 2 * 60_000L
        else -> 10 * 60_000L
    }

    /** Задание, которое сдалось после всех автоповторов, — для «Повторить». */
    fun failedJob(ctx: Context): Pair<DownloadJob, String>? = try {
        val p = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        p.getString(key(KEY_FAILED), null)?.let { json.decodeFromString<DownloadJob>(it) to p.getString(key(KEY_FAILED_WHY), "").orEmpty() }
    } catch (_: Exception) {
        null
    }

    fun saveFailed(ctx: Context, job: DownloadJob?, why: String = "") {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(key(KEY_FAILED), job?.let { json.encodeToString(it) })
            .putString(key(KEY_FAILED_WHY), why)
            .apply()
    }

    /**
     * Причина сбоя по-человечески: какие серии не скачались и почему. Ошибки
     * серий приходят событиями утилиты; stderr — запасной источник (его
     * хвост, а не голова: голова — это INFO первых серий).
     */
    fun describeFailure(failed: Map<String, String>, stderrTail: String, done: Int, runError: String = ""): String {
        val head = if (done > 0) "Скачано серий: $done. " else ""
        if (failed.isNotEmpty()) {
            val (key, why) = failed.entries.first()
            val more = if (failed.size > 1) " (и ещё ${failed.size - 1})" else ""
            return "${head}Не скачалась $key$more: ${humanReason(why)}"
        }
        if (runError.isNotEmpty()) return "$head${humanReason(runError)}"
        val tail = lastLine(StringBuilder(stderrTail))
        val reason = when {
            tail.isEmpty() || Regex("^\\d{4}-\\d{2}-\\d{2} .*\\[INFO]").containsMatchIn(tail) ->
                "загрузка оборвалась (процесс остановлен системой)"
            else -> humanReason(tail)
        }
        return "$head$reason"
    }

    /** Частые ответы утилиты — по-русски; остальное как есть. */
    fun humanReason(raw: String): String {
        val r = raw.trim()
        return when {
            "input URL is invalid" in r || "not a platform title page" in r ->
                "качалка не приняла ссылку на тайтл"
            "has no episodes" in r || "is not part of title" in r || "matches the selection" in r ||
                Regex("title \\d+ \\(.*\\) has no ").containsMatchIn(r) -> "на сервере нет такой серии"
            "session" in r && ("401" in r || "Unauthorized" in r) ->
                "сервер не принял вход — откройте приложение с интернетом и повторите"
            "no variants" in r -> "площадка отдала поток, который качалка не разобрала"
            "403" in r -> "площадка отказала в доступе (403)"
            "404" in r -> "площадка не нашла поток (404)"
            "timeout" in r.lowercase() || "deadline" in r -> "площадка не ответила вовремя"
            "no such host" in r || "lookup" in r -> "нет связи: не нашёлся адрес сервера"
            "connection refused" in r || "dial tcp" in r || "network is unreachable" in r -> "нет связи с сервером"
            "no space" in r.lowercase() -> "на телефоне кончилось место"
            else -> r.take(200)
        }
    }

    /**
     * Ошибка, которую повтор не исправит: ссылка не та или такой серии нет.
     * Такое задание сдаётся сразу, без трёх автоповторов.
     */
    fun isPermanent(runError: String): Boolean =
        listOf("input URL is invalid", "not a platform title page", "is not part of title", "has no episodes", "matches the selection")
            .any { it in runError } || Regex("title \\d+ \\(.*\\) has no ").containsMatchIn(runError)

    /**
     * Чем кончился прогон утилиты. Вынесено из службы ради тестов: ошибка
     * всего прогона (`result.error`, ноль серий) — не успех, иначе фильм,
     * который не скачался вовсе, кончается уведомлением «Готово».
     */
    fun outcome(
        code: Int,
        result: DownloadEvent.Result?,
        failedKeys: Map<String, String>,
        moved: Int,
        job: DownloadJob,
        stderrTail: String,
        exportError: String = "",
    ): RunOutcome {
        if (exportError.isNotEmpty()) return RunOutcome.Failed("Не удалось сохранить файл: $exportError")
        if (failedKeys.isNotEmpty() || (result?.failed ?: 0) > 0 || (code != 0 && result == null)) {
            return RunOutcome.Failed(describeFailure(failedKeys, stderrTail, moved))
        }
        val runError = result?.error.orEmpty()
        if (runError.isNotEmpty()) {
            return RunOutcome.Failed(describeFailure(emptyMap(), stderrTail, moved, runError), isPermanent(runError))
        }
        val succeeded = result?.succeeded ?: 0
        val skipped = result?.skipped ?: 0
        if (moved == 0 && succeeded == 0) {
            return when {
                // Утилита не нашла, что качать, и не сказала почему.
                skipped == 0 -> RunOutcome.Failed(
                    lastLine(StringBuilder(stderrTail))
                        .takeIf { it.isNotEmpty() && !Regex("^\\d{4}-\\d{2}-\\d{2} .*\\[(INFO|WARN)]").containsMatchIn(it) }
                        ?.let(::humanReason) ?: "качалка ничего не скачала",
                )
                // С --force пропуск — это площадка, не отдавшая поток.
                job.force -> RunOutcome.Failed("площадка не отдала поток: все серии пропущены")
                // Человек попросил скачать, а утилита сочла всё скачанным
                // (файлы давно перенесены, может, и стёрты) — качаем
                // по-настоящему. Автоповторы так не делают: им пропуск
                // готового и нужен.
                job.attempt == 0 -> RunOutcome.Force
                else -> RunOutcome.Done(0)
            }
        }
        return RunOutcome.Done(moved)
    }

    fun loadQueue(ctx: Context): MutableList<DownloadJob> = try {
        val raw = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(key(KEY_QUEUE), null)
        if (raw.isNullOrEmpty()) mutableListOf()
        else json.decodeFromString<List<DownloadJob>>(raw).toMutableList()
    } catch (_: Exception) {
        mutableListOf()
    }

    fun saveQueue(ctx: Context, queue: List<DownloadJob>) {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit().putString(key(KEY_QUEUE), json.encodeToString(queue)).apply()
    }

    /**
     * Задание, которое качается сейчас. Из очереди оно уже вынуто, и если
     * система убьёт процесс посреди задания (HyperOS так делает: 13 серий
     * готовы, 14-я оборвалась), без этой записи оно пропадёт бесследно.
     */
    fun runningJob(ctx: Context): DownloadJob? = try {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(key(KEY_RUNNING), null)
            ?.let { json.decodeFromString<DownloadJob>(it) }
    } catch (_: Exception) {
        null
    }

    fun saveRunning(ctx: Context, job: DownloadJob?) {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(key(KEY_RUNNING), job?.let { json.encodeToString(it) })
            .commit() // процесс могут убить сразу после — запись должна лечь
    }

    fun pausedJob(ctx: Context): DownloadJob? = try {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(key(KEY_PAUSED), null)
            ?.let { json.decodeFromString<DownloadJob>(it) }
    } catch (_: Exception) {
        null
    }

    fun savePaused(ctx: Context, job: DownloadJob?) {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(key(KEY_PAUSED), job?.let { json.encodeToString(it) })
            .apply()
    }

    /** Название серии для уведомлений: S02E05. */
    fun keyOf(season: Int, episode: Int): String = "S%02dE%02d".format(season, episode)

    /** titleId из ссылки страницы. */
    fun titleIdOf(pageUrl: String): Long = LocalLibrary.titleIdOf(pageUrl)

    fun maskCookie(s: String): String =
        s.replace(Regex("kino_session=[^ ;]+"), "kino_session=…")
}

/** Итог прогона утилиты: см. [Downloader.outcome]. */
sealed class RunOutcome {
    /** Скачано (moved — перенесено серий; 0 — всё было скачано раньше). */
    data class Done(val moved: Int) : RunOutcome()
    /** Всё пропущено как скачанное — перекачать с --force. */
    object Force : RunOutcome()
    /** Сбой; permanent — повтор не поможет, автоповторов не будет. */
    data class Failed(val why: String, val permanent: Boolean = false) : RunOutcome()
}

/** Событие NDJSON-потока утилиты (типы start/episode/track/progress/stop/result). */
sealed class DownloadEvent {
    data class Start(val title: String?, val total: Int, val done: Int) : DownloadEvent()
    data class Episode(val key: String, val state: String, val error: String?) : DownloadEvent()
    data class Track(val key: String) : DownloadEvent()
    data class Progress(
        val key: String?, val percent: Int,
        val downloaded: Long, val totalBytes: Long, val approx: Boolean,
    ) : DownloadEvent()
    data class Stop(val empty: Boolean = true) : DownloadEvent()
    data class Result(
        val succeeded: Int, val failed: Int, val skipped: Int, val error: String?,
    ) : DownloadEvent()
}

/** Чистый разбор строки NDJSON; не-JSON и чужие типы — null. */
fun parseDownloadEvent(line: String, json: Json): DownloadEvent? {
    if (!line.startsWith("{")) return null
    val obj = try { json.parseToJsonElement(line).jsonObject } catch (_: Exception) { return null }
    return when (obj["type"]?.jsonPrimitive?.content) {
        "start" -> DownloadEvent.Start(
            title = obj["title"]?.jsonPrimitive?.content,
            total = obj["total"]?.jsonPrimitive?.intOrNull ?: 0,
            done = obj["done"]?.jsonPrimitive?.intOrNull ?: 0,
        )
        "episode" -> DownloadEvent.Episode(
            key = obj["key"]?.jsonPrimitive?.content ?: "",
            state = obj["state"]?.jsonPrimitive?.content ?: "",
            error = obj["error"]?.jsonPrimitive?.content,
        )
        "track" -> DownloadEvent.Track(obj["key"]?.jsonPrimitive?.content ?: "")
        "progress" -> DownloadEvent.Progress(
            key = obj["key"]?.jsonPrimitive?.content,
            percent = obj["percent"]?.jsonPrimitive?.intOrNull ?: 0,
            downloaded = obj["downloaded"]?.jsonPrimitive?.longOrNull ?: 0,
            totalBytes = obj["totalBytes"]?.jsonPrimitive?.longOrNull ?: 0,
            approx = obj["approx"]?.jsonPrimitive?.content == "true",
        )
        "stop" -> DownloadEvent.Stop()
        "result" -> DownloadEvent.Result(
            succeeded = obj["succeeded"]?.jsonPrimitive?.intOrNull ?: 0,
            failed = obj["failed"]?.jsonPrimitive?.intOrNull ?: 0,
            skipped = obj["skipped"]?.jsonPrimitive?.intOrNull ?: 0,
            error = obj["error"]?.jsonPrimitive?.content,
        )
        else -> null
    }
}
