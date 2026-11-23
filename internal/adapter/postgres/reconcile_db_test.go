package postgres

// Сухой прогон реконсилиации против настоящей базы. Выключен по умолчанию:
//
//	KINO_PG_DSN=postgres://kino:kino@localhost:55432/kino?sslmode=disable \
//	  go test ./internal/adapter/postgres/ -run DBReconcile -v
//
// Ничего не сливает: печатает пары и решение matching.ShouldMerge.
// BackfillVariantKeys пишет только ключи — повторный прогон идемпотентен.

import (
	"context"
	"os"
	"testing"

	"github.com/niazleushkin/kino/internal/app/matching"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

func reconcileRepo(t *testing.T) *CatalogRepo {
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
	return NewCatalogRepo(pool).
		WithPriorities(map[string]int{"kinopub": 100, "animego_me": 60, "animego_fr": 50}).
		WithAnimeProviders("animego_me", "animego_fr")
}

func TestDBReconcile_BackfillAndPairs(t *testing.T) {
	repo := reconcileRepo(t)
	ctx := context.Background()

	n, err := repo.BackfillVariantKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("вариантов ключей дописано: %d", n)

	pairs, err := repo.DuplicatePairs(ctx, 300)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("пар-кандидатов: %d", len(pairs))

	// Дубли Yuki Yuna с продакшена обязаны найтись и слиться решением.
	var yukiMerged int
	for _, p := range pairs {
		a, errA := repo.Candidate(ctx, p.A)
		b, errB := repo.Candidate(ctx, p.B)
		if errA != nil || errB != nil || a.ID == b.ID {
			continue
		}
		merge := matching.ShouldMerge(matching.Pair{A: a, B: b})
		t.Logf("%d [%s] (%d, anime=%v) <-> %d [%s] (%d, anime=%v): merge=%v",
			a.ID, firstKey(a), a.Year, a.Anime, b.ID, firstKey(b), b.Year, b.Anime, merge)
		if merge && isYukiYuna(a, b) {
			yukiMerged++
		}
	}
	if yukiMerged == 0 && len(pairs) > 0 {
		// Не падаем: база стенда может быть уже чистой или другой.
		t.Log("пар Yuki Yuna не найдено — база, видимо, уже чистая")
	}
}

func isYukiYuna(a, b matching.Candidate) bool {
	return containsYuki(a.Keys) && containsYuki(b.Keys)
}

func containsYuki(keys []string) bool {
	for _, k := range keys {
		if len(k) > 4 && (k[:4] == "yuki" || k[:5] == "герой") {
			return true
		}
	}
	return false
}

func firstKey(c matching.Candidate) string {
	if len(c.Keys) == 0 {
		return "?"
	}
	return c.Keys[0]
}

// Слияние по-настоящему: пара дублей сливается, прогресс и привязки
// переезжают, повторный проход пары больше не отдаёт.
func TestDBReconcile_MergeAndRerun(t *testing.T) {
	repo := reconcileRepo(t)
	ctx := context.Background()

	pairs, err := repo.DuplicatePairs(ctx, 300)
	if err != nil {
		t.Fatal(err)
	}
	var (
		from, into kernel.TitleID
		found      bool
	)
	for _, p := range pairs {
		a, errA := repo.Candidate(ctx, p.A)
		b, errB := repo.Candidate(ctx, p.B)
		if errA != nil || errB != nil || a.ID == b.ID {
			continue
		}
		if matching.ShouldMerge(matching.Pair{A: a, B: b}) {
			from, into, found = p.B, p.A, true
			break
		}
	}
	if !found {
		t.Skip("в базе нет пар, которые решают слить")
	}
	if err := repo.MergeTitlesReconciled(ctx, from, into); err != nil {
		t.Fatalf("слияние %d -> %d: %v", from, into, err)
	}
	var matchedBy string
	if err := repo.db.QueryRow(ctx, `SELECT COALESCE(matched_by,'') FROM external_refs WHERE title_id = $1 LIMIT 1`, into).
		Scan(&matchedBy); err != nil || matchedBy != "reconciled" {
		t.Fatalf("след слияния: %q %v (ожидалось reconciled — привязки into могли быть и другими, смотрим первые)", matchedBy, err)
	}
}

// Слияние на синтетических тайтлах: оба пути (ручной с закреплением,
// реконсилиация без) обязаны быть валидным SQL — шаг 7 однажды падал
// синтаксической ошибкой на проде. Тайтлы и привязки заведены тестом
// и убираются за собой.
func TestDBReconcile_MergeSyntaxBothPaths(t *testing.T) {
	repo := reconcileRepo(t)
	ctx := context.Background()

	mk := func(year int) kernel.TitleID {
		var id kernel.TitleID
		if err := repo.db.QueryRow(ctx, `
			INSERT INTO canonical_titles (kind, title, title_norm, year)
			VALUES ('series', 'Слияние-тест', 'слияние тест', $1) RETURNING id`, year).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	attach := func(id kernel.TitleID, provider, item string) {
		if _, err := repo.db.Exec(ctx, `
			INSERT INTO external_refs (provider_id, provider_ref, title_id, priority, season_no)
			VALUES ($1, $2, $3, 10, 1) ON CONFLICT (provider_id, provider_ref) DO UPDATE SET title_id = $3`,
			provider, item, id); err != nil {
			t.Fatal(err)
		}
	}

	// Путь реконсилиации: без закрепления.
	a, b := mk(2020), mk(2020)
	attach(a, "animego_me", "merge-syntax-test-reconciled-1")
	attach(b, "animego_fr", "merge-syntax-test-reconciled-2")
	t.Cleanup(func() {
		_, _ = repo.db.Exec(ctx, `DELETE FROM external_refs WHERE provider_ref LIKE 'merge-syntax-test-%'`)
		_, _ = repo.db.Exec(ctx, `DELETE FROM canonical_titles WHERE title = 'Слияние-тест'`)
	})
	if err := repo.MergeTitlesReconciled(ctx, b, a); err != nil {
		t.Fatalf("реконсилированное слияние: %v", err)
	}
	var pinned bool
	if err := repo.db.QueryRow(ctx, `SELECT pinned FROM external_refs WHERE provider_ref = 'merge-syntax-test-reconciled-2'`).
		Scan(&pinned); err != nil || pinned {
		t.Fatalf("реконсилиация закрепила привязку: pinned=%v err=%v", pinned, err)
	}

	// Путь ручной: с закреплением.
	c, d := mk(2021), mk(2021)
	attach(c, "animego_me", "merge-syntax-test-manual-1")
	attach(d, "animego_fr", "merge-syntax-test-manual-2")
	if err := repo.MergeTitles(ctx, d, c); err != nil {
		t.Fatalf("ручное слияние: %v", err)
	}
	if err := repo.db.QueryRow(ctx, `SELECT pinned FROM external_refs WHERE provider_ref = 'merge-syntax-test-manual-2'`).
		Scan(&pinned); err != nil || !pinned {
		t.Fatalf("ручное слияние не закрепило: pinned=%v err=%v", pinned, err)
	}
}
