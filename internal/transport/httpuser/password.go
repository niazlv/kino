package httpuser

import (
	"net/http"
	"time"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

type changePasswordReq struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// handleChangePassword меняет пароль самому себе.
//
// Смена гасит ВСЕ сессии пользователя — в том числе текущую, иначе действие
// не защищало бы от угнанной. Поэтому здесь же выдаётся новая сессия: иначе
// человек, сменив пароль, выкидывал бы сам себя и гадал, что сломалось.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var in changePasswordReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	a := actorFrom(r.Context())
	if err := s.auth.ChangePassword(r.Context(), a, in.OldPassword, in.NewPassword); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}

	u, err := s.auth.Me(r.Context(), a)
	if err != nil {
		// Пароль уже сменён; сессий нет. Честнее попросить войти заново,
		// чем делать вид, что ничего не произошло.
		s.clearCookie(w)
		httpx.JSON(w, http.StatusOK, map[string]any{"reloginRequired": true})
		return
	}
	sess, token, err := s.auth.Login(r.Context(), auth.Credentials{
		Login: u.Login, Password: in.NewPassword,
	}, account.SpaceUser)
	if err != nil {
		s.clearCookie(w)
		httpx.JSON(w, http.StatusOK, map[string]any{"reloginRequired": true})
		return
	}
	s.setCookie(w, token, int(time.Until(sess.ExpiresAt).Seconds()))
	httpx.JSON(w, http.StatusOK, map[string]any{"expiresAt": sess.ExpiresAt})
}
