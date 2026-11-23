package postgres

// Порядок поиска против настоящей базы (KINO_PG_DSN, см.
// catalog_match_db_test.go). Тайтлы заводятся свои, с выдуманным словом,
// которого в каталоге нет, и за собой убираются.

import (
	"context"
	"os"
	"testing"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

func TestDBSearch_PopularFirstWithinMatchKind(t *testing.T) {
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
	repo := NewCatalogRepo(pool).WithPriorities(map[string]int{"kinopub": 100})

	var ids []kernel.TitleID
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = pool.Exec(ctx, `DELETE FROM canonical_titles WHERE id = $1`, id)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM external_refs WHERE provider_ref LIKE 'srchtest-%'`)
	})
	put := func(ref, title string, year int, pop int64) kernel.TitleID {
		t.Helper()
		id, err := repo.UpsertTitle(ctx, dp.Title{
			Ref:  kernel.ProviderRef{ProviderID: "kinopub", ItemID: "srchtest-" + ref},
			Kind: "movie", Title: title, Year: year, Popularity: pop,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		return id
	}
	exact := put("exact", "Зюзябра / Zyuzyabra", 1990, 1_000)
	sequel := put("sequel", "Зюзябра: Возвращение / Zyuzyabra Returns", 2001, 50_000)
	midWord := put("mid", "Великая зюзябра", 2010, 200)
	unknown := put("unknown", "Зюзябра и все-все-все", 2020, 0)
	inside := put("inside", "Мегазюзябра", 2015, 900_000)

	search := func(q string) []kernel.TitleID {
		t.Helper()
		items, _, err := repo.Search(ctx, q, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out []kernel.TitleID
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out
	}
	same := func(q string, got, want []kernel.TitleID) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%q: %v, want %v", q, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%q: %v, want %v", q, got, want)
			}
		}
	}

	// Начало названия или слова — одна ступень, внутри неё популярные
	// первыми; точное название весит втрое (1 000 → 3 000), но
	// продолжение с 50 000 всё равно впереди. «Мега…» — запрос внутри
	// слова: после всех, хоть и самый популярный.
	same("зюзябра", search("зюзябра"), []kernel.TitleID{sequel, exact, midWord, unknown, inside})

	// Повторный обход с меньшим числом популярность не уменьшает: шкала
	// общая, берётся наибольшее известное.
	put("exact", "Зюзябра / Zyuzyabra", 1990, 10)
	var pop int64
	if err := pool.QueryRow(ctx, `SELECT popularity FROM canonical_titles WHERE id = $1`, exact).Scan(&pop); err != nil {
		t.Fatal(err)
	}
	if pop != 1_000 {
		t.Errorf("популярность после обхода с меньшим числом: %d, want 1000", pop)
	}

	// Регистр не важен, а знаки между словами — по ключам названий:
	// витринное «Зюзябра: Возвращение» с двоеточием, набирают без него.
	// Дальше идут лишь похожие (одно слово из двух) — каталог настоящий,
	// и «Возвращение короля» там тоже есть.
	got := search("ЗЮЗЯБРА ВОЗВРАЩЕНИЕ")
	if len(got) == 0 || got[0] != sequel {
		t.Fatalf("«ЗЮЗЯБРА ВОЗВРАЩЕНИЕ»: %v, первым ждали %d", got, sequel)
	}
	for _, id := range got[1:] {
		if id == exact || id == midWord || id == unknown || id == inside {
			t.Errorf("«ЗЮЗЯБРА ВОЗВРАЩЕНИЕ» нашло %d — в нём нет «возвращения»", id)
		}
	}
}
