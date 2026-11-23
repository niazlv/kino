package httpuser

import (
	"net/http"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

type sessionReq struct {
	// TicketID служит идентификатором сессии: он уже уникален и связывает
	// телеметрию с конкретным просмотром, не требуя второго счётчика.
	TicketID   string `json:"ticketId"`
	EpisodeID  int64  `json:"episodeId"`
	DeviceKind string `json:"deviceKind"`
	AppVersion string `json:"appVersion"`

	// Impl — движок плеера: hlsjs или native_hls. Ключевое поле: у плеера
	// два независимых кодовых пути, и без разбивки по ним жалоба
	// «на айфоне тормозит» неотлаживаема в принципе.
	Impl string `json:"impl"`

	TTFFMS          int64  `json:"ttffMs"`
	RebufferCount   int    `json:"rebufferCount"`
	RebufferMSTotal int64  `json:"rebufferMsTotal"`
	BitrateSwitches int    `json:"bitrateSwitches"`
	MaxHeight       int    `json:"maxHeight"`
	EndReason       string `json:"endReason"`
	ErrorCode       string `json:"errorCode"`
}

func (s *Server) handleSessionTelemetry(w http.ResponseWriter, r *http.Request) {
	var in sessionReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if in.TicketID == "" {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	err := s.telemetry.Report(r.Context(), actorFrom(r.Context()), playback.Session{
		ID:              kernel.SessionID(in.TicketID),
		EpisodeID:       kernel.EpisodeID(in.EpisodeID),
		DeviceKind:      in.DeviceKind,
		AppVersion:      in.AppVersion,
		PlayerImpl:      playback.PlayerImpl(in.Impl),
		TTFFMS:          in.TTFFMS,
		RebufferCount:   in.RebufferCount,
		RebufferMSTotal: in.RebufferMSTotal,
		BitrateSwitches: in.BitrateSwitches,
		MaxHeight:       in.MaxHeight,
		EndReason:       playback.EndReason(in.EndReason),
		ErrorCode:       in.ErrorCode,
	})
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
