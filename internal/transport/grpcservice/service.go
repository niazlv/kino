// Package grpcservice — СЛУЖЕБНОЕ пространство: вызовы между сервисами.
//
// Единственный потребитель — медиа-шлюз, и единственная операция — обновить
// протухший тикет. Контракт намеренно generic: в нём нет ни одного
// провайдерского понятия, поэтому шлюз по-прежнему ничего о площадках
// не знает (ADR-0002).
//
// Транспорт HTTP, а не gRPC, как предполагал ADR: одна операция с одним
// параметром не окупает генерацию кода и вторую схему. Важное свойство —
// отсутствие провайдерских типов в контракте — от транспорта не зависит.
package grpcservice

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/app/playback"
	"github.com/niazleushkin/kino/internal/app/progress"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dplayback "github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// TicketSource — чтение тикета. Пользователь и серия берутся из тикета,
// а не из тела запроса: шлюзу доверяют как службе, но лишний раз не дают
// утверждать, чей это просмотр.
type TicketSource interface {
	Get(ctx context.Context, id kernel.TicketID) (dplayback.Ticket, error)
}

type Server struct {
	playback *playback.Service
	progress *progress.Service
	tickets  TicketSource
	token    string
	log      *slog.Logger
}

func New(p *playback.Service, prog *progress.Service, tickets TicketSource,
	token string, log *slog.Logger) *Server {
	return &Server{playback: p, progress: prog, tickets: tickets, token: token, log: log}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.Recover(s.log), httpx.Log(s.log))
	r.Group(func(r chi.Router) {
		r.Use(s.requireService)
		r.Post("/internal/tickets/{ticketID}/refresh", s.handleRefresh)
		r.Post("/internal/tickets/{ticketID}/progress", s.handleProgress)
	})
	return r
}

// requireService — служебные креды. Отдельное пространство: этот токен
// не даёт ни пользовательского API, ни админки.
func (s *Server) requireService(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" || r.Header.Get("X-Service-Token") != s.token {
			httpx.Fail(w, r, s.log, kernel.ErrUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleRefresh перевыпускает тикет ПОД ТЕМ ЖЕ ID.
//
// Тот же id принципиально: шлюз держит его в работающем сеансе, и смена
// заставила бы клиента переоткрывать поток посреди фильма.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	id := kernel.TicketID(chi.URLParam(r, "ticketID"))
	if id == "" {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	t, err := s.playback.RefreshTicket(r.Context(), id)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	s.log.Info("тикет обновлён", "ticket", string(t.ID), "expires", t.ExpiresAt)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"ticketId":  string(t.ID),
		"expiresAt": t.ExpiresAt,
	})
}

// handleProgress — позиция, восстановленная шлюзом из фактов отдачи сегментов.
//
// Внешний плеер (playExternal) назад ничего не присылает: сегменты через
// шлюз — единственный след его просмотра. Кто доскачал до сегмента, до того
// и досмотрел; точность до сегмента, зато для любого плеера. Пользователь
// и серия читаются из тикета: шлюзу доверяют, но не его арифметике прав.
func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	id := kernel.TicketID(chi.URLParam(r, "ticketID"))
	if id == "" {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	var in struct {
		PositionMS int64 `json:"positionMs"`
		DurationMS int64 `json:"durationMs"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if in.PositionMS < 0 {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	t, err := s.tickets.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	// Буфер браузера может быть на минуты впереди фактического просмотра.
	// Старые тикеты тоже не включают эту оценку без явного external.
	if !t.TrackProgressFromSegments {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if err := s.progress.ReportFromGateway(r.Context(),
		t.UserID, t.EpisodeID, in.PositionMS, in.DurationMS); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
