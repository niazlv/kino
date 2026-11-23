package httpuser

import (
	"net/http"

	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// История поиска. Все три операции возвращают свежий список: витрине он
// нужен в любом случае, и лишний поход за ним незачем.

func (s *Server) handleSearchHistory(w http.ResponseWriter, r *http.Request) {
	items, err := s.searchHist.List(r.Context(), actorFrom(r.Context()))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleSearchHistoryAdd(w http.ResponseWriter, r *http.Request) {
	var in struct{ Query string `json:"query"` }
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	items, err := s.searchHist.Record(r.Context(), actorFrom(r.Context()), in.Query)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleSearchHistoryDelete удаляет один запрос (?q=) или всю историю
// (без параметра). Один путь на оба действия: запрос в пути пришлось бы
// экранировать дважды — слеши и юникод в нём обычное дело.
func (s *Server) handleSearchHistoryDelete(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var (
		items []string
		err   error
	)
	if q == "" {
		items, err = s.searchHist.Clear(r.Context(), actorFrom(r.Context()))
	} else {
		items, err = s.searchHist.Delete(r.Context(), actorFrom(r.Context()), q)
	}
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}
