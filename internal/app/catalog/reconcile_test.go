package catalog

// Реконсилиация на фейковых хранилищах: решения matching.ShouldMerge
// проверяются своим пакетом, здесь — механика прохода (порядок слияний,
// пропуск уже слитых, добор MAL).

import (
	"context"
	"errors"
	"testing"

	"github.com/niazleushkin/kino/internal/app/matching"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"log/slog"
)

type fakeStore struct {
	pairs   []DuplicatePair
	titles  map[kernel.TitleID]matching.Candidate
	merged  []DuplicatePair // фактические слияния
	failOn  kernel.TitleID  // слияние «в» этот тайтл падает
	noMAL   []kernel.ProviderRef
	setMAL  map[kernel.ProviderRef]string
	refmals map[kernel.ProviderRef]string // что ответит площадка
}

func (f *fakeStore) DuplicatePairs(_ context.Context, _ int) ([]DuplicatePair, error) {
	return f.pairs, nil
}

func (f *fakeStore) Candidate(_ context.Context, id kernel.TitleID) (matching.Candidate, error) {
	c, ok := f.titles[id]
	if !ok {
		return matching.Candidate{}, errors.New("нет тайтла")
	}
	return c, nil
}

func (f *fakeStore) MergeTitlesReconciled(_ context.Context, from, into kernel.TitleID) error {
	if into == f.failOn {
		return errors.New("слияние сорвалось")
	}
	f.merged = append(f.merged, DuplicatePair{A: into, B: from})
	// У слитого тайтла больше нет своих привязок — он исчез.
	delete(f.titles, from)
	return nil
}

func (f *fakeStore) RefsWithoutMAL(_ context.Context, providerID string, _ int) ([]kernel.ProviderRef, error) {
	var out []kernel.ProviderRef
	for _, ref := range f.noMAL {
		if ref.ProviderID == providerID {
			out = append(out, ref)
		}
	}
	return out, nil
}

func (f *fakeStore) SetRefMAL(_ context.Context, ref kernel.ProviderRef, mal string) error {
	f.setMAL[ref] = mal
	return nil
}

func cand(id kernel.TitleID, year int, anime bool, keys []string, refs ...matching.Ref) matching.Candidate {
	return matching.Candidate{ID: id, Kind: "series", Year: year, Anime: anime, Keys: keys, Refs: refs}
}

// KeysForTest — ключи из matching, чтобы тест не дублировал нормализацию.
func KeysForTest(names ...string) []string { return matching.Keys(names...) }

func TestReconciler_MergesDuplicatePair(t *testing.T) {
	// Ключи — как их реально даст Keys(): точная форма + варианты.
	a := KeysForTest("Герой Юки Юна / Yuki Yuna is a Hero")
	b := KeysForTest("Герой Юна Юки / Yuuki Yuuna wa Yuusha de Aru")
	store := &fakeStore{
		pairs: []DuplicatePair{{A: 100, B: 200}},
		titles: map[kernel.TitleID]matching.Candidate{
			100: cand(100, 2014, true, a, matching.Ref{ProviderID: "animego_me", ItemID: "me-1", SeasonNo: 1}),
			200: cand(200, 2014, true, b, matching.Ref{ProviderID: "animego_fr", ItemID: "fr-1", SeasonNo: 1}),
		},
	}
	r := NewReconciler(store, store, store, store, nil, nil, slog.Default())
	n, err := r.Run(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("проход: %d слияний, err %v", n, err)
	}
	if len(store.merged) != 1 || store.merged[0] != (DuplicatePair{A: 100, B: 200}) {
		t.Fatalf("слияния: %+v", store.merged)
	}
	// После слияния дубль исчез — повторный проход тих.
	n, err = r.Run(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("повторный проход: %d слияний, err %v", n, err)
	}
}

// Пары, которые правила отклонили (год разошёлся), не сливаются.
func TestReconciler_RefusesByRules(t *testing.T) {
	store := &fakeStore{
		pairs: []DuplicatePair{{A: 1, B: 2}},
		titles: map[kernel.TitleID]matching.Candidate{
			1: cand(1, 2016, false, []string{"герой"}),
			2: cand(2, 2023, false, []string{"герой"}),
		},
	}
	r := NewReconciler(store, store, store, store, nil, nil, slog.Default())
	if n, _ := r.Run(context.Background()); n != 0 {
		t.Fatalf("однофамильцы слиты: %d", n)
	}
}

// Слияние «в» хозяина: сторона A приоритетнее, слияние B -> A.
func TestReconciler_FailedMergeKeepsGoing(t *testing.T) {
	store := &fakeStore{
		pairs:  []DuplicatePair{{A: 1, B: 2}, {A: 3, B: 4}},
		failOn: 1,
		titles: map[kernel.TitleID]matching.Candidate{
			1: cand(1, 2014, true, []string{"blue exorcist", "f:blue exorcist"}),
			2: cand(2, 2014, true, []string{"blue exorcist", "f:blue exorcist"}),
			3: cand(3, 2014, true, []string{"tol love ru", "f:tol love ru"}),
			4: cand(4, 2014, true, []string{"tol love ru", "f:tol love ru"}),
		},
	}
	r := NewReconciler(store, store, store, store, nil, nil, slog.Default())
	n, _ := r.Run(context.Background())
	if n != 1 || len(store.merged) != 1 || store.merged[0].A != 3 {
		t.Fatalf("падение одного слияния сорвало проход: n=%d %+v", n, store.merged)
	}
}

func TestReconciler_EnrichMAL(t *testing.T) {
	me := kernel.ProviderRef{ProviderID: "animego_me", ItemID: "slug-1"}
	fr := kernel.ProviderRef{ProviderID: "animego_fr", ItemID: "42"}
	store := &fakeStore{
		noMAL:  []kernel.ProviderRef{me, fr},
		setMAL: map[kernel.ProviderRef]string{},
		refmals: map[kernel.ProviderRef]string{
			me: "25519",
			fr: "", // у этой площадки номера нет
		},
	}
	titles := func(providerID string) (TitleSource, error) {
		return func(_ context.Context, ref kernel.ProviderRef) (dp.Title, error) {
			return dp.Title{MAL: store.refmals[ref]}, nil
		}, nil
	}
	r := NewReconciler(store, store, store, store, titles, []string{"animego_me", "animego_fr"}, slog.Default())
	n, err := r.EnrichMAL(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("добор: %d, err %v", n, err)
	}
	if store.setMAL[me] != "25519" {
		t.Fatalf("MAL не записан: %+v", store.setMAL)
	}
	if _, ok := store.setMAL[fr]; ok {
		t.Fatal("пустой MAL записан")
	}
}

// ── мост по названиям: реестр добирает MAL площадке, у которой его нет ──

type fakeBridge struct {
	titles    []MALTitle
	set       map[kernel.TitleID]string
	skipped   []kernel.TitleID
	failTitle kernel.TitleID
}

func (f *fakeBridge) AnimeTitlesWithoutMAL(_ context.Context, _ string, _ int) ([]MALTitle, error) {
	return f.titles, nil
}

func (f *fakeBridge) SetTitleRefsMAL(_ context.Context, id kernel.TitleID, _, mal string) error {
	if id == f.failTitle {
		return errors.New("запись не удалась")
	}
	if f.set == nil {
		f.set = map[kernel.TitleID]string{}
	}
	f.set[id] = mal
	return nil
}

type fakeNames struct {
	byQuery map[string][]NameCandidate
	asked   []string
}

func (f *fakeNames) Search(_ context.Context, q string) ([]NameCandidate, error) {
	f.asked = append(f.asked, q)
	return f.byQuery[q], nil
}

func TestBridge_ResolvesByOriginalThenRussian(t *testing.T) {
	bridge := &fakeBridge{
		titles: []MALTitle{
			{TitleID: 10, Title: "Герой Юки Юна", Original: "Yuki Yuna is a Hero", Year: 2014, Kind: "series"},
			{TitleID: 11, Title: "Герой Юна Юки", Original: "", Year: 2015, Kind: "series"},
		},
	}
	names := &fakeNames{byQuery: map[string][]NameCandidate{
		// Ромадзи узнаётся с долгими гласными: варианты ключей обязаны свести.
		"Yuki Yuna is a Hero": {{ID: "25537", Names: []string{"Yuuki Yuuna wa Yuusha de Aru", "Герой Юна Юки"}, Year: 2014, Kind: "series"}},
		// Русское — только порядком слов: s:вариант сводит.
		"Герой Юна Юки": {{ID: "999", Names: []string{"Герой Юки Юна"}, Year: 2015, Kind: "series"}},
	}}
	r := NewReconciler(nil, nil, nil, nil, nil, nil, slog.Default()).
		WithBridge("kinopub", bridge, names)
	n, err := r.EnrichMALByNames(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("мост: %d, err %v", n, err)
	}
	if bridge.set[10] != "25537" {
		t.Fatalf("MAL по ромадзи не записан: %+v", bridge.set)
	}
	if bridge.set[11] != "999" {
		t.Fatalf("MAL по русскому не записан: %+v", bridge.set)
	}
}

func TestBridge_AmbiguousOrAlienIsSkipped(t *testing.T) {
	bridge := &fakeBridge{
		titles: []MALTitle{
			// Два кандидата — не выбирать.
			{TitleID: 20, Title: "Однофамилец", Original: "Persona", Year: 2016, Kind: "series"},
			// Фильм против сериала.
			{TitleID: 21, Title: "Кино", Original: "Not a Series", Year: 2016, Kind: "movie"},
			// Год мимо окна.
			{TitleID: 22, Title: "Старое", Original: "Old One", Year: 2001, Kind: "series"},
		},
	}
	names := &fakeNames{byQuery: map[string][]NameCandidate{
		"Persona": {
			{ID: "1", Names: []string{"Persona 4"}, Year: 2011, Kind: "series"},
			{ID: "2", Names: []string{"Persona 5"}, Year: 2016, Kind: "series"},
		},
		"Not a Series": {{ID: "3", Names: []string{"Not a Series"}, Year: 2016, Kind: "series"}},
		"Old One":      {{ID: "4", Names: []string{"Old One"}, Year: 2016, Kind: "series"}},
	}}
	r := NewReconciler(nil, nil, nil, nil, nil, nil, slog.Default()).
		WithBridge("kinopub", bridge, names)
	n, _ := r.EnrichMALByNames(context.Background())
	if n != 0 || len(bridge.set) != 0 {
		t.Fatalf("сомнительное приклеено: n=%d set=%+v", n, bridge.set)
	}
	if len(bridge.skipped) != 0 {
		t.Fatalf("skipped не должен заполняться: %+v", bridge.skipped)
	}
	// Память об отказах: повторный проход не ищет то же самое.
	before := len(names.asked)
	if _, err := r.EnrichMALByNames(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(names.asked) != before {
		t.Fatalf("отказанные тайтлы искались заново: %d -> %d", before, len(names.asked))
	}
}

func TestBridge_PersistingFailureDoesNotStopPass(t *testing.T) {
	bridge := &fakeBridge{
		titles:    []MALTitle{{TitleID: 30, Title: "Тайтл", Original: "Title", Year: 2016, Kind: "series"}},
		failTitle: 30,
	}
	names := &fakeNames{byQuery: map[string][]NameCandidate{
		"Title": {{ID: "5", Names: []string{"Title"}, Year: 2016, Kind: "series"}},
	}}
	r := NewReconciler(nil, nil, nil, nil, nil, nil, slog.Default()).
		WithBridge("kinopub", bridge, names)
	if _, err := r.EnrichMALByNames(context.Background()); err == nil {
		t.Fatal("ошибка записи должна возвращаться")
	}
}

// fakeSeasons — хранилище лечения сезонов поверх fakeStore.
type fakeSeasons struct {
	*fakeStore
	conflicts []kernel.TitleID
	orphans   []OrphanTitle
	reseason  map[kernel.TitleID][]matching.Move
	adopted   []matching.Adoption
}

func (f *fakeSeasons) SeasonConflicts(context.Context) ([]kernel.TitleID, error) {
	return f.conflicts, nil
}

func (f *fakeSeasons) ReseasonRefs(_ context.Context, id kernel.TitleID, moves []matching.Move) error {
	f.reseason[id] = moves
	c := f.titles[id]
	for _, m := range moves {
		for i := range c.Refs {
			if c.Refs[i].ItemID == m.Ref.ItemID {
				c.Refs[i].SeasonNo = m.To
			}
		}
	}
	f.titles[id] = c
	f.conflicts = nil
	return nil
}

func (f *fakeSeasons) SeasonOrphans(context.Context, []string) ([]OrphanTitle, error) {
	return f.orphans, nil
}

func (f *fakeSeasons) SeasonOwners(_ context.Context, self kernel.TitleID, _, _ []string) ([]matching.Candidate, error) {
	var out []matching.Candidate
	for id, c := range f.titles {
		if id != self {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeSeasons) AdoptSeason(_ context.Context, from, into kernel.TitleID, season int) error {
	f.adopted = append(f.adopted, matching.Adoption{Into: into, SeasonNo: season})
	c := f.titles[into]
	for _, r := range f.titles[from].Refs {
		r.SeasonNo = season
		c.Refs = append(c.Refs, r)
	}
	f.titles[into] = c
	delete(f.titles, from)
	return nil
}

// Проход: сначала выравнивание внутри тайтла, потом перенос сезонов по
// году — ранний даёт следующему, что продолжать («Гинтама 7» голым числом
// приклеивается только после шестого).
func TestReconciler_FixSeasonsInOrder(t *testing.T) {
	series := cand(1, 2006, true, KeysForTest("Гинтама / Gintama"),
		matching.Ref{ProviderID: "kinopub", ItemID: "k", SeasonNo: 0},
		matching.Ref{ProviderID: "animego_me", ItemID: "g5", SeasonNo: 5},
		matching.Ref{ProviderID: "animego_fr", ItemID: "f2", SeasonNo: 2, MAL: "9969"},
		matching.Ref{ProviderID: "animego_me", ItemID: "g2", SeasonNo: 1, MAL: "9969"})
	six := cand(6, 2017, true, KeysForTest("Гинтама 6"),
		matching.Ref{ProviderID: "animego_me", ItemID: "g6", SeasonNo: 1})
	seven := cand(7, 2018, true, KeysForTest("Гинтама 7"),
		matching.Ref{ProviderID: "animego_me", ItemID: "g7", SeasonNo: 1})
	store := &fakeSeasons{
		fakeStore: &fakeStore{titles: map[kernel.TitleID]matching.Candidate{1: series, 6: six, 7: seven}},
		conflicts: []kernel.TitleID{1},
		orphans: []OrphanTitle{
			{ID: 6, Title: "Гинтама 6", Year: 2017},
			{ID: 7, Title: "Гинтама 7", Year: 2018},
		},
		reseason: map[kernel.TitleID][]matching.Move{},
	}
	rec := NewReconciler(store, store, store, store, nil, []string{"animego_me", "animego_fr"}, slog.Default()).
		WithSeasons(store, false)
	fix, err := rec.FixSeasons(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fix.Realigned != 1 || fix.Adopted != 2 {
		t.Fatalf("итог %+v", fix)
	}
	if m := store.reseason[1]; len(m) != 1 || m[0].Ref.ItemID != "g2" || m[0].To != 2 {
		t.Fatalf("выравнивание %+v", store.reseason)
	}
	want := []matching.Adoption{{Into: 1, SeasonNo: 6}, {Into: 1, SeasonNo: 7}}
	if len(store.adopted) != 2 || store.adopted[0] != want[0] || store.adopted[1] != want[1] {
		t.Fatalf("переносы %+v", store.adopted)
	}
}

// Сухой режим ничего не пишет.
func TestReconciler_FixSeasonsDry(t *testing.T) {
	series := cand(1, 2006, true, KeysForTest("Гинтама"),
		matching.Ref{ProviderID: "kinopub", ItemID: "k", SeasonNo: 0},
		matching.Ref{ProviderID: "animego_me", ItemID: "g5", SeasonNo: 5})
	six := cand(6, 2017, true, KeysForTest("Гинтама 6"),
		matching.Ref{ProviderID: "animego_me", ItemID: "g6", SeasonNo: 1})
	store := &fakeSeasons{
		fakeStore: &fakeStore{titles: map[kernel.TitleID]matching.Candidate{1: series, 6: six}},
		orphans:   []OrphanTitle{{ID: 6, Title: "Гинтама 6", Year: 2017}},
		reseason:  map[kernel.TitleID][]matching.Move{},
	}
	rec := NewReconciler(store, store, store, store, nil, nil, slog.Default()).WithSeasons(store, true)
	fix, err := rec.FixSeasons(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fix.Adopted != 1 || len(store.adopted) != 0 {
		t.Fatalf("сухой проход записал: %+v, %+v", fix, store.adopted)
	}
}
