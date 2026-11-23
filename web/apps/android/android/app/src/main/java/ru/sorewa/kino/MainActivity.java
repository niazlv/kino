package ru.sorewa.kino;

import android.app.DownloadManager;
import android.app.PendingIntent;
import android.app.PictureInPictureParams;
import android.app.RemoteAction;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.IntentFilter;
import android.content.res.Configuration;
import android.graphics.drawable.Icon;
import android.util.Rational;
import android.content.pm.ActivityInfo;
import android.Manifest;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.CookieManager;
import android.webkit.URLUtil;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.widget.FrameLayout;
import android.widget.Toast;
import androidx.activity.OnBackPressedCallback;
import androidx.annotation.RequiresApi;
import androidx.core.graphics.Insets;
import androidx.core.view.ViewCompat;
import androidx.core.view.WindowCompat;
import androidx.core.view.WindowInsetsCompat;
import androidx.core.app.ActivityCompat;
import androidx.core.view.WindowInsetsControllerCompat;
import com.getcapacitor.Bridge;
import com.getcapacitor.BridgeActivity;
import com.getcapacitor.BridgeWebChromeClient;
import java.net.URLDecoder;
import java.util.ArrayList;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Оболочка витрины. Своего интерфейса нет: WebView открывает адрес из
 * capacitor.config.ts, всё остальное приезжает с сервера обычным деплоем.
 *
 * Здесь только то, чего WebView и Capacitor не делают сами:
 *
 * 1. Полноэкранное видео. Клиент Capacitor принимает запрос и тут же его
 *    гасит, так что кнопка «полный экран» в плеере не делала ничего.
 * 2. Кнопка «назад». Ядро Capacitor 8 её не трогает, а системное поведение
 *    закрывает приложение с любой страницы. Здесь она выходит из полного
 *    экрана, листает историю WebView и только с первой страницы уводит
 *    приложение в фон.
 * 3. Системные панели. С Android 15 окно рисуется под строкой состояния,
 *    и шапка витрины оказывалась под часами. Витрина про панели телефона
 *    не знает и знать не должна, поэтому поля под них и под клавиатуру
 *    получает контейнер WebView.
 * 4. Скачивание. Без слушателя кнопка «Скачать» на странице серии молча
 *    ничего не делает. Файл уходит системному загрузчику: качается в фоне
 *    и виден в «Загрузках».
 * 5. Самообновление с нашего сервера — см. Updater.
 * 6. Встроенная качалка. «Скачать» на витрине перехватывается
 *    (KinoWebViewClient) и уходит утилите kinopub внутри APK (Downloads,
 *    DownloadService): все озвучки и субтитры, уведомления, папка.
 * 7. Телевизор. На устройстве с полкой Android TV к UA дописывается
 *    «kino-tv»: по ней витрина включает раскладку под пульт. Стрелки и OK
 *    пульта WebView сам отдаёт странице как клавиши.
 * 8. Без сети. Не ждём таймаутов Capacitor, а сразу показываем страницу
 *    скачанного (www/error.html); превью для неё готовит Previews.
 */
public class MainActivity extends BridgeActivity {

    private static final String TAG = "kino";
    static final int REQ_TREE = 41;
    static final int REQ_NOTIFY = 42;
    private FullscreenClient chrome;
    private Updater updater;

    /**
     * «Картинка в картинке» для витрины.
     *
     * Кнопка в плеере витрины сама по себе не работает: WebView отдаёт
     * document.pictureInPictureEnabled = false, и запрос молча отклоняется.
     * Уменьшать окно умеет только система, поэтому витрина зовёт нас.
     *
     * В окно попадает весь WebView. Страница, узнав о форточке (kinoPip),
     * оставляет в нём один плеер во всё окно; старая витрина для этого
     * сначала разворачивала плеер на весь экран.
     *
     * @return false, если система этого не умеет: тогда витрина не станет
     *         показывать кнопку, которая ничего не делает.
     */
    boolean enterPip(int width, int height) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O || !pipSupported()) return false;
        if (width > 0 && height > 0) { mediaW = width; mediaH = height; }
        try {
            return enterPictureInPictureMode(pipParams());
        } catch (Exception e) {
            Log.w(TAG, "картинка в картинке: " + e);
            return false;
        }
    }

    // ── форточка по-нативному ────────────────────────────────────────────
    // Витрина сообщает, идёт ли просмотр (KinoBridge.mediaState). Пока видео
    // играет, уход на домашний экран уводит приложение в форточку: с
    // Android 12 — сама система (autoEnter), раньше — onUserLeaveHint. На
    // паузе приложение сворачивается как обычно.
    static final String ACTION_PIP = "ru.sorewa.kino.PIP";
    private boolean mediaPlaying;
    private int mediaW = 16, mediaH = 9;
    private BroadcastReceiver pipReceiver;

    /** Форточка есть у системы, и это не телевизор: там её не ждут. */
    private boolean pipSupported() {
        return Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                && getPackageManager().hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)
                && !getPackageManager().hasSystemFeature(PackageManager.FEATURE_LEANBACK);
    }

    void onMediaState(boolean playing, int width, int height) {
        mediaPlaying = playing;
        if (width > 0 && height > 0) { mediaW = width; mediaH = height; }
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O || !pipSupported()) return;
        // Параметры обновляются и в самой форточке: так кнопка «пауза»
        // меняется на «играть» и обратно.
        try {
            setPictureInPictureParams(pipParams());
        } catch (Exception e) {
            Log.w(TAG, "параметры форточки: " + e);
        }
    }

    @RequiresApi(Build.VERSION_CODES.O)
    private PictureInPictureParams pipParams() {
        // Система принимает соотношения примерно от 1:2.39 до 2.39:1;
        // за этими границами вызов падает, поэтому зажимаем сами.
        int w = mediaW, h = mediaH;
        double r = (double) w / h;
        if (r > 2.39) { w = 239; h = 100; }
        if (r < 1 / 2.39) { w = 100; h = 239; }
        PictureInPictureParams.Builder b = new PictureInPictureParams.Builder()
                .setAspectRatio(new Rational(w, h))
                .setActions(pipActions());
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            // Видео, а не страница: плавная подгонка содержимого не нужна,
            // а автовход — только пока идёт просмотр.
            b.setAutoEnterEnabled(mediaPlaying).setSeamlessResizeEnabled(false);
        }
        return b.build();
    }

    /** Кнопки форточки: −15, пауза или «играть», +15 — как у панели плеера. */
    @RequiresApi(Build.VERSION_CODES.O)
    private List<RemoteAction> pipActions() {
        List<RemoteAction> list = new ArrayList<>();
        list.add(pipAction(1, "back", android.R.drawable.ic_media_rew, "Назад 15 секунд"));
        list.add(mediaPlaying
                ? pipAction(2, "toggle", android.R.drawable.ic_media_pause, "Пауза")
                : pipAction(2, "toggle", android.R.drawable.ic_media_play, "Играть"));
        list.add(pipAction(3, "fwd", android.R.drawable.ic_media_ff, "Вперёд 15 секунд"));
        return list;
    }

    @RequiresApi(Build.VERSION_CODES.O)
    private RemoteAction pipAction(int code, String cmd, int icon, String title) {
        // Приёмник зарегистрирован без экспорта: адресуем его по пакету,
        // иначе широковещательное сообщение до него не дойдёт.
        Intent i = new Intent(ACTION_PIP).setPackage(getPackageName()).putExtra("cmd", cmd);
        PendingIntent pi = PendingIntent.getBroadcast(this, code, i,
                PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        return new RemoteAction(Icon.createWithResource(this, icon), title, title, pi);
    }

    /** Кнопка форточки нажата — команду исполняет плеер на странице. */
    private void registerPipReceiver() {
        if (!pipSupported() || pipReceiver != null) return;
        pipReceiver = new BroadcastReceiver() {
            @Override
            public void onReceive(Context ctx, Intent intent) {
                String cmd = intent.getStringExtra("cmd");
                if (cmd == null || !cmd.matches("[a-z]+")) return;
                getBridge().getWebView().evaluateJavascript(
                        "window.kinoPipAction && window.kinoPipAction('" + cmd + "')", null);
            }
        };
        IntentFilter f = new IntentFilter(ACTION_PIP);
        if (Build.VERSION.SDK_INT >= 33) registerReceiver(pipReceiver, f, Context.RECEIVER_NOT_EXPORTED);
        else registerReceiver(pipReceiver, f);
    }

    /** До Android 12 автовхода нет: уходим в форточку сами, пока видео играет. */
    @Override
    protected void onUserLeaveHint() {
        super.onUserLeaveHint();
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && Build.VERSION.SDK_INT < Build.VERSION_CODES.S
                && mediaPlaying && pipSupported()
                && !isInPictureInPictureMode()) {
            try {
                enterPictureInPictureMode(pipParams());
            } catch (Exception e) {
                Log.w(TAG, "форточка при уходе: " + e);
            }
        }
    }

    /** Страница показывает в форточке только плеер и прячет свою панель. */
    @Override
    public void onPictureInPictureModeChanged(boolean inPip, Configuration cfg) {
        super.onPictureInPictureModeChanged(inPip, cfg);
        Log.i(TAG, "форточка: " + inPip);
        getBridge().getWebView().evaluateJavascript(
                "window.kinoPip && window.kinoPip(" + inPip + ")", null);
    }

    /**
     * Приложение ушло с экрана совсем: закрыли форточку крестиком, погасили
     * экран или ушли домой на паузе. В форточке активность только на паузе,
     * а не остановлена, так что это не мешает смотреть в ней. Без этого
     * закрытая форточка продолжала бы играть звук невидимого видео.
     */
    @Override
    public void onStop() {
        super.onStop();
        if (isChangingConfigurations()) return;
        getBridge().getWebView().evaluateJavascript(
                "window.kinoPipAction && window.kinoPipAction('pause')", null);
    }

    @Override
    public void onDestroy() {
        if (pipReceiver != null) {
            try { unregisterReceiver(pipReceiver); } catch (Exception ignored) { }
            pipReceiver = null;
        }
        if (Previews.listener == libraryChanged) Previews.listener = null;
        offlineTimer.removeCallbacks(giveUp);
        super.onDestroy();
    }

    // ── без сети ────────────────────────────────────────────────────────
    // Витрина живёт на сервере, и без сети её не показать. Capacitor сам
    // запрашивает её страницу с таймаутами по 30 секунд на соединение и на
    // чтение: на Wi-Fi без интернета или при слабом сигнале человек минуту
    // смотрел на заставку, прежде чем видел скачанное. Поэтому: сети нет
    // совсем — сразу страница скачанного; сеть есть, но страница не пришла
    // за несколько секунд — тоже она. «Повторить» там снова идёт на сервер.
    private final Handler offlineTimer = new Handler(Looper.getMainLooper());
    private final Runnable giveUp = this::showOffline;
    private final Runnable libraryChanged = () -> {
        if (getBridge() != null) getBridge().getWebView().evaluateJavascript(
                "window.kinoLibraryChanged && window.kinoLibraryChanged()", null);
    };

    /** Пошли за страницей витрины: ждём её не дольше, чем позволяет сеть. */
    void serverLoadStarted(String url) {
        String server = getBridge().getConfig().getServerUrl();
        if (server == null || url == null || !url.startsWith(server)) return;
        offlineTimer.removeCallbacks(giveUp);
        long wait = serverWaitMs();
        Log.i(TAG, "витрина: ждём " + wait + " мс");
        if (wait == 0) offlineTimer.post(giveUp); else offlineTimer.postDelayed(giveUp, wait);
    }

    /** Страница пришла (витрина или страница без сети) — сторож не нужен. */
    void pageArrived() { offlineTimer.removeCallbacks(giveUp); }

    private void showOffline() {
        String err = getBridge().getErrorUrl();
        if (err == null) return;
        Log.i(TAG, "витрина не пришла — страница скачанного");
        WebView web = getBridge().getWebView();
        web.stopLoading();
        web.loadUrl(err);
    }

    /**
     * 0 — сети нет, ждать нечего. Сеть без проверенного выхода в интернет
     * (Wi-Fi с авторизацией, роутер без связи) — недолго. Обычная сеть —
     * с запасом на медленный мобильный интернет: страница витрины крошечная.
     */
    private long serverWaitMs() {
        try {
            ConnectivityManager cm = getSystemService(ConnectivityManager.class);
            Network n = cm == null ? null : cm.getActiveNetwork();
            NetworkCapabilities c = n == null ? null : cm.getNetworkCapabilities(n);
            if (c == null || !c.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) return 0;
            return c.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED) ? 15_000 : 5_000;
        } catch (Exception e) {
            return 15_000;
        }
    }

    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        WebView web = getBridge().getWebView();

        chrome = new FullscreenClient(getBridge());
        web.setWebChromeClient(chrome);
        web.setWebViewClient(new KinoWebViewClient(getBridge(), this));
        // Объект kinoApp в окне витрины: её диалог скачивания зовёт нас.
        String site = getBridge().getConfig().getServerUrl();
        if (site != null && !site.isEmpty()) {
            web.addJavascriptInterface(new KinoBridge(this, web, site), "kinoApp");
            // Первую загрузку витрины Bridge уже начал — ставим сторожа.
            serverLoadStarted(site);
        }
        // Скачанное: подхватить файлы, которых нет в описи, нарезать кадры,
        // докачать постеры, пока есть сеть. Страница узнает о новом сама.
        Previews.listener = libraryChanged;
        Previews.refresh(this, site);
        openFromIntent(getIntent());
        registerPipReceiver();

        // Уведомления о ходе загрузки: с Android 13 на них нужно согласие.
        if (Build.VERSION.SDK_INT >= 33
                && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            ActivityCompat.requestPermissions(this, new String[]{Manifest.permission.POST_NOTIFICATIONS}, REQ_NOTIFY);
        }

        if (getPackageManager().hasSystemFeature(PackageManager.FEATURE_LEANBACK)) {
            WebSettings ws = web.getSettings();
            ws.setUserAgentString(ws.getUserAgentString() + " kino-tv");
            Log.i(TAG, "телевизор: раскладка под пульт");
        }

        String server = getBridge().getConfig().getServerUrl();
        if (server != null && !server.isEmpty()) {
            updater = new Updater(this, server);
            updater.cleanup();
        }

        View frame = (View) web.getParent();
        frame.setBackgroundColor(0xFF0E1116);
        ViewCompat.setOnApplyWindowInsetsListener(frame, (v, insets) -> {
            Insets bars = insets.getInsets(
                WindowInsetsCompat.Type.systemBars() | WindowInsetsCompat.Type.displayCutout());
            Insets ime = insets.getInsets(WindowInsetsCompat.Type.ime());
            boolean keyboard = insets.isVisible(WindowInsetsCompat.Type.ime());
            v.setPadding(bars.left, bars.top, bars.right, keyboard ? ime.bottom : bars.bottom);
            return WindowInsetsCompat.CONSUMED;
        });

        getOnBackPressedDispatcher().addCallback(this, new OnBackPressedCallback(true) {
            @Override
            public void handleOnBackPressed() {
                Log.i(TAG, "назад: полный экран=" + chrome.isFullscreen() + ", история=" + web.canGoBack());
                // Из полного экрана — так же, как это делает Chrome: прячем
                // view, а WebView сам завершает fullscreen на странице.
                if (chrome.isFullscreen()) {
                    chrome.onHideCustomView();
                } else {
                    goBack(web);
                }
            }
        });

        web.setDownloadListener(
            (url, userAgent, contentDisposition, mimeType, contentLength) -> {
                String name = fileName(url, contentDisposition, mimeType);
                Log.i(TAG, "скачивание: " + name + " <- " + url);
                DownloadManager.Request req = new DownloadManager.Request(Uri.parse(url));
                // Шлюз пускает по тикету в адресе, но cookie не помешает:
                // так же сработают и ссылки, которым нужна сессия.
                String cookie = CookieManager.getInstance().getCookie(url);
                if (cookie != null) req.addRequestHeader("Cookie", cookie);
                req.addRequestHeader("User-Agent", userAgent);
                req.setTitle(name);
                req.setMimeType(mimeType);
                req.setNotificationVisibility(
                    DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);
                // Общие «Загрузки» без разрешений доступны с Android 10;
                // раньше — только папка приложения.
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                    req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name);
                } else {
                    req.setDestinationInExternalFilesDir(this, Environment.DIRECTORY_DOWNLOADS, name);
                }
                ((DownloadManager) getSystemService(DOWNLOAD_SERVICE)).enqueue(req);
                Toast.makeText(this, "Скачивается: " + name, Toast.LENGTH_SHORT).show();
            });
    }

    /** Нажатие на уведомление загрузки открывает страницу той серии. */
    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        openFromIntent(intent);
    }

    private void openFromIntent(Intent intent) {
        String url = intent == null ? null : intent.getStringExtra("open");
        if (url != null && !url.isEmpty()) {
            intent.removeExtra("open");
            getBridge().getWebView().loadUrl(url);
            serverLoadStarted(url);
        }
    }

    /** Папка для загрузок, выбранная в системном диалоге: право запоминается насовсем. */
    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != REQ_TREE || resultCode != RESULT_OK || data == null || data.getData() == null) return;
        android.net.Uri tree = data.getData();
        getContentResolver().takePersistableUriPermission(tree,
            Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_GRANT_WRITE_URI_PERMISSION);
        getSharedPreferences(Downloads.PREFS, MODE_PRIVATE).edit().putString(Downloads.PREF_TREE, tree.toString()).apply();
    }

    private boolean firstResume = true;

    @Override
    public void onResume() {
        super.onResume();
        if (updater != null) {
            // Холодный старт проверяет версию всегда, возвраты — по расписанию.
            updater.onResume(firstResume);
            firstResume = false;
        }
    }

    /**
     * «Назад» по-честному: сначала страница закрывает то, что открыла сама
     * (диалог, меню плеера), и лишь потом листается история. Ответ страницы
     * приходит асинхронно, поэтому решение принимается в обратном вызове.
     */
    private void goBack(WebView web) {
        web.evaluateJavascript("!!(window.kinoBack && window.kinoBack())", handled -> {
            if ("true".equals(handled)) return;
            if (web.canGoBack()) web.goBack(); else moveTaskToBack(true);
        });
    }

    /**
     * В полном экране view Chromium забирает «назад» себе и ничего с ним не
     * делает: до обработчика активности клавиша не доходит. Перехват здесь,
     * до раздачи события по view.
     */
    @Override
    public boolean dispatchKeyEvent(KeyEvent event) {
        if (event.getKeyCode() == KeyEvent.KEYCODE_BACK && chrome != null && chrome.isFullscreen()) {
            if (event.getAction() == KeyEvent.ACTION_UP) chrome.onHideCustomView();
            return true;
        }
        return super.dispatchKeyEvent(event);
    }

    /**
     * Имя файла из Content-Disposition. Стандартный разборщик понимает только
     * filename= с ASCII-запасом («file.mp4»), а настоящее имя с кириллицей
     * шлюз кладёт в filename* по RFC 5987.
     */
    private static String fileName(String url, String disposition, String mime) {
        if (disposition != null) {
            Matcher m = Pattern.compile("filename\\*=UTF-8''([^;]+)").matcher(disposition);
            if (m.find()) {
                try {
                    return URLDecoder.decode(m.group(1).trim().replace("+", "%2B"), "UTF-8");
                } catch (Exception ignored) { /* ниже — запасной вариант */ }
            }
        }
        return URLUtil.guessFileName(url, disposition, mime);
    }

    /**
     * Полный экран: view плеера кладётся поверх окна, системные панели
     * прячутся, экран поворачивается в альбом. На выходе всё возвращается.
     */
    private class FullscreenClient extends BridgeWebChromeClient {
        private View custom;
        private CustomViewCallback done;

        FullscreenClient(Bridge bridge) { super(bridge); }

        boolean isFullscreen() { return custom != null; }

        @Override
        public void onShowCustomView(View view, CustomViewCallback callback) {
            if (custom != null) { callback.onCustomViewHidden(); return; }
            Log.i(TAG, "полный экран: вход");
            custom = view;
            done = callback;
            view.setBackgroundColor(Color.BLACK);
            FrameLayout decor = (FrameLayout) getWindow().getDecorView();
            decor.addView(view, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
            WindowInsetsControllerCompat bars = WindowCompat.getInsetsController(getWindow(), decor);
            bars.setSystemBarsBehavior(WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);
            bars.hide(WindowInsetsCompat.Type.systemBars());
            setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE);
        }

        @Override
        public void onHideCustomView() {
            if (custom == null) return;
            Log.i(TAG, "полный экран: выход");
            FrameLayout decor = (FrameLayout) getWindow().getDecorView();
            decor.removeView(custom);
            custom = null;
            WindowInsetsControllerCompat bars = WindowCompat.getInsetsController(getWindow(), decor);
            bars.show(WindowInsetsCompat.Type.systemBars());
            bars.setSystemBarsBehavior(WindowInsetsControllerCompat.BEHAVIOR_DEFAULT);
            setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED);
            done.onCustomViewHidden();
            done = null;
        }
    }
}
