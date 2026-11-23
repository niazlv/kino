package postgres

// Даты тайтла против настоящей базы (KINO_PG_DSN, см.
// catalog_match_db_test.go). Тайтл заводится свой и за собой убирается.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// «Новинки» и «Свежие серии» не принадлежат хозяину метаданных: появилось
// произведение тогда, когда его первым выложил кто угодно, обновилось —
// когда его последним обновил кто угодно. Хозяин (kinopub) своими датами
// чужие не затирает ни в одну сторону.
func TestDBDates_AnySourceNotOwner(t *testing.T) {
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
	repo := NewCatalogRepo(pool).WithPriorities(map[string]int{"kinopub": 100, "lordfilm": 40, "animego_me": 60})

	var id kernel.TitleID
	if err := pool.QueryRow(ctx, `
		INSERT INTO canonical_titles (kind, title, title_norm, year)
		VALUES ('series', 'Даты (проверка новинок)', 'даты (проверка новинок)', 2020)
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM canonical_titles WHERE id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM external_refs WHERE provider_ref LIKE 'datestest-%'`)
	})
	kp := kernel.ProviderRef{ProviderID: "kinopub", ItemID: "datestest-kp"}
	lf := kernel.ProviderRef{ProviderID: "lordfilm", ItemID: "datestest-lf"}
	ag := kernel.ProviderRef{ProviderID: "animego_me", ItemID: "datestest-ag"}
	for _, r := range []kernel.ProviderRef{kp, lf, ag} {
		refRow(t, repo, r.ProviderID, r.ItemID, id, 0, "")
	}
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, time.UTC) }
	upsert := func(ref kernel.ProviderRef, created, updated time.Time) {
		t.Helper()
		got, err := repo.UpsertTitle(ctx, dp.Title{
			Ref: ref, Kind: "series", Title: "Даты (проверка новинок)", Year: 2020,
			CreatedAt: created, UpdatedAt: updated,
		})
		if err != nil || got != id {
			t.Fatalf("%s: %v (тайтл %d, ждали %d)", ref.ProviderID, err, got, id)
		}
	}
	dates := func() (time.Time, time.Time) {
		t.Helper()
		var c, u time.Time
		if err := pool.QueryRow(ctx, `SELECT source_created_at, source_updated_at FROM canonical_titles WHERE id = $1`,
			id).Scan(&c, &u); err != nil {
			t.Fatal(err)
		}
		return c, u
	}

	// Хозяин первым: его даты.
	upsert(kp, day(2024, 3, 1), day(2025, 1, 1))
	// Площадка ниже по приоритету выложила раньше и обновила позже.
	upsert(lf, day(2023, 6, 1), day(2026, 11, 15))
	if c, u := dates(); !c.Equal(day(2023, 6, 1)) || !u.Equal(day(2026, 11, 15)) {
		t.Fatalf("после второй площадки: появился %v, обновлён %v", c, u)
	}
	// Хозяин снова: свою позднюю дату появления и старое обновление не
	// навязывает.
	upsert(kp, day(2024, 3, 1), day(2025, 2, 1))
	if c, u := dates(); !c.Equal(day(2023, 6, 1)) || !u.Equal(day(2026, 11, 15)) {
		t.Fatalf("хозяин затёр даты: появился %v, обновлён %v", c, u)
	}
	// Площадка без даты появления: дата обновления — тоже граница
	// появления (обновить можно только выложенное).
	upsert(ag, time.Time{}, day(2022, 1, 1))
	if c, _ := dates(); !c.Equal(day(2022, 1, 1)) {
		t.Fatalf("дата обновления как граница появления: %v", c)
	}

	// «Свежие серии»: самый свежий сериал — первым.
	if _, err := pool.Exec(ctx, `UPDATE canonical_titles SET source_updated_at = now() + interval '1 day' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	pg1, err := repo.Browse(ctx, BrowseQuery{Kind: "series", Sort: "updated", Limit: 5})
	if err != nil || len(pg1.Items) == 0 || pg1.Items[0].ID != id {
		t.Fatalf("свежие серии: %v %+v", err, pg1.Items)
	}
}
