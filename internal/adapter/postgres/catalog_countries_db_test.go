package postgres

// Страны производства и вид против настоящей базы. Выключена по умолчанию:
//
//	KINO_PG_DSN=postgres://kino:kino@localhost:55432/kino?sslmode=disable \
//	  go test ./internal/adapter/postgres/ -run DB_Countries -v
//
// Пишет свой тайтл и прибирает за собой.

import (
	"context"
	"os"
	"testing"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

func TestDB_CountriesCarryAndSpecies(t *testing.T) {
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
	repo := NewCatalogRepo(pool)

	const ref = "countries-species-dbtest"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM canonical_titles WHERE id IN (
				SELECT title_id FROM external_refs WHERE provider_id = 'lordfilm' AND provider_ref = $1)`, ref)
	})

	// Заводится с странами: дорама — по стране, не по жанру.
	id, err := repo.UpsertTitle(ctx, dp.Title{
		Ref:       kernel.ProviderRef{ProviderID: "lordfilm", ItemID: ref},
		Kind:      "series",
		Title:     "Король земель / DB Countries Species",
		Year:      2023,
		Genres:    []string{"Мелодрама", "Комедия"},
		Countries: []string{"Корея Южная"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.Title(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Countries) != 1 || got.Countries[0] != "Корея Южная" {
		t.Fatalf("страны не доехали: %v", got.Countries)
	}
	if got.Species() != catalog.SpeciesDorama {
		t.Fatalf("вид %q, ожидался dorama", got.Species())
	}

	// Дорама — это и корейский фильм, не только сериал.
	film, err := repo.UpsertTitle(ctx, dp.Title{
		Ref:       kernel.ProviderRef{ProviderID: "lordfilm", ItemID: ref + "-film"},
		Kind:      "movie",
		Title:     "Паразиты / DB Countries Species",
		Year:      2019,
		Genres:    []string{"Драма", "Триллер"},
		Countries: []string{"Корея Южная"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err = repo.Title(ctx, film)
	if err != nil {
		t.Fatal(err)
	}
	if got.Species() != catalog.SpeciesDorama {
		t.Fatalf("у фильма вид %q, ожидался dorama", got.Species())
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM canonical_titles WHERE id IN (
				SELECT title_id FROM external_refs WHERE provider_id = 'lordfilm'
				AND provider_ref LIKE $1 || '%')`, ref)
	})

	// Хозяин перезаписывает пустое, но не стирает чужое: второй элемент той
	// же площади без стран не должен обнулить корейскую.
	if _, err := repo.UpsertTitle(ctx, dp.Title{
		Ref:   kernel.ProviderRef{ProviderID: "lordfilm", ItemID: ref + "-2"},
		Kind:  "series",
		Title: "Король земель / DB Countries Species",
		Year:  2023,
	}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Title(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Countries) != 1 || got.Countries[0] != "Корея Южная" {
		t.Fatalf("страны стёрлись вторым элементом: %v", got.Countries)
	}
}
