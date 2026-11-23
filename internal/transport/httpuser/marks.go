package httpuser

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

type marksDTO struct {
	Following bool `json:"following"`
	Watchlist bool `json:"watchlist"`
	Hidden    bool `json:"hidden"`
}

func (s *Server) handleGetMarks(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "titleID"), 10, 64)
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	m, err := s.marks.Get(r.Context(), actorFrom(r.Context()), kernel.TitleID(id))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, marksDTO{
		Following: m.Following, Watchlist: m.Watchlist, Hidden: m.Hidden,
	})
}

// handlePutMarks записывает отметки целиком.
//
// Целиком, а не по полю: интерфейс всегда знает текущее состояние, а
// частичное обновление породило бы гонки между вкладками.
func (s *Server) handlePutMarks(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "titleID"), 10, 64)
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	var in marksDTO
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	m, err := s.marks.Set(r.Context(), actorFrom(r.Context()), account.Marks{
		TitleID:   kernel.TitleID(id),
		Following: in.Following, Watchlist: in.Watchlist,
		Hidden: in.Hidden,
	})
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, marksDTO{
		Following: m.Following, Watchlist: m.Watchlist, Hidden: m.Hidden,
	})
}

// prefsDTO — выбор дорожек в плеере. null — «не выбирал»; для субтитров
// пустая строка — «выключены намеренно».
type prefsDTO struct {
	Audio    *string `json:"audio"`
	Subtitle *string `json:"subtitle"`
	// Source — выбранная площадка; пустая строка — «автоматически».
	Source *string `json:"source"`
}

// handlePutPrefs запоминает переключение дорожки. Поле, которого нет в
// теле, не меняется: плеер сообщает об одном переключении за раз.
func (s *Server) handlePutPrefs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "titleID"), 10, 64)
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	var in prefsDTO
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	p, err := s.marks.SetPrefs(r.Context(), actorFrom(r.Context()), account.TitlePrefs{
		TitleID: kernel.TitleID(id), Audio: in.Audio, Subtitle: in.Subtitle, Source: in.Source,
	})
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, prefsDTO{Audio: p.Audio, Subtitle: p.Subtitle, Source: p.Source})
}

// listPage — карточек на страницу списка.
const listPage = 60

// handleList отдаёт страницу списка по виду отметки: слежу, буду смотреть.
// Ответ — страница с total и nextOffset: список с подгрузкой,
// а не окно, обрезанное на первой странице.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	kind := account.MarkKind(chi.URLParam(r, "kind"))
	offset := 0
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 && n <= 10000 {
		offset = n
	}
	titles, total, err := s.marks.List(r.Context(), actorFrom(r.Context()), kind, listPage, offset)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	out := make([]titleDTO, 0, len(titles))
	for _, t := range titles {
		out = append(out, titleDTO{
			ID: int64(t.ID), Kind: string(t.Kind), Title: t.Title,
			Year: t.Year, HasArt: t.Poster != "",
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"items": out, "total": total,
		"nextOffset": offset + len(out), "hasMore": offset+len(out) < total,
	})
}
