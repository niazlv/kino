package httpuser

import (
	"context"
	"net/http"

	"github.com/niazleushkin/kino/internal/app/skips"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// Skips — разметка пропуска серии (см. app/skips).
type Skips interface {
	Episode(ctx context.Context, req skips.Request) ([]skips.Mark, error)
}

// WithSkips включает разметку пропуска.
func (s *Server) WithSkips(sk Skips) *Server {
	s.skips = sk
	return s
}

// segmentDTO — отрезок, который можно пропустить. Время — секундами, как
// у видео в браузере.
type segmentDTO struct {
	// Kind: intro, outro, recap, preview или skip — «пропустить, а что
	// это — реши по месту в серии».
	Kind  string  `json:"kind"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	// ForDuration — длина файла, по которому размечено, если разметка
	// чужая. Плеер берёт её, только когда длина его потока совпала.
	ForDuration float64 `json:"forDuration,omitempty"`
	// Origin — откуда разметка: площадка или aniskip. Для отладки.
	Origin string `json:"origin,omitempty"`
}

func toSegment(sg dp.Segment, origin string) segmentDTO {
	return segmentDTO{
		Kind: string(sg.Kind), Start: float64(sg.StartMS) / 1000, End: float64(sg.EndMS) / 1000,
		ForDuration: float64(sg.ForDurationMS) / 1000, Origin: origin,
	}
}

func toSegments(in []dp.Segment, origin string) []segmentDTO {
	out := make([]segmentDTO, 0, len(in))
	for _, sg := range in {
		if sg.Valid() {
			out = append(out, toSegment(sg, origin))
		}
	}
	return out
}

// handleSkips — разметка, которую не принёс выпуск тикета: площадки,
// отдающие её отдельным запросом, и AniSkip. Плеер спрашивает после
// старта: первый кадр её не ждёт.
//
// source и variant — площадка и вариант, которые играют: разметка
// привязана к файлу, а не к серии.
func (s *Server) handleSkips(w http.ResponseWriter, r *http.Request) {
	ep, err := episodeID(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	out := []segmentDTO{}
	if s.skips != nil {
		marks, err := s.skips.Episode(r.Context(), skips.Request{
			Episode: ep,
			Source:  r.URL.Query().Get("source"),
			Variant: r.URL.Query().Get("variant"),
		})
		if err != nil {
			httpx.Fail(w, r, s.log, err)
			return
		}
		for _, m := range marks {
			out = append(out, toSegment(m.Segment, m.Origin))
		}
	}
	// Разметка меняется редко: повторное открытие серии её не ждёт.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	httpx.JSON(w, http.StatusOK, map[string]any{"segments": out})
}
