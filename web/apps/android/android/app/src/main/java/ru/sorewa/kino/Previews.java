package ru.sorewa.kino;

import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.media.MediaMetadataRetriever;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;
import android.webkit.CookieManager;
import android.webkit.WebResourceResponse;
import org.json.JSONArray;
import org.json.JSONObject;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.regex.Pattern;

/**
 * Превью скачанного, которые видны без сети.
 *
 * Картинки витрины приезжают с сервера, и без сети на месте постеров были
 * пустые рамки. Поэтому у каждой скачанной серии есть свой кадр — его
 * вынимаем из самого файла, сеть для этого не нужна, — а у тайтла постер,
 * который скачивается, пока сеть есть. Лежат в личной папке приложения
 * (files/art) и отдаются страницам по адресу /_kino/art/<имя>: этот путь
 * перехватывает KinoWebViewClient, так что он работает и у витрины, и у
 * страницы без сети.
 *
 * Здесь же фоновый проход по описи: подхват найденных файлов (Library.adopt),
 * нарезка кадров, докачка постеров. Он идёт при запуске и после каждой
 * загрузки; страница узнаёт о новом через kinoLibraryChanged.
 */
final class Previews {
    private static final String TAG = "kino";
    static final String PATH = "/_kino/art/";
    private static final Pattern NAME = Pattern.compile("[a-z0-9]+\\.jpg");
    private static final ExecutorService ONE = Executors.newSingleThreadExecutor();

    /** Кому сказать, что опись или превью поменялись (страница в MainActivity). */
    static volatile Runnable listener;

    private Previews() {}

    static String frameName(String uri) { return "e" + Integer.toHexString(uri.hashCode()) + ".jpg"; }

    static String posterName(long titleId) { return "t" + titleId + ".jpg"; }

    private static File dir(Context ctx) {
        File d = new File(ctx.getFilesDir(), "art");
        //noinspection ResultOfMethodCallIgnored
        d.mkdirs();
        return d;
    }

    /** Адрес для страницы или null, если картинки ещё нет. */
    static String url(Context ctx, String name) {
        return new File(dir(ctx), name).isFile() ? PATH + name : null;
    }

    /** Ответ на /_kino/art/<имя>; null — такой картинки нет. */
    static WebResourceResponse serve(Context ctx, String name) {
        if (name == null || !NAME.matcher(name).matches()) return null;
        File f = new File(dir(ctx), name);
        if (!f.isFile()) return null;
        try {
            Map<String, String> h = new HashMap<>();
            // Постер может смениться на сервере, а кадр — появиться позже:
            // кешировать нечего, файл и так рядом.
            h.put("Cache-Control", "no-store");
            return new WebResourceResponse("image/jpeg", null, 200, "OK", h, new FileInputStream(f));
        } catch (Exception e) {
            return null;
        }
    }

    /**
     * Фоном: подхватить найденные файлы, нарезать недостающие кадры, докачать
     * постеры. Без сети постеры просто не докачаются — до следующего раза.
     *
     * @param server адрес витрины (для постеров); null — только кадры
     */
    static void refresh(Context context, String server) {
        Context ctx = context.getApplicationContext();
        ONE.execute(() -> {
            boolean changed = false;
            Library lib = Library.of(ctx);
            try {
                changed = lib.adopt() > 0;
            } catch (Exception e) {
                Log.w(TAG, "превью: подхват: " + e);
            }
            String cookie = null;
            try {
                if (server != null) cookie = CookieManager.getInstance().getCookie(server);
            } catch (Exception ignored) { /* без cookie сервер постер не отдаст — не беда */ }
            JSONArray a = lib.list();
            for (int i = 0; i < a.length(); i++) {
                JSONObject o = a.optJSONObject(i);
                if (o == null) continue;
                String uri = o.optString("uri");
                File frame = new File(dir(ctx), frameName(uri));
                File none = new File(dir(ctx), frameName(uri) + ".none");
                if (!frame.exists() && !none.exists()) {
                    long dur = frame(ctx, Uri.parse(uri), frame);
                    if (frame.exists()) changed = true;
                    else touch(none); // не вынулся — не мучить файл при каждом запуске
                    if (dur > 0 && o.optLong("durationMs") <= 0) {
                        lib.setDuration(o.optString("id"), dur);
                        changed = true;
                    }
                }
                long tid = o.optLong("titleId");
                if (tid > 0 && server != null) {
                    File poster = new File(dir(ctx), posterName(tid));
                    if (!poster.exists() && poster(server, cookie, tid, poster)) changed = true;
                }
            }
            Runnable l = listener;
            if (changed && l != null) new Handler(Looper.getMainLooper()).post(l);
        });
    }

    /**
     * Кадр из файла — на 15% длины, но не дальше двух минут: в начале
     * заставка, в середине — спойлер. Возвращает длительность в мс (0, если
     * файл не открылся).
     */
    private static long frame(Context ctx, Uri uri, File out) {
        MediaMetadataRetriever r = new MediaMetadataRetriever();
        try {
            r.setDataSource(ctx, uri);
            long dur = 0;
            try { dur = Long.parseLong(r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)); }
            catch (Exception ignored) { /* длительность неизвестна — кадр всё равно попробуем */ }
            long at = dur > 0 ? Math.min(dur * 15 / 100, 120_000) : 30_000;
            Bitmap b = r.getFrameAtTime(at * 1000, MediaMetadataRetriever.OPTION_CLOSEST_SYNC);
            if (b != null) save(scale(b, 480), out);
            return dur;
        } catch (Exception e) {
            Log.w(TAG, "превью: кадр " + uri + ": " + e);
            return 0;
        } finally {
            try { r.release(); } catch (Exception ignored) { /* уже закрыт */ }
        }
    }

    /** Постер тайтла с сервера, той же сессией, что у витрины. */
    private static boolean poster(String server, String cookie, long titleId, File out) {
        HttpURLConnection c = null;
        try {
            String base = server.endsWith("/") ? server.substring(0, server.length() - 1) : server;
            c = (HttpURLConnection) new URL(base + "/api/v1/titles/" + titleId + "/art").openConnection();
            c.setConnectTimeout(8000);
            c.setReadTimeout(15000);
            if (cookie != null) c.setRequestProperty("Cookie", cookie);
            if (c.getResponseCode() != 200) return false;
            Bitmap b;
            try (InputStream in = c.getInputStream()) { b = BitmapFactory.decodeStream(in); }
            if (b == null) return false;
            save(scale(b, 360), out);
            return out.exists();
        } catch (Exception e) {
            return false; // нет сети или сервер молчит — докачаем в следующий раз
        } finally {
            if (c != null) c.disconnect();
        }
    }

    private static Bitmap scale(Bitmap b, int width) {
        if (b.getWidth() <= width) return b;
        int h = Math.max(1, Math.round(b.getHeight() * (width / (float) b.getWidth())));
        return Bitmap.createScaledBitmap(b, width, h, true);
    }

    /** Через временный файл: недописанная картинка не должна выглядеть готовой. */
    private static void save(Bitmap b, File out) throws Exception {
        File tmp = new File(out.getPath() + ".tmp");
        try (FileOutputStream o = new FileOutputStream(tmp)) {
            if (!b.compress(Bitmap.CompressFormat.JPEG, 82, o)) throw new Exception("не сжать");
        }
        if (!tmp.renameTo(out)) {
            //noinspection ResultOfMethodCallIgnored
            tmp.delete();
        }
    }

    private static void touch(File f) {
        try { new FileOutputStream(f).close(); } catch (Exception ignored) { /* не страшно — попробуем ещё раз */ }
    }
}
