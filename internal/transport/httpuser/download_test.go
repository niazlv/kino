package httpuser

import (
	"net/url"
	"slices"
	"testing"

	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Утилита просит манифест GET-запросом: выбор площадки и студии приходит
// строкой запроса и должен лечь в тот же Request, что у /playback.
func TestManifestRequestTakesChoiceFromQuery(t *testing.T) {
	q := url.Values{
		"voice":  {" anilibria "},
		"source": {"animego_me"},
		"avoid":  {"lordfilm, kinopub", "", "yummy"},
	}
	req := manifestRequest(5, 7, q)
	if req.User != 5 || req.Episode != 7 || !req.Client.SupportsHLS {
		t.Fatalf("кто и что: %+v", req)
	}
	if req.Voice != "anilibria" || req.Choice.Source != "animego_me" || req.Choice.Strict {
		t.Fatalf("выбор: voice=%q source=%q strict=%v", req.Voice, req.Choice.Source, req.Choice.Strict)
	}
	if !slices.Equal(req.Avoid, []string{"lordfilm", "kinopub", "yummy"}) {
		t.Fatalf("avoid: %q", req.Avoid)
	}

	// Без параметров — как раньше: лучшая площадка, её озвучка по умолчанию.
	if req := manifestRequest(5, 7, url.Values{}); req.Voice != "" || req.Choice.Source != "" || len(req.Avoid) != 0 {
		t.Fatalf("пустой запрос: %+v", req)
	}
}

// У площадок, где студия — отдельный поток, звук вшит в видео без имени:
// манифест называет его выданным вариантом, иначе в файле «eng» без подписи.
func TestDownloadAudiosNamesVariantVoice(t *testing.T) {
	variants := []dp.Variant{{ID: "t96", Title: "AniLibria"}, {ID: "t215", Title: "Дублированный"},
		{ID: "o", Title: "Оригинал"}, {ID: "s", Title: "Crunchyroll.Subtitles"}}

	got := downloadAudios(dp.Source{Variant: "t215", Variants: variants})
	if len(got) != 1 || got[0].ID != "t215" || got[0].Title != "Дубляж" || got[0].Language != "rus" {
		t.Fatalf("дубляж: %+v", got)
	}
	if got := downloadAudios(dp.Source{Variant: "t96", Variants: variants}); len(got) != 1 || got[0].Title != "AniLibria" || got[0].Language != "rus" {
		t.Fatalf("студия: %+v", got)
	}
	// Звук исходника: язык неизвестен — не называть его русским.
	for _, id := range []string{"o", "s"} {
		if got := downloadAudios(dp.Source{Variant: id, Variants: variants}); len(got) != 1 || got[0].Language != "" {
			t.Fatalf("%s: %+v", id, got)
		}
	}

	// kinopub: дорожки потока называет плейлист — как есть.
	kp := dp.Source{Audios: []dp.AudioTrack{{ID: "1", Language: "rus", Title: "DUB"}, {ID: "2", Language: "eng", Title: "Original"}}}
	if got := downloadAudios(kp); len(got) != 2 || got[0].Title != "DUB" || got[1].Language != "eng" {
		t.Fatalf("kinopub: %+v", got)
	}
	// Вариант без описания — пусто, а не выдумка.
	if got := downloadAudios(dp.Source{Variant: "t1"}); got == nil || len(got) != 0 {
		t.Fatalf("неизвестный вариант: %#v", got)
	}
}
