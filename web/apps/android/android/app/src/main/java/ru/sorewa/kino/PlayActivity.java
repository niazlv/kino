package ru.sorewa.kino;

import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.view.WindowManager;
import android.widget.TextView;
import android.widget.Toast;

import androidx.annotation.OptIn;
import androidx.appcompat.app.AppCompatActivity;
import androidx.core.view.WindowCompat;
import androidx.core.view.WindowInsetsCompat;
import androidx.core.view.WindowInsetsControllerCompat;
import androidx.media3.common.MediaItem;
import androidx.media3.common.PlaybackException;
import androidx.media3.common.Player;
import androidx.media3.common.util.UnstableApi;
import androidx.media3.exoplayer.ExoPlayer;
import androidx.media3.ui.PlayerView;

import org.json.JSONObject;

/**
 * Просмотр скачанной серии.
 *
 * Этот экран существует ровно потому, что WebView не открывает Matroska —
 * а в нём лежат несколько озвучек и субтитры. Появляется он только при запуске
 * скачанного и закрывается обратно в витрину: отдельного «приложения со своим
 * интерфейсом» здесь нет, это та же оболочка.
 *
 * Где остановились — записывается в опись, а не на сервер: скачанное смотрят
 * как раз без сети. Витрина потом отдаст прогресс серверу, когда сеть будет.
 */
@OptIn(markerClass = UnstableApi.class)
public class PlayActivity extends AppCompatActivity {

    private static final String EXTRA_ID = "entry";
    private static final String EXTRA_POS = "position";
    /** Досмотренным считаем с 92%: дальше идут титры, возвращаться туда незачем. */
    private static final double WATCHED_AT = 0.92;

    static Intent intent(Context ctx, String id, long positionMs) {
        return new Intent(ctx, PlayActivity.class)
                .putExtra(EXTRA_ID, id)
                .putExtra(EXTRA_POS, positionMs);
    }

    private ExoPlayer player;
    private PlayerView view;
    private Library library;
    private String id;
    private final Handler ticker = new Handler(Looper.getMainLooper());
    private final Runnable save = new Runnable() {
        @Override public void run() {
            store();
            ticker.postDelayed(this, 10_000);
        }
    };

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        setContentView(R.layout.activity_play);
        view = findViewById(R.id.player);

        library = Library.of(this);
        id = getIntent().getStringExtra(EXTRA_ID);
        JSONObject entry = id == null ? null : library.get(id);
        if (entry == null) {
            Toast.makeText(this, "Эта серия больше не скачана", Toast.LENGTH_LONG).show();
            finish();
            return;
        }

        TextView caption = findViewById(R.id.caption);
        caption.setText(label(entry));

        // Кино смотрят целиком: гасить экран посреди сцены нельзя, а полосы
        // системы отнимают кадр.
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        WindowCompat.setDecorFitsSystemWindows(getWindow(), false);
        WindowInsetsControllerCompat bars =
                WindowCompat.getInsetsController(getWindow(), getWindow().getDecorView());
        bars.hide(WindowInsetsCompat.Type.systemBars());
        bars.setSystemBarsBehavior(
                WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);

        long start = getIntent().getLongExtra(EXTRA_POS, 0);
        if (start <= 0) start = entry.optLong("positionMs");

        player = new ExoPlayer.Builder(this).build();
        view.setPlayer(player);
        view.setShowSubtitleButton(true);
        // С пульта первым делом жмут «вниз» или «ок» — управление должно быть
        // уже под рукой, а не появляться после слепого тычка.
        view.requestFocus();

        player.addListener(new Player.Listener() {
            @Override public void onPlayerError(PlaybackException error) {
                Toast.makeText(PlayActivity.this,
                        "Не воспроизвести: " + error.getErrorCodeName(), Toast.LENGTH_LONG).show();
            }

            @Override public void onPlaybackStateChanged(int st) {
                if (st == Player.STATE_ENDED) {
                    library.setWatched(id, true);
                    finish();
                }
            }
        });

        player.setMediaItem(MediaItem.fromUri(Uri.parse(entry.optString("uri"))));
        if (start > 0) player.seekTo(start);
        player.setPlayWhenReady(true);
        player.prepare();
    }

    private static String label(JSONObject e) {
        String t = e.optString("title");
        int s = e.optInt("season"), n = e.optInt("episode");
        // У фильма сезон и серия всё равно первые: подпись «S01E01» на фильме
        // выглядит поломкой, хотя внутри это нормальный ключ.
        if (s <= 1 && n <= 1) return t;
        return t + " · S" + String.format("%02d", s) + "E" + String.format("%02d", n);
    }

    /** Кладёт в опись, докуда досмотрели. */
    private void store() {
        if (player == null || id == null) return;
        long pos = player.getCurrentPosition();
        long dur = player.getDuration();
        if (dur <= 0) return;
        boolean done = pos >= dur * WATCHED_AT;
        library.progress(id, pos, dur, done);
    }

    @Override protected void onStart() {
        super.onStart();
        ticker.postDelayed(save, 10_000);
    }

    @Override protected void onPause() {
        super.onPause();
        if (player != null) player.pause();
        store();
    }

    @Override protected void onStop() {
        super.onStop();
        ticker.removeCallbacks(save);
        store();
    }

    @Override protected void onDestroy() {
        super.onDestroy();
        ticker.removeCallbacks(save);
        if (player != null) {
            store();
            player.release();
            player = null;
        }
    }

    @Override public void onWindowFocusChanged(boolean has) {
        super.onWindowFocusChanged(has);
        if (has) view.setSystemUiVisibility(View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_FULLSCREEN);
    }
}
