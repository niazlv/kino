package history

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

type fakeCatalog struct {
	ids map[kernel.ProviderRef]kernel.TitleID
}

// Серия находится, если тайтл заведён: id серии — из тайтла, сезона и номера.
func (c *fakeCatalog) EpisodeIDByProviderRef(_ context.Context, r kernel.ProviderRef, s, e int) (kernel.EpisodeID, error) {
	id, ok := c.ids[r]
	if !ok {
		return 0, kernel.ErrNotFound
	}
	return kernel.EpisodeID(int64(id)*10000 + int64(s)*100 + int64(e)), nil
}
func (c *fakeCatalog) TitleIDByProviderRef(_ context.Context, r kernel.ProviderRef) (kernel.TitleID, error) {
	id, ok := c.ids[r]
	if !ok {
		return 0, kernel.ErrNotFound
	}
	return id, nil
}
func (c *fakeCatalog) UpsertTitle(_ context.Context, t dp.Title) (kernel.TitleID, error) {
	if c.ids == nil {
		c.ids = map[kernel.ProviderRef]kernel.TitleID{}
	}
	if id, ok := c.ids[t.Ref]; ok {
		return id, nil
	}
	id := kernel.TitleID(len(c.ids) + 1)
	c.ids[t.Ref] = id
	return id, nil
}
func (c *fakeCatalog) UpsertEpisodes(context.Context, kernel.TitleID, []dp.Episode) error { return nil }

type fakeMarks struct {
	watchlist, following map[kernel.UserID][]kernel.TitleID
	at                   map[kernel.TitleID]time.Time
}

func (m *fakeMarks) SetFollowing(_ context.Context, u kernel.UserID, t kernel.TitleID, _ bool, at time.Time) error {
	if m.following == nil {
		m.following = map[kernel.UserID][]kernel.TitleID{}
	}
	m.following[u] = append(m.following[u], t)
	m.stamp(t, at)
	return nil
}
func (m *fakeMarks) SetWatchlist(_ context.Context, u kernel.UserID, t kernel.TitleID, _ bool, at time.Time) error {
	if m.watchlist == nil {
		m.watchlist = map[kernel.UserID][]kernel.TitleID{}
	}
	m.watchlist[u] = append(m.watchlist[u], t)
	m.stamp(t, at)
	return nil
}
func (m *fakeMarks) stamp(t kernel.TitleID, at time.Time) {
	if m.at == nil {
		m.at = map[kernel.TitleID]time.Time{}
	}
	m.at[t] = at
}

type memProgress struct {
	rows map[kernel.EpisodeID]playback.Progress
}

func (m *memProgress) Get(_ context.Context, _ kernel.UserID, e kernel.EpisodeID) (playback.Progress, error) {
	p, ok := m.rows[e]
	if !ok {
		return playback.Progress{}, kernel.ErrNotFound
	}
	return p, nil
}
func (m *memProgress) Upsert(_ context.Context, p playback.Progress) error {
	if m.rows == nil {
		m.rows = map[kernel.EpisodeID]playback.Progress{}
	}
	m.rows[p.EpisodeID] = p
	return nil
}

type noProgress struct{}

func (noProgress) Get(context.Context, kernel.UserID, kernel.EpisodeID) (playback.Progress, error) {
	return playback.Progress{}, kernel.ErrNotFound
}
func (noProgress) Upsert(context.Context, playback.Progress) error { return nil }

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(1700000000, 0) }

// fakeSource — площадка с одной папкой; остального переносу папки не нужно.
type fakeSource struct {
	folders map[string][]dp.Title
	asked   []string
	recent  []dp.WatchRecord
	titles  map[kernel.ProviderRef]dp.Title
}

func (f *fakeSource) ImportWatchHistory(context.Context, func(dp.WatchRecord) error) error {
	return nil
}
func (f *fakeSource) Title(_ context.Context, r kernel.ProviderRef) (dp.Title, error) {
	if t, ok := f.titles[r]; ok {
		return t, nil
	}
	return dp.Title{}, kernel.ErrNotFound
}
func (f *fakeSource) RecentHistory(context.Context, time.Time) ([]dp.WatchRecord, error) {
	f.asked = append(f.asked, "recent")
	return f.recent, nil
}
func (f *fakeSource) Episodes(context.Context, kernel.ProviderRef) ([]dp.Episode, error) {
	return nil, nil
}
func (f *fakeSource) Watchlist(context.Context) ([]dp.Title, error) { return nil, nil }
func (f *fakeSource) WatchHistoryFor(context.Context, kernel.ProviderRef) ([]dp.WatchRecord, error) {
	return nil, nil
}
func (f *fakeSource) BookmarkFolders(context.Context) ([]dp.BookmarkFolder, error) {
	return []dp.BookmarkFolder{{ID: "893479", Title: "Данис", Count: len(f.folders["893479"])}}, nil
}
func (f *fakeSource) BookmarkFolder(_ context.Context, id string) ([]dp.Title, error) {
	f.asked = append(f.asked, id)
	return f.folders[id], nil
}

func admin() auth.Actor {
	now := time.Now()
	return auth.Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, FactorAt: &now}
}

func ref(id string) kernel.ProviderRef { return kernel.ProviderRef{ProviderID: "kinopub", ItemID: id} }

// Папка ложится в «буду смотреть» того, кому переносят, а не того, кто
// переносит; повторный перенос не задваивает тайтлы в каталоге.
func TestImportFolderGoesToTargetWatchlist(t *testing.T) {
	cat, marks := &fakeCatalog{}, &fakeMarks{}
	s := NewService(cat, noProgress{}, marks, fixedClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	src := &fakeSource{folders: map[string][]dp.Title{"893479": {
		{Ref: ref("22551"), Kind: "series", Title: "Инуяшики"},
		{Ref: ref("26"), Kind: "movie", Title: "Вечное сияние чистого разума"},
		// Площадка держит один тайтл в папке дважды — так и есть в «Данис».
		{Ref: ref("22551"), Kind: "series", Title: "Инуяшики"},
	}}}
	const danis = kernel.UserID(3)

	res, err := s.ImportFolder(context.Background(), admin(), danis, src, "893479")
	if err != nil {
		t.Fatal(err)
	}
	if res.Seen != 3 || res.Added != 2 || res.Repeats != 1 || len(marks.watchlist[danis]) != 2 || len(marks.watchlist[1]) != 0 {
		t.Fatalf("итог %+v, отметки %+v", res, marks.watchlist)
	}
	if len(marks.following) != 0 {
		t.Fatalf("папка — это «буду смотреть», а не слежение: %+v", marks.following)
	}
	// Порядок папки держится: верхний у площадки — самый свежий здесь.
	if first, second := marks.at[cat.ids[ref("22551")]], marks.at[cat.ids[ref("26")]]; !first.After(second) {
		t.Fatalf("порядок папки потерян: %v не позже %v", first, second)
	}
	if _, err := s.ImportFolder(context.Background(), admin(), danis, src, "893479"); err != nil {
		t.Fatal(err)
	}
	if len(cat.ids) != 2 {
		t.Fatalf("повторный перенос завёл лишние тайтлы: %d", len(cat.ids))
	}
}

// Перенос тратит общий аккаунт площадки: пользователю — даже себе — нельзя.
func TestImportIsAdminOnly(t *testing.T) {
	s := NewService(&fakeCatalog{}, noProgress{}, &fakeMarks{}, fixedClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	src := &fakeSource{}
	user := auth.Actor{UserID: 3, Role: account.RoleUser, Space: account.SpaceUser}
	ctx := context.Background()

	if _, err := s.Import(ctx, user, 3, src); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("Import: %v", err)
	}
	if _, err := s.ImportWatchlist(ctx, user, 3, src); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ImportWatchlist: %v", err)
	}
	if _, err := s.ImportFolder(ctx, user, 3, src, "893479"); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ImportFolder: %v", err)
	}
	if _, err := s.Folders(ctx, user, src); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("Folders: %v", err)
	}
	if len(src.asked) != 0 {
		t.Fatalf("площадку спросили до проверки прав: %v", src.asked)
	}
}

// Журнал ложится с настоящими датами: по ним сортируются «История» и
// «Продолжить смотреть». Недостающий тайтл дотягивается, сериал ставится
// на слежение, фильм — нет.
func TestImportRecentKeepsDatesAndFollowsSeries(t *testing.T) {
	cat, marks, prog := &fakeCatalog{}, &fakeMarks{}, &memProgress{}
	s := NewService(cat, prog, marks, fixedClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	yesterday := time.Unix(1700000000, 0).Add(-24 * time.Hour)
	week := time.Unix(1700000000, 0).Add(-7 * 24 * time.Hour)
	src := &fakeSource{
		titles: map[kernel.ProviderRef]dp.Title{
			ref("16739"): {Ref: ref("16739"), Kind: "series", Title: "Проза бродячих псов"},
			ref("26"):    {Ref: ref("26"), Kind: "movie", Title: "Вечное сияние"},
		},
		recent: []dp.WatchRecord{
			{Ref: ref("16739"), SeasonNo: 2, EpisodeNo: 12, PositionSec: 737, DurationSec: 1420, WatchedAt: yesterday},
			{Ref: ref("16739"), SeasonNo: 2, EpisodeNo: 11, PositionSec: 1420, DurationSec: 1420, WatchedAt: yesterday.Add(-time.Hour)},
			{Ref: ref("26"), SeasonNo: 0, EpisodeNo: 1, PositionSec: 6400, DurationSec: 6480, WatchedAt: week},
		},
	}
	const danis = kernel.UserID(3)
	res, err := s.ImportRecent(context.Background(), admin(), danis, src, week.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.Seen != 3 || res.Imported != 3 || res.Fetched != 2 {
		t.Fatalf("итог %+v", res)
	}
	for _, want := range []time.Time{yesterday, yesterday.Add(-time.Hour), week} {
		found := false
		for _, p := range prog.rows {
			if p.UserID != danis || !p.UpdatedAt.Equal(p.ClientTS) {
				t.Fatalf("строка %+v", p)
			}
			found = found || p.UpdatedAt.Equal(want)
		}
		if !found {
			t.Fatalf("дата %v потерялась: %+v", want, prog.rows)
		}
	}
	if len(marks.following[danis]) != 1 {
		t.Fatalf("на слежение — только сериал, и один раз: %+v", marks.following)
	}
	// Дата слежения — последний просмотр сериала, а не время переноса.
	if got := marks.at[cat.ids[ref("16739")]]; !got.Equal(yesterday) {
		t.Fatalf("дата слежения %v, ждали %v", got, yesterday)
	}
}
