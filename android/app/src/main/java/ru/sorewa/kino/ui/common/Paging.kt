package ru.sorewa.kino.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import ru.sorewa.kino.data.Page
import ru.sorewa.kino.data.describeApiError

/**
 * Список с догрузкой по offset: один на все экраны вместо пяти копий
 * одного и того же. fetch получает offset (или номер страницы — как
 * удобно источнику) и отдаёт страницу.
 */
class Paged<T>(
    private val scope: CoroutineScope,
    private val pageBased: Boolean,
    private val fetch: suspend (Int) -> Page<T>,
) {
    var items by mutableStateOf<List<T>?>(null)
        private set
    var total by mutableStateOf(0)
        private set
    var hasMore by mutableStateOf(false)
        private set
    var loading by mutableStateOf(false)
        private set
    var error by mutableStateOf("")
        private set

    private var cursor = 0
    private var job: Job? = null
    private var refreshing = false

    fun reload() {
        job?.cancel()
        loading = false
        refreshing = false
        cursor = if (pageBased) 1 else 0
        load(reset = true)
    }

    fun more() {
        if (!hasMore || loading || refreshing) return
        load(reset = false)
    }

    /**
     * Перечитать уже показанное, не трогая экран: список остаётся на месте,
     * пока не придёт новый, — прокрутка и фокус пульта не сбрасываются.
     * Перечитывается столько, сколько было догружено (не больше 10 страниц).
     */
    fun refresh() {
        val shown = items ?: return
        if (loading || refreshing) return
        refreshing = true
        job = scope.launch {
            try {
                var cur = if (pageBased) 1 else 0
                val acc = ArrayList<T>()
                var tot: Int
                var more: Boolean
                var pages = 0
                do {
                    val p = fetch(cur)
                    acc += p.items
                    tot = p.total
                    pages++
                    if (pageBased) {
                        cur += 1
                        more = p.items.isNotEmpty() && (p.hasMore || acc.size < p.total)
                    } else {
                        cur = p.nextOffset
                        more = p.hasMore
                    }
                } while (more && acc.size < shown.size && pages < 10)
                items = acc
                total = tot
                hasMore = more
                cursor = cur
                error = ""
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (_: Exception) {
                // Сеть моргнула — остаётся прежний список.
            } finally {
                refreshing = false
            }
        }
    }

    /** Убрать элемент локально (скрыли карточку) — без перечитывания. */
    fun drop(pred: (T) -> Boolean) {
        items = items?.filterNot(pred)
    }

    private fun load(reset: Boolean) {
        loading = true
        job = scope.launch {
            try {
                val p = fetch(cursor)
                items = if (reset) p.items else (items ?: emptyList()) + p.items
                total = p.total
                if (pageBased) {
                    cursor += 1
                    val shown = items?.size ?: 0
                    hasMore = p.items.isNotEmpty() && (p.hasMore || shown < p.total)
                } else {
                    cursor = p.nextOffset
                    hasMore = p.hasMore
                }
                error = ""
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                error = describeApiError(e)
                if (reset) items = items
            } finally {
                loading = false
            }
        }
    }
}

/**
 * Списки экрана живут, пока экран в стеке навигации (ViewModel записи
 * стека): «Назад» с тайтла возвращает каталог сразу — с той же прокруткой,
 * и пульт встаёт на ту же карточку, а не грузит список заново с начала
 * сетки при каждом уходе с экрана.
 * Держим последние несколько наборов (смена жанра, вкладки).
 */
class PagedLists : ViewModel() {
    val lists = object : LinkedHashMap<List<Any?>, Paged<*>>(16, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<List<Any?>, Paged<*>>?) = size > 12
    }
}

/**
 * refresh = true — список перечитывается тихо при каждом возвращении на
 * экран (с тайтла, из плеера): «Продолжить», «Слежу», история.
 */
@Composable
fun <T> rememberPaged(
    vararg keys: Any?,
    pageBased: Boolean = false,
    refresh: Boolean = false,
    fetch: suspend (Int) -> Page<T>,
): Paged<T> {
    val holder: PagedLists = viewModel()
    val key = keys.toList()
    @Suppress("UNCHECKED_CAST")
    val paged = remember(key) {
        holder.lists.getOrPut(key) { Paged(holder.viewModelScope, pageBased, fetch) } as Paged<T>
    }
    LaunchedEffect(paged) { if (paged.items == null && !paged.loading) paged.reload() }
    if (refresh) {
        // Первое появление — обычная загрузка выше; дальше каждое возвращение.
        LifecycleResumeEffect(paged) {
            if (paged.items != null) paged.refresh()
            onPauseOrDispose { }
        }
    }
    return paged
}
