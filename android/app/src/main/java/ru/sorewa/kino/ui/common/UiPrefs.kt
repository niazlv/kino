package ru.sorewa.kino.ui

import android.content.Context
import androidx.compose.runtime.mutableStateOf
import ru.sorewa.kino.KinoApp

/**
 * Оформление телефона — настройки этого устройства, а не аккаунта: на сервер
 * не уходят. Читаются в композиции, поэтому смена видна сразу, без перезапуска.
 */
object UiPrefs {
    private const val PREFS = "ui"
    private const val K_CAPSULE = "capsule_bar"
    private const val K_SWIPE_BACK = "swipe_back"

    private val prefs by lazy { KinoApp.instance.getSharedPreferences(PREFS, Context.MODE_PRIVATE) }
    private val capsule by lazy { mutableStateOf(prefs.getBoolean(K_CAPSULE, true)) }
    private val swipe by lazy { mutableStateOf(prefs.getBoolean(K_SWIPE_BACK, true)) }

    /** Нижняя панель плавающей капсулой (как в iOS 26) или во всю ширину (Material 3). */
    var capsuleBar: Boolean
        get() = capsule.value
        set(v) {
            capsule.value = v
            prefs.edit().putBoolean(K_CAPSULE, v).apply()
        }

    /** «Назад» свайпом вправо с любого места страницы (см. swipeBack). */
    var swipeBack: Boolean
        get() = swipe.value
        set(v) {
            swipe.value = v
            prefs.edit().putBoolean(K_SWIPE_BACK, v).apply()
        }
}
