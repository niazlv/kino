package postgres

// Пагинация поиска против настоящей базы. Выключена по умолчанию:
//
//	KINO_PG_DSN=postgres://kino:kino@localhost:55432/kino?sslmode=disable \
//	  go test ./internal/adapter/postgres/ -run DBSearch -v
//
// Читает только каталог стенда, ничего не пишет.

import (
	"context"
	"os"
	"testing"

	"github.com/niazleushkin/kino/internal/platform/pg"
)

func TestDB_SearchPages(t *testing.T) {
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

	// «принцесса» в каталоге стенда находит больше страницы — иначе тест
	// проверял бы пустоту; пропускаем, если стенд пуст.
	first, total, err := repo.Search(ctx, "принцесса", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total <= 30 {
		t.Skipf("в каталоге стенда «принцесса» даёт %d находок — нечего листать", total)
	}
	if len(first) != 30 {
		t.Fatalf("первая страница отдала %d, ждали 30", len(first))
	}

	second, total2, err := repo.Search(ctx, "принцесса", 30, 30)
	if err != nil {
		t.Fatal(err)
	}
	if total2 != total {
		t.Fatalf("total поплыл между страницами: %d и %d", total, total2)
	}
	if len(second) == 0 {
		t.Fatal("вторая страница пуста, хотя total больше")
	}

	// Страницы не пересекаются и не теряются: вторая продолжает первую.
	seen := map[int]bool{}
	for _, it := range first {
		seen[int(it.ID)] = true
	}
	for _, it := range second {
		if seen[int(it.ID)] {
			t.Fatalf("тайтл %d оказался на обеих страницах", it.ID)
		}
	}
}
