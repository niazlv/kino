package engine

// Вход в площадку из приложения (device flow) — у площадок, у которых есть
// вход (appprov.DeviceLogin). Движок держит начатый вход
// и опрашивает авторизацию площадки, а приложение показывает человеку код и
// ждёт.
//
// Опрос авторизации — не чаще интервала, который она сама назвала (и реже по
// slow_down): приложение вправе спрашивать движок хоть раз в секунду, на
// площадку это не ложится. Где лежит выданная сессия — дело площадки; после
// входа движок перезапускается, чтобы площадка поднялась уже с ней.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	appprov "github.com/niazleushkin/kino/internal/app/provider"
)

// Состояния входа для приложения.
const (
	loginNone    = "none"    // входа не начинали
	loginPending = "pending" // ждём подтверждения человека
	loginOK      = "ok"      // сессия получена и записана — перезапустить движок
	loginExpired = "expired" // код истёк
	loginDenied  = "denied"  // человек отклонил
	loginError   = "error"   // клиент не признан или авторизация лежит — см. message
)

// loginMaxFailures — сколько подряд сбоев связи терпим, прежде чем сдаться.
const loginMaxFailures = 5

type loginView struct {
	UserCode                string    `json:"userCode"`
	VerificationURI         string    `json:"verificationUri"`
	VerificationURIComplete string    `json:"verificationUriComplete,omitempty"`
	ExpiresAt               time.Time `json:"expiresAt"`
	IntervalSec             int       `json:"intervalSec"`
}

type loginState struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

type pendingLogin struct {
	dc       appprov.DeviceCode
	interval time.Duration
	lastPoll time.Time
	failures int
}

// siteLogin — вход в одну площадку: её DeviceLogin и начатый вход.
type siteLogin struct {
	id  string
	l   appprov.DeviceLogin
	now func() time.Time
	log *slog.Logger

	mu      sync.Mutex
	pending *pendingLogin
}

func newSiteLogin(id string, l appprov.DeviceLogin, log *slog.Logger) *siteLogin {
	return &siteLogin{id: id, l: l, now: time.Now, log: log}
}

// siteLogins — входы по id площадки, в порядке id (для маршрутов и статуса).
func siteLogins(m map[string]appprov.DeviceLogin, log *slog.Logger) []*siteLogin {
	out := make([]*siteLogin, 0, len(m))
	for id, l := range m {
		out = append(out, newSiteLogin(id, l, log.With("provider", id)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// canLogin — есть чем входить (у площадки задан ключ клиента и есть куда
// положить сессию).
func (l *siteLogin) canLogin() bool { return l != nil && l.l.Ready() }

func (l *siteLogin) hasSession() bool { return l != nil && l.l.HasSession() }

// begin начинает вход: новый код, прежний (если был) забывается.
func (l *siteLogin) begin(ctx context.Context) (loginView, error) {
	if !l.canLogin() {
		return loginView{}, appprov.ErrLoginNotConfigured
	}
	dc, err := l.l.Start(ctx)
	if err != nil {
		return loginView{}, err
	}
	l.mu.Lock()
	l.pending = &pendingLogin{dc: dc, interval: dc.Interval}
	l.mu.Unlock()
	return loginView{
		UserCode: dc.UserCode, VerificationURI: dc.VerificationURI,
		VerificationURIComplete: dc.VerificationURIComplete, ExpiresAt: dc.ExpiresAt,
		IntervalSec: int(dc.Interval / time.Second),
	}, nil
}

// poll отвечает приложению, где вход. К авторизации ходит не чаще интервала.
func (l *siteLogin) poll(ctx context.Context) loginState {
	l.mu.Lock()
	p := l.pending
	if p == nil {
		l.mu.Unlock()
		return loginState{State: loginNone}
	}
	now := l.now()
	if now.After(p.dc.ExpiresAt) {
		l.pending = nil
		l.mu.Unlock()
		return loginState{State: loginExpired, Message: "код истёк — начните вход заново"}
	}
	if now.Before(p.lastPoll.Add(p.interval)) {
		// Рано: авторизацию не тревожим, человек ещё подтверждает.
		l.mu.Unlock()
		return loginState{State: loginPending}
	}
	p.lastPoll = now
	code := p.dc.Code
	l.mu.Unlock()

	err := l.l.Poll(ctx, code)

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending != p { // за время опроса вход начали заново или вышли
		return loginState{State: loginPending}
	}
	site := l.l.Site()
	switch {
	case err == nil:
		l.pending = nil
		l.log.Info("вход выполнен, сессия сохранена")
		return loginState{State: loginOK}
	case errors.Is(err, appprov.ErrLoginSave):
		l.pending = nil
		return loginState{State: loginError, Message: "сессия получена, но не записалась: " + err.Error()}
	case errors.Is(err, appprov.ErrLoginPending):
		p.failures = 0
		return loginState{State: loginPending}
	case errors.Is(err, appprov.ErrLoginSlowDown):
		p.failures = 0
		p.interval += 5 * time.Second
		return loginState{State: loginPending}
	case errors.Is(err, appprov.ErrLoginExpired):
		l.pending = nil
		return loginState{State: loginExpired, Message: "код истёк — начните вход заново"}
	case errors.Is(err, appprov.ErrLoginDenied):
		l.pending = nil
		return loginState{State: loginDenied, Message: "вход отклонён на странице " + site}
	case errors.Is(err, appprov.ErrLoginRejected):
		l.pending = nil
		return loginState{State: loginError, Message: site + " не признал клиента: проверьте секрет"}
	default:
		// Сбой связи или лежащая авторизация: код ещё жив, пробуем снова — но не вечно.
		p.failures++
		l.log.Debug("опрос входа не удался", "err", err, "подряд", p.failures)
		if p.failures >= loginMaxFailures {
			l.pending = nil
			return loginState{State: loginError, Message: "нет связи с " + site + ": " + err.Error()}
		}
		return loginState{State: loginPending}
	}
}

// logout — выход: сессия стирается, начатый вход забывается.
func (l *siteLogin) logout() error {
	l.mu.Lock()
	l.pending = nil
	l.mu.Unlock()
	return l.l.Logout()
}

// requireOwner пускает дальше только владельца (по токену, как весь API).
func requireOwner(a authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !ownerOK(a, r) {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// loginRoutes монтирует ручки входа каждой площадки с входом:
// /engine/<id>/login — POST начать, GET где вход, DELETE выйти. Всё под
// токеном владельца.
func loginRoutes(mux *http.ServeMux, a authenticator, logins []*siteLogin) {
	owner := requireOwner(a)
	for _, l := range logins {
		path := "/engine/" + l.id + "/login"
		mux.Handle("POST "+path, owner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			view, err := l.begin(r.Context())
			site := l.l.Site()
			switch {
			case errors.Is(err, appprov.ErrLoginNotConfigured):
				writeJSONError(w, http.StatusConflict, "no_secret", fmt.Sprintf("нужен секрет OAuth-клиента %s", site))
			case errors.Is(err, appprov.ErrLoginRejected):
				writeJSONError(w, http.StatusBadGateway, "client_rejected", err.Error())
			case err != nil:
				writeJSONError(w, http.StatusBadGateway, "upstream", site+" не ответил: "+err.Error())
			default:
				writeJSON(w, http.StatusOK, view)
			}
		})))
		mux.Handle("GET "+path, owner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, l.poll(r.Context()))
		})))
		mux.Handle("DELETE "+path, owner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := l.logout(); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "logout", err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})))
	}
}
