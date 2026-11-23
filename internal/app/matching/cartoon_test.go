package matching

import (
	"testing"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// kinopub пишет китайскую и корейскую анимацию «Мультфильмом»: с аниме-
// площадки та же вещь приходит «Аниме» и должна дополнить его тайтл.

var cross = Options{CrossCartoon: true}

func cartoonTitle(id kernel.TitleID, title string, year int) Candidate {
	c := kinopubTitle(id, title, year, false)
	c.Cartoon = true
	return c
}

func TestDecide_AnimeJoinsKinopubCartoonByOriginal(t *testing.T) {
	in := animeItem("jutsu_tv", "5689", "Расхититель гробниц", 2026, 1)
	in.Original = "Dogulwang"
	cands := []Candidate{cartoonTitle(4, "Расхититель гробниц / Dogulwang", 2026)}
	if d := DecideWith(cross, in, cands); d.TitleID != 4 || d.By != "name" {
		t.Fatalf("аниме не дополнило мультфильм kinopub: %+v", d)
	}
	// Правило включается отдельно: без него — как раньше, новый тайтл.
	if d := Decide(in, cands); d.TitleID != 0 {
		t.Fatalf("склеилось без включённого правила: %+v", d)
	}
}

// Совпало только русское название — у мультфильма и аниме-однофамильца это
// бывает часто, а оригинал у них разный.
func TestDecide_AnimeAndCartoonNeedSameOriginal(t *testing.T) {
	in := animeItem("jutsu_tv", "1", "Монстры", 2024, 1)
	in.Original = "Monsters: Ippyaku Sanjou Hiryuu Jigoku"
	d := DecideWith(cross, in, []Candidate{cartoonTitle(7, "Монстры / Monsters vs. Aliens", 2024)})
	if d.TitleID != 0 || d.By != "new" {
		t.Fatalf("склеилось по одному русскому названию: %+v", d)
	}
}

// Игровое кино — не мультфильм: «Монстр» 2004 года остаётся отдельным и с
// включённым правилом.
func TestDecide_CrossCartoonStillNotLiveAction(t *testing.T) {
	in := animeItem("animego_fr", "1", "Монстр", 2004, 1)
	in.Original = "Monster"
	d := DecideWith(cross, in, []Candidate{kinopubTitle(5, "Монстр / Monster", 2004, false)})
	if d.TitleID != 0 {
		t.Fatalf("аниме склеилось с игровым: %+v", d)
	}
}

// И наоборот: мультфильм kinopub, пришедший позже, находит аниме-тайтл.
func TestDecide_KinopubCartoonJoinsAnime(t *testing.T) {
	in := dp.Title{Ref: kernel.ProviderRef{ProviderID: "kinopub", ItemID: "125785"}, Kind: "series",
		Title: "Стать королём за сто дней / Bai Ri Cheng Wang", Original: "Bai Ri Cheng Wang", Year: 2026,
		Genres: []string{"Боевик", "Мультфильм"}}
	anime := Candidate{ID: 59497, Kind: "series", Year: 2026, Anime: true,
		Keys: Keys("Стать королём за сто дней / Bai Ri Cheng Wang"),
		Refs: []Ref{{ProviderID: "jutsu_tv", ItemID: "5658", SeasonNo: 1, MAL: "62957"}}}
	if d := DecideWith(cross, in, []Candidate{anime}); d.TitleID != 59497 {
		t.Fatalf("мультфильм kinopub не нашёл аниме-тайтл: %+v", d)
	}
}

func TestShouldMerge_AnimeAndCartoon(t *testing.T) {
	cartoon := Candidate{ID: 213, Kind: "series", Year: 2018, Cartoon: true,
		Keys: Keys("Боевой континент / Douluo Dalu"),
		Refs: []Ref{{ProviderID: "kinopub", ItemID: "74866"}}}
	anime := Candidate{ID: 59501, Kind: "series", Year: 2018, Anime: true,
		Keys: Keys("Боевой континент / Douluo Dalu 2nd Season"),
		Refs: []Ref{{ProviderID: "jutsu_net", ItemID: "437", SeasonNo: 2}}}
	if !ShouldMergeWith(cross, Pair{A: cartoon, B: anime}) {
		t.Fatal("мультфильм kinopub и аниме с тем же оригиналом не слиты")
	}
	if ShouldMerge(Pair{A: cartoon, B: anime}) {
		t.Fatal("слиты без включённого правила")
	}
	// Только русское многословное название — мало.
	ru := anime
	ru.Keys = Keys("Боевой континент")
	if ShouldMergeWith(cross, Pair{A: cartoon, B: ru}) {
		t.Fatal("слиты по одному русскому названию")
	}
	// Игровое кино с аниме не сливается и при совпавшем оригинале.
	live := cartoon
	live.Cartoon = false
	if ShouldMergeWith(cross, Pair{A: live, B: anime}) {
		t.Fatal("аниме слито с игровым кино")
	}
}

func TestLatinIntersect(t *testing.T) {
	if latinIntersect(Keys("Призрак в доспехах: Синдром одиночки 2045"), Keys("Синдром одиночки 2045 / SAC 2045")) {
		// общий ключ здесь только русский и цифры
		t.Fatal("русский ключ принят за латинский")
	}
	if !latinIntersect(Keys("Расхититель гробниц / Dogulwang"), Keys("Dogulwang")) {
		t.Fatal("оригинал не узнан")
	}
}
