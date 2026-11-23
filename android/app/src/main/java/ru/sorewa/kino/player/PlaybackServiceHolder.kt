package ru.sorewa.kino.player

import kotlinx.coroutines.flow.MutableStateFlow

/**
 * Мост к полосе пропускания реального ExoPlayer'а (оценка идёт в службе,
 * MediaController её не пересылает). Читается тикером прогресса.
 */
object PlaybackServiceHolder {
    private val bandwidth = MutableStateFlow(0L)

    fun publish(bps: Long) { if (bps > 0) bandwidth.value = bps }
    fun bandwidth(): Long = bandwidth.value
}
