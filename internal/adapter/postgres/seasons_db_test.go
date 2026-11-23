package postgres

// Лечение сезонов против настоящей базы (KINO_PG_DSN, см.
// catalog_match_db_test.go). Тайтлы заводятся свои и за собой убираются.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/app/matching"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

func seasonsDB(t *testing.T) (*CatalogRepo, func(string, ...any) kernel.TitleID) {
	t.Helper()
	dsn := os.Getenv("KINO_PG_DSN")
	if dsn == "" {
		t.Skip("нужна база стенда: KINO_PG_DSN")
	}
	ctx := context.Background()
	pool, err := pg.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := NewCatalogRepo(pool).
		WithPriorities(map[string]int{"kinopub": 100, "animego_me": 60, "animego_fr": 50}).
		WithAnimeProviders("animego_me", "animego_fr")
	var made []kernel.TitleID
	t.Cleanup(func() {
		// Серии и отметки уходят каскадом; предложения — вместе с привязками.
		for _, id := range made {
			_, _ = pool.Exec(ctx, `DELETE FROM canonical_titles WHERE id = $1`, id)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM external_refs WHERE provider_ref LIKE 'seasonstest-%'`)
	})
	title := func(name string, args ...any) kernel.TitleID {
		var id kernel.TitleID
		if err := pool.QueryRow(ctx, `
			INSERT INTO canonical_titles (kind, title, title_norm, year, genres)
			VALUES ('series', $1, lower($1), $2, '{Аниме}') RETURNING id`, name, args[0]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		made = append(made, id)
		return id
	}
	return repo, title
}

func refRow(t *testing.T, repo *CatalogRepo, provider, item string, title kernel.TitleID, season int, mal string) {
	t.Helper()
	if _, err := repo.db.Exec(context.Background(), `
		INSERT INTO external_refs (provider_id, provider_ref, title_id, season_no, mal_id, matched_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), 'new')`, provider, item, title, season, mal); err != nil {
		t.Fatal(err)
	}
}

// Тайтл-сезон с чьим-то прогрессом переносится в сериал седьмым сезоном:
// привязки, серии и позиция просмотра — туда же, сам тайтл исчезает.
func TestDBSeasons_AdoptMovesProgressIntoSeason(t *testing.T) {
	repo, title := seasonsDB(t)
	ctx := context.Background()
	var user kernel.UserID
	if err := repo.db.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&user); err != nil {
		t.Skip("в базе нет пользователей")
	}

	series := title("Гинтама (проверка сезонов)", 2006)
	refRow(t, repo, "kinopub", "seasonstest-kp", series, 0, "")
	orphan := title("Гинтама 7 (проверка сезонов)", 2018)
	refRow(t, repo, "animego_me", "seasonstest-me7", orphan, 1, "")
	refRow(t, repo, "animego_fr", "seasonstest-fr7", orphan, 7, "36838")

	// Обе площадки успели разложить серии: одна в «первый», другая в седьмой.
	var eps []dp.Episode
	for n := 1; n <= 3; n++ {
		eps = append(eps,
			dp.Episode{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "seasonstest-me7"}, SeasonNo: 1, EpisodeNo: n},
			dp.Episode{Ref: kernel.ProviderRef{ProviderID: "animego_fr", ItemID: "seasonstest-fr7"}, SeasonNo: 1, EpisodeNo: n})
	}
	if err := repo.UpsertEpisodes(ctx, orphan, eps); err != nil {
		t.Fatal(err)
	}
	var s1e2, s7e2 kernel.EpisodeID
	_ = repo.db.QueryRow(ctx, `SELECT id FROM canonical_episodes WHERE title_id = $1 AND season_no = 1 AND episode_no = 2`, orphan).Scan(&s1e2)
	_ = repo.db.QueryRow(ctx, `SELECT id FROM canonical_episodes WHERE title_id = $1 AND season_no = 7 AND episode_no = 2`, orphan).Scan(&s7e2)
	if s1e2 == 0 || s7e2 == 0 {
		t.Fatal("серии тайтла-сезона не разложились")
	}
	// Одна серия, посмотренная через обе площадки: свежая позиция побеждает.
	now := time.Now()
	for _, p := range []struct {
		ep  kernel.EpisodeID
		pos int
		at  time.Time
	}{{s1e2, 111000, now.Add(-time.Hour)}, {s7e2, 222000, now}} {
		if _, err := repo.db.Exec(ctx, `
			INSERT INTO watch_progress (user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts)
			VALUES ($1, $2, $3, 1400000, 'in_progress', $4, $4)`, user, p.ep, p.pos, p.at); err != nil {
			t.Fatal(err)
		}
	}

	if err := repo.AdoptSeason(ctx, orphan, series, 7); err != nil {
		t.Fatal(err)
	}

	var gone bool
	_ = repo.db.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM canonical_titles WHERE id = $1)`, orphan).Scan(&gone)
	if !gone {
		t.Fatal("тайтл-сезон остался")
	}
	rows, err := repo.db.Query(ctx, `SELECT provider_ref, title_id, season_no FROM external_refs WHERE provider_ref LIKE 'seasonstest-%7'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var item string
		var tid kernel.TitleID
		var season int
		_ = rows.Scan(&item, &tid, &season)
		if tid != series || season != 7 {
			t.Errorf("%s: тайтл %d сезон %d, ожидалось %d сезон 7", item, tid, season, series)
		}
	}
	rows.Close()
	var pos int
	if err := repo.db.QueryRow(ctx, `
		SELECT p.position_ms FROM watch_progress p JOIN canonical_episodes e ON e.id = p.episode_id
		WHERE p.user_id = $1 AND e.title_id = $2 AND e.season_no = 7 AND e.episode_no = 2`, user, series).Scan(&pos); err != nil {
		t.Fatalf("позиция не переехала в седьмой сезон: %v", err)
	}
	if pos != 222000 {
		t.Fatalf("позиция %d, ожидалась свежая 222000", pos)
	}
	var offers int
	_ = repo.db.QueryRow(ctx, `
		SELECT count(*) FROM episode_offers o JOIN canonical_episodes e ON e.id = o.episode_id
		WHERE e.title_id = $1 AND e.season_no = 7 AND o.provider_ref LIKE 'seasonstest-%'`, series).Scan(&offers)
	if offers != 6 {
		t.Fatalf("предложений площадок в седьмом сезоне %d, ожидалось 6", offers)
	}
	var stray int
	_ = repo.db.QueryRow(ctx, `SELECT count(*) FROM canonical_episodes WHERE title_id = $1 AND season_no = 1`, series).Scan(&stray)
	if stray != 0 {
		t.Fatalf("в первом сезоне сериала появились чужие серии: %d", stray)
	}
}

// Выравнивание по MAL: привязка уходит на верный сезон, её серии из чужого
// сезона убираются, а площадка остаётся непрочитанной — разложится заново.
func TestDBSeasons_ReseasonDropsWrongEpisodes(t *testing.T) {
	repo, title := seasonsDB(t)
	ctx := context.Background()

	id := title("Пять невест (проверка сезонов)", 2019)
	refRow(t, repo, "animego_fr", "seasonstest-fr2", id, 2, "39783")
	refRow(t, repo, "animego_me", "seasonstest-me2", id, 1, "39783")
	var eps []dp.Episode
	for n := 1; n <= 2; n++ {
		eps = append(eps,
			dp.Episode{Ref: kernel.ProviderRef{ProviderID: "animego_fr", ItemID: "seasonstest-fr2"}, SeasonNo: 1, EpisodeNo: n},
			dp.Episode{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "seasonstest-me2"}, SeasonNo: 1, EpisodeNo: n})
	}
	if err := repo.UpsertEpisodes(ctx, id, eps); err != nil {
		t.Fatal(err)
	}

	conflicts, err := repo.SeasonConflicts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range conflicts {
		found = found || c == id
	}
	if !found {
		t.Fatal("расхождение сезонов по MAL не найдено")
	}
	c, err := repo.Candidate(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	moves := matching.Realign(c)
	if len(moves) != 1 || moves[0].Ref.ItemID != "seasonstest-me2" || moves[0].To != 2 {
		t.Fatalf("переносы %+v", moves)
	}
	if err := repo.ReseasonRefs(ctx, id, moves); err != nil {
		t.Fatal(err)
	}
	var season, offers, first int
	_ = repo.db.QueryRow(ctx, `SELECT season_no FROM external_refs WHERE provider_ref = 'seasonstest-me2'`).Scan(&season)
	_ = repo.db.QueryRow(ctx, `SELECT count(*) FROM episode_offers WHERE provider_ref = 'seasonstest-me2'`).Scan(&offers)
	_ = repo.db.QueryRow(ctx, `SELECT count(*) FROM canonical_episodes WHERE title_id = $1 AND season_no = 1`, id).Scan(&first)
	if season != 2 || offers != 0 || first != 0 {
		t.Fatalf("сезон %d, предложений %d, серий в первом сезоне %d; ожидалось 2, 0, 0", season, offers, first)
	}
}
