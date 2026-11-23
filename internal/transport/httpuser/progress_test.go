package httpuser

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/app/progress"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

type progressMemory struct {
	progress.Repo
	value playback.Progress
}

func (m *progressMemory) Get(context.Context, kernel.UserID, kernel.EpisodeID) (playback.Progress, error) {
	return m.value, nil
}
func (m *progressMemory) Upsert(_ context.Context, p playback.Progress) error {
	m.value = p
	return nil
}

type progressClock struct{}

func (progressClock) Now() time.Time { return time.Now() }

func TestProgressBeaconRouteRequiresAuthentication(t *testing.T) {
	s := &Server{log: slog.Default()}
	for _, method := range []string{http.MethodPut, http.MethodPost} {
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, httptest.NewRequest(method, "/api/v1/progress/2", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: got %d, want 401 (route exists and is protected)", method, w.Code)
		}
	}
}

func TestProgressBeaconUsesSessionAndPreservesRewind(t *testing.T) {
	repo := &progressMemory{value: playback.Progress{UserID: 1, EpisodeID: 2,
		PositionMS: 900_000, DurationMS: 1_800_000, DeviceID: "session", ClientTS: time.Now().Add(-time.Second)}}
	s := &Server{progress: progress.NewService(repo, nil, nil, progressClock{}), log: slog.Default()}
	r := chi.NewRouter()
	r.Post("/progress/{episodeID}", s.handlePutProgress)
	req := httptest.NewRequest(http.MethodPost, "/progress/2", strings.NewReader(`{"positionMs":120500,"durationMs":1800000}`))
	actor := auth.Actor{UserID: 1, SessionID: "session", Space: account.SpaceUser, Role: account.RoleUser}
	req = req.WithContext(context.WithValue(req.Context(), actorKey, actor))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || repo.value.PositionMS != 120_500 || repo.value.DeviceID != "session" {
		t.Fatalf("beacon was not saved: status=%d progress=%+v", w.Code, repo.value)
	}
}
