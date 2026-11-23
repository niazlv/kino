package postgres

import "testing"

// Раскладка «Рика и Морти» из настоящего каталога: у kinopub сезоны 1–9, в пятом 12 серий;
// ЛордФильм (Alloha) нумерует спецвыпуски пятого сезона до 13-й.
func rickLayout() seasonLayout {
	return seasonLayout{
		prio: map[string]int{"kinopub": 100, "lordfilm": 40, "filmix": 38, "yummy": 36},
		tail: map[string]map[int]int{
			"kinopub":  {4: 10, 5: 12, 6: 11, 9: 10},
			"lordfilm": {4: 10, 5: 13, 6: 10, 9: 10},
		},
		last: map[string]int{"kinopub": 9, "lordfilm": 9},
	}
}

func TestSeasonLayout_Extra(t *testing.T) {
	anime := map[string]bool{"yummy": true}
	l := rickLayout()
	cases := []struct {
		name  string
		p     string
		s, ep int
		want  bool
	}{
		{"за концом закрытого сезона", "lordfilm", 5, 13, true},
		{"в пределах сезона", "lordfilm", 5, 12, false},
		{"последний сезон — новая серия", "lordfilm", 9, 11, false},
		{"новый сезон", "lordfilm", 10, 1, false},
		{"сезона нет у старшей площадки", "lordfilm", 7, 1, false},
		{"старшая площадка не сверяется с младшей", "kinopub", 5, 14, false},
		{"Filmix младше kinopub", "filmix", 6, 12, true},
		{"аниме-площадка не трогается", "yummy", 5, 13, false},
	}
	for _, c := range cases {
		if got := l.extra(c.p, c.s, c.ep, anime); got != c.want {
			t.Errorf("%s: extra(%s, %d×%d) = %v, ожидалось %v", c.name, c.p, c.s, c.ep, got, c.want)
		}
	}

	// Аниме-площадка не служит образцом: её сезоны — не телевизионные.
	l.prio["animego_me"] = 60
	l.tail["animego_me"] = map[int]int{1: 12, 2: 12}
	l.last["animego_me"] = 2
	if l.extra("lordfilm", 1, 13, map[string]bool{"animego_me": true}) {
		t.Error("ЛордФильм сверился с аниме-площадкой")
	}

	// Равный приоритет (ничего не настроено) — правило молчит.
	eq := rickLayout()
	eq.prio = map[string]int{"kinopub": 10, "lordfilm": 10}
	if eq.extra("lordfilm", 5, 13, nil) {
		t.Error("при равных приоритетах серия признана лишней")
	}

	// Ручной приоритет у тайтла поднял ЛордФильм выше kinopub — теперь
	// образец он, и его 5×13 не лишняя.
	up := rickLayout()
	up.prio["lordfilm"] = 120
	if up.extra("lordfilm", 5, 13, nil) {
		t.Error("ЛордФильм с ручным приоритетом выше kinopub всё равно сверяется с ним")
	}
}
