package provider

import "testing"

func TestVoiceTitle(t *testing.T) {
	for in, want := range map[string]string{
		"01. Одноголосый. Воротилин (RUS)": "Воротилин", // плейлист kinopub
		"Воротилин":          "Воротилин", // API kinopub
		"02. Оригинал (JPN)": "Оригинал",
		"Orig":               "Оригинал",
		"Track 1 (RUS)":      "Русская дорожка", // безымянная у kinopub
		"03. Многоголосый закадровый. AniLibria (RUS)": "AniLibria",
		"А. Гаврилов":     "А. Гаврилов", // инициал — не вид дорожки
		"SHIZA Project":   "SHIZA Project",
		"Субтитры":        "Субтитры",
		"Русская дорожка": "Русская дорожка", // служебные слова без вида — не вид
		// Вид без студии — одной подписью на все площадки.
		"DUB":           "Дубляж", // kinopub
		"Дублированный": "Дубляж", // ЛордФильм, YummyAnime
		"01. Дублированный. DUB (RUS)":   "Дубляж",
		"Полное дублирование (лицензия)": "Дубляж",
		"Профессиональный многоголосый":  "Многоголосый",
		"Многоголосый, профессиональный": "Многоголосый",
		"Многоголосый закадровый":        "Многоголосый",
		"MVO": "Многоголосый",
		"Двухголосый закадровый":               "Двухголосый",
		"Авторский одноголосый":                "Авторский",
		"Одноголосый закадровый":               "Одноголосый",
		"Любительский многоголосый закадровый": "Любительский многоголосый",
		"Любительский":                         "Любительский",
		"Оригинальный":                         "Оригинал",
		"Оригинальная дорожка":                 "Оригинал",
		// Чужое слово — студия, а не вид.
		"AniDUB":         "AniDUB",
		"2D-DUB":         "2D-DUB",
		"Дубляж Netflix": "Дубляж Netflix",
		"AniLiberty":     "AniLiberty",
	} {
		if got := VoiceTitle(in); got != want {
			t.Errorf("VoiceTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// Одна озвучка у разных площадок и плееров — один ключ; разные — разные.
func TestVoiceKey(t *testing.T) {
	same := [][]string{
		{"AniLibria", "AnilibriaTV", "AniLibria.TV", "03. Многоголосый. AniLibria (RUS)"},
		{"Оригинал", "Orig", "02. Оригинал (JPN)", "Оригинальный"},
		{"Track 1 (RUS)", "Русская дорожка"},
		{"Studio Band", "Studio band", "STUDIO BAND"},
		// «DUB» kinopub и «Дублированный» ЛордФильма — не две студии, а
		// один вид: иначе обход тормозящего kinopub не найдёт дубляж.
		{"DUB", "Дубляж", "Дублированный", "Полное дублирование", "01. Дублированный (RUS)", "Dubbing"},
		{"MVO", "Многоголосый закадровый", "Профессиональный многоголосый", "02. Многоголосый закадровый. MVO (RUS)"},
		{"DVO", "Двухголосый закадровый", "Двуголосый"},
		{"Любительский многоголосый", "Многоголосый любительский", "Любительский многоголосый закадровый"},
	}
	for _, g := range same {
		for _, x := range g[1:] {
			if VoiceKey(x) != VoiceKey(g[0]) {
				t.Errorf("%q и %q — разные ключи: %q и %q", g[0], x, VoiceKey(g[0]), VoiceKey(x))
			}
		}
	}
	if VoiceKey("AniLibria") == VoiceKey("AniDUB") || VoiceKey("Оригинал") == VoiceKey("Субтитры") {
		t.Fatal("разные озвучки слились")
	}
	// Студии со словом «dub» в имени — не дубляж; любительский — не
	// профессиональный.
	for _, x := range []string{"AniDUB", "FumoDub", "2D-DUB", "NoNameDUB Project", "Любительский многоголосый", "Дубляж Netflix"} {
		if VoiceKey(x) == VoiceKey("DUB") || VoiceKey(x) == VoiceKey("MVO") {
			t.Errorf("%q слилась с видом озвучки", x)
		}
	}
}

// Ключ вида — как пишет kinopub: дорожку его потока установленные
// приложения ищут по ключу сами и знают только прежнее написание.
func TestVoiceKeyKeepsKinopubSpelling(t *testing.T) {
	for in, want := range map[string]string{
		"DUB": "dub", "Дублированный": "dub", "MVO": "mvo", "DVO": "dvo", "AVO": "avo",
		"Оригинальный": "оригинал", "LostFilm": "lostfilm", "Кубик в Кубе": "кубиквкубе",
	} {
		if got := VoiceKey(in); got != want {
			t.Errorf("VoiceKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// Псевдонимы студий — из таблицы, которую ведёт админка.
func TestVoiceAliases(t *testing.T) {
	t.Cleanup(func() { SetVoiceAliases(nil) })
	if VoiceKey("AniLiberty") == VoiceKey("AniLibria") {
		t.Fatal("без таблицы студии не сливаются")
	}
	SetVoiceAliases(map[string]string{"aniliberty": "anilibria", "мегааниме": "megaanime"})
	for _, g := range [][2]string{{"AniLiberty", "AniLibria"}, {"AniLiberty.TV", "AniLibria"}, {"Мега-Аниме", "Mega-Anime"}} {
		if VoiceKey(g[0]) != VoiceKey(g[1]) {
			t.Errorf("%q и %q не слились", g[0], g[1])
		}
	}
	if BaseVoiceKey("AniLiberty") != "aniliberty" {
		t.Fatalf("ключ написания не должен зависеть от таблицы: %q", BaseVoiceKey("AniLiberty"))
	}
}

// Грубая латиница сводит транслит, но не разные студии.
func TestLooseVoice(t *testing.T) {
	for _, g := range [][2]string{
		{"Мега-Аниме", "Mega-Anime"}, {"Сыендук", "Syenduk"}, {"Анилибрия", "AniLibria"},
		{"Комната Диди", "KOMNATA DIDI"}, {"Анимевост", "AnimeVost"}, {"Джутсу", "Jutsu"},
	} {
		if LooseVoice(g[0]) != LooseVoice(g[1]) {
			t.Errorf("%q и %q: %q и %q", g[0], g[1], LooseVoice(g[0]), LooseVoice(g[1]))
		}
	}
	for _, g := range [][2]string{{"AniLibria", "AniDUB"}, {"Studio Band", "Студийная Банда"}, {"AniStar", "AniStar Многоголосый"}} {
		if LooseVoice(g[0]) == LooseVoice(g[1]) {
			t.Errorf("%q и %q слились: %q", g[0], g[1], LooseVoice(g[0]))
		}
	}
}

func TestSubtitled(t *testing.T) {
	for in, want := range map[string]bool{"Субтитры": true, "Укр. Субтитры": true, "Crunchyroll.Subtitles": true,
		"AniLibria": false, "Оригинал": false} {
		if Subtitled(in) != want {
			t.Errorf("Subtitled(%q) = %v", in, !want)
		}
	}
}
