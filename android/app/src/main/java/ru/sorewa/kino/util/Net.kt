package ru.sorewa.kino.util

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import ru.sorewa.kino.KinoApp

/** Активная сеть с подтверждённым интернетом; нет сети — офлайн-кнопка сразу. */
fun isOnline(): Boolean {
    val cm = KinoApp.instance.getSystemService(Context.CONNECTIVITY_SERVICE)
        as? ConnectivityManager ?: return true
    val net = cm.activeNetwork ?: return false
    val caps = cm.getNetworkCapabilities(net) ?: return false
    return caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
}

/**
 * Есть ли вообще сеть (не проверяя интернет). Нет — режим полёта или всё
 * выключено: ждать сервер бессмысленно. Проверка интернета (VALIDATED) тут
 * не годится — она ложно молчит за VPN и в сетях, где не открывается Google.
 */
fun hasNetwork(): Boolean {
    val cm = KinoApp.instance.getSystemService(Context.CONNECTIVITY_SERVICE)
        as? ConnectivityManager ?: return true
    return cm.activeNetwork != null
}
