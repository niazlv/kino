package ru.sorewa.kino;

import android.content.Context;
import android.net.Uri;
import androidx.documentfile.provider.DocumentFile;
import java.io.File;
import java.util.ArrayList;
import java.util.List;

/**
 * Скачивание утилитой kinopub, встроенной в приложение.
 *
 * Сама утилита — тот же бинарник, что в командной строке, положенный в APK
 * под именем libkinopub.so (Android разрешает запускать только то, что лежит
 * среди нативных библиотек), и такой же минимальный ffmpeg (libffmpeg.so).
 * Приложение с ней разговаривает флагами и читает прогресс как JSON-строки
 * (--progress-json). Сессию платформы утилита получает из cookie WebView:
 * входить второй раз не нужно.
 *
 * Выбор дорожек делает витрина (её диалог, через KinoBridge); здесь — пути,
 * аргументы и папка. Сама закачка — в DownloadService.
 */
public final class Downloads {
    static final String PREFS = "downloads";
    static final String PREF_TREE = "tree";

    private Downloads() {}

    static String binary(Context ctx) { return new File(ctx.getApplicationInfo().nativeLibraryDir, "libkinopub.so").getPath(); }
    static String ffmpeg(Context ctx) { return new File(ctx.getApplicationInfo().nativeLibraryDir, "libffmpeg.so").getPath(); }
    static boolean hasFFmpeg(Context ctx) { return new File(ffmpeg(ctx)).canExecute(); }

    /** Куда утилита пишет: своя папка приложения, без разрешений. Готовые файлы переносит сервис. */
    static File workDir(Context ctx) {
        File d = new File(ctx.getExternalFilesDir(null), "downloads");
        //noinspection ResultOfMethodCallIgnored
        d.mkdirs();
        return d;
    }

    /** Общие аргументы: сессия из cookie, без уведомлений Termux, JSON-прогресс. */
    static List<String> baseArgs(Context ctx, String cookie, String userAgent) {
        List<String> a = new ArrayList<>();
        a.add(binary(ctx));
        a.add("--progress-json");
        a.add("--no-notify");
        a.add("--ffmpeg"); a.add(ffmpeg(ctx));
        if (cookie != null && !cookie.isEmpty()) { a.add("--cookie"); a.add(cookie); }
        if (userAgent != null && !userAgent.isEmpty()) { a.add("--user-agent"); a.add(userAgent); }
        return a;
    }

    static ProcessBuilder process(Context ctx, List<String> args) {
        ProcessBuilder pb = new ProcessBuilder(args);
        // HOME — для настроек и хранилища утилиты, TMPDIR — для сегментов.
        pb.environment().put("HOME", ctx.getFilesDir().getPath());
        pb.environment().put("TMPDIR", ctx.getCacheDir().getPath());
        return pb;
    }

    static String lastLine(String s, String fallback) {
        String[] lines = s.trim().split("\n");
        String last = lines.length > 0 ? lines[lines.length - 1].trim() : "";
        return last.isEmpty() ? fallback : last;
    }

    static String bytes(long n) {
        if (n <= 0) return "";
        String[] u = {"Б", "КБ", "МБ", "ГБ"};
        double v = n; int i = 0;
        while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
        return (v < 10 ? String.format("%.1f", v) : String.valueOf(Math.round(v))) + " " + u[i];
    }

    static Uri tree(Context ctx) {
        String s = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(PREF_TREE, null);
        return s == null ? null : Uri.parse(s);
    }

    /** Подпись папки для диалога витрины. */
    static String folderLabel(Context ctx) {
        Uri t = tree(ctx);
        if (t != null) {
            DocumentFile d = DocumentFile.fromTreeUri(ctx, t);
            if (d != null && d.canWrite()) return d.getName() == null ? t.getLastPathSegment() : d.getName();
        }
        return "Фильмы → kino";
    }
}
