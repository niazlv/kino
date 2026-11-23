package ru.sorewa.kino;

import android.graphics.Bitmap;
import android.net.Uri;
import android.webkit.CookieManager;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;
import com.getcapacitor.Bridge;
import com.getcapacitor.BridgeWebViewClient;
import java.io.ByteArrayInputStream;
import java.util.regex.Pattern;

/**
 * Клиент WebView поверх клиента Capacitor.
 *
 * 1. Превью скачанного: /_kino/art/<имя> отдаётся из личной папки
 *    приложения (Previews) — у витрины и у страницы без сети одинаково.
 * 2. Сторож загрузки: MainActivity узнаёт, что витрина начала и закончила
 *    грузиться, и без сети не ждёт таймаутов Capacitor (см. там).
 * 3. Запасной путь для «Скачать»: витрина в приложении открывает свой диалог
 *    и зовёт KinoBridge, но старая, закешированная страница может повести по
 *    прямой ссылке. Тогда серия уходит утилите как есть — все дорожки.
 */
public class KinoWebViewClient extends BridgeWebViewClient {
    private static final Pattern DOWNLOAD = Pattern.compile("^/api/v1/episodes/\\d+/download$");
    private final MainActivity activity;

    KinoWebViewClient(Bridge bridge, MainActivity activity) {
        super(bridge);
        this.activity = activity;
    }

    @Override
    public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
        String path = request.getUrl().getPath();
        if (path != null && path.startsWith(Previews.PATH)) {
            WebResourceResponse r = Previews.serve(activity, path.substring(Previews.PATH.length()));
            return r != null ? r
                : new WebResourceResponse("text/plain", "utf-8", 404, "Not Found", null, new ByteArrayInputStream(new byte[0]));
        }
        return super.shouldInterceptRequest(view, request);
    }

    @Override
    public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
        Uri u = request.getUrl();
        if (u.getPath() != null && DOWNLOAD.matcher(u.getPath()).matches() && Downloads.hasFFmpeg(activity)) {
            DownloadService.Job j = new DownloadService.Job();
            j.pageUrl = view.getUrl();
            j.cookie = CookieManager.getInstance().getCookie(j.pageUrl);
            j.userAgent = view.getSettings().getUserAgentString();
            DownloadService.enqueue(activity, j);
            return true;
        }
        boolean handled = super.shouldOverrideUrlLoading(view, request);
        // «Повторить» на странице без сети — снова идём на сервер, и снова
        // под присмотром сторожа.
        if (!handled && request.isForMainFrame()) activity.serverLoadStarted(u.toString());
        return handled;
    }

    @Override
    public void onPageStarted(WebView view, String url, Bitmap favicon) {
        super.onPageStarted(view, url, favicon);
        activity.pageArrived();
    }
}
