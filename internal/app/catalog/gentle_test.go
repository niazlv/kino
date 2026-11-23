package catalog

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// memSearch — память о запросах «на диске»: её держит тест, а не LazyCatalog, как
// и в жизни — база переживает процесс.
type memSearch struct {
	mu   sync.Mutex
	at   map[string]time.Time
	hits map[string]int
}

func newMemSearch() *memSearch { return &memSearch{at: map[string]time.Time{}, hits: map[string]int{}} }

func (m *memSearch) Asked(_ context.Context, key string) (time.Time, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.at[key], m.hits[key], nil
}

func (m *memSearch) MarkAsked(_ context.Context, key string, at time.Time, hits int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.at[key], m.hits[key] = at, hits
	return nil
}

func emptyReader() *fakeReader {
	return &fakeReader{titles: map[kernel.TitleID]catalog.Title{}}
}

// Главное обещание локального режима: тот же запрос на площадки не уходит
// повторно — и после перезапуска тоже, и когда он ничего не нашёл.
func TestSearchMemory_SameQueryIsNotAskedAgainEvenAfterRestart(t *testing.T) {
	mem := newMemSearch()
	calls := 0
	live := func(context.Context, string, int) ([]dp.Title, error) {
		calls++
		return nil, nil // ничего не нашли — и это тоже запоминается
	}
	newCat := func() *LazyCatalog { // «процесс» движка; память — общая, как база
		return NewLazyCatalog(emptyReader(), nil, nil).WithLiveSearch(&fakeTitles{}, live).
			WithSearchMemory(mem, 7*24*time.Hour)
	}

	if _, err := newCat().Search(context.Background(), "Умпа Лумпа", 10, 0); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("первый поиск: площадки спрошены %d раз, ждали 1", calls)
	}
	// Перезапуск: новый LazyCatalog, память та же. Регистр и дефис в запросе не важны.
	for _, q := range []string{"Умпа Лумпа", "умпа-лумпа", "  УМПА   лумпа "} {
		if _, err := newCat().Search(context.Background(), q, 10, 0); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("повтор того же запроса дошёл до площадок (%d вызовов): они бережутся", calls)
	}
	// Другой запрос — другое дело.
	_, _ = newCat().Search(context.Background(), "Наруто", 10, 0)
	if calls != 2 {
		t.Errorf("новый запрос должен спросить площадки: %d вызовов", calls)
	}
}

// Нет сети — это не «ничего не нашли». Запомнить такой запрос значило бы
// оставить его без ответа на неделю.
func TestSearchMemory_OfflineQueryIsNotRemembered(t *testing.T) {
	mem := newMemSearch()
	calls := 0
	var fail = true
	live := func(context.Context, string, int) ([]dp.Title, error) {
		calls++
		if fail {
			return nil, errors.New("ни одна площадка не ответила")
		}
		return nil, nil
	}
	c := NewLazyCatalog(emptyReader(), nil, nil).WithLiveSearch(&fakeTitles{}, live).
		WithSearchMemory(mem, 7*24*time.Hour)

	_, _ = c.Search(context.Background(), "дюна", 10, 0)
	if at, _, _ := mem.Asked(context.Background(), catalog.NormalizeName("дюна")); !at.IsZero() {
		t.Fatal("запрос, на который никто не ответил, запомнился как спрошенный")
	}
	fail = false // сеть вернулась
	_, _ = c.Search(context.Background(), "дюна", 10, 0)
	if calls != 2 {
		t.Errorf("после возвращения сети запрос должен уйти снова: %d вызовов", calls)
	}
}

// Запомненное устаревает: спустя repeat запрос снова вправе уйти.
func TestSearchMemory_ExpiresAfterRepeat(t *testing.T) {
	mem := newMemSearch()
	mem.at[catalog.NormalizeName("дюна")] = time.Now().Add(-8 * 24 * time.Hour)
	mem.hits[catalog.NormalizeName("дюна")] = 3
	calls := 0
	live := func(context.Context, string, int) ([]dp.Title, error) { calls++; return nil, nil }
	c := NewLazyCatalog(emptyReader(), nil, nil).WithLiveSearch(&fakeTitles{}, live).
		WithSearchMemory(mem, 7*24*time.Hour)
	_, _ = c.Search(context.Background(), "дюна", 10, 0)
	if calls != 1 {
		t.Errorf("запрос недельной давности должен уйти снова: %d вызовов", calls)
	}
}

// Найденное когда-то — помнится и не теряется: поиск, у которого в каталоге
// уже есть тайтл с запросом в названии, на площадки не идёт вовсе. Именно так
// найденная «Умпа Лумпа» отдаётся из каталога, без единого запроса.
func TestFoundOnceIsServedFromCatalog(t *testing.T) {
	r := &fakeReader{
		local:  []catalog.Title{{ID: 9, Title: "Умпа-Лумпа и шоколадная фабрика"}},
		titles: map[kernel.TitleID]catalog.Title{},
	}
	calls := 0
	live := func(context.Context, string, int) ([]dp.Title, error) { calls++; return nil, nil }
	c := NewLazyCatalog(r, nil, nil).WithLiveSearch(&fakeTitles{}, live).
		WithSearchMemory(newMemSearch(), time.Hour)
	got, err := c.Search(context.Background(), "умпа лумпа", 10, 0)
	if err != nil || len(got.Items) != 1 || got.Items[0].ID != 9 {
		t.Fatalf("%+v %v", got, err)
	}
	if calls != 0 {
		t.Errorf("тайтл уже в каталоге, а площадки спросили %d раз", calls)
	}
}

func TestStaleAfterIsConfigurable(t *testing.T) {
	c := NewLazyCatalog(emptyReader(), nil, nil)
	if c.staleAfterOrDefault() != staleAfter {
		t.Errorf("по умолчанию — сутки сервера, а не %v", c.staleAfterOrDefault())
	}
	c.WithStaleAfter(36 * time.Hour)
	if c.staleAfterOrDefault() != 36*time.Hour {
		t.Errorf("настройка не применилась: %v", c.staleAfterOrDefault())
	}
	c.WithStaleAfter(0)
	if c.staleAfterOrDefault() != staleAfter {
		t.Errorf("ноль — снова умолчание: %v", c.staleAfterOrDefault())
	}
}

// Пустой ответ помнится недолго: один случайный пустой ответ площадки (антибот,
// лимит) не должен закрывать запрос на неделю, а находки — помнятся долго.
func TestSearchMemory_EmptyIsRememberedShorterThanFinds(t *testing.T) {
	key := catalog.NormalizeName("дюна")
	run := func(ago time.Duration, hits int) int {
		mem := newMemSearch()
		mem.at[key], mem.hits[key] = time.Now().Add(-ago), hits
		calls := 0
		live := func(context.Context, string, int) ([]dp.Title, error) { calls++; return nil, nil }
		c := NewLazyCatalog(emptyReader(), nil, nil).WithLiveSearch(&fakeTitles{}, live).
			WithSearchMemory(mem, 7*24*time.Hour)
		_, _ = c.Search(context.Background(), "дюна", 10, 0)
		return calls
	}
	if n := run(2*time.Hour, 0); n != 0 {
		t.Errorf("пустой ответ двухчасовой давности: площадки спрошены %d раз, ждали 0", n)
	}
	if n := run(13*time.Hour, 0); n != 1 {
		t.Errorf("пустой ответ 13-часовой давности должен уйти снова: %d вызовов", n)
	}
	if n := run(3*24*time.Hour, 5); n != 0 {
		t.Errorf("находки трёхдневной давности помнятся: %d вызовов", n)
	}
	if n := run(8*24*time.Hour, 5); n != 1 {
		t.Errorf("находки недельной давности устарели: %d вызовов", n)
	}
}

func TestSearchMemory_RecordsHitCount(t *testing.T) {
	mem := newMemSearch()
	live := func(context.Context, string, int) ([]dp.Title, error) {
		return []dp.Title{{Title: "Дюна"}, {Title: "Дюна 2"}}, nil
	}
	c := NewLazyCatalog(emptyReader(), nil, nil).WithLiveSearch(&fakeTitles{}, live).
		WithSearchMemory(mem, time.Hour)
	_, _ = c.Search(context.Background(), "дюна", 10, 0)
	if _, hits, _ := mem.Asked(context.Background(), catalog.NormalizeName("дюна")); hits != 2 {
		t.Errorf("запомнено находок %d, ждали 2", hits)
	}
}
