package ru.sorewa.kino;

import android.app.Activity;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageInfo;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;
import android.util.Log;
import android.widget.ProgressBar;
import android.widget.Toast;
import androidx.appcompat.app.AlertDialog;
import androidx.core.content.FileProvider;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import org.json.JSONObject;

/**
 * Самообновление с нашего же сервера.
 *
 * Приложение стоит в обход магазина, и без этого каждое обновление оболочки
 * означало бы «скачай и поставь заново» для всей семьи. При холодном старте
 * и не чаще раза в шесть часов читается /app/version.json; если версия там
 * новее установленной, предлагается обновиться: APK скачивается в кеш,
 * сверяется по sha256 и отдаётся системному установщику. Подпись проверяет
 * сам Android: APK с чужим ключом поверх нашего он не поставит.
 *
 * Выкладывает файлы deploy/prod/publish-apk.sh.
 */
final class Updater {
    private static final String TAG = "kino";
    private static final long CHECK_EVERY_MS = 6L * 60 * 60 * 1000;
    private static final long SNOOZE_MS = 24L * 60 * 60 * 1000;

    private final Activity activity;
    private final String base;
    private final SharedPreferences prefs;
    /** Скачанный APK, который ждёт разрешения на установку. */
    private File pending;

    Updater(Activity activity, String serverUrl) {
        this.activity = activity;
        this.base = serverUrl.endsWith("/") ? serverUrl.substring(0, serverUrl.length() - 1) : serverUrl;
        this.prefs = activity.getSharedPreferences("updater", Context.MODE_PRIVATE);
    }

    /** Холодный старт: остатки прошлого обновления больше не нужны. */
    void cleanup() {
        File[] files = dir().listFiles();
        if (files != null) for (File f : files) f.delete();
    }

    /**
     * Возврат на передний план. Если ждали разрешения на установку и его
     * дали — ставим; иначе проверяем версию, но не чаще раза в шесть часов.
     */
    void onResume(boolean force) {
        if (pending != null && canInstall()) {
            File f = pending;
            pending = null;
            install(f);
            return;
        }
        long now = System.currentTimeMillis();
        if (!force && now - prefs.getLong("lastCheck", 0) < CHECK_EVERY_MS) return;
        prefs.edit().putLong("lastCheck", now).apply();
        new Thread(this::check, "kino-update-check").start();
    }

    private void check() {
        try {
            JSONObject v = new JSONObject(fetch(base + "/app/version.json"));
            long remote = v.getLong("versionCode");
            long local = installedCode();
            if (remote <= local) {
                Log.i(TAG, "обновление: стоит " + local + ", на сервере " + remote + " — нечего ставить");
                return;
            }
            boolean snoozed = prefs.getLong("snoozedCode", 0) == remote
                && System.currentTimeMillis() - prefs.getLong("snoozedAt", 0) < SNOOZE_MS;
            if (snoozed) return;
            String name = v.optString("versionName", String.valueOf(remote));
            String file = v.optString("file", "kino.apk");
            String sha = v.optString("sha256", "");
            Log.i(TAG, "обновление: есть " + name + " (" + remote + ")");
            activity.runOnUiThread(() -> offer(remote, name, file, sha));
        } catch (Exception e) {
            // Нет сети или сервера — не повод шуметь: витрина сама покажет,
            // что связи нет, а проверка повторится позже.
            Log.w(TAG, "обновление: проверка не удалась: " + e);
        }
    }

    private void offer(long code, String name, String file, String sha) {
        if (activity.isFinishing()) return;
        new AlertDialog.Builder(activity, androidx.appcompat.R.style.Theme_AppCompat_Dialog_Alert)
            .setTitle("Доступно обновление " + name)
            .setMessage("Загрузим и поставим поверх текущей версии. Вход и настройки сохранятся.")
            .setPositiveButton("Обновить", (d, w) -> download(file, sha))
            .setNegativeButton("Позже", (d, w) -> prefs.edit()
                .putLong("snoozedCode", code)
                .putLong("snoozedAt", System.currentTimeMillis()).apply())
            .show();
    }

    private void download(String file, String sha) {
        AlertDialog progress = new AlertDialog.Builder(activity, androidx.appcompat.R.style.Theme_AppCompat_Dialog_Alert)
            .setTitle("Загружаем обновление…")
            .setView(new ProgressBar(activity))
            .setCancelable(false)
            .show();
        new Thread(() -> {
            try {
                File out = new File(dir(), "kino.apk");
                String got = save(base + "/app/" + file, out);
                if (!sha.isEmpty() && !sha.equalsIgnoreCase(got)) {
                    throw new IllegalStateException("sha256 не совпал");
                }
                activity.runOnUiThread(() -> { progress.dismiss(); install(out); });
            } catch (Exception e) {
                Log.w(TAG, "обновление: не скачалось: " + e);
                activity.runOnUiThread(() -> {
                    progress.dismiss();
                    Toast.makeText(activity, "Не удалось скачать обновление", Toast.LENGTH_LONG).show();
                });
            }
        }, "kino-update-download").start();
    }

    /**
     * Установка. С Android 8 сначала нужно разовое разрешение «устанавливать
     * неизвестные приложения» именно для kino: без него установщик молча
     * не откроется.
     */
    private void install(File apk) {
        if (!canInstall()) {
            pending = apk;
            new AlertDialog.Builder(activity, androidx.appcompat.R.style.Theme_AppCompat_Dialog_Alert)
                .setTitle("Нужно разрешение")
                .setMessage("Разрешите kino ставить обновления и вернитесь в приложение.")
                .setPositiveButton("Открыть настройки", (d, w) -> activity.startActivity(
                    new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                        Uri.parse("package:" + activity.getPackageName()))))
                .setNegativeButton("Позже", null)
                .show();
            return;
        }
        Uri uri = FileProvider.getUriForFile(activity, activity.getPackageName() + ".fileprovider", apk);
        activity.startActivity(new Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, "application/vnd.android.package-archive")
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK));
    }

    private boolean canInstall() {
        return Build.VERSION.SDK_INT < Build.VERSION_CODES.O
            || activity.getPackageManager().canRequestPackageInstalls();
    }

    private long installedCode() throws Exception {
        PackageInfo pi = activity.getPackageManager().getPackageInfo(activity.getPackageName(), 0);
        return Build.VERSION.SDK_INT >= Build.VERSION_CODES.P ? pi.getLongVersionCode() : pi.versionCode;
    }

    private File dir() {
        File d = new File(activity.getCacheDir(), "update");
        //noinspection ResultOfMethodCallIgnored
        d.mkdirs();
        return d;
    }

    private static HttpURLConnection open(String url) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setConnectTimeout(8000);
        c.setReadTimeout(20000);
        c.setUseCaches(false);
        if (c.getResponseCode() != 200) throw new IllegalStateException("HTTP " + c.getResponseCode() + " " + url);
        return c;
    }

    private static String fetch(String url) throws Exception {
        HttpURLConnection c = open(url);
        try (InputStream in = c.getInputStream()) {
            ByteArrayOutputStream buf = new ByteArrayOutputStream();
            byte[] b = new byte[8192];
            int n;
            while ((n = in.read(b)) > 0) buf.write(b, 0, n);
            return buf.toString(StandardCharsets.UTF_8.name());
        } finally {
            c.disconnect();
        }
    }

    /** Скачивает файл и возвращает его sha256 в hex. */
    private static String save(String url, File out) throws Exception {
        HttpURLConnection c = open(url);
        MessageDigest md = MessageDigest.getInstance("SHA-256");
        try (InputStream in = c.getInputStream(); FileOutputStream fo = new FileOutputStream(out)) {
            byte[] b = new byte[64 * 1024];
            int n;
            while ((n = in.read(b)) > 0) {
                fo.write(b, 0, n);
                md.update(b, 0, n);
            }
        } finally {
            c.disconnect();
        }
        StringBuilder sb = new StringBuilder();
        for (byte x : md.digest()) sb.append(String.format("%02x", x));
        return sb.toString();
    }
}
