package httpuser

import (
	"context"
	"net/http"
	"strconv"

	"github.com/niazleushkin/kino/internal/adapter/postgres"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// Browser — чтение разделов каталога.
type Browser interface {
	Browse(ctx context.Context, q postgres.BrowseQuery) (postgres.BrowsePage, error)
	Genres(ctx context.Context) ([]string, error)
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))

	// Вид берётся из белого списка, а не подставляется как есть: параметр
	// уходит в запрос, и принимать оттуда произвольное значение незачем.
	kind := q.Get("kind")
	if kind != "movie" && kind != "series" {
		kind = ""
	}
	res, err := s.browser.Browse(r.Context(), postgres.BrowseQuery{
		Kind: kind, Genre: q.Get("genre"), Sort: q.Get("sort"), Page: page,
	})
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	out := make([]titleDTO, 0, len(res.Items))
	for _, t := range res.Items {
		out = append(out, titleDTO{
			ID: int64(t.ID), Kind: string(t.Kind), Title: t.Title,
			Year: t.Year, HasArt: t.Poster != "",
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out, "total": res.Total})
}

func (s *Server) handleGenres(w http.ResponseWriter, r *http.Request) {
	g, err := s.browser.Genres(r.Context())
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": g})
}
