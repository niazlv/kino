package provider

import (
	"regexp"
	"strings"
	"sync/atomic"
)

// Voice — озвучка серии у площадки: то, что человек выбирает в плеере, и
// то, как площадка этот выбор понимает.
//
// Человеку площадка безразлична — ему нужна студия. Одна и та же студия
// есть у нескольких площадок, и выбирать её надо одним пунктом, а откуда
// взять поток, решает платформа. Для этого площадка умеет назвать свои
// озвучки серии, не выдавая поток (CapVoices).
type Voice struct {
	// Title — как площадка называет озвучку: студия («AniLibria»),
	// «Оригинал», «Субтитры».
	Title string
	// Variant — номер потока для Resolve у площадок, где каждая озвучка —
	// свой поток. Пусто — дорожка внутри общего потока (kinopub): её
	// выбирает плеер по подписи.
	Variant string
	// Player — чей плеер отдаёт поток (CVH, Kodik…): одна студия бывает у
	// двух плееров одной площадки.
	Player string
}

var (
	// «01. Одноголосый. Воротилин (RUS)» — подпись дорожки в плейлисте
	// kinopub: номер, вид, студия, язык.
	trackNoRe   = regexp.MustCompile(`^\s*\d+\s*\.\s*`)
	trackLangRe = regexp.MustCompile(`\s*\(([A-Za-z]{2,3})\)\s*$`)
	// «Track 1» — дорожка без имени: у kinopub так подписана русская
	// дорожка без студии.
	unnamedRe = regexp.MustCompile(`^(?i:track|дорожка)\s*\d*$`)
)

// languages — как назвать дорожку без студии по её языку.
var languages = map[string]string{
	"rus": "Русская дорожка", "ru": "Русская дорожка",
	"eng": "Английская дорожка", "en": "Английская дорожка",
	"jpn": "Японская дорожка", "ja": "Японская дорожка",
	"ukr": "Украинская дорожка", "uk": "Украинская дорожка",
	"kor": "Корейская дорожка", "chi": "Китайская дорожка", "zho": "Китайская дорожка",
}

// LanguageVoice — подпись дорожки без студии и без вида, по языку. Пусто —
// язык неизвестен.
func LanguageVoice(lang string) string {
	return languages[strings.ToLower(strings.TrimSpace(lang))]
}

// VoiceTitle — подпись озвучки так, как её узнаёт человек, из того, как её
// подписала площадка или плейлист.
//
// Плейлист kinopub пишет «01. Одноголосый. Воротилин (RUS)», API того же
// kinopub — «Воротилин», AnimeGO — «Воротилин». Студия — последняя часть.
// Подпись без студии — вид озвучки, и он один на все площадки: «DUB»,
// «Дублированный», «Полное дублирование (лицензия)» — «Дубляж» (см.
// parseKind). Безымянная «Track 1 (RUS)» — по языку.
func VoiceTitle(raw string) string {
	t, _ := parseVoice(raw)
	return t
}

// VoiceKey сводит подписи одной озвучки к одному ключу: по нему озвучки
// площадок сливаются в один пункт и по нему же запоминаются.
//
// Три слоя, от общего к частному:
//   - вид без студии разбирается по словам (parseKind): «DUB» kinopub и
//     «Дублированный» ЛордФильма — один дубляж;
//   - у студии не важны регистр, пробелы, точки и хвост «TV»: «AniLibria»,
//     «AnilibriaTV», «AniLibria.TV» — одна;
//   - студии под разными именами («AniLiberty» — новое имя AniLibria,
//     «Мега-Аниме» и «Mega-Anime») сводит таблица псевдонимов: её ведут
//     админка и обучение по транслиту (SetVoiceAliases).
func VoiceKey(raw string) string {
	return ResolveVoiceKey(BaseVoiceKey(raw))
}

// BaseVoiceKey — ключ по одной подписи, без таблицы псевдонимов: под ним
// написание запоминается, и на него ссылаются сами псевдонимы.
func BaseVoiceKey(raw string) string {
	_, k := parseVoice(raw)
	return k
}

// parseVoice — подпись для человека и ключ без псевдонимов.
func parseVoice(raw string) (title, key string) {
	s := strings.TrimSpace(raw)
	s = trackNoRe.ReplaceAllString(s, "")
	lang := ""
	if m := trackLangRe.FindStringSubmatch(s); m != nil {
		lang = m[1]
		s = trackLangRe.ReplaceAllString(s, "")
	}
	// «Одноголосый. Воротилин» — вид и студия; «А. Гаврилов» — инициал, его
	// не отрезать.
	if i := strings.LastIndex(s, ". "); i >= 0 && strings.TrimSpace(s[i+2:]) != "" && trackKind(s[:i]) {
		s = s[i+2:]
	}
	s = strings.TrimSpace(s)
	if k, ok := parseKind(s); ok {
		return k.title, k.key
	}
	if s == "" || unnamedRe.MatchString(s) {
		if l := LanguageVoice(lang); l != "" {
			return l, squash(l)
		}
	}
	if s == "" {
		s = strings.TrimSpace(raw)
	}
	return s, squash(s)
}

// trackKind — похоже ли на вид дорожки, как его пишет kinopub:
// «Одноголосый», «Многоголосый закадровый», «Дублированный», «Авторский».
func trackKind(s string) bool {
	s = strings.ToLower(s)
	for _, w := range []string{"голос", "дубл", "закадр", "автор", "профес", "любит", "оригин"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// IsVoiceKeyForm — строка уже ключ (строчные буквы и цифры без
// разделителей), а не подпись. Ключ повторно не разбирают: слитое
// «многоголосыйзакадровыйневафильм» разбор принял бы за вид.
func IsVoiceKeyForm(s string) bool {
	return s != "" && squash(s) == s
}

// squash — написание без регистра, «ё», пробелов, точек и хвоста «TV».
func squash(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "ё", "е")
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'а' && r <= 'я' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return strings.TrimSuffix(b.String(), "tv")
}

// ── вид озвучки ───────────────────────────────────────────────────────────

// voiceKind — вид озвучки без студии. Порядок — старшинство: в «Авторском
// одноголосом» вид — авторский, в «Многоголосом дублированном» — дубляж.
type voiceKind int

const (
	kindNone voiceKind = iota
	kindOriginal
	kindOne
	kindTwo
	kindMulti
	kindAuthor
	kindDub
)

// kindNames — ключ и подпись вида. Ключ — как пишет kinopub («DUB», «MVO»):
// дорожку его потока установленные приложения ищут по ключу сами, и
// прежних сборок с другим ключом на руках ещё много.
var kindNames = map[voiceKind]struct{ key, title string }{
	kindDub:      {"dub", "Дубляж"},
	kindAuthor:   {"avo", "Авторский"},
	kindMulti:    {"mvo", "Многоголосый"},
	kindTwo:      {"dvo", "Двухголосый"},
	kindOne:      {"vo", "Одноголосый"},
	kindOriginal: {"оригинал", "Оригинал"},
}

type parsedKind struct{ key, title string }

// parseKind разбирает подпись без студии: «DUB», «Дублированный», «Полное
// дублирование (лицензия)», «Профессиональный многоголосый закадровый»,
// «MVO». Подпись — вид, только если ВСЕ её слова из словаря вида: в «AniDUB»,
// «2D-DUB» и «Дубляж Netflix» есть чужое слово, и это студии.
//
// Словарь — корни, а не написания: новая формулировка площадки («Многоголосый,
// профессиональный») разбирается без правки кода. Любительский — отдельный
// вид: это другие люди, чем у профессионального.
func parseKind(s string) (parsedKind, bool) {
	words := strings.FieldsFunc(strings.ReplaceAll(strings.ToLower(s), "ё", "е"), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'а' && r <= 'я' || r >= '0' && r <= '9')
	})
	if len(words) == 0 {
		return parsedKind{}, false
	}
	best, amateur := kindNone, false
	for _, w := range words {
		k, am, filler := kindWord(w)
		switch {
		case k != kindNone:
			if k > best {
				best = k
			}
		case am:
			amateur = true
		case !filler:
			return parsedKind{}, false // чужое слово — это студия
		}
	}
	switch {
	case best == kindOriginal:
		n := kindNames[kindOriginal]
		return parsedKind{n.key, n.title}, true
	case best == kindNone && amateur:
		return parsedKind{"любительский", "Любительский"}, true
	case best == kindNone:
		return parsedKind{}, false // одни служебные слова: «Русская дорожка»
	case amateur:
		t := "Любительский " + strings.ToLower(kindNames[best].title)
		return parsedKind{squash(t), t}, true
	}
	n := kindNames[best]
	return parsedKind{n.key, n.title}, true
}

// kindWord — что слово значит для вида: сам вид, «любительский» или
// служебное слово, которое вида не меняет («закадровый», «лицензия»).
func kindWord(w string) (k voiceKind, amateur, filler bool) {
	has := func(roots ...string) bool {
		for _, r := range roots {
			if strings.HasPrefix(w, r) {
				return true
			}
		}
		return false
	}
	switch {
	case w == "dub" || w == "dubbing" || w == "dubbed" || has("дубл"):
		return kindDub, false, false
	case w == "avo" || has("автор"):
		return kindAuthor, false, false
	case w == "mvo" || has("многогол"):
		return kindMulti, false, false
	case w == "dvo" || has("двухгол", "двугол"):
		return kindTwo, false, false
	case w == "vo" || has("одногол"):
		return kindOne, false, false
	case w == "orig" || w == "original" || has("оригин"):
		return kindOriginal, false, false
	case w == "amateur" || has("любит"):
		return kindNone, true, false
	case w == "проф" || w == "pro" || w == "voice" || w == "over" || w == "voiceover" || w == "track" ||
		has("закадр", "профес", "полн", "официал", "лиценз", "перевод", "озвуч", "дорожк", "русск"):
		return kindNone, false, true
	}
	return kindNone, false, false
}

// ── студии под разными именами ────────────────────────────────────────────

var voiceAliases atomic.Pointer[map[string]string]

// SetVoiceAliases ставит таблицу псевдонимов студий: ключ написания (по
// BaseVoiceKey) → ключ студии. Цепочки должны быть уже развёрнуты: здесь
// псевдоним разрешается за один шаг. Ведёт её app/voicenames — из базы.
func SetVoiceAliases(m map[string]string) {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		if k != "" && v != "" && k != v {
			cp[k] = v
		}
	}
	voiceAliases.Store(&cp)
}

// ResolveVoiceKey — ключ студии по ключу написания.
func ResolveVoiceKey(k string) string {
	if m := voiceAliases.Load(); m != nil {
		if to, ok := (*m)[k]; ok {
			return to
		}
	}
	return k
}

// translit — кириллица латиницей, как её пишут названия студий: без
// диакритики и с упрощениями ниже (LooseVoice), где «я» и «ya» сходятся.
var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ж': "zh", 'з': "z",
	'и': "i", 'й': "i", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p",
	'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "h", 'ц': "c", 'ч': "ch",
	'ш': "sh", 'щ': "sh", 'ъ': "", 'ы': "i", 'ь': "", 'э': "e", 'ю': "iu", 'я': "ia",
}

var looseLatin = strings.NewReplacer("kh", "h", "ts", "c", "tz", "c", "ph", "f", "ck", "k",
	"q", "k", "w", "v", "x", "ks", "y", "i")

// LooseVoice — подпись, сведённая к латинице с грубыми упрощениями:
// «Мега-Аниме» и «Mega-Anime», «Сыендук» и «Syenduk», «Анилибрия» и
// «AniLibria» дают одно. Ключом оно не служит — слишком грубое для
// студий, которых никто не видел; по нему обучение находит кандидатов в
// псевдонимы (app/voicenames), а решает уже оно и человек в админке.
func LooseVoice(raw string) string {
	s := strings.ReplaceAll(strings.ToLower(VoiceTitle(raw)), "ё", "е")
	s = strings.ReplaceAll(s, "дж", "j")
	var b strings.Builder
	for _, r := range s {
		if t, ok := translit[r]; ok {
			b.WriteString(t)
		} else if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	s = looseLatin.Replace(b.String())
	// Удвоенные буквы: «Анилибрия» → «anilibriia», «Мега-Аниме» → «megaanime».
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && s[i] == s[i-1] {
			continue
		}
		out = append(out, s[i])
	}
	return strings.TrimSuffix(string(out), "tv")
}

// IsVoiceKind — ключ вида озвучки (дубляж, многоголосый…), а не студии:
// виды сводит разбор, псевдонимы им не нужны.
func IsVoiceKind(key string) bool {
	for _, n := range kindNames {
		if key == n.key {
			return true
		}
	}
	return strings.HasPrefix(key, "любительский")
}

// Subtitled — вариант с субтитрами, а не озвучка: «Субтитры»,
// «Crunchyroll.Subtitles» у Kodik.
func Subtitled(title string) bool {
	t := strings.ToLower(title)
	return strings.Contains(t, "субтитр") || strings.Contains(t, "subtitle")
}
