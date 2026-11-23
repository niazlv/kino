package postgres

// Лишние серии закрытого сезона против настоящей базы (KINO_PG_DSN, см.
// catalog_match_db_test.go). Тайтлы заводятся свои и за собой убираются.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

type extrasDB struct {
	t    *testing.T
	ctx  context.Context
	repo *CatalogRepo
	n    int
}

func newExtrasDB(t *testing.T) *extrasDB {
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
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM canonical_titles WHERE title LIKE '%(проверка лишних серий)'`)
		_, _ = pool.Exec(ctx, `DELETE FROM external_refs WHERE provider_ref LIKE 'extrastest-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE login = 'extrastest'`)
	})
	repo := NewCatalogRepo(pool).
		WithPriorities(map[string]int{"kinopub": 100, "lordfilm": 40, "yummy": 36}).
		WithAnimeProviders("yummy")
	return &extrasDB{t: t, ctx: ctx, repo: repo}
}

// title заводит тайтл с привязками kinopub и ЛордФильма.
func (d *extrasDB) title() (id kernel.TitleID, kp, lf kernel.ProviderRef) {
	d.t.Helper()
	d.n++
	name := fmt.Sprintf("Рик и Морти %d (проверка лишних серий)", d.n)
	if err := d.repo.db.QueryRow(d.ctx, `
		INSERT INTO canonical_titles (kind, title, title_norm, year)
		VALUES ('series', $1, lower($1), 2013) RETURNING id`, name).Scan(&id); err != nil {
		d.t.Fatal(err)
	}
	kp = kernel.ProviderRef{ProviderID: "kinopub", ItemID: fmt.Sprintf("extrastest-kp%d", d.n)}
	lf = kernel.ProviderRef{ProviderID: "lordfilm", ItemID: fmt.Sprintf("extrastest-lf%d", d.n)}
	refRow(d.t, d.repo, kp.ProviderID, kp.ItemID, id, 0, "")
	refRow(d.t, d.repo, lf.ProviderID, lf.ItemID, id, 0, "")
	return id, kp, lf
}

// eps — серии площадки: season → число серий.
func eps(ref kernel.ProviderRef, seasons map[int]int) []dp.Episode {
	var out []dp.Episode
	for s, n := range seasons {
		for e := 1; e <= n; e++ {
			out = append(out, dp.Episode{Ref: ref, SeasonNo: s, EpisodeNo: e})
		}
	}
	return out
}

func (d *extrasDB) upsert(id kernel.TitleID, e []dp.Episode) {
	d.t.Helper()
	if err := d.repo.UpsertEpisodes(d.ctx, id, e); err != nil {
		d.t.Fatal(err)
	}
}

// count — серий в сезоне канонического тайтла.
func (d *extrasDB) count(id kernel.TitleID, season int) int {
	d.t.Helper()
	var n int
	if err := d.repo.db.QueryRow(d.ctx, `
		SELECT count(*) FROM canonical_episodes WHERE title_id = $1 AND season_no = $2`,
		id, season).Scan(&n); err != nil {
		d.t.Fatal(err)
	}
	return n
}

func (d *extrasDB) episode(id kernel.TitleID, s, e int) (kernel.EpisodeID, bool) {
	d.t.Helper()
	var ep kernel.EpisodeID
	err := d.repo.db.QueryRow(d.ctx, `
		SELECT id FROM canonical_episodes WHERE title_id = $1 AND season_no = $2 AND episode_no = $3`,
		id, s, e).Scan(&ep)
	return ep, err == nil
}

// kinopub первым: 5×13 ЛордФильма не заводится, его 6×3 в последнем
// сезоне — заводится, свои 5×1–12 он предлагает как обычно.
func TestDBExtras_ClosedSeasonTailNotCreated(t *testing.T) {
	d := newExtrasDB(t)
	id, kp, lf := d.title()
	d.upsert(id, eps(kp, map[int]int{5: 12, 6: 2}))
	d.upsert(id, eps(lf, map[int]int{5: 13, 6: 3}))

	if n := d.count(id, 5); n != 12 {
		t.Errorf("в пятом сезоне %d серий, ожидалось 12", n)
	}
	if n := d.count(id, 6); n != 3 {
		t.Errorf("в шестом (последнем) сезоне %d серий, ожидалось 3 — новая серия ЛордФильма потерялась", n)
	}
	var offers int
	if err := d.repo.db.QueryRow(d.ctx, `
		SELECT count(*) FROM episode_offers o JOIN canonical_episodes e ON e.id = o.episode_id
		WHERE e.title_id = $1 AND e.season_no = 5 AND o.provider_id = 'lordfilm'`, id).Scan(&offers); err != nil {
		t.Fatal(err)
	}
	if offers != 12 {
		t.Errorf("у ЛордФильма %d предложений в пятом сезоне, ожидалось 12", offers)
	}
}

// ЛордФильм успел раньше kinopub: 5×13 заведена, kinopub закрывает сезон —
// лишняя серия убирается, если её никто не смотрел; иначе остаётся.
func TestDBExtras_PrunedWhenSeasonCloses(t *testing.T) {
	d := newExtrasDB(t)

	id, kp, lf := d.title()
	d.upsert(id, eps(lf, map[int]int{5: 13}))
	if n := d.count(id, 5); n != 13 {
		t.Fatalf("без kinopub в пятом сезоне %d серий, ожидалось 13", n)
	}
	// Пятый сезон у kinopub пока последний — сравнивать не с чем.
	d.upsert(id, eps(kp, map[int]int{5: 12}))
	if n := d.count(id, 5); n != 13 {
		t.Fatalf("незакрытый сезон потерял серию: %d", n)
	}
	d.upsert(id, eps(kp, map[int]int{6: 1}))
	if _, ok := d.episode(id, 5, 13); ok {
		t.Error("5×13 осталась после того, как kinopub закрыл пятый сезон")
	}
	// Следующий проход ЛордФильма её не возвращает.
	d.upsert(id, eps(lf, map[int]int{5: 13, 6: 1}))
	if _, ok := d.episode(id, 5, 13); ok {
		t.Error("5×13 вернулась со следующим проходом ЛордФильма")
	}

	// Чей-то прогресс держит серию.
	watched, kp2, lf2 := d.title()
	d.upsert(watched, eps(lf2, map[int]int{5: 13}))
	ep, _ := d.episode(watched, 5, 13)
	var user kernel.UserID
	if err := d.repo.db.QueryRow(d.ctx, `
		INSERT INTO users (login, display_name, role) VALUES ('extrastest', 'проверка', 'user')
		RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := d.repo.db.Exec(d.ctx, `
		INSERT INTO watch_progress (user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts)
		VALUES ($1, $2, 60000, 1380000, 'in_progress', now(), now())`, user, ep); err != nil {
		t.Fatal(err)
	}
	d.upsert(watched, eps(kp2, map[int]int{5: 12, 6: 1}))
	if _, ok := d.episode(watched, 5, 13); !ok {
		t.Fatal("серия с прогрессом удалена")
	}
	// И продолжает получать предложение ЛордФильма — иначе её нечем играть.
	if _, err := d.repo.db.Exec(d.ctx, `DELETE FROM episode_offers WHERE episode_id = $1`, ep); err != nil {
		t.Fatal(err)
	}
	d.upsert(watched, eps(lf2, map[int]int{5: 13}))
	var offers int
	if err := d.repo.db.QueryRow(d.ctx, `SELECT count(*) FROM episode_offers WHERE episode_id = $1`, ep).Scan(&offers); err != nil {
		t.Fatal(err)
	}
	if offers != 1 {
		t.Errorf("у оставленной серии %d предложений, ожидалось 1", offers)
	}
}

// Аниме-площадка нумерует по-своему: её серия за концом сезона kinopub
// остаётся, даже если ЛордФильм предлагает ту же.
func TestDBExtras_AnimeOfferKeepsEpisode(t *testing.T) {
	d := newExtrasDB(t)
	id, kp, lf := d.title()
	ym := kernel.ProviderRef{ProviderID: "yummy", ItemID: "extrastest-ym"}
	refRow(t, d.repo, ym.ProviderID, ym.ItemID, id, 5, "")

	d.upsert(id, eps(kp, map[int]int{5: 12, 6: 1}))
	d.upsert(id, []dp.Episode{{Ref: ym, SeasonNo: 1, EpisodeNo: 13}})
	d.upsert(id, eps(lf, map[int]int{5: 13}))
	if _, ok := d.episode(id, 5, 13); !ok {
		t.Fatal("серия аниме-площадки удалена")
	}
	var lfOffer bool
	if err := d.repo.db.QueryRow(d.ctx, `
		SELECT EXISTS (SELECT 1 FROM episode_offers o JOIN canonical_episodes e ON e.id = o.episode_id
		               WHERE e.title_id = $1 AND e.season_no = 5 AND e.episode_no = 13
		                 AND o.provider_id = 'lordfilm')`, id).Scan(&lfOffer); err != nil {
		t.Fatal(err)
	}
	if !lfOffer {
		t.Error("ЛордФильм не предложил уже заведённую серию")
	}
}
