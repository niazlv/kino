package httpuser

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

type titleDTO struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	// Species — вид произведения: anime, cartoon, dorama, series, movie.
	// Точнее kind: дораму жанром или видом «фильм/сериал» не показать.
	Species  string   `json:"species,omitempty"`
	Title    string   `json:"title"`
	Year     int      `json:"year"`
	IMDB     string   `json:"imdb,omitempty"`
	Plot     string   `json:"plot,omitempty"`
	Genres   []string `json:"genres,omitempty"`
	Finished bool     `json:"finished,omitempty"`
	// HasArt говорит витрине, стоит ли запрашивать постер: запрос за
	// отсутствующим постером на каждую карточку — лишний круг по сети.
	HasArt bool `json:"hasArt"`
	// Sources — площадки тайтла по приоритету; только на странице тайтла.
	Sources []sourceDTO `json:"sources,omitempty"`
}

type episodeDTO struct {
	ID          int64  `json:"id"`
	SeasonNo    int    `json:"seasonNo"`
	EpisodeNo   int    `json:"episodeNo"`
	Title       string `json:"title,omitempty"`
	DurationSec int    `json:"durationSec"`
	PositionMS  int64  `json:"positionMs,omitempty"`
	Status      string `json:"status,omitempty"`
	HasArt      bool   `json:"hasArt"`
}

// handleSearch ищет по локальному каталогу, а не в площадке.
//
// Так поиск работает и когда площадка недоступна — а она блокируется,
// и это регулярное состояние, а не авария. Ответ — страница: total говорит,
// сколько находок всего, nextOffset — откуда листать дальше, поэтому витрина
// докручивает по мере прокрутки, а не обрезает всё на первом экране.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		httpx.JSON(w, http.StatusOK, map[string]any{"items": []titleDTO{}, "total": 0, "nextOffset": 0})
		return
	}
	limit := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	offset := 0
	// Глубже десятка страниц по trigram-поиску не спрашивают: это дорогой
	// запрос, а прокрутка витрины ходит по странице за раз, не прыгая.
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 && n <= 10000 {
		offset = n
	}
	page, err := s.catalog.Search(r.Context(), q, limit, offset)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	out := make([]titleDTO, 0, len(page.Items))
	for _, t := range page.Items {
		out = append(out, titleDTO{ID: int64(t.ID), Kind: string(t.Kind), Title: t.Title,
			Year: t.Year, HasArt: t.Poster != ""})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"items":      out,
		"total":      page.Total,
		"nextOffset": page.Next,
		"hasMore":    page.HasMore,
	})
}

// handleSearchSuggest — несколько тайтлов под строкой поиска, пока человек
// набирает: щелчок ведёт прямо на страницу тайтла, минуя выдачу.
//
// Короче двух букв не ищем: на одну букву подходит полкаталога, и
// подсказка ничего не подсказывает. Ответ можно недолго держать в кеше
// браузера: стирая букву, человек повторяет только что заданный запрос.
func (s *Server) handleSearchSuggest(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := []titleDTO{}
	if utf8.RuneCountInString(q) >= 2 {
		limit := 6
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 20 {
			limit = n
		}
		items, err := s.catalog.Suggest(r.Context(), q, limit)
		if err != nil {
			httpx.Fail(w, r, s.log, err)
			return
		}
		for _, t := range items {
			out = append(out, titleDTO{ID: int64(t.ID), Kind: string(t.Kind), Title: t.Title,
				Year: t.Year, HasArt: t.Poster != ""})
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleTitle(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "titleID"), 10, 64)
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	// Серии — раньше тайтла: их загрузка может дополнить тайтл площадками
	// (досбор при первом открытии), и страница должна увидеть их сразу.
	eps, err := s.catalog.Episodes(r.Context(), kernel.TitleID(id))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	t, err := s.catalog.Title(r.Context(), kernel.TitleID(id))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	// Прогресс по всем сериям одним запросом: у длинного сериала их под
	// сотню, и отдельный поход за каждой превратил бы открытие страницы
	// в шквал запросов.
	seen, err := s.progressOf(r, kernel.TitleID(id))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	// Кадр, которого в каталоге ещё нет, стоит попросить, если у тайтла
	// есть площадка, отдающая кадры по запросу.
	lazyArt := false
	if s.canThumb != nil {
		for _, src := range t.Sources {
			if !src.Disabled && s.canThumb(src.Ref.ProviderID) {
				lazyArt = true
			}
		}
	}
	out := make([]episodeDTO, 0, len(eps))
	for _, e := range eps {
		d := episodeDTO{
			ID: int64(e.ID), SeasonNo: e.SeasonNo,
			EpisodeNo: e.EpisodeNo, Title: e.Title, DurationSec: e.DurationSec,
			HasArt: e.Thumbnail != "" || lazyArt,
		}
		if p, ok := seen[e.ID]; ok {
			d.PositionMS = p.PositionMS
			d.Status = string(p.Status)
		}
		out = append(out, d)
	}
	// Предпочтения плеера едут вместе с тайтлом: плеер стартует сразу
	// после открытия страницы, и отдельный поход за ними задержал бы
	// выбор дорожки до второго запроса.
	prefs, err := s.marks.Prefs(r.Context(), actorFrom(r.Context()), kernel.TitleID(id))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"title": titleDTO{
			ID: int64(t.ID), Kind: string(t.Kind), Species: string(t.Species()), Title: t.Title,
			Year: t.Year, IMDB: t.IDs.IMDB, Plot: t.Plot, Genres: t.Genres,
			Finished: t.Finished, HasArt: t.Poster != "",
			Sources: s.titleSources(t.Sources),
		},
		"episodes": out,
		"prefs":    prefsDTO{Audio: prefs.Audio, Subtitle: prefs.Subtitle, Source: prefs.Source},
	})
}

type refreshDTO struct {
	// Asked — каких площадок спросили о тайтле; Added — какие к нему привязались;
	// Failed — какие не ответили.
	Asked  []sourceDTO `json:"asked"`
	Added  []sourceDTO `json:"added"`
	Failed []sourceDTO `json:"failed"`
	// Refetched — у каких площадок перечитаны серии; RefetchFailed — какие их не отдали.
	Refetched     []sourceDTO `json:"refetched"`
	RefetchFailed []sourceDTO `json:"refetchFailed"`
	// Sources — площадки тайтла после обновления.
	Sources        []sourceDTO `json:"sources"`
	EpisodesBefore int         `json:"episodesBefore"`
	EpisodesAfter  int         `json:"episodesAfter"`
	// WaitSec — тайтл только что обновляли: площадки не спрашивались, повторить
	// можно через столько секунд.
	WaitSec int `json:"waitSec,omitempty"`
}

func (s *Server) handleRefreshTitle(w http.ResponseWriter, r *http.Request) {
	if s.refresher == nil {
		httpx.Fail(w, r, s.log, kernel.ErrNotFound)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "titleID"), 10, 64)
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	rep, err := s.refresher.Refresh(r.Context(), kernel.TitleID(id))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	t, err := s.catalog.Title(r.Context(), kernel.TitleID(id))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, refreshDTO{
		Asked: s.namedSources(rep.Asked), Added: s.namedSources(rep.Attached),
		Failed: s.namedSources(rep.Failed), Refetched: s.namedSources(rep.Refetched),
		RefetchFailed: s.namedSources(rep.RefetchFailed), Sources: s.titleSources(t.Sources),
		EpisodesBefore: rep.EpisodesBefore, EpisodesAfter: rep.EpisodesAfter, WaitSec: rep.Wait,
	})
}

func (s *Server) namedSources(ids []string) []sourceDTO {
	out := make([]sourceDTO, 0, len(ids))
	for _, id := range ids {
		out = append(out, sourceDTO{ID: id, Name: s.providerName(id)})
	}
	return out
}

// titleSources — площадки тайтла для витрины: без выключенных и без
// повторов (у аниме-площадки на тайтл бывает по странице на сезон).
func (s *Server) titleSources(in []catalog.Source) []sourceDTO {
	out := make([]sourceDTO, 0, len(in))
	seen := map[string]bool{}
	for _, src := range in {
		if src.Disabled || seen[src.Ref.ProviderID] {
			continue
		}
		seen[src.Ref.ProviderID] = true
		out = append(out, sourceDTO{ID: src.Ref.ProviderID, Name: s.providerName(src.Ref.ProviderID)})
	}
	return out
}
