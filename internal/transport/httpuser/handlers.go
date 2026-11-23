package httpuser

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

type registerReq struct {
	Invite      string `json:"invite"`
	Login       string `json:"login"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var in registerReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	u, err := s.auth.Register(r.Context(), in.Invite, in.Login, in.Password, in.DisplayName)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, userDTO{ID: int64(u.ID), Login: u.Login, DisplayName: u.DisplayName})
}

type loginReq struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	Device   string `json:"device"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in loginReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	sess, token, err := s.auth.Login(r.Context(), auth.Credentials{
		Login: in.Login, Password: in.Password, Device: in.Device,
	}, account.SpaceUser)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	s.setCookie(w, token, int(time.Until(sess.ExpiresAt).Seconds()))
	httpx.JSON(w, http.StatusOK, map[string]any{"expiresAt": sess.ExpiresAt})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		if err := s.auth.Logout(r.Context(), c.Value); err != nil {
			httpx.Fail(w, r, s.log, err)
			return
		}
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

type userDTO struct {
	ID          int64  `json:"id"`
	Login       string `json:"login"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role,omitempty"`
	// CreatedAt — «в kino с …» в профиле. Указатель: регистрация отвечает
	// без него, и нулевая дата не должна притворяться настоящей.
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	// Local — аккаунт локального движка: без пароля, выхода и входа по коду.
	// В ответе только у него (omitempty): боевой API не меняется.
	Local bool `json:"local,omitempty"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a := actorFrom(r.Context())
	u, err := s.auth.Me(r.Context(), a)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, userDTO{
		ID: int64(u.ID), Login: u.Login, DisplayName: u.DisplayName, Role: string(u.Role),
		CreatedAt: &u.CreatedAt, Local: s.local,
	})
}

// continueDTO — форма ответа принадлежит транспорту, а не use case'у.
// Вешать json-теги на тип из app значило бы протащить в него знание
// о представлении и связать имена полей API с внутренними именами.
type continueDTO struct {
	EpisodeID  int64  `json:"episodeId"`
	TitleID    int64  `json:"titleId"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Year       int    `json:"year"`
	HasArt     bool   `json:"hasArt"`
	SeasonNo   int    `json:"seasonNo"`
	EpisodeNo  int    `json:"episodeNo"`
	PositionMS int64  `json:"positionMs"`
	DurationMS int64  `json:"durationMs"`
}

// continuePage — карточек на страницу «продолжить смотреть».
const continuePage = 30

func (s *Server) handleContinue(w http.ResponseWriter, r *http.Request) {
	offset := 0
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 && n <= 100000 {
		offset = n
	}
	items, total, err := s.progress.Continue(r.Context(), actorFrom(r.Context()), continuePage, offset)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	out := make([]continueDTO, 0, len(items))
	for _, it := range items {
		out = append(out, continueDTO{
			EpisodeID: int64(it.EpisodeID), TitleID: int64(it.TitleID), Title: it.Title,
			Kind: it.Kind, Year: it.Year, HasArt: it.HasArt,
			SeasonNo: it.SeasonNo, EpisodeNo: it.EpisodeNo,
			PositionMS: it.PositionMS, DurationMS: it.DurationMS,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"items": out, "total": total,
		"nextOffset": offset + len(out), "hasMore": offset+len(out) < total,
	})
}

type progressReq struct {
	PositionMS int64     `json:"positionMs"`
	DurationMS int64     `json:"durationMs"`
	ClientTS   time.Time `json:"clientTs"`
	DeviceID   string    `json:"deviceId"`
}

func (s *Server) handlePutProgress(w http.ResponseWriter, r *http.Request) {
	ep, err := episodeID(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	var in progressReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if in.ClientTS.IsZero() {
		in.ClientTS = time.Now()
	}
	if in.PositionMS < 0 || in.DurationMS < 0 {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	a := actorFrom(r.Context())
	if in.DeviceID == "" {
		in.DeviceID = string(a.SessionID)
	}
	// UserID берётся ИЗ СЕССИИ, не из тела запроса: иначе клиент пишет
	// в чужой прогресс, и вся затея теряет смысл.
	out, err := s.progress.Report(r.Context(), a, playback.Progress{
		UserID: a.UserID, EpisodeID: ep,
		PositionMS: in.PositionMS, DurationMS: in.DurationMS,
		ClientTS: in.ClientTS, DeviceID: in.DeviceID,
	})
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"positionMs": out.PositionMS, "status": out.Status,
	})
}

func (s *Server) handleGetProgress(w http.ResponseWriter, r *http.Request) {
	ep, err := episodeID(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	a := actorFrom(r.Context())
	p, err := s.progress.Get(r.Context(), a, a.UserID, ep, auth.DiagnosticGrant{})
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"positionMs": p.PositionMS, "durationMs": p.DurationMS, "status": p.Status,
	})
}

func episodeID(r *http.Request) (kernel.EpisodeID, error) {
	v, err := strconv.ParseInt(chi.URLParam(r, "episodeID"), 10, 64)
	if err != nil {
		return 0, kernel.ErrInvalidInput
	}
	return kernel.EpisodeID(v), nil
}
