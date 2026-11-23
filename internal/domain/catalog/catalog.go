// Package catalog — канонический контент: тайтлы, сезоны, эпизоды и их связь
// с площадками. Канонические сущности — единственное, на что ключуется
// пользовательское состояние.
package catalog

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type Kind string

const (
	KindMovie  Kind = "movie"
	KindSeries Kind = "series"
)

// Species — вид произведения, как его называет человек: не жанр и не
// «фильм или сериал», а то, что стоит в анкете тайтла.
type Species string

const (
	SpeciesAnime   Species = "anime"
	SpeciesCartoon Species = "cartoon"
	SpeciesDorama  Species = "dorama"
	SpeciesSeries  Species = "series"
	SpeciesMovie   Species = "movie"
)

// ExternalIDs — фундамент склейки одного тайтла из разных площадок.
type ExternalIDs struct {
	IMDB      string
	TMDB      string
	Kinopoisk string
}

// StrongKey возвращает надёжный ключ матчинга, если он есть.
// Пусто -> придётся падать в fuzzy-матч по названию и году.
func (e ExternalIDs) StrongKey() (string, bool) {
	switch {
	case e.IMDB != "":
		return "imdb:" + e.IMDB, true
	case e.TMDB != "":
		return "tmdb:" + e.TMDB, true
	case e.Kinopoisk != "":
		return "kp:" + e.Kinopoisk, true
	}
	return "", false
}

// Title — КАНОНИЧЕСКИЙ тайтл, не тайтл площадки. Один Title может иметь
// несколько Source из разных провайдеров.
type Title struct {
	ID       kernel.TitleID
	Kind     Kind
	IDs      ExternalIDs
	Title    string
	Original string
	Year     int
	Genres   []string
	// Countries — страны производства, как их называют площадки: «Корея
	// Южная», «США». Не всегда есть: приехать должен от площадки, и у
	// заведённых до неё тайтлов пусто до ближайшего её прохода.
	Countries []string
	Plot      string
	Poster    string
	Finished  bool
	Sources   []Source
	// SourcesCheckedAt — когда площадки в последний раз спрашивали об этом
	// тайтле при открытии (досбор). Нулевое — не спрашивали.
	SourcesCheckedAt time.Time
}

// Source — присутствие канонического тайтла на конкретной площадке.
type Source struct {
	Ref      kernel.ProviderRef
	Priority int // при резолве берётся источник с наибольшим приоритетом
	// SeasonNo > 0 — элемент площадки покрывает только этот сезон
	// произведения (аниме-площадки заводят страницу на сезон). Ноль —
	// элемент покрывает произведение целиком в своей нумерации.
	SeasonNo int
	// Disabled — источник отвязан от резолва вручную (плохое качество,
	// ошибочная склейка), но сама связь помнится: иначе следующий обход
	// площадки завёл бы её заново.
	Disabled bool
}

// BestSource — какую площадку спрашивать за ссылкой.
func (t Title) BestSource() (Source, bool) {
	var best Source
	found := false
	for _, s := range t.Sources {
		if s.Disabled {
			continue
		}
		if !found || s.Priority > best.Priority {
			best, found = s, true
		}
	}
	return best, found
}

type Episode struct {
	ID         kernel.EpisodeID
	TitleID    kernel.TitleID
	SeasonNo   int
	EpisodeNo  int
	AbsoluteNo *int // площадки расходятся в нумерации (классика для аниме)
	Title      string
	// TitleKnown — название серии уже спрашивали у площадки. Пустое Title
	// при TitleKnown значит «площадка не даёт», без него — «ещё не
	// перечитывали»; по второму ленивый каталог подтягивает серии заново.
	TitleKnown  bool
	DurationSec int
	Thumbnail   string
}

// Offer — канонская серия у конкретной площадки, в ЕЁ нумерации.
//
// Нумерации расходятся: у kinopub «Принцесса-медуза» — сериал с сезонами,
// у аниме-площадки каждый сезон — отдельная страница с сериями от единицы.
// Поэтому пересчёт номера делается один раз, при записи серий, и хранится:
// резолв спрашивает площадку её же номерами и ничего не пересчитывает.
type Offer struct {
	Ref       kernel.ProviderRef
	SeasonNo  int
	EpisodeNo int
	Priority  int
	// MAL — номер MyAnimeList у элемента площадки (у аниме-площадок).
	// Вместе с EpisodeNo — адрес серии в базах, ключованных по MAL
	// (AniSkip): элемент площадки и запись MAL — обычно один сезон.
	MAL string
}

// Choice — чего хочет человек от резолва.
type Choice struct {
	// Source — площадка, выбранная явно или запомненная для тайтла.
	Source string
	// Strict — выбор сделан только что и руками: если площадка не отдала
	// серию, об этом надо сказать, а не молча показать другую.
	Strict bool
}

// Rank упорядочивает предложения: выбранная площадка первой, дальше по
// приоритету. Предложения выключенных источников сюда не попадают — их
// отсекает хранилище.
//
// Остальные площадки остаются в списке и при нестрогом выборе: серия,
// которой нет у любимой площадки, всё равно должна играть — ради этого
// источники и дополняют друг друга.
func Rank(offers []Offer, c Choice) []Offer {
	out := make([]Offer, 0, len(offers))
	for _, o := range offers {
		if c.Strict && c.Source != "" && o.Ref.ProviderID != c.Source {
			continue
		}
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].Ref.ProviderID == c.Source, out[j].Ref.ProviderID == c.Source
		if ai != aj {
			return ai
		}
		return out[i].Priority > out[j].Priority
	})
	return out
}

// NormalizeName сводит написание названия к одной форме: регистр, «ё»,
// знаки препинания. «Принцесса-медуза» и «принцесса медуза» — одно и то же
// для склейки и для поиска, и правило у них обязано быть общим.
func NormalizeName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
			continue
		}
		space = true
	}
	return b.String()
}

// IsAnime — есть ли среди жанров аниме. Жанр, а не вид: у kinopub аниме
// приходит в жанрах, а вид остаётся фильмом или сериалом.
func IsAnime(genres []string) bool {
	for _, g := range genres {
		g = strings.TrimSpace(g)
		if strings.EqualFold(g, "аниме") || strings.EqualFold(g, "anime") {
			return true
		}
	}
	return false
}

// IsCartoon — есть ли среди жанров мультфильм: так kinopub пишет
// анимацию, которую не считает аниме, — китайскую, корейскую и западную.
func IsCartoon(genres []string) bool {
	for _, g := range genres {
		g = strings.TrimSpace(g)
		if strings.EqualFold(g, "мультфильм") || strings.EqualFold(g, "animation") {
			return true
		}
	}
	return false
}

// doramaCountries — Корея: дорама — это корейский сериал или фильм, и вид
// берётся из страны производства, потому что жанра «дорама» у площадок
// нет. Формы, какие встречаются: кинопоисковская «Корея Южная», голое
// «Корея», английские. «Корея Северная» — не дорама, сериалы Японии,
// Китая и прочей Азии — тоже.
var doramaCountries = map[string]bool{
	"корея": true, "корея южная": true, "южная корея": true,
	"korea": true, "south korea": true,
}

// IsDoramaCountry — корейское ли произведение по странам производства.
// Пустой список — «неизвестно», и это не «не дорама»: у тайтла без стран
// вида дорамы не бывает, но и опровергнуть его нельзя.
func IsDoramaCountry(countries []string) bool {
	for _, c := range countries {
		if doramaCountries[strings.ToLower(strings.TrimSpace(c))] {
			return true
		}
	}
	return false
}

// Species — вид произведения: аниме и мультфильм живут в жанрах, дорама —
// в стране производства (Корея; сериал и фильм), остальное решает вид
// фильм/сериал. Приоритет снизу вверх: японская анимация — аниме, а не
// дорама, корейская — мультфильм, а не дорама.
func (t Title) Species() Species {
	switch {
	case IsAnime(t.Genres):
		return SpeciesAnime
	case IsCartoon(t.Genres):
		return SpeciesCartoon
	case IsDoramaCountry(t.Countries):
		return SpeciesDorama
	case t.Kind == KindSeries:
		return SpeciesSeries
	default:
		return SpeciesMovie
	}
}

// OriginalOf — оригинальное название: отдельным полем, а если его нет —
// вторая половина «Русское / Original», как пишет kinopub.
func (t Title) OriginalOf() string {
	if t.Original != "" {
		return t.Original
	}
	if _, orig, ok := strings.Cut(t.Title, " / "); ok {
		return strings.TrimSpace(orig)
	}
	return ""
}
