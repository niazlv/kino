package ru.sorewa.kino

import android.app.Application
import kotlinx.coroutines.launch
import ru.sorewa.kino.data.Api
import ru.sorewa.kino.data.AuthRepository
import ru.sorewa.kino.data.TokenStore

/**
 * Приложение. Единый контейнер без DI-фреймворка: API, сессия, хранилище.
 */
class KinoApp : Application(), coil.ImageLoaderFactory {

    lateinit var container: AppContainer
        private set

    /** Телефон или ТВ: ставит та активность, с которой стартовало приложение. */
    companion object {
        lateinit var instance: KinoApp
            private set
        @Volatile var deviceKind: String = "android"
    }

    override fun onCreate() {
        super.onCreate()
        instance = this
        // Процесс-перезапускатель живёт доли секунды и только запускает приложение
        // заново: ни контейнер, ни движок ему не нужны (см. AppRestart).
        if (ru.sorewa.kino.engine.AppRestart.isRestartProcess()) return
        container = AppContainer(this)
    }

    /**
     * Постеры и кадры сервер отдаёт только с сессией: Coil ходит тем же
     * OkHttp-клиентом, что и API (cookie + обновление по 401). Без этого все
     * карточки были пустыми.
     */
    override fun newImageLoader(): coil.ImageLoader = coil.ImageLoader.Builder(this)
        .okHttpClient { container.api.http }
        .crossfade(220)
        .respectCacheHeaders(false)
        .memoryCache { coil.memory.MemoryCache.Builder(this).maxSizePercent(0.25).build() }
        .diskCache {
            coil.disk.DiskCache.Builder()
                .directory(cacheDir.resolve("img"))
                .maxSizeBytes(256L * 1024 * 1024)
                .build()
        }
        .build()
}

class AppContainer(context: android.content.Context) {

    companion object {
        /** Адрес-заглушка, пока сервер не выбран: .invalid не резолвится никогда (RFC 2606). */
        const val NO_SERVER = "https://server.invalid"
    }

    /**
     * Режим подключения читается один раз, на старте процесса, и дальше не
     * меняется: переключение перезапускает приложение (см. AppRestart) —
     * от режима зависит, какие хранилища открывать и куда ходит Api.
     */
    val connection: ru.sorewa.kino.engine.ConnectionConfig =
        ru.sorewa.kino.engine.ConnectionConfig.of(context)

    /** Работаем на движке этого устройства, а не на сервере (ADR-0015). */
    val local: Boolean = connection.isLocal

    /** Фоновая работа, переживающая экраны: подготовка скачанного к офлайну. */
    val scope = kotlinx.coroutines.CoroutineScope(
        kotlinx.coroutines.SupervisorJob() + kotlinx.coroutines.Dispatchers.IO,
    )

    /** Локальный движок; null в режиме сервера. */
    val engine: ru.sorewa.kino.engine.LocalEngine? =
        if (local) ru.sorewa.kino.engine.LocalEngineFactory.create(context, connection.engineToken(), scope) else null

    /**
     * Чьи данные открыты: вшитого сервера (null), движка или выбранного
     * сервера. Как и режим, на всю жизнь процесса (см. ConnectionConfig.profile).
     */
    val profile: String? = connection.profile

    /** Адрес сервера; пусто в локальном режиме и пока сервер не выбран. */
    val serverUrl: String = if (local) "" else connection.serverUrl

    /**
     * Работаем с сервером, вшитым в сборку, и с его прежним хранилищем. Только
     * здесь есть что переносить из старой оболочки (она жила на этом сервере).
     */
    val onBuiltInServer: Boolean = !local && profile == null && serverUrl.isNotEmpty()

    val store: TokenStore = TokenStore(context, profile)
    val api: Api = Api(
        store,
        isTv = false,
        // Сервер ещё не выбран — заглушка: экраны в это время показывают выбор,
        // а случайный фоновый запрос получит «нет связи», а не падение.
        baseUrl = engine?.baseUrl ?: serverUrl.ifEmpty { NO_SERVER },
        gate = engine?.let { ru.sorewa.kino.engine.EngineGate(it) },
    )
    val auth: AuthRepository = AuthRepository(api, store)

    /**
     * Данные, привязанные к идентификаторам серий, у локального режима свои:
     * id движка и сервера не совпадают, и очередь отметок одного не должна
     * уйти в другой (см. ModeContext).
     */
    private val data: android.content.Context = ru.sorewa.kino.engine.ModeContext(context, profile)
    val library: ru.sorewa.kino.data.LocalLibrary = ru.sorewa.kino.data.LocalLibrary(data)
    val state: ru.sorewa.kino.data.StateStore = ru.sorewa.kino.data.StateStore(data)
    val art: ru.sorewa.kino.data.Art = ru.sorewa.kino.data.Art(data, api)
    val sync: ru.sorewa.kino.data.Sync = ru.sorewa.kino.data.Sync(api, library, state)
    val offline: ru.sorewa.kino.data.OfflineCatalog =
        ru.sorewa.kino.data.OfflineCatalog(data, api, library, art, sync)

    /**
     * Локальная сессия — токен движка, и она не бывает «мёртвой»: если сервер
     * (движок) вдруг её не признал, ставим её заново, а не уводим на экран
     * входа, которого в этом режиме нет.
     */
    fun reassertLocalSession() {
        if (!local) return
        val token = connection.engineToken()
        api.attachToken(token)
        scope.launch { store.saveSession(token = token) }
    }

    /** Подготовить скачанное к офлайну в фоне (с сетью; без неё — тихо ничего). */
    fun warmUpOffline(force: Boolean = false) {
        scope.launch { try { offline.warmUp(force) } catch (_: Exception) { } }
    }

    /** Сеть вернулась — ждущий стейт (офлайн-прогресс, метки) уезжает на сервер. */
    fun kickSync() {
        scope.launch { try { sync.syncPendingState() } catch (_: Exception) { } }
    }

    init {
        // Сохранённый токен поднимается немедленно: первый же запрос должен
        // нести сессию (потом SessionRenewal продлит её сам, если протухла).
        if (local) {
            // Локальный аккаунт: сессия — токен движка, входа нет. Пишем её
            // сразу и до первого кадра: иначе интерфейс успел бы показать вход.
            val token = connection.engineToken()
            kotlinx.coroutines.runBlocking { store.saveSession(token = token) }
            api.attachToken(token)
            engine?.start()
        } else {
            val s = kotlinx.coroutines.runBlocking { store.session() }
            api.attachToken(s.token)
        }
        // Сеть пропала совсем (режим полёта) — висящие запросы обрываем: уже
        // открытое HTTP/2-соединение иначе ждёт таймаута до 40 с, и экраны
        // крутят спиннер вместо скачанного. Вернулась — сливаем очередь.
        try {
            val cm = context.getSystemService(android.net.ConnectivityManager::class.java)
            cm?.registerDefaultNetworkCallback(object : android.net.ConnectivityManager.NetworkCallback() {
                override fun onLost(network: android.net.Network) {
                    // Движок на этом устройстве от сети не зависит: обрыв интернета
                    // не повод рвать запросы к нему (источники сами ответят ошибкой).
                    if (!local && cm.activeNetwork == null) {
                        api.http.dispatcher.cancelAll()
                        api.http.connectionPool.evictAll()
                    }
                }

                override fun onAvailable(network: android.net.Network) {
                    kickSync()
                }
            })
        } catch (_: Exception) { }
        // Продлить сессию нечем (refresh отозван или истёк) — стираем её,
        // интерфейс сам уйдёт на вход: без этого человек видел ошибки на
        // каждом экране до перезапуска.
        api.onSessionDead = {
            if (local) reassertLocalSession() else scope.launch { auth.dropSession() }
        }
        // Выход стирает карточки с главного экрана ТВ (на телефоне — пустой вызов).
        auth.onSignedOut = {
            ru.sorewa.kino.tvhome.TvLauncherWork.cancel(context)
            ru.sorewa.kino.tvhome.TvLauncher.clear(context)
        }
    }
}
