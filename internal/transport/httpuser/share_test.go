package httpuser

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	dshare "github.com/niazleushkin/kino/internal/domain/share"
)

func TestLatinizeCutsByRunesNotBytes(t *testing.T) {
	// Кириллица занимает два байта на символ: срез по байтам обрубил бы
	// имя вдвое раньше и мог оставить половину символа.
	long := strings.Repeat("я", 120)
	got := latinize(long)
	if n := len([]rune(got)); n != 80 {
		t.Fatalf("хотели 80 символов, получили %d", n)
	}
	if !utf8.ValidString(got) {
		t.Fatal("имя файла — не валидный UTF-8")
	}
}

func TestFileNameForMovieHasNoSeasonSuffix(t *testing.T) {
	if got := fileNameFor("Хороший фильм", 0, 0); got != "Хороший.фильм" {
		t.Fatalf("получили %q", got)
	}
	if got := fileNameFor("Сериал", 3, 13); got != "Сериал.S03E13" {
		t.Fatalf("получили %q", got)
	}
}

func TestManifestForEmptySelectionMeansAll(t *testing.T) {
	s := &Server{gatewayBase: "https://media.example"}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/episodes/7/manifest", nil)
	m := s.manifestFor(r, "tkt_1", time.Unix(0, 0).UTC(),
		dp.Source{Audios: []dp.AudioTrack{{ID: "a1", Language: "rus", Title: "AniLibria"}}},
		catalog.Episode{SeasonNo: 3, EpisodeNo: 13}, catalog.Title{Title: "Сериал"}, dshare.Selection{})

	if m.V != 1 || m.Protocol != "HLS" {
		t.Fatalf("v=%d protocol=%q", m.V, m.Protocol)
	}
	if want := "https://media.example/stream/tkt_1/master.m3u8"; m.URL != want {
		t.Fatalf("url = %q, want %q", m.URL, want)
	}
	if m.Filename != "Сериал.S03E13" {
		t.Fatalf("filename = %q", m.Filename)
	}
	if len(m.Audios) != 1 || m.Audios[0].Title != "AniLibria" {
		t.Fatalf("audios = %+v", m.Audios)
	}
	// Пустой выбор обязан сериализоваться как [], а не null: для загрузчика
	// null — «поля нет», а [] — «всё».
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"select":{"audios":[],"subtitles":[]}`) {
		t.Fatalf("select сериализован как %s", b)
	}
}

// Субтитры отдельными файлами: плееру — с адресом на шлюзе по номеру в
// тикете, утилите — никак: она берёт дорожки из плейлиста, а там их нет.
func TestSidecarSubtitlesOnlyForPlayer(t *testing.T) {
	// Шлюз на том же origin: конструктор срезает «/» из настройки до пустой строки.
	s := &Server{gatewayBase: ""}
	src := dp.Source{Subtitles: []dp.TextTrack{
		{ID: "0", Language: "eng", Title: "eng", URL: "https://cdn.example/in-stream.srt"},
		{ID: "s0", Language: "rus", Title: "Русские", URL: "https://cdn.example/sub_rus.vtt", Sidecar: true, Default: true},
		{ID: "s1", Language: "eng", Title: "English", URL: "https://cdn.example/sub_eng.vtt", Sidecar: true},
	}}

	got := s.playerTextTracks(src, "tkt_1")
	if len(got) != 3 || got[0].URL != "" || got[0].ID != "0" {
		t.Fatalf("дорожка потока: %+v", got)
	}
	if got[1].URL != "/stream/tkt_1/sub/0.vtt" || !got[1].Default || got[2].URL != "/stream/tkt_1/sub/1.vtt" || got[2].Default {
		t.Fatalf("отдельные файлы: %+v", got[1:])
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/episodes/7/manifest", nil)
	m := s.manifestFor(r, "tkt_1", time.Unix(0, 0).UTC(), src,
		catalog.Episode{SeasonNo: 1, EpisodeNo: 1}, catalog.Title{Title: "Сериал"}, dshare.Selection{})
	if len(m.Subtitles) != 1 || m.Subtitles[0].ID != "0" {
		t.Fatalf("манифест утилиты: %+v", m.Subtitles)
	}
}

// MP4 одним файлом качается как файл: протокол источника и адрес /file, а
// не плейлист, которого у источника нет.
func TestManifestForProgressiveSource(t *testing.T) {
	s := &Server{gatewayBase: "https://media.example"}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/episodes/7/manifest", nil)
	m := s.manifestFor(r, "tkt_1", time.Unix(0, 0).UTC(), dp.Source{Protocol: "PROGRESSIVE"},
		catalog.Episode{SeasonNo: 1, EpisodeNo: 2}, catalog.Title{Title: "Аниме"}, dshare.Selection{})
	if m.Protocol != "PROGRESSIVE" || m.URL != "https://media.example/stream/tkt_1/file" {
		t.Fatalf("protocol=%q url=%q", m.Protocol, m.URL)
	}
}
