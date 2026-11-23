package httpuser

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/postgres"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// Feeds — производные ленты. Тип адаптера здесь потому, что это чтение
// для витрины без бизнес-правил: заводить сервис-прослойку ради двух
// запросов значило бы плодить слои без содержания.
type Feeds interface {
	NewEpisodes(ctx context.Context, u kernel.UserID, limit, offset int,
		onlyUnwatched bool) (items []postgres.NewEpisodeItem, total, active int, err error)
	History(ctx context.Context, u kernel.UserID, limit, offset int,
		f postgres.HistoryFilter) ([]postgres.HistoryItem, int, error)
}

type newEpisodeDTO struct {
	TitleID   int64  `json:"titleId"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Year      int    `json:"year"`
	HasArt    bool   `json:"hasArt"`
	Unwatched int    `json:"unwatched"`
	Total     int    `json:"total"`
}

// newEpisodesPage — карточек на страницу ленты.
const newEpisodesPage = 60

// handleNewEpisodes отдаёт страницу ленты слежения. Ответ — страница
// с total/activeTotal и nextOffset: лента с подгрузкой, досмотренное
// приходит в хвостовых страницах, а не прячется за капом.
func (s *Server) handleNewEpisodes(w http.ResponseWriter, r *http.Request) {
	// only=new -> лишь то, где есть непросмотренное; иначе всё слежение
	// со счётчиками. Разница между «новыми эпизодами» и «я смотрю» ровно
	// в этом, поэтому и эндпоинт один.
	onlyNew := r.URL.Query().Get("only") == "new"
	offset := 0
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 && n <= 10000 {
		offset = n
	}
	items, total, active, err := s.feeds.NewEpisodes(r.Context(),
		actorFrom(r.Context()).UserID, newEpisodesPage, offset, onlyNew)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	out := make([]newEpisodeDTO, 0, len(items))
	for _, it := range items {
		out = append(out, newEpisodeDTO{
			TitleID: int64(it.TitleID), Title: it.Title, Kind: it.Kind, Year: it.Year,
			HasArt: it.HasArt, Unwatched: it.Unwatched, Total: it.Total,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"items": out, "total": total, "activeTotal": active,
		"nextOffset": offset + len(out), "hasMore": offset+len(out) < total,
	})
}

type historyDTO struct {
	EpisodeID  int64     `json:"episodeId"`
	TitleID    int64     `json:"titleId"`
	Title      string    `json:"title"`
	Kind       string    `json:"kind"`
	SeasonNo   int       `json:"seasonNo"`
	EpisodeNo  int       `json:"episodeNo"`
	PositionMS int64     `json:"positionMs"`
	DurationMS int64     `json:"durationMs"`
	Status     string    `json:"status"`
	HasArt     bool      `json:"hasArt"`
	HasShot    bool      `json:"hasShot"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// historyPage — строк на странице.
const historyPage = 60

// handleHistory отдаёт страницу истории по номеру и число страниц.
//
// Номер за пределами — ошибка ввода, а не тихая первая страница: иначе
// баг клиента, попавшего на «страницу 0», молча зациклил бы его.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
			return
		}
		page = n
	}
	// Вид — только из известных: чужое значение не фильтр, а опечатка.
	f := postgres.HistoryFilter{Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	switch k := r.URL.Query().Get("kind"); k {
	case "", "movie", "series":
		f.Kind = k
	default:
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	if len(f.Query) > 100 {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	items, total, err := s.feeds.History(r.Context(), actorFrom(r.Context()).UserID,
		historyPage, (page-1)*historyPage, f)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	out := make([]historyDTO, 0, len(items))
	for _, it := range items {
		out = append(out, historyDTO{
			EpisodeID: int64(it.EpisodeID), TitleID: int64(it.TitleID), Title: it.Title,
			Kind: it.Kind, SeasonNo: it.SeasonNo, EpisodeNo: it.EpisodeNo,
			PositionMS: it.PositionMS, DurationMS: it.DurationMS,
			Status: it.Status, HasArt: it.HasArt, HasShot: it.HasShot, UpdatedAt: it.UpdatedAt,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"items": out, "page": page, "pages": (total + historyPage - 1) / historyPage, "total": total,
	})
}
