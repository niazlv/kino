package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

func TestDBProgress_DelayedWriteCannotUndoRewind(t *testing.T) {
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
	var user kernel.UserID
	var title kernel.TitleID
	var episode kernel.EpisodeID
	if err := pool.QueryRow(ctx, `INSERT INTO users(login,display_name,role) VALUES($1,'Проверка прогресса','user') RETURNING id`,
		fmt.Sprintf("progress-test-%d", time.Now().UnixNano())).Scan(&user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user) })
	if err := pool.QueryRow(ctx, `INSERT INTO canonical_titles(kind,title,title_norm) VALUES('movie','Проверка прогресса','проверка прогресса') RETURNING id`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM canonical_titles WHERE id=$1`, title) })
	if err := pool.QueryRow(ctx, `INSERT INTO canonical_episodes(title_id,season_no,episode_no) VALUES($1,0,0) RETURNING id`, title).Scan(&episode); err != nil {
		t.Fatal(err)
	}
	repo := NewProgressRepo(pool)
	now := time.Now().Truncate(time.Millisecond)
	old := playback.Progress{UserID: user, EpisodeID: episode, PositionMS: 900_000, DurationMS: 1_800_000,
		Status: playback.StatusInProgress, DeviceID: "session-1", ClientTS: now, UpdatedAt: now}
	latest := old
	latest.PositionMS = 120_500
	latest.ClientTS = now.Add(time.Second)
	latest.UpdatedAt = latest.ClientTS
	// Это два уже рассчитанных отчёта: старый запрос дошёл до SQL последним.
	for _, p := range []playback.Progress{old, latest, old} {
		if err := repo.Upsert(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.Get(ctx, user, episode)
	if err != nil {
		t.Fatal(err)
	}
	if got.PositionMS != latest.PositionMS || !got.ClientTS.Equal(latest.ClientTS) {
		t.Fatalf("late write undid rewind: %+v", got)
	}
	// Часы разных устройств не сравниваются.
	old.DeviceID = "session-2"
	if err := repo.Upsert(ctx, old); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, user, episode)
	if err != nil || got.DeviceID != "session-2" {
		t.Fatalf("different device rejected: %+v %v", got, err)
	}
}

// Регрессия: отметка поверх строки, которую раньше писал плеер, оставляла
// в ней его устройство, и отчёт этого же плеера с позицией до отметки —
// ноль открытой и не включённой серии — снимал галочку целиком.
func TestDBProgress_ManualMarkSurvivesSameDeviceEcho(t *testing.T) {
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
	var user kernel.UserID
	var title kernel.TitleID
	var episode kernel.EpisodeID
	if err := pool.QueryRow(ctx, `INSERT INTO users(login,display_name,role) VALUES($1,'Проверка отметки','user') RETURNING id`,
		fmt.Sprintf("mark-test-%d", time.Now().UnixNano())).Scan(&user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user) })
	if err := pool.QueryRow(ctx, `INSERT INTO canonical_titles(kind,title,title_norm) VALUES('series','Проверка отметки','проверка отметки') RETURNING id`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM canonical_titles WHERE id=$1`, title) })
	if err := pool.QueryRow(ctx, `INSERT INTO canonical_episodes(title_id,season_no,episode_no,duration_sec) VALUES($1,1,1,1422) RETURNING id`, title).Scan(&episode); err != nil {
		t.Fatal(err)
	}
	repo := NewProgressRepo(pool)
	earlier := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	if err := repo.Upsert(ctx, playback.Progress{UserID: user, EpisodeID: episode, DurationMS: 1_422_000,
		Status: playback.StatusInProgress, DeviceID: "session-1", ClientTS: earlier, UpdatedAt: earlier}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetWatched(ctx, user, episode, true); err != nil {
		t.Fatal(err)
	}
	// То, что делает progress.Service.Report с отчётом плеера.
	cur, err := repo.Get(ctx, user, episode)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	echo := cur.Merge(playback.Progress{UserID: user, EpisodeID: episode, DurationMS: 1_422_000,
		DeviceID: "session-1", ClientTS: now, UpdatedAt: now})
	if err := repo.Upsert(ctx, echo); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, user, episode)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != playback.StatusCompleted {
		t.Fatalf("manual mark lost to the player's echo: %+v", got)
	}
}
