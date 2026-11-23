package ru.sorewa.kino.engine

import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

/** Процесс-двойник: «живёт», пока его не убьют; waitFor блокируется, как у настоящего. */
class FakeProc : EngineProcess {
    private val gone = CountDownLatch(1)

    @Volatile var alive = true
    val destroyed = AtomicBoolean(false)

    override fun isAlive() = alive

    override fun waitFor(): Int {
        gone.await()
        return 0
    }

    override fun destroy() {
        destroyed.set(true)
        die()
    }

    /** Процесс умер сам (не по нашей команде). */
    fun die() {
        alive = false
        gone.countDown()
    }
}

/** Запуск-двойник: считает запуски, помнит процессы, умеет падать при запуске. */
class FakeLauncher : EngineLauncher {
    val launches = AtomicInteger()
    val procs = CopyOnWriteArrayList<FakeProc>()
    val envs = CopyOnWriteArrayList<Map<String, String>>()

    @Volatile var failLaunch = false

    /** Новые процессы рождаются мёртвыми — движок падает сразу после запуска. */
    @Volatile var bornDead = false

    override fun launch(env: Map<String, String>): EngineProcess {
        launches.incrementAndGet()
        envs += env
        if (failLaunch) throw java.io.IOException("нет бинаря")
        val p = FakeProc()
        if (bornDead) p.die()
        procs += p
        return p
    }
}

/** Ждёт условия до [ms] мс; true — дождались. */
fun eventually(ms: Long = 3000, cond: () -> Boolean): Boolean {
    val end = System.nanoTime() + ms * 1_000_000
    while (System.nanoTime() < end) {
        if (cond()) return true
        Thread.sleep(10)
    }
    return cond()
}
