package postgres

// Длительность серий против настоящей базы (KINO_PG_DSN, см.
// catalog_match_db_test.go). Тайтл заводится свой и за собой убирается.

import (
	"context"
	"os"
	"testing"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// «Рик и Морти» с бою: kinopub знает точную длину спешлов пятого сезона,
// ЛордФильм раздаёт всем сериям одну оценку и знает серию, которой у
// kinopub нет. Площадки пишут по очереди, как их обходит добор серий.
func TestDBDurations_EstimateNeverReplacesExact(t *testing.T) {
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
	repo := NewCatalogRepo(pool)

	var id kernel.TitleID
	if err := pool.QueryRow(ctx, `
		INSERT INTO canonical_titles (kind, title, title_norm, year)
		VALUES ('series', 'Рик и Морти (проверка длительности)', 'рик и морти (проверка длительности)', 2013)
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM canonical_titles WHERE id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM external_refs WHERE provider_ref LIKE 'durtest-%'`)
	})
	kp := kernel.ProviderRef{ProviderID: "kinopub", ItemID: "durtest-kp"}
	lf := kernel.ProviderRef{ProviderID: "lordfilm", ItemID: "durtest-lf"}
	for _, r := range []kernel.ProviderRef{kp, lf} {
		refRow(t, repo, r.ProviderID, r.ItemID, id, 0, "")
	}

	exact := []dp.Episode{
		{Ref: kp, SeasonNo: 5, EpisodeNo: 10, DurationSec: 1320},
		{Ref: kp, SeasonNo: 5, EpisodeNo: 11, DurationSec: 722},
		{Ref: kp, SeasonNo: 5, EpisodeNo: 12, DurationSec: 391},
	}
	var estimate []dp.Episode
	for n := 10; n <= 13; n++ {
		estimate = append(estimate, dp.Episode{Ref: lf, SeasonNo: 5, EpisodeNo: n, DurationSec: 1380, DurationApprox: true})
	}
	durations := func() map[int]int {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT episode_no, COALESCE(duration_sec, 0) FROM canonical_episodes
			WHERE title_id = $1 AND season_no = 5`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[int]int{}
		for rows.Next() {
			var n, d int
			if err := rows.Scan(&n, &d); err != nil {
				t.Fatal(err)
			}
			out[n] = d
		}
		return out
	}
	want := map[int]int{10: 1320, 11: 722, 12: 391, 13: 1380}
	check := func(step string) {
		t.Helper()
		got := durations()
		for n, d := range want {
			if got[n] != d {
				t.Errorf("%s: s5e%d длится %d с, ожидалось %d", step, n, got[n], d)
			}
		}
	}

	// Оценка первой: серий ещё нет — её хватает, пока точной нет.
	if err := repo.UpsertEpisodes(ctx, id, estimate); err != nil {
		t.Fatal(err)
	}
	if got := durations(); got[11] != 1380 || got[13] != 1380 {
		t.Fatalf("оценка не легла в пустые серии: %v", got)
	}
	// Точная заменяет оценку, оценка следом её не трогает — и так на
	// каждом проходе, в каком бы порядке площадки ни пришли.
	for i := 0; i < 2; i++ {
		if err := repo.UpsertEpisodes(ctx, id, exact); err != nil {
			t.Fatal(err)
		}
		check("после kinopub")
		if err := repo.UpsertEpisodes(ctx, id, estimate); err != nil {
			t.Fatal(err)
		}
		check("после ЛордФильма")
	}

	// Площадка без длины (0) точную не стирает.
	if err := repo.UpsertEpisodes(ctx, id, []dp.Episode{{Ref: kp, SeasonNo: 5, EpisodeNo: 11}}); err != nil {
		t.Fatal(err)
	}
	check("после kinopub без длины")

	// Точная обновляет точную: kinopub перезалил серию.
	if err := repo.UpsertEpisodes(ctx, id, []dp.Episode{{Ref: kp, SeasonNo: 5, EpisodeNo: 11, DurationSec: 725}}); err != nil {
		t.Fatal(err)
	}
	want[11] = 725
	check("после перезаливки")
}
