package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// sweepWorld — каталог одного тайтла и площадки вокруг него. Элемент площадки
// «про наш тайтл», если его ItemID начинается с "same": склейка привязывает
// такие к тайтлу, остальные заводит отдельными.
type sweepWorld struct {
	mu        sync.Mutex
	title     catalog.Title
	stored    map[string]bool // «сезон:серия» в каталоге
	found     map[string][]dp.Title
	lookup    map[string]dp.Title
	down      map[string]bool
	episodes  map[string]int // сколько серий отдаёт площадка
	searches  []string
	lookups   []string
	fetched   []string
	asks      map[string]time.Time
	others    int
	fetchedCh chan string
}

func newSweepWorld(t catalog.Title) *sweepWorld {
	return &sweepWorld{title: t, stored: map[string]bool{}, found: map[string][]dp.Title{},
		lookup: map[string]dp.Title{}, down: map[string]bool{}, episodes: map[string]int{},
		asks: map[string]time.Time{}, fetchedCh: make(chan string, 16)}
}

// Reader
func (w *sweepWorld) Search(context.Context, string, int, int) ([]catalog.Title, int, error) {
	return nil, 0, nil
}
func (w *sweepWorld) Title(context.Context, kernel.TitleID) (catalog.Title, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.title
	t.Sources = append([]catalog.Source(nil), w.title.Sources...)
	return t, nil
}
func (w *sweepWorld) Episode(context.Context, kernel.EpisodeID) (catalog.Episode, error) {
	return catalog.Episode{}, kernel.ErrNotFound
}
func (w *sweepWorld) Episodes(context.Context, kernel.TitleID) ([]catalog.Episode, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []catalog.Episode
	for k := range w.stored {
		var s, e int
		fmt.Sscanf(k, "%d:%d", &s, &e)
		out = append(out, catalog.Episode{ID: kernel.EpisodeID(s*1000 + e), SeasonNo: s, EpisodeNo: e,
			Thumbnail: "x", TitleKnown: true})
	}
	return out, nil
}

// EpisodeWriter
func (w *sweepWorld) UpsertEpisodes(_ context.Context, _ kernel.TitleID, eps []dp.Episode) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, e := range eps {
		w.stored[fmt.Sprintf("%d:%d", e.SeasonNo, e.EpisodeNo)] = true
	}
	return nil
}

// TitleWriter — склейка.
func (w *sweepWorld) UpsertTitle(_ context.Context, ft dp.Title) (kernel.TitleID, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !strings.HasPrefix(ft.Ref.ItemID, "same") {
		w.others++
		return 9000 + kernel.TitleID(w.others), nil
	}
	for _, s := range w.title.Sources {
		if s.Ref == ft.Ref {
			return w.title.ID, nil
		}
	}
	w.title.Sources = append(w.title.Sources, catalog.Source{Ref: ft.Ref, Priority: 10})
	return w.title.ID, nil
}

// EpisodeSource — серии площадки: 1..N первого сезона.
type sweepSource struct {
	w  *sweepWorld
	id string
}

func (s sweepSource) Episodes(_ context.Context, ref kernel.ProviderRef) ([]dp.Episode, error) {
	s.w.mu.Lock()
	n, down := s.w.episodes[s.id], s.w.down[s.id]
	s.w.fetched = append(s.w.fetched, s.id)
	s.w.mu.Unlock()
	defer func() { s.w.fetchedCh <- s.id }()
	if down {
		return nil, kernel.ErrUnreachable
	}
	var out []dp.Episode
	for i := 1; i <= n; i++ {
		out = append(out, dp.Episode{Ref: ref, SeasonNo: 1, EpisodeNo: i})
	}
	return out, nil
}

// SourceAsks
func (w *sweepWorld) SourcesAsked(context.Context, kernel.TitleID) (map[string]time.Time, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string]time.Time{}
	for k, v := range w.asks {
		out[k] = v
	}
	return out, nil
}
func (w *sweepWorld) MarkSourcesAsked(_ context.Context, _ kernel.TitleID, ps []string, at time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, p := range ps {
		w.asks[p] = at
	}
	return nil
}

func (w *sweepWorld) search(_ context.Context, id, q string, _ int) ([]dp.Title, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.searches = append(w.searches, id+":"+q)
	if w.down[id] {
		return nil, kernel.ErrUnreachable
	}
	return w.found[id], nil
}

func (w *sweepWorld) lookupFn(_ context.Context, id string, _ dp.ExternalIDs) (dp.Title, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lookups = append(w.lookups, id)
	if w.down[id] {
		return dp.Title{}, kernel.ErrUnreachable
	}
	ft, ok := w.lookup[id]
	if !ok {
		return dp.Title{}, kernel.ErrNotFound
	}
	return ft, nil
}

func (w *sweepWorld) log() (searches, lookups []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := append([]string(nil), w.searches...)
	l := append([]string(nil), w.lookups...)
	sort.Strings(s)
	sort.Strings(l)
	return s, l
}

func (w *sweepWorld) sources() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, s := range w.title.Sources {
		out = append(out, s.Ref.ProviderID)
	}
	sort.Strings(out)
	return out
}

// sweepCatalog — каталог с досбором поверх мира; providers — кто включён сейчас.
func sweepCatalog(w *sweepWorld, auto bool, providers *[]string, lookups ...string) *LazyCatalog {
	return NewLazyCatalog(w, w, func(id string) (EpisodeSource, error) { return sweepSource{w, id}, nil }).
		WithLiveSearch(w, nil).
		WithSourceSweep(Sweep{
			Search: w.search,
			Lookup: w.lookupFn,
			Providers: func() ([]string, []string) {
				return append([]string(nil), *providers...), lookups
			},
			Asks: w,
			Auto: auto,
		})
}

func frieren() catalog.Title {
	return catalog.Title{ID: 7, Kind: catalog.KindSeries, Title: "Провожающая в последний путь Фрирен",
		Original: "Sousou no Frieren", Genres: []string{"Аниме"},
		Sources: []catalog.Source{{Ref: kernel.ProviderRef{ProviderID: "anilibria", ItemID: "same-1"}, Priority: 100}}}
}

func waitFetched(t *testing.T, w *sweepWorld, id string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-w.fetchedCh:
			if got == id {
				return
			}
		case <-deadline:
			t.Fatalf("серии площадки %s так и не запрошены", id)
		}
	}
}

// Режим «все площадки»: тайтл, пришедший с одной площадки, при открытии
// спрашивает остальные включённые — и только их, и только один раз; включённая
// позже площадка спрашивается при следующем открытии.
func TestSweep_AutoAsksMissingSourcesOnce(t *testing.T) {
	w := newSweepWorld(frieren())
	w.found["animego_me"] = []dp.Title{{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "same-ag"}},
		{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "other-show"}}}
	w.episodes["anilibria"], w.episodes["animego_me"] = 10, 12
	w.stored["1:1"] = true
	enabled := []string{"anilibria", "animego_me", "yummy"}
	c := sweepCatalog(w, true, &enabled)

	if _, err := c.Episodes(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	waitFetched(t, w, "animego_me")
	if got := w.sources(); strings.Join(got, ",") != "anilibria,animego_me" {
		t.Fatalf("площадки тайтла %v, ждали anilibria и animego_me", got)
	}
	s, _ := w.log()
	// По оригиналу — обе; по-русски — только yummy (animego_me уже нашлась).
	want := []string{"animego_me:Sousou no Frieren", "yummy:Sousou no Frieren", "yummy:Провожающая в последний путь Фрирен"}
	if strings.Join(s, "|") != strings.Join(want, "|") {
		t.Fatalf("спросили %v,\nждали %v", s, want)
	}
	if _, ok := w.asks["anilibria"]; ok {
		t.Fatal("свою площадку тайтла спрашивать незачем")
	}
	if len(w.asks) != 2 {
		t.Fatalf("помечены спрошенными %v", w.asks)
	}

	// Повторное открытие (в том числе новым процессом — память в «базе»): никого.
	c2 := sweepCatalog(w, true, &enabled)
	_, _ = c2.Episodes(context.Background(), 7)
	time.Sleep(30 * time.Millisecond)
	if s2, _ := w.log(); len(s2) != len(s) {
		t.Fatalf("досбор повторился: %v", s2)
	}

	// Человек включил ещё площадку — спрашивается только она.
	enabled = append(enabled, "jutsu_net")
	c3 := sweepCatalog(w, true, &enabled)
	_, _ = c3.Episodes(context.Background(), 7)
	deadline := time.Now().Add(2 * time.Second)
	for {
		s3, _ := w.log()
		if len(s3) == len(s)+2 {
			if !strings.Contains(strings.Join(s3, "|"), "jutsu_net:Sousou no Frieren") {
				t.Fatalf("новая площадка не спрошена: %v", s3)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("новая площадка не спрошена: %v", s3)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Бережный режим: открытие тайтла площадки не спрашивает — только «Обновить».
// «Обновить» привязывает недостающие площадки, перечитывает серии всех и
// не повторяется чаще раза в минуту.
func TestSweep_RefreshOnlyByButtonInFirstMode(t *testing.T) {
	w := newSweepWorld(frieren())
	w.found["animego_me"] = []dp.Title{{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "same-ag"}}}
	w.episodes["anilibria"], w.episodes["animego_me"] = 10, 12
	w.stored["1:1"] = true
	enabled := []string{"anilibria", "animego_me"}
	c := sweepCatalog(w, false, &enabled)

	if _, err := c.Episodes(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if s, _ := w.log(); len(s) != 0 {
		t.Fatalf("в бережном режиме открытие спросило площадки: %v", s)
	}

	rep, err := c.Refresh(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rep.Attached, ",") != "animego_me" {
		t.Fatalf("привязались %v", rep.Attached)
	}
	if strings.Join(rep.Refetched, ",") != "anilibria,animego_me" {
		t.Fatalf("серии перечитаны у %v, ждали у обеих", rep.Refetched)
	}
	if rep.EpisodesBefore != 1 || rep.EpisodesAfter != 12 {
		t.Fatalf("серий было %d стало %d, ждали 1 → 12", rep.EpisodesBefore, rep.EpisodesAfter)
	}

	again, err := c.Refresh(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if again.Wait <= 0 || len(again.Asked) != 0 {
		t.Fatalf("повтор через миг ушёл к площадкам: %+v", again)
	}
}

// «Обновить» спрашивает и тех, кто уже ответил «нет» (срок памяти не ждёт), но
// не тех, кого спросили только что.
func TestSweep_RefreshIgnoresRepeatButNotLastMinute(t *testing.T) {
	w := newSweepWorld(frieren())
	enabled := []string{"anilibria", "yummy", "animego_fr"}
	w.asks["yummy"] = time.Now().Add(-48 * time.Hour)       // спрошена давно — в пределах недели
	w.asks["animego_fr"] = time.Now().Add(-5 * time.Second) // только что, автодосбором
	c := sweepCatalog(w, false, &enabled)
	if _, err := c.Refresh(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	s, _ := w.log()
	joined := strings.Join(s, "|")
	if !strings.Contains(joined, "yummy:") || strings.Contains(joined, "animego_fr:") {
		t.Fatalf("спросили %v: ждали yummy и не animego_fr", s)
	}
}

// Площадка, не ответившая (нет сети), не помечается спрошенной: её спросят при
// следующем открытии. Ответившая пустотой — помечается.
func TestSweep_FailedSourceIsAskedAgain(t *testing.T) {
	w := newSweepWorld(frieren())
	w.down["animego_me"] = true
	enabled := []string{"anilibria", "animego_me", "yummy"}
	c := sweepCatalog(w, false, &enabled)
	rep, err := c.Refresh(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rep.Failed, ",") != "animego_me" || strings.Join(rep.Asked, ",") != "yummy" {
		t.Fatalf("отчёт %+v", rep.SweepReport)
	}
	if _, ok := w.asks["animego_me"]; ok {
		t.Fatal("не ответившая площадка помечена спрошенной")
	}
	// Лежащей площадке не шлём второй запрос (по-русски) в том же досборе.
	s, _ := w.log()
	n := 0
	for _, q := range s {
		if strings.HasPrefix(q, "animego_me:") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("лежащую площадку спросили %d раз: %v", n, s)
	}

	// Ожила — автодосбор её спросит.
	w.mu.Lock()
	w.down["animego_me"] = false
	w.found["animego_me"] = []dp.Title{{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "same-ag"}}}
	w.mu.Unlock()
	w.episodes["animego_me"] = 3
	auto := sweepCatalog(w, true, &enabled)
	_, _ = auto.Episodes(context.Background(), 7)
	waitFetched(t, w, "animego_me")
	if got := w.sources(); strings.Join(got, ",") != "anilibria,animego_me" {
		t.Fatalf("площадки %v", got)
	}
}

// Площадка с поиском по номерам спрашивается по номерам (точно, один запрос), а не
// по названию; находка с чужим номером не привязывается.
func TestSweep_LookupByIDsWhenTitleHasThem(t *testing.T) {
	ti := catalog.Title{ID: 7, Kind: catalog.KindSeries, Title: "Тьма / Dark", Year: 2017,
		IDs:     catalog.ExternalIDs{Kinopoisk: "1020941", IMDB: "tt5753856"},
		Sources: []catalog.Source{{Ref: kernel.ProviderRef{ProviderID: "kinopub", ItemID: "same-kp"}, Priority: 100}}}
	w := newSweepWorld(ti)
	w.lookup["lordfilm"] = dp.Title{Ref: kernel.ProviderRef{ProviderID: "lordfilm", ItemID: "same-lf"},
		Kinopoisk: "1020941"}
	w.lookup["cvh"] = dp.Title{Ref: kernel.ProviderRef{ProviderID: "cvh", ItemID: "same-wrong"},
		Kinopoisk: "1020941", IMDB: "tt0000001"}
	enabled := []string{"kinopub", "lordfilm", "filmix"}
	c := sweepCatalog(w, false, &enabled, "lordfilm", "cvh")
	rep, err := c.Refresh(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rep.Attached, ",") != "lordfilm" {
		t.Fatalf("привязались %v, ждали только lordfilm", rep.Attached)
	}
	s, l := w.log()
	if strings.Join(l, ",") != "cvh,lordfilm" {
		t.Fatalf("по номерам спросили %v", l)
	}
	// filmix — по названию (оригинал, затем русское); lordfilm по названию не спрашивался.
	if strings.Join(s, "|") != "filmix:Dark|filmix:Тьма" {
		t.Fatalf("по названию спросили %v", s)
	}
}

// Досбор и «Обновить» на одном тайтле не идут одновременно: второй ждёт первого и
// не спрашивает тех, кого тот только что спросил.
func TestSweep_RefreshWaitsForRunningAutoSweep(t *testing.T) {
	w := newSweepWorld(frieren())
	enabled := []string{"anilibria", "yummy"}
	gate := make(chan struct{})
	c := NewLazyCatalog(w, w, func(id string) (EpisodeSource, error) { return sweepSource{w, id}, nil }).
		WithLiveSearch(w, nil).
		WithSourceSweep(Sweep{
			Search: func(ctx context.Context, id, q string, limit int) ([]dp.Title, error) {
				<-gate
				return w.search(ctx, id, q, limit)
			},
			Providers: func() ([]string, []string) { return enabled, nil },
			Asks:      w, Auto: true,
		})
	_, _ = c.Episodes(context.Background(), 7) // автодосбор повис на площадке
	done := make(chan struct{})
	go func() {
		_, _ = c.Refresh(context.Background(), 7)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	close(gate)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("«Обновить» не дождалось досбора")
	}
	s, _ := w.log()
	// yummy спрошена автодосбором (по двум названиям) и больше — нет.
	if len(s) != 2 {
		t.Fatalf("спросили %v: «Обновить» повторило запросы досбора", s)
	}
}

// Память поиска зависит от набора площадок: тот же запрос, «спрошенный» при
// другом наборе, уходит снова.
func TestSearchScope_NewSourcesAskAgain(t *testing.T) {
	mem := newMemSearch()
	calls := 0
	live := func(context.Context, string, int) ([]dp.Title, error) { calls++; return nil, nil }
	newCat := func(scope string) *LazyCatalog {
		return NewLazyCatalog(emptyReader(), nil, nil).WithLiveSearch(&fakeTitles{}, live).
			WithSearchMemory(mem, 7*24*time.Hour).WithSearchScope(scope)
	}
	_, _ = newCat("first:anilibria").Search(context.Background(), "Фрирен", 10, 0)
	_, _ = newCat("first:anilibria").Search(context.Background(), "Фрирен", 10, 0)
	if calls != 1 {
		t.Fatalf("тот же набор: спросили %d раз, ждали 1", calls)
	}
	_, _ = newCat("first:anilibria,animego_me").Search(context.Background(), "Фрирен", 10, 0)
	if calls != 2 {
		t.Fatalf("новый набор площадок: спросили %d раз, ждали 2", calls)
	}
}
