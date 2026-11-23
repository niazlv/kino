package httpuser

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/niazleushkin/kino/internal/app/auth"
)

func serverInfo(t *testing.T, s *Server) serverInfoDTO {
	t.Helper()
	s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := httptest.NewRecorder()
	// Без cookie: визитку спрашивает тот, кто ещё не вошёл.
	s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/server", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body)
	}
	var out serverInfoDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestServerInfoWithoutSession(t *testing.T) {
	s := (&Server{auth: (&auth.Service{}).WithRegistration(auth.RegistrationOpen)}).WithServerName("Кино друзей")
	got := serverInfo(t, s)
	want := serverInfoDTO{Kind: ServerKind, API: 1, Name: "Кино друзей", Registration: "open"}
	if got != want {
		t.Fatalf("получили %+v, ждали %+v", got, want)
	}
}

func TestServerInfoDefaultsToInvite(t *testing.T) {
	got := serverInfo(t, &Server{auth: &auth.Service{}})
	if got.Registration != "invite" || got.Name != "" || got.Local {
		t.Fatalf("получили %+v", got)
	}
}

func TestServerInfoLocalEngineIsClosed(t *testing.T) {
	// Движок на устройстве: хозяин один, регистрироваться некому.
	s := (&Server{auth: (&auth.Service{}).WithRegistration(auth.RegistrationOpen)}).WithLocalMode()
	got := serverInfo(t, s)
	if got.Registration != "closed" || !got.Local {
		t.Fatalf("получили %+v", got)
	}
}
