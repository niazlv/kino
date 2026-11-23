package matching

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

func TestKeys_SameWorkDifferentSpelling(t *testing.T) {
	a := Keys("Принцесса-шпионка / Princess Principal")
	b := Keys("Принцесса шпионка 2 сезон", "Princess Principal: Crown Handler")
	if !intersects(a, b) {
		t.Fatalf("ключи не пересеклись: %v и %v", a, b)
	}
	if got := Keys("Ёлки"); !reflect.DeepEqual(got, []string{"елки"}) {
		t.Fatalf("ё не сведена: %v", got)
	}
	if got := Keys("K", "92"); len(got) != 0 {
		t.Fatalf("короткие ключи должны отбрасываться: %v", got)
	}
}

// Реальный случай с продакшена: одно произведение, а площадки пишут его
// лицензионным английским и ромадзи с долгими гласными.
func TestKeys_RomajiFoldVowels(t *testing.T) {
	a := Keys("Герой Юки Юна / Yuki Yuna is a Hero")
	b := Keys("Герой Юна Юки / Yuuki Yuuna wa Yuusha de Aru")
	if !intersects(a, b) {
		t.Fatalf("ромадзи не свёрнулись к одной форме: %v и %v", a, b)
	}
}

// Порядок слов в русском названии не фиксирован: «Герой Юки Юна» и
// «Герой Юна Юки» — одно и то же произведение.
func TestKeys_WordOrder(t *testing.T) {
	a := Keys("Герой Юна Юки: История героя")
	b := Keys("Герой Юки Юна: История героя")
	if !intersects(a, b) {
		t.Fatalf("порядок слов не сведён: %v и %v", a, b)
	}
	// Одно русское слово вариантов не даёт: свёртка — только для латиницы,
	// а сортировать нечего.
	if got := Variants("дюна"); len(got) != 0 {
		t.Fatalf("лишние варианты у одного слова: %v", got)
	}
	// Русский ключ не сворачивает гласные, только сортируется.
	if got := Variants("герой юки юна"); !reflect.DeepEqual(got, []string{"s:герой юки юна"}) {
		t.Fatalf("вариант русского ключа: %v", got)
	}
}

func TestSplitSeason(t *testing.T) {
	cases := map[string]struct {
		base   string
		season int
	}{
		"Врата Штейна 2 сезон":          {"Врата Штейна", 2},
		"Магическая битва 2-й сезон":    {"Магическая битва", 2},
		"Кагуя: Сезон 3":                {"Кагуя", 3},
		"Mob Psycho 100 Season 3":       {"Mob Psycho 100", 3},
		"Shingeki no Kyojin 2nd Season": {"Shingeki no Kyojin", 2},
		"Ван-Пис 3":                     {"Ван-Пис 3", 0},
		"Принцесса-медуза 1 сезон":      {"Принцесса-медуза", 1},
		"Сезон охоты":                   {"Сезон охоты", 0},
	}
	for in, want := range cases {
		base, season := SplitSeason(in)
		if base != want.base || season != want.season {
			t.Errorf("%q -> (%q, %d), ожидалось (%q, %d)", in, base, season, want.base, want.season)
		}
	}
}

func kinopubTitle(id kernel.TitleID, title string, year int, anime bool) Candidate {
	return Candidate{
		ID: id, Kind: "series", Year: year, Anime: anime, Keys: Keys(title),
		Refs: []Ref{{ProviderID: "kinopub", ItemID: "k" + title}},
	}
}

func animeItem(provider, item, title string, year, season int) dp.Title {
	return dp.Title{
		Ref:  kernel.ProviderRef{ProviderID: provider, ItemID: item},
		Kind: "series", Title: title, Year: year, SeasonNo: season,
		Genres: []string{"Аниме", "Комедия"},
	}
}

// Главный сценарий: аниме с аниме-площадки дополняет уже заведённый
// kinopub-тайтл, а не заводит второй.
func TestDecide_AnimeJoinsKinopubByName(t *testing.T) {
	in := animeItem("animego_me", "421", "Принцесса-шпионка", 2017, 1)
	in.Original = "Princess Principal"
	d := Decide(in, []Candidate{kinopubTitle(38219, "Принцесса-шпионка / Princess Principal", 2017, true)})
	if d.TitleID != 38219 || d.SeasonNo != 1 || d.By != "name" {
		t.Fatalf("решение %+v", d)
	}
}

// Второй сезон — другой год, но тот же тайтл.
func TestDecide_LaterSeasonJoinsByName(t *testing.T) {
	in := animeItem("animego_me", "900", "Магическая битва 2 сезон", 2023, 2)
	d := Decide(in, []Candidate{kinopubTitle(2886, "Магическая битва / Jujutsu Kaisen", 2020, true)})
	if d.TitleID != 2886 || d.SeasonNo != 2 {
		t.Fatalf("решение %+v", d)
	}
}

func TestDecide_AnimeNeverJoinsLiveAction(t *testing.T) {
	in := animeItem("animego_fr", "1", "Монстр", 2004, 1)
	d := Decide(in, []Candidate{kinopubTitle(5, "Монстр / Monster", 2004, false)})
	if d.TitleID != 0 || d.By != "new" {
		t.Fatalf("аниме склеилось с игровым: %+v", d)
	}
}

func TestDecide_AmbiguousNameMakesNewTitle(t *testing.T) {
	in := animeItem("animego_fr", "1", "Хеллсинг", 2006, 1)
	in.Year = 2005
	d := Decide(in, []Candidate{
		kinopubTitle(1, "Хеллсинг / Hellsing", 2004, true),
		kinopubTitle(2, "Хеллсинг / Hellsing Ultimate", 2006, true),
	})
	if d.TitleID != 0 {
		t.Fatalf("однофамильцы склеены наугад: %+v", d)
	}
}

// MAL сезонный: совпадение переносит и сезон уже сделанной привязки.
func TestDecide_MALCarriesSeason(t *testing.T) {
	c := kinopubTitle(7, "Атака титанов / Shingeki no Kyojin", 2013, true)
	c.Refs = append(c.Refs, Ref{ProviderID: "animego_fr", ItemID: "55", SeasonNo: 3, MAL: "35760"})
	in := animeItem("animego_me", "900", "Атака титанов: Сезон 3", 2018, 3)
	in.MAL = "35760"
	d := Decide(in, []Candidate{c})
	if d.TitleID != 7 || d.SeasonNo != 3 || d.By != "mal" {
		t.Fatalf("решение %+v", d)
	}
}

// Два разных элемента одной площадки на один и тот же сезон — это два
// произведения с одним названием, а не одно.
func TestDecide_SameProviderSameSeasonIsAnotherWork(t *testing.T) {
	c := kinopubTitle(9, "Тетрадь смерти", 2006, true)
	c.Refs = append(c.Refs, Ref{ProviderID: "animego_me", ItemID: "old", SeasonNo: 1})
	in := animeItem("animego_me", "new", "Тетрадь смерти", 2006, 1)
	if d := Decide(in, []Candidate{c}); d.TitleID != 0 {
		t.Fatalf("решение %+v", d)
	}
}

func TestDecide_NoYearNoNameMatch(t *testing.T) {
	in := animeItem("animego_fr", "1", "Принцесса-шпионка", 0, 1)
	if d := Decide(in, []Candidate{kinopubTitle(1, "Принцесса-шпионка", 2017, true)}); d.TitleID != 0 {
		t.Fatalf("склейка без года: %+v", d)
	}
}

// --- реконсилиация: ShouldMerge ---

// Дубль с продакшена: animego.me и animego.fr завели каждый свой тайтл
// первого сезона Yuki Yuna, ключи совпали только вариантами.
func TestShouldMerge_WordOrderAndRomaji(t *testing.T) {
	a := Candidate{ID: 1, Kind: "series", Year: 2014, Anime: true,
		Keys: Keys("Герой Юки Юна / Yuki Yuna is a Hero"),
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "me-1", SeasonNo: 1}}}
	b := Candidate{ID: 2, Kind: "series", Year: 2014, Anime: true,
		Keys: Keys("Герой Юна Юки / Yuuki Yuuna wa Yuusha de Aru"),
		Refs: []Ref{{ProviderID: "animego_fr", ItemID: "fr-1", SeasonNo: 1}}}
	if !ShouldMerge(Pair{A: a, B: b}) {
		t.Fatal("разночтения площадок не признаны одним произведением")
	}
}

// MAL одинаков — остальное не смотрим: год и написание разошлись.
func TestShouldMerge_MALWins(t *testing.T) {
	a := Candidate{ID: 1, Kind: "series", Year: 2017, Anime: true,
		Keys: Keys("Герой Юки Юна: Глава Суми Васио"),
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "me-2", SeasonNo: 1}}}
	b := Candidate{ID: 2, Kind: "series", Year: 2016, Anime: true,
		Keys: Keys("Герой Юна Юки: История Суми Вашио"),
		Refs: []Ref{{ProviderID: "animego_fr", ItemID: "fr-2", SeasonNo: 1, MAL: "34284"}}}
	a.Refs[0].MAL = "34284"
	if !ShouldMerge(Pair{A: a, B: b}) {
		t.Fatal("общий номер MAL не склеил")
	}
}

// Второй сезон франшизы — тот же тайтл с другим годом, окно лет шире.
func TestShouldMerge_SeasonYearsWindow(t *testing.T) {
	a := Candidate{ID: 1, Kind: "series", Year: 2013, Anime: true, Keys: Keys("Атака титанов"),
		Refs: []Ref{{ProviderID: "kinopub", ItemID: "k1", SeasonNo: 1}}}
	b := Candidate{ID: 2, Kind: "series", Year: 2018, Anime: true, Keys: Keys("Атака титанов 3 сезон"),
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "me-3", SeasonNo: 3}}}
	if !ShouldMerge(Pair{A: a, B: b}) {
		t.Fatal("сезон франшизы не признан тем же произведением")
	}
}

// Ремейки и однофамильцы сливать нельзя.
func TestShouldMerge_RefusesSameNameDifferentWorks(t *testing.T) {
	base := func(id kernel.TitleID, year int) Candidate {
		return Candidate{ID: id, Kind: "movie", Year: year, Anime: false, Keys: Keys("Герой"),
			Refs: []Ref{{ProviderID: "kinopub", ItemID: "k" + strconv.Itoa(int(id))}}}
	}
	if ShouldMerge(Pair{A: base(1, 2016), B: base(2, 2023)}) {
		t.Fatal("однофамильцы с разными годами слиты")
	}
	// Аниме к игровому — никогда.
	if ShouldMerge(Pair{A: base(1, 2004), B: Candidate{ID: 3, Kind: "movie", Year: 2004, Anime: true,
		Keys: Keys("Монстр"), Refs: []Ref{{ProviderID: "kinopub", ItemID: "k3"}}}}) {
		t.Fatal("аниме слито с игровым")
	}
	// Оба закреплены человеком — не трогаем.
	p := Pair{A: base(1, 2016), B: base(2, 2016)}
	p.A.Refs[0].Pinned = true
	if ShouldMerge(p) {
		t.Fatal("закреплённый тайтл слит")
	}
	// Без года по названию — лотерея.
	if ShouldMerge(Pair{A: base(1, 0), B: base(2, 2016)}) {
		t.Fatal("склейка без года")
	}
}

// Одна площадка дважды на одном сезоне — два произведения-однофамильца.
func TestShouldMerge_SameProviderSameSeason(t *testing.T) {
	a := Candidate{ID: 1, Kind: "series", Year: 2006, Anime: true, Keys: Keys("Хеллсинг"),
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "old", SeasonNo: 1}}}
	b := Candidate{ID: 2, Kind: "series", Year: 2006, Anime: true, Keys: Keys("Хеллсинг"),
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "new", SeasonNo: 1}}}
	if ShouldMerge(Pair{A: a, B: b}) {
		t.Fatal("элементы одной площадки на одном сезоне слиты")
	}
}

// Регрессия с продакшена: Persona 4 и Persona 5 делят однословный ключ
// «персона», а у Persona 5 есть привязка позднего сезона, открывающая
// широкое окно лет. Одно слово — не повод сливать.
func TestShouldMerge_SingleWordKeyTooWeak(t *testing.T) {
	p4 := Candidate{ID: 1, Kind: "series", Year: 2011, Anime: true, Keys: []string{"персона", "persona 4 the animation"},
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "p4", SeasonNo: 1, MAL: "10588"}}}
	p5 := Candidate{ID: 2, Kind: "series", Year: 2018, Anime: true, Keys: []string{"персона", "persona 5 the animation"},
		Refs: []Ref{{ProviderID: "animego_fr", ItemID: "p5", SeasonNo: 5, MAL: "36023"}}}
	if ShouldMerge(Pair{A: p4, B: p5}) {
		t.Fatal("однословный ключ «персона» слил разные произведения")
	}
}

// --- сезоны: голое число, однофамильцы-сезоны, Realign, Adopt ---

func TestBareNumber(t *testing.T) {
	cases := map[string]struct {
		base   string
		season int
	}{
		"Гинтама 3":             {"Гинтама", 3},
		"Психопаспорт-2":        {"Психопаспорт", 2},
		"Кайдзю № 8":            {"Кайдзю № 8", 0},
		"Кэнган Асура. Часть 2": {"Кэнган Асура. Часть 2", 0},
		"Kengan Ashura Part 2":  {"Kengan Ashura Part 2", 0},
		"Mob Psycho 100":        {"Mob Psycho 100", 0},
		"Врата Штейна 0":        {"Врата Штейна 0", 0},
		"Ранма 1":               {"Ранма 1", 0},
		"Гинтама":               {"Гинтама", 0},
	}
	for in, want := range cases {
		base, season := BareNumber(in)
		if base != want.base || season != want.season {
			t.Errorf("%q -> (%q, %d), ожидалось (%q, %d)", in, base, season, want.base, want.season)
		}
	}
}

// gintama — «Гинтама» kinopub, как в настоящем каталоге: весь сериал одной
// привязкой и сезоны аниме-площадок, которые уже приклеились.
func gintama() Candidate {
	c := kinopubTitle(28066, "Гинтама / Gintama", 2006, true)
	c.Refs[0].ItemID = "15204"
	c.Refs = append(c.Refs,
		Ref{ProviderID: "animego_me", ItemID: "gintama-249", SeasonNo: 1},
		Ref{ProviderID: "animego_me", ItemID: "gintama-2-250", SeasonNo: 2},
		Ref{ProviderID: "animego_fr", ItemID: "5814", SeasonNo: 4, MAL: "28977"},
		Ref{ProviderID: "animego_me", ItemID: "gintama-5-253", SeasonNo: 5, MAL: "34096"},
	)
	return c
}

// Регрессия с продакшена: animego.me пишет «Гинтама 6» без слова «сезон»,
// и сезон заводился отдельным тайтлом.
func TestDecide_BareNumberContinuesSeries(t *testing.T) {
	in := animeItem("animego_me", "gintama-6-254", "Гинтама 6", 2017, 1)
	in.Original = "Gintama. Slip Arc"
	d := Decide(in, []Candidate{gintama()})
	if d.TitleID != 28066 || d.SeasonNo != 6 || d.By != "name" {
		t.Fatalf("решение %+v", d)
	}
}

// Голое число без предыдущего сезона — не сезон: «Макросс 7» — другой
// сериал франшизы, «Кайдзю № 8» — просто название.
func TestDecide_BareNumberNeedsPreviousSeason(t *testing.T) {
	macross := kinopubTitle(1, "Макросс / Macross", 1982, true)
	macross.Refs = append(macross.Refs, Ref{ProviderID: "animego_me", ItemID: "m1", SeasonNo: 1})
	in := animeItem("animego_me", "m7", "Макросс 7", 1994, 1)
	if d := Decide(in, []Candidate{macross}); d.TitleID != 0 {
		t.Fatalf("«Макросс 7» стал седьмым сезоном: %+v", d)
	}
	kaiju := kinopubTitle(2, "Кайдзю", 2020, true)
	in = animeItem("animego_me", "k8", "Кайдзю № 8", 2024, 1)
	if d := Decide(in, []Candidate{kaiju}); d.TitleID != 0 {
		t.Fatalf("«Кайдзю № 8» стал сезоном: %+v", d)
	}
	// Второй сезон к сериалу, заведённому целиком, — можно.
	pp := kinopubTitle(3, "Психо-паспорт / Psycho-Pass", 2012, true)
	pp.Keys = Keys("Психопаспорт / Psycho-Pass")
	in = animeItem("animego_me", "pp2", "Психопаспорт 2", 2014, 1)
	if d := Decide(in, []Candidate{pp}); d.TitleID != 3 || d.SeasonNo != 2 {
		t.Fatalf("второй сезон не приклеился: %+v", d)
	}
}

// Уже заведённые отдельно сезоны не должны делать неоднозначным каждый
// следующий: хозяин сезона позже первого — сериал целиком или самый ранний.
func TestDecide_LaterSeasonPrefersWholeSeries(t *testing.T) {
	orphan := Candidate{ID: 53418, Kind: "series", Year: 2012, Anime: true, Keys: Keys("Гинтама 3", "Гинтама"),
		Refs: []Ref{{ProviderID: "animego_fr", ItemID: "5821", SeasonNo: 3, MAL: "15417"}}}
	later := Candidate{ID: 53423, Kind: "series", Year: 2017, Anime: true, Keys: Keys("Гинтама"),
		Refs: []Ref{{ProviderID: "animego_fr", ItemID: "5809", SeasonNo: 1}}}
	in := animeItem("animego_fr", "5823", "Гинтама", 2011, 2)
	in.Original = "Gintama'"
	d := Decide(in, []Candidate{orphan, gintama(), later})
	if d.TitleID != 28066 || d.SeasonNo != 2 {
		t.Fatalf("решение %+v", d)
	}
	// Первый сезон по-прежнему не выбирает наугад.
	in = animeItem("animego_fr", "x", "Гинтама", 2009, 1)
	if d := Decide(in, []Candidate{orphan, gintama(), later}); d.TitleID != 0 {
		t.Fatalf("первый сезон склеен наугад: %+v", d)
	}
}

// Одна работа на двух тайтлах в разных сезонах — простым слиянием нельзя:
// серии и прогресс легли бы в чужой сезон.
func TestShouldMerge_RefusesSameMALDifferentSeasons(t *testing.T) {
	a := Candidate{ID: 1, Kind: "series", Year: 2006, Anime: true, Keys: Keys("Гинтама"),
		Refs: []Ref{{ProviderID: "animego_fr", ItemID: "5809", SeasonNo: 6, MAL: "35843"}}}
	b := Candidate{ID: 2, Kind: "series", Year: 2017, Anime: true, Keys: Keys("Гинтама 6"),
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "gintama-6-254", SeasonNo: 1, MAL: "35843"}}}
	if ShouldMerge(Pair{A: a, B: b}) {
		t.Fatal("работа слита в чужой сезон")
	}
}

// Регрессия с продакшена: «Пять невест» 2 сезон с animego.me стоял первым
// сезоном, хотя animego.fr с тем же MAL — вторым.
func TestRealign_ExplicitSeasonWins(t *testing.T) {
	c := kinopubTitle(25251, "Пять невест / Gotoubun no Hanayome", 2019, true)
	c.Refs = append(c.Refs,
		Ref{ProviderID: "animego_fr", ItemID: "fr2", SeasonNo: 2, MAL: "39783"},
		Ref{ProviderID: "animego_me", ItemID: "me2", SeasonNo: 1, MAL: "39783"},
	)
	got := Realign(c)
	if len(got) != 1 || got[0].Ref.ItemID != "me2" || got[0].To != 2 {
		t.Fatalf("перенос %+v", got)
	}
}

// Две работы одной площадки на «первом» сезоне расходятся по своим.
func TestRealign_TwoWorksSplit(t *testing.T) {
	c := Candidate{ID: 54591, Kind: "series", Year: 2016, Anime: true, Refs: []Ref{
		{ProviderID: "animego_fr", ItemID: "fr2", SeasonNo: 2, MAL: "34978"},
		{ProviderID: "animego_fr", ItemID: "fr3", SeasonNo: 3, MAL: "37126"},
		{ProviderID: "animego_me", ItemID: "me2", SeasonNo: 1, MAL: "34978"},
		{ProviderID: "animego_me", ItemID: "me3", SeasonNo: 1, MAL: "37126"},
	}}
	got := map[string]int{}
	for _, m := range Realign(c) {
		got[m.Ref.ItemID] = m.To
	}
	if !reflect.DeepEqual(got, map[string]int{"me2": 2, "me3": 3}) {
		t.Fatalf("переносы %v", got)
	}
}

// Человек сказал — так и есть; и занятый чужой работой сезон не отдаётся.
func TestRealign_PinnedAndOccupied(t *testing.T) {
	c := Candidate{ID: 1, Kind: "series", Year: 2016, Anime: true, Refs: []Ref{
		{ProviderID: "animego_fr", ItemID: "fr", SeasonNo: 2, MAL: "1"},
		{ProviderID: "animego_me", ItemID: "me", SeasonNo: 1, MAL: "1", Pinned: true},
	}}
	got := Realign(c)
	if len(got) != 1 || got[0].Ref.ItemID != "fr" || got[0].To != 1 {
		t.Fatalf("закреплённый сезон не победил: %+v", got)
	}
	c = Candidate{ID: 1, Kind: "series", Year: 2016, Anime: true, Refs: []Ref{
		{ProviderID: "animego_fr", ItemID: "fr", SeasonNo: 2, MAL: "1"},
		{ProviderID: "animego_me", ItemID: "me", SeasonNo: 1, MAL: "1"},
		{ProviderID: "animego_me", ItemID: "other", SeasonNo: 2, MAL: "2"},
	}}
	if got := Realign(c); len(got) != 0 {
		t.Fatalf("занятый сезон отдан: %+v", got)
	}
}

// Сироты «Гинтамы» переносятся к сериалу своими сезонами.
func TestAdopt_GintamaOrphans(t *testing.T) {
	cases := []struct {
		name  string
		o     Candidate
		names []string
		want  Adoption
		// owned — сезоны, уже перенесённые к сериалу раньше в этом проходе.
		owned []Ref
	}{
		{"голое число", Candidate{ID: 53423, Kind: "series", Year: 2017, Anime: true, Refs: []Ref{
			{ProviderID: "animego_me", ItemID: "gintama-6-254", SeasonNo: 1},
			{ProviderID: "animego_fr", ItemID: "5809", SeasonNo: 1, MAL: "35843"},
		}}, []string{"Гинтама 6", "Gintama. Slip Arc"}, Adoption{Into: 28066, SeasonNo: 6, By: "name"}, nil},
		{"явный сезон и «первый» по умолчанию", Candidate{ID: 53425, Kind: "series", Year: 2018, Anime: true, Refs: []Ref{
			{ProviderID: "animego_me", ItemID: "gintama-7-255", SeasonNo: 1},
			{ProviderID: "animego_fr", ItemID: "5804", SeasonNo: 7, MAL: "36838"},
		}}, []string{"Гинтама 7", "Gintama. Silver Soul Arc"}, Adoption{Into: 28066, SeasonNo: 7, By: "name"},
			[]Ref{{ProviderID: "animego_me", ItemID: "gintama-6-254", SeasonNo: 6}}},
		{"явный сезон без числа в названии", Candidate{ID: 53868, Kind: "series", Year: 2011, Anime: true, Refs: []Ref{
			{ProviderID: "animego_fr", ItemID: "5823", SeasonNo: 2, MAL: "9969"},
		}}, []string{"Гинтама", "Gintama'"}, Adoption{Into: 28066, SeasonNo: 2, By: "name"}, nil},
	}
	for _, tc := range cases {
		other := Candidate{ID: 53418, Kind: "series", Year: 2012, Anime: true, Keys: Keys("Гинтама 3", "Гинтама"),
			Refs: []Ref{{ProviderID: "animego_fr", ItemID: "5821", SeasonNo: 3, MAL: "15417"}}}
		owner := gintama()
		owner.Refs = append(owner.Refs, tc.owned...)
		got, ok := Adopt(tc.o, tc.names, []Candidate{other, owner, tc.o})
		if !ok || got != tc.want {
			t.Errorf("%s: %+v %v, ожидалось %+v", tc.name, got, ok, tc.want)
		}
	}
}

// Номер MAL сильнее названия: работа уже есть у другого тайтла — туда и
// тем же сезоном.
func TestAdopt_ByMAL(t *testing.T) {
	m := gintama()
	m.Refs = append(m.Refs, Ref{ProviderID: "animego_fr", ItemID: "5809", SeasonNo: 6, MAL: "35843"})
	o := Candidate{ID: 53423, Kind: "series", Year: 2017, Anime: true, Keys: Keys("Гинтама 6"),
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "gintama-6-254", SeasonNo: 1, MAL: "35843"}}}
	got, ok := Adopt(o, []string{"Гинтама 6"}, []Candidate{m})
	if !ok || got != (Adoption{Into: 28066, SeasonNo: 6, By: "mal"}) {
		t.Fatalf("%+v %v", got, ok)
	}
}

// Чего Adopt не трогает: тайтл из нескольких работ, «первый» без номера,
// закреплённое человеком, сезон хозяина, занятый другой работой.
func TestAdopt_Refuses(t *testing.T) {
	m := gintama()
	two := Candidate{ID: 9, Kind: "series", Year: 2018, Anime: true, Refs: []Ref{
		{ProviderID: "animego_me", ItemID: "a", SeasonNo: 1, MAL: "1"},
		{ProviderID: "animego_fr", ItemID: "b", SeasonNo: 3, MAL: "2"},
	}}
	if _, ok := Adopt(two, []string{"Гинтама 7"}, []Candidate{m}); ok {
		t.Error("тайтл из двух работ перенесён")
	}
	plain := Candidate{ID: 9, Kind: "series", Year: 2018, Anime: true,
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "a", SeasonNo: 1}}}
	if _, ok := Adopt(plain, []string{"Гинтама: Спешл"}, []Candidate{m}); ok {
		t.Error("тайтл без номера сезона перенесён")
	}
	pinned := Candidate{ID: 9, Kind: "series", Year: 2018, Anime: true,
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "a", SeasonNo: 1, Pinned: true}}}
	if _, ok := Adopt(pinned, []string{"Гинтама 6"}, []Candidate{m}); ok {
		t.Error("закреплённая привязка перенесена")
	}
	seventh := Candidate{ID: 9, Kind: "series", Year: 2018, Anime: true,
		Refs: []Ref{{ProviderID: "animego_fr", ItemID: "5804", SeasonNo: 7, MAL: "36838"}}}
	if _, ok := Adopt(seventh, []string{"Гинтама"}, []Candidate{m}); ok {
		t.Error("седьмой сезон перенесён раньше шестого")
	}
	taken := Candidate{ID: 9, Kind: "series", Year: 2012, Anime: true,
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "gintama-2-other", SeasonNo: 1}}}
	if _, ok := Adopt(taken, []string{"Гинтама 2"}, []Candidate{m}); ok {
		t.Error("сезон, занятый другим элементом площадки, отдан")
	}
}

// Регрессия из настоящего каталога: «Берсерк 2nd Season» (2017) — продолжение
// «Берсерка» 2016 года. Его второй сезон занят, но это не повод уйти к
// «Берсерку» 1997-го; ремейки kinopub заводит отдельно, хозяин — ближайший.
func TestAdopt_RemakeTakesClosestWholeSeries(t *testing.T) {
	old := kinopubTitle(12209, "Берсерк / Berserk", 1997, true)
	old.Refs = append(old.Refs, Ref{ProviderID: "animego_fr", ItemID: "527", SeasonNo: 1, MAL: "33"})
	remake := kinopubTitle(38315, "Берсерк / Berserk", 2016, true)
	remake.Refs = append(remake.Refs, Ref{ProviderID: "animego_fr", ItemID: "192", SeasonNo: 2, MAL: "32379"})
	o := Candidate{ID: 57296, Kind: "series", Year: 2017, Anime: true, Refs: []Ref{
		{ProviderID: "animego_fr", ItemID: "610", SeasonNo: 2, MAL: "34055"},
		{ProviderID: "animego_me", ItemID: "berserk-2017-329", SeasonNo: 2, MAL: "34055"},
	}}
	if got, ok := Adopt(o, []string{"Берсерк", "Berserk 2nd Season"}, []Candidate{old, remake}); ok {
		t.Fatalf("ушёл к чужому «Берсерку»: %+v", got)
	}
	// Второй сезон ремейка свободен — туда и переносится.
	remake.Refs = remake.Refs[:1]
	got, ok := Adopt(o, []string{"Берсерк", "Berserk 2nd Season"}, []Candidate{old, remake})
	if !ok || got.Into != 38315 || got.SeasonNo != 2 {
		t.Fatalf("%+v %v", got, ok)
	}
}

// «Часть 2» — половина сезона, не сезон: «Кэнган Асура 2. Часть 2» не
// становится вторым сезоном «Кэнган Асуры. Часть 2».
func TestAdopt_PartIsNotSeason(t *testing.T) {
	part := Candidate{ID: 54770, Kind: "series", Year: 2019, Anime: true, Keys: Keys("Кэнган Асура. Часть 2"),
		Refs: []Ref{{ProviderID: "animego_me", ItemID: "kengan-asura-2-1213", SeasonNo: 1}}}
	o := Candidate{ID: 53243, Kind: "series", Year: 2024, Anime: true, Refs: []Ref{
		{ProviderID: "animego_fr", ItemID: "6589", SeasonNo: 1, MAL: "56704"},
		{ProviderID: "animego_me", ItemID: "kengan-asura-2-chast-2-2653", SeasonNo: 1, MAL: "56704"},
	}}
	if got, ok := Adopt(o, []string{"Кэнган Асура 2. Часть 2", "Kengan Ashura Season 2 Part 2"}, []Candidate{part}); ok {
		t.Fatalf("часть принята за сезон: %+v", got)
	}
}
