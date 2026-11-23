package ru.sorewa.kino.data

import kotlinx.serialization.Serializable

/**
 * Формы ответов сервера — зеркало web/apps/web/src/api.ts и
 * internal/transport/httpuser/catalog.go. Все необязательные поля —
 * с null-дефолтами: сервер расширяет ответы, клиент не должен падать.
 */

@Serializable
data class Me(
    val id: Long,
    val login: String,
    val displayName: String,
    val role: String? = null,
)

@Serializable
data class DeviceBegin(
    val deviceCode: String,
    val userCode: String,
    val verificationUrl: String,
    val expiresAt: String,
    val interval: Long = 5,
)

@Serializable
data class DeviceAuthInfo(
    val device: String,
    val requestedAt: String,
    val expiresAt: String,
)

@Serializable
data class Page<T>(
    val items: List<T>,
    val total: Int = 0,
    val nextOffset: Int = 0,
    val hasMore: Boolean = false,
    val activeTotal: Int = 0,
    val page: Int = 0,
    val pages: Int = 0,
)

@Serializable
data class ContinueItem(
    val episodeId: Long,
    val titleId: Long,
    val title: String,
    val kind: String = "",
    val year: Int = 0,
    val hasArt: Boolean = false,
    val seasonNo: Int = 0,
    val episodeNo: Int = 0,
    val positionMs: Long = 0,
    val durationMs: Long = 0,
)

/** Площадка, с которой приходит видео. */
@Serializable
data class SourceRef(val id: String, val name: String)

@Serializable
data class Title(
    val id: Long,
    val kind: String = "",
    val title: String = "",
    val year: Int = 0,
    /** Вид произведения: anime, cartoon, dorama, series, movie. Точнее kind. */
    val species: String? = null,
    val imdb: String? = null,
    val plot: String? = null,
    val genres: List<String> = emptyList(),
    val finished: Boolean = false,
    val hasArt: Boolean = false,
    /** Площадки тайтла по приоритету; только на странице тайтла. */
    val sources: List<SourceRef> = emptyList(),
)

@Serializable
data class Episode(
    val id: Long,
    val seasonNo: Int = 0,
    val episodeNo: Int = 0,
    val title: String? = null,
    val durationSec: Long = 0,
    val positionMs: Long = 0,
    val status: String? = null,
    val hasArt: Boolean = false,
)

/**
 * Выбор в плеере для тайтла. null — не выбирал; субтитры "" — выключены;
 * площадка "" — «автоматически».
 */
@Serializable
data class Prefs(
    val audio: String? = null,
    val subtitle: String? = null,
    val source: String? = null,
)

@Serializable
data class NewEpisodeItem(
    val titleId: Long,
    val title: String,
    val kind: String = "",
    val year: Int = 0,
    val hasArt: Boolean = false,
    val unwatched: Int = 0,
    val total: Int = 0,
)

@Serializable
data class HistoryItem(
    val episodeId: Long,
    val titleId: Long,
    val title: String,
    val kind: String = "",
    val seasonNo: Int = 0,
    val episodeNo: Int = 0,
    val positionMs: Long = 0,
    val durationMs: Long = 0,
    val status: String = "",
    val hasArt: Boolean = false,
    val hasShot: Boolean = false,
    val updatedAt: String = "",
)

@Serializable
data class Track(
    val id: String,
    val language: String = "",
    val title: String = "",
    /** Только у субтитров отдельным файлом: адрес на шлюзе. */
    val url: String? = null,
    /** Включить сразу: вариант «с субтитрами». */
    val default: Boolean = false,
)

@Serializable
data class Rendition(val height: Int, val codec: String? = null)

/** Поток той же серии у той же площадки: у аниме-площадок так устроены озвучки. */
@Serializable
data class Variant(val id: String, val title: String, val player: String? = null)

@Serializable
data class PassedSource(
    val id: String,
    val name: String,
    val reason: String = "",
)

@Serializable
data class Playback(
    val ticketId: String,
    val url: String,
    val protocol: String = "HLS",
    val expiresAt: String = "",
    val audios: List<Track> = emptyList(),
    val subtitles: List<Track> = emptyList(),
    val renditions: List<Rendition> = emptyList(),
    /** Площадка, выдавшая поток, и все, у кого эта серия есть. */
    val source: SourceRef? = null,
    val sources: List<SourceRef> = emptyList(),
    /** Площадки, стоявшие в очереди раньше выдавшей, и почему поток не с них. */
    val passed: List<PassedSource> = emptyList(),
    val variants: List<Variant> = emptyList(),
    val variant: String? = null,
    /** Разметка пропуска, пришедшая вместе с потоком. Остальная — skips(). */
    val segments: List<SkipSegment> = emptyList(),
)

/**
 * Что в серии можно пропустить. Время — секунды от начала файла.
 */
@Serializable
data class SkipSegment(
    val kind: String = "skip",
    val start: Double,
    val end: Double,
    /** Длина размеченного файла, если разметка чужая (AniSkip). */
    val forDuration: Double? = null,
    val origin: String? = null,
)

@Serializable
data class VoiceOption(
    val key: String,
    val title: String,
    val ways: List<VoiceWay> = emptyList(),
)

@Serializable
data class VoiceWay(
    val source: String,
    val variant: String? = null,
    val player: String? = null,
)

/** silent — площадки, не рассказавшие о своих озвучках: список неполон. */
@Serializable
data class VoiceList(val voices: List<VoiceOption> = emptyList(), val silent: List<PassedSource> = emptyList())

/** Ключи озвучек по подписям дорожек — их считает сервер (POST /voices/keys). */
@Serializable
data class VoiceKeysRequest(val labels: List<String>)

@Serializable
data class VoiceKeyInfo(val key: String = "", val title: String = "")

@Serializable
data class VoiceKeys(val keys: Map<String, VoiceKeyInfo> = emptyMap())

/**
 * Чего хочет человек от выпуска тикета — тело /playback. Профиль устройства
 * (codecs, maxHeight, supportsHls) добавляет Api.playback.
 */
@Serializable
data class PlaybackChoice(
    /** Площадка: запомненная или выбранная только что. */
    val source: String? = null,
    /** Выбрана только что руками — не подменять другой площадкой. */
    val strict: Boolean = false,
    val variant: String? = null,
    /** Запомненная студия озвучки — подходит к любой площадке. */
    val variantLabel: String? = null,
    /** Студия из общего списка (VoiceOption.key). */
    val voice: String? = null,
    /** Площадки, чей поток на этом устройстве только что тормозил. */
    val avoid: List<String> = emptyList(),
)

@Serializable
data class Marks(
    val following: Boolean = false,
    val watchlist: Boolean = false,
    val hidden: Boolean = false,
)

@Serializable
data class TitleDetail(
    val title: Title,
    val episodes: List<Episode> = emptyList(),
    val prefs: Prefs? = null,
)

@Serializable
data class ProgressState(
    val positionMs: Long = 0,
    val durationMs: Long = 0,
    val status: String = "",
)

@Serializable
data class ProgressReport(
    val positionMs: Long,
    val durationMs: Long,
    val clientTs: String,
    /**
     * Своё устройство: с одним id сервер держит «кто последний по client_ts»,
     * с разными в окне 5 минут — «кто дальше досмотрел». Пусто/нет — сервер
     * подставит id сессии, и слияние двух устройств сломается.
     */
    val deviceId: String? = null,
)

@Serializable
data class ShareLink(
    val token: String,
    val kind: String,
    val url: String,
    val command: String? = null,
    val expiresAt: String,
)

@Serializable
data class SearchHistoryRsp(val items: List<String> = emptyList())

/** Одно переключение за раз: поле, которого нет (null), не меняется. */
@Serializable
data class PrefsPatch(
    val audio: String? = null,
    val subtitle: String? = null,
    val source: String? = null,
)

/** Площадка в /engine/status: ready — подключена, starting — подключается, skipped — отключена по reason. */
@Serializable
data class EngineProviderStatus(val id: String, val state: String, val reason: String = "")

@Serializable
data class EngineHostCount(val host: String, val count: Int)

/** Сколько запросов движок отправил на внешние сайты с запуска. */
@Serializable
data class EngineRequests(
    val total: Int = 0,
    val since: String = "",
    val hosts: List<EngineHostCount> = emptyList(),
)

/** С какой подписью движок ходит наружу: по умолчанию и где человек задал свою ("*" — для всех). */
@Serializable
data class UserAgentStatus(val default: String = "", val custom: List<String> = emptyList())

/** Вход в kinopub: есть ли сессия и есть ли чем входить (задан секрет клиента). */
@Serializable
data class KinopubStatus(val session: Boolean = false, val canLogin: Boolean = false)

/** Что показать человеку, чтобы он подтвердил вход на kino.pub. Секретный код опроса остаётся в движке. */
@Serializable
data class KinopubLoginCode(
    val userCode: String,
    val verificationUri: String = "",
    val verificationUriComplete: String = "",
    val expiresAt: String = "",
    val intervalSec: Int = 5,
)

/** Где вход: none | pending | ok | expired | denied | error (message — причина). */
@Serializable
data class KinopubLoginState(val state: String = "none", val message: String = "")

@Serializable
data class EngineStatus(
    val startedAt: String = "",
    val providers: List<EngineProviderStatus> = emptyList(),
    val routes: List<String> = emptyList(),
    val requests: EngineRequests = EngineRequests(),
    val kinopub: KinopubStatus = KinopubStatus(),
    val userAgent: UserAgentStatus = UserAgentStatus(),
)

// ---- площадки локального движка (/engine/sources, ADR-0017) ----

/** Настройка площадки: ключ или адрес, значение уходит движку переменной [env]. */
@Serializable
data class EngineSourceSetting(
    val env: String = "",
    val label: String = "",
    val help: String = "",
    val secret: Boolean = false,
    val required: Boolean = false,
    val advanced: Boolean = false,
)

/**
 * Площадка, как её знает движок: на Go (builtin), скрипт из сборки (bundled) или
 * поставленный на устройстве (local). Состояние — у включённых.
 */
@Serializable
data class EngineSourceInfo(
    val id: String = "",
    val name: String = "",
    val note: String = "",
    val kind: String = "",
    val lookup: Boolean = false,
    val egress: String = "",
    val settings: List<EngineSourceSetting> = emptyList(),
    val hidden: Boolean = false,
    val origin: String = "",
    val version: String = "",
    val enabled: Boolean = false,
    val state: String = "",
    val reason: String = "",
    val file: String = "",
    val hash: String = "",
    /** Что заменила местная правка: builtin или bundled. */
    val overrides: String = "",
    val removable: Boolean = false,
    /** Местный файл не разобрался — почему. */
    val broken: String = "",
    /** На диске другой файл, чем работает: нужен перезапуск движка. */
    val pending: Boolean = false,
) {
    val isScript: Boolean get() = origin == "bundled" || origin == "local" || file.isNotEmpty()
}

@Serializable
data class EngineSourceList(val sources: List<EngineSourceInfo> = emptyList(), val dir: String = "")

@Serializable
data class EngineSourceInstalled(
    val id: String = "",
    val name: String = "",
    val version: String = "",
    val hash: String = "",
    val restart: Boolean = false,
)

@Serializable
data class EngineSourceCheckReq(
    val id: String = "",
    val source: String = "",
    val query: String = "",
    val want: String = "",
    val fetch: Boolean = true,
)

@Serializable
data class EngineSourceCheck(val failed: Boolean = true, val text: String = "")
