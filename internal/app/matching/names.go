// Package matching — склейка одного произведения с разных площадок.
//
// Самое дорогое место при подключении второго источника: ошибка в одну
// сторону заводит дубль (история просмотров раскалывается на два тайтла),
// в другую — склеивает разные произведения (серии одного играют под
// названием другого). Поэтому правила здесь нарочно осторожные: при
// сомнении заводится новый тайтл, а ошибочную развязку чинит админка.
//
// Пакет — чистая логика без хранилища: решение принимается по кандидатам,
// которые достаёт адаптер. Так правила проверяются тестами без базы.
package matching

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/niazleushkin/kino/internal/domain/catalog"
)

// minKeyRunes — короче этого ключ не участвует в склейке: у «K» и «92»
// слишком много однофамильцев.
const minKeyRunes = 3

// Keys строит ключи названий для склейки.
//
// Площадки пишут одно и то же по-разному: «Принцесса-шпионка» и «Принцесса
// шпионка», «Re:Zero» и «Re Zero», «ё» и «е», «Название / Original»
// у kinopub одной строкой. Ключ сводит это к одной форме. Маркер сезона
// («2 сезон», «Season 2») отрезается: сезон у склейки идёт отдельным полем,
// а в названии мешает узнать то же произведение.
//
// Помимо точной формы каждый ключ даёт варианты (см. Variants) — иначе
// одно и то же произведение с разными площадками не узнаётся вовсе:
// «Yuki Yuna is a Hero» у одной и «Yuuki Yuuna wa Yuusha de Aru» у другой.
// Варианты пишутся и читаются той же функцией, поэтому расходятся так же
// не могут, как и сама нормализация.
//
// Одна и та же функция применяется при записи ключей и при поиске: две
// разные нормализации однажды разошлись бы и молча перестали склеивать.
func Keys(names ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		for _, part := range strings.Split(n, " / ") {
			base, _ := SplitSeason(part)
			k := normalize(base)
			if len([]rune(k)) < minKeyRunes || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, k)
			for _, v := range Variants(k) {
				if !seen[v] {
					seen[v] = true
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// Variants — записи одного ключа, устойчивые к тому, как его пишет площадка:
//
//   - «f:» — латиница со свёрнутыми долгими гласными (Yuuki -> Yuki):
//     ромадзи одной фразы пишут и с удвоением, и без, а animego.me
//     пользуется лицензионным английским, animego.fr — ромадзи;
//   - «s:» — слова по алфавиту: «Герой Юна Юки» и «Герой Юки Юна» —
//     у русских названий порядок слов не фиксирован.
//
// Префикс не может встретиться в самом ключе (нормализация оставляет только
// буквы, цифры и пробелы), поэтому варианты не сталкиваются с точными.
// Вариант пишется и тогда, когда он совпал с точной формой: пересечение
// работает, только если обе стороны дают одну и ту же запись.
func Variants(k string) []string {
	var out []string
	if isLatin(k) {
		out = append(out, "f:"+foldVowels(k))
	}
	if w := strings.Split(k, " "); len(w) > 1 {
		sort.Strings(w)
		out = append(out, "s:"+strings.Join(w, " "))
	}
	return out
}

// isLatin: свёртка гласных имеет смысл только для ромадзи; русские ключи
// она не меняет, а трогать их лишний раз незачем.
func isLatin(s string) bool {
	for _, r := range s {
		if r >= 0x80 {
			return false
		}
	}
	return true
}

// foldVowels убирает удвоение одинаковых гласных: Yuuna -> Yuna. Обычный
// английский от этого меняется тоже (Book -> Bok), но одинаково на обеих
// сторонах — само по себе это склейку не ломает.
func foldVowels(s string) string {
	var b strings.Builder
	var prev rune
	for _, r := range s {
		if isVowel(r) && r == prev {
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

func isVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

func normalize(s string) string { return catalog.NormalizeName(s) }

var seasonPatterns = []*regexp.Regexp{
	// «Название 2 сезон», «Название 2-й сезон»
	regexp.MustCompile(`(?i)^(.*?)[\s:.,-]+(\d{1,2})(?:-?(?:й|ой|ий))?\s+сезон\s*$`),
	// «Название (сезон 2)», «Название. Сезон 2»
	regexp.MustCompile(`(?i)^(.*?)[\s:.,(-]+сезон\s+(\d{1,2})\)?\s*$`),
	// «Title Season 2», «Title 2nd Season»
	regexp.MustCompile(`(?i)^(.*?)[\s:.,(-]+season\s+(\d{1,2})\)?\s*$`),
	regexp.MustCompile(`(?i)^(.*?)[\s:.,-]+(\d{1,2})(?:st|nd|rd|th)\s+season\s*$`),
}

// bareRe — голое число в конце: «Гинтама 3», «Психопаспорт-2». Три цифры
// не берутся: «Mob Psycho 100» — название, а не сотый сезон.
var bareRe = regexp.MustCompile(`^(.*?\S)[\s:.,-]+(\d{1,2})\s*$`)

// BareNumber отделяет голое число в конце названия: «Гинтама 3» ->
// («Гинтама», 3). Число меньше двух — не номер сезона.
//
// Маркером сезона оно не считается (см. SplitSeason): «Ван-Пис 3» — фильм,
// «Макросс 7» — отдельный сериал франшизы, «Часть 2» — половина сезона.
// Но animego.me пишет сезоны именно так, поэтому число решает — только
// вместе с известным сериалом, который этот номер продолжает (continues).
func BareNumber(name string) (string, int) {
	name = strings.TrimSpace(name)
	m := bareRe.FindStringSubmatch(name)
	if m == nil {
		return name, 0
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n < 2 {
		return name, 0
	}
	base := strings.TrimSpace(m[1])
	if w := strings.Fields(strings.ToLower(base)); len(w) > 0 && notSeason[strings.Trim(w[len(w)-1], ".:,-")] {
		return name, 0
	}
	return base, n
}

// notSeason — слово перед числом, после которого число не сезон:
// «Кэнган Асура. Часть 2» — вторая половина сезона, «Кайдзю № 8» — имя.
var notSeason = map[string]bool{
	"часть": true, "part": true, "cour": true, "№": true, "no": true,
	"глава": true, "chapter": true, "том": true, "vol": true, "volume": true,
	"фильм": true, "movie": true, "film": true, "серия": true, "episode": true, "ova": true,
}

// bareSeason — номер из голого числа в конце названий и сами названия без
// него. Разные числа у разных названий («X 2» и «Y 3») — не ответ.
func bareSeason(names ...string) ([]string, int) {
	var bases []string
	season := 0
	for _, n := range names {
		for _, part := range strings.Split(n, " / ") {
			base, _ := SplitSeason(part)
			b, s := BareNumber(base)
			if s == 0 {
				continue
			}
			if season != 0 && s != season {
				return nil, 0
			}
			season = s
			bases = append(bases, b)
		}
	}
	return bases, season
}

// SplitSeason отделяет маркер сезона от названия: «Врата Штейна 2 сезон»
// -> («Врата Штейна», 2). Без маркера сезон нулевой — «не сказано».
//
// Голое число в конце маркером не считается: «Ван-Пис 3» — это фильм,
// а не третий сезон (см. BareNumber).
func SplitSeason(name string) (string, int) {
	name = strings.TrimSpace(name)
	for _, re := range seasonPatterns {
		m := re.FindStringSubmatch(name)
		if m == nil || strings.TrimSpace(m[1]) == "" {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 {
			continue
		}
		return strings.TrimSpace(m[1]), n
	}
	return name, 0
}
