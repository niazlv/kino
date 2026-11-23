package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/bootstrap/providers"
)

// fakeLogin — поддельный вход площадки: что ответить на очередной опрос.
type fakeLogin struct {
	ready    bool
	startErr error
	dc       appprov.DeviceCode
	polls    []error // очередной опрос берёт следующий; nil — вход выполнен
	session  bool
	started  int
	polled   int
}

func (f *fakeLogin) Site() string     { return "site.example" }
func (f *fakeLogin) Ready() bool      { return f.ready }
func (f *fakeLogin) HasSession() bool { return f.session }
func (f *fakeLogin) Logout() error    { f.session = false; return nil }

func (f *fakeLogin) Start(context.Context) (appprov.DeviceCode, error) {
	f.started++
	return f.dc, f.startErr
}

func (f *fakeLogin) Poll(context.Context, string) error {
	i := f.polled
	f.polled++
	if i >= len(f.polls) {
		i = len(f.polls) - 1
	}
	if f.polls[i] != nil {
		return f.polls[i]
	}
	f.session = true
	return nil
}

type loginEnv struct {
	l *siteLogin
	f *fakeLogin
	t time.Time
}

func newLoginEnv(t *testing.T, polls ...error) *loginEnv {
	t.Helper()
	e := &loginEnv{t: time.Unix(100_000, 0)}
	e.f = &fakeLogin{
		ready: true,
		dc: appprov.DeviceCode{
			Code: "secret-code", UserCode: "ABCD-1234", VerificationURI: "https://site.example/device",
			VerificationURIComplete: "https://site.example/device?code=ABCD-1234",
			Interval:                5 * time.Second, ExpiresAt: e.t.Add(10 * time.Minute),
		},
		polls: polls,
	}
	e.l = newSiteLogin("site", e.f, quietLog())
	e.l.now = func() time.Time { return e.t }
	return e
}

func (e *loginEnv) advance(d time.Duration) { e.t = e.t.Add(d) }

func TestLoginNeedsSecret(t *testing.T) {
	e := newLoginEnv(t)
	e.f.ready = false
	if e.l.canLogin() {
		t.Error("без секрета вход «возможен»")
	}
	if _, err := e.l.begin(context.Background()); !errors.Is(err, appprov.ErrLoginNotConfigured) {
		t.Errorf("begin без секрета: %v", err)
	}
	if e.f.started != 0 {
		t.Error("без секрета к авторизации всё равно пошли")
	}
	// nil-вход (у площадки входа нет вовсе) безопасен для статуса.
	var none *siteLogin
	if none.canLogin() || none.hasSession() {
		t.Error("nil-вход что-то умеет")
	}
}

func TestLoginHappyPath(t *testing.T) {
	e := newLoginEnv(t, appprov.ErrLoginPending, appprov.ErrLoginPending, nil)
	ctx := context.Background()
	if st := e.l.poll(ctx); st.State != loginNone {
		t.Fatalf("до начала: %+v", st)
	}
	v, err := e.l.begin(ctx)
	if err != nil || v.UserCode != "ABCD-1234" || v.VerificationURIComplete == "" || v.IntervalSec != 5 {
		t.Fatalf("begin: %+v %v", v, err)
	}
	// Секретный код опроса приложению не отдаётся.
	if b, _ := json.Marshal(v); strings.Contains(string(b), "secret-code") {
		t.Fatal("код опроса утёк в ответ приложению")
	}
	for i := 0; i < 2; i++ {
		e.advance(6 * time.Second)
		if st := e.l.poll(ctx); st.State != loginPending {
			t.Fatalf("опрос %d: %+v", i, st)
		}
	}
	e.advance(6 * time.Second)
	if st := e.l.poll(ctx); st.State != loginOK {
		t.Fatalf("после подтверждения: %+v", st)
	}
	if !e.l.hasSession() {
		t.Fatal("вход выполнен, а сессии нет")
	}
	// Вход закончен: следующий опрос — «нет входа», авторизацию не трогаем.
	n := e.f.polled
	if st := e.l.poll(ctx); st.State != loginNone || e.f.polled != n {
		t.Errorf("после входа: %+v, опросов %d→%d", st, n, e.f.polled)
	}
}

// Приложение вправе спрашивать движок хоть каждую секунду — площадка при этом
// не должен получать запросов чаще, чем сам разрешил.
func TestLoginPollsUpstreamNoMoreOftenThanInterval(t *testing.T) {
	e := newLoginEnv(t, appprov.ErrLoginPending)
	ctx := context.Background()
	_, _ = e.l.begin(ctx)
	for i := 0; i < 20; i++ { // 20 секунд опросов раз в секунду
		e.advance(time.Second)
		if st := e.l.poll(ctx); st.State != loginPending {
			t.Fatalf("%+v", st)
		}
	}
	// Интервал 5 с: за 20 с — не больше 4 запросов.
	if e.f.polled > 4 || e.f.polled < 3 {
		t.Errorf("к авторизации ушло %d запросов за 20 с при интервале 5 с", e.f.polled)
	}
}

func TestLoginSlowDownWidensInterval(t *testing.T) {
	e := newLoginEnv(t, appprov.ErrLoginSlowDown, appprov.ErrLoginPending)
	ctx := context.Background()
	_, _ = e.l.begin(ctx)
	e.advance(6 * time.Second)
	_ = e.l.poll(ctx) // slow_down: интервал 5 → 10 с
	e.advance(6 * time.Second)
	_ = e.l.poll(ctx) // рано (6 < 10) — к авторизации не идём
	if e.f.polled != 1 {
		t.Fatalf("после slow_down ушло %d запросов, ждали 1", e.f.polled)
	}
	e.advance(5 * time.Second)
	_ = e.l.poll(ctx)
	if e.f.polled != 2 {
		t.Errorf("через 11 с запрос должен уйти: %d", e.f.polled)
	}
}

func TestLoginTerminalStates(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"отклонил", appprov.ErrLoginDenied, loginDenied},
		{"код истёк у авторизации", appprov.ErrLoginExpired, loginExpired},
		{"секрет неверный", appprov.ErrLoginRejected, loginError},
		{"сессия не сохранилась", appprov.ErrLoginSave, loginError},
	} {
		e := newLoginEnv(t, c.err)
		_, _ = e.l.begin(ctx)
		e.advance(6 * time.Second)
		st := e.l.poll(ctx)
		if st.State != c.want || st.Message == "" {
			t.Errorf("%s: %+v", c.name, st)
		}
		if e.l.hasSession() {
			t.Errorf("%s: сессия записалась", c.name)
		}
		if again := e.l.poll(ctx); again.State != loginNone {
			t.Errorf("%s: после конца вход не сброшен: %+v", c.name, again)
		}
	}
	// Код истёк по часам, без запроса к авторизации.
	e := newLoginEnv(t, appprov.ErrLoginPending)
	_, _ = e.l.begin(ctx)
	e.advance(11 * time.Minute)
	if st := e.l.poll(ctx); st.State != loginExpired || e.f.polled != 0 {
		t.Errorf("истёк по часам: %+v, опросов %d", st, e.f.polled)
	}
}

func TestLoginSurvivesBlipsButNotForever(t *testing.T) {
	ctx := context.Background()
	net := errors.New("connection reset")
	// Один сбой связи — не конец: код жив, пробуем дальше и входим.
	e := newLoginEnv(t, net, nil)
	_, _ = e.l.begin(ctx)
	e.advance(6 * time.Second)
	if st := e.l.poll(ctx); st.State != loginPending {
		t.Fatalf("сбой связи: %+v", st)
	}
	e.advance(6 * time.Second)
	if st := e.l.poll(ctx); st.State != loginOK {
		t.Fatalf("после сбоя: %+v", st)
	}
	// Авторизация лежит насовсем — через несколько сбоев подряд сдаёмся с причиной.
	e = newLoginEnv(t, net)
	_, _ = e.l.begin(ctx)
	var st loginState
	for i := 0; i < loginMaxFailures; i++ {
		e.advance(6 * time.Second)
		st = e.l.poll(ctx)
	}
	if st.State != loginError || !strings.Contains(st.Message, "connection reset") {
		t.Errorf("после серии сбоев: %+v", st)
	}
}

func TestLoginRestartReplacesCodeAndLogoutClears(t *testing.T) {
	e := newLoginEnv(t, appprov.ErrLoginPending)
	ctx := context.Background()
	_, _ = e.l.begin(ctx)
	_, _ = e.l.begin(ctx) // «Ещё раз»: прежний код забывается
	if e.f.started != 2 {
		t.Fatalf("кодов запрошено %d", e.f.started)
	}
	// Выход стирает и сессию, и начатый вход.
	e.f.session = true
	if err := e.l.logout(); err != nil || e.l.hasSession() {
		t.Fatalf("выход: %v, сессия %v", err, e.l.hasSession())
	}
	if st := e.l.poll(ctx); st.State != loginNone {
		t.Errorf("после выхода: %+v", st)
	}
}

// ---- ручки ----

func TestLoginRoutes(t *testing.T) {
	e := newLoginEnv(t, appprov.ErrLoginPending)
	mux := http.NewServeMux()
	loginRoutes(mux, fakeAuth{ok: "tok"}, []*siteLogin{e.l})

	do := func(method, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/engine/site/login", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "kino_session", Value: cookie})
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	// Вход — только владельцу: без токена и с чужим — 401 на все три метода.
	for _, m := range []string{"POST", "GET", "DELETE"} {
		for _, c := range []string{"", "чужой"} {
			if r := do(m, c); r.Code != 401 {
				t.Errorf("%s с токеном %q: %d", m, c, r.Code)
			}
		}
	}
	if e.f.started != 0 {
		t.Fatal("без токена вход всё равно начался")
	}
	r := do("POST", "tok")
	var v loginView
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &v) != nil || v.UserCode != "ABCD-1234" {
		t.Fatalf("POST: %d %s", r.Code, r.Body.String())
	}
	if r := do("GET", "tok"); r.Code != 200 || !strings.Contains(r.Body.String(), `"pending"`) {
		t.Errorf("GET: %d %s", r.Code, r.Body.String())
	}
	if r := do("DELETE", "tok"); r.Code != 204 {
		t.Errorf("DELETE: %d", r.Code)
	}

	// Нет секрета — 409 с понятным кодом, а не 500.
	none := newSiteLogin("site", &fakeLogin{}, quietLog())
	mux2 := http.NewServeMux()
	loginRoutes(mux2, fakeAuth{ok: "tok"}, []*siteLogin{none})
	req := httptest.NewRequest("POST", "/engine/site/login", nil)
	req.AddCookie(&http.Cookie{Name: "kino_session", Value: "tok"})
	rec := httptest.NewRecorder()
	mux2.ServeHTTP(rec, req)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "no_secret") {
		t.Errorf("без секрета: %d %s", rec.Code, rec.Body.String())
	}
}

// Причина «нет сессии» у площадки со входом в статусе — то, что человеку
// делать, а не строка из хранилища.
func TestStatusLoginReasonSaysWhatToDo(t *testing.T) {
	e := newLoginEnv(t)
	issues := []providers.Issue{{ID: "site", Reason: "провайдер site: site/session: no stored credentials"}}
	do := func() (statusDTO, map[string]json.RawMessage) {
		h := statusHandler(fakeAuth{ok: "tok"}, appprov.NewRegistry(), []string{"site"}, issues, nil, time.Now(),
			newEgressCounter(quietLog()), []*siteLogin{e.l}, nil)
		req := httptest.NewRequest("GET", "/engine/status", nil)
		req.AddCookie(&http.Cookie{Name: "kino_session", Value: "tok"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var d statusDTO
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		return d, raw
	}
	d, raw := do()
	if d.Providers[0].Reason != "нужен вход или ключ" || d.Logins["site"].Session || !d.Logins["site"].CanLogin {
		t.Fatalf("без сессии: %+v %+v", d.Providers, d.Logins)
	}
	// Прежняя форма для приложения: вход площадки — ещё и ключом с её id.
	var compat loginStatus
	if err := json.Unmarshal(raw["site"], &compat); err != nil || compat != d.Logins["site"] {
		t.Fatalf("прежняя форма: %s (%v)", raw["site"], err)
	}
	// Сессия есть, а площадка всё равно не поднялась — причина настоящая, не «войдите».
	e.f.session = true
	d, _ = do()
	if !d.Logins["site"].Session || strings.Contains(d.Providers[0].Reason, "нужен вход") {
		t.Fatalf("с сессией: %+v %+v", d.Providers, d.Logins)
	}
}
