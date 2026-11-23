package grpcservice

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/app/progress"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

type progressMemory struct {
	progress.Repo
	value  playback.Progress
	writes int
}

func (m *progressMemory) Get(context.Context, kernel.UserID, kernel.EpisodeID) (playback.Progress, error) {
	return m.value, nil
}
func (m *progressMemory) Upsert(_ context.Context, p playback.Progress) error {
	m.value = p
	m.writes++
	return nil
}

type progressClock struct{}

func (progressClock) Now() time.Time { return time.Now() }

type progressTicket struct{ playback.Ticket }

func (t progressTicket) Get(context.Context, kernel.TicketID) (playback.Ticket, error) {
	return t.Ticket, nil
}

func TestBufferedSegmentsCannotOverwriteBrowserPosition(t *testing.T) {
	for _, external := range []bool{false, true} {
		repo := &progressMemory{value: playback.Progress{
			UserID: 1, EpisodeID: 2, PositionMS: 120_000, DurationMS: 1_800_000,
			DeviceID: "browser-session", ClientTS: time.Now(), UpdatedAt: time.Now(),
		}}
		ticket := progressTicket{playback.Ticket{ID: "ticket", UserID: 1, EpisodeID: 2,
			TrackProgressFromSegments: external}}
		srv := New(nil, progress.NewService(repo, nil, nil, progressClock{}), ticket, "service", slog.Default())
		req := httptest.NewRequest(http.MethodPost, "/internal/tickets/ticket/progress",
			strings.NewReader(`{"positionMs":240000,"durationMs":1800000}`))
		req.Header.Set("X-Service-Token", "service")
		res := httptest.NewRecorder()
		srv.Routes().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		if external {
			if repo.writes != 1 || repo.value.PositionMS != 240_000 {
				t.Fatalf("external estimate not saved: %+v", repo)
			}
		} else if repo.writes != 0 || repo.value.PositionMS != 120_000 {
			t.Fatalf("browser position overwritten by prefetch: %+v", repo)
		}
	}
}
