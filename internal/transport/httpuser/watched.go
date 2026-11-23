package httpuser

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

type watchedReq struct {
	Watched bool `json:"watched"`
}

// handleSetWatched — ручная отметка серии.
//
// Автоматика знает только «докуда доиграл плеер». Человек может посмотреть
// серию не здесь, бросить её как неинтересную или наоборот вернуть
// в непросмотренные — это решение, а не следствие позиции.
func (s *Server) handleSetWatched(w http.ResponseWriter, r *http.Request) {
	ep, err := episodeID(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	var in watchedReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if err := s.progress.SetWatched(r.Context(), actorFrom(r.Context()), ep, in.Watched); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"watched": in.Watched})
}

// handleSetSeasonWatched — отметка сезона целиком.
//
// Одной операцией, а не циклом на клиенте: у сезона бывает под сотню серий,
// и сотня запросов ради одного нажатия применится наполовину при обрыве.
func (s *Server) handleSetSeasonWatched(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.ParseInt(chi.URLParam(r, "titleID"), 10, 64)
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	season, err := strconv.Atoi(chi.URLParam(r, "season"))
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	var in watchedReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	n, err := s.progress.SetSeasonWatched(r.Context(), actorFrom(r.Context()),
		kernel.TitleID(tid), season, in.Watched)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"watched": in.Watched, "episodes": n})
}
