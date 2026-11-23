package matching

import (
	"strings"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Лечение сезонов, склеенных не туда.
//
// Аниме-площадки заводят страницу на сезон, а kinopub — сериал целиком.
// Когда номер сезона со страницы не прочитан (animego.me пишет «Гинтама 3»,
// без слова «сезон»), сезон становится отдельным тайтлом, а одна и та же
// работа у двух площадок — разными сезонами одного тайтла. Здесь — чистые
// решения, как это свести: Realign выравнивает сезоны внутри тайтла,
// Adopt переносит тайтл-сезон к его сериалу.

// Move — привязку ref перевести на сезон To.
type Move struct {
	Ref Ref
	To  int
}

// Realign выравнивает сезоны привязок одного тайтла по номеру MAL.
//
// Номер MAL сезонный: у двух площадок с одним номером — одна работа, и
// разные сезоны у них значат, что серии одной площадки играют под чужим
// сезоном («Пять невест» 2 сезон с animego.me в первом). Верный сезон —
// поставленный человеком, иначе наибольший: номер больше первого берётся
// только из явного маркера на странице, а «первый» — это ещё и «площадка
// не сказала».
func Realign(c Candidate) []Move {
	groups := map[string][]Ref{}
	var order []string
	for _, r := range c.Refs {
		if r.MAL == "" || r.SeasonNo == 0 {
			continue
		}
		if _, ok := groups[r.MAL]; !ok {
			order = append(order, r.MAL)
		}
		groups[r.MAL] = append(groups[r.MAL], r)
	}
	var out []Move
	for _, mal := range order {
		g := groups[mal]
		target, ok := groupSeason(g)
		if !ok {
			continue
		}
		for _, r := range g {
			if r.Pinned || r.SeasonNo == target || occupied(c, r, target) {
				continue
			}
			out = append(out, Move{Ref: r, To: target})
		}
	}
	return out
}

// groupSeason — сезон одной работы: закреплённый человеком или наибольший.
// Ложь — спорить не о чем или закреплённые расходятся между собой.
func groupSeason(g []Ref) (int, bool) {
	pinned, top := 0, 0
	seasons := map[int]bool{}
	for _, r := range g {
		seasons[r.SeasonNo] = true
		if r.SeasonNo > top {
			top = r.SeasonNo
		}
		if r.Pinned {
			if pinned != 0 && pinned != r.SeasonNo {
				return 0, false
			}
			pinned = r.SeasonNo
		}
	}
	if len(seasons) < 2 {
		return 0, false
	}
	if pinned != 0 {
		return pinned, true
	}
	return top, true
}

// occupied — сезон target у той же площадки уже занят другой работой.
func occupied(c Candidate, r Ref, target int) bool {
	for _, x := range c.Refs {
		if x.ProviderID == r.ProviderID && x.ItemID != r.ItemID &&
			x.SeasonNo == target && x.MAL != r.MAL {
			return true
		}
	}
	return false
}

// Adoption — тайтл-сезон переносится в Into сезоном SeasonNo.
type Adoption struct {
	Into     kernel.TitleID
	SeasonNo int
	// By — mal | name, как у Decision.
	By string
}

// Adopt решает, не сезон ли тайтл o другого сериала из cands.
//
// o — тайтл, заведённый аниме-площадками отдельно; names — его название и
// оригинал. Переносится только тайтл одной работы (один номер MAL на все
// привязки): тайтл из нескольких сезонов разбирать правилами опасно, это
// дело админки.
//
// Сначала номер MAL: та же работа уже есть у другого тайтла — сезон берётся
// оттуда (или явный свой, если он больше). Затем название без номера и
// номер сезона — явный со страницы («2 сезон») или голое число («Гинтама
// 3»). Перенос строже склейки при обходе: он пересматривает уже принятое
// решение, поэтому хозяин обязан этот номер продолжать (continues).
func Adopt(o Candidate, names []string, cands []Candidate) (Adoption, bool) {
	if o.Kind != "series" || !o.Anime || len(o.Refs) == 0 {
		return Adoption{}, false
	}
	mal, explicit := "", 0
	for _, r := range o.Refs {
		if r.Pinned || r.SeasonNo == 0 {
			return Adoption{}, false
		}
		if r.MAL != "" {
			if mal != "" && mal != r.MAL {
				return Adoption{}, false
			}
			mal = r.MAL
		}
		if r.SeasonNo > explicit {
			explicit = r.SeasonNo
		}
	}

	if mal != "" {
		var owners []Candidate
		n := explicit
		for _, c := range cands {
			if !kin(o, c) {
				continue
			}
			found := false
			for _, r := range c.Refs {
				if r.MAL == mal && r.SeasonNo > 0 {
					found = true
					if r.SeasonNo > n {
						n = r.SeasonNo
					}
				}
			}
			if found {
				owners = append(owners, c)
			}
		}
		if len(owners) > 1 {
			return Adoption{}, false
		}
		if len(owners) == 1 {
			// Сезоны совпали и равны первому — это обычный дубль, его
			// сливает ShouldMerge.
			if n <= 1 || !fits(o, n) || clash(owners[0], o, n, mal) {
				return Adoption{}, false
			}
			return Adoption{Into: owners[0].ID, SeasonNo: n, By: "mal"}, true
		}
	}

	n := explicit
	bases := BaseNames(names...)
	if n <= 1 {
		var b []string
		if b, n = bareSeason(names...); n <= 1 {
			return Adoption{}, false
		}
		bases = b
	}
	if !fits(o, n) || o.Year == 0 {
		return Adoption{}, false
	}
	keys := Keys(bases...)
	var hits []Candidate
	for _, c := range cands {
		if !kin(o, c) || !intersects(keys, c.Keys) || !continues(c, n) {
			continue
		}
		if c.Year == 0 || c.Year > o.Year || o.Year-c.Year > 30 {
			continue
		}
		hits = append(hits, c)
	}
	// Хозяин — лучший по названию. Если его сезон занят другой работой, это
	// не повод отдать сезон кандидату похуже: «Берсерк 2nd Season» ушёл бы
	// к «Берсерку» 1997 года.
	if hits = narrow(hits, o.Year, n); len(hits) == 1 && !clash(hits[0], o, n, mal) {
		return Adoption{Into: hits[0].ID, SeasonNo: n, By: "name"}, true
	}
	return Adoption{}, false
}

// kin — c годится в хозяева o: другой тайтл, сериал, аниме. Закреплённые
// привязки хозяина не мешают: clash не даст занять сезон, который человек
// отдал другой работе.
func kin(o, c Candidate) bool {
	return c.ID != o.ID && c.Kind == "series" && c.Anime
}

// fits — все привязки o ложатся в сезон n: они стоят либо на нём, либо на
// «первом» по умолчанию. Другой номер значит, что в тайтле не один сезон.
func fits(o Candidate, n int) bool {
	for _, r := range o.Refs {
		if r.SeasonNo != 1 && r.SeasonNo != n {
			return false
		}
	}
	return true
}

// clash — сезон n у хозяина занят другой работой: той же площадкой с
// другим элементом или другим номером MAL.
func clash(c, o Candidate, n int, mal string) bool {
	for _, x := range c.Refs {
		if x.SeasonNo != n {
			continue
		}
		if mal != "" && x.MAL != "" && x.MAL != mal {
			return true
		}
		for _, r := range o.Refs {
			if x.ProviderID == r.ProviderID && x.ItemID != r.ItemID {
				return true
			}
		}
	}
	return false
}

// BaseNames — названия без маркера сезона и без голого числа в конце: по
// ним ищется сериал, чьим сезоном может быть тайтл.
func BaseNames(names ...string) []string {
	var out []string
	for _, n := range names {
		for _, part := range strings.Split(n, " / ") {
			base, _ := SplitSeason(part)
			if b, s := BareNumber(base); s > 0 {
				base = b
			}
			out = append(out, base)
		}
	}
	return out
}
