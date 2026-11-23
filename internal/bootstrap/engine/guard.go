package engine

// Доступ к локальному движку (ADR-0015, раздел «Доступ»).
//
// Движок слушает петлю и отвечает одному владельцу, но «петля» — не защита:
// в браузере владельца открыты чужие страницы, и любая из них может ударить
// в 127.0.0.1. На сервере от этого спасает SameSite у cookie сессии; движок
// сессию сам не подставляет (так бы эта защита исчезла), а требует токен,
// который знает только владелец: его же читает приложение, запуская движок.
//
//   - loopbackGuard — Host и Origin только свои (DNS-rebinding и запросы
//     с чужих страниц отсекаются до всякой логики);
//   - ownerSession — токен владельца живёт в файле рядом с базой либо
//     задаётся ENGINE_TOKEN; в БД только хеш, как у любой сессии;
//   - connectHandler — разовый вход браузера по ключу из лога запуска;
//   - localModeGuard — выход, пароль, чужие сессии в этом режиме не нужны,
//     а «Выйти» убило бы единственную сессию до перезапуска.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/transport/httpuser"
)

// minTokenLen — токен из окружения короче этого не принимается: он и есть
// весь пароль к локальному аккаунту.
const minTokenLen = 32

// ownerSessionTTL — срок сессии владельца. Сессия не «входная»: токен живёт
// в файле/окружении, а не в чьей-то памяти.
const ownerSessionTTL = 10 * 365 * 24 * time.Hour

// selfURL — адрес, по которому процесс стучится к самому себе (обновление
// тикета, доклад внешнего плеера). Порт — из Addr, хост — свой, если он
// назван конкретно, иначе петля. Склеивать строки нельзя: при Addr вида
// «127.0.0.1:8080» получался «http://127.0.0.1127.0.0.1:8080».
func selfURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", strings.TrimPrefix(addr, ":")
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// hostPolicy — какие имена хоста считаются «своими»: петля всегда, остальное
// (адрес эмулятора 10.0.2.2, имя в домашней сети) — явно, ENGINE_ALLOWED_HOSTS.
type hostPolicy map[string]bool

func newHostPolicy(extra []string) hostPolicy {
	p := hostPolicy{}
	for _, h := range extra {
		if h = strings.ToLower(strings.Trim(strings.TrimSpace(h), "[]")); h != "" {
			p[h] = true
		}
	}
	return p
}

func (p hostPolicy) ok(h string) bool {
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" || p[h] {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// hostOf — имя хоста из Host-заголовка с портом или без.
func hostOf(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func writeJSONError(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": kind, "message": msg})
}

// loopbackGuard пускает запросы только на «свой» Host и только с «своим»
// Origin (или без него — так ходят приложение и <video>). Чужой Host — это
// DNS-rebinding, чужой Origin — страница из интернета в браузере владельца.
func loopbackGuard(hp hostPolicy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hp.ok(hostOf(r.Host)) {
				writeJSONError(w, http.StatusForbidden, "forbidden", "чужой Host")
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !hp.ok(u.Hostname()) {
					writeJSONError(w, http.StatusForbidden, "forbidden", "чужой Origin")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// localDenied — действия, которые в локальном режиме бессмысленны или
// вредны: сессия одна, пароля нет. Выход убил бы её до перезапуска.
func localDenied(method, path string) bool {
	switch method {
	case http.MethodPost:
		switch path {
		case "/api/v1/auth/logout", "/api/v1/auth/login", "/api/v1/auth/register":
			return true
		}
	case http.MethodPut:
		return path == "/api/v1/me/password"
	case http.MethodDelete:
		return path == "/api/v1/me/sessions" || strings.HasPrefix(path, "/api/v1/me/sessions/")
	}
	return false
}

func localModeGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if localDenied(r.Method, r.URL.Path) {
			writeJSONError(w, http.StatusForbidden, "forbidden", "недоступно в локальном режиме")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// connectHandler — разовый вход браузера: ключ из лога запуска → cookie
// сессии владельца. Cookie привязана к хосту, не к порту, поэтому и витрина
// на соседнем порту (vite) получает её. next — куда вернуть человека; только
// «свой» адрес, иначе открытый редирект.
func connectHandler(token string, secure bool, hp hostPolicy) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if subtle.ConstantTimeCompare([]byte(key), []byte(token)) != 1 {
			writeJSONError(w, http.StatusForbidden, "forbidden", "неверный ключ")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     httpuser.SessionCookie,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
			MaxAge:   int(ownerSessionTTL.Seconds()),
		})
		// Ключ в адресной строке: не кешировать и не светить в Referer.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if next := r.URL.Query().Get("next"); next != "" {
			if u, err := url.Parse(next); err == nil &&
				(u.Scheme == "http" || u.Scheme == "https") && hp.ok(u.Hostname()) {
				http.Redirect(w, r, u.String(), http.StatusFound)
				return
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "Подключено. Откройте витрину.\n")
	})
}

// tokenIssuer — то, что нужно от генератора токенов; crypto.TokenGen его
// и реализует.
type tokenIssuer interface {
	NewToken() (raw, hash string, err error)
	HashToken(raw string) string
}

// ownerSession возвращает токен владельца, заведя под него сессию, если её
// нет. Порядок: ENGINE_TOKEN → файл tokenPath → новый случайный (и в файл).
// Токен переживает перезапуск, поэтому сессия не плодится на каждый запуск, а
// открытая у владельца cookie не протухает. Когда выдан новый токен, прочие
// сессии гасятся: прошлые токены больше никому не нужны. Живую сессию при
// перезапуске не трогаем — на ней могут сидеть подключённые по коду
// устройства.
func ownerSession(ctx context.Context, repo auth.SessionRepo, tk tokenIssuer,
	userID kernel.UserID, envToken, tokenPath string, now time.Time) (string, error) {

	if envToken != "" && len(envToken) < minTokenLen {
		return "", fmt.Errorf("ENGINE_TOKEN короче %d знаков", minTokenLen)
	}
	raw := envToken
	if raw == "" && tokenPath != "" {
		if b, err := os.ReadFile(tokenPath); err == nil {
			raw = strings.TrimSpace(string(b))
		}
	}

	if raw != "" {
		sess, err := repo.ByTokenHash(ctx, tk.HashToken(raw))
		switch {
		case err == nil && sess.UserID == userID && sess.Valid(now, account.SpaceUser) == nil:
			return raw, nil
		case err == nil, errors.Is(err, kernel.ErrNotFound):
			if envToken != "" {
				if err == nil {
					return "", errors.New("ENGINE_TOKEN: сессия отозвана или истекла — задайте другой токен")
				}
				// Токен от приложения, сессии под него ещё нет — заводим.
				if err := createOwnerSession(ctx, repo, tk.HashToken(raw), userID, now); err != nil {
					return "", err
				}
				return raw, revokeOthers(ctx, repo, userID, tk.HashToken(raw), now)
			}
			// Токен из файла протух (отозван, истёк, база новая) — выпустим другой.
		default:
			return "", err
		}
	}

	raw, hash, err := tk.NewToken()
	if err != nil {
		return "", err
	}
	if err := createOwnerSession(ctx, repo, hash, userID, now); err != nil {
		return "", err
	}
	if tokenPath != "" {
		if err := os.WriteFile(tokenPath, []byte(raw+"\n"), 0o600); err != nil {
			return "", fmt.Errorf("файл токена: %w", err)
		}
		_ = os.Chmod(tokenPath, 0o600)
	}
	return raw, revokeOthers(ctx, repo, userID, hash, now)
}

func createOwnerSession(ctx context.Context, repo auth.SessionRepo, hash string,
	userID kernel.UserID, now time.Time) error {
	return repo.Create(ctx, account.Session{
		ID:         kernel.SessionID(hash[:16]),
		UserID:     userID,
		Space:      account.SpaceUser,
		DeviceKind: "engine",
		IssuedAt:   now,
		ExpiresAt:  now.Add(ownerSessionTTL),
	}, hash)
}

// revokeOthers гасит все живые сессии пользователя, кроме той, чей хеш
// keepHash: после выдачи нового токена старые ни к чему.
func revokeOthers(ctx context.Context, repo auth.SessionRepo, userID kernel.UserID,
	keepHash string, now time.Time) error {
	list, err := repo.ListForUser(ctx, userID)
	if err != nil {
		return err
	}
	keep := kernel.SessionID(keepHash[:16])
	for _, s := range list {
		if s.ID != keep && s.RevokedAt == nil {
			if err := repo.Revoke(ctx, s.ID, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// watchStdin читает r до конца и зовёт onEOF: конец потока — это закрытый
// родителем пайп (приложение завершилось, в том числе убитое системой).
func watchStdin(r io.Reader, onEOF func()) {
	_, _ = io.Copy(io.Discard, r)
	onEOF()
}

// randomHex — случайная строка для токенов процесса (служебный токен).
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Без случайности токен не токен: лучше упасть, чем выдать
		// предсказуемое.
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}
