package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/niazleushkin/kino/internal/app/auth"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/bootstrap/providers"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/transport/httpuser"
)

// Состояния площадки в /engine/status.
const (
	stateReady    = "ready"    // подключена и ответила на Describe
	stateStarting = "starting" // подключается: регистрация идёт в фоне, с повторами
	stateSkipped  = "skipped"  // не подключена: см. reason
)

type providerStatus struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type statusDTO struct {
	StartedAt time.Time        `json:"startedAt"`
	Providers []providerStatus `json:"providers"`
	Routes    []string         `json:"routes"`
	// Requests — сколько запросов движок отправил на внешние сайты с запуска.
	Requests egressSnapshot `json:"requests"`
	// Logins — вход в площадки, у которых он есть: есть ли сессия и есть ли
	// чем входить. Ключ — id площадки.
	Logins map[string]loginStatus `json:"logins"`
	// UserAgent — с какой подписью движок ходит наружу и где её задал человек.
	UserAgent userAgentStatus `json:"userAgent"`
}

type userAgentStatus struct {
	// Default — подпись по умолчанию (браузероподобная, с пометкой приложения).
	Default string `json:"default"`
	// Custom — площадки, где человек задал свою подпись ("*" — для всех).
	Custom []string `json:"custom"`
}

type loginStatus struct {
	// Session — человек вошёл: у площадки есть сессия.
	Session bool `json:"session"`
	// CanLogin — задан секрет клиента, вход по коду возможен.
	CanLogin bool `json:"canLogin"`
}

// MarshalJSON пишет статус вместе с прежней формой входа: до logins вход
// площадки лежал ключом верхнего уровня с её id ({"kinopub": {...}}), и
// приложение читает его оттуда. Ключ, занятый полем статуса, не трогаем.
func (d statusDTO) MarshalJSON() ([]byte, error) {
	type plain statusDTO
	b, err := json.Marshal(plain(d))
	if err != nil || len(d.Logins) == 0 {
		return b, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for id, st := range d.Logins {
		if _, taken := m[id]; !taken {
			if m[id], err = json.Marshal(st); err != nil {
				return nil, err
			}
		}
	}
	return json.Marshal(m)
}

// authenticator — то, что статусу нужно от auth.Service.
type authenticator interface {
	Authenticate(ctx context.Context, rawToken string, want account.Space) (auth.Actor, error)
}

// providerStates сводит настройку и фактическое состояние: что просили
// (порядок как в PROVIDERS), что не удалось подключить и почему, что уже
// зарегистрировано. Приложение показывает это в настройках движка — иначе
// человек, включивший площадку без ключа, не узнал бы, почему её нет.
func providerStates(ids []string, issues []providers.Issue, registered func(id string) bool) []providerStatus {
	why := map[string]string{}
	for _, is := range issues {
		why[is.ID] = is.Reason
	}
	out := make([]providerStatus, 0, len(ids))
	for _, id := range ids {
		switch reason, bad := why[id]; {
		case bad:
			out = append(out, providerStatus{ID: id, State: stateSkipped, Reason: reason})
		case registered(id):
			out = append(out, providerStatus{ID: id, State: stateReady})
		default:
			out = append(out, providerStatus{ID: id, State: stateStarting})
		}
	}
	return out
}

// statusHandler отдаёт состояние движка владельцу (по тому же токену, что и
// весь API): площадки, маршруты, время старта.
func statusHandler(a authenticator, reg *appprov.Registry, ids []string, issues []providers.Issue,
	routes []string, started time.Time, egress *egressCounter, logins []*siteLogin, ua *uaPolicy) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ownerOK(a, r) {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
			return
		}
		states := providerStates(ids, issues, func(id string) bool { _, err := reg.Get(id); return err == nil })
		// Причина «нет сессии» у площадки со входом — не техническая строка из
		// хранилища, а то, что человеку делать: войти или вставить ключ.
		ls := map[string]loginStatus{}
		for _, l := range logins {
			ls[l.id] = loginStatus{Session: l.hasSession(), CanLogin: l.canLogin()}
		}
		for i := range states {
			if st, ok := ls[states[i].ID]; ok && states[i].State == stateSkipped && !st.Session {
				states[i].Reason = "нужен вход или ключ"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(statusDTO{
			StartedAt: started,
			Providers: states,
			Routes:    routes,
			Requests:  egress.Snapshot(),
			Logins:    ls,
			UserAgent: userAgentStatus{Default: ua.Default(), Custom: nonNil(ua.Custom())},
		})
	})
}

// readyGate отвечает 503 вместо next, пока pending() площадок ещё регистрируются
// и не прошло grace с запуска. Так «готов» для приложения значит «можно
// спрашивать площадки», а не «порт открыт».
func readyGate(next http.Handler, started time.Time, grace time.Duration, pending func() int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if time.Since(started) < grace && pending() > 0 {
			writeJSONError(w, http.StatusServiceUnavailable, "starting", "площадки подключаются")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ownerOK — запрос несёт токен владельца.
func ownerOK(a authenticator, r *http.Request) bool {
	c, err := r.Cookie(httpuser.SessionCookie)
	if err != nil {
		return false
	}
	_, err = a.Authenticate(r.Context(), c.Value, account.SpaceUser)
	return err == nil
}

// nonNil — пустой список в JSON как [], а не null: приложению проще.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
