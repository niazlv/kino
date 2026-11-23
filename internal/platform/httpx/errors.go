// Package httpx — общие примитивы HTTP-транспорта: маппинг ошибок, JSON,
// middleware. Знание про статус-коды живёт ТОЛЬКО здесь: ни домен, ни use
// cases про HTTP не знают и знать не должны.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type ErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// Status переводит доменную таксономию в коды HTTP.
func Status(err error) (int, string) {
	switch {
	case errors.Is(err, kernel.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, kernel.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, kernel.ErrInvalidInput):
		return http.StatusBadRequest, "invalid_input"
	case errors.Is(err, kernel.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, kernel.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, kernel.ErrExpired):
		return http.StatusGone, "expired"
	case errors.Is(err, kernel.ErrUnavailable):
		return http.StatusBadGateway, "upstream_unavailable"
	case errors.Is(err, kernel.ErrBusy):
		return http.StatusServiceUnavailable, "upstream_busy"
	default:
		return http.StatusInternalServerError, "internal"
	}
}

// Fail отвечает клиенту кодом и стабильным машинным кодом ошибки.
// Внутренний текст наружу НЕ уходит: он может содержать имена констрейнтов,
// хосты CDN и прочую внутреннюю кухню. Полная ошибка — только в лог.
func Fail(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	code, machine := Status(err)
	if code >= 500 {
		log.ErrorContext(r.Context(), "request failed",
			"err", err, "path", r.URL.Path, "method", r.Method)
	} else {
		log.DebugContext(r.Context(), "request rejected",
			"err", err, "code", machine, "path", r.URL.Path)
	}
	body := ErrorBody{Error: machine}
	if code < 500 {
		body.Message = safeMessage(err)
	}
	JSON(w, code, body)
}

// safeMessage отдаёт только доменную часть, без обёрток с внутренними деталями.
func safeMessage(err error) string {
	for _, e := range []error{
		kernel.ErrNotFound, kernel.ErrConflict, kernel.ErrInvalidInput,
		kernel.ErrUnauthorized, kernel.ErrForbidden, kernel.ErrExpired,
	} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return ""
}

func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

const maxBody = 1 << 20 // 1 МиБ: тело запроса к API не бывает больше

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return kernel.ErrInvalidInput
	}
	return nil
}
