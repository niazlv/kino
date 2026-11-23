package ru.sorewa.kino.engine

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.withTimeoutOrNull
import java.io.File
import java.net.HttpURLConnection
import java.net.Proxy
import java.net.URL

/** Запущенный процесс движка — ровно то, что нужно надзирателю. */
interface EngineProcess {
    fun isAlive(): Boolean

    /** Блокирует до выхода процесса; прерывается через interrupt. */
    fun waitFor(): Int

    fun destroy()
}

fun interface EngineLauncher {
    fun launch(env: Map<String, String>): EngineProcess
}

fun interface HealthProbe {
    fun ok(port: Int): Boolean
}

/** Движок отвечает на /api/v1/health — и этого довольно: токена проба не знает. */
object HttpHealthProbe : HealthProbe {
    override fun ok(port: Int): Boolean = try {
        val c = URL(EngineLaunch.baseUrl(port) + EngineLaunch.HEALTH_PATH)
            .openConnection(Proxy.NO_PROXY) as HttpURLConnection
        c.connectTimeout = 400
        c.readTimeout = 800
        try { c.responseCode == 200 } finally { c.disconnect() }
    } catch (_: Exception) {
        false
    }
}

/**
 * Настоящий запуск: бинарь из каталога нативных библиотек, вывод — в файл
 * журнала. stdin остаётся открытым в нашей руке: пока приложение живо, пайп
 * держится, умерло — закрылся, и движок уходит сам (ENGINE_EXIT_ON_STDIN_EOF).
 */
class ProcessEngineLauncher(
    private val binary: File,
    private val workDir: File,
    private val logFile: File,
) : EngineLauncher {

    override fun launch(env: Map<String, String>): EngineProcess {
        // Журнал не растёт вечно: перед новым запуском режется до хвоста.
        if (logFile.length() > MAX_LOG_BYTES) logFile.writeText("")
        val pb = ProcessBuilder(binary.path)
            .directory(workDir)
            .redirectErrorStream(true)
            .redirectOutput(ProcessBuilder.Redirect.appendTo(logFile))
        pb.environment().putAll(env)
        val p = pb.start()
        return object : EngineProcess {
            // Ссылка на поток stdin держит пайп открытым, пока жив этот объект.
            private val stdin = p.outputStream
            override fun isAlive() = p.isAlive
            override fun waitFor() = p.waitFor()
            override fun destroy() {
                try { stdin.close() } catch (_: Exception) { }
                p.destroy()
            }
        }
    }

    private companion object {
        const val MAX_LOG_BYTES = 1_000_000L
    }
}

/**
 * Локальный движок: поднимает процесс, ждёт готовности, перезапускает при
 * смерти на том же порту (адрес приложения не меняется), по [stop] убирает.
 *
 * Порт задан заранее: адрес движка известен приложению ещё до первого запуска,
 * поэтому Api строится сразу и не знает о перезапусках. Окружение (настройки
 * человека) — функцией: каждый запуск читает его заново.
 */
class LocalEngine(
    val port: Int,
    /** Окружение читается при каждом запуске: сменили настройки — restart() их подхватит. */
    private val env: () -> Map<String, String>,
    private val launcher: EngineLauncher,
    private val scope: CoroutineScope,
    private val probe: HealthProbe = HttpHealthProbe,
    private val readyTimeoutMs: Long = 20_000,
    private val maxRestarts: Int = 6,
    private val backoffMs: (Int) -> Long = { minOf(300L shl it, 4_000L) },
    private val log: (String) -> Unit = {},
) {
    enum class State { STOPPED, STARTING, READY, FAILED }

    private val _state = MutableStateFlow(State.STOPPED)
    val state: StateFlow<State> = _state

    val baseUrl: String = EngineLaunch.baseUrl(port)

    private val lock = Any()
    private var job: Job? = null

    /**
     * Поколение надзора. stop() и restart() его сдвигают, и корутина прошлого
     * поколения, дожившая до своего finally, ни состояние, ни чужой процесс
     * трогать не вправе.
     */
    private var generation = 0

    @Volatile private var current: EngineProcess? = null

    /** Запускает надзор. Повторный вызов при живом надзоре ничего не делает. */
    fun start() {
        synchronized(lock) {
            if (job?.isActive == true) return
            val g = ++generation
            _state.value = State.STARTING
            job = scope.launch(Dispatchers.IO) { supervise(g) }
        }
    }

    /** «Повторить» после провала: надзор начинается заново с чистым счётчиком. */
    fun restart() {
        stop()
        start()
    }

    fun stop() {
        synchronized(lock) {
            generation++
            job?.cancel()
            job = null
            current?.destroy()
            current = null
            _state.value = State.STOPPED
        }
    }

    /** true — движок отвечает; false — не поднялся за [timeoutMs] или сдался. */
    suspend fun awaitReady(timeoutMs: Long): Boolean =
        withTimeoutOrNull(timeoutMs) {
            _state.first { it == State.READY || it == State.FAILED }
        } == State.READY

    private fun alive(g: Int) = synchronized(lock) { g == generation }

    private fun setState(g: Int, s: State) {
        synchronized(lock) { if (g == generation) _state.value = s }
    }

    private suspend fun supervise(g: Int) {
        var restarts = 0
        var proc: EngineProcess? = null
        try {
            while (currentCoroutineContext().isActive && alive(g)) {
                setState(g, State.STARTING)
                proc = try {
                    launcher.launch(env())
                } catch (e: Exception) {
                    log("запуск не вышел: ${e.message}")
                    null
                }
                var readySince = 0L
                val p = proc
                if (p != null) {
                    synchronized(lock) { if (g == generation) current = p }
                    if (waitHealthy(p)) {
                        setState(g, State.READY)
                        readySince = System.nanoTime()
                        log("движок готов на порту $port")
                    }
                    // Живой процесс держим, пока сам не выйдет (или его не убьёт stop).
                    if (p.isAlive()) runInterruptible { p.waitFor() }
                    log("процесс движка вышел")
                    synchronized(lock) { if (current === p) current = null }
                }
                if (!alive(g)) break
                // Счётчик обнуляет только долгая жизнь: движок, падающий сразу
                // после «готов», иначе перезапускался бы вечно.
                if (readySince != 0L && System.nanoTime() - readySince > STABLE_NS) restarts = 0
                if (++restarts > maxRestarts) {
                    log("движок не удалось удержать — сдаёмся")
                    setState(g, State.FAILED)
                    return
                }
                setState(g, State.STARTING)
                delay(backoffMs(restarts - 1))
            }
        } finally {
            // Только свой процесс: чужое поколение уже запустило новый.
            proc?.destroy()
            synchronized(lock) {
                if (current === proc) current = null
                if (g == generation && _state.value != State.FAILED) _state.value = State.STOPPED
            }
        }
    }

    private suspend fun waitHealthy(proc: EngineProcess): Boolean {
        val deadline = System.nanoTime() + readyTimeoutMs * 1_000_000
        while (System.nanoTime() < deadline) {
            if (!proc.isAlive()) return false
            if (probe.ok(port)) return true
            delay(120)
        }
        // Завис, не открыв порт: убираем — надзор запустит заново.
        proc.destroy()
        return false
    }

    private companion object {
        /** Столько проработавший движок считается устойчивым. */
        const val STABLE_NS = 10_000L * 1_000_000
    }
}
