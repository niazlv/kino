package voicenames

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/provider"
)

// memStore — хранилище в памяти с той же семантикой, что у postgres.
type memStore struct {
	mu        sync.Mutex
	aliases   map[string]Alias
	rejected  map[[2]string]bool
	spellings map[spellingID]Spelling
}

func newMem() *memStore {
	return &memStore{aliases: map[string]Alias{}, rejected: map[[2]string]bool{}, spellings: map[spellingID]Spelling{}}
}

func (m *memStore) Aliases(context.Context) ([]Alias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Alias
	for _, a := range m.aliases {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From < out[j].From })
	return out, nil
}

func (m *memStore) PutAlias(_ context.Context, a Alias) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if x, ok := m.aliases[a.To]; ok && x.To == a.From {
		delete(m.aliases, a.To)
	}
	for k, x := range m.aliases {
		if x.To == a.From {
			x.To = a.To
			m.aliases[k] = x
		}
	}
	m.aliases[a.From] = a
	delete(m.rejected, pair(a.From, a.To))
	return nil
}

func (m *memStore) DeleteAlias(_ context.Context, from string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.aliases, from)
	return nil
}

func (m *memStore) Rejected(context.Context) ([][2]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out [][2]string
	for p := range m.rejected {
		out = append(out, p)
	}
	return out, nil
}

func (m *memStore) Reject(_ context.Context, a, b string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected[pair(a, b)] = true
	return nil
}

func (m *memStore) RecordSpellings(_ context.Context, sp []Spelling) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range sp {
		id := spellingID{x.Key, x.Title, x.Provider}
		if old, ok := m.spellings[id]; ok {
			x.Hits += old.Hits
		}
		m.spellings[id] = x
	}
	return nil
}

func (m *memStore) Spellings(context.Context) ([]Spelling, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Spelling
	for _, x := range m.spellings {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key+out[i].Provider < out[j].Key+out[j].Provider })
	return out, nil
}

func newSvc(t *testing.T) (*Service, *memStore) {
	t.Helper()
	t.Cleanup(func() { provider.SetVoiceAliases(nil) })
	m := newMem()
	// kinopub — образец написания, как его объявляет сборка площадок.
	return New(m, slog.New(slog.NewTextHandler(io.Discard, nil))).WithPreferred("kinopub"), m
}

func observe(s *Service, provider string, titles ...string) {
	for _, t := range titles {
		s.Observe(kernel.EpisodeID(7), provider, t)
	}
}

// Написания, которые сходятся по латинице, склеиваются сами; главное —
// написание kinopub. Виды озвучки обучение не трогает: их сводит разбор.
func TestLearnTranslit(t *testing.T) {
	s, m := newSvc(t)
	ctx := context.Background()
	changed := 0
	s.OnChange(func() { changed++ })
	observe(s, "kinopub", "Mega-Anime", "DUB", "AniLibria")
	observe(s, "animego_me", "Мега-Аниме", "Мега-Аниме", "AniDUB", "Анилибрия")
	observe(s, "lordfilm", "Дублированный")
	if !s.flush(ctx) {
		t.Fatal("нечего писать")
	}
	n, err := s.Learn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("выучено %d, ждали 2: %+v", n, m.aliases)
	}
	if provider.VoiceKey("Мега-Аниме") != "megaanime" || provider.VoiceKey("Анилибрия") != "anilibria" {
		t.Fatalf("главным должен быть kinopub: %+v", m.aliases)
	}
	if provider.VoiceKey("AniDUB") == provider.VoiceKey("AniLibria") || changed == 0 {
		t.Fatalf("лишнее слияние или не позвали OnChange (%d)", changed)
	}
	// Второй раз учить нечего.
	if n, _ := s.Learn(ctx); n != 0 {
		t.Fatalf("выучено повторно: %d", n)
	}
	// Перечитали ту же таблицу — списки озвучек не забываются.
	before := changed
	if err := s.Reload(ctx); err != nil || changed != before {
		t.Fatalf("пустое перечитывание сбросило списки: %d → %d, %v", before, changed, err)
	}
}

// Человек разъединил выученное — обучение его не возвращает.
func TestSplitIsRemembered(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	observe(s, "kinopub", "Mega-Anime")
	observe(s, "animego_me", "Мега-Аниме")
	s.flush(ctx)
	if _, err := s.Learn(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Split(ctx, "Мега-Аниме"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Learn(ctx); n != 0 || provider.VoiceKey("Мега-Аниме") == provider.VoiceKey("Mega-Anime") {
		t.Fatalf("разъединённое вернулось: %d", n)
	}
	if err := s.Split(ctx, "Мега-Аниме"); err != kernel.ErrNotFound {
		t.Fatalf("второй раз разъединять нечего: %v", err)
	}
}

// Ручная склейка: по написаниям, с развёрнутыми цепочками и разворотом
// уже склеенного.
func TestMerge(t *testing.T) {
	s, m := newSvc(t)
	ctx := context.Background()
	if err := s.Merge(ctx, "AniLiberty", "AniLibria", "переименовалась"); err != nil {
		t.Fatal(err)
	}
	if provider.VoiceKey("AniLiberty.TV") != "anilibria" {
		t.Fatal("не склеилось")
	}
	// Цепочка: Анилиберти → AniLiberty → AniLibria разворачивается.
	if err := s.Merge(ctx, "Анилиберти", "AniLiberty", ""); err != nil {
		t.Fatal(err)
	}
	if m.aliases["анилиберти"].To != "anilibria" {
		t.Fatalf("цепочка не развёрнута: %+v", m.aliases)
	}
	// Обратная сторона: главной становится AniLiberty.
	if err := s.Merge(ctx, "AniLibria", "AniLiberty", ""); err != nil {
		t.Fatal(err)
	}
	if provider.VoiceKey("AniLibria") != "aniliberty" || provider.VoiceKey("Анилиберти") != "aniliberty" {
		t.Fatalf("разворот: %+v", m.aliases)
	}
	for _, bad := range [][2]string{{"DUB", "AniLibria"}, {"", "AniLibria"}, {"AniLibria", "AniLibria"}} {
		if err := s.Merge(ctx, bad[0], bad[1], ""); err != kernel.ErrInvalidInput {
			t.Errorf("Merge(%q, %q) = %v", bad[0], bad[1], err)
		}
	}
}

// Подсказки: похожие студии — да, отвергнутые и виды — нет.
func TestOverviewSuggestions(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	observe(s, "animego_fr", "AniStar", "AniStar", "AniStar Многоголосый", "AniLibria", "AniLibria.TV Old", "Studio Band")
	observe(s, "kodik", "AniSter", "DUB", "Дублированный")
	s.flush(ctx)
	o, err := s.Overview(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, x := range o.Suggestions {
		got[x.A.Title+" | "+x.B.Title] = x.Why
	}
	for _, want := range []string{"AniStar | AniStar Многоголосый", "AniStar | AniSter", "AniLibria | AniLibria.TV Old"} {
		if got[want] == "" {
			t.Errorf("нет подсказки %q: %v", want, got)
		}
	}
	if err := s.Reject(ctx, "AniStar", "AniStar Многоголосый"); err != nil {
		t.Fatal(err)
	}
	o, _ = s.Overview(ctx, "многогол")
	for _, x := range o.Suggestions {
		if x.B.Title == "AniStar Многоголосый" {
			t.Fatal("отвергнутая пара вернулась")
		}
	}
	if len(o.Studios) != 1 || o.Studios[0].Title != "AniStar Многоголосый" {
		t.Fatalf("поиск: %+v", o.Studios)
	}
	for _, st := range o.Studios {
		if provider.IsVoiceKind(st.Key) {
			t.Fatalf("вид озвучки среди студий: %q", st.Key)
		}
	}
}
