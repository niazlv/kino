package catalog

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

type fakeReader struct {
	local  []catalog.Title
	titles map[kernel.TitleID]catalog.Title
}

func (f *fakeReader) Search(context.Context, string, int, int) ([]catalog.Title, int, error) {
	return f.local, len(f.local), nil
}

func (f *fakeReader) Title(_ context.Context, id kernel.TitleID) (catalog.Title, error) {
	t, ok := f.titles[id]
	if !ok {
		return catalog.Title{}, kernel.ErrNotFound
	}
	return t, nil
}
func (f *fakeReader) Episode(context.Context, kernel.EpisodeID) (catalog.Episode, error) {
	return catalog.Episode{}, kernel.ErrNotFound
}
func (f *fakeReader) Episodes(context.Context, kernel.TitleID) ([]catalog.Episode, error) {
	return nil, nil
}

type fakeTitles struct {
	next kernel.TitleID
	seen []string
}

func (f *fakeTitles) UpsertTitle(_ context.Context, t dp.Title) (kernel.TitleID, error) {
	f.next++
	f.seen = append(f.seen, t.Title)
	return f.next, nil
}

func TestLazySearch_FallsBackToProviderOnlyWhenLocalIsEmpty(t *testing.T) {
	r := &fakeReader{titles: map[kernel.TitleID]catalog.Title{
		1: {ID: 1, Title: "Табакошка"},
	}}
	w := &fakeTitles{}
	calls := 0
	live := func(context.Context, string, int) ([]dp.Title, error) {
		calls++
		return []dp.Title{{Title: "Табакошка"}}, nil
	}
	c := NewLazyCatalog(r, nil, nil).WithLiveSearch(w, live)

	got, err := c.Search(context.Background(), "табак", 10, 0)
	if err != nil || len(got.Items) != 1 || got.Items[0].ID != 1 || got.Total != 1 {
		t.Fatalf("ждали найденный на площадке тайтл с нашим id: %+v %v", got, err)
	}
	if calls != 1 || len(w.seen) != 1 {
		t.Errorf("площадку спросили %d раз, записали %d — ждали по одному", calls, len(w.seen))
	}

	// Есть локальный результат — площадку не трогаем: это общий с просмотром лимит.
	r.local = []catalog.Title{{ID: 7, Title: "Табакошка (наша)"}}
	got, _ = c.Search(context.Background(), "табак", 10, 0)
	if calls != 1 || len(got.Items) != 1 || got.Items[0].ID != 7 {
		t.Errorf("при непустом локальном ответе живой поиск не должен звучать: calls=%d got=%v", calls, got.Items)
	}
}

// Подсказки летят на каждую букву: площадку они не спрашивают никогда,
// даже когда у нас пусто, — иначе «д», «дю», «дюн» тратили бы общий с
// просмотром лимит kinopub.
func TestSuggest_NeverAsksProviders(t *testing.T) {
	r := &fakeReader{titles: map[kernel.TitleID]catalog.Title{}}
	calls := 0
	live := func(context.Context, string, int) ([]dp.Title, error) {
		calls++
		return []dp.Title{{Title: "Дюна"}}, nil
	}
	c := NewLazyCatalog(r, nil, nil).WithLiveSearch(&fakeTitles{}, live)

	got, err := c.Suggest(context.Background(), "дюн", 6)
	if err != nil || len(got) != 0 || calls != 0 {
		t.Fatalf("подсказки: %v, %v, площадку спросили %d раз", got, err, calls)
	}
	r.local = []catalog.Title{{ID: 3, Title: "Дюна"}}
	got, _ = c.Suggest(context.Background(), "дюн", 6)
	if len(got) != 1 || got[0].ID != 3 || calls != 0 {
		t.Errorf("подсказки из каталога: %v, площадку спросили %d раз", got, calls)
	}
}

func TestLazySearch_ProviderFailureIsNotAnError(t *testing.T) {
	r := &fakeReader{titles: map[kernel.TitleID]catalog.Title{}}
	live := func(context.Context, string, int) ([]dp.Title, error) {
		return nil, errors.New("kinopub: rate limited")
	}
	c := NewLazyCatalog(r, nil, nil).WithLiveSearch(&fakeTitles{}, live)
	got, err := c.Search(context.Background(), "что-то", 10, 0)
	if err != nil || len(got.Items) != 0 || got.Total != 0 {
		t.Errorf("отказ площадки — пустой результат, а не ошибка: %+v %v", got, err)
	}
	// Без живого поиска поведение прежнее.
	plain := NewLazyCatalog(r, nil, nil)
	if got, err := plain.Search(context.Background(), "что-то", 10, 0); err != nil || len(got.Items) != 0 {
		t.Errorf("без живого поиска — просто локальный результат: %+v %v", got, err)
	}
}

// Вторая страница не спрашивает площадки: живой поиск — про «совсем нет
// подходящего» при первом заходе, а листание — про то, что уже найдено;
// находки вдобавок сдвигали бы окно, и человек потерял бы несколько карточек.
// NextOffset при этом продолжает окно каталога, а не «сколько показано»:
// находки первой страницы в каталоге не сидели.
func TestLazySearch_SecondPageSkipsProviders(t *testing.T) {
	r := &fakeReader{titles: map[kernel.TitleID]catalog.Title{}}
	// Локальная находка не по запросу: первая страница должна спросить площадки.
	r.local = []catalog.Title{{ID: 3, Title: "Другое"}}
	w := &fakeTitles{}
	calls := 0
	live := func(context.Context, string, int) ([]dp.Title, error) {
		calls++
		return nil, nil
	}
	c := NewLazyCatalog(r, nil, nil).WithLiveSearch(w, live)

	// Первая страница пуста — площадки спрошены, но ничего не нашли.
	if _, err := c.Search(context.Background(), "что-то", 10, 0); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("на первой странице площадки не спрошены: calls=%d", calls)
	}
	// Вторая — локальное окно без живого поиска.
	page, err := c.Search(context.Background(), "что-то", 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("площадки спрошены на второй странице: calls=%d", calls)
	}
	// Окно каталога целиком: фейк отдаёт всё тем же ответом.
	if page.Next != 10+len(r.local) {
		t.Fatalf("next=%d, ждали смещение каталога", page.Next)
	}
}

type episodesReader struct {
	fakeReader
	title catalog.Title
	eps   []catalog.Episode
}

func (r *episodesReader) Title(context.Context, kernel.TitleID) (catalog.Title, error) {
	return r.title, nil
}
func (r *episodesReader) Episodes(context.Context, kernel.TitleID) ([]catalog.Episode, error) {
	return r.eps, nil
}

type recordingWriter struct{ got []dp.Episode }

func (w *recordingWriter) UpsertEpisodes(_ context.Context, _ kernel.TitleID, eps []dp.Episode) error {
	w.got = append(w.got, eps...)
	return nil
}

type fixedStats map[kernel.ProviderRef]OfferStat

func (s fixedStats) OfferStats(context.Context, kernel.TitleID) (map[kernel.ProviderRef]OfferStat, error) {
	return s, nil
}

type episodesFrom struct {
	calls *[]string
}

func (e episodesFrom) Episodes(_ context.Context, ref kernel.ProviderRef) ([]dp.Episode, error) {
	*e.calls = append(*e.calls, ref.ProviderID)
	return []dp.Episode{{Ref: ref, SeasonNo: 1, EpisodeNo: 1}}, nil
}

// Площадка, привязанная к тайтлу позже (аниме-площадка к kinopub-тайтлу),
// должна отдать свои серии при открытии, хотя у лучшей они уже есть.
func TestLazyEpisodes_FetchesNewlyAttachedSource(t *testing.T) {
	kp := kernel.ProviderRef{ProviderID: "kinopub", ItemID: "38219"}
	ag := kernel.ProviderRef{ProviderID: "animego_me", ItemID: "princessa-shpionka-421"}
	off := kernel.ProviderRef{ProviderID: "animego_fr", ItemID: "1"}
	r := &episodesReader{
		title: catalog.Title{ID: 1, Finished: true, Sources: []catalog.Source{
			{Ref: kp, Priority: 100}, {Ref: ag, Priority: 60, SeasonNo: 1}, {Ref: off, Priority: 50, Disabled: true},
		}},
		eps: []catalog.Episode{{ID: 1, SeasonNo: 1, EpisodeNo: 1, Thumbnail: "x", TitleKnown: true}},
	}
	w := &recordingWriter{}
	var calls []string
	c := NewLazyCatalog(r, w, func(string) (EpisodeSource, error) { return episodesFrom{&calls}, nil }).
		WithOfferStats(fixedStats{kp: {Episodes: 12, SeenAt: time.Now()}})

	if _, err := c.Episodes(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "animego_me" {
		t.Fatalf("спрошены площадки %v, ожидалась только animego_me (kinopub свежий, animego_fr выключен)", calls)
	}
	// Площадка не отдала серий — повторно при следующем открытии не спрашиваем.
	calls = nil
	c2 := NewLazyCatalog(r, w, func(string) (EpisodeSource, error) { return emptySource{&calls}, nil }).
		WithOfferStats(fixedStats{kp: {Episodes: 12, SeenAt: time.Now()}})
	_, _ = c2.Episodes(context.Background(), 1)
	_, _ = c2.Episodes(context.Background(), 1)
	if len(calls) != 1 {
		t.Fatalf("пустая площадка спрошена %d раз, ожидался 1", len(calls))
	}
}

type emptySource struct{ calls *[]string }

func (e emptySource) Episodes(_ context.Context, ref kernel.ProviderRef) ([]dp.Episode, error) {
	*e.calls = append(*e.calls, ref.ProviderID)
	return nil, nil
}

// Нашлось у нас, но не то: на «Принцесса Медуза» — только другая принцесса.
// Площадки спрашиваются, найденное идёт первым; повтор того же запроса
// через минуту площадки не трогает.
func TestLazySearch_AsksProvidersWhenLocalIsIrrelevant(t *testing.T) {
	r := &fakeReader{
		local:  []catalog.Title{{ID: 7, Title: "Принцесса-шпионка / Princess Principal"}},
		titles: map[kernel.TitleID]catalog.Title{1: {ID: 1, Title: "Принцесса-медуза / Kuragehime"}},
	}
	w := &fakeTitles{}
	calls := 0
	live := func(context.Context, string, int) ([]dp.Title, error) {
		calls++
		return []dp.Title{{Title: "Принцесса-медуза / Kuragehime"}}, nil
	}
	c := NewLazyCatalog(r, nil, nil).WithLiveSearch(w, live)

	got, _ := c.Search(context.Background(), "Принцесса Медуза", 10, 0)
	if calls != 1 || len(got.Items) != 2 || got.Items[0].ID != 1 || got.Items[1].ID != 7 {
		t.Fatalf("calls=%d got=%v", calls, got.Items)
	}
	_, _ = c.Search(context.Background(), "принцесса  медуза", 10, 0)
	if calls != 1 {
		t.Fatalf("тот же запрос повторно ушёл на площадки: calls=%d", calls)
	}
	// Дефис и пробел — одно и то же: такой локальный ответ уже подходящий.
	if !relevant([]catalog.Title{{Title: "Принцесса-медуза / Kuragehime"}}, "принцесса медуза") {
		t.Fatal("нормализация не свела дефис к пробелу")
	}
}

type attachingTitles struct {
	mu    sync.Mutex
	to    kernel.TitleID
	calls int
}

func (a *attachingTitles) UpsertTitle(context.Context, dp.Title) (kernel.TitleID, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return a.to, nil
}

func (a *attachingTitles) attached() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls > 0
}

// syncLog — журнал вызовов из фонового досбора.
type syncLog struct {
	mu    sync.Mutex
	items []string
	wrote chan struct{}
}

func (l *syncLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, s)
}

func (l *syncLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.items...)
}

type signalWriter struct{ log *syncLog }

func (w signalWriter) UpsertEpisodes(_ context.Context, _ kernel.TitleID, eps []dp.Episode) error {
	w.log.add(eps[0].Ref.ProviderID)
	select {
	case w.log.wrote <- struct{}{}:
	default:
	}
	return nil
}

type loggedSource struct{ log *syncLog }

func (e loggedSource) Episodes(_ context.Context, ref kernel.ProviderRef) ([]dp.Episode, error) {
	return []dp.Episode{{Ref: ref, SeasonNo: 1, EpisodeNo: 1}}, nil
}

// Аниме-тайтл kinopub без аниме-площадок при открытии досбирает их в фоне
// по оригинальному названию (и по-русски, если нашлись не все), вместе с
// сериями найденных.
func TestLazyEpisodes_DiscoversAnimeSources(t *testing.T) {
	kp := kernel.ProviderRef{ProviderID: "kinopub", ItemID: "21267"}
	ag := kernel.ProviderRef{ProviderID: "animego_me", ItemID: "princessa-shpionka-421"}
	titles := &attachingTitles{to: 38219}
	r := &discoverReader{
		before: catalog.Title{ID: 38219, Title: "Принцесса-шпионка / Princess Principal", Genres: []string{"Аниме"},
			Finished: true, Sources: []catalog.Source{{Ref: kp, Priority: 100}}},
		eps:      []catalog.Episode{{ID: 1, SeasonNo: 1, EpisodeNo: 1, Thumbnail: "x", TitleKnown: true}},
		attached: titles.attached,
	}
	r.after = r.before
	r.after.Sources = append(r.after.Sources, catalog.Source{Ref: ag, Priority: 60, SeasonNo: 1})

	queries := &syncLog{}
	discovery := func(_ context.Context, q string, _ int) ([]dp.Title, error) {
		queries.add(q)
		return []dp.Title{{Ref: ag}}, nil
	}
	written := &syncLog{wrote: make(chan struct{}, 1)}
	c := NewLazyCatalog(r, signalWriter{written}, func(string) (EpisodeSource, error) { return loggedSource{written}, nil }).
		WithLiveSearch(titles, nil).
		WithOfferStats(fixedStats{kp: {Episodes: 12, SeenAt: time.Now()}}).
		WithDiscovery(discovery, nil, "animego_me", "animego_fr")

	if _, err := c.Episodes(context.Background(), 38219); err != nil {
		t.Fatal(err)
	}
	select {
	case <-written.wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("досбор не подтянул серии найденной площадки")
	}
	// Нашлась одна площадка из двух — вторая спрошена ещё и по-русски.
	if q := queries.all(); len(q) != 2 || q[0] != "Princess Principal" || q[1] != "Принцесса-шпионка" {
		t.Fatalf("спросили %v, ожидались оригинал и русское название", q)
	}
	if w := written.all(); len(w) != 1 || w[0] != "animego_me" {
		t.Fatalf("записаны серии %v", w)
	}
	// Повторное открытие — площадки не спрашиваются.
	_, _ = c.Episodes(context.Background(), 38219)
	time.Sleep(20 * time.Millisecond)
	if q := queries.all(); len(q) != 2 {
		t.Fatalf("досбор повторился: %v", q)
	}

	// Не аниме — не спрашиваем вовсе.
	r2 := &discoverReader{before: catalog.Title{ID: 5, Title: "Монстр / Monster", Genres: []string{"Драма"},
		Sources: []catalog.Source{{Ref: kp, Priority: 100}}}, eps: r.eps}
	r2.after = r2.before
	other := &syncLog{}
	_, _ = NewLazyCatalog(r2, signalWriter{written}, func(string) (EpisodeSource, error) { return loggedSource{written}, nil }).
		WithLiveSearch(titles, nil).
		WithDiscovery(func(_ context.Context, q string, _ int) ([]dp.Title, error) { other.add(q); return nil, nil },
			nil, "animego_me").
		Episodes(context.Background(), 5)
	time.Sleep(20 * time.Millisecond)
	if len(other.all()) != 0 {
		t.Fatalf("не-аниме отправлено на аниме-площадки: %v", other.all())
	}
}

type discoverReader struct {
	fakeReader
	before, after catalog.Title
	eps           []catalog.Episode
	attached      func() bool
}

func (r *discoverReader) Title(context.Context, kernel.TitleID) (catalog.Title, error) {
	if r.attached != nil && r.attached() {
		return r.after, nil
	}
	return r.before, nil
}
func (r *discoverReader) Episodes(context.Context, kernel.TitleID) ([]catalog.Episode, error) {
	return r.eps, nil
}

type thumbReader struct {
	fakeReader
	ep catalog.Episode
}

func (r *thumbReader) Episode(context.Context, kernel.EpisodeID) (catalog.Episode, error) {
	return r.ep, nil
}

type thumbStore struct {
	offers []catalog.Offer
	set    map[kernel.EpisodeID]string
	r      *thumbReader
}

func (s *thumbStore) Offers(context.Context, kernel.EpisodeID) ([]catalog.Offer, error) {
	return s.offers, nil
}
func (s *thumbStore) SetEpisodeThumbnail(_ context.Context, id kernel.EpisodeID, url string) error {
	s.set[id] = url
	s.r.ep.Thumbnail = url
	return nil
}

// Кадр спрашивается у площадки один раз — дальше он в каталоге; площадка,
// у которой кадра нет, пропускается ради следующей.
func TestEpisodeThumbnail_AskedOnceAndStored(t *testing.T) {
	r := &thumbReader{ep: catalog.Episode{ID: 5, SeasonNo: 1, EpisodeNo: 3}}
	st := &thumbStore{r: r, set: map[kernel.EpisodeID]string{}, offers: []catalog.Offer{
		{Ref: kernel.ProviderRef{ProviderID: "kinopub", ItemID: "1"}, SeasonNo: 1, EpisodeNo: 3, Priority: 100},
		{Ref: kernel.ProviderRef{ProviderID: "animego_fr", ItemID: "1876"}, SeasonNo: 1, EpisodeNo: 3, Priority: 50},
	}}
	var asked []string
	src := func(_ context.Context, ref kernel.ProviderRef, _, e int) (string, error) {
		asked = append(asked, ref.ProviderID)
		if ref.ProviderID == "kinopub" {
			return "", kernel.ErrNotFound
		}
		return "https://iv.okcdn.ru/videoPreview?id=1", nil
	}
	c := NewLazyCatalog(r, nil, nil).WithThumbnails(src, st)

	u, err := c.EpisodeThumbnail(context.Background(), 5)
	if err != nil || u == "" || st.set[5] != u {
		t.Fatalf("кадр %q, сохранён %q, err %v", u, st.set[5], err)
	}
	if len(asked) != 2 || asked[1] != "animego_fr" {
		t.Fatalf("спрошены %v", asked)
	}
	if _, err := c.EpisodeThumbnail(context.Background(), 5); err != nil || len(asked) != 2 {
		t.Fatalf("сохранённый кадр снова спрошен у площадки: %v %v", asked, err)
	}

	// Не нашёлся нигде — повторно не спрашиваем до thumbRetry.
	r2 := &thumbReader{ep: catalog.Episode{ID: 6}}
	st2 := &thumbStore{r: r2, set: map[kernel.EpisodeID]string{}, offers: st.offers[:1]}
	asked = nil
	c2 := NewLazyCatalog(r2, nil, nil).WithThumbnails(src, st2)
	_, _ = c2.EpisodeThumbnail(context.Background(), 6)
	_, err = c2.EpisodeThumbnail(context.Background(), 6)
	if !errors.Is(err, kernel.ErrNotFound) || len(asked) != 1 {
		t.Fatalf("ненайденный кадр спрошен %d раз, err %v", len(asked), err)
	}
}

type marksRecorder struct {
	mu     sync.Mutex
	marked []kernel.TitleID
}

func (m *marksRecorder) MarkSourcesChecked(_ context.Context, id kernel.TitleID, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.marked = append(m.marked, id)
	return nil
}

// Тайтл с одной аниме-площадкой открывается сразу: недостающая ищется в
// фоне (чаще всего её там просто нет — ждать отказа по восемь секунд
// незачем). Отметка в базе не даёт искать снова после перезапуска.
func TestLazyEpisodes_DiscoveryDoesNotBlockWhenAnimeSourceExists(t *testing.T) {
	fr := kernel.ProviderRef{ProviderID: "animego_fr", ItemID: "1876"}
	r := &discoverReader{
		before: catalog.Title{ID: 53018, Title: "Принцесса-медуза / Kuragehime", Genres: []string{"Аниме"},
			Finished: true, Sources: []catalog.Source{{Ref: fr, Priority: 50, SeasonNo: 1}}},
		eps: []catalog.Episode{{ID: 1, SeasonNo: 1, EpisodeNo: 1, Thumbnail: "x", TitleKnown: true}},
	}
	r.after = r.before
	release := make(chan struct{})
	slow := func(ctx context.Context, _ string, _ int) ([]dp.Title, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}
	marks := &marksRecorder{}
	c := NewLazyCatalog(r, &recordingWriter{}, nil).
		WithLiveSearch(&fakeTitles{}, nil).
		WithOfferStats(fixedStats{fr: {Episodes: 11, SeenAt: time.Now()}}).
		WithDiscovery(slow, marks, "animego_me", "animego_fr")

	start := time.Now()
	if _, err := c.Episodes(context.Background(), 53018); err != nil {
		t.Fatal(err)
	}
	close(release)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("страница ждала досбор %s", d)
	}
	if len(marks.marked) != 1 {
		t.Fatalf("отметка о досборе не записана: %v", marks.marked)
	}

	// Недавно искали (отметка из базы) — не ищем, даже в новом процессе.
	r.before.SourcesCheckedAt = time.Now().Add(-time.Hour)
	r.after = r.before
	called := &syncLog{}
	c2 := NewLazyCatalog(r, &recordingWriter{}, nil).WithLiveSearch(&fakeTitles{}, nil).
		WithOfferStats(fixedStats{fr: {Episodes: 11, SeenAt: time.Now()}}).
		WithDiscovery(func(_ context.Context, q string, _ int) ([]dp.Title, error) { called.add(q); return nil, nil },
			marks, "animego_me", "animego_fr")
	_, _ = c2.Episodes(context.Background(), 53018)
	time.Sleep(20 * time.Millisecond)
	if len(called.all()) != 0 {
		t.Fatal("досбор повторился, хотя в базе отмечено, что площадки недавно спрашивали")
	}
}

// Фильм kinopub при открытии спрашивает площадки, умеющие искать по
// номерам, — не только аниме. Находка с чужим номером не привязывается, а
// повторное открытие площадки не спрашивает.
func TestLazyEpisodes_DiscoversByExternalIDs(t *testing.T) {
	kp := kernel.ProviderRef{ProviderID: "kinopub", ItemID: "7"}
	lf := kernel.ProviderRef{ProviderID: "lordfilm", ItemID: "72d249c8bf37235f677d74abdd14db"}
	titles := &attachingTitles{to: 301}
	r := &discoverReader{
		before: catalog.Title{ID: 301, Kind: catalog.KindMovie, Title: "Матрица / The Matrix", Genres: []string{"Фантастика"},
			IDs:     catalog.ExternalIDs{IMDB: "tt0133093", Kinopoisk: "301"},
			Sources: []catalog.Source{{Ref: kp, Priority: 100}}},
		eps:      []catalog.Episode{{ID: 1, SeasonNo: 0, EpisodeNo: 1, Thumbnail: "x", TitleKnown: true}},
		attached: titles.attached,
	}
	r.after = r.before
	r.after.Sources = append(r.after.Sources, catalog.Source{Ref: lf, Priority: 40})

	written := &syncLog{wrote: make(chan struct{}, 4)}
	asked := &syncLog{}
	lookup := func(_ context.Context, ids dp.ExternalIDs, skip map[string]bool) []dp.Title {
		asked.add(ids.Kinopoisk + "|" + ids.IMDB)
		if skip["lordfilm"] {
			return nil
		}
		return []dp.Title{
			// Тот же номер Кинопоиска, но чужой IMDb — ошибка в данных площадки.
			{Ref: kernel.ProviderRef{ProviderID: "lordfilm", ItemID: "other"}, Kinopoisk: "301", IMDB: "tt9999999"},
			{Ref: lf, Kind: "movie", Kinopoisk: "301", IMDB: "tt0133093"},
		}
	}
	c := NewLazyCatalog(r, signalWriter{written}, func(string) (EpisodeSource, error) { return loggedSource{written}, nil }).
		WithLiveSearch(titles, nil).
		WithIDLookup(lookup, "lordfilm")

	if _, err := c.Episodes(context.Background(), 301); err != nil {
		t.Fatal(err)
	}
	select {
	case <-written.wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("досбор по номерам не подтянул серии")
	}
	if a := asked.all(); len(a) != 1 || a[0] != "301|tt0133093" {
		t.Fatalf("спросили %v", a)
	}
	titles.mu.Lock()
	calls := titles.calls
	titles.mu.Unlock()
	if calls != 1 {
		t.Fatalf("в каталог положено %d находок, ждали одну — без противоречащей", calls)
	}
	if w := written.all(); len(w) != 1 || w[0] != "lordfilm" {
		t.Fatalf("записаны серии %v", w)
	}

	// Площадка уже есть — не спрашиваем.
	_, _ = c.Episodes(context.Background(), 301)
	time.Sleep(20 * time.Millisecond)
	if a := asked.all(); len(a) != 1 {
		t.Fatalf("досбор повторился: %v", a)
	}

	// Без номеров спрашивать нечем.
	r2 := &discoverReader{before: catalog.Title{ID: 9, Title: "Без номеров", Sources: []catalog.Source{{Ref: kp}}}, eps: r.eps}
	r2.after = r2.before
	_, _ = NewLazyCatalog(r2, signalWriter{written}, nil).
		WithLiveSearch(titles, nil).
		WithIDLookup(lookup, "lordfilm").
		Episodes(context.Background(), 9)
	time.Sleep(20 * time.Millisecond)
	if a := asked.all(); len(a) != 1 {
		t.Fatalf("спросили без номеров: %v", a)
	}
}

func TestSameWork(t *testing.T) {
	ids := dp.ExternalIDs{IMDB: "tt1", Kinopoisk: "10"}
	cases := []struct {
		ft   dp.Title
		want bool
	}{
		{dp.Title{IMDB: "tt1", Kinopoisk: "10"}, true},
		{dp.Title{Kinopoisk: "10"}, true},
		{dp.Title{IMDB: "tt1"}, true},
		{dp.Title{IMDB: "tt2", Kinopoisk: "10"}, false},
		{dp.Title{IMDB: "tt1", Kinopoisk: "11"}, false},
		{dp.Title{}, false},
	}
	for _, c := range cases {
		if got := sameWork(ids, c.ft); got != c.want {
			t.Errorf("%+v: %v", c.ft, got)
		}
	}
	// У тайтла только IMDb — номер Кинопоиска находки ничему не противоречит.
	if !sameWork(dp.ExternalIDs{IMDB: "tt1"}, dp.Title{IMDB: "tt1", Kinopoisk: "10"}) {
		t.Error("IMDb совпал, Кинопоиск у тайтла неизвестен")
	}
}

// namedTitles кладёт находку под заранее известным id — по названию.
type namedTitles struct {
	mu    sync.Mutex
	ids   map[string]kernel.TitleID
	calls int
}

func (n *namedTitles) UpsertTitle(_ context.Context, t dp.Title) (kernel.TitleID, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	return n.ids[t.Title], nil
}

func (n *namedTitles) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

// Страница витрины перезагрузилась, пока площадки искали: находки всё
// равно ложатся в каталог, а не теряются вместе с соединением (так на
// проде не нашлась «Эта чёртова любовь»).
func TestLazySearch_SurvivesClientLeaving(t *testing.T) {
	r := &fakeReader{titles: map[kernel.TitleID]catalog.Title{7: {ID: 7, Title: "Эта чёртова любовь / Ireon yeot gateun sarang"}}}
	tw := &namedTitles{ids: map[string]kernel.TitleID{"Эта чёртова любовь": 7}}
	release := make(chan struct{})
	var asked atomic.Int32
	live := func(ctx context.Context, _ string, _ int) ([]dp.Title, error) {
		asked.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []dp.Title{{Title: "Эта чёртова любовь"}}, nil
	}
	c := NewLazyCatalog(r, nil, nil).WithLiveSearch(tw, live)

	gone, cancel := context.WithCancel(context.Background())
	res := make(chan SearchPage, 1)
	go func() { p, _ := c.Search(gone, "Эта чёртова любовь", 30, 0); res <- p }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if p := <-res; len(p.Items) != 0 {
		t.Fatalf("ушедшему клиенту: %+v", p.Items)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for tw.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if tw.count() != 1 {
		t.Fatal("находка не легла в каталог после ухода клиента")
	}
	// Следующий запрос площадки не спрашивает, но находку отдаёт.
	p, err := c.Search(context.Background(), "Эта чёртова любовь", 30, 0)
	if err != nil || len(p.Items) != 1 || p.Items[0].ID != 7 || asked.Load() != 1 {
		t.Fatalf("повтор: %v %+v, походов %d", err, p.Items, asked.Load())
	}
}

// Два запроса с одной строкой почти разом (витрина загрузилась дважды):
// площадки спрашиваются один раз, и второй получает те же находки, а не
// пустоту.
func TestLazySearch_ConcurrentSameQueryShareResults(t *testing.T) {
	r := &fakeReader{titles: map[kernel.TitleID]catalog.Title{7: {ID: 7, Title: "Эта чёртова любовь"}}}
	tw := &namedTitles{ids: map[string]kernel.TitleID{"Эта чёртова любовь": 7}}
	release := make(chan struct{})
	var asked atomic.Int32
	live := func(context.Context, string, int) ([]dp.Title, error) {
		asked.Add(1)
		<-release
		return []dp.Title{{Title: "Эта чёртова любовь"}}, nil
	}
	c := NewLazyCatalog(r, nil, nil).WithLiveSearch(tw, live)

	res := make(chan SearchPage, 2)
	for range 2 {
		go func() { p, _ := c.Search(context.Background(), "эта чертова любовь", 30, 0); res <- p }()
	}
	time.Sleep(30 * time.Millisecond)
	close(release)
	for range 2 {
		if p := <-res; len(p.Items) != 1 || p.Items[0].ID != 7 {
			t.Fatalf("ответ: %+v", p.Items)
		}
	}
	if asked.Load() != 1 {
		t.Fatalf("площадки спрошены %d раз", asked.Load())
	}
}

// Находки, в чьём названии есть запрос, идут первыми, как бы ни была
// приоритетна площадка, нашедшая остальное.
func TestLazySearch_ExactNamesFirst(t *testing.T) {
	r := &fakeReader{titles: map[kernel.TitleID]catalog.Title{
		1: {ID: 1, Title: "Любовь и продюсер"},
		2: {ID: 2, Title: "Эта фарфоровая кукла влюбилась"},
		3: {ID: 3, Title: "Эта чёртова любовь / Ireon yeot gateun sarang"},
	}}
	tw := &namedTitles{ids: map[string]kernel.TitleID{"a": 1, "b": 2, "c": 3}}
	live := func(context.Context, string, int) ([]dp.Title, error) {
		return []dp.Title{{Title: "a"}, {Title: "b"}, {Title: "c"}}, nil
	}
	p, err := NewLazyCatalog(r, nil, nil).WithLiveSearch(tw, live).Search(context.Background(), "Эта чертова любовь", 30, 0)
	if err != nil || len(p.Items) != 3 || p.Items[0].ID != 3 || p.Items[1].ID != 1 {
		t.Fatalf("порядок: %v %+v", err, p.Items)
	}
}
