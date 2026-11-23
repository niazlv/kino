package matching

import (
	"strings"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Candidate — канонический тайтл, с которым входящий МОЖЕТ совпасть.
type Candidate struct {
	ID    kernel.TitleID
	Kind  string
	Year  int
	IMDB  string
	KP    string
	Anime bool
	// Cartoon — у хозяина метаданных это «Мультфильм»: так kinopub пишет
	// китайскую и корейскую анимацию, а не «Аниме» (см. sameMedium).
	Cartoon bool
	// Keys — ключи названий (см. Keys), уже сохранённые у тайтла.
	Keys []string
	Refs []Ref
}

// Ref — уже привязанный к кандидату элемент площадки.
type Ref struct {
	ProviderID string
	ItemID     string
	SeasonNo   int
	MAL        string
	// Pinned — привязку поставил человек; такие решения не пересматриваются.
	Pinned bool
}

// Pair — два канонических тайтла, между которыми склейка могла развалиться.
// Кандидатов достаёт адаптер по совпадению MAL или ключей названий,
// решение — здесь, в чистой логике.
type Pair struct {
	A, B Candidate
}

// Decision — куда класть входящий элемент.
type Decision struct {
	// TitleID ноль — заводить новый тайтл.
	TitleID kernel.TitleID
	// SeasonNo — какой сезон канонического тайтла покрывает элемент;
	// ноль — весь тайтл в нумерации площадки.
	SeasonNo int
	// By — чем склеено: imdb | kp | mal | name | new. Видно в админке.
	By string
}

// Decide выбирает тайтл для входящего элемента площадки.
//
// Порядок — по надёжности ключа: внешние идентификаторы, номер
// MyAnimeList, и только потом названия. Совпадение по названию принимается
// лишь однозначным: два подходящих кандидата — это уже не склейка, а
// лотерея, и тогда заводится новый тайтл.
func Decide(in dp.Title, cands []Candidate) Decision {
	return DecideWith(Options{}, in, cands)
}

// Options — правила склейки, которые включаются отдельно.
type Options struct {
	// CrossCartoon — аниме склеивается с «Мультфильмом» kinopub по
	// латинскому названию (см. sameMedium). Без него аниме — только с аниме.
	CrossCartoon bool
}

// DecideWith — Decide с отдельно включаемыми правилами.
func DecideWith(opts Options, in dp.Title, cands []Candidate) Decision {
	season := in.SeasonNo

	for _, c := range cands {
		if in.IMDB != "" && c.IMDB == in.IMDB {
			return Decision{TitleID: c.ID, SeasonNo: season, By: "imdb"}
		}
	}
	for _, c := range cands {
		if in.Kinopoisk != "" && c.KP == in.Kinopoisk {
			return Decision{TitleID: c.ID, SeasonNo: season, By: "kp"}
		}
	}
	if in.MAL != "" {
		for _, c := range cands {
			for _, r := range c.Refs {
				if r.MAL == in.MAL {
					// Номер MAL сезонный: совпал — значит тот же сезон, и
					// верить надо уже сделанной привязке, а не разбору имени.
					return Decision{TitleID: c.ID, SeasonNo: r.SeasonNo, By: "mal"}
				}
			}
		}
	}

	keys := Keys(append([]string{in.Title, in.Original}, in.AltTitles...)...)
	med := medium{anime: IsAnime(in.Genres), cartoon: opts.CrossCartoon && IsCartoon(in.Genres)}
	var hits []Candidate
	for _, c := range cands {
		if byName(in, med, keys, c, opts) {
			hits = append(hits, c)
		}
	}
	if hits = narrow(hits, in.Year, season); len(hits) == 1 {
		if taken(hits[0], in.Ref, season) {
			// Лучший по названию уже держит этот сезон другим элементом
			// той же площадки — это другое произведение с тем же
			// названием, и отдавать сезон кандидату похуже нельзя.
			return Decision{SeasonNo: season, By: "new"}
		}
		return Decision{TitleID: hits[0].ID, SeasonNo: season, By: "name"}
	}

	// «Гинтама 3»: animego.me пишет сезон голым числом, и без этого шага
	// каждый сезон становился отдельным тайтлом — а за ним и следующие,
	// которым отдельные тайтлы-однофамильцы мешали склеиться по названию.
	if len(hits) == 0 && in.Kind == "series" && season <= 1 {
		if bases, n := bareSeason(in.Title, in.Original); n > 1 {
			bin := in
			bin.SeasonNo = n
			bkeys := Keys(bases...)
			var bare []Candidate
			for _, c := range cands {
				if byName(bin, med, bkeys, c, opts) && continues(c, n) {
					bare = append(bare, c)
				}
			}
			if bare = narrow(bare, in.Year, n); len(bare) == 1 && !taken(bare[0], in.Ref, n) {
				return Decision{TitleID: bare[0].ID, SeasonNo: n, By: "name"}
			}
		}
	}
	return Decision{SeasonNo: season, By: "new"}
}

// narrow выбирает из однофамильцев.
//
// Для первого сезона — точный год, если он отличает. Сезон позже первого
// принадлежит сериалу, который начался не позже него и этот номер
// продолжает (continues). Из заведённых целиком (привязка на весь тайтл,
// как у kinopub) — ближайший по году: ремейк kinopub заводит отдельным
// сериалом, и «Берсерк» 2017 года — продолжение «Берсерка» 2016-го, а не
// 1997-го. Остальные однофамильцы — такие же отдельно заведённые сезоны,
// и из них хозяин — самый ранний. Без этого один отдельный сезон «Гинтамы»
// делал бы неоднозначным каждый следующий, и они множились бы цепочкой.
func narrow(hits []Candidate, year, season int) []Candidate {
	if len(hits) < 2 {
		return hits
	}
	if season <= 1 {
		return filter(hits, func(c Candidate) bool { return c.Year == year })
	}
	hits = filter(hits, func(c Candidate) bool { return c.Year <= year })
	if cont := filter(hits, func(c Candidate) bool { return continues(c, season) }); len(cont) > 0 {
		hits = cont
	}
	if len(hits) < 2 {
		return hits
	}
	if whole := filter(hits, wholeTitle); len(whole) > 0 {
		return byYear(whole, func(a, b int) bool { return a > b })
	}
	return byYear(hits, func(a, b int) bool { return a < b })
}

// byYear оставляет тайтлы с «лучшим» годом; better(a, b) — a лучше b.
func byYear(cs []Candidate, better func(a, b int) bool) []Candidate {
	best := cs[0].Year
	for _, c := range cs {
		if better(c.Year, best) {
			best = c.Year
		}
	}
	return filter(cs, func(c Candidate) bool { return c.Year == best })
}

// wholeTitle — у тайтла есть привязка на весь сериал в нумерации площадки
// (сезон 0): так kinopub заводит франшизу со всеми сезонами сразу.
func wholeTitle(c Candidate) bool {
	for _, r := range c.Refs {
		if r.SeasonNo == 0 {
			return true
		}
	}
	return false
}

// continues — сезон n продолжает сериал: у него уже есть сезон n-1, а для
// второго — хотя бы первый или весь тайтл целиком. «Макросс 7» к
// «Макроссу» без шестого сезона так не приклеится: это другой сериал.
func continues(c Candidate, n int) bool {
	for _, r := range c.Refs {
		if r.SeasonNo == n-1 || (n == 2 && r.SeasonNo == 0) {
			return true
		}
	}
	return false
}

// taken — у кандидата этот сезон уже держит другой элемент той же площадки:
// значит, это другое произведение с тем же названием, а не наше.
func taken(c Candidate, ref kernel.ProviderRef, season int) bool {
	for _, r := range c.Refs {
		if r.ProviderID == ref.ProviderID && r.SeasonNo == season && r.ItemID != ref.ItemID {
			return true
		}
	}
	return false
}

func filter(cs []Candidate, keep func(Candidate) bool) []Candidate {
	var out []Candidate
	for _, c := range cs {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

func byName(in dp.Title, med medium, keys []string, c Candidate, opts Options) bool {
	if c.Kind != in.Kind || !intersects(keys, c.Keys) {
		return false
	}
	// Аниме склеивается только с аниме: «Монстр» 2004 года и одноимённое
	// игровое кино совпадают по названию и году, но это разные вещи.
	if !sameMedium(med, keys, medium{anime: c.Anime, cartoon: opts.CrossCartoon && c.Cartoon}, c.Keys) {
		return false
	}
	if in.Year == 0 || c.Year == 0 {
		// Без года по названию склеивать нельзя: слишком много ремейков.
		return false
	}
	if in.SeasonNo > 1 {
		// Второй сезон выходит позже первого: год тайтла у площадки,
		// заводящей произведение целиком, — год ПЕРВОГО сезона.
		return c.Year <= in.Year+1 && in.Year-c.Year <= 30
	}
	return abs(c.Year-in.Year) <= 1
}

// ShouldMerge решает, два ли это тайтла для одного произведения.
//
// Реконсилиация работает с уже заведёнными тайтлами и не знает, кто из
// двух приехал раньше, — поэтому проверка симметрична, без «входящего».
//
// Совпадение MAL сильнее всего остального: одинаковый номер — это одно
// произведение у реестра MyAnimeList, и разночтения названий и годов ему
// не помеха. Без MAL решают ключи названий (включая варианты), но тогда
// включаются те же предохранители, что у byName: вид, «аниме к аниме»,
// год (окно шире, если одна из сторон — сезон позже первого, как у
// byName) и «одна площадка дважды на одном сезоне». Если оба тайтла
// закреплены людьми — не трогаем.
func ShouldMerge(p Pair) bool { return ShouldMergeWith(Options{}, p) }

// ShouldMergeWith — ShouldMerge с отдельно включаемыми правилами.
func ShouldMergeWith(opts Options, p Pair) bool {
	a, b := p.A, p.B
	if anyPinned(a) || anyPinned(b) {
		return false
	}
	if a.Kind != b.Kind {
		return false
	}
	ma := medium{anime: a.Anime, cartoon: opts.CrossCartoon && a.Cartoon}
	mb := medium{anime: b.Anime, cartoon: opts.CrossCartoon && b.Cartoon}
	cross := ma.anime != mb.anime
	if cross && !crossable(ma, mb) {
		return false
	}
	if seasonsDisagree(a, b) {
		// Одна работа у двух тайтлов на разных сезонах: простое слияние
		// положило бы её серии (и чей-то прогресс) в чужой сезон. Такую
		// пару переносит Adopt — вместе с номером сезона.
		return false
	}
	if sharedMAL(a, b) {
		return true
	}
	// Дальше — совпадение по названию, и предохранители byName.
	if a.Year == 0 || b.Year == 0 || !seasonAwareYearsFit(a, b) {
		return false
	}
	if !strongKeyIntersect(a.Keys, b.Keys) {
		return false
	}
	if cross && !latinIntersect(a.Keys, b.Keys) {
		return false
	}
	// Один сезон одного произведения у обеих сторон с разными элементами
	// площадки — два произведения-однофамильца; так byName и решает для
	// свежих элементов, реконсилиация обязана уважать то же правило.
	for _, ra := range a.Refs {
		for _, rb := range b.Refs {
			if ra.ProviderID == rb.ProviderID && ra.SeasonNo == rb.SeasonNo && ra.ItemID != rb.ItemID {
				return false
			}
		}
	}
	return true
}

// seasonsDisagree — общий номер MAL стоит у двух сторон на разных сезонах.
// Привязка на весь тайтл (сезон 0) нумерацию площадки не задаёт и спором
// не считается.
func seasonsDisagree(a, b Candidate) bool {
	for _, ra := range a.Refs {
		if ra.MAL == "" || ra.SeasonNo == 0 {
			continue
		}
		for _, rb := range b.Refs {
			if rb.MAL == ra.MAL && rb.SeasonNo != 0 && rb.SeasonNo != ra.SeasonNo {
				return true
			}
		}
	}
	return false
}

func sharedMAL(a, b Candidate) bool {
	for _, ra := range a.Refs {
		if ra.MAL == "" {
			continue
		}
		for _, rb := range b.Refs {
			if ra.MAL == rb.MAL {
				return true
			}
		}
	}
	return false
}

// anyPinned — хотя бы одну привязку тайтла поставил человек.
func anyPinned(c Candidate) bool {
	for _, r := range c.Refs {
		if r.Pinned {
			return true
		}
	}
	return false
}

// strongKeyIntersect — совпадение по многословному ключу.
//
// Одно слово — слишком слабый признак для слияния уже заведённых тайтлов:
// «персона» стоит и у Persona 4, и у Persona 5, а широкое окно лет у
// сезонных произведений пропускает такие пары. Полное название франшизы
// («восхождение героя щита») повторяется у всех её сезонов, и одно слово
// в слиянии не нуждается — его накрывает MAL или точная склейка при обходе.
func strongKeyIntersect(a, b []string) bool {
	for _, x := range a {
		if !strings.Contains(x, " ") {
			continue
		}
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// seasonAwareYearsFit — окно лет. У произведений, у которых есть сезоны
// позже первого, годы расходятся сильнее, чем у ремейка: второй сезон
// выходит через несколько лет после первого (полное правило — в byName).
func seasonAwareYearsFit(a, b Candidate) bool {
	hi, lo := a.Year, b.Year
	if hi < lo {
		hi, lo = lo, hi
	}
	if maxSeason(a, b) > 1 {
		return hi-lo <= 30
	}
	return hi-lo <= 1
}

func maxSeason(cs ...Candidate) int {
	m := 0
	for _, c := range cs {
		for _, r := range c.Refs {
			if r.SeasonNo > m {
				m = r.SeasonNo
			}
		}
	}
	return m
}

// IsAnime — есть ли среди жанров аниме (см. catalog.IsAnime).
func IsAnime(genres []string) bool { return catalog.IsAnime(genres) }

// IsCartoon — есть ли среди жанров мультфильм (см. catalog.IsCartoon).
func IsCartoon(genres []string) bool { return catalog.IsCartoon(genres) }

// medium — что это за вещь для склейки: аниме, мультфильм или ни то ни
// другое (игровое кино).
type medium struct{ anime, cartoon bool }

// crossable — аниме и мультфильм: такие стороны склеиваются, но строже
// обычного (см. sameMedium). Аниме с игровым кино — никогда.
func crossable(a, b medium) bool {
	return a.anime && b.cartoon || b.anime && a.cartoon
}

// sameMedium — «аниме склеивается только с аниме» с одной поправкой.
//
// kinopub пишет китайскую и корейскую анимацию «Мультфильмом», а не
// «Аниме», и «Расхититель гробниц / Dogulwang» у него и у аниме-площадок
// заводился двумя тайтлами. Аниме с мультфильмом склеивается — но только по
// совпадению латинского названия (оригинал, ромадзи, пиньинь): русское у
// мультфильма и аниме-однофамильца совпадает куда чаще оригинального.
func sameMedium(a medium, aKeys []string, b medium, bKeys []string) bool {
	if a.anime == b.anime {
		return true
	}
	return crossable(a, b) && latinIntersect(aKeys, bKeys)
}

// latinIntersect — общий ключ латиницей не короче четырёх символов, с
// буквами: оригинальное название, а не «2045» и не «one».
func latinIntersect(a, b []string) bool {
	set := make(map[string]bool, len(b))
	for _, k := range b {
		set[k] = true
	}
	for _, k := range a {
		if !set[k] {
			continue
		}
		base := strings.TrimPrefix(strings.TrimPrefix(k, "f:"), "s:")
		if len(base) >= 4 && isLatin(base) && strings.ContainsFunc(base, func(r rune) bool { return r >= 'a' && r <= 'z' }) {
			return true
		}
	}
	return false
}

func intersects(a, b []string) bool {
	set := make(map[string]bool, len(b))
	for _, x := range b {
		set[x] = true
	}
	for _, x := range a {
		if set[x] {
			return true
		}
	}
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
