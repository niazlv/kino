package ru.sorewa.kino;

import android.content.Context;
import android.net.Uri;
import android.util.Log;

import androidx.documentfile.provider.DocumentFile;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Опись скачанного.
 *
 * Сами файлы остаются там, куда их положили, — в «Фильмы/kino» или в выбранной
 * человеком папке. Сюда пишутся только адреса и состояние просмотра, потому что
 * файл принадлежит человеку: он вправе унести его на компьютер или стереть
 * файловым менеджером, и приложение обязано это пережить.
 *
 * Состояние просмотра держится здесь же, а не только на сервере: скачанное
 * смотрят как раз тогда, когда сети нет. Отданный серверу прогресс помечается
 * как переданный (см. {@code dirty}), чтобы не слать одно и то же дважды.
 */
final class Library {

    private static final String TAG = "kino";
    private static final String FILE = "library.json";

    /** Ключ записи: тайтл, сезон и номер серии — то, чем её называет витрина. */
    static String keyOf(long titleId, int season, int episode) {
        return titleId + ":" + season + ":" + episode;
    }

    /** «S01E02» → номер сезона и серии; null, если имя не в этом виде. */
    private static final Pattern SE = Pattern.compile("(?i)S(\\d{1,3})E(\\d{1,4})");

    static int[] seasonEpisode(String name) {
        Matcher m = SE.matcher(name);
        if (!m.find()) return null;
        try {
            return new int[]{Integer.parseInt(m.group(1)), Integer.parseInt(m.group(2))};
        } catch (NumberFormatException e) {
            return null;
        }
    }

    /** «…/#/title/53014» → 53014; 0, если в ссылке нет тайтла. */
    static long titleIdOf(String pageUrl) {
        if (pageUrl == null) return 0;
        Matcher m = Pattern.compile("/title/(\\d+)").matcher(pageUrl);
        if (!m.find()) return 0;
        try { return Long.parseLong(m.group(1)); } catch (NumberFormatException e) { return 0; }
    }

    private final Context ctx;
    private static Library shared;

    private Library(Context ctx) { this.ctx = ctx.getApplicationContext(); }

    /**
     * Один экземпляр на процесс. Опись пишут плеер, сервис загрузки, мост и
     * фоновый подхват найденных файлов; synchronized на разных экземплярах
     * их бы не разводил, и одна запись затирала бы другую.
     */
    static synchronized Library of(Context ctx) {
        if (shared == null) shared = new Library(ctx);
        return shared;
    }

    private File file() { return new File(ctx.getFilesDir(), FILE); }

    private synchronized JSONArray read() {
        File f = file();
        if (!f.exists()) return new JSONArray();
        try {
            byte[] buf = new byte[(int) f.length()];
            try (java.io.FileInputStream in = new java.io.FileInputStream(f)) {
                int off = 0, n;
                while (off < buf.length && (n = in.read(buf, off, buf.length - off)) > 0) off += n;
            }
            return new JSONArray(new String(buf, StandardCharsets.UTF_8));
        } catch (Exception e) {
            Log.w(TAG, "опись: чтение: " + e);
            return new JSONArray();
        }
    }

    private synchronized void write(JSONArray a) {
        // Пишем через временный файл: обрыв на середине не должен оставить
        // огрызок вместо описи — тогда потерялось бы всё скачанное разом.
        File tmp = new File(ctx.getFilesDir(), FILE + ".tmp");
        try {
            try (java.io.FileOutputStream out = new java.io.FileOutputStream(tmp)) {
                out.write(a.toString().getBytes(StandardCharsets.UTF_8));
                out.getFD().sync();
            }
            if (!tmp.renameTo(file())) throw new Exception("не переименовать");
        } catch (Exception e) {
            Log.w(TAG, "опись: запись: " + e);
            //noinspection ResultOfMethodCallIgnored
            tmp.delete();
        }
    }

    private static int indexOf(JSONArray a, String id) {
        for (int i = 0; i < a.length(); i++) {
            JSONObject o = a.optJSONObject(i);
            if (o != null && id.equals(o.optString("id"))) return i;
        }
        return -1;
    }

    /**
     * Добавляет (или заменяет) запись о скачанной серии. Повторная загрузка той
     * же серии заменяет запись, но сохраняет уже накопленный прогресс: человек
     * перекачал файл в другом качестве, а не начал смотреть заново.
     */
    synchronized void put(long titleId, String title, int season, int episode,
                          Uri video, long size) {
        JSONArray a = read();
        String id = keyOf(titleId, season, episode);
        int at = indexOf(a, id);
        JSONObject old = at >= 0 ? a.optJSONObject(at) : null;
        try {
            JSONObject o = new JSONObject();
            o.put("id", id);
            o.put("titleId", titleId);
            o.put("title", title == null ? "" : title);
            o.put("season", season);
            o.put("episode", episode);
            o.put("uri", video.toString());
            o.put("size", size);
            o.put("addedAt", System.currentTimeMillis());
            o.put("positionMs", old != null ? old.optLong("positionMs") : 0);
            o.put("durationMs", old != null ? old.optLong("durationMs") : 0);
            o.put("watched", old != null && old.optBoolean("watched"));
            o.put("dirty", old != null && old.optBoolean("dirty"));
            if (at >= 0) a.put(at, o); else a.put(o);
            write(a);
        } catch (Exception e) {
            Log.w(TAG, "опись: добавление: " + e);
        }
    }

    /** Все записи, у которых файл ещё на месте; пропавшие вычищаются. */
    synchronized JSONArray list() {
        JSONArray a = read();
        JSONArray alive = new JSONArray();
        boolean lost = false;
        for (int i = 0; i < a.length(); i++) {
            JSONObject o = a.optJSONObject(i);
            if (o == null) continue;
            if (exists(Uri.parse(o.optString("uri")))) alive.put(o); else lost = true;
        }
        // Файл могли унести или стереть мимо приложения — тогда запись только
        // обманывает: показывает «скачано» там, где смотреть нечего.
        if (lost) write(alive);
        return alive;
    }

    /**
     * То же, что list(), но с адресами превью — для страниц. В файл описи
     * адреса не пишутся: превью появляются фоном и позже самих записей.
     */
    synchronized JSONArray listWithArt() {
        JSONArray a = list();
        for (int i = 0; i < a.length(); i++) {
            JSONObject o = a.optJSONObject(i);
            if (o == null) continue;
            try {
                String frame = Previews.url(ctx, Previews.frameName(o.optString("uri")));
                if (frame != null) o.put("frame", frame);
                long tid = o.optLong("titleId");
                String poster = tid > 0 ? Previews.url(ctx, Previews.posterName(tid)) : null;
                if (poster != null) o.put("poster", poster);
            } catch (Exception ignored) { /* без превью страница покажет плашку */ }
        }
        return a;
    }

    /**
     * Подхватывает видео, которые лежат в папке загрузок, но в опись не
     * попали: скачанные до того, как опись появилась (6 сентября), и всё,
     * что по какой-то причине не записалось при переносе. Без этого такие
     * серии нельзя было посмотреть из приложения вовсе — ни с сетью, ни без.
     *
     * Номер тайтла у найденного неизвестен (0): по названию папки его потом
     * находит витрина, когда есть сеть (setTitleId). Смотреть и стирать
     * можно и без него.
     *
     * @return сколько записей добавлено
     */
    int adopt() {
        List<JSONObject> found = new ArrayList<>();
        findInMediaStore(found);
        findInTree(found);
        if (found.isEmpty()) return 0;
        synchronized (this) {
            JSONArray a = read();
            // Уже известное узнаём и по адресу, и по приметам: у файла в
            // выбранной папке адрес в описи может быть записан в другом виде,
            // и без второй проверки серия задвоилась бы.
            java.util.Set<String> known = new java.util.HashSet<>();
            for (int i = 0; i < a.length(); i++) {
                JSONObject o = a.optJSONObject(i);
                if (o == null) continue;
                known.add(o.optString("uri"));
                known.add(sign(o));
            }
            int added = 0;
            for (JSONObject o : found) {
                if (known.contains(o.optString("uri")) || known.contains(sign(o))) continue;
                known.add(o.optString("uri"));
                known.add(sign(o));
                a.put(o);
                added++;
            }
            if (added > 0) write(a);
            return added;
        }
    }

    /** Приметы серии: название без знаков, сезон, серия, размер. */
    private static String sign(JSONObject o) {
        String t = o.optString("title").toLowerCase(Locale.ROOT).replaceAll("[^\\p{L}\\p{N}]+", " ").trim();
        return t + "|" + o.optInt("season") + "|" + o.optInt("episode") + "|" + o.optLong("size");
    }

    /** Видео в «Фильмы/kino/<название>/»: свои файлы MediaStore показывает без разрешений. */
    private void findInMediaStore(List<JSONObject> out) {
        if (android.os.Build.VERSION.SDK_INT < android.os.Build.VERSION_CODES.Q) return;
        Uri table = android.provider.MediaStore.Video.Media.EXTERNAL_CONTENT_URI;
        String[] cols = {
            android.provider.MediaStore.MediaColumns._ID,
            android.provider.MediaStore.MediaColumns.DISPLAY_NAME,
            android.provider.MediaStore.MediaColumns.RELATIVE_PATH,
            android.provider.MediaStore.MediaColumns.SIZE,
            android.provider.MediaStore.MediaColumns.DURATION,
        };
        String where = android.provider.MediaStore.MediaColumns.RELATIVE_PATH + " LIKE ?";
        try (android.database.Cursor c = ctx.getContentResolver().query(
                table, cols, where, new String[]{"Movies/kino/%"}, null)) {
            if (c == null) return;
            while (c.moveToNext()) {
                String path = c.getString(2);
                // «Movies/kino/Масяня/» → «Масяня»; файл прямо в «kino» — без названия.
                String rest = path == null ? "" : path.replaceFirst("(?i)^Movies/kino/?", "");
                String folder = rest.contains("/") ? rest.substring(0, rest.indexOf('/')) : rest;
                JSONObject o = foundEntry(android.content.ContentUris.withAppendedId(table, c.getLong(0)),
                        c.getString(1), folder, c.getLong(3), c.getLong(4));
                if (o != null) out.add(o);
            }
        } catch (Exception e) {
            Log.w(TAG, "опись: поиск в MediaStore: " + e);
        }
    }

    /** Папка, выбранная в «Выбрать папку…»: <выбранная>/<название>/S01E02.mkv. */
    private void findInTree(List<JSONObject> out) {
        Uri tree = Downloads.tree(ctx);
        if (tree == null) return;
        try {
            DocumentFile root = DocumentFile.fromTreeUri(ctx, tree);
            if (root == null || !root.canRead()) return;
            for (DocumentFile dir : root.listFiles()) {
                if (!dir.isDirectory()) continue;
                for (DocumentFile f : dir.listFiles()) {
                    if (!f.isFile()) continue;
                    JSONObject o = foundEntry(f.getUri(), f.getName(), dir.getName(), f.length(), 0);
                    if (o != null) out.add(o);
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "опись: поиск в выбранной папке: " + e);
        }
    }

    /** Запись о найденном файле; null — не серия (субтитры, чужое имя). */
    private static JSONObject foundEntry(Uri uri, String name, String folder, long size, long durationMs) {
        if (name == null) return null;
        String low = name.toLowerCase(Locale.ROOT);
        if (!low.endsWith(".mkv") && !low.endsWith(".mp4")) return null;
        int[] se = seasonEpisode(name);
        if (se == null) return null;
        // Папку называет перенос: «/» в названии он заменил пробелом, и вокруг
        // него остались ещё два — «Табакошка / Yani Neko» → «Табакошка   Yani Neko».
        String title = folder == null || folder.trim().isEmpty() ? "kino" : folder.replace("   ", " / ").trim();
        try {
            JSONObject o = new JSONObject();
            o.put("id", "f" + Integer.toHexString(uri.toString().hashCode()) + ":" + se[0] + ":" + se[1]);
            o.put("titleId", 0);
            o.put("title", title);
            o.put("season", se[0]);
            o.put("episode", se[1]);
            o.put("uri", uri.toString());
            o.put("size", size);
            o.put("addedAt", System.currentTimeMillis());
            o.put("positionMs", 0);
            o.put("durationMs", Math.max(0, durationMs));
            o.put("watched", false);
            o.put("dirty", false);
            return o;
        } catch (Exception e) {
            return null;
        }
    }

    /**
     * Найденная по названию папки серия получила номер тайтла. id записи не
     * меняется: витрина сопоставляет по номеру тайтла, сезону и серии, а
     * id для неё — просто ключ.
     */
    synchronized void setTitleId(String id, long titleId) {
        if (titleId <= 0) return;
        JSONArray a = read();
        int at = indexOf(a, id);
        if (at < 0) return;
        JSONObject o = a.optJSONObject(at);
        if (o == null || o.optLong("titleId") > 0) return;
        try {
            o.put("titleId", titleId);
            a.put(at, o);
            write(a);
        } catch (Exception e) {
            Log.w(TAG, "опись: номер тайтла: " + e);
        }
    }

    /** Длительность, узнанная по самому файлу, — для полоски прогресса до первого просмотра. */
    synchronized void setDuration(String id, long durationMs) {
        if (durationMs <= 0) return;
        JSONArray a = read();
        int at = indexOf(a, id);
        if (at < 0) return;
        JSONObject o = a.optJSONObject(at);
        if (o == null || o.optLong("durationMs") > 0) return;
        try {
            o.put("durationMs", durationMs);
            a.put(at, o);
            write(a);
        } catch (Exception e) {
            Log.w(TAG, "опись: длительность: " + e);
        }
    }

    private boolean exists(Uri u) {
        if (u == null) return false;
        try {
            if ("file".equals(u.getScheme())) return new File(u.getPath()).exists();
            DocumentFile d = DocumentFile.fromSingleUri(ctx, u);
            if (d != null && d.exists()) return true;
            // MediaStore не всегда отвечает через DocumentFile — спрашиваем напрямую.
            try (android.database.Cursor c =
                         ctx.getContentResolver().query(u, null, null, null, null)) {
                return c != null && c.moveToFirst();
            }
        } catch (Exception e) {
            return false;
        }
    }

    synchronized JSONObject get(String id) {
        JSONArray a = read();
        int at = indexOf(a, id);
        return at >= 0 ? a.optJSONObject(at) : null;
    }

    /** Запоминает, докуда досмотрели. Помечает запись как не отданную серверу. */
    synchronized void progress(String id, long positionMs, long durationMs, boolean watched) {
        JSONArray a = read();
        int at = indexOf(a, id);
        if (at < 0) return;
        JSONObject o = a.optJSONObject(at);
        if (o == null) return;
        try {
            o.put("positionMs", Math.max(0, positionMs));
            if (durationMs > 0) o.put("durationMs", durationMs);
            o.put("watched", watched || o.optBoolean("watched"));
            o.put("dirty", true);
            a.put(at, o);
            write(a);
        } catch (Exception e) {
            Log.w(TAG, "опись: прогресс: " + e);
        }
    }

    synchronized void setWatched(String id, boolean watched) {
        JSONArray a = read();
        int at = indexOf(a, id);
        if (at < 0) return;
        JSONObject o = a.optJSONObject(at);
        if (o == null) return;
        try {
            o.put("watched", watched);
            o.put("dirty", true);
            a.put(at, o);
            write(a);
        } catch (Exception e) {
            Log.w(TAG, "опись: отметка: " + e);
        }
    }

    /** Прогресс дошёл до сервера — больше его слать не нужно. */
    synchronized void synced(String id) {
        JSONArray a = read();
        int at = indexOf(a, id);
        if (at < 0) return;
        JSONObject o = a.optJSONObject(at);
        if (o == null) return;
        try {
            o.put("dirty", false);
            a.put(at, o);
            write(a);
        } catch (Exception e) {
            Log.w(TAG, "опись: отметка передачи: " + e);
        }
    }

    /**
     * Убирает серию: сначала файл, потом запись. Если файл стереть не вышло
     * (например, папку выбрали на карте, а карту вынули), запись остаётся —
     * иначе место занято, а в списке пусто, и вернуть его уже нечем.
     */
    synchronized boolean remove(String id) {
        JSONArray a = read();
        int at = indexOf(a, id);
        if (at < 0) return false;
        JSONObject o = a.optJSONObject(at);
        if (o == null) return false;
        Uri u = Uri.parse(o.optString("uri"));
        if (!delete(u)) {
            Log.w(TAG, "опись: файл не стёрся: " + u);
            return false;
        }
        JSONArray rest = new JSONArray();
        for (int i = 0; i < a.length(); i++) if (i != at) rest.put(a.opt(i));
        write(rest);
        return true;
    }

    private boolean delete(Uri u) {
        if (u == null) return true;
        try {
            if ("file".equals(u.getScheme())) {
                File f = new File(u.getPath());
                return !f.exists() || f.delete();
            }
            DocumentFile d = DocumentFile.fromSingleUri(ctx, u);
            if (d != null && d.exists() && d.delete()) return true;
            return ctx.getContentResolver().delete(u, null, null) > 0;
        } catch (Exception e) {
            Log.w(TAG, "опись: удаление: " + e);
            return false;
        }
    }

    /** Сколько места занято скачанным — для раздела «Скачанное». */
    synchronized long bytes() {
        JSONArray a = list();
        long n = 0;
        for (int i = 0; i < a.length(); i++) {
            JSONObject o = a.optJSONObject(i);
            if (o != null) n += o.optLong("size");
        }
        return n;
    }

    /** Подписи файлов, попавших в опись при последней загрузке, — для журнала. */
    static String describe(List<String> names) {
        return names.isEmpty() ? "—" : String.join(", ", names);
    }

    static List<String> names(List<File> files) {
        List<String> out = new ArrayList<>();
        for (File f : files) out.add(f.getName().toLowerCase(Locale.ROOT));
        return out;
    }
}
