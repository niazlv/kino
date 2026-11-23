package catalog

import (
	"testing"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

func offer(p string, prio int) Offer {
	return Offer{Ref: kernel.ProviderRef{ProviderID: p, ItemID: "1"}, SeasonNo: 1, EpisodeNo: 1, Priority: prio}
}

func ids(os []Offer) []string {
	out := make([]string, 0, len(os))
	for _, o := range os {
		out = append(out, o.Ref.ProviderID)
	}
	return out
}

func TestRank_PriorityWhenNothingChosen(t *testing.T) {
	got := ids(Rank([]Offer{offer("animego_fr", 40), offer("kinopub", 100), offer("animego_me", 50)}, Choice{}))
	want := []string{"kinopub", "animego_me", "animego_fr"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("порядок %v, ожидался %v", got, want)
		}
	}
}

// Запомненная площадка первой, но остальные остаются запасными: серии,
// которой нет у неё, всё равно надо сыграть.
func TestRank_RememberedSourceFirstOthersKept(t *testing.T) {
	got := ids(Rank([]Offer{offer("kinopub", 100), offer("animego_me", 50)}, Choice{Source: "animego_me"}))
	if len(got) != 2 || got[0] != "animego_me" {
		t.Fatalf("порядок %v", got)
	}
}

// Выбор руками только что: подменять площадку молча нельзя.
func TestRank_StrictChoiceHasNoFallback(t *testing.T) {
	got := ids(Rank([]Offer{offer("kinopub", 100), offer("animego_me", 50)}, Choice{Source: "animego_me", Strict: true}))
	if len(got) != 1 || got[0] != "animego_me" {
		t.Fatalf("строгий выбор дал %v", got)
	}
}

func TestBestSource_SkipsDisabled(t *testing.T) {
	tt := Title{Sources: []Source{
		{Ref: kernel.ProviderRef{ProviderID: "kinopub", ItemID: "1"}, Priority: 100, Disabled: true},
		{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "2"}, Priority: 50},
	}}
	s, ok := tt.BestSource()
	if !ok || s.Ref.ProviderID != "animego_me" {
		t.Fatalf("выключенный источник выбран: %+v", s)
	}
}

// Вид произведения решается первым подходящим правилом: аниме и
// мультфильм сильнее страны (японская анимация — аниме, корейская —
// мультфильм), дорама — корейский сериал или фильм, каких жанров у
// площадок нет.
func TestSpecies(t *testing.T) {
	cases := []struct {
		name  string
		title Title
		want  Species
	}{
		{"аниме по жанру", Title{Kind: KindSeries, Genres: []string{"Аниме", "Комедия"}}, SpeciesAnime},
		{"японская анимация — аниме, а не дорама", Title{Kind: KindSeries, Genres: []string{"Аниме"}, Countries: []string{"Япония"}}, SpeciesAnime},
		{"корейская анимация — мультфильм, а не дорама", Title{Kind: KindMovie, Genres: []string{"Мультфильм"}, Countries: []string{"Корея Южная"}}, SpeciesCartoon},
		{"мультфильм по жанру", Title{Kind: KindMovie, Genres: []string{"Мультфильм", "Фэнтези"}}, SpeciesCartoon},
		{"дорама по стране — сериал", Title{Kind: KindSeries, Genres: []string{"Мелодрама", "Комедия"}, Countries: []string{"Корея Южная"}}, SpeciesDorama},
		{"дорама по стране — фильм тоже", Title{Kind: KindMovie, Genres: []string{"Мелодрама"}, Countries: []string{"Корея Южная"}}, SpeciesDorama},
		{"японский сериал — не дорама", Title{Kind: KindSeries, Countries: []string{"Япония"}}, SpeciesSeries},
		{"китайский сериал — не дорама", Title{Kind: KindSeries, Countries: []string{"Китай", "Тайвань"}}, SpeciesSeries},
		{"стран не знаем — просто сериал", Title{Kind: KindSeries, Genres: []string{"Драма"}}, SpeciesSeries},
		{"стран не знаем — просто фильм", Title{Kind: KindMovie}, SpeciesMovie},
		{"неазиатский сериал", Title{Kind: KindSeries, Countries: []string{"США", "Австралия"}}, SpeciesSeries},
	}
	for _, c := range cases {
		if got := c.title.Species(); got != c.want {
			t.Errorf("%s: вид %q, ожидался %q", c.name, got, c.want)
		}
	}
}

// Формы стран у площадок расходятся: кинопоисковская «Корея Южная», голое
// «Корея», английские. Пробелы по краям — мусор разбора; «Корея Северная»
// и прочая Азия — не дорама.
func TestIsDoramaCountry(t *testing.T) {
	yes := [][]string{
		{"Корея Южная"}, {"корея южная"}, {"Корея"}, {"США", "Корея Южная"}, {" South Korea "},
	}
	for _, cs := range yes {
		if !IsDoramaCountry(cs) {
			t.Errorf("%q — дорама, а не распознана", cs)
		}
	}
	no := [][]string{
		{"США"}, {"Корея Северная"}, {"Индия"}, {"Япония"}, {"Таиланд"}, {}, {" "},
	}
	for _, cs := range no {
		if IsDoramaCountry(cs) {
			t.Errorf("%q — не дорама, а распознана", cs)
		}
	}
}
