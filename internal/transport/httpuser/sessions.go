package httpuser

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// «Где вы вошли» в профиле витрины: свои сессии, выход на одном устройстве
// и на всех остальных. Решения о доступе — в auth.Service (MySessions и
// соседи), здесь только форма ответа.

type sessionDTO struct {
	ID       string    `json:"id"`
	Device   string    `json:"device"`
	IssuedAt time.Time `json:"issuedAt"`
	// Until — когда вход кончится сам, если устройством не пользоваться.
	// У вошедшего по коду это срок продления, а не сессии: он продлевается.
	Until time.Time `json:"until"`
	// Renewable — устройство, вошедшее по коду: телевизор, приложение.
	Renewable bool `json:"renewable,omitempty"`
	// Current — это та самая сессия, из которой спрашивают.
	Current bool `json:"current,omitempty"`
}

func (s *Server) handleMySessions(w http.ResponseWriter, r *http.Request) {
	a := actorFrom(r.Context())
	list, err := s.auth.MySessions(r.Context(), a)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	items := make([]sessionDTO, 0, len(list))
	for _, sess := range list {
		until := sess.ExpiresAt
		if sess.RefreshExpiresAt != nil {
			until = *sess.RefreshExpiresAt
		}
		items = append(items, sessionDTO{
			ID: string(sess.ID), Device: sess.DeviceKind, IssuedAt: sess.IssuedAt, Until: until,
			Renewable: sess.Renewable(), Current: sess.ID == a.SessionID,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleSignOutSession — выйти на одном устройстве. Своя текущая тоже
// годится: тогда это обычный выход, и cookie гасится здесь же.
func (s *Server) handleSignOutSession(w http.ResponseWriter, r *http.Request) {
	a := actorFrom(r.Context())
	id := kernel.SessionID(chi.URLParam(r, "sessionID"))
	if err := s.auth.SignOutSession(r.Context(), a, id); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if id == a.SessionID {
		s.clearCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSignOutOthers — выйти везде, кроме этого устройства.
func (s *Server) handleSignOutOthers(w http.ResponseWriter, r *http.Request) {
	n, err := s.auth.SignOutOthers(r.Context(), actorFrom(r.Context()))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]int{"signedOut": n})
}
