// Package httpuser — ПОЛЬЗОВАТЕЛЬСКОЕ пространство API.
//
// Здесь только АУТЕНТИФИКАЦИЯ (достать сессию, собрать Actor) и маршрутный
// отсев. Решения «может ли этот actor трогать эту строку» принимает
// auth.Policy в слое app — см. docs/boundaries.md.
package httpuser

import (
	"context"
	"net/http"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

const SessionCookie = "kino_session"

type ctxKey int

const actorKey ctxKey = iota

func actorFrom(ctx context.Context) auth.Actor {
	a, _ := ctx.Value(actorKey).(auth.Actor)
	return a
}

// requireUser пускает только сессии пространства user. Админская сессия
// сюда не пройдёт: пространства изолированы на уровне домена.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil {
			httpx.Fail(w, r, s.log, kernel.ErrUnauthorized)
			return
		}
		actor, err := s.auth.Authenticate(r.Context(), c.Value, account.SpaceUser)
		if err != nil {
			s.clearCookie(w)
			httpx.Fail(w, r, s.log, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey, actor)))
	})
}

func (s *Server) setCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:  SessionCookie,
		Value: token,
		Path:  "/",
		// HttpOnly: токен недоступен из JS -> XSS не крадёт сессию.
		HttpOnly: true,
		// Lax, а не Strict: переход по ссылке на плеер должен оставлять
		// пользователя залогиненным.
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secureCookies,
		MaxAge:   maxAge,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter) { s.setCookie(w, "", -1) }
