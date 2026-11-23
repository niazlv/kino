package httpadmin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/adapter/postgres"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// Тайтлы и их площадки.
//
// Склейка произведений между площадками автоматическая и осторожная, но
// ошибается в обе стороны. Здесь человек видит, с каких площадок собран
// тайтл и чем склеен, и чинит: меняет приоритет площадки для этого тайтла
// («это аниме смотрим с AnimeGO»), выключает площадку, переносит элемент
// площадки в другой тайтл или сезон, сливает дубли.

// WithCatalog включает раздел тайтлов. names — человеческие названия
// площадок, ids — подключённые площадки (для формы привязки).
func (s *Server) WithCatalog(c *postgres.CatalogRepo, names func(string) string, ids []string) *Server {
	s.catalog, s.names, s.providerIDs = c, names, ids
	return s
}

func (s *Server) providerName(id string) string {
	if s.names == nil {
		return id
	}
	return s.names(id)
}

type sourceView struct {
	postgres.SourceRow
	Name string
}

// titlesPage — строк на страницу списков тайтлов.
const titlesPage = 50

// QUEUE_LABELS — очереди дедупликации: что показывать в табах.
var queueLabels = []struct{ Kind, Label string }{
	{"recent", "Новые"},
	{"one", "Одна площадка"},
	{"conflict", "Конфликт матча"},
	{"nomal", "Без MAL"},
}

func (s *Server) showTitles(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		s.fail(w, r, kernel.ErrNotFound)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := r.URL.Query().Get("f")
	if kind == "" {
		kind = "recent"
	}
	offset := 0
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 && n <= 100000 {
		offset = n
	}

	var rows []postgres.TitleRow
	total := 0
	if q != "" {
		var err error
		if rows, total, err = s.catalog.AdminSearchTitles(r.Context(), q, titlesPage, offset); err != nil {
			s.fail(w, r, err)
			return
		}
	} else {
		if !validQueue(kind) {
			s.fail(w, r, kernel.ErrInvalidInput)
			return
		}
		var err error
		if rows, total, err = s.catalog.AdminTitleQueue(r.Context(), kind, titlesPage, offset); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	counts, err := s.catalog.AdminTitleQueueCounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tabs := make([]queueTab, 0, len(queueLabels))
	for _, l := range queueLabels {
		tabs = append(tabs, queueTab{Kind: l.Kind, Label: l.Label, Count: counts[l.Kind]})
	}
	s.render(w, r, "titles", "Тайтлы", map[string]any{
		"Q": q, "Kind": kind, "Tabs": tabs, "Rows": rows,
		"Total": total, "NextOffset": nextOffset(offset, len(rows), total),
	})
}

type queueTab struct {
	Kind  string
	Label string
	Count int
}

func validQueue(kind string) bool {
	for _, l := range queueLabels {
		if l.Kind == kind {
			return true
		}
	}
	return false
}

// nextOffset — смещение следующей страницы или 0, если это была последняя.
func nextOffset(offset, got, total int) int {
	if got == 0 || offset+got >= total {
		return 0
	}
	return offset + got
}

// suggest подсказывает каталог: тайтлы для слияния и элементы площадок для
// привязки. Один эндпоинт на оба списка: форма знает, что ей нужно, по
// параметру kind, а выросшие подсказки не плодят ручек.
func (s *Server) suggest(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		s.fail(w, r, kernel.ErrNotFound)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		httpx.JSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	switch r.URL.Query().Get("kind") {
	case "refs":
		rows, err := s.catalog.AdminSearchRefs(r.Context(), q, 8)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := make([]map[string]string, 0, len(rows))
		for _, ref := range rows {
			out = append(out, map[string]string{
				"value":  ref.ItemID,
				"label":  ref.ItemID + " · " + s.providerName(ref.ProviderID) + " · " + ref.Title,
				"detail": "#" + strconv.FormatInt(int64(ref.TitleID), 10),
			})
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"items": out})
	default:
		rows, _, err := s.catalog.AdminSearchTitles(r.Context(), q, 8, 0)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := make([]map[string]string, 0, len(rows))
		for _, t := range rows {
			label := t.Title
			if t.Year > 0 {
				label += " (" + strconv.Itoa(t.Year) + ")"
			}
			out = append(out, map[string]string{
				"value":  strconv.FormatInt(int64(t.ID), 10),
				"label":  "#" + strconv.FormatInt(int64(t.ID), 10) + " · " + label,
				"detail": strconv.Itoa(t.Sources) + " площадок",
			})
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

func (s *Server) showTitle(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		s.fail(w, r, kernel.ErrNotFound)
		return
	}
	id, err := titleID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.catalog.Title(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.catalog.AdminSources(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	views := make([]sourceView, 0, len(rows))
	for _, row := range rows {
		views = append(views, sourceView{SourceRow: row, Name: s.providerName(row.ProviderID)})
	}
	providers := make([]sourceView, 0, len(s.providerIDs))
	for _, p := range s.providerIDs {
		providers = append(providers, sourceView{SourceRow: postgres.SourceRow{ProviderID: p}, Name: s.providerName(p)})
	}
	s.render(w, r, "title", t.Title, map[string]any{
		"T": t, "Sources": views, "Providers": providers,
	})
}

// titleAction — общая часть правок: разбор формы, журнал, возврат на
// карточку тайтла.
func (s *Server) titleAction(w http.ResponseWriter, r *http.Request, action string,
	do func(id kernel.TitleID) (back kernel.TitleID, flash string, err error)) {
	if s.catalog == nil {
		s.fail(w, r, kernel.ErrNotFound)
		return
	}
	id, err := titleID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, kernel.ErrInvalidInput)
		return
	}
	back, flash, err := do(id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	target := r.PostFormValue("provider") + ":" + r.PostFormValue("item")
	if target == ":" {
		target = "title:" + strconv.FormatInt(int64(id), 10)
	}
	after := map[string]string{}
	for k := range r.PostForm {
		after[k] = r.PostFormValue(k)
	}
	_ = s.audit.Record(r.Context(), actorFrom(r.Context()).UserID, action, target,
		r.PostFormValue("reason"), map[string]any{"title": id}, after)
	http.Redirect(w, r, Prefix+"/titles/"+strconv.FormatInt(int64(back), 10)+"?m="+flash, http.StatusSeeOther)
}

func (s *Server) setSourcePriority(w http.ResponseWriter, r *http.Request) {
	s.titleAction(w, r, "source.priority", func(id kernel.TitleID) (kernel.TitleID, string, error) {
		var prio *int
		if v := strings.TrimSpace(r.PostFormValue("priority")); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return 0, "", kernel.ErrInvalidInput
			}
			prio = &n
		}
		return id, "source_saved", s.catalog.SetSourcePriority(r.Context(),
			r.PostFormValue("provider"), r.PostFormValue("item"), prio)
	})
}

func (s *Server) toggleSource(w http.ResponseWriter, r *http.Request) {
	s.titleAction(w, r, "source.disabled", func(id kernel.TitleID) (kernel.TitleID, string, error) {
		off := r.PostFormValue("off") == "true"
		return id, "source_saved", s.catalog.SetSourceDisabled(r.Context(),
			r.PostFormValue("provider"), r.PostFormValue("item"), off)
	})
}

func (s *Server) attachSource(w http.ResponseWriter, r *http.Request) {
	s.titleAction(w, r, "source.attach", func(id kernel.TitleID) (kernel.TitleID, string, error) {
		provider, item := strings.TrimSpace(r.PostFormValue("provider")), strings.TrimSpace(r.PostFormValue("item"))
		if provider == "" || item == "" {
			return 0, "", kernel.ErrInvalidInput
		}
		// Пустое поле — ноль: элемент площадки покрывает весь тайтл в
		// своей нумерации.
		season := 0
		if v := strings.TrimSpace(r.PostFormValue("season")); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return 0, "", kernel.ErrInvalidInput
			}
			season = n
		}
		to := id
		if v := strings.TrimSpace(r.PostFormValue("to")); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return 0, "", kernel.ErrInvalidInput
			}
			to = kernel.TitleID(n)
		}
		return to, "source_attached", s.catalog.AttachSource(r.Context(), provider, item, to, season)
	})
}

func (s *Server) detachSource(w http.ResponseWriter, r *http.Request) {
	s.titleAction(w, r, "source.detach", func(id kernel.TitleID) (kernel.TitleID, string, error) {
		fresh, err := s.catalog.DetachSource(r.Context(), r.PostFormValue("provider"), r.PostFormValue("item"))
		return fresh, "source_detached", err
	})
}

func (s *Server) mergeTitle(w http.ResponseWriter, r *http.Request) {
	s.titleAction(w, r, "title.merge", func(id kernel.TitleID) (kernel.TitleID, string, error) {
		into, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("into")), 10, 64)
		if err != nil {
			return 0, "", kernel.ErrInvalidInput
		}
		if err := s.catalog.MergeTitles(r.Context(), id, kernel.TitleID(into)); err != nil {
			return 0, "", err
		}
		return kernel.TitleID(into), "merged", nil
	})
}

func titleID(r *http.Request) (kernel.TitleID, error) {
	n, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || n <= 0 {
		return 0, kernel.ErrInvalidInput
	}
	return kernel.TitleID(n), nil
}
