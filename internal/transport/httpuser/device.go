package httpuser

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// Вход устройства: утилита получает код, человек подтверждает его на сайте,
// утилита получает сессию с refresh-токеном. Контракт — api/openapi/device-auth.md.

type deviceBeginReq struct {
	Device string `json:"device"`
}

type deviceBeginResp struct {
	DeviceCode      string    `json:"deviceCode"`
	UserCode        string    `json:"userCode"`
	VerificationURL string    `json:"verificationUrl"`
	ExpiresAt       time.Time `json:"expiresAt"`
	Interval        int       `json:"interval"`
}

type deviceTokenReq struct {
	DeviceCode string `json:"deviceCode"`
	// Cookie — сессию положить и в cookie, как при обычном входе: так входит
	// витрина на телевизоре, которой токены в теле ответа ни к чему.
	Cookie bool `json:"cookie"`
}

type refreshReq struct {
	RefreshToken string `json:"refreshToken"`
}

type deviceTokensResp struct {
	Token            string    `json:"token"`
	RefreshToken     string    `json:"refreshToken"`
	ExpiresAt        time.Time `json:"expiresAt"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
}

type deviceInfoResp struct {
	Device      string    `json:"device"`
	RequestedAt time.Time `json:"requestedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// handleDeviceBegin — устройство просит код. Без сессии: это и есть вход.
func (s *Server) handleDeviceBegin(w http.ResponseWriter, r *http.Request) {
	var in deviceBeginReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	start, err := s.auth.BeginDeviceAuth(r.Context(), in.Device)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, deviceBeginResp{
		DeviceCode: start.DeviceCode,
		UserCode:   start.UserCode,
		// Ссылка ведёт на страницу подтверждения с уже подставленным кодом:
		// её сканируют с экрана, руками код вводят только без камеры.
		VerificationURL: s.publicBase(r) + "/#/link/" + start.UserCode,
		ExpiresAt:       start.ExpiresAt,
		Interval:        int(start.Interval.Seconds()),
	})
}

// handleDevicePoll — устройство спрашивает, подтвердили ли его. Ответы
// «ещё нет» и «спрашивай реже» — не ошибки платформы, а состояние запроса,
// поэтому у них свои коды в теле, как в device authorization grant.
func (s *Server) handleDevicePoll(w http.ResponseWriter, r *http.Request) {
	var in deviceTokenReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	sess, tokens, err := s.auth.PollDeviceAuth(r.Context(), in.DeviceCode)
	if err != nil {
		if code := deviceErrorCode(err); code != "" {
			httpx.JSON(w, http.StatusBadRequest, httpx.ErrorBody{Error: code})
			return
		}
		httpx.Fail(w, r, s.log, err)
		return
	}
	if in.Cookie {
		s.setCookie(w, tokens.Token, int(time.Until(sess.ExpiresAt).Seconds()))
	}
	httpx.JSON(w, http.StatusOK, deviceTokensResp{
		Token: tokens.Token, RefreshToken: tokens.RefreshToken,
		ExpiresAt: sess.ExpiresAt, RefreshExpiresAt: tokens.RefreshExpiresAt,
	})
}

// deviceErrorCode переводит исход опроса в код из device authorization grant.
// Пусто — обычная ошибка, её отдаёт httpx.Fail.
func deviceErrorCode(err error) string {
	switch {
	case errors.Is(err, auth.ErrDeviceAuthPending):
		return "authorization_pending"
	case errors.Is(err, auth.ErrDeviceSlowDown):
		return "slow_down"
	case errors.Is(err, auth.ErrDeviceAuthDenied):
		return "access_denied"
	case errors.Is(err, kernel.ErrExpired):
		return "expired_token"
	case errors.Is(err, kernel.ErrNotFound):
		return "invalid_grant"
	}
	return ""
}

// handleRefresh — продление сессии устройства по refresh-токену. Без
// сессии: сама сессия к этому моменту могла истечь.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var in refreshReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	sess, tokens, err := s.auth.RefreshSession(r.Context(), in.RefreshToken)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, deviceTokensResp{
		Token: tokens.Token, RefreshToken: tokens.RefreshToken,
		ExpiresAt: sess.ExpiresAt, RefreshExpiresAt: tokens.RefreshExpiresAt,
	})
}

// handleDeviceInfo — что показать человеку перед решением.
func (s *Server) handleDeviceInfo(w http.ResponseWriter, r *http.Request) {
	d, err := s.auth.DeviceAuthByUserCode(r.Context(), actorFrom(r.Context()), chi.URLParam(r, "userCode"))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, deviceInfoResp{Device: d.Device, RequestedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt})
}

func (s *Server) handleDeviceApprove(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.ApproveDeviceAuth(r.Context(), actorFrom(r.Context()), chi.URLParam(r, "userCode")); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeviceDeny(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.DenyDeviceAuth(r.Context(), actorFrom(r.Context()), chi.URLParam(r, "userCode")); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
