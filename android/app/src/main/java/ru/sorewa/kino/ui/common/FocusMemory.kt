package ru.sorewa.kino.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.Saver
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.focus.onFocusChanged

/**
 * На ТВ сетка или лента помнит карточку, с которой пульт из неё ушёл, и
 * возвращает фокус на неё же. Без этого «влево в меню → вправо обратно»
 * искало ближайшую карточку напротив пункта меню — обычно на ряд выше
 * той, где стояли, и всегда в первой колонке. Ставится на саму ленивую
 * сетку/ленту: память у неё на один уровень, поэтому вложенным лентам
 * (главная) нужен свой модификатор на каждом уровне.
 *
 * fallback — куда встать, если своей памяти нет: карточка из [TvFocusKey]
 * (вернулись с тайтла) или серия, с которой продолжать.
 */
@Composable
fun Modifier.tvFocusMemory(fallback: FocusRequester? = null): Modifier =
    if (LocalTv.current) this.focusRestorer(fallback ?: FocusRequester.Default) else this

/**
 * Карточка, на которой стоял пульт, — через уход с экрана. Память
 * [tvFocusMemory] живёт, только пока сетка на экране, и заполняется, лишь
 * когда фокус уходит стрелкой; по OK на карточке (открыли тайтл) она пуста.
 * Здесь ключ карточки сохраняется вместе с состоянием экрана, и «Назад»
 * с тайтла ставит пульт туда же, где он был, а не на первую таблетку.
 */
class TvFocusKey(saved: String?) {
    var key by mutableStateOf(saved)
    val requester = FocusRequester()

    /** Пульт ушёл в шапку, на кнопки — возвращаться к карточке не нужно. */
    fun forget() { key = null }

    fun tryRestore(): Boolean = key != null && try { requester.requestFocus() } catch (_: Exception) { false }
}

@Composable
fun rememberTvFocusKey(): TvFocusKey =
    rememberSaveable(saver = Saver<TvFocusKey, String>(save = { it.key ?: "" }, restore = { TvFocusKey(it.ifEmpty { null }) })) {
        TvFocusKey(null)
    }

/**
 * Вернулись на экран (с тайтла, из меню) — пульт встаёт на запомненную
 * карточку, как только список на месте. Один раз за появление экрана:
 * смена жанра или вкладки потом не утягивает фокус из таблеток в сетку.
 * Срабатывает на первых кадрах нового экрана, пока прежний ещё гаснет, —
 * раньше, чем общий возврат фокуса в TvShell поставил бы его на первую
 * таблетку.
 */
@Composable
fun TvFocusRestore(mem: TvFocusKey, ready: Boolean) {
    if (!LocalTv.current) return
    var done by androidx.compose.runtime.remember { mutableStateOf(false) }
    androidx.compose.runtime.LaunchedEffect(ready) {
        if (!ready || done) return@LaunchedEffect
        done = true
        if (mem.key == null) return@LaunchedEffect
        repeat(3) {
            androidx.compose.runtime.withFrameNanos { }
            if (mem.tryRestore()) return@LaunchedEffect
        }
    }
}

/** Отметка карточки для [TvFocusKey]: запоминает её, когда на ней фокус. */
@Composable
fun Modifier.tvFocusKey(mem: TvFocusKey?, key: Any): Modifier {
    if (mem == null || !LocalTv.current) return this
    val k = key.toString()
    return this
        .onFocusChanged { if (it.hasFocus) mem.key = k }
        .then(if (mem.key == k) Modifier.focusRequester(mem.requester) else Modifier)
}
