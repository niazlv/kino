package httpadmin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/app/history"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Новый человек в семье: завести, пустить его телефон по коду, перенести
// ему историю и папку с площадки. Всё отсюда, потому что у новичка ещё нет
// ни одного устройства, где он вошёл, а перенос тратит общий аккаунт
// площадки и не должен висеть кнопкой у каждого.

// WithHistory включает перенос с площадки. Источник берётся лениво, как у
// витрины: площадка может быть недоступна на старте.
func (s *Server) WithHistory(h *history.Service, src func() (history.Source, error)) *Server {
	s.history, s.historySrc = h, src
	return s
}

// ── пользователь и устройство ────────────────────────────────────────────

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, kernel.ErrInvalidInput)
		return
	}
	login := strings.ToLower(strings.TrimSpace(r.PostFormValue("login")))
	name := strings.TrimSpace(r.PostFormValue("name"))
	u, pw, err := s.auth.CreateUser(r.Context(), actorFrom(r.Context()), login, name)
	switch {
	case errors.Is(err, kernel.ErrInvalidInput):
		http.Redirect(w, r, Prefix+"/users?m=bad_login", http.StatusSeeOther)
		return
	case errors.Is(err, kernel.ErrConflict):
		http.Redirect(w, r, Prefix+"/users?m=login_taken", http.StatusSeeOther)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("%s/users/%d?m=user_created&reveal=%s",
		Prefix, u.ID, s.stash("created", pw)), http.StatusSeeOther)
}

// approveDevice — код с экрана телефона, введённый в карточке пользователя.
// Ошибки кода — не страница ошибки, а строка над карточкой: код набирают
// руками, и опечатка — обычное дело.
func (s *Server) approveDevice(w http.ResponseWriter, r *http.Request) {
	s.userAction(w, r, func(id kernel.UserID, _ string) (string, error) {
		d, err := s.auth.ApproveDeviceFor(r.Context(), actorFrom(r.Context()), id, r.PostFormValue("code"))
		switch {
		case err == nil:
			return "?m=device_linked&reveal=" + s.stash("device", d.Device), nil
		case errors.Is(err, auth.ErrStepUp):
			return "", err
		case errors.Is(err, kernel.ErrNotFound):
			return "?m=device_unknown", nil
		case errors.Is(err, kernel.ErrExpired):
			return "?m=device_expired", nil
		case errors.Is(err, kernel.ErrConflict):
			return "?m=device_conflict", nil
		}
		return "", err
	})
}

// ── перенос с площадки ───────────────────────────────────────────────────

// importJob — перенос, идущий в фоне. Позиции — это запрос на каждый тайтл
// списка, минута-другая: держать ради них запрос открытым нельзя (прокси
// перед админкой рвёт через минуту), поэтому перенос уходит в фон, а
// страница показывает, чем закончился последний.
type importJob struct {
	What     string
	Started  time.Time
	Finished time.Time
	Summary  string
	Err      string
}

func (j importJob) Running() bool { return j.Finished.IsZero() }

// imports — по одному переносу за раз на всю платформу: аккаунт площадки
// один, и два переноса параллельно — удвоенная нагрузка на тот же лимит,
// что держит просмотр.
type imports struct {
	mu      sync.Mutex
	running bool
	last    map[kernel.UserID]importJob
}

func (m *imports) start(u kernel.UserID, what string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return false
	}
	if m.last == nil {
		m.last = map[kernel.UserID]importJob{}
	}
	m.running = true
	m.last[u] = importJob{What: what, Started: time.Now()}
	return true
}

func (m *imports) finish(u kernel.UserID, summary string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = false
	j := m.last[u]
	j.Finished, j.Summary = time.Now(), summary
	if err != nil {
		j.Err = err.Error()
	}
	m.last[u] = j
}

func (m *imports) lastFor(u kernel.UserID) (importJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.last[u]
	return j, ok
}

func (s *Server) showImport(w http.ResponseWriter, r *http.Request) {
	id, err := userID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	a := actorFrom(r.Context())
	u, err := s.auth.GetUser(r.Context(), a, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data := map[string]any{"User": u}
	if j, ok := s.imports.lastFor(id); ok {
		data["Job"] = j
	}
	// Папки — один запрос к площадке. Отказ не прячет страницу: перенос
	// истории от папок не зависит.
	if s.history == nil {
		data["FoldersErr"] = "перенос не подключён"
	} else if src, err := s.historySrc(); err != nil {
		data["FoldersErr"] = "площадка недоступна"
	} else if folders, err := s.history.Folders(r.Context(), a, src); err != nil {
		s.log.WarnContext(r.Context(), "папки площадки не получены", "err", err)
		data["FoldersErr"] = "площадка не отдала папки — попробуйте обновить страницу"
	} else {
		data["Folders"] = folders
	}
	s.render(w, r, "import", "Перенос из kinopub · "+u.DisplayName, data)
}

func (s *Server) importHistory(w http.ResponseWriter, r *http.Request) {
	s.runImport(w, r, "«я смотрю» и позиции", func(ctx context.Context, a auth.Actor, id kernel.UserID, src history.Source) (string, error) {
		wl, err := s.history.ImportWatchlist(ctx, a, id, src)
		if err != nil {
			return "", err
		}
		res, err := s.history.Import(ctx, a, id, src)
		summary := fmt.Sprintf("в «Слежу» %d из %d; позиций перенесено %d из %d",
			wl.Added, wl.Seen, res.Imported, res.Seen)
		if res.Fetched > 0 {
			summary += fmt.Sprintf(", догружено тайтлов %d", res.Fetched)
		}
		if res.Skipped > 0 {
			summary += fmt.Sprintf(", свои позиции не тронуты у %d", res.Skipped)
		}
		if res.NoEpisode > 0 {
			summary += fmt.Sprintf(", не нашлось серий %d", res.NoEpisode)
		}
		return summary, err
	})
}

// importRecent — журнал просмотров площадки за последние дни, с датами.
func (s *Server) importRecent(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("days")))
	if err != nil || days < 1 || days > 365 {
		s.fail(w, r, kernel.ErrInvalidInput)
		return
	}
	since := time.Now().AddDate(0, 0, -days)
	s.runImport(w, r, fmt.Sprintf("журнал просмотров за %s", plural(days, "день", "дня", "дней")),
		func(ctx context.Context, a auth.Actor, id kernel.UserID, src history.Source) (string, error) {
			res, err := s.history.ImportRecent(ctx, a, id, src, since)
			summary := fmt.Sprintf("серий перенесено %d из %d", res.Imported, res.Seen)
			if res.Fetched > 0 {
				summary += fmt.Sprintf(", догружено тайтлов %d", res.Fetched)
			}
			if res.Skipped > 0 {
				summary += fmt.Sprintf(", свои позиции не тронуты у %d", res.Skipped)
			}
			if res.NoEpisode > 0 {
				summary += fmt.Sprintf(", не нашлось серий %d", res.NoEpisode)
			}
			return summary, err
		})
}

func (s *Server) importFolder(w http.ResponseWriter, r *http.Request) {
	folder := strings.TrimSpace(r.PostFormValue("folder"))
	name := strings.TrimSpace(r.PostFormValue("folder_name"))
	if _, err := strconv.Atoi(folder); err != nil {
		s.fail(w, r, kernel.ErrInvalidInput)
		return
	}
	s.runImport(w, r, "папка «"+name+"» в «Буду смотреть»", func(ctx context.Context, a auth.Actor, id kernel.UserID, src history.Source) (string, error) {
		res, err := s.history.ImportFolder(ctx, a, id, src, folder)
		summary := fmt.Sprintf("перенесено %d из %d", res.Added, res.Seen)
		if res.Repeats > 0 {
			summary += fmt.Sprintf(" (повторов в папке %d)", res.Repeats)
		}
		return summary, err
	})
}

// runImport проверяет права и запускает перенос в фоне. Итог — в памяти
// для страницы и в журнал: «откуда у брата четыреста тайтлов» через месяц
// должно иметь ответ.
func (s *Server) runImport(w http.ResponseWriter, r *http.Request, what string,
	job func(context.Context, auth.Actor, kernel.UserID, history.Source) (summary string, err error)) {
	id, err := userID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	a := actorFrom(r.Context())
	if err := (auth.Policy{}).CanImportFor(a, id); err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.auth.GetUser(r.Context(), a, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.history == nil {
		s.fail(w, r, kernel.ErrUnavailable)
		return
	}
	src, err := s.historySrc()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page := fmt.Sprintf("%s/users/%d/import", Prefix, id)
	if !s.imports.start(id, what) {
		http.Redirect(w, r, page+"?m=import_busy", http.StatusSeeOther)
		return
	}
	// Запрос закончится раньше переноса: контекст отвязан от него, но с
	// потолком, чтобы зависшая площадка не держала замок вечно.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	go func() {
		defer cancel()
		var (
			summary string
			err     error
		)
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("перенос упал: %v", p)
			}
			s.imports.finish(id, summary, err)
			after := map[string]string{"what": what, "result": summary}
			if err != nil {
				after["error"] = err.Error()
				s.log.WarnContext(ctx, "перенос из kinopub", "user", u.Login, "what", what, "err", err)
			}
			_ = s.audit.Record(ctx, a.UserID, "user.import_kinopub", u.Login, "", nil, after)
		}()
		summary, err = job(ctx, a, id, src)
	}()
	http.Redirect(w, r, page+"?m=import_started", http.StatusSeeOther)
}
