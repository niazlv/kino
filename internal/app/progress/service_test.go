package progress

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

type fakeRepo struct {
	store map[kernel.EpisodeID]playback.Progress
}

func (f fakeRepo) Get(_ context.Context, _ kernel.UserID, e kernel.EpisodeID) (playback.Progress, error) {
	p, ok := f.store[e]
	if !ok {
		return playback.Progress{}, kernel.ErrNotFound
	}
	return p, nil
}
func (f fakeRepo) Upsert(_ context.Context, p playback.Progress) error {
	f.store[p.EpisodeID] = p
	return nil
}

func (f fakeRepo) SetWatched(_ context.Context, _ kernel.UserID,
	e kernel.EpisodeID, watched bool) error {
	if !watched {
		// Снятие удаляет запись: «не смотрел» и «смотрел нисколько»
		// обязаны быть неотличимы.
		delete(f.store, e)
		return nil
	}
	f.store[e] = playback.Progress{EpisodeID: e, Status: playback.StatusCompleted}
	return nil
}

func (f fakeRepo) SetSeasonWatched(_ context.Context, _ kernel.UserID,
	_ kernel.TitleID, _ int, _ bool) (int64, error) {
	return 0, nil
}

type fakeList struct {
	called    kernel.UserID
	gotLimit  int
	gotOffset int
}

func (f *fakeList) Continue(_ context.Context, u kernel.UserID, limit, offset int) ([]Item, int, error) {
	f.called, f.gotLimit, f.gotOffset = u, limit, offset
	return []Item{{EpisodeID: 1, Title: "Тест"}}, 42, nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func newSvc() (*Service, *fakeList) {
	l := &fakeList{}
	return NewService(
		fakeRepo{store: map[kernel.EpisodeID]playback.Progress{}}, l, nil,
		fixedClock{time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)},
	), l
}

func user(id kernel.UserID) auth.Actor {
	return auth.Actor{UserID: id, Role: account.RoleUser, Space: account.SpaceUser}
}

func TestReport_MergesFarthestPositionWithinWindow(t *testing.T) {
	svc, _ := newSvc()
	ctx, base := context.Background(), time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	if _, err := svc.Report(ctx, user(1), playback.Progress{
		UserID: 1, EpisodeID: 100, PositionMS: 900_000, DurationMS: 2_700_000, ClientTS: base,
	}); err != nil {
		t.Fatal(err)
	}
	// Второе устройство отчиталось меньшей позицией, но в пределах окна.
	got, err := svc.Report(ctx, user(1), playback.Progress{
		UserID: 1, EpisodeID: 100, PositionMS: 300_000, DurationMS: 2_700_000,
		ClientTS: base.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.PositionMS != 900_000 {
		t.Fatalf("position = %d, want 900000 (дальняя позиция побеждает в окне)", got.PositionMS)
	}
}

func TestReport_MarksCompletedNearEnd(t *testing.T) {
	svc, _ := newSvc()
	got, err := svc.Report(context.Background(), user(1), playback.Progress{
		UserID: 1, EpisodeID: 101, PositionMS: 2_640_000, DurationMS: 2_700_000,
		ClientTS: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != playback.StatusCompleted {
		t.Fatalf("status = %q, want completed (<2 мин до конца)", got.Status)
	}
}

// Главный инвариант продукта: свой прогресс у каждого, чужой недоступен.
func TestReport_CannotWriteSomeoneElsesProgress(t *testing.T) {
	svc, _ := newSvc()
	_, err := svc.Report(context.Background(), user(1), playback.Progress{
		UserID: 2, EpisodeID: 100, PositionMS: 1, DurationMS: 100, ClientTS: time.Now(),
	})
	if !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("запись в чужой прогресс не отклонена: %v", err)
	}
}

func TestGet_ForbiddenForOtherUser(t *testing.T) {
	svc, _ := newSvc()
	_, err := svc.Get(context.Background(), user(1), 2, 100, auth.DiagnosticGrant{})
	if !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("чтение чужого прогресса не отклонено: %v", err)
	}
}

func TestContinue_AlwaysScopedToActor(t *testing.T) {
	svc, list := newSvc()
	if _, _, err := svc.Continue(context.Background(), user(7), 30, 60); err != nil {
		t.Fatal(err)
	}
	if list.called != 7 {
		t.Fatalf("запрошен список пользователя %d, а не актора", list.called)
	}
	// Окно идёт насквозь: размер страницы решает транспорт, use case
	// только передаёт его в хранилище.
	if list.gotLimit != 30 || list.gotOffset != 60 {
		t.Fatalf("limit/offset искажены: %d/%d, want 30/60", list.gotLimit, list.gotOffset)
	}
}

// Снятие отметки должно УДАЛЯТЬ прогресс, а не обнулять позицию: иначе
// серия навсегда осядет в «продолжить смотреть» с нулевым прогрессом.
func TestSetWatched_UnmarkRemovesProgress(t *testing.T) {
	svc, _ := newSvc()
	ctx := context.Background()

	if err := svc.SetWatched(ctx, user(1), 100, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, user(1), 1, 100, auth.DiagnosticGrant{}); err != nil {
		t.Fatalf("после отметки прогресс должен быть: %v", err)
	}

	if err := svc.SetWatched(ctx, user(1), 100, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, user(1), 1, 100, auth.DiagnosticGrant{}); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("после снятия записи быть не должно, получено: %v", err)
	}
}

// Отметить чужой прогресс нельзя — как и записать его.
func TestSetWatched_ForbiddenForOtherUser(t *testing.T) {
	svc, _ := newSvc()
	err := svc.SetWatched(context.Background(),
		auth.Actor{UserID: 1, Role: account.RoleUser, Space: account.SpaceAdmin}, 100, true)
	if !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("админская сессия не должна писать прогресс: %v", err)
	}
}
