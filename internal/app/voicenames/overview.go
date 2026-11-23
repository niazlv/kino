package voicenames

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/niazleushkin/kino/internal/domain/provider"
)

// Studio — студия, как её видят площадки: ключ после псевдонимов и все
// написания под ним.
type Studio struct {
	Key       string
	Title     string // самое частое написание
	Spellings []Spelling
	Providers []string
	Hits      int
	// Example — где встречалась последней: человеку проверить на слух.
	Example Spelling
}

// Suggestion — две студии с похожими написаниями: склеить или сказать,
// что разные.
type Suggestion struct {
	A, B Studio
	Why  string
}

// AliasView — псевдоним с тем, как выглядят обе стороны.
type AliasView struct {
	Alias
	FromTitle string
	ToTitle   string
}

type Overview struct {
	Aliases     []AliasView
	Suggestions []Suggestion
	// Studios — найденные по запросу, без запроса — самые частые.
	Studios []Studio
	// Seen — сколько разных написаний записано: пусто — подсказкам не из
	// чего взяться, и это надо сказать, а не показать пустую таблицу.
	Seen int
}

const (
	suggestMax = 60
	studiosMax = 60
)

// Overview — всё для страницы «Озвучки» в админке.
func (s *Service) Overview(ctx context.Context, q string) (Overview, error) {
	sp, err := s.store.Spellings(ctx)
	if err != nil {
		return Overview{}, err
	}
	as, err := s.store.Aliases(ctx)
	if err != nil {
		return Overview{}, err
	}
	rej, err := s.rejected(ctx)
	if err != nil {
		return Overview{}, err
	}
	studios := groupStudios(sp)
	byKey := make(map[string]Studio, len(studios))
	for _, st := range studios {
		byKey[st.Key] = st
	}
	titleOfBase := map[string]string{}
	for _, x := range sp {
		if _, ok := titleOfBase[x.Key]; !ok {
			titleOfBase[x.Key] = x.Title
		}
	}

	out := Overview{Seen: len(sp)}
	for _, a := range as {
		// Справа — написание самой главной студии, а не самое частое в
		// группе: иначе «AniLiberty → AniLiberty», когда новое имя
		// встречается чаще старого.
		v := AliasView{Alias: a, FromTitle: titleOfBase[a.From], ToTitle: titleOfBase[a.To]}
		if v.FromTitle == "" {
			v.FromTitle = a.From
		}
		if v.ToTitle == "" {
			v.ToTitle = byKey[a.To].Title
		}
		if v.ToTitle == "" {
			v.ToTitle = a.To
		}
		out.Aliases = append(out.Aliases, v)
	}
	sort.SliceStable(out.Aliases, func(i, j int) bool {
		return out.Aliases[i].CreatedAt.After(out.Aliases[j].CreatedAt)
	})
	out.Suggestions = suggest(studios, rej)

	q = strings.TrimSpace(strings.ToLower(q))
	lq := provider.LooseVoice(q)
	for _, st := range studios {
		if q != "" && !studioMatches(st, q, lq) {
			continue
		}
		out.Studios = append(out.Studios, st)
	}
	sort.SliceStable(out.Studios, func(i, j int) bool { return out.Studios[i].Hits > out.Studios[j].Hits })
	if len(out.Studios) > studiosMax {
		out.Studios = out.Studios[:studiosMax]
	}
	return out, nil
}

func studioMatches(st Studio, q, lq string) bool {
	if strings.Contains(st.Key, q) || lq != "" && strings.Contains(provider.LooseVoice(st.Title), lq) {
		return true
	}
	for _, x := range st.Spellings {
		if strings.Contains(strings.ToLower(x.Title), q) {
			return true
		}
	}
	return false
}

// groupStudios сводит написания в студии по ключу после псевдонимов. Виды
// озвучки и субтитры — не студии: их сводит разбор.
func groupStudios(sp []Spelling) []Studio {
	idx := map[string]int{}
	var out []Studio
	for _, x := range sp {
		if provider.IsVoiceKind(x.Key) || provider.Subtitled(x.Title) {
			continue
		}
		k := provider.ResolveVoiceKey(x.Key)
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, Studio{Key: k})
		}
		st := &out[i]
		st.Spellings = append(st.Spellings, x)
		st.Hits += x.Hits
		if x.LastSeen.After(st.Example.LastSeen) {
			st.Example = x
		}
	}
	for i := range out {
		st := &out[i]
		sort.SliceStable(st.Spellings, func(a, b int) bool { return st.Spellings[a].Hits > st.Spellings[b].Hits })
		st.Title = st.Spellings[0].Title
		seen := map[string]bool{}
		for _, x := range st.Spellings {
			if !seen[x.Provider] {
				seen[x.Provider] = true
				st.Providers = append(st.Providers, x.Provider)
			}
		}
		sort.Strings(st.Providers)
	}
	return out
}

// suggest — пары похожих студий. Сравниваются только студии с общим
// началом латиницы (три буквы): опечатки и приписки («AniStar
// Многоголосый», «AniLibria.TV Old») живут в хвосте, а сравнивать
// каждую с каждой из тысяч незачем.
func suggest(studios []Studio, rej map[[2]string]bool) []Suggestion {
	type item struct {
		st    Studio
		loose string
	}
	buckets := map[string][]item{}
	for _, st := range studios {
		l := provider.LooseVoice(st.Title)
		if len(l) < minLoose {
			continue
		}
		buckets[l[:3]] = append(buckets[l[:3]], item{st, l})
	}
	var out []Suggestion
	for _, b := range buckets {
		for i := 0; i < len(b); i++ {
			for j := i + 1; j < len(b); j++ {
				x, y := b[i], b[j]
				if x.st.Key == y.st.Key || rej[pair(x.st.Key, y.st.Key)] {
					continue
				}
				why := similar(x.loose, y.loose)
				if why == "" {
					continue
				}
				a, c := x.st, y.st
				// Главной слева — та, что встречается чаще: её и
				// предлагается оставить.
				if c.Hits > a.Hits {
					a, c = c, a
				}
				out = append(out, Suggestion{A: a, B: c, Why: why})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		hi, hj := out[i].A.Hits+out[i].B.Hits, out[j].A.Hits+out[j].B.Hits
		if hi != hj {
			return hi > hj
		}
		return out[i].A.Key+out[i].B.Key < out[j].A.Key+out[j].B.Key
	})
	if len(out) > suggestMax {
		out = out[:suggestMax]
	}
	return out
}

// similar — чем похожи две латиницы, пусто — ничем.
func similar(a, b string) string {
	if len(a) > len(b) {
		a, b = b, a
	}
	if strings.HasPrefix(b, a) {
		return "одно — начало другого"
	}
	max := 1
	if len(a) >= 8 {
		max = 2
	}
	if len(b)-len(a) > max {
		return ""
	}
	if d := distance(a, b, max); d <= max {
		return "отличаются на " + strconv.Itoa(d) + " " + letters(d)
	}
	return ""
}

func letters(n int) string {
	if n == 1 {
		return "букву"
	}
	return "буквы"
}

// distance — расстояние Левенштейна, но не дальше max+1: больше и не нужно.
func distance(a, b string, max int) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		low := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			low = min(low, cur[j])
		}
		if low > max {
			return max + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
