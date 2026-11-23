package postgres

// Склейка против настоящей базы с каталогом kinopub. Выключена по умолчанию:
//
//	KINO_PG_DSN=postgres://kino:kino@localhost:55432/kino?sslmode=disable \
//	  go test ./internal/adapter/postgres/ -run DB -v
//
// Пишет в базу ровно то, что записал бы обходчик animego.me для этих
// тайтлов, поэтому повторный прогон ничего не портит.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

func TestDB_AnimeJoinsKinopubAndMALJoinsAnime(t *testing.T) {
	dsn := os.Getenv("KINO_PG_DSN")
	if dsn == "" {
		t.Skip("нужна база стенда: KINO_PG_DSN")
	}
	ctx := context.Background()
	pool, err := pg.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := NewCatalogRepo(pool).
		WithPriorities(map[string]int{"kinopub": 100, "animego_me": 60, "animego_fr": 50}).
		WithAnimeProviders("animego_me", "animego_fr")

	var kinopubID kernel.TitleID
	if err := pool.QueryRow(ctx, `
		SELECT t.id FROM canonical_titles t JOIN external_refs x ON x.title_id = t.id
		WHERE x.provider_id = 'kinopub' AND t.title = 'Принцесса-шпионка / Princess Principal'`).Scan(&kinopubID); err != nil {
		t.Skipf("в базе нет kinopub-тайтла «Принцесса-шпионка»: %v", err)
	}
	var genresBefore []string
	_ = pool.QueryRow(ctx, `SELECT genres FROM canonical_titles WHERE id = $1`, kinopubID).Scan(&genresBefore)

	// Так его отдаёт провайдер animego.me (сверено живым тестом).
	id, err := repo.UpsertTitle(ctx, dp.Title{
		Ref:  kernel.ProviderRef{ProviderID: "animego_me", ItemID: "princessa-shpionka-421"},
		Kind: "series", Title: "Принцесса-шпионка / Princess Principal", Original: "Princess Principal",
		SeasonNo: 1, Year: 2017, MAL: "35240", Genres: []string{"Аниме", "Экшен", "Тайна"},
		Poster: "https://img.cdngos.com/anime/5a/5ab171ac30ec0336110643",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != kinopubID {
		t.Fatalf("animego.me завёл тайтл %d вместо склейки с kinopub %d", id, kinopubID)
	}
	var genresAfter []string
	_ = pool.QueryRow(ctx, `SELECT genres FROM canonical_titles WHERE id = $1`, kinopubID).Scan(&genresAfter)
	if len(genresAfter) != len(genresBefore) {
		t.Fatalf("площадка ниже по приоритету переписала жанры хозяина: %v -> %v", genresBefore, genresAfter)
	}

	// «Принцесса-медуза» на animego.fr уже заведена живым поиском; animego.me
	// с тем же номером MAL обязан попасть туда же, а не в дубль.
	var meduza kernel.TitleID
	if err := pool.QueryRow(ctx, `SELECT title_id FROM external_refs WHERE provider_id = 'animego_fr' AND mal_id = '8129'`).Scan(&meduza); err != nil {
		t.Skipf("animego.fr «Принцесса-медуза» ещё не заведена: %v", err)
	}
	id2, err := repo.UpsertTitle(ctx, dp.Title{
		Ref:  kernel.ProviderRef{ProviderID: "animego_me", ItemID: "princessa-meduza-matchtest-999999"},
		Kind: "series", Title: "Медуза-принцесса / Kuragehime", Original: "Kuragehime",
		SeasonNo: 1, Year: 2010, MAL: "8129",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM external_refs WHERE provider_ref = 'princessa-meduza-matchtest-999999'`)
	if id2 != meduza {
		t.Fatalf("совпадение по MAL не склеило: %d вместо %d", id2, meduza)
	}
}

// Ручная правка: отвязать элемент площадки в свой тайтл, посмотреть серию
// там, слить обратно — прогресс обязан переехать на ту же серию.
func TestDB_DetachAndMergeKeepProgress(t *testing.T) {
	dsn := os.Getenv("KINO_PG_DSN")
	if dsn == "" {
		t.Skip("нужна база стенда: KINO_PG_DSN")
	}
	ctx := context.Background()
	pool, err := pg.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := NewCatalogRepo(pool).WithPriorities(map[string]int{"kinopub": 100, "animego_me": 60})

	const provider, item = "animego_me", "princessa-shpionka-421"
	var home kernel.TitleID
	if err := pool.QueryRow(ctx, `SELECT title_id FROM external_refs WHERE provider_id = $1 AND provider_ref = $2`,
		provider, item).Scan(&home); err != nil {
		t.Skipf("привязки %s нет — сначала TestDB_AnimeJoinsKinopubAndMALJoinsAnime: %v", item, err)
	}
	var user kernel.UserID
	if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&user); err != nil {
		t.Skip("в базе нет пользователей")
	}

	moved, err := repo.DetachSource(ctx, provider, item)
	if err != nil {
		t.Fatal(err)
	}
	if moved == home {
		t.Fatal("отвязка не завела новый тайтл")
	}
	// Серии площадки в отвязанном тайтле и позиция на второй из них.
	eps := []dp.Episode{}
	for n := 1; n <= 3; n++ {
		eps = append(eps, dp.Episode{Ref: kernel.ProviderRef{ProviderID: provider, ItemID: item}, SeasonNo: 1, EpisodeNo: n})
	}
	if err := repo.UpsertEpisodes(ctx, moved, eps); err != nil {
		t.Fatal(err)
	}
	var ep2 kernel.EpisodeID
	if err := pool.QueryRow(ctx, `SELECT id FROM canonical_episodes WHERE title_id = $1 AND season_no = 1 AND episode_no = 2`, moved).Scan(&ep2); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour) // свежее любой позиции в тайтле-приёмнике
	if _, err := pool.Exec(ctx, `
		INSERT INTO watch_progress (user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts)
		VALUES ($1, $2, 424242, 1400000, 'in_progress', $3, $3)`, user, ep2, future); err != nil {
		t.Fatal(err)
	}

	if err := repo.MergeTitles(ctx, moved, home); err != nil {
		t.Fatal(err)
	}
	var gone bool
	_ = pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM canonical_titles WHERE id = $1)`, moved).Scan(&gone)
	if !gone {
		t.Fatal("слитый тайтл остался")
	}
	var back kernel.TitleID
	var pinned bool
	_ = pool.QueryRow(ctx, `SELECT title_id, pinned FROM external_refs WHERE provider_id = $1 AND provider_ref = $2`,
		provider, item).Scan(&back, &pinned)
	if back != home || !pinned {
		t.Fatalf("площадка не вернулась: тайтл %d, закреплено %v", back, pinned)
	}
	var pos int64
	if err := pool.QueryRow(ctx, `
		SELECT p.position_ms FROM watch_progress p JOIN canonical_episodes e ON e.id = p.episode_id
		WHERE p.user_id = $1 AND e.title_id = $2 AND e.season_no = 1 AND e.episode_no = 2`, user, home).Scan(&pos); err != nil || pos != 424242 {
		t.Fatalf("прогресс не переехал: %d %v", pos, err)
	}
	var offers int
	_ = pool.QueryRow(ctx, `
		SELECT count(*) FROM episode_offers o JOIN canonical_episodes e ON e.id = o.episode_id
		WHERE o.provider_id = $1 AND e.title_id = $2`, provider, home).Scan(&offers)
	if offers < 3 {
		t.Fatalf("предложения площадки не переехали: %d", offers)
	}
	// Позицию, поставленную тестом, убираем: её никто не смотрел. Серии
	// площадки тест завёл не все — их предложения тоже убираем, иначе
	// ленивый каталог счёл бы площадку уже прочитанной и последние серии
	// у неё пропали бы. Привязка — обратно в автоматическую.
	_, _ = pool.Exec(ctx, `
		DELETE FROM watch_progress p USING canonical_episodes e
		WHERE p.episode_id = e.id AND p.user_id = $1 AND e.title_id = $2 AND p.position_ms = 424242`, user, home)
	_, _ = pool.Exec(ctx, `DELETE FROM episode_offers WHERE provider_id = $1 AND provider_ref = $2`, provider, item)
	_, _ = pool.Exec(ctx, `UPDATE external_refs SET matched_by = 'name', pinned = false
		WHERE provider_id = $1 AND provider_ref = $2`, provider, item)
}
