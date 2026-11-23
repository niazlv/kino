package ru.sorewa.kino.tvhome

import android.content.ActivityNotFoundException
import android.content.ContentResolver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.util.Log
import androidx.activity.result.ActivityResultLauncher
import androidx.annotation.RequiresApi
import androidx.core.content.ContextCompat
import androidx.core.graphics.drawable.toBitmap
import androidx.tvprovider.media.tv.PreviewChannel
import androidx.tvprovider.media.tv.PreviewChannelHelper
import androidx.tvprovider.media.tv.PreviewProgram
import androidx.tvprovider.media.tv.TvContractCompat
import androidx.tvprovider.media.tv.TvContractCompat.PreviewPrograms
import androidx.tvprovider.media.tv.TvContractCompat.WatchNextPrograms
import androidx.tvprovider.media.tv.WatchNextProgram
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import ru.sorewa.kino.R
import ru.sorewa.kino.data.Api
import ru.sorewa.kino.data.ContinueItem
import ru.sorewa.kino.data.NewEpisodeItem
import ru.sorewa.kino.data.parseIsoTime
import ru.sorewa.kino.ui.describeContinue
import ru.sorewa.kino.ui.plural
import ru.sorewa.kino.ui.splitTitle

/**
 * Главный экран Android TV — как у официального Kino.Watch:
 *
 *  - системная строка «Продолжить просмотр» (Watch Next): то же, что лента
 *    «Продолжить» приложения (`/continue`), с позицией и длительностью;
 *    карточка сразу запускает серию;
 *  - свой канал «Новые серии» (`/new-episodes?only=new`): тайтлы, за которыми
 *    человек следит и где есть непросмотренное; карточка открывает тайтл.
 *    На главный экран канал попадает только с согласия — системный диалог
 *    «Добавить канал?» (один раз сам, дальше — из «Профиля»).
 *
 * Только телевизор (leanback и системный TvProvider, Android 8+): на
 * телефоне всё это — пустые вызовы. Строки и канал — только свои
 * (провайдер сам ограничивает приложение его пакетом); выход из аккаунта
 * стирает всё ([clear]): на общем телевизоре чужое «продолжить» не остаётся.
 */
object TvLauncher {

    private const val TAG = "KinoTvHome"
    private const val PREFS = "tvhome"
    private const val K_ASKED = "channel_asked"

    /** Метка своих строк: всё остальное в провайдере не наше и не трогается. */
    private const val KEY_PREFIX = "kino-t"
    private const val CHANNEL_KEY = "kino-new-episodes"

    /** Сколько карточек отдавать: длинный хвост на главном экране никто не листает. */
    private const val MAX_NEXT = 15
    private const val MAX_NEW = 20

    /** Синхронизация и очистка — по одной: выход посреди обновления не должен оставить хвостов. */
    private val mutex = Mutex()

    fun supported(context: Context): Boolean =
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.O &&
            context.packageManager.hasSystemFeature(PackageManager.FEATURE_LEANBACK) &&
            context.packageManager.resolveContentProvider(TvContractCompat.AUTHORITY, 0) != null

    /** Что опубликовано: сколько карточек в строке и канале, id канала (0 — нет). */
    data class Synced(val next: Int = 0, val fresh: Int = 0, val channelId: Long = 0)

    enum class ChannelState { NONE, HIDDEN, SHOWN }

    /**
     * Приводит главный экран к состоянию сервера. Бросает [ru.sorewa.kino.data.ApiError]
     * (сеть, сессия) — тогда опубликованное остаётся как было.
     */
    @RequiresApi(Build.VERSION_CODES.O)
    suspend fun sync(context: Context, api: Api): Synced = withContext(Dispatchers.IO) {
        mutex.withLock {
            if (api.currentToken.isEmpty()) {
                clearLocked(context)
                return@withLock Synced()
            }
            val cont = api.continueWatching().items.take(MAX_NEXT)
            val fresh = api.newEpisodes(onlyNew = true).items.take(MAX_NEW)
            // Когда человек трогал тайтл в последний раз — для порядка в
            // строке «Продолжить просмотр»: лаунчер сортирует карточки всех
            // приложений по этому времени, и «сейчас» при каждом обновлении
            // выталкивало бы наши наверх без повода.
            val touched = HashMap<Long, Long>()
            try {
                for (h in api.history(1, null, null).items) {
                    val t = parseIsoTime(h.updatedAt)
                    if (t > (touched[h.titleId] ?: 0)) touched[h.titleId] = t
                }
            } catch (e: ru.sorewa.kino.data.ApiError) {
                if (e.sessionDead) throw e
            }

            val art = TvArt(context, api)
            val nextPics = cont.associate { it.titleId to (art.shot(it.episodeId) ?: art.posterIf(it.titleId, it.hasArt)) }
            val freshPics = fresh.associate { it.titleId to art.posterIf(it.titleId, it.hasArt) }

            // Вышли, пока ходили на сервер, — публиковать уже нечего.
            if (api.currentToken.isEmpty()) {
                clearLocked(context)
                return@withLock Synced()
            }
            publishWatchNext(context, cont, nextPics, touched)
            val channelId = publishChannel(context, fresh, freshPics)
            art.prune()
            Log.i(TAG, "главный экран: продолжить ${cont.size}, новые серии ${fresh.size}, канал $channelId")
            Synced(cont.size, fresh.size, channelId)
        }
    }

    /** Стереть всё своё с главного экрана: строки, канал, картинки. */
    suspend fun clear(context: Context) {
        if (!supported(context)) return
        withContext(Dispatchers.IO) { mutex.withLock { clearLocked(context) } }
    }

    private fun clearLocked(context: Context) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        try {
            val cr = context.contentResolver
            for (p in ownWatchNext(cr).values) cr.delete(TvContractCompat.buildWatchNextProgramUri(p.id), null, null)
            val helper = PreviewChannelHelper(context)
            for (ch in helper.allChannels) {
                for (p in channelPrograms(cr, ch.id).values) helper.deletePreviewProgram(p.id)
                helper.deletePreviewChannel(ch.id)
            }
        } catch (e: Exception) {
            Log.w(TAG, "очистка главного экрана", e)
        }
        TvArt.wipe(context)
        // Следующий вход (может, другого человека) снова спросит про канал.
        prefs(context).edit().remove(K_ASKED).apply()
    }

    // ── «Продолжить просмотр» ──────────────────────────────────────────────

    @RequiresApi(Build.VERSION_CODES.O)
    private fun publishWatchNext(
        context: Context,
        items: List<ContinueItem>,
        pics: Map<Long, TvArt.Pic?>,
        touched: Map<Long, Long>,
    ) {
        val cr = context.contentResolver
        val helper = PreviewChannelHelper(context)
        val existing = ownWatchNext(cr)
        var prevEngaged = System.currentTimeMillis()
        for (it in items) {
            val key = KEY_PREFIX + it.titleId
            // Порядок /continue — по последнему касанию; без него в истории
            // (давнее) — сразу за предыдущим, чтобы порядок не поплыл.
            val engaged = touched[it.titleId]?.takeIf { t -> t > 0 } ?: (prevEngaged - 60_000)
            prevEngaged = engaged
            val program = watchNextProgram(context, it, pics[it.titleId], engaged)
            val old = existing.remove(key)
            when {
                old == null -> helper.publishWatchNextProgram(program)
                !old.hasAnyUpdatedValues(program) -> {}
                // Карточку убрали с главного экрана: не навязываем, пока не
                // сдвинется сам просмотр (другая серия или позиция).
                !old.isBrowsable && old.contentId == program.contentId &&
                    old.lastPlaybackPositionMillis == program.lastPlaybackPositionMillis -> {}
                // Убранная, но смотрят дальше — возвращается: обновление
                // скрытую строку не показало бы.
                !old.isBrowsable -> {
                    cr.delete(TvContractCompat.buildWatchNextProgramUri(old.id), null, null)
                    helper.publishWatchNextProgram(program)
                }
                else -> helper.updateWatchNextProgram(program, old.id)
            }
        }
        // Досмотренное и скрытое из «Продолжить» — долой.
        for (old in existing.values) cr.delete(TvContractCompat.buildWatchNextProgramUri(old.id), null, null)
    }

    @RequiresApi(Build.VERSION_CODES.O)
    private fun watchNextProgram(context: Context, it: ContinueItem, pic: TvArt.Pic?, engaged: Long): WatchNextProgram {
        val series = it.seasonNo > 0
        val b = WatchNextProgram.Builder()
            .setWatchNextType(
                if (it.positionMs > 0) WatchNextPrograms.WATCH_NEXT_TYPE_CONTINUE
                else WatchNextPrograms.WATCH_NEXT_TYPE_NEXT,
            )
            .setLastEngagementTimeUtcMillis(engaged)
        b.setType(if (series) PreviewPrograms.TYPE_TV_EPISODE else PreviewPrograms.TYPE_MOVIE)
            .setTitle(splitTitle(it.title).first)
            .setDescription(
                if (it.positionMs <= 0 && series) "Следующая: ${it.seasonNo} сезон, ${it.episodeNo} серия"
                else describeContinue(it),
            )
            .setIntentUri(TvLinks.play(context, it.titleId, it.episodeId))
            .setInternalProviderId(KEY_PREFIX + it.titleId)
            .setContentId("e${it.episodeId}")
        if (series) b.setSeasonNumber(it.seasonNo).setEpisodeNumber(it.episodeNo)
        if (it.durationMs > 0) b.setDurationMillis(it.durationMs.coerceAtMost(Int.MAX_VALUE.toLong()).toInt())
        if (it.positionMs > 0) b.setLastPlaybackPositionMillis(it.positionMs.coerceAtMost(Int.MAX_VALUE.toLong()).toInt())
        if (pic != null) b.setPosterArtUri(pic.uri).setPosterArtAspectRatio(pic.aspect)
        return b.build()
    }

    private fun ownWatchNext(cr: ContentResolver): HashMap<String, WatchNextProgram> {
        val out = HashMap<String, WatchNextProgram>()
        cr.query(WatchNextPrograms.CONTENT_URI, WatchNextProgram.PROJECTION, null, null, null)?.use { c ->
            while (c.moveToNext()) {
                val p = WatchNextProgram.fromCursor(c)
                val key = p.internalProviderId ?: continue
                if (!key.startsWith(KEY_PREFIX)) continue
                // Дубль (гонка двух обновлений) — лишний убираем сразу.
                if (out.containsKey(key)) cr.delete(TvContractCompat.buildWatchNextProgramUri(p.id), null, null)
                else out[key] = p
            }
        }
        return out
    }

    // ── Канал «Новые серии» ────────────────────────────────────────────────

    @RequiresApi(Build.VERSION_CODES.O)
    private fun publishChannel(context: Context, items: List<NewEpisodeItem>, pics: Map<Long, TvArt.Pic?>): Long {
        val cr = context.contentResolver
        val helper = PreviewChannelHelper(context)
        val channelId = ensureChannelLocked(context, helper)
        if (channelId <= 0) return 0
        val existing = channelPrograms(cr, channelId)
        items.forEachIndexed { i, it ->
            val key = KEY_PREFIX + it.titleId
            val program = PreviewProgram.Builder()
                .setChannelId(channelId)
                // Порядок ленты сервера: больший вес — левее.
                .setWeight(items.size - i)
                .setType(PreviewPrograms.TYPE_TV_SERIES)
                .setTitle(splitTitle(it.title).first)
                .setDescription(newEpisodesLine(it))
                .setIntentUri(TvLinks.title(context, it.titleId))
                .setInternalProviderId(key)
                .setContentId("t${it.titleId}")
                .apply {
                    pics[it.titleId]?.let { p -> setPosterArtUri(p.uri).setPosterArtAspectRatio(p.aspect) }
                }
                .build()
            val old = existing.remove(key)
            when {
                old == null -> helper.publishPreviewProgram(program)
                !old.hasAnyUpdatedValues(program) -> {}
                // Убранную карточку возвращает только новое: вышла серия, сдвинулся счёт.
                !old.isBrowsable -> {
                    helper.deletePreviewProgram(old.id)
                    helper.publishPreviewProgram(program)
                }
                else -> helper.updatePreviewProgram(old.id, program)
            }
        }
        for (old in existing.values) helper.deletePreviewProgram(old.id)
        return channelId
    }

    private fun newEpisodesLine(it: NewEpisodeItem): String {
        val n = it.unwatched
        return if (n <= 0) "" else "$n ${plural(n, "новая серия", "новые серии", "новых серий")}"
    }

    /** Канал есть — его id; нет — заводится (пока невидимым). */
    @RequiresApi(Build.VERSION_CODES.O)
    private fun ensureChannelLocked(context: Context, helper: PreviewChannelHelper): Long {
        val ours = helper.allChannels.filter { it.internalProviderId == CHANNEL_KEY }
        ours.drop(1).forEach { helper.deletePreviewChannel(it.id) }
        ours.firstOrNull()?.let { return it.id }
        return try {
            val logo = ContextCompat.getDrawable(context, R.mipmap.ic_launcher)!!.toBitmap(160, 160)
            helper.publishChannel(
                PreviewChannel.Builder()
                    .setDisplayName("Новые серии")
                    .setDescription("Непросмотренное в том, за чем вы следите")
                    .setAppLinkIntentUri(TvLinks.home(context))
                    .setInternalProviderId(CHANNEL_KEY)
                    .setLogo(logo)
                    .build(),
            )
        } catch (e: Exception) {
            Log.w(TAG, "канал не заведён", e)
            0
        }
    }

    private fun channelPrograms(cr: ContentResolver, channelId: Long): HashMap<String, PreviewProgram> {
        val out = HashMap<String, PreviewProgram>()
        cr.query(
            TvContractCompat.buildPreviewProgramsUriForChannel(channelId),
            PreviewProgram.PROJECTION, null, null, null,
        )?.use { c ->
            while (c.moveToNext()) {
                val p = PreviewProgram.fromCursor(c)
                val key = p.internalProviderId
                if (key == null || out.containsKey(key)) cr.delete(TvContractCompat.buildPreviewProgramUri(p.id), null, null)
                else out[key] = p
            }
        }
        return out
    }

    /**
     * Виден ли канал на главном экране. Свой запрос: в проекции
     * PreviewChannelHelper.getAllChannels колонки browsable нет, и
     * PreviewChannel.isBrowsable() там всегда false.
     */
    suspend fun channelState(context: Context): ChannelState {
        if (!supported(context) || Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return ChannelState.NONE
        return withContext(Dispatchers.IO) {
            var state = ChannelState.NONE
            try {
                context.contentResolver.query(
                    TvContractCompat.Channels.CONTENT_URI,
                    arrayOf(TvContractCompat.Channels.COLUMN_INTERNAL_PROVIDER_ID, TvContractCompat.Channels.COLUMN_BROWSABLE),
                    null, null, null,
                )?.use { c ->
                    while (c.moveToNext()) {
                        if (c.getString(0) != CHANNEL_KEY) continue
                        state = if (c.getInt(1) == 1) ChannelState.SHOWN else ChannelState.HIDDEN
                        break
                    }
                }
            } catch (e: Exception) {
                Log.w(TAG, "видимость канала", e)
            }
            state
        }
    }

    /**
     * Спросить один раз сам: канал заведён, в нём есть что показать, на
     * главном экране его нет, и этот вход ещё не спрашивали.
     */
    suspend fun shouldAskOnce(context: Context, synced: Synced): Boolean =
        synced.channelId > 0 && synced.fresh > 0 &&
            !prefs(context).getBoolean(K_ASKED, false) &&
            channelState(context) == ChannelState.HIDDEN

    /**
     * Системный диалог «Добавить канал на главный экран?». Нет диалога в
     * прошивке — тихая просьба системе (для первого канала приложения
     * Android TV соглашается без вопросов).
     */
    fun askToShow(context: Context, launcher: ActivityResultLauncher<Intent>, channelId: Long) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        prefs(context).edit().putBoolean(K_ASKED, true).apply()
        try {
            launcher.launch(
                Intent(TvContractCompat.ACTION_REQUEST_CHANNEL_BROWSABLE)
                    .putExtra(TvContractCompat.EXTRA_CHANNEL_ID, channelId),
            )
        } catch (_: ActivityNotFoundException) {
            try { TvContractCompat.requestChannelBrowsable(context, channelId) } catch (_: Exception) { }
        }
    }

    private fun prefs(context: Context) = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
}
