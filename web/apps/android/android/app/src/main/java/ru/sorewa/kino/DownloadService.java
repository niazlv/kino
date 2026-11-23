package ru.sorewa.kino;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.ContentValues;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.os.IBinder;
import android.provider.MediaStore;
import android.util.Log;
import androidx.core.app.NotificationCompat;
import androidx.documentfile.provider.DocumentFile;
import java.io.BufferedReader;
import java.io.File;
import java.io.FileInputStream;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import org.json.JSONObject;

/**
 * Очередь загрузок: запускает утилиту, читает её JSON-прогресс, показывает
 * его в уведомлении и по окончании переносит готовые файлы туда, где их
 * увидит человек — в выбранную папку или в «Фильмы» через MediaStore.
 *
 * Сервис переднего плана: закачка переживает сворачивание и уход в другое
 * приложение; убить её можно кнопкой в уведомлении.
 */
public class DownloadService extends Service {
    private static final String TAG = "kino";
    static final String CHANNEL = "downloads";
    static final String ACTION_ENQUEUE = "ru.sorewa.kino.ENQUEUE";
    static final String ACTION_CANCEL = "ru.sorewa.kino.CANCEL";
    static final String ACTION_PAUSE = "ru.sorewa.kino.PAUSE";
    static final String ACTION_RESUME = "ru.sorewa.kino.RESUME";
    private static final int NOTE_ID = 1001;

    /** Одно задание — одна ссылка со своим выбором. */
    static final class Job {
        String pageUrl, cookie, userAgent, title, quality, audio, subs, seasons;
        Intent toIntent(Context ctx) {
            return new Intent(ctx, DownloadService.class).setAction(ACTION_ENQUEUE)
                .putExtra("pageUrl", pageUrl).putExtra("cookie", cookie).putExtra("userAgent", userAgent)
                .putExtra("title", title).putExtra("quality", quality).putExtra("audio", audio).putExtra("subs", subs)
                .putExtra("seasons", seasons);
        }
        static Job from(Intent i) {
            Job j = new Job();
            j.pageUrl = i.getStringExtra("pageUrl"); j.cookie = i.getStringExtra("cookie");
            j.userAgent = i.getStringExtra("userAgent"); j.title = i.getStringExtra("title");
            j.quality = i.getStringExtra("quality"); j.audio = i.getStringExtra("audio"); j.subs = i.getStringExtra("subs");
            j.seasons = i.getStringExtra("seasons");
            return j;
        }
    }

    static void enqueue(Context ctx, Job job) {
        Intent i = job.toIntent(ctx);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) ctx.startForegroundService(i); else ctx.startService(i);
    }

    private final ArrayDeque<Job> queue = new ArrayDeque<>();
    private Thread worker;
    private volatile Process current;
    private volatile boolean cancelled;
    private volatile boolean pausing;
    /** Остановленное «Паузой» задание ждёт «Продолжить»; утилита доскачает с того же сегмента. */
    private Job paused;
    private int seq;

    @Override public IBinder onBind(Intent intent) { return null; }

    @Override
    public void onCreate() {
        super.onCreate();
        NotificationManager nm = getSystemService(NotificationManager.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel ch = new NotificationChannel(CHANNEL, "Загрузки", NotificationManager.IMPORTANCE_LOW);
            ch.setDescription("Ход скачивания серий");
            nm.createNotificationChannel(ch);
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent != null && ACTION_CANCEL.equals(intent.getAction())) {
            cancelled = true;
            Process p = current;
            if (p != null) p.destroy();
            synchronized (queue) { queue.clear(); paused = null; }
            if (p == null) { wipeWork(); stopForeground(STOP_FOREGROUND_REMOVE); stopSelf(); }
            return START_NOT_STICKY;
        }
        if (intent != null && ACTION_PAUSE.equals(intent.getAction())) {
            pausing = true;
            Process p = current;
            if (p != null) p.destroy();
            return START_NOT_STICKY;
        }
        if (intent != null && ACTION_RESUME.equals(intent.getAction())) {
            synchronized (queue) {
                if (paused != null) { queue.addFirst(paused); paused = null; }
            }
            if (worker == null || !worker.isAlive()) {
                worker = new Thread(this::loop, "kino-downloads");
                worker.start();
            }
            return START_NOT_STICKY;
        }
        if (intent != null && ACTION_ENQUEUE.equals(intent.getAction())) {
            Job j = Job.from(intent);
            synchronized (queue) { queue.add(j); }
            startForeground(NOTE_ID, note(j.title == null || j.title.isEmpty() ? "Загрузка" : j.title, j.pageUrl, "В очереди", 0, true));
            if (worker == null || !worker.isAlive()) {
                worker = new Thread(this::loop, "kino-downloads");
                worker.start();
            }
        }
        return START_NOT_STICKY;
    }

    private void loop() {
        while (true) {
            Job j;
            synchronized (queue) { j = queue.poll(); }
            if (j == null) break;
            cancelled = false;
            pausing = false;
            run(j);
        }
        // На паузе сервис остаётся на переднем плане с уведомлением: иначе
        // система убьёт его вместе с остановленным заданием.
        synchronized (queue) {
            if (paused != null) {
                String t = paused.title == null || paused.title.isEmpty() ? "Загрузка" : paused.title;
                getSystemService(NotificationManager.class).notify(NOTE_ID, pausedNote(t, paused.pageUrl));
                return;
            }
        }
        stopForeground(STOP_FOREGROUND_REMOVE);
        stopSelf();
    }

    private void run(Job j) {
        String title = j.title == null || j.title.isEmpty() ? "Загрузка" : j.title;
        File work = Downloads.workDir(this);
        long started = System.currentTimeMillis();
        List<String> args = Downloads.baseArgs(this, j.cookie, j.userAgent);
        args.add("-o"); args.add(work.getPath());
        args.add("--container"); args.add("mkv");
        if (j.quality != null && !j.quality.isEmpty()) { args.add("-q"); args.add(j.quality); }
        if (j.audio != null && !j.audio.isEmpty()) { args.add("--audio"); args.add(j.audio); }
        if (j.subs != null && !j.subs.isEmpty()) { args.add("--subs"); args.add(j.subs); }
        if (j.seasons != null && !j.seasons.isEmpty()) { args.add("--seasons"); args.add(j.seasons); }
        args.add(j.pageUrl);

        String error = null, lastEpisodeError = null;
        int done = 0, count = 0;
        Log.i(TAG, "загрузка: " + String.join(" ", args).replaceAll("kino_session=[^ ;]+", "kino_session=…"));
        Process proc = null;
        try {
            final Process p = Downloads.process(this, args).start();
            proc = p;
            current = p;
            StringBuilder err = new StringBuilder();
            Thread errPump = new Thread(() -> {
                try (BufferedReader e = new BufferedReader(new InputStreamReader(p.getErrorStream()))) {
                    String line; while ((line = e.readLine()) != null) { if (err.length() < 8000) err.append(line).append('\n'); }
                } catch (Exception ignored) { }
            });
            errPump.start();
            long lastNote = 0;
            try (BufferedReader out = new BufferedReader(new InputStreamReader(p.getInputStream()))) {
                String line;
                while ((line = out.readLine()) != null) {
                    if (!line.startsWith("{")) continue;
                    JSONObject e = new JSONObject(line);
                    String type = e.optString("type");
                    switch (type) {
                        case "start":
                            count = e.optInt("total") - e.optInt("done");
                            if (j.title == null || j.title.isEmpty()) title = e.optString("title", title);
                            break;
                        case "episode":
                            if ("completed".equals(e.optString("state"))) done++;
                            if ("failed".equals(e.optString("state"))) lastEpisodeError = e.optString("error");
                            notify(title, j.pageUrl, e.optString("key") + " · " + stateText(e.optString("state")) + (count > 1 ? " · " + done + " из " + count : ""), 0, true);
                            break;
                        case "progress":
                            if (System.currentTimeMillis() - lastNote < 1000) break;
                            lastNote = System.currentTimeMillis();
                            String text = e.optString("key") + " · " + e.optInt("percent") + "%";
                            long dl = e.optLong("downloaded"), tot = e.optLong("totalBytes");
                            if (dl > 0) text += " · " + Downloads.bytes(dl) + (tot > 0 ? " из " + (e.optBoolean("approx") ? "~" : "") + Downloads.bytes(tot) : "");
                            notify(title, j.pageUrl, text, e.optInt("percent"), false);
                            break;
                        case "result":
                            if (!e.optString("error").isEmpty()) error = e.optString("error");
                            else if (e.optInt("succeeded") == 0 && e.optInt("failed") > 0)
                                error = lastEpisodeError != null ? lastEpisodeError : "ни одна серия не скачалась";
                            break;
                        default:
                            break;
                    }
                }
            }
            int code = p.waitFor();
            errPump.join(2000);
            if (!pausing && !cancelled && error == null && code != 0)
                error = Downloads.lastLine(err.toString(), "код выхода " + code);
        } catch (Exception ex) {
            // Пауза и отмена глушат утилиту через destroy(): чтение из
            // закрытого потока падает исключением, но это наш собственный
            // сигнал, а не сбой загрузки.
            if (!pausing && !cancelled) { Log.w(TAG, "загрузка: " + ex); error = ex.getMessage(); }
        }
        // Чтение падает в тот же миг, когда destroy() рвёт потоки, — а утилита
        // ещё доживает свои секунды. Дожидаемся её: иначе «Отменить» сотрёт
        // рабочую папку под живым процессом, и тот создаст её заново.
        if (proc != null) reap(proc);
        current = null;
        if (pausing) { synchronized (queue) { paused = j; } return; }
        if (cancelled) { wipeWork(); finish(title, "Отменено", null); return; }
        if (error != null) { finish(title, "Ошибка: " + error, null); return; }

        // Готовые файлы — туда, где их найдут: перенос показывается как продолжение работы.
        notify(title, j.pageUrl, "Переносим в папку…", 0, true);
        List<File> produced = newFiles(work, started);
        Uri open = null;
        int moved = 0;
        long titleId = Library.titleIdOf(j.pageUrl);
        for (File f : produced) {
            try {
                // Размер спрашиваем до переноса: export() забирает файл с собой.
                long size = f.length();
                String name = f.getName();
                Uri u = export(f, title);
                if (u != null) { open = u; moved++; }
                // В опись попадает только видео: субтитры вшиты в контейнер,
                // а отдельные файлы сами по себе не смотрят.
                int[] se = Library.seasonEpisode(name);
                boolean video = name.toLowerCase(Locale.ROOT).endsWith(".mkv")
                        || name.toLowerCase(Locale.ROOT).endsWith(".mp4");
                if (u != null && video && se != null && titleId > 0) {
                    Library.of(this).put(titleId, title, se[0], se[1], u, size);
                }
            } catch (Exception ex) {
                Log.w(TAG, "перенос " + f.getName() + ": " + ex);
                error = "не удалось перенести " + f.getName() + ": " + ex.getMessage();
            }
        }
        // Папка сериала в рабочем каталоге больше не нужна: файлы перенесены,
        // а оставшийся файл состояния заставил бы утилиту в следующий раз
        // молча пропустить «уже скачанное».
        if (error == null) {
            for (File f : produced) {
                File series = f;
                while (series.getParentFile() != null && !work.equals(series.getParentFile())) series = series.getParentFile();
                if (!work.equals(series)) deleteTree(series);
            }
        }
        // Сеть сейчас есть — самое время взять постер и нарезать кадры: потом
        // их будут смотреть как раз без неё.
        if (moved > 0) {
            Uri page = Uri.parse(j.pageUrl == null ? "" : j.pageUrl);
            Previews.refresh(this, page.getScheme() == null ? null : page.getScheme() + "://" + page.getEncodedAuthority());
        }
        finish(title, error != null ? error : (moved > 0 ? "Готово · " + moved + (moved == 1 ? " файл" : " файла") : "Готово"), open);
    }

    // Ждём смерти утилиты, опрашивая её код возврата: waitFor со сроком
    // появился только в API 26, а нам жить с 24. Упрямому процессу на
    // середине срока повторяем destroy(), который на Android бьёт насмерть.
    private static void reap(Process p) {
        for (int i = 0; i < 100; i++) {
            try { p.exitValue(); return; } catch (IllegalThreadStateException alive) {
                if (i == 20) p.destroy();
                try { Thread.sleep(100); } catch (InterruptedException stop) { Thread.currentThread().interrupt(); return; }
            }
        }
        Log.w(TAG, "утилита не завершилась за 10 с");
    }

    // «Отменить» — это отказ, а не пауза: недокачанные сегменты (гигабайты
    // в закрытой папке приложения) никому больше не нужны.
    private void wipeWork() {
        File[] kids = Downloads.workDir(this).listFiles();
        if (kids != null) for (File k : kids) deleteTree(k);
    }

    private static void deleteTree(File f) {
        File[] kids = f.listFiles();
        if (kids != null) for (File k : kids) deleteTree(k);
        //noinspection ResultOfMethodCallIgnored
        f.delete();
    }

    private static String stateText(String s) {
        switch (s) {
            case "started": return "качается";
            case "completed": return "готово";
            case "failed": return "ошибка";
            case "deferred": return "повторим позже";
            default: return s;
        }
    }

    /** Файлы, появившиеся в рабочей папке после старта: их и переносим. */
    private static List<File> newFiles(File dir, long since) {
        List<File> out = new ArrayList<>();
        File[] kids = dir.listFiles();
        if (kids == null) return out;
        for (File f : kids) {
            if (f.isDirectory()) { out.addAll(newFiles(f, since)); continue; }
            String n = f.getName().toLowerCase(Locale.ROOT);
            if (f.lastModified() >= since - 60_000 && (n.endsWith(".mkv") || n.endsWith(".mp4") || n.endsWith(".srt"))) out.add(f);
        }
        return out;
    }

    /**
     * Перенос одного файла: в выбранную папку (SAF) или в «Фильмы/kino»
     * через MediaStore. Возвращает адрес, по которому файл можно открыть.
     */
    private Uri export(File f, String title) throws Exception {
        Uri tree = Downloads.tree(this);
        String mime = f.getName().endsWith(".srt") ? "application/x-subrip" : "video/x-matroska";
        if (tree != null) {
            DocumentFile root = DocumentFile.fromTreeUri(this, tree);
            if (root != null && root.canWrite()) {
                DocumentFile dir = root.findFile(safe(title));
                if (dir == null || !dir.isDirectory()) dir = root.createDirectory(safe(title));
                if (dir == null) dir = root;
                DocumentFile old = dir.findFile(f.getName());
                if (old != null) old.delete();
                DocumentFile target = dir.createFile(mime, f.getName());
                if (target == null) throw new Exception("папка не даёт создать файл");
                copy(f, getContentResolver().openOutputStream(target.getUri()));
                //noinspection ResultOfMethodCallIgnored
                f.delete();
                return target.getUri();
            }
        }
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return Uri.fromFile(f); // остаётся в папке приложения
        ContentValues v = new ContentValues();
        v.put(MediaStore.MediaColumns.DISPLAY_NAME, f.getName());
        v.put(MediaStore.MediaColumns.MIME_TYPE, mime);
        v.put(MediaStore.MediaColumns.RELATIVE_PATH, "Movies/kino/" + safe(title));
        v.put(MediaStore.MediaColumns.IS_PENDING, 1);
        Uri table = f.getName().endsWith(".srt") ? MediaStore.Files.getContentUri("external") : MediaStore.Video.Media.EXTERNAL_CONTENT_URI;
        Uri u = getContentResolver().insert(table, v);
        if (u == null) throw new Exception("MediaStore отказал");
        copy(f, getContentResolver().openOutputStream(u));
        v.clear(); v.put(MediaStore.MediaColumns.IS_PENDING, 0);
        getContentResolver().update(u, v, null, null);
        //noinspection ResultOfMethodCallIgnored
        f.delete();
        return u;
    }

    private static void copy(File f, OutputStream out) throws Exception {
        if (out == null) throw new Exception("нет потока для записи");
        try (InputStream in = new FileInputStream(f); OutputStream o = out) {
            byte[] buf = new byte[1 << 20];
            int n;
            while ((n = in.read(buf)) > 0) o.write(buf, 0, n);
        }
    }

    private static String safe(String s) {
        String t = s == null ? "" : s.replaceAll("[\\\\/:*?\"<>|]+", " ").trim();
        return t.isEmpty() ? "kino" : t;
    }

    // ── уведомления ───────────────────────────────────────────────────────

    private PendingIntent action(String action, int req) {
        Intent i = new Intent(this, DownloadService.class).setAction(action);
        return PendingIntent.getService(this, req, i, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }

    /** Нажатие на уведомление открывает в приложении страницу серии. */
    private PendingIntent openPage(String pageUrl) {
        Intent i = new Intent(this, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_SINGLE_TOP);
        if (pageUrl != null) i.putExtra("open", pageUrl);
        return PendingIntent.getActivity(this, 3, i, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }

    private NotificationCompat.Builder base(String title, String text, String pageUrl) {
        return new NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_kino).setColor(0xFF3DDC84)
            .setContentTitle(title).setContentText(text)
            .setContentIntent(openPage(pageUrl))
            .setOnlyAlertOnce(true).setSilent(true);
    }

    private Notification note(String title, String pageUrl, String text, int percent, boolean indeterminate) {
        return base(title, text, pageUrl)
            .setProgress(100, percent, indeterminate).setOngoing(true)
            .addAction(0, "Пауза", action(ACTION_PAUSE, 1))
            .addAction(0, "Отменить", action(ACTION_CANCEL, 2))
            .build();
    }

    private Notification pausedNote(String title, String pageUrl) {
        return base(title, "На паузе — продолжить можно здесь", pageUrl)
            .setOngoing(true)
            .addAction(0, "Продолжить", action(ACTION_RESUME, 4))
            .addAction(0, "Отменить", action(ACTION_CANCEL, 2))
            .build();
    }

    private void notify(String title, String pageUrl, String text, int percent, boolean indeterminate) {
        getSystemService(NotificationManager.class).notify(NOTE_ID, note(title, pageUrl, text, percent, indeterminate));
    }

    /** Итог остаётся отдельным уведомлением: нажатие открывает файл. */
    private void finish(String title, String text, Uri open) {
        NotificationCompat.Builder b = new NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_kino).setColor(0xFF3DDC84)
            .setContentTitle(title).setContentText(text)
            .setAutoCancel(true).setSilent(false);
        if (open != null) {
            Intent view = new Intent(Intent.ACTION_VIEW).setDataAndType(open, "video/*")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK);
            b.setContentIntent(PendingIntent.getActivity(this, ++seq, view, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE));
        }
        getSystemService(NotificationManager.class).notify(2000 + (++seq), b.build());
    }
}
