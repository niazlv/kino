package ru.sorewa.kino.player

import android.media.MediaCodecList
import android.media.MediaFormat
import android.os.Build

/**
 * Что это устройство заявляет площадкам при запросе потока: кодеки и
 * наибольшее качество. Порт web/apps/web/src/quality.ts, только детект
 * кодеков идёт по MediaCodecList, а не по canPlayType.
 *
 * Выше 1080p у площадок бывает в разных кодеках: у kinopub — HEVC, у
 * ЛордФильма — только AV1. Заявить кодек, которого устройство не умеет, —
 * получить чёрный экран или звук без картинки, поэтому по умолчанию кодеки
 * определяются сами, а качество — не выше 1080p. Телевизор тянет 4K.
 */

object Quality {

    /** Настройка кодека: "" — определять самому, "on" — заявлять, "off" — нет. */
    val QUALITIES = intArrayOf(1080, 1440, 2160)

    private val hevcSupported: Boolean by lazy { hasDecoder(MediaFormat.MIMETYPE_VIDEO_HEVC) }
    private val av1Supported: Boolean by lazy { hasDecoder("video/x-av1") }

    private fun hasDecoder(mime: String): Boolean = try {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            MediaCodecList(MediaCodecList.REGULAR_CODECS).codecInfos.any {
                !it.isEncoder && it.supportedTypes.contains(mime)
            }
        } else false
    } catch (_: Exception) {
        false
    }

    fun detectHevc(): Boolean = hevcSupported
    fun detectAv1(): Boolean = av1Supported

    private fun claimed(c: String, detected: Boolean): Boolean = c == "on" || (c.isEmpty() && detected)

    /** Кодеки для заявки: H.264 умеют все, остальное — по настройке. */
    fun claimedCodecs(hevcClaim: String, av1Claim: String): List<String> {
        val out = mutableListOf("avc1")
        if (claimed(hevcClaim, detectHevc())) out.add("hvc1")
        if (claimed(av1Claim, detectAv1())) out.add("av01")
        return out
    }

    /** Наибольшее качество, которое заявляется площадкам. 0 — не задано: телефон 1080p, ТВ 2160. */
    fun maxQuality(maxq: Int, isTv: Boolean): Int =
        if (maxq in QUALITIES) maxq else if (isTv) 2160 else 1080
}
