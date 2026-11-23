package ru.sorewa.kino.engine

import kotlinx.coroutines.runBlocking
import okhttp3.Interceptor
import okhttp3.Response
import java.io.IOException

/**
 * Ворота перед сетью в локальном режиме: запрос ждёт, пока движок поднимется.
 *
 * Движок стартует вместе с приложением и первые секунды ещё не слушает порт;
 * без ворот каждый экран получал бы «отказ в соединении» и рисовал ошибку
 * там, где надо чуть подождать. Не поднялся вовсе — [IOException], то есть
 * «нет связи»: интерфейс покажет её так же, как обрыв сети.
 */
class EngineGate(
    private val engine: LocalEngine,
    private val waitMs: Long = 25_000,
) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        // Надзор запускается лениво: первый же запрос не должен зависеть от
        // того, успел ли контейнер его включить.
        if (engine.state.value == LocalEngine.State.STOPPED) engine.start()
        if (engine.state.value != LocalEngine.State.READY) {
            val ready = runBlocking { engine.awaitReady(waitMs) }
            if (!ready) throw IOException("локальный движок не отвечает")
        }
        return chain.proceed(chain.request())
    }
}
