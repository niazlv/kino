package ru.sorewa.kino.engine

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.atomic.AtomicBoolean

class LocalEngineTest {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val launcher = FakeLauncher()
    private val healthy = AtomicBoolean(true)

    @After fun tearDown() { scope.cancel() }

    private val envNow = java.util.concurrent.atomic.AtomicReference(mapOf("ENGINE_ADDR" to "127.0.0.1:4321"))

    private fun engine(
        readyTimeoutMs: Long = 600,
        maxRestarts: Int = 3,
    ) = LocalEngine(
        port = 4321,
        env = { envNow.get() },
        launcher = launcher,
        scope = scope,
        probe = { healthy.get() },
        readyTimeoutMs = readyTimeoutMs,
        maxRestarts = maxRestarts,
        backoffMs = { 10 },
    )

    @Test fun `старт доводит движок до готовности`() = runBlocking {
        val e = engine()
        assertEquals(LocalEngine.State.STOPPED, e.state.value)
        e.start()
        assertTrue(e.awaitReady(2000))
        assertEquals(LocalEngine.State.READY, e.state.value)
        assertEquals(1, launcher.launches.get())
        assertEquals("http://127.0.0.1:4321", e.baseUrl)
    }

    @Test fun `повторный start не запускает второй процесс`() = runBlocking {
        val e = engine()
        e.start(); e.start(); e.start()
        assertTrue(e.awaitReady(2000))
        assertEquals(1, launcher.launches.get())
    }

    @Test fun `движок умер сам — надзор поднимает его на том же окружении`() = runBlocking {
        val e = engine()
        e.start()
        assertTrue(e.awaitReady(2000))
        launcher.procs[0].die()
        assertTrue("второй запуск", eventually { launcher.launches.get() == 2 })
        assertTrue("снова готов", eventually { e.state.value == LocalEngine.State.READY })
        // Порт и токен прежние: адрес приложения от перезапусков не зависит.
        assertEquals(launcher.envs[0], launcher.envs[1])
    }

    @Test fun `stop убирает процесс и не даёт ему вернуться`() = runBlocking {
        val e = engine()
        e.start()
        assertTrue(e.awaitReady(2000))
        e.stop()
        assertTrue(launcher.procs[0].destroyed.get())
        assertEquals(LocalEngine.State.STOPPED, e.state.value)
        Thread.sleep(200)
        assertEquals("после stop перезапуска нет", 1, launcher.launches.get())
        assertEquals(LocalEngine.State.STOPPED, e.state.value)
    }

    @Test fun `движок падает сразу после запуска — через maxRestarts сдаёмся`() = runBlocking {
        launcher.bornDead = true
        val e = engine(maxRestarts = 3)
        e.start()
        assertFalse("не поднялся", e.awaitReady(3000))
        assertEquals(LocalEngine.State.FAILED, e.state.value)
        // Первый запуск и три перезапуска — и хватит: без предела падающий движок
        // крутился бы вечно, съедая батарею.
        assertEquals(4, launcher.launches.get())
    }

    @Test fun `бинаря нет — запуск падает, это тоже провал, а не вечный цикл`() = runBlocking {
        launcher.failLaunch = true
        val e = engine(maxRestarts = 2)
        e.start()
        assertFalse(e.awaitReady(3000))
        assertEquals(LocalEngine.State.FAILED, e.state.value)
        assertEquals(3, launcher.launches.get())
    }

    @Test fun `после провала restart начинает с чистого листа`() = runBlocking {
        launcher.bornDead = true
        val e = engine(maxRestarts = 1)
        e.start()
        assertFalse(e.awaitReady(2000))
        assertEquals(LocalEngine.State.FAILED, e.state.value)

        launcher.bornDead = false
        e.restart()
        assertTrue("«Повторить» оживляет движок", e.awaitReady(2000))
        assertEquals(LocalEngine.State.READY, e.state.value)
    }

    @Test fun `завис, не открыв порт — убираем и пробуем снова`() = runBlocking {
        healthy.set(false)
        val e = engine(readyTimeoutMs = 120, maxRestarts = 5)
        e.start()
        assertTrue("первый процесс убран по таймауту", eventually { launcher.procs.isNotEmpty() && launcher.procs[0].destroyed.get() })
        healthy.set(true)
        assertTrue("со второй попытки готов", e.awaitReady(3000))
        assertTrue(launcher.launches.get() >= 2)
    }

    @Test fun `awaitReady по таймауту говорит нет, не зависая`() = runBlocking {
        healthy.set(false)
        val e = engine(readyTimeoutMs = 5000)
        e.start()
        val t0 = System.nanoTime()
        assertFalse(e.awaitReady(150))
        assertTrue("ждали около 150 мс, а не секунды", (System.nanoTime() - t0) / 1_000_000 < 1500)
    }

    @Test fun `restart подряд не портит состояние (гонка поколений)`() = runBlocking {
        val e = engine()
        e.start()
        assertTrue(e.awaitReady(2000))
        repeat(5) { e.restart() }
        assertTrue("после серии restart движок готов", e.awaitReady(3000))
        // Старые поколения дожили до своих finally — они не вправе сбросить
        // состояние нового или убить его процесс.
        Thread.sleep(250)
        assertEquals(LocalEngine.State.READY, e.state.value)
        val last = launcher.procs.last()
        assertFalse("живой процесс не убит чужим finally", last.destroyed.get())
        e.stop()
        assertTrue(last.destroyed.get())
    }

    @Test fun `restart читает окружение заново — так настройки вступают в силу`() = runBlocking {
        val e = engine()
        e.start()
        assertTrue(e.awaitReady(2000))
        assertEquals("anilibria", launcher.envs[0]["PROVIDERS"] ?: "anilibria")

        // Человек поменял настройки: следующий запуск обязан получить новое окружение.
        envNow.set(mapOf("ENGINE_ADDR" to "127.0.0.1:4321", "PROVIDERS" to "anilibria,yummy"))
        e.restart()
        assertTrue(e.awaitReady(2000))
        assertEquals("anilibria,yummy", launcher.envs.last()["PROVIDERS"])
        assertEquals(2, launcher.launches.get())
    }
}
