package ru.sorewa.kino;

import android.content.ActivityNotFoundException;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;

/**
 * Запуск во внешнем плеере.
 *
 * Списка приложений здесь нет намеренно: система сама покажет всё, что умеет
 * открывать видео. Вшитый перечень устарел бы в тот же день, когда человек
 * поставит плеер, о котором мы не подумали.
 *
 * Дополнительные поля — общепринятые у видеоплееров: подпись и стартовая
 * позиция. Кто их не понимает, просто откроет с начала; ошибкой это не
 * считается.
 */
final class ExternalPlayer {

    private ExternalPlayer() { }

    /**
     * @param uri   адрес: content:// для скачанного, https:// для потока
     * @param mime  тип; для HLS важен, иначе часть плееров не берётся за ссылку
     */
    static Intent intent(Uri uri, String title, long positionMs, String mime) {
        Intent i = new Intent(Intent.ACTION_VIEW);
        i.setDataAndType(uri, mime == null || mime.isEmpty() ? "video/*" : mime);
        i.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK);
        if (title != null && !title.isEmpty()) {
            i.putExtra("title", title);
            // MX Player и VLC читают разные ключи для одного и того же.
            i.putExtra("secure_uri", true);
        }
        if (positionMs > 0) {
            i.putExtra("position", (int) positionMs);
            i.putExtra("from_start", false);
        }
        return i;
    }

    /**
     * Показывает системный выбор. Возвращает false, если открыть видео нечем —
     * тогда зовущий откатывается на встроенный плеер, а не оставляет человека
     * с пустым экраном.
     */
    static boolean open(Context ctx, Uri uri, String title, long positionMs, String mime) {
        Intent view = intent(uri, title, positionMs, mime);
        // Спрашиваем заранее: createChooser показывает окно даже тогда, когда
        // открывать нечем, и человек упирается в «нет приложений» вместо кино.
        // Ответ виден только благодаря <queries> в манифесте — с Android 11
        // чужие приложения иначе не перечислить.
        if (ctx.getPackageManager().queryIntentActivities(view, 0).isEmpty()) return false;
        Intent chooser = Intent.createChooser(view, "Открыть в приложении");
        chooser.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        // Разрешение на чтение выдаётся всем, кого показал выбор: иначе
        // выбранный плеер получит content:// и не сможет его открыть.
        chooser.putExtra(Intent.EXTRA_INITIAL_INTENTS, new Intent[]{view});
        try {
            ctx.startActivity(chooser);
            return true;
        } catch (ActivityNotFoundException e) {
            return false;
        }
    }
}
