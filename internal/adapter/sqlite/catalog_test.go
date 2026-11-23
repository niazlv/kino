package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func testTitle(item, name string) dp.Title {
	return dp.Title{
		Ref:    kernel.ProviderRef{ProviderID: "anilibria", ItemID: item},
		Kind:   "series",
		Title:  name,
		Year:   2023,
		Genres: []string{"аниме", "фэнтези"},
		Plot:   "Магическая путешественница.",
	}
}

func accountMarks(titleID kernel.TitleID) account.Marks {
	return account.Marks{UserID: 1, TitleID: titleID, Watchlist: true}
}

func playbackProgress(u kernel.UserID, ep kernel.EpisodeID, pos, dur int64) playback.Progress {
	now := time.Now()
	return playback.Progress{
		UserID: u, EpisodeID: ep, PositionMS: pos, DurationMS: dur,
		Status: "in_progress", UpdatedAt: now, ClientTS: now, DeviceID: "test",
	}
}

func TestUpsertTitleAndSearch(t *testing.T) {
	ctx := context.Background()
	repo := NewCatalogRepo(openTestDB(t))

	id, err := repo.UpsertTitle(ctx, testTitle("101", "Фрирен / Frieren"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("id = %d", id)
	}

	// Повторный тот же элемент — тот же тайтл.
	id2, err := repo.UpsertTitle(ctx, testTitle("101", "Фрирен / Frieren"))
	if err != nil {
		t.Fatalf("upsert again: %v", err)
	}
	if id2 != id {
		t.Fatalf("второй upsert дал другой тайтл: %d != %d", id2, id)
	}

	// Тайтл другой площадки с тем же названием склеивается (по ключу имени).
	// У kinopub аниме живёт в жанрах — так же и здесь: без жанра правило
	// склейки честно разъединило бы аниме-тайтл с «просто сериалом».
	other, err := repo.UpsertTitle(ctx, dp.Title{
		Ref:    kernel.ProviderRef{ProviderID: "kinopub", ItemID: "k1"},
		Kind:   "series",
		Title:  "Фрирен",
		Year:   2023,
		Genres: []string{"аниме"},
	})
	if err != nil {
		t.Fatalf("upsert other: %v", err)
	}
	if other != id {
		t.Fatalf("склейка по имени не сработала: %d != %d", other, id)
	}

	titles, total, err := repo.Search(ctx, "фрирен", 20, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 1 || len(titles) != 1 {
		t.Fatalf("search: total=%d len=%d", total, len(titles))
	}
}

func TestUpsertEpisodesAndOffers(t *testing.T) {
	ctx := context.Background()
	repo := NewCatalogRepo(openTestDB(t))
	id, err := repo.UpsertTitle(ctx, testTitle("101", "Фрирен / Frieren"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	ref := kernel.ProviderRef{ProviderID: "anilibria", ItemID: "101"}
	eps := make([]dp.Episode, 0, 3)
	for i := 1; i <= 3; i++ {
		eps = append(eps, dp.Episode{Ref: ref, SeasonNo: 1, EpisodeNo: i,
			Title: "Серия", DurationSec: 1420})
	}
	if err := repo.UpsertEpisodes(ctx, id, eps); err != nil {
		t.Fatalf("upsert episodes: %v", err)
	}

	list, err := repo.Episodes(ctx, id)
	if err != nil {
		t.Fatalf("episodes: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("episodes len = %d", len(list))
	}

	offers, err := repo.Offers(ctx, list[0].ID)
	if err != nil {
		t.Fatalf("offers: %v", err)
	}
	if len(offers) != 1 || offers[0].Ref != ref {
		t.Fatalf("offers = %+v", offers)
	}

	// Резолв адресует серию номерами площадки.
	epID, err := repo.EpisodeIDByProviderRef(ctx, ref, 1, 2)
	if err != nil || epID != list[1].ID {
		t.Fatalf("EpisodeIDByProviderRef: %v, %d", err, epID)
	}
}

func TestMarksAndProgressRoundtrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	// Пользователь обязателен: user_titles и watch_progress держат FK.
	if _, err := NewUserRepo(db).Create(ctx, account.User{
		Login: "engine", DisplayName: "Владелец", Role: account.RoleUser,
		CreatedAt: time.Now(),
	}, account.Credential{Hash: "x"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	repo := NewCatalogRepo(db)
	id, err := repo.UpsertTitle(ctx, testTitle("101", "Фрирен"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	ref := kernel.ProviderRef{ProviderID: "anilibria", ItemID: "101"}
	if err := repo.UpsertEpisodes(ctx, id, []dp.Episode{
		{Ref: ref, SeasonNo: 1, EpisodeNo: 1, DurationSec: 1420},
	}); err != nil {
		t.Fatalf("upsert episodes: %v", err)
	}
	eps, _ := repo.Episodes(ctx, id)

	marks := NewMarksRepo(db)
	if err := marks.Set(ctx, accountMarks(id)); err != nil {
		t.Fatalf("marks set: %v", err)
	}
	got, err := marks.Get(ctx, 1, id)
	if err != nil || !got.Watchlist {
		t.Fatalf("marks get: %+v, %v", got, err)
	}

	progress := NewProgressRepo(db)
	if err := progress.Upsert(ctx, playbackProgress(1, eps[0].ID, 61_000, 1_420_000)); err != nil {
		t.Fatalf("progress upsert: %v", err)
	}
	p, err := progress.Get(ctx, 1, eps[0].ID)
	if err != nil || p.PositionMS != 61_000 {
		t.Fatalf("progress get: %+v, %v", p, err)
	}
	// Своё устройство не откатывает свой свежий отчёт.
	older := playbackProgress(1, eps[0].ID, 10_000, 1_420_000)
	older.ClientTS = p.ClientTS.Add(-time.Minute)
	if err := progress.Upsert(ctx, older); err != nil {
		t.Fatalf("progress stale upsert: %v", err)
	}
	p2, _ := progress.Get(ctx, 1, eps[0].ID)
	if p2.PositionMS != 61_000 {
		t.Fatalf("свежий отчёт затёрся: %d", p2.PositionMS)
	}

	cont, total, err := progress.Continue(ctx, 1, 10, 0)
	if err != nil || total != 1 || len(cont) != 1 || cont[0].PositionMS != 61_000 {
		t.Fatalf("continue: %+v total=%d err=%v", cont, total, err)
	}

	if err := progress.SetWatched(ctx, 1, eps[0].ID, true); err != nil {
		t.Fatalf("set watched: %v", err)
	}
	_, total2, err := progress.Continue(ctx, 1, 10, 0)
	if err != nil || total2 != 0 {
		t.Fatalf("continue после галочки: total=%d err=%v", total2, err)
	}
}

func TestSearchMemoryRepoSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mem.db")
	open := func() *DB {
		db, err := Open(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	ctx := context.Background()
	db := open()
	repo := NewSearchMemoryRepo(db)
	if at, _, err := repo.Asked(ctx, "дюна"); err != nil || !at.IsZero() {
		t.Fatalf("до записи: %v %v", at, err)
	}
	when := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	if err := repo.MarkAsked(ctx, "дюна", when, 0); err != nil {
		t.Fatal(err)
	}
	// Повторная отметка обновляет, а не падает на ключе.
	when = time.Now().Truncate(time.Millisecond)
	if err := repo.MarkAsked(ctx, "дюна", when, 4); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// «Перезапуск»: то, что спрашивали, на месте.
	db = open()
	defer db.Close()
	got, hits, err := NewSearchMemoryRepo(db).Asked(ctx, "дюна")
	if err != nil || !got.Equal(when) || hits != 4 {
		t.Fatalf("после перезапуска: %v hits=%d (ждали %v, 4) %v", got, hits, when, err)
	}
}

// База, созданная до появления колонки hits (первый коммит памяти запросов),
// открывается и получает колонку, не теряя записей.
func TestOpenAddsHitsColumnToOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE search_queries (query_key TEXT PRIMARY KEY, asked_at INTEGER NOT NULL);
		INSERT INTO search_queries VALUES ('старый', 1700000000000);`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	db, err := Open(path, nil)
	if err != nil {
		t.Fatalf("открытие старой базы: %v", err)
	}
	defer db.Close()
	at, hits, err := NewSearchMemoryRepo(db).Asked(context.Background(), "старый")
	if err != nil || at.IsZero() || hits != 1 {
		t.Fatalf("запись старой базы: %v hits=%d %v", at, hits, err)
	}
}
