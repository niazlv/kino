package m3u8

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// Формы взяты с живого kinopub: мастер лежит на одном хосте, варианты
// и сегменты — на другом, аудиодорожек больше десятка.
const master = `#EXTM3U
#EXT-X-VERSION:4
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="DUB",LANGUAGE="ru",DEFAULT=YES,URI="audio/dub.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="П. Гланц",LANGUAGE="ru",URI="audio/glanz.m3u8"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="sub",NAME="rus",URI="subs/ru.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=5000000,CODECS="avc1.640028,mp4a.40.2",AUDIO="aud",SUBTITLES="sub"
https://cdn7.example/v/1080.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2000000,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="aud"
720.m3u8
#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=100000,URI="iframe/1080.m3u8"
`

func rewriteAll(t *testing.T, base, playlist string) string {
	t.Helper()
	b, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Rewriter{
		Base: b,
		Resolve: func(u *url.URL) (string, error) {
			return "/stream/tkt/" + url.QueryEscape(u.String()), nil
		},
	}.Rewrite(playlist)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	return got
}

// Главный инвариант: НИ ОДНОЙ ссылки наружу. Любая пропущенная уходит мимо
// авторизации, а для EXT-X-MEDIA это ещё и «видео идёт, звука нет».
func TestRewrite_NoOriginalURLSurvives(t *testing.T) {
	got := rewriteAll(t, "https://api.example/hls/master.m3u8", master)
	for _, leaked := range []string{
		`URI="audio/dub.m3u8"`,
		`URI="audio/glanz.m3u8"`,
		`URI="subs/ru.m3u8"`,
		`URI="iframe/1080.m3u8"`,
		"\nhttps://cdn7.example/v/1080.m3u8\n",
		"\n720.m3u8\n",
	} {
		if strings.Contains(got, leaked) {
			t.Errorf("ссылка утекла мимо переписывания: %s", leaked)
		}
	}
	if n := strings.Count(got, "/stream/tkt/"); n != 6 {
		t.Fatalf("переписано %d ссылок, ожидалось 6:\n%s", n, got)
	}
}

// Относительные пути обязаны разрешаться относительно ПЛЕЙЛИСТА, иначе
// вариант с другого хоста уедет не туда.
func TestRewrite_ResolvesRelativeAgainstPlaylist(t *testing.T) {
	got := rewriteAll(t, "https://api.example/hls/master.m3u8", master)
	if !strings.Contains(got, url.QueryEscape("https://api.example/hls/audio/dub.m3u8")) {
		t.Errorf("относительный URI разрешён неверно:\n%s", got)
	}
	if !strings.Contains(got, url.QueryEscape("https://api.example/hls/720.m3u8")) {
		t.Errorf("относительный сегмент разрешён неверно:\n%s", got)
	}
	// Абсолютный адрес должен остаться абсолютным, а не склеиться с базой.
	if !strings.Contains(got, url.QueryEscape("https://cdn7.example/v/1080.m3u8")) {
		t.Errorf("абсолютный URI испорчен:\n%s", got)
	}
}

// Атрибуты содержат запятые внутри кавычек (CODECS="avc1,mp4a").
// Разбор по запятым ломает строку — здесь это зафиксировано.
func TestRewrite_KeepsAttributesWithCommasIntact(t *testing.T) {
	got := rewriteAll(t, "https://api.example/hls/master.m3u8", master)
	if !strings.Contains(got, `CODECS="avc1.640028,mp4a.40.2"`) {
		t.Fatalf("атрибут с запятой испорчен:\n%s", got)
	}
	if !strings.Contains(got, `NAME="П. Гланц"`) {
		t.Fatalf("не-ASCII имя дорожки испорчено:\n%s", got)
	}
}

func TestRewrite_MediaPlaylistTagsWithURI(t *testing.T) {
	const media = `#EXTM3U
#EXT-X-TARGETDURATION:6
#EXT-X-KEY:METHOD=AES-128,URI="key.bin",IV=0x0
#EXT-X-MAP:URI="init.mp4"
#EXTINF:6.0,
seg0.ts
#EXTINF:6.0,
seg1.ts
#EXT-X-ENDLIST
`
	got := rewriteAll(t, "https://cdn7.example/v/media.m3u8", media)
	for _, must := range []string{"key.bin", "init.mp4", "seg0.ts", "seg1.ts"} {
		if !strings.Contains(got, url.QueryEscape("https://cdn7.example/v/"+must)) {
			t.Errorf("%s не переписан:\n%s", must, got)
		}
	}
	// Ключ шифрования — тоже ссылка, и её пропуск оставляет клиента без
	// расшифровки при внешне рабочем плейлисте.
	if strings.Contains(got, `URI="key.bin"`) {
		t.Error("EXT-X-KEY не переписан")
	}
	if !strings.Contains(got, "#EXT-X-ENDLIST") {
		t.Error("служебные теги должны сохраняться")
	}
}

// Тег без URI — законный случай (METHOD=NONE), падать на нём нельзя.
func TestRewrite_TagWithoutURIPassesThrough(t *testing.T) {
	got := rewriteAll(t, "https://x/y.m3u8", "#EXTM3U\n#EXT-X-KEY:METHOD=NONE\n")
	if !strings.Contains(got, "#EXT-X-KEY:METHOD=NONE") {
		t.Fatalf("тег без URI испорчен: %q", got)
	}
}

// Отказ Resolve обязан останавливать всё: половина переписанного плейлиста
// хуже ошибки — часть ссылок пойдёт мимо авторизации.
func TestRewrite_ResolveErrorAbortsWholePlaylist(t *testing.T) {
	b, _ := url.Parse("https://api.example/hls/master.m3u8")
	boom := errors.New("origin запрещён")
	_, err := Rewriter{Base: b, Resolve: func(*url.URL) (string, error) {
		return "", boom
	}}.Rewrite(master)
	if !errors.Is(err, boom) {
		t.Fatalf("ошибка Resolve проглочена: %v", err)
	}
}

func TestRewrite_RejectsUnterminatedURI(t *testing.T) {
	b, _ := url.Parse("https://x/y.m3u8")
	_, err := Rewriter{Base: b, Resolve: func(*url.URL) (string, error) { return "ok", nil }}.
		Rewrite("#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\n")
	if err == nil {
		t.Fatal("незакрытая кавычка должна быть ошибкой, а не молча пропущена")
	}
}

// Реклама, вклеенная прямо в поток: чужой хост между разрывами. Вырезается
// сегмент вместе со своими тегами, остальное остаётся на месте.
func TestRewrite_DropsAdSegmentsWithTheirTags(t *testing.T) {
	const media = `#EXTM3U
#EXT-X-TARGETDURATION:6
#EXTINF:6.0,
seg1.ts
#EXT-X-DISCONTINUITY
#EXTINF:5.0,
https://ads.example/ad1.ts
#EXTINF:5.0,
https://ads.example/ad2.ts
#EXT-X-DISCONTINUITY
#EXTINF:6.0,
seg2.ts
#EXT-X-ENDLIST
`
	b, _ := url.Parse("https://cdn.example/v/index.m3u8")
	got, err := Rewriter{
		Base: b,
		Resolve: func(u *url.URL) (string, error) {
			if u.Host == "ads.example" {
				return "", ErrDrop
			}
			return "/r/" + u.Path, nil
		},
	}.Rewrite(media)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if strings.Contains(got, "ads.example") || strings.Count(got, "#EXTINF") != 2 {
		t.Fatalf("реклама не вырезана:\n%s", got)
	}
	// Второй разрыв стоит перед настоящим сегментом и обязан остаться:
	// за ним плеер перестраивается на продолжение контента.
	if strings.Count(got, "#EXT-X-DISCONTINUITY") != 1 {
		t.Fatalf("разрывы вырезаны неверно:\n%s", got)
	}
	want := "#EXTINF:6.0,\n/r//v/seg1.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:6.0,\n/r//v/seg2.ts\n#EXT-X-ENDLIST\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("порядок нарушен:\n%s", got)
	}
}

// Вариант мастер-плейлиста вырезать нельзя: остался бы висящий
// #EXT-X-STREAM-INF. ErrDrop там — обычная ошибка.
func TestRewrite_DropOnVariantIsAnError(t *testing.T) {
	b, _ := url.Parse("https://api.example/hls/master.m3u8")
	_, err := Rewriter{
		Base: b,
		Resolve: func(u *url.URL) (string, error) {
			if strings.HasSuffix(u.Path, "720.m3u8") {
				return "", ErrDrop
			}
			return "/r", nil
		},
	}.Rewrite(master)
	if !errors.Is(err, ErrDrop) {
		t.Fatalf("ожидалась ошибка ErrDrop, получено %v", err)
	}
}
