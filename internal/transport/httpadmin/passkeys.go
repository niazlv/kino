package httpadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/adapter/webauthn"
	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Passkey: JSON-эндпоинты трёх церемоний — вход без пароля, подтверждение
// (второй шаг или sudo) и регистрация ключа. Состояние церемонии между
// begin и finish живёт в памяти под ключом из cookie: ни в адрес, ни в
// тело оно не попадает, а пять минут — с запасом на любой authenticator.

const (
	ceremonyCookie = "kino_wa"
	ceremonyTTL    = 5 * time.Minute
)

type ceremony struct {
	kind  string // login | factor | register
	state []byte
	user  kernel.UserID
	at    time.Time
}

func (s *Server) startCeremony(w http.ResponseWriter, kind string, state []byte, user kernel.UserID) {
	key := randomKey()
	s.mu.Lock()
	now := time.Now()
	for k, c := range s.ceremonies {
		if now.Sub(c.at) > ceremonyTTL {
			delete(s.ceremonies, k)
		}
	}
	s.ceremonies[key] = ceremony{kind: kind, state: state, user: user, at: now}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: ceremonyCookie, Value: key, Path: Prefix, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: s.https, MaxAge: int(ceremonyTTL.Seconds()),
	})
}

// takeCeremony забирает состояние один раз: повторный finish с тем же
// ответом authenticator'а не пройдёт.
func (s *Server) takeCeremony(w http.ResponseWriter, r *http.Request, kind string) (ceremony, bool) {
	http.SetCookie(w, &http.Cookie{Name: ceremonyCookie, Path: Prefix, MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.https})
	c, err := r.Cookie(ceremonyCookie)
	if err != nil {
		return ceremony{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cer, ok := s.ceremonies[c.Value]
	if !ok {
		return ceremony{}, false
	}
	delete(s.ceremonies, c.Value)
	if cer.kind != kind || time.Since(cer.at) > ceremonyTTL {
		return ceremony{}, false
	}
	return cer, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, code int, text string) {
	writeJSON(w, code, map[string]string{"error": text})
}

func nextOr(r *http.Request, def string) string {
	if n := safeNext(r.URL.Query().Get("next")); n != "" {
		return n
	}
	return def
}

func (s *Server) owner(r *http.Request) webauthn.Owner {
	return func(id kernel.UserID) (account.User, []account.Passkey, error) {
		return s.auth.PasskeyOwner(r.Context(), id)
	}
}

// ── вход без пароля ───────────────────────────────────────────────────────

func (s *Server) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	opts, state, err := s.engine.BeginLogin()
	if err != nil {
		s.log.Error("passkey: begin login", "err", err)
		jsonError(w, http.StatusInternalServerError, "Не удалось начать вход по ключу.")
		return
	}
	s.startCeremony(w, "login", state, 0)
	writeJSON(w, http.StatusOK, opts)
}

func (s *Server) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	c, ok := s.takeCeremony(w, r, "login")
	if !ok {
		jsonError(w, http.StatusBadRequest, "Время на вход вышло, попробуйте ещё раз.")
		return
	}
	as, err := s.engine.FinishLogin(c.state, r, s.owner(r))
	if err != nil {
		s.log.Warn("passkey: вход отклонён", "err", err, "ip", clientIP(r))
		jsonError(w, http.StatusUnauthorized, "Ключ не подошёл.")
		return
	}
	sess, token, err := s.auth.LoginWithPasskey(r.Context(), as.CredentialID, as.SignCount, "admin-ui")
	if err != nil {
		s.log.Warn("passkey: вход отклонён", "err", err, "ip", clientIP(r))
		jsonError(w, http.StatusUnauthorized, "Ключ не подошёл.")
		return
	}
	s.setCookie(w, token, sess.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]string{"next": nextOr(r, Prefix+"/")})
}

// ── второй шаг и sudo ─────────────────────────────────────────────────────

func (s *Server) passkeyFactorBegin(w http.ResponseWriter, r *http.Request) {
	a := actorFrom(r.Context())
	keys, err := s.auth.ListPasskeys(r.Context(), a, a.UserID)
	if err != nil || len(keys) == 0 {
		jsonError(w, http.StatusBadRequest, "У вас нет ключей.")
		return
	}
	opts, state, err := s.engine.BeginAssertion(userFrom(r.Context()), keys)
	if err != nil {
		s.log.Error("passkey: begin assertion", "err", err)
		jsonError(w, http.StatusInternalServerError, "Не удалось начать проверку.")
		return
	}
	s.startCeremony(w, "factor", state, a.UserID)
	writeJSON(w, http.StatusOK, opts)
}

func (s *Server) passkeyFactorFinish(w http.ResponseWriter, r *http.Request) {
	a := actorFrom(r.Context())
	c, ok := s.takeCeremony(w, r, "factor")
	if !ok || c.user != a.UserID {
		jsonError(w, http.StatusBadRequest, "Время на проверку вышло, попробуйте ещё раз.")
		return
	}
	keys, err := s.auth.ListPasskeys(r.Context(), a, a.UserID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Не удалось прочитать ключи.")
		return
	}
	as, err := s.engine.FinishAssertion(userFrom(r.Context()), keys, c.state, r)
	if err != nil {
		s.log.Warn("passkey: подтверждение отклонено", "err", err, "ip", clientIP(r))
		jsonError(w, http.StatusUnauthorized, "Ключ не подошёл.")
		return
	}
	sess, token, err := s.auth.ElevateWithPasskey(r.Context(), a, as.CredentialID, as.SignCount, "admin-ui")
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "Ключ не подошёл.")
		return
	}
	s.setCookie(w, token, sess.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]string{"next": nextOr(r, Prefix+"/")})
}

// ── регистрация ключа ─────────────────────────────────────────────────────

func (s *Server) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	a := actorFrom(r.Context())
	u, keys, err := s.auth.CanRegisterPasskey(r.Context(), a)
	switch {
	case errors.Is(err, auth.ErrStepUp):
		card := Prefix + "/users/" + strconv.FormatInt(int64(a.UserID), 10)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Код вводился давно, подтвердите его заново.",
			"next":  Prefix + "/factor?m=stepup&next=" + url.QueryEscape(card),
		})
		return
	case errors.Is(err, kernel.ErrConflict):
		jsonError(w, http.StatusConflict, "Второй фактор уже настроен: добавлять ключ можно только из повышенной сессии.")
		return
	case err != nil:
		jsonError(w, http.StatusForbidden, "Нет прав.")
		return
	}
	opts, state, err := s.engine.BeginRegistration(u, keys)
	if err != nil {
		s.log.Error("passkey: begin registration", "err", err)
		jsonError(w, http.StatusInternalServerError, "Не удалось начать регистрацию.")
		return
	}
	s.startCeremony(w, "register", state, a.UserID)
	writeJSON(w, http.StatusOK, opts)
}

func (s *Server) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	a := actorFrom(r.Context())
	c, ok := s.takeCeremony(w, r, "register")
	if !ok || c.user != a.UserID {
		jsonError(w, http.StatusBadRequest, "Время на регистрацию вышло, попробуйте ещё раз.")
		return
	}
	u, keys, err := s.auth.CanRegisterPasskey(r.Context(), a)
	if err != nil {
		jsonError(w, http.StatusForbidden, "Нет прав.")
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "Passkey"
	}
	if utf8.RuneCountInString(name) > 60 {
		name = string([]rune(name)[:60])
	}
	p, err := s.engine.FinishRegistration(u, keys, c.state, r, name)
	if err != nil {
		s.log.Warn("passkey: ключ не принят", "err", err, "ip", clientIP(r))
		jsonError(w, http.StatusBadRequest, "Ключ не принят устройством или сервером.")
		return
	}
	sess, token, err := s.auth.AddPasskey(r.Context(), a, p, "admin-ui")
	if err != nil {
		s.log.Warn("passkey: не сохранён", "err", err)
		jsonError(w, http.StatusForbidden, "Не удалось сохранить ключ.")
		return
	}
	def := Prefix + "/?m=passkey_added"
	if token != "" {
		// Частичная сессия повышена: новый токен, старый уже отозван.
		s.setCookie(w, token, sess.ExpiresAt)
	} else {
		def = Prefix + "/users/" + strconv.FormatInt(int64(a.UserID), 10) + "?m=passkey_added"
	}
	writeJSON(w, http.StatusOK, map[string]string{"next": nextOr(r, def)})
}

// deletePasskey — форма из карточки пользователя, под sudo.
func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.ParseInt(chi.URLParam(r, "pid"), 10, 64)
	if err != nil {
		s.fail(w, r, kernel.ErrInvalidInput)
		return
	}
	s.userAction(w, r, func(id kernel.UserID, reason string) (string, error) {
		err := s.auth.RemovePasskey(r.Context(), actorFrom(r.Context()), id, pid, reason)
		if errors.Is(err, kernel.ErrConflict) {
			return "?m=last_factor", nil
		}
		return "?m=passkey_removed", err
	})
}
