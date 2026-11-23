package progress

import (
	"context"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

// fakeFollower запоминает, по какому поводу ставили «Слежу».
type fakeFollower struct {
	episodes []kernel.EpisodeID
	titles   []kernel.TitleID
}

func (f *fakeFollower) FollowByEpisode(_ context.Context, _ kernel.UserID, e kernel.EpisodeID) error {
	f.episodes = append(f.episodes, e)
	return nil
}

func (f *fakeFollower) FollowByTitle(_ context.Context, _ kernel.UserID, t kernel.TitleID) error {
	f.titles = append(f.titles, t)
	return nil
}

// seasonRepo отдаёт число отмеченных серий: у общего фейка оно всегда ноль.
type seasonRepo struct {
	fakeRepo
	n int64
}

func (s seasonRepo) SetSeasonWatched(context.Context, kernel.UserID, kernel.TitleID, int, bool) (int64, error) {
	return s.n, nil
}

func followSvc(n int64) (*Service, *fakeFollower) {
	f := &fakeFollower{}
	repo := seasonRepo{fakeRepo{store: map[kernel.EpisodeID]playback.Progress{}}, n}
	return NewService(repo, &fakeList{}, nil,
		fixedClock{time.Date(2026, 11, 15, 16, 30, 0, 0, time.UTC)}).WithFollower(f), f
}

func report(t *testing.T, svc *Service, e kernel.EpisodeID, pos int64) {
	t.Helper()
	if _, err := svc.Report(context.Background(), user(2), playback.Progress{
		UserID: 2, EpisodeID: e, PositionMS: pos, DurationMS: 1_440_000, ClientTS: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// Досмотренная серия ставит «Слежу» один раз: отчёты титров и пересмотр
// той же серии — не новый повод.
func TestFollow_CompletedEpisodeFollowsOnce(t *testing.T) {
	svc, f := followSvc(0)
	report(t, svc, 9, 600_000)
	if len(f.episodes) != 0 {
		t.Fatalf("начатая серия не повод для слежения: %v", f.episodes)
	}
	report(t, svc, 9, 1_430_000)
	report(t, svc, 9, 1_440_000)
	if len(f.episodes) != 1 || f.episodes[0] != 9 {
		t.Fatalf("слежение по досмотру: %v, want [9]", f.episodes)
	}
}

// Внешний плеер досматривает через шлюз — тот же повод.
func TestFollow_GatewayCompletionFollows(t *testing.T) {
	svc, f := followSvc(0)
	if err := svc.ReportFromGateway(context.Background(), 2, 7, 1_430_000, 1_440_000); err != nil {
		t.Fatal(err)
	}
	if len(f.episodes) != 1 || f.episodes[0] != 7 {
		t.Fatalf("слежение по досмотру через шлюз: %v, want [7]", f.episodes)
	}
}

// Ручная отметка — смотрел, пусть и не здесь; снятие отметки — не повод.
func TestFollow_ManualMarks(t *testing.T) {
	svc, f := followSvc(8)
	ctx := context.Background()
	if err := svc.SetWatched(ctx, user(2), 5, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWatched(ctx, user(2), 5, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSeasonWatched(ctx, user(2), 10106, 3, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSeasonWatched(ctx, user(2), 10106, 3, true); err != nil {
		t.Fatal(err)
	}
	if len(f.episodes) != 1 || f.episodes[0] != 5 {
		t.Fatalf("слежение по отметке серии: %v, want [5]", f.episodes)
	}
	if len(f.titles) != 1 || f.titles[0] != 10106 {
		t.Fatalf("слежение по отметке сезона: %v, want [10106]", f.titles)
	}
}

// Пустой сезон отмечать нечем — и слежение по нему не ставится.
func TestFollow_EmptySeasonDoesNotFollow(t *testing.T) {
	svc, f := followSvc(0)
	if _, err := svc.SetSeasonWatched(context.Background(), user(2), 10106, 4, true); err != nil {
		t.Fatal(err)
	}
	if len(f.titles) != 0 {
		t.Fatalf("пустой сезон поставил слежение: %v", f.titles)
	}
}
