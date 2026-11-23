// Package httpadmin — СЛУЖЕБНОЕ пространство.
//
// Server-rendered HTML, а не второй SPA. Причина не в моде: React-админка
// была бы ещё одним клиентом со своим циклом обновления и своим
// версионируемым API-контрактом (ось 4). Здесь полтора десятка экранов
// форм и таблиц с нулевым клиентским состоянием — сервер-рендер дешевле
// во всём, шаблоны вшиты в бинарник и деплоятся вместе с core.
//
// Живёт под префиксом /admin на ОТДЕЛЬНОМ слушателе. Наружу его выводит
// Caddy тем же доменом, что и витрину: ssh-туннель больше не нужен.
// Раз дверь на улице, её держат несколько замков сразу:
//   - вход в два шага: пароль выдаёт ЧАСТИЧНУЮ сессию, годную только на
//     подтверждение; верный код или подпись passkey повышает её и ротирует
//     токен. Нет фактора — тот же шаг настраивает его: QR или passkey;
//   - passkey с проверкой пользователя — оба фактора сразу: вход по нему
//     идёт без пароля (adapter/webauthn, auth.LoginWithPasskey);
//   - sudo-режим: опасные действия просят код заново, если он старше
//     четверти часа (auth.Policy.CanActSensitive);
//   - лимит попыток — по адресу и общий (limiter.go), плюс пять неверных
//     кодов гасят частичную сессию;
//   - cookie только на /admin, HttpOnly, SameSite=Strict, Secure за TLS;
//   - CSP без внешних скриптов, запрет фреймов, no-store на страницах;
//   - изменяющие запросы — только POST со своей же площадки.
package httpadmin

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/adapter/postgres"
	"github.com/niazleushkin/kino/internal/adapter/webauthn"
	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/app/health"
	"github.com/niazleushkin/kino/internal/app/history"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

//go:embed templates/*.html static/*
var files embed.FS

const (
	// Prefix — под ним живёт всё. Один и тот же за Caddy и в туннеле,
	// поэтому ни ссылки, ни cookie не зависят от того, откуда зашли.
	Prefix        = "/admin"
	sessionCookie = "kino_admin"
)

type Server struct {
	auth      *auth.Service
	audit     *postgres.AuditRepo
	sync      *postgres.SyncStateRepo
	play      *postgres.PlaybackSessionRepo
	sessions  *postgres.SessionRepo
	engine    *webauthn.Engine
	publicURL string
	log       *slog.Logger
	tpl       map[string]*template.Template
	https     bool
	limiter   *limiter
	assetV    string

	// Раздел тайтлов: склейка площадок. Включается WithCatalog.
	catalog     *postgres.CatalogRepo
	names       func(string) string
	providerIDs []string

	// Состояние входов площадок. Включается WithProviderAuth.
	reauths  func() []health.Reauth
	authInfo func() map[string]AuthInfo

	// Озвучки: псевдонимы студий. Включается WithVoiceNames.
	voices VoiceNames

	// Перенос с площадки новому человеку. Включается WithHistory.
	history    *history.Service
	historySrc func() (history.Source, error)
	imports    imports

	// reveals — одноразовые секреты между POST и следующим GET: новый
	// пароль, otpauth-адрес. В памяти процесса, живут минуты, читаются
	// один раз — чтобы не класть их ни в cookie, ни в адрес.
	mu         sync.Mutex
	reveals    map[string]reveal
	ceremonies map[string]ceremony
}

type reveal struct {
	Kind  string // password | totp
	Value string
	At    time.Time
}

func New(a *auth.Service, audit *postgres.AuditRepo, sync *postgres.SyncStateRepo,
	play *postgres.PlaybackSessionRepo, sessions *postgres.SessionRepo, engine *webauthn.Engine,
	publicURL string, log *slog.Logger, https bool) (*Server, error) {
	pages := map[string]*template.Template{}
	for _, name := range []string{"dashboard", "users", "user", "import", "invites", "providers", "playback", "audit", "titles", "title", "voices"} {
		t, err := template.New("").Funcs(funcs).ParseFS(files,
			"templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		pages[name] = t
	}
	for _, name := range standalone {
		t, err := template.New("").Funcs(funcs).ParseFS(files, "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		pages[name] = t
	}
	return &Server{
		auth: a, audit: audit, sync: sync, play: play, sessions: sessions, engine: engine,
		publicURL: strings.TrimRight(publicURL, "/"),
		log:       log, tpl: pages, https: https,
		// Десять попыток с адреса и сорок со всех за четверть часа: живому
		// админу хватает с запасом, перебору — нет.
		limiter:    newLimiter(15*time.Minute, 10, 40),
		assetV:     assetVersion(),
		reveals:    map[string]reveal{},
		ceremonies: map[string]ceremony{},
	}, nil
}

// standalone — страницы без общего каркаса: до входа каркас показывать нечего.
var standalone = []string{"login", "factor", "enroll", "error"}

// assetVersion — короткий хеш статики для сброса кеша браузера при выкатке.
func assetVersion() string {
	h := sha256.New()
	for _, name := range []string{"static/admin.css", "static/admin.js", "static/passkey.js", "static/icon.svg"} {
		b, _ := files.ReadFile(name)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

func (s *Server) Routes() http.Handler {
	root := chi.NewRouter()
	root.Use(httpx.RequestID, httpx.Recover(s.log), httpx.Log(s.log), harden)

	// Корень слушателя: сюда приходят по привычке из ssh-туннеля.
	root.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, Prefix+"/", http.StatusFound)
	})
	root.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, kernel.ErrNotFound)
	})

	root.Route(Prefix, func(r chi.Router) {
		r.Handle("/static/*", s.static())

		// Шаг 1: пароль — или сразу ключ, он даёт оба фактора.
		r.Get("/login", s.showLogin)
		r.With(s.limitLogin).Post("/login", s.doLogin)
		r.Post("/login/passkey/begin", s.passkeyLoginBegin)
		r.With(s.limitLogin).Post("/login/passkey/finish", s.passkeyLoginFinish)
		r.Post("/logout", s.doLogout)

		r.Group(func(r chi.Router) {
			r.Use(s.authenticate)
			// Шаг 2: код — или его настройка. Под тем же лимитом, что и
			// пароль: шестизначный код перебирается так же.
			r.Get("/factor", s.showFactor)
			r.With(s.limitLogin).Post("/factor", s.doFactor)
			r.Get("/enroll", s.showEnroll)
			r.With(s.limitLogin).Post("/enroll", s.doEnroll)
			r.Post("/factor/passkey/begin", s.passkeyFactorBegin)
			r.With(s.limitLogin).Post("/factor/passkey/finish", s.passkeyFactorFinish)
			// Регистрация ключа: правила — в auth.CanRegisterPasskey.
			r.Post("/passkeys/begin", s.passkeyRegisterBegin)
			r.Post("/passkeys/finish", s.passkeyRegisterFinish)

			r.Group(func(r chi.Router) {
				r.Use(s.requireElevated)
				r.Get("/", s.showDashboard)

				r.Get("/users", s.showUsers)
				r.Post("/users", s.createUser)
				r.Get("/users/{id}", s.showUser)
				r.Post("/users/{id}/device", s.approveDevice)
				r.Get("/users/{id}/import", s.showImport)
				r.Post("/users/{id}/import/history", s.importHistory)
				r.Post("/users/{id}/import/recent", s.importRecent)
				r.Post("/users/{id}/import/folder", s.importFolder)
				r.Post("/users/{id}/disable", s.disableUser)
				r.Post("/users/{id}/enable", s.enableUser)
				r.Post("/users/{id}/role", s.setRole)
				r.Post("/users/{id}/reset-password", s.resetPassword)
				r.Post("/users/{id}/totp", s.provisionTOTP)
				r.Post("/users/{id}/revoke-sessions", s.revokeSessions)
				r.Post("/users/{id}/passkeys/{pid}/delete", s.deletePasskey)

				r.Get("/invites", s.showInvites)
				r.Post("/invites", s.createInvite)
				r.Post("/invites/{code}/revoke", s.revokeInvite)

				r.Get("/titles", s.showTitles)
				// suggest ДО {id}: иначе «suggest» съелся бы как номер тайтла.
				r.Get("/titles/suggest", s.suggest)
				r.Get("/titles/{id}", s.showTitle)
				r.Post("/titles/{id}/sources/priority", s.setSourcePriority)
				r.Post("/titles/{id}/sources/toggle", s.toggleSource)
				r.Post("/titles/{id}/sources/attach", s.attachSource)
				r.Post("/titles/{id}/sources/detach", s.detachSource)
				r.Post("/titles/{id}/merge", s.mergeTitle)

				r.Get("/voices", s.showVoices)
				r.Post("/voices/merge", s.mergeVoice)
				r.Post("/voices/split", s.splitVoice)
				r.Post("/voices/reject", s.rejectVoice)
				r.Post("/voices/learn", s.learnVoices)

				r.Get("/providers", s.showProviders)
				r.Post("/providers/{provider}/rescan", s.requestRescan)
				r.Get("/playback", s.showPlayback)
				r.Get("/audit", s.showAudit)
			})
		})
	})
	return root
}

// harden — заголовки, которые не зависят от страницы.
//
// CSP запрещает чужие скрипты: страница показывает пароли и секреты второго
// фактора, и один подключённый CDN — это один способ их утащить. Стили
// инлайном разрешены: ширина полоски графика задаётся атрибутом, а
// вынести её в класс дороже, чем стоит риск от инъекции стиля.
func harden(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Robots-Tag", "noindex, nofollow")

		// Изменяющие запросы — только со своей площадки. SameSite=Strict у
		// cookie уже не даёт чужой странице предъявить сессию; это второй
		// замок на случай старого браузера.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameSite(r) {
			http.Error(w, "запрос с чужой площадки", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameSite(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil {
			return false
		}
		host := r.Host
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
			host = fh
		}
		return strings.EqualFold(u.Host, host)
	}
	return r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func (s *Server) static() http.Handler {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	h := http.StripPrefix(Prefix+"/static/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		h.ServeHTTP(w, r)
	})
}

// ── сессия ────────────────────────────────────────────────────────────────

type ctxKey int

const (
	actorKey ctxKey = iota
	userKey
)

func withActor(ctx context.Context, a auth.Actor, u account.User) context.Context {
	return context.WithValue(context.WithValue(ctx, actorKey, a), userKey, u)
}

func actorFrom(ctx context.Context) auth.Actor {
	a, _ := ctx.Value(actorKey).(auth.Actor)
	return a
}

func userFrom(ctx context.Context) account.User {
	u, _ := ctx.Value(userKey).(account.User)
	return u
}

// authenticate пускает любую живую сессию пространства admin — частичную
// или повышенную. Что ей можно, решают requireElevated и Policy.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, Prefix+"/login", http.StatusFound)
			return
		}
		// Пространство admin: пользовательская сессия сюда не пройдёт.
		actor, err := s.auth.Authenticate(r.Context(), c.Value, account.SpaceAdmin)
		if err != nil {
			s.clearCookie(w)
			http.Redirect(w, r, Prefix+"/login", http.StatusFound)
			return
		}
		me, err := s.auth.Me(r.Context(), actor)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(withActor(r.Context(), actor, me)))
	})
}

// requireElevated — только после кода. Частичную сессию уводит на его ввод,
// запоминая, куда человек шёл.
func (s *Server) requireElevated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !actorFrom(r.Context()).Elevated() {
			to := Prefix + "/factor"
			if r.Method == http.MethodGet {
				to += "?next=" + url.QueryEscape(r.URL.RequestURI())
			}
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) setCookie(w http.ResponseWriter, token string, until time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token,
		// Только на /admin: витрине и API эта cookie ни к чему.
		Path:     Prefix,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.https,
		MaxAge: int(time.Until(until).Seconds()),
	})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: Prefix, MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.https})
}

// safeNext пропускает только свои относительные адреса: «вернуться туда,
// куда шёл» не должно превращаться в открытый редирект.
func safeNext(next string) string {
	if strings.HasPrefix(next, Prefix+"/") && !strings.Contains(next, "//") &&
		!strings.ContainsAny(next, "\r\n") {
		return next
	}
	return ""
}

func (s *Server) loginError(w http.ResponseWriter, r *http.Request, status int, text, login string) {
	s.renderStatus(w, r, "login", status, map[string]any{"Error": text, "Login": login})
}

// ── шаг 1: пароль ─────────────────────────────────────────────────────────

func (s *Server) showLogin(w http.ResponseWriter, r *http.Request) {
	// Уже вошедшему форма не нужна: повышенного — на обзор, частичного —
	// на код.
	if c, err := r.Cookie(sessionCookie); err == nil {
		if a, err := s.auth.Authenticate(r.Context(), c.Value, account.SpaceAdmin); err == nil {
			if a.Elevated() {
				http.Redirect(w, r, Prefix+"/", http.StatusFound)
			} else {
				http.Redirect(w, r, Prefix+"/factor", http.StatusFound)
			}
			return
		}
	}
	s.renderStatus(w, r, "login", http.StatusOK, map[string]any{})
}

func (s *Server) doLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.loginError(w, r, http.StatusBadRequest, "Некорректная форма.", "")
		return
	}
	login := strings.TrimSpace(r.PostFormValue("login"))
	sess, token, err := s.auth.Login(r.Context(), auth.Credentials{
		Login: login, Password: r.PostFormValue("password"), Device: "admin-ui",
	}, account.SpaceAdmin)
	if err != nil {
		// Причину не уточняем: «нет пользователя» и «не админ» снаружи
		// обязаны выглядеть одинаково.
		s.log.Warn("вход в админку отклонён", "login", login, "ip", clientIP(r))
		s.loginError(w, r, http.StatusUnauthorized, "Не удалось войти. Проверьте логин и пароль.", login)
		return
	}
	s.setCookie(w, token, sess.ExpiresAt)
	http.Redirect(w, r, Prefix+"/factor", http.StatusFound)
}

func (s *Server) doLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.auth.Logout(r.Context(), c.Value)
	}
	s.clearCookie(w)
	http.Redirect(w, r, Prefix+"/login", http.StatusFound)
}

// ── шаг 2: код ────────────────────────────────────────────────────────────

var factorFlashes = map[string]string{
	"stepup": "Код вводился давно. Подтвердите его заново и повторите действие.",
}

// factors — чем человек может подтвердиться: кодом, ключом, или ничем.
func (s *Server) factors(r *http.Request) (hasTOTP, hasKeys bool) {
	a := actorFrom(r.Context())
	keys, _ := s.auth.ListPasskeys(r.Context(), a, a.UserID)
	return userFrom(r.Context()).TOTPEnabled, len(keys) > 0
}

func (s *Server) renderFactor(w http.ResponseWriter, r *http.Request, status int, next, errText string) {
	hasTOTP, hasKeys := s.factors(r)
	s.renderStatus(w, r, "factor", status, map[string]any{
		"Next": next, "Error": errText,
		"Flash":   factorFlashes[r.URL.Query().Get("m")],
		"Login":   userFrom(r.Context()).Login,
		"HasTOTP": hasTOTP, "HasPasskeys": hasKeys,
	})
}

func (s *Server) showFactor(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	// Нет ни одного фактора — нечего вводить: сразу на настройку. Иначе
	// человек упирается в поле с кодом, которого не существует.
	if hasTOTP, hasKeys := s.factors(r); !hasTOTP && !hasKeys {
		to := Prefix + "/enroll"
		if next != "" {
			to += "?next=" + url.QueryEscape(next)
		}
		http.Redirect(w, r, to, http.StatusFound)
		return
	}
	// Повышенному код не нужен — кроме sudo-режима, когда его попросили
	// явно (m=stepup).
	if actorFrom(r.Context()).Elevated() && r.URL.Query().Get("m") == "" {
		if next == "" {
			next = Prefix + "/"
		}
		http.Redirect(w, r, next, http.StatusFound)
		return
	}
	s.renderFactor(w, r, http.StatusOK, next, "")
}

func (s *Server) doFactor(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.loginError(w, r, http.StatusBadRequest, "Некорректная форма.", "")
		return
	}
	next := safeNext(r.PostFormValue("next"))
	code := strings.ReplaceAll(r.PostFormValue("code"), " ", "")
	sess, token, err := s.auth.ConfirmFactor(r.Context(), actorFrom(r.Context()), code, "admin-ui")
	switch {
	case err == nil:
		s.setCookie(w, token, sess.ExpiresAt)
		if next == "" {
			next = Prefix + "/"
		}
		http.Redirect(w, r, next, http.StatusFound)
	case errors.Is(err, auth.ErrEnrollTOTP):
		to := Prefix + "/enroll"
		if next != "" {
			to += "?next=" + url.QueryEscape(next)
		}
		http.Redirect(w, r, to, http.StatusFound)
	case errors.Is(err, kernel.ErrUnauthorized):
		s.renderFactor(w, r, http.StatusUnauthorized, next, "Код не подошёл. Подождите новый и попробуйте ещё раз.")
	case errors.Is(err, kernel.ErrExpired):
		s.log.Warn("частичная сессия погашена перебором кода", "ip", clientIP(r))
		s.clearCookie(w)
		s.loginError(w, r, http.StatusUnauthorized, "Слишком много неверных кодов. Войдите заново.", "")
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) renderEnroll(w http.ResponseWriter, r *http.Request, status int, enr auth.Enrollment, next, errText string) {
	data := map[string]any{"URL": enr.OTPAuthURL, "Next": next, "Error": errText}
	if qr, err := qrDataURI(enr.OTPAuthURL); err == nil {
		data["QR"] = qr
	}
	s.renderStatus(w, r, "enroll", status, data)
}

func (s *Server) showEnroll(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	enr, err := s.auth.BeginEnrollment(r.Context(), actorFrom(r.Context()))
	switch {
	case err == nil:
		s.renderEnroll(w, r, http.StatusOK, enr, next, "")
	case errors.Is(err, kernel.ErrConflict):
		// Фактор уже есть — значит, нужен код, а не QR.
		http.Redirect(w, r, Prefix+"/factor", http.StatusFound)
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) doEnroll(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.loginError(w, r, http.StatusBadRequest, "Некорректная форма.", "")
		return
	}
	next := safeNext(r.PostFormValue("next"))
	code := strings.ReplaceAll(r.PostFormValue("code"), " ", "")
	a := actorFrom(r.Context())
	sess, token, err := s.auth.CompleteEnrollment(r.Context(), a, code, "admin-ui")
	switch {
	case err == nil:
		s.setCookie(w, token, sess.ExpiresAt)
		if next == "" {
			next = Prefix + "/?m=enrolled"
		}
		http.Redirect(w, r, next, http.StatusFound)
	case errors.Is(err, kernel.ErrUnauthorized):
		// Тот же QR ещё раз: секрет не менялся, человек опечатался или
		// приложение ещё не перещёлкнуло минуту.
		enr, err := s.auth.BeginEnrollment(r.Context(), a)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.renderEnroll(w, r, http.StatusUnauthorized, enr, next, "Код не подошёл. Подождите новый и попробуйте ещё раз.")
	case errors.Is(err, kernel.ErrConflict):
		http.Redirect(w, r, Prefix+"/factor", http.StatusFound)
	case errors.Is(err, kernel.ErrExpired):
		s.clearCookie(w)
		s.loginError(w, r, http.StatusUnauthorized, "Время на настройку вышло. Войдите заново — покажется новый код.", "")
	default:
		s.fail(w, r, err)
	}
}

// limitLogin отсекает перебор до того, как он дойдёт до argon2 или TOTP.
func (s *Server) limitLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(clientIP(r), time.Now()) {
			s.log.Warn("вход в админку: слишком много попыток", "ip", clientIP(r))
			w.Header().Set("Retry-After", "900")
			s.loginError(w, r, http.StatusTooManyRequests, "Слишком много попыток. Подождите четверть часа.", "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP — адрес того, кто стучится, с учётом двух прокси впереди.
//
// X-Real-IP ставит nginx на входе, X-Forwarded-For — он же и Caddy.
// Подделать их может только тот, кто зашёл в обход прокси, то есть уже
// внутри сети; общий лимит в limiter на этот случай и существует.
func clientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if first, _, ok := strings.Cut(v, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ── одноразовые секреты ───────────────────────────────────────────────────

func (s *Server) stash(kind, value string) string {
	key := randomKey()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.reveals {
		if now.Sub(v.At) > 10*time.Minute {
			delete(s.reveals, k)
		}
	}
	s.reveals[key] = reveal{Kind: kind, Value: value, At: now}
	return key
}

func (s *Server) take(key string) *reveal {
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.reveals[key]
	if !ok {
		return nil
	}
	delete(s.reveals, key)
	if time.Since(v.At) > 10*time.Minute {
		return nil
	}
	return &v
}

// ── рендер ────────────────────────────────────────────────────────────────

// flashes — короткие подтверждения после действия. Код в адресе, текст
// здесь: в адрес попадает только то, что можно показать кому угодно.
var flashes = map[string]string{
	"blocked":         "Пользователь заблокирован, все его сессии завершены.",
	"enabled":         "Пользователь разблокирован.",
	"role":            "Роль изменена.",
	"signed_out":      "Все сессии пользователя завершены.",
	"invite_created":  "Инвайт создан, ссылка действует семь дней.",
	"invite_revoked":  "Инвайт отозван.",
	"enrolled":        "Второй фактор настроен. Теперь код из приложения нужен при каждом входе.",
	"passkey_added":   "Passkey добавлен. Им можно входить без пароля и подтверждать действия.",
	"passkey_removed": "Passkey удалён.",
	"last_factor":     "Нельзя удалить единственный фактор: сначала настройте другой.",
	"source_saved":    "Площадка сохранена.",
	"source_attached": "Элемент площадки привязан. Серии пересчитаются при следующем открытии тайтла.",
	"source_detached": "Элемент площадки вынесен в отдельный тайтл.",
	"merged":          "Тайтлы слиты: площадки, серии, прогресс и отметки перенесены сюда.",
	"rescan":          "Пересканирование заявлено: обходчик возьмёт его на ближайшем проходе.",
	"user_created":    "Пользователь заведён. Пустите его телефон кодом — форма ниже.",
	"bad_login":       "Логин — от 3 латинских строчных букв, цифр, «_» и «-».",
	"login_taken":     "Такой логин уже занят.",
	"device_linked":   "Устройство подключено: оно войдёт само в ближайшие секунды.",
	"device_unknown":  "Такого кода нет. Проверьте, не опечатались ли, — или попросите показать новый.",
	"device_expired":  "Код истёк: он живёт десять минут. На телефоне появится новый — введите его.",
	"device_conflict": "На этот код уже ответили или пользователь заблокирован.",
	"import_started":  "Перенос запущен в фоне. Обновите страницу через минуту — итог появится ниже.",
	"import_busy":     "Уже идёт другой перенос: площадка одна на всех, по очереди. Попробуйте, когда он закончится.",
	"voice_merged":    "Склеено. Списки озвучек в плеере пересобраны — новое имя уже одно.",
	"voice_split":     "Разъединено. Обучение по транслиту эту пару больше не склеит.",
	"voice_rejected":  "Запомнено как разные — больше не предложим.",
	"voice_learned":   "Сведено по транслиту: всё, что сошлось, — в таблице ниже.",
	"voice_bad":       "Так склеить нельзя: оба поля нужны и должны быть разными, а вид озвучки (дубляж, многоголосый…) склеивает разбор сам.",
	"voice_gone":      "Такой склейки уже нет — кто-то разъединил раньше.",
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page, title string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Title"] = title
	data["Active"] = page
	data["Me"] = userFrom(r.Context())
	data["Reauths"] = s.reauthBanner()
	if m := r.URL.Query().Get("m"); m != "" {
		data["Flash"] = flashes[m]
	}
	s.renderStatus(w, r, page, http.StatusOK, data)
}

func (s *Server) renderStatus(w http.ResponseWriter, r *http.Request, page string, status int, data map[string]any) {
	data["V"] = s.assetV
	data["Prefix"] = Prefix
	name := "layout"
	for _, p := range standalone {
		if p == page {
			name = page
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Страницы содержат то, что не должно всплыть по «назад» после выхода.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := s.tpl[page].ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render", "page", page, "err", err)
	}
}

// fail показывает человеку ошибку страницей, а не JSON: это админка,
// и читать её будет человек в браузере.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	code, _ := httpx.Status(err)
	if code >= 500 {
		s.log.ErrorContext(r.Context(), "admin request failed",
			"err", err, "path", r.URL.Path, "method", r.Method)
	}
	text := map[int]string{
		http.StatusNotFound:     "Такой страницы нет.",
		http.StatusForbidden:    "Нет прав на это действие.",
		http.StatusBadRequest:   "Запрос составлен неверно.",
		http.StatusUnauthorized: "Нужно войти заново.",
		http.StatusConflict:     "Действие конфликтует с текущим состоянием.",
		http.StatusGone:         "Срок истёк.",
	}[code]
	if text == "" {
		text = "Внутренняя ошибка. Подробности в журнале core."
	}
	s.renderStatus(w, r, "error", code, map[string]any{
		"Code": code, "Text": text, "Me": userFrom(r.Context()),
	})
}

// publicBase — адрес, каким платформу видит человек снаружи: ссылка на
// инвайт уедет в переписку, и localhost в ней бесполезен.
func (s *Server) publicBase(r *http.Request) string {
	if s.publicURL != "" {
		return s.publicURL
	}
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}
