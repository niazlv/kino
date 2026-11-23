package postgres

// Автослежение против настоящей базы (KINO_PG_DSN, см.
// catalog_match_db_test.go). Тайтлы заводятся свои и за собой убираются:
// отметки и серии уходят каскадом.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

type followFixture struct {
	repo *MarksRepo
	user kernel.UserID
	// title заводит тайтл с одной серией и отдаёт id тайтла и серии.
	title func(kind string, finished bool) (kernel.TitleID, kernel.EpisodeID)
}

func followDB(t *testing.T) followFixture {
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
	f := followFixture{repo: NewMarksRepo(pool)}
	if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&f.user); err != nil {
		t.Skip("в базе нет пользователей")
	}
	var made []kernel.TitleID
	t.Cleanup(func() {
		for _, id := range made {
			_, _ = pool.Exec(ctx, `DELETE FROM canonical_titles WHERE id = $1`, id)
		}
	})
	f.title = func(kind string, finished bool) (kernel.TitleID, kernel.EpisodeID) {
		t.Helper()
		var id kernel.TitleID
		if err := pool.QueryRow(ctx, `
			INSERT INTO canonical_titles (kind, title, title_norm, finished)
			VALUES ($1, 'Проверка слежения', 'проверка слежения', $2) RETURNING id`,
			kind, finished).Scan(&id); err != nil {
			t.Fatal(err)
		}
		made = append(made, id)
		var ep kernel.EpisodeID
		if err := pool.QueryRow(ctx, `
			INSERT INTO canonical_episodes (title_id, season_no, episode_no)
			VALUES ($1, 1, 1) RETURNING id`, id).Scan(&ep); err != nil {
			t.Fatal(err)
		}
		return id, ep
	}
	return f
}

func (f followFixture) marks(t *testing.T, id kernel.TitleID) (account.Marks, bool) {
	t.Helper()
	var n int
	if err := f.repo.db.QueryRow(context.Background(),
		`SELECT count(*) FROM user_titles WHERE user_id = $1 AND title_id = $2`, f.user, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	m, err := f.repo.Get(context.Background(), f.user, id)
	if err != nil {
		t.Fatal(err)
	}
	return m, n > 0
}

func (f followFixture) unfollowed(t *testing.T, id kernel.TitleID) bool {
	t.Helper()
	var at *time.Time
	if err := f.repo.db.QueryRow(context.Background(),
		`SELECT unfollowed_at FROM user_titles WHERE user_id = $1 AND title_id = $2`, f.user, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at != nil
}

// Досмотренная серия идущего сериала ставит «Слежу»; завершённый сериал и
// фильм — нет: новых серий у них не будет.
func TestDBFollow_OnlyOngoingSeries(t *testing.T) {
	f := followDB(t)
	ctx := context.Background()

	ongoing, ep := f.title("series", false)
	if err := f.repo.FollowByEpisode(ctx, f.user, ep); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.marks(t, ongoing); !m.Following {
		t.Fatal("идущий сериал не встал в «Слежу»")
	}

	done, ep := f.title("series", true)
	if err := f.repo.FollowByEpisode(ctx, f.user, ep); err != nil {
		t.Fatal(err)
	}
	movie, _ := f.title("movie", false)
	if err := f.repo.FollowByTitle(ctx, f.user, movie); err != nil {
		t.Fatal(err)
	}
	for _, id := range []kernel.TitleID{done, movie} {
		if _, ok := f.marks(t, id); ok {
			t.Fatalf("тайтл %d получил строку отметок, а не должен был", id)
		}
	}
}

// Снятое руками «Слежу» автоматика не возвращает; поставленное руками
// снова — стирает память о снятии.
func TestDBFollow_RespectsManualUnfollow(t *testing.T) {
	f := followDB(t)
	ctx := context.Background()
	id, ep := f.title("series", false)

	set := func(following bool) {
		t.Helper()
		if err := f.repo.Set(ctx, account.Marks{UserID: f.user, TitleID: id, Following: following}); err != nil {
			t.Fatal(err)
		}
	}
	set(true)
	set(false)
	if !f.unfollowed(t, id) {
		t.Fatal("снятие «Слежу» не запомнилось")
	}
	if err := f.repo.FollowByEpisode(ctx, f.user, ep); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.FollowByTitle(ctx, f.user, id); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.marks(t, id); m.Following {
		t.Fatal("автоматика вернула снятое руками слежение")
	}

	set(true)
	if f.unfollowed(t, id) {
		t.Fatal("явное «Слежу» не стёрло память о снятии")
	}
}

// «Буду смотреть» и «скрыть» — не снятие слежения: отложенное, которое
// начали смотреть, встаёт в «Слежу», а «Буду смотреть» остаётся.
func TestDBFollow_WatchlistIsNotUnfollow(t *testing.T) {
	f := followDB(t)
	ctx := context.Background()
	id, _ := f.title("series", false)

	if err := f.repo.Set(ctx, account.Marks{UserID: f.user, TitleID: id, Watchlist: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Set(ctx, account.Marks{UserID: f.user, TitleID: id, Watchlist: false}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Set(ctx, account.Marks{UserID: f.user, TitleID: id, Watchlist: true}); err != nil {
		t.Fatal(err)
	}
	if f.unfollowed(t, id) {
		t.Fatal("переключение «Буду смотреть» записалось как снятие слежения")
	}
	if err := f.repo.FollowByTitle(ctx, f.user, id); err != nil {
		t.Fatal(err)
	}
	m, _ := f.marks(t, id)
	if !m.Following || !m.Watchlist {
		t.Fatalf("отметки после досмотра: %+v, want following и watchlist", m)
	}
}

// Уже стоящее слежение не трогается: каждая досмотренная серия иначе
// переставляла бы тайтл в списке «Я смотрю».
func TestDBFollow_KeepsExistingFollowTime(t *testing.T) {
	f := followDB(t)
	ctx := context.Background()
	id, ep := f.title("series", false)

	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	if err := f.repo.SetFollowing(ctx, f.user, id, true, at); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.FollowByEpisode(ctx, f.user, ep); err != nil {
		t.Fatal(err)
	}
	var got time.Time
	if err := f.repo.db.QueryRow(ctx,
		`SELECT updated_at FROM user_titles WHERE user_id = $1 AND title_id = $2`, f.user, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(at) {
		t.Fatalf("время отметки сдвинуто: %v, want %v", got, at)
	}
}
