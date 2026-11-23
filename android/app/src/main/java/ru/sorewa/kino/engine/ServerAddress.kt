package ru.sorewa.kino.engine

import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import java.net.IDN
import java.net.URI
import java.security.MessageDigest

/**
 * Адрес сервера kino platform, который человек вводит сам («свой сервер»).
 *
 * Сервер не зашит: приложение одно, а серверов сколько угодно — свой у семьи,
 * у друзей, у сообщества. Всё, что приложение знает о сервере, — его origin;
 * каждый ответ API строится от него.
 */
object ServerAddress {

    /**
     * Ввод человека → origin сервера или null, если это не адрес.
     *
     * «kino.example.com» → «https://kino.example.com»: без схемы — https.
     * Путь, запрос и фрагмент отбрасываются: человек вставит ссылку на тайтл
     * (`…/#/title/42`) или на админку, а платформа живёт в корне домена —
     * витрина и ссылки шлюза (`/stream/…`) строятся от корня. Порт по
     * умолчанию для схемы опускается: один сервер — один адрес и одно
     * хранилище, как бы его ни написали.
     */
    fun normalize(input: String): String? {
        // Пробелы — и по краям, и перед отброшенным фрагментом (вставили «адрес #…»).
        var s = input.trim().substringBefore('#').trim()
        if (s.isEmpty()) return null
        if (!s.contains("://")) s = "https://$s"
        s = asciiHost(s) ?: return null
        val uri = try { URI(s) } catch (_: Exception) { return null }
        val scheme = uri.scheme?.lowercase() ?: return null
        if (scheme != "http" && scheme != "https") return null
        // URI.host == null у адресов, которые Java не считает хостом
        // (подчёркивания, пробелы): такие не откроет и OkHttp.
        val host = uri.host?.lowercase()?.takeIf { it.isNotEmpty() } ?: return null
        val port = uri.port
        val portPart = if (port == -1 || port == defaultPort(scheme)) "" else ":$port"
        return "$scheme://$host$portPart"
    }

    /** Сервер без шифрования: пароль и история видны всем в сети по дороге. */
    fun isInsecure(url: String): Boolean = url.startsWith("http://")

    /**
     * Можно ли к этому серверу без шифрования. https — всегда; http — только к
     * серверу домашней сети ([isLocalHost]): там свой сервер часто живёт без
     * сертификата, а пароль по чужим сетям открытым текстом не ходит. Сервер в
     * интернете — только https (сертификат бесплатно выдаёт тот же Caddy).
     *
     * Сеть Android пускает http куда угодно (network_security_config не умеет
     * диапазоны адресов) — поэтому правило держит само приложение, на выборе
     * сервера. Тот же список — в iOS (ServerAddress.isLocalHost).
     */
    fun allowsCleartext(url: String): Boolean {
        if (!isInsecure(url)) return true
        val host = try { URI(url).host } catch (_: Exception) { null } ?: return false
        return isLocalHost(host)
    }

    /**
     * Адрес домашней сети: частные и служебные IP (10/8, 172.16/12, 192.168/16,
     * 127/8, 169.254/16, 100.64/10 — CGNAT и Tailscale; IPv6 ::1, fc00::/7,
     * fe80::/10), имя без точки (`nas`) и домашние зоны (.local, .lan, .home,
     * .home.arpa, .internal, .localhost). DNS не спрашивается: решает запись адреса.
     */
    fun isLocalHost(rawHost: String): Boolean {
        val host = rawHost.lowercase().trim('[', ']').trimEnd('.')
        if (host.isEmpty()) return false
        ipv4(host)?.let { return isLocalV4(it) }
        if (host.contains(':')) {
            val a = try { java.net.InetAddress.getByName(host) } catch (_: Exception) { return false }
            if (a is java.net.Inet4Address) return isLocalV4(a.address.map { it.toInt() and 0xff })
            val b = a.address
            return a.isLoopbackAddress || a.isLinkLocalAddress || (b[0].toInt() and 0xfe) == 0xfc
        }
        if (!host.contains('.')) return true
        return LOCAL_ZONES.any { host.endsWith(".$it") }
    }

    private val LOCAL_ZONES = listOf("local", "lan", "home", "home.arpa", "internal", "localhost")

    private fun ipv4(host: String): List<Int>? {
        val parts = host.split('.')
        if (parts.size != 4) return null
        return parts.map { p -> p.toIntOrNull()?.takeIf { p.length in 1..3 && it in 0..255 } ?: return null }
    }

    private fun isLocalV4(o: List<Int>): Boolean = when {
        o[0] == 10 || o[0] == 127 -> true
        o[0] == 172 && o[1] in 16..31 -> true
        o[0] == 192 && o[1] == 168 -> true
        o[0] == 169 && o[1] == 254 -> true
        o[0] == 100 && o[1] in 64..127 -> true
        else -> false
    }

    /**
     * Как показать адрес человеку: https — просто хост; http — со схемой,
     * чтобы незащищённое соединение было видно.
     */
    fun display(url: String): String = if (isInsecure(url)) url else url.removePrefix("https://")

    /**
     * Профиль данных сервера: всё, что ссылается на id серий и тайтлов
     * (сессия, очередь отметок, опись скачанного, очередь загрузок), у каждого
     * сервера своё — id у серверов разные, и отметка, набранная для одного,
     * легла бы на чужую серию другого.
     *
     * null — сервер, вшитый в сборку: его данные лежат там же, где до
     * появления выбора сервера, и установленное приложение после обновления
     * ничего не теряет. Остальные — `s_<12 hex sha-256 адреса>`.
     */
    fun profileOf(url: String, builtIn: String): String? {
        if (url == normalize(builtIn)) return null
        val d = MessageDigest.getInstance("SHA-256").digest(url.toByteArray())
        return "s_" + d.take(6).joinToString("") { "%02x".format(it) }
    }

    private fun defaultPort(scheme: String) = if (scheme == "https") 443 else 80

    /**
     * Кириллический домен (кино.рф) — в punycode: java.net.URI принимает
     * только ASCII-хосты. IPv6 в скобках и ASCII-хосты не трогаем.
     */
    private fun asciiHost(s: String): String? {
        val start = s.indexOf("://") + 3
        val end = s.indexOfAny(charArrayOf('/', '?'), start).let { if (it < 0) s.length else it }
        val authority = s.substring(start, end)
        if (authority.startsWith("[") || authority.all { it.code < 128 }) return s
        val host = authority.substringBefore(':')
        val port = authority.substring(host.length)
        val ascii = try { IDN.toASCII(host, IDN.USE_STD3_ASCII_RULES) } catch (_: IllegalArgumentException) { return null }
        return s.substring(0, start) + ascii + port + s.substring(end)
    }
}

/** Сервер, на котором уже бывали: быстрый возврат без набора адреса. */
@Serializable
data class KnownServer(val url: String, val name: String = "")

/** Список известных серверов в настройках: свежие первыми, без повторов. */
object KnownServers {
    private val json = Json { ignoreUnknownKeys = true }
    private val ser = ListSerializer(KnownServer.serializer())
    const val LIMIT = 8

    fun decode(raw: String?): List<KnownServer> =
        if (raw.isNullOrEmpty()) emptyList()
        else try { json.decodeFromString(ser, raw) } catch (_: Exception) { emptyList() }

    fun encode(list: List<KnownServer>): String = json.encodeToString(ser, list)

    /**
     * Поднять сервер наверх. Имя, если новое пустое, остаётся прежним:
     * старый сервер без визитки не должен стирать имя, узнанное раньше.
     */
    fun remember(list: List<KnownServer>, url: String, name: String): List<KnownServer> {
        val prev = list.firstOrNull { it.url == url }
        val entry = KnownServer(url, name.ifEmpty { prev?.name.orEmpty() })
        return (listOf(entry) + list.filter { it.url != url }).take(LIMIT)
    }

    fun forget(list: List<KnownServer>, url: String): List<KnownServer> = list.filter { it.url != url }
}
