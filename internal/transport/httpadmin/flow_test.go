package httpadmin

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	webauthnadapter "github.com/niazleushkin/kino/internal/adapter/webauthn"
	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Сквозной тест входа через настоящий маршрутизатор и настоящий auth.Service
// на подделках хранилищ. Две подряд ошибки этого потока были найдены
// человеком в браузере — обязательное поле без кода и страница кода без
// фактора; здесь они ловятся до выкатки.

type memUsers struct{ u account.User }

func (m *memUsers) ByID(context.Context, kernel.UserID) (account.User, error) { return m.u, nil }
func (m *memUsers) ByLogin(_ context.Context, l string) (account.User, error) {
	if l != m.u.Login {
		return account.User{}, kernel.ErrNotFound
	}
	return m.u, nil
}
func (m *memUsers) List(context.Context) ([]account.User, error) { return []account.User{m.u}, nil }
func (m *memUsers) Create(context.Context, account.User, account.Credential) (account.User, error) {
	return m.u, nil
}
func (m *memUsers) SetDisabled(context.Context, kernel.UserID, bool) error     { return nil }
func (m *memUsers) SetRole(context.Context, kernel.UserID, account.Role) error { return nil }
func (m *memUsers) TouchLogin(context.Context, kernel.UserID, time.Time) error { return nil }

type memCreds struct {
	users *memUsers
	hash  string
	totp  string
}

func (m *memCreds) ByUser(context.Context, kernel.UserID) (account.Credential, error) {
	return account.Credential{UserID: 1, Hash: m.hash}, nil
}
func (m *memCreds) SetPassword(_ context.Context, _ kernel.UserID, h string) error {
	m.hash = h
	return nil
}
func (m *memCreds) TOTPSecret(context.Context, kernel.UserID) (string, error) { return m.totp, nil }
func (m *memCreds) SetTOTPSecret(_ context.Context, _ kernel.UserID, s string) error {
	m.totp = s
	m.users.u.TOTPEnabled = true // как в БД: флаг выводится из секрета
	return nil
}
func (m *memCreds) ClearTOTPSecret(context.Context, kernel.UserID) error {
	m.totp = ""
	m.users.u.TOTPEnabled = false
	return nil
}

type memInvites struct{}

func (memInvites) ByCode(context.Context, string) (account.Invite, error) {
	return account.Invite{}, kernel.ErrNotFound
}
func (memInvites) Create(context.Context, account.Invite) error                     { return nil }
func (memInvites) MarkUsed(context.Context, string, kernel.UserID, time.Time) error { return nil }
func (memInvites) ListOpen(context.Context) ([]account.Invite, error)               { return nil, nil }
func (memInvites) Revoke(context.Context, string, time.Time) error                  { return nil }
func (memInvites) ListRecent(context.Context, int) ([]account.Invite, error)        { return nil, nil }

type memSessions struct{ byHash map[string]*account.Session }

func (m *memSessions) Create(_ context.Context, s account.Session, hash string) error {
	m.byHash[hash] = &s
	return nil
}
func (m *memSessions) ByTokenHash(_ context.Context, hash string) (account.Session, error) {
	if s, ok := m.byHash[hash]; ok {
		return *s, nil
	}
	return account.Session{}, kernel.ErrNotFound
}
func (m *memSessions) Revoke(_ context.Context, id kernel.SessionID, at time.Time) error {
	for _, s := range m.byHash {
		if s.ID == id {
			s.RevokedAt = &at
		}
	}
	return nil
}
func (m *memSessions) RevokeAllForUser(_ context.Context, u kernel.UserID, at time.Time) error {
	for _, s := range m.byHash {
		if s.UserID == u {
			s.RevokedAt = &at
		}
	}
	return nil
}
func (m *memSessions) ListForUser(context.Context, kernel.UserID) ([]account.Session, error) {
	return nil, nil
}

type memPasskeys struct{ keys []account.Passkey }

func (m *memPasskeys) Create(_ context.Context, p account.Passkey) (account.Passkey, error) {
	p.ID = int64(len(m.keys) + 1)
	m.keys = append(m.keys, p)
	return p, nil
}
func (m *memPasskeys) ByCredentialID(_ context.Context, id []byte) (account.Passkey, error) {
	for _, k := range m.keys {
		if string(k.CredentialID) == string(id) {
			return k, nil
		}
	}
	return account.Passkey{}, kernel.ErrNotFound
}
func (m *memPasskeys) ListForUser(context.Context, kernel.UserID) ([]account.Passkey, error) {
	return m.keys, nil
}
func (m *memPasskeys) Touch(context.Context, int64, uint32, time.Time) error { return nil }
func (m *memPasskeys) Delete(context.Context, int64, kernel.UserID) error    { return nil }

type plainHasher struct{}

func (plainHasher) Hash(p string) (string, error) { return "h:" + p, nil }
func (plainHasher) Verify(p, h string) bool       { return h != "" && h == "h:"+p }

type stubTokens struct{ n int }

func (t *stubTokens) NewToken() (string, string, error) {
	t.n++
	raw := fmt.Sprintf("tok%d", t.n)
	return raw, t.HashToken(raw), nil
}
func (stubTokens) HashToken(raw string) string { return "hash-" + raw + "-000000000000000000" }
func (stubTokens) NewInviteCode() string       { return "ABCDE-FGHJK" }
func (stubTokens) NewPassword() string         { return "abcd-efgh-jkmn-pqrs" }

type stubTOTP struct{}

func (stubTOTP) Validate(code, secret string) bool { return code == "123456" && secret == "SECRET" }
func (stubTOTP) Provision(login string) (string, string, error) {
	return "SECRET", "otpauth://totp/kino:" + login + "?secret=SECRET&issuer=kino", nil
}

type nopAudit struct{}

func (nopAudit) Record(context.Context, kernel.UserID, string, string, string, any, any) error {
	return nil
}

type sysClock struct{}

func (sysClock) Now() time.Time { return time.Now() }

func newFlowServer(t *testing.T) (*httptest.Server, *memCreds) {
	t.Helper()
	users := &memUsers{u: account.User{ID: 1, Login: "niaz", DisplayName: "Нияз", Role: account.RoleAdmin}}
	creds := &memCreds{users: users, hash: "h:пароль-настоящий"}
	svc := auth.NewService(users, creds, memInvites{}, &memSessions{byHash: map[string]*account.Session{}},
		&memPasskeys{}, plainHasher{}, &stubTokens{}, stubTOTP{}, nopAudit{}, sysClock{})
	engine, err := webauthnadapter.New("localhost", "kino", []string{"http://localhost"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(svc, nil, nil, nil, nil, engine, "", slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	t.Cleanup(ts.Close)
	return ts, creds
}

type browser struct {
	t  *testing.T
	c  *http.Client
	ts *httptest.Server
}

func newBrowser(t *testing.T, ts *httptest.Server) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, ts: ts, c: &http.Client{Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *browser) do(req *http.Request) (*http.Response, string) {
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(body)
}

func (b *browser) get(path string) (*http.Response, string) {
	req, _ := http.NewRequest("GET", b.ts.URL+path, nil)
	return b.do(req)
}

func (b *browser) post(path string, form url.Values) (*http.Response, string) {
	req, _ := http.NewRequest("POST", b.ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", b.ts.URL)
	return b.do(req)
}

func (b *browser) postJSON(path string) (*http.Response, string) {
	req, _ := http.NewRequest("POST", b.ts.URL+path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", b.ts.URL)
	return b.do(req)
}

func (b *browser) sessionCookie() string {
	u, _ := url.Parse(b.ts.URL + Prefix)
	for _, c := range b.c.Jar.Cookies(u) {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	return ""
}

func expect(t *testing.T, res *http.Response, code int, location string) {
	t.Helper()
	if res.StatusCode != code {
		t.Fatalf("%s: код %d, ждали %d", res.Request.URL.Path, res.StatusCode, code)
	}
	if location != "" && !strings.HasPrefix(res.Header.Get("Location"), location) {
		t.Fatalf("%s: редирект на %q, ждали %q", res.Request.URL.Path, res.Header.Get("Location"), location)
	}
}

func TestFirstLoginEnrollsFactorAndElevates(t *testing.T) {
	ts, creds := newFlowServer(t)
	b := newBrowser(t, ts)

	// Без сессии всё уводит на вход.
	res, _ := b.get(Prefix + "/")
	expect(t, res, http.StatusFound, Prefix+"/login")

	// Неверный пароль — отказ без сессии.
	res, _ = b.post(Prefix+"/login", url.Values{"login": {"niaz"}, "password": {"чужой"}})
	expect(t, res, http.StatusUnauthorized, "")
	if b.sessionCookie() != "" {
		t.Fatal("сессия выдана по неверному паролю")
	}

	// Верный пароль — частичная сессия и второй шаг.
	res, _ = b.post(Prefix+"/login", url.Values{"login": {"niaz"}, "password": {"пароль-настоящий"}})
	expect(t, res, http.StatusFound, Prefix+"/factor")
	partial := b.sessionCookie()
	if partial == "" {
		t.Fatal("после пароля нет cookie сессии")
	}

	// Частичная сессия не пускает внутрь.
	res, _ = b.get(Prefix + "/users")
	expect(t, res, http.StatusFound, Prefix+"/factor?next=")

	// Фактора нет — страница кода сама ведёт на настройку, а не требует
	// код, которого не существует.
	res, _ = b.get(Prefix + "/factor")
	expect(t, res, http.StatusFound, Prefix+"/enroll")

	res, body := b.get(Prefix + "/enroll")
	expect(t, res, http.StatusOK, "")
	for _, want := range []string{`data-passkey="register"`, "otpauth://totp/kino:niaz", `action="/admin/enroll"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("на странице настройки нет %q", want)
		}
	}

	// Регистрацию passkey можно начать из частичной сессии без фактора.
	res, body = b.postJSON(Prefix + "/passkeys/begin")
	expect(t, res, http.StatusOK, "")
	if !strings.Contains(body, `"rp"`) || !strings.Contains(body, `"localhost"`) {
		t.Fatalf("begin регистрации не вернул параметры WebAuthn: %.200s", body)
	}

	// Неверный код — тот же экран с ошибкой, секрет не сохранён.
	res, body = b.post(Prefix+"/enroll", url.Values{"code": {"000000"}})
	expect(t, res, http.StatusUnauthorized, "")
	if !strings.Contains(body, "Код не подошёл") || creds.totp != "" {
		t.Fatal("неверный код должен вернуть на настройку без сохранения")
	}

	// Верный код — секрет сохранён, сессия повышена и ротирована.
	res, _ = b.post(Prefix+"/enroll", url.Values{"code": {"123456"}})
	expect(t, res, http.StatusFound, Prefix+"/?m=enrolled")
	if creds.totp != "SECRET" {
		t.Fatal("секрет не сохранён после верного кода")
	}
	if b.sessionCookie() == partial {
		t.Fatal("повышение не сменило токен сессии")
	}
	res, _ = b.get(Prefix + "/factor")
	expect(t, res, http.StatusFound, Prefix+"/")

	// Теперь TOTP настроен: паролем его заново не настроить, а страница
	// кода показывает форму кода.
	b2 := newBrowser(t, ts)
	res, _ = b2.post(Prefix+"/login", url.Values{"login": {"niaz"}, "password": {"пароль-настоящий"}})
	expect(t, res, http.StatusFound, Prefix+"/factor")
	res, body = b2.get(Prefix + "/factor")
	expect(t, res, http.StatusOK, "")
	if !strings.Contains(body, `name="code"`) {
		t.Fatal("страница кода без формы кода")
	}
	res, _ = b2.get(Prefix + "/enroll")
	expect(t, res, http.StatusFound, Prefix+"/factor")
	res, _ = b2.postJSON(Prefix + "/passkeys/begin")
	expect(t, res, http.StatusConflict, "")

	// Чужой Origin на изменяющий запрос — отказ.
	req, _ := http.NewRequest("POST", ts.URL+Prefix+"/factor", strings.NewReader("code=123456"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	res, _ = b2.do(req)
	expect(t, res, http.StatusForbidden, "")
}
