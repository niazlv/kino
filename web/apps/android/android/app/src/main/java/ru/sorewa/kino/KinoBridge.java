package ru.sorewa.kino;

import android.content.Intent;
import android.webkit.CookieManager;
import android.webkit.JavascriptInterface;
import android.webkit.WebView;
import android.widget.Toast;
import org.json.JSONArray;
import org.json.JSONObject;

/**
 * Объект kinoApp в окне витрины: так страница узнаёт, что она внутри
 * приложения, и отдаёт ему выбор для скачивания. Витрина показывает свой
 * диалог — в своём дизайне и сразу, из состава серии, который у неё уже
 * есть; оболочка только запускает утилиту.
 *
 * Методы зовутся из JS-потока WebView, поэтому всё, что трогает интерфейс,
 * уходит на главный поток. Страница — только наша (Capacitor не пускает
 * WebView на чужие адреса), но cookie всё равно берётся по адресу сервера,
 * а не из аргументов.
 */
public class KinoBridge {
    private final MainActivity act;
    private final WebView web;
    private final String server;

    KinoBridge(MainActivity act, WebView web, String server) {
        this.act = act;
        this.web = web;
        this.server = server;
    }

    /** {link, title, quality, audio: [], subtitles: [], seasons} → в очередь. */
    @JavascriptInterface
    public void download(String json) {
        try {
            JSONObject o = new JSONObject(json);
            DownloadService.Job j = new DownloadService.Job();
            j.pageUrl = o.optString("link");
            j.title = o.optString("title");
            j.quality = o.optString("quality");
            j.audio = join(o.optJSONArray("audio"));
            j.subs = join(o.optJSONArray("subtitles"));
            j.seasons = o.optString("seasons");
            j.cookie = CookieManager.getInstance().getCookie(server);
            act.runOnUiThread(() -> {
                j.userAgent = web.getSettings().getUserAgentString();
                DownloadService.enqueue(act, j);
                Toast.makeText(act, "В очереди: " + (j.title.isEmpty() ? "загрузка" : j.title), Toast.LENGTH_SHORT).show();
            });
        } catch (Exception e) {
            act.runOnUiThread(() -> Toast.makeText(act, "Не понял, что скачать: " + e.getMessage(), Toast.LENGTH_LONG).show());
        }
    }

    @JavascriptInterface
    public String folderName() { return Downloads.folderLabel(act); }

    @JavascriptInterface
    public void chooseFolder() {
        act.runOnUiThread(() -> act.startActivityForResult(
            new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION
                | Intent.FLAG_GRANT_WRITE_URI_PERMISSION | Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION),
            MainActivity.REQ_TREE));
    }

    /**
     * Опись скачанного для витрины: массив записей с ключом «тайтл:сезон:серия».
     * Витрина сама сопоставляет их со своими сериями по номерам — числовой id
     * серии оболочке неизвестен, да и незачем: номера переживают переобход
     * каталога, а id может смениться.
     */
    @JavascriptInterface
    public String library() { return Library.of(act).listWithArt().toString(); }

    /**
     * Найденный файл (его не было в описи) получил номер тайтла: витрина
     * нашла тайтл по названию папки, пока была сеть.
     */
    @JavascriptInterface
    public void setTitleId(String id, String titleId) {
        Library.of(act).setTitleId(id, parseLong(titleId));
        Previews.refresh(act, server); // теперь есть чей постер докачать
    }

    /** Адрес витрины — для «Повторить» на странице без сети. */
    @JavascriptInterface
    public String server() { return server; }

    /** Сколько места занято скачанным, в байтах. */
    @JavascriptInterface
    public String librarySize() { return String.valueOf(Library.of(act).bytes()); }

    /**
     * Открывает скачанную серию во встроенном плеере. Через WebView её не
     * показать: он не умеет Matroska, а именно в нём лежат несколько озвучек
     * и субтитры.
     */
    @JavascriptInterface
    public void play(String id, String positionMs) {
        act.runOnUiThread(() -> {
            try {
                // Выбор плеера спрашивается здесь, а не у витрины: так же
                // ведёт себя страница без сети, у которой своих настроек нет.
                if ("external".equals(getSetting("player"))) {
                    JSONObject e = Library.of(act).get(id);
                    if (e != null && ExternalPlayer.open(act, android.net.Uri.parse(e.optString("uri")),
                            e.optString("title"), parseLong(positionMs), "video/*")) {
                        // Внешний плеер не сообщает, докуда досмотрели: место
                        // остановки в описи не сдвинется, и это честнее, чем
                        // сбросить его в ноль.
                        return;
                    }
                }
                act.startActivity(PlayActivity.intent(act, id, parseLong(positionMs)));
            } catch (Exception e) {
                Toast.makeText(act, "Не открыть: " + e.getMessage(), Toast.LENGTH_LONG).show();
            }
        });
    }

    /** Стереть скачанное. Возвращает «1», если файла больше нет. */
    @JavascriptInterface
    public String remove(String id) { return Library.of(act).remove(id) ? "1" : "0"; }

    @JavascriptInterface
    public void setWatched(String id, boolean watched) { Library.of(act).setWatched(id, watched); }

    /** Витрина отдала прогресс серверу — больше его копить не нужно. */
    @JavascriptInterface
    public void synced(String id) { Library.of(act).synced(id); }

    /**
     * Открывает видео в стороннем плеере через системный выбор.
     *
     * {url, title, positionMs, mime}. Для скачанного сюда приходит content://
     * из описи, для потока — ссылка тикета: она авторизует сама, и внешнему
     * плееру не нужна наша сессия.
     *
     * Возвращает «0», если открыть нечем: витрина тогда играет сама, а не
     * оставляет человека перед пустым экраном.
     */
    @JavascriptInterface
    public String playExternal(String json) {
        try {
            JSONObject o = new JSONObject(json);
            String url = o.optString("url");
            if (url.isEmpty()) return "0";
            android.net.Uri uri = android.net.Uri.parse(url);
            return ExternalPlayer.open(act, uri, o.optString("title"),
                    o.optLong("positionMs"), o.optString("mime")) ? "1" : "0";
        } catch (Exception e) {
            return "0";
        }
    }

    /**
     * «Картинка в картинке» для плеера витрины: в WebView свой PiP не работает,
     * уменьшать окно умеет только система. Витрина перед вызовом разворачивает
     * плеер на весь экран — в форточку попадает весь WebView.
     */
    @JavascriptInterface
    public String pip(String width, String height) {
        final boolean[] ok = {false};
        final Object lock = new Object();
        act.runOnUiThread(() -> {
            boolean r = act.enterPip((int) parseLong(width), (int) parseLong(height));
            synchronized (lock) { ok[0] = r; lock.notifyAll(); }
        });
        synchronized (lock) {
            try { lock.wait(2000); } catch (InterruptedException e) { Thread.currentThread().interrupt(); }
        }
        return ok[0] ? "1" : "0";
    }

    /**
     * Витрина сообщает, идёт ли просмотр и какого размера кадр. По этому
     * оболочка сама уводит приложение в форточку, когда человек уходит на
     * домашний экран, и рисует в ней нужную кнопку — «пауза» или «играть».
     */
    @JavascriptInterface
    public void mediaState(String playing, String width, String height) {
        boolean p = "1".equals(playing);
        int w = (int) parseLong(width), h = (int) parseLong(height);
        act.runOnUiThread(() -> act.onMediaState(p, w, h));
    }

    /** Настройки оболочки. Общие для витрины, встроенного плеера и страницы без сети. */
    @JavascriptInterface
    public String getSetting(String key) {
        return act.getSharedPreferences(Downloads.PREFS, android.content.Context.MODE_PRIVATE)
                .getString("settings/" + key, "");
    }

    @JavascriptInterface
    public void setSetting(String key, String value) {
        act.getSharedPreferences(Downloads.PREFS, android.content.Context.MODE_PRIVATE)
                .edit().putString("settings/" + key, value == null ? "" : value).apply();
    }

    private static long parseLong(String s) {
        try { return Long.parseLong(s); } catch (Exception e) { return 0; }
    }

    @JavascriptInterface
    public String version() {
        try {
            return act.getPackageManager().getPackageInfo(act.getPackageName(), 0).versionName;
        } catch (Exception e) {
            return "";
        }
    }

    private static String join(JSONArray a) {
        if (a == null) return "";
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < a.length(); i++) {
            String s = a.optString(i).trim();
            if (s.isEmpty()) continue;
            if (sb.length() > 0) sb.append(',');
            sb.append(s);
        }
        return sb.toString();
    }
}
