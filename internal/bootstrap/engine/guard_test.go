package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/crypto"
	"github.com/niazleushkin/kino/internal/adapter/sqlite"
	"github.com/niazleushkin/kino/internal/app/auth"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/bootstrap/providers"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dprovider "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/transport/httpuser"
)

// Регрессия: при Addr «127.0.0.1:8080» адрес для самого себя склеивался в
// «http://127.0.0.1127.0.0.1:8080», и обновление тикета уходило в никуда.
func TestSelfURL(t *testing.T) {
	for addr, want := range map[string]string{
		":8080":          "http://127.0.0.1:8080",
		"127.0.0.1:8080": "http://127.0.0.1:8080",
		"0.0.0.0:9000":   "http://127.0.0.1:9000",
		"[::]:9000":      "http://127.0.0.1:9000",
		"[::1]:8080":     "http://[::1]:8080",
		"localhost:8091": "http://localhost:8091",
		"192.168.1.5:80": "http://192.168.1.5:80",
		"8080":           "http://127.0.0.1:8080",
	} {
		if got := selfURL(addr); got != want {
			t.Errorf("selfURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

// Не только строка, но и достижимость: адрес, который listener даёт сам
// («127.0.0.1:порт»), через selfURL должен отвечать.
func TestSelfURLReachable(t *testing.T) {
	srv := httptest.NewServer(okHandler())
	defer srv.Close()
	resp, err := http.Get(selfURL(srv.Listener.Addr().String()) + "/internal/tickets/x/refresh")
	if err != nil {
		t.Fatalf("self-адрес недостижим: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

func serve(h http.Handler, method, target, host, origin string) int {
	req := httptest.NewRequest(method, target, nil)
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestLoopbackGuard(t *testing.T) {
	h := loopbackGuard(newHostPolicy([]string{"10.0.2.2"}))(okHandler())
	cases := []struct {
		name         string
		host, origin string
		want         int
	}{
		{"петля без Origin (приложение, <video>)", "127.0.0.1:8080", "", 204},
		{"localhost", "localhost:8080", "", 204},
		{"ipv6-петля", "[::1]:8080", "", 204},
		{"витрина на соседнем порту", "127.0.0.1:8080", "http://127.0.0.1:5174", 204},
		{"витрина по localhost", "127.0.0.1:8080", "http://localhost:5174", 204},
		{"свой Origin на разрешённом хосте", "10.0.2.2:8080", "http://10.0.2.2:5174", 204},
		{"DNS-rebinding: чужое имя на 127.0.0.1", "evil.example:8080", "", 403},
		{"страница из интернета", "127.0.0.1:8080", "https://evil.example", 403},
		{"Origin: null (sandbox-iframe, file://)", "127.0.0.1:8080", "null", 403},
		{"адрес из LAN как Origin (vite по LAN-IP)", "127.0.0.1:8080", "http://10.128.37.222:5173", 403},
		{"Host из LAN", "10.128.37.222:8080", "", 403},
		{"мусор в Origin", "127.0.0.1:8080", "ht!tp://", 403},
	}
	for _, c := range cases {
		if got := serve(h, "POST", "/api/v1/auth/logout", c.host, c.origin); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

func TestLocalModeGuard(t *testing.T) {
	h := localModeGuard(okHandler())
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/api/v1/auth/logout", 403},
		{"POST", "/api/v1/auth/login", 403},
		{"POST", "/api/v1/auth/register", 403},
		{"PUT", "/api/v1/me/password", 403},
		{"DELETE", "/api/v1/me/sessions", 403},
		{"DELETE", "/api/v1/me/sessions/abc", 403},
		{"GET", "/api/v1/me", 204},
		{"GET", "/api/v1/me/sessions", 204}, // «Где вы вошли» читается
		{"PUT", "/api/v1/progress/11", 204},
		{"POST", "/api/v1/playback/11", 204},
		{"GET", "/api/v1/auth/logout", 204}, // не тот метод — не наш запрет
	} {
		if got := serve(h, c.method, c.path, "", ""); got != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, got, c.want)
		}
	}
}

func TestConnectHandler(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef-token"
	h := connectHandler(token, false, newHostPolicy(nil))

	get := func(q string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/engine/connect?"+q, nil))
		return rec
	}

	if rec := get("key=wrong"); rec.Code != 403 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("неверный ключ: %d, cookies=%v", rec.Code, rec.Result().Cookies())
	}
	if rec := get(""); rec.Code != 403 {
		t.Fatalf("без ключа: %d", rec.Code)
	}

	rec := get("key=" + token)
	if rec.Code != 200 {
		t.Fatalf("верный ключ: %d", rec.Code)
	}
	cs := rec.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != httpuser.SessionCookie || cs[0].Value != token ||
		!cs[0].HttpOnly || cs[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie: %+v", cs)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("ключ в URL не должен кешироваться и утекать в Referer: %v", rec.Header())
	}

	// next — только «свой» адрес: иначе открытый редирект на чужой сайт.
	rec = get("key=" + token + "&next=http://127.0.0.1:5174/")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "http://127.0.0.1:5174/" {
		t.Fatalf("redirect на свою витрину: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = get("key=" + token + "&next=https://evil.example/")
	if rec.Code != 200 || rec.Header().Get("Location") != "" {
		t.Fatalf("чужой next не должен редиректить: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// ---- ownerSession на настоящем SQLite ----

type sessionEnv struct {
	repo *sqlite.SessionRepo
	user kernel.UserID
	path string // файл токена
	tk   crypto.TokenGen
}

func newSessionEnv(t *testing.T) sessionEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "e.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	u, err := sqlite.NewUserRepo(db).Create(context.Background(),
		account.User{Login: "engine", DisplayName: "Владелец", Role: account.RoleUser, CreatedAt: time.Now()},
		account.Credential{Hash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	return sessionEnv{repo: sqlite.NewSessionRepo(db), user: u.ID, path: filepath.Join(dir, "e.db.token"), tk: crypto.NewTokenGen()}
}

func (e sessionEnv) active(t *testing.T) int {
	t.Helper()
	l, err := e.repo.ListForUser(context.Background(), e.user)
	if err != nil {
		t.Fatal(err)
	}
	return len(l)
}

func (e sessionEnv) owner(t *testing.T, envToken string) (string, error) {
	t.Helper()
	return ownerSession(context.Background(), e.repo, e.tk, e.user, envToken, e.path, time.Now())
}

func TestOwnerSessionTokenSurvivesRestart(t *testing.T) {
	e := newSessionEnv(t)

	first, err := e.owner(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < minTokenLen {
		t.Fatalf("токен слишком короткий: %d", len(first))
	}
	st, err := os.Stat(e.path)
	if err != nil {
		t.Fatalf("файл токена не записан: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("права файла токена %v, нужно 0600", st.Mode().Perm())
	}

	// «Перезапуск»: тот же токен, сессия не плодится.
	for i := 0; i < 3; i++ {
		again, err := e.owner(t, "")
		if err != nil || again != first {
			t.Fatalf("перезапуск %d: %q err=%v, want прежний токен", i, again, err)
		}
	}
	if n := e.active(t); n != 1 {
		t.Fatalf("живых сессий %d, нужна одна", n)
	}

	// Токен настоящий: по нему сессию находит обычная аутентификация.
	s, err := e.repo.ByTokenHash(context.Background(), e.tk.HashToken(first))
	if err != nil || s.Valid(time.Now(), account.SpaceUser) != nil {
		t.Fatalf("сессия под токеном: %+v err=%v", s, err)
	}
}

func TestOwnerSessionReissuesWhenRevokedAndCleansUp(t *testing.T) {
	e := newSessionEnv(t)
	ctx := context.Background()

	old, err := e.owner(t, "")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := e.repo.ByTokenHash(ctx, e.tk.HashToken(old))
	if err := e.repo.Revoke(ctx, s.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	fresh, err := e.owner(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old {
		t.Fatal("отозванный токен возвращён как живой")
	}
	if n := e.active(t); n != 1 {
		t.Fatalf("живых сессий %d, нужна одна", n)
	}
	// Файл теперь хранит новый токен: следующий запуск берёт его.
	if again, _ := e.owner(t, ""); again != fresh {
		t.Fatalf("после перевыпуска файл не обновлён: %q", again)
	}
}

func TestOwnerSessionRevokesStrays(t *testing.T) {
	e := newSessionEnv(t)
	ctx := context.Background()

	// Сессии прежнего устройства (десятилетние, токены потеряны) гасятся,
	// когда выдаётся новый токен.
	for i := 0; i < 3; i++ {
		_, hash, _ := e.tk.NewToken()
		if err := createOwnerSession(ctx, e.repo, hash, e.user, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.owner(t, ""); err != nil {
		t.Fatal(err)
	}
	if n := e.active(t); n != 1 {
		t.Fatalf("живых сессий %d, нужна одна (чужие погашены)", n)
	}
}

func TestOwnerSessionRevokedTokenFileIsNotReused(t *testing.T) {
	e := newSessionEnv(t)
	if err := os.WriteFile(e.path, []byte("not-a-known-token-0000000000000000000000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := e.owner(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if got == "not-a-known-token-0000000000000000000000" {
		t.Fatal("токен из файла без сессии в базе принят как есть")
	}
}

func TestOwnerSessionEnvToken(t *testing.T) {
	e := newSessionEnv(t)
	ctx := context.Background()

	if _, err := e.owner(t, "short"); err == nil {
		t.Fatal("короткий ENGINE_TOKEN принят")
	}

	tok := strings.Repeat("a", 40)
	got, err := e.owner(t, tok)
	if err != nil || got != tok {
		t.Fatalf("ENGINE_TOKEN: %q err=%v", got, err)
	}
	if _, err := os.Stat(e.path); err == nil {
		t.Fatal("токен приложения не должен писаться в файл")
	}
	// Повторный запуск с тем же токеном — та же сессия.
	if got, err = e.owner(t, tok); err != nil || got != tok {
		t.Fatalf("повторный запуск: %q err=%v", got, err)
	}
	if n := e.active(t); n != 1 {
		t.Fatalf("живых сессий %d", n)
	}
	// Отозванный токен приложения не воскресает молча.
	s, _ := e.repo.ByTokenHash(ctx, e.tk.HashToken(tok))
	_ = e.repo.Revoke(ctx, s.ID, time.Now())
	if _, err := e.owner(t, tok); err == nil {
		t.Fatal("отозванный ENGINE_TOKEN принят")
	}
}

// Родитель (приложение) умер — его конец пайпа закрылся, и движок обязан
// уйти, а не висеть сиротой с занятым портом и открытой базой.
func TestWatchStdinFiresOnParentExit(t *testing.T) {
	pr, pw := io.Pipe()
	fired := make(chan struct{})
	go watchStdin(pr, func() { close(fired) })

	// Пока родитель жив и молчит — не срабатывает.
	select {
	case <-fired:
		t.Fatal("сработало до закрытия пайпа")
	case <-time.After(50 * time.Millisecond):
	}
	// Данные в stdin — не повод уходить.
	_, _ = pw.Write([]byte("мусор\n"))
	select {
	case <-fired:
		t.Fatal("сработало на данных")
	case <-time.After(50 * time.Millisecond):
	}

	_ = pw.Close()
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("не сработало после закрытия пайпа")
	}
}

// ---- /engine/status ----

type fakeAuth struct{ ok string }

func (f fakeAuth) Authenticate(_ context.Context, raw string, _ account.Space) (auth.Actor, error) {
	if raw == f.ok {
		return auth.Actor{}, nil
	}
	return auth.Actor{}, kernel.ErrUnauthorized
}

func TestProviderStates(t *testing.T) {
	got := providerStates(
		[]string{"anilibria", "kinopub", "animego_me", "nope"},
		[]providers.Issue{{ID: "kinopub", Reason: "нужен ключ"}, {ID: "nope", Reason: "площадка неизвестна"}},
		func(id string) bool { return id == "anilibria" },
	)
	want := []providerStatus{
		{ID: "anilibria", State: "ready"},
		{ID: "kinopub", State: "skipped", Reason: "нужен ключ"},
		{ID: "animego_me", State: "starting"},
		{ID: "nope", State: "skipped", Reason: "площадка неизвестна"},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestStatusNeedsOwnerToken(t *testing.T) {
	h := statusHandler(fakeAuth{ok: "tok"}, appprov.NewRegistry(), []string{"anilibria"}, nil, []string{"default"}, time.Now(), newEgressCounter(quietLog()), nil, nil)
	do := func(cookie string) int {
		req := httptest.NewRequest("GET", "/engine/status", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: httpuser.SessionCookie, Value: cookie})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do(""); c != 401 {
		t.Errorf("без токена: %d", c)
	}
	if c := do("чужой"); c != 401 {
		t.Errorf("чужой токен: %d", c)
	}
	if c := do("tok"); c != 200 {
		t.Errorf("токен владельца: %d", c)
	}
}

// ---- ступенчатый поиск и счётчик запросов ----

type fakeSearcher struct {
	name  string
	found int
	err   error
	calls *[]string
}

func (f fakeSearcher) Search(context.Context, string, int) ([]dprovider.Title, error) {
	*f.calls = append(*f.calls, f.name)
	if f.err != nil {
		return nil, f.err
	}
	return make([]dprovider.Title, f.found), nil
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestSearchStepwiseStopsWhenEnoughFound(t *testing.T) {
	var calls []string
	ss := []titleSearcher{
		fakeSearcher{"главная", 6, nil, &calls},
		fakeSearcher{"вторая", 3, nil, &calls},
		fakeSearcher{"третья", 3, nil, &calls},
	}
	out, err := searchStepwise(context.Background(), ss, "q", 10, time.Second, 5, quietLog())
	if err != nil || len(out) != 6 {
		t.Fatalf("%d находок, %v", len(out), err)
	}
	// Главная площадка нашла больше порога — остальных не беспокоим.
	if len(calls) != 1 || calls[0] != "главная" {
		t.Errorf("спрошены %v, ждали одну главную", calls)
	}
}

func TestSearchStepwiseAsksNextOnlyWhenTooFew(t *testing.T) {
	var calls []string
	ss := []titleSearcher{
		fakeSearcher{"главная", 1, nil, &calls},
		fakeSearcher{"вторая", 2, nil, &calls},
		fakeSearcher{"третья", 9, nil, &calls},
	}
	out, _ := searchStepwise(context.Background(), ss, "q", 10, time.Second, 5, quietLog())
	// 1, потом 1+2=3 (мало), потом 3+9 — хватило; порядок сохранён.
	if len(calls) != 3 || calls[0] != "главная" || calls[1] != "вторая" || calls[2] != "третья" || len(out) != 12 {
		t.Errorf("спрошены %v, находок %d", calls, len(out))
	}
}

func TestSearchStepwiseSurvivesFailingProvider(t *testing.T) {
	var calls []string
	ss := []titleSearcher{
		fakeSearcher{"упала", 0, errors.New("таймаут"), &calls},
		fakeSearcher{"живая", 2, nil, &calls},
	}
	out, err := searchStepwise(context.Background(), ss, "q", 10, time.Second, 5, quietLog())
	if err != nil || len(out) != 2 || len(calls) != 2 {
		t.Errorf("%d находок, %v, спрошены %v", len(out), err, calls)
	}
}

// Все лежат (нет сети) — это ошибка, а не «ничего не нашли»: иначе запрос
// запомнился бы как спрошенный и на дни остался без ответа.
func TestSearchStepwiseNobodyAnsweredIsError(t *testing.T) {
	var calls []string
	ss := []titleSearcher{
		fakeSearcher{"а", 0, errors.New("нет сети"), &calls},
		fakeSearcher{"б", 0, errors.New("нет сети"), &calls},
	}
	if _, err := searchStepwise(context.Background(), ss, "q", 10, time.Second, 5, quietLog()); !errors.Is(err, errNobodyAnswered) {
		t.Fatalf("ждали errNobodyAnswered, получили %v", err)
	}
	// Площадки ответили пустотой — это честное «ничего», не ошибка.
	empty := []titleSearcher{fakeSearcher{"а", 0, nil, &calls}}
	if out, err := searchStepwise(context.Background(), empty, "q", 10, time.Second, 5, quietLog()); err != nil || len(out) != 0 {
		t.Fatalf("пустота: %v %v", out, err)
	}
	// Искать некому (площадки ещё регистрируются) — тоже ошибка: иначе запрос,
	// на который не спросили никого, запомнился бы как спрошенный на неделю.
	if _, err := searchStepwise(context.Background(), nil, "q", 10, time.Second, 5, quietLog()); !errors.Is(err, errNobodyAnswered) {
		t.Fatalf("без площадок: %v", err)
	}
}

func TestSearchStepwiseMinHitsZeroAsksAll(t *testing.T) {
	var calls []string
	ss := []titleSearcher{
		fakeSearcher{"а", 50, nil, &calls},
		fakeSearcher{"б", 50, nil, &calls},
	}
	_, _ = searchStepwise(context.Background(), ss, "q", 10, time.Second, 0, quietLog())
	if len(calls) != 2 {
		t.Errorf("minHits=0 — как прежде, все: %v", calls)
	}
}

func TestEgressCounterCountsRequestsByHost(t *testing.T) {
	c := newEgressCounter(quietLog())
	rt := c.Wrap(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	}))
	for _, u := range []string{
		"https://api.anilibria.app/x?q=секрет", "https://api.anilibria.app/y", "https://animego.me/z",
	} {
		req, _ := http.NewRequest("GET", u, nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	snap := c.Snapshot()
	if snap.Total != 3 || len(snap.Hosts) != 2 || snap.Hosts[0].Host != "api.anilibria.app" || snap.Hosts[0].Count != 2 {
		t.Errorf("%+v", snap)
	}
}

func TestReadyGate(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	pending := 2
	h := func(started time.Time, grace time.Duration) int {
		rec := httptest.NewRecorder()
		readyGate(ok, started, grace, func() int { return pending }).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))
		return rec.Code
	}
	if c := h(time.Now(), time.Minute); c != 503 {
		t.Errorf("площадки регистрируются — ждали 503, получили %d", c)
	}
	pending = 0
	if c := h(time.Now(), time.Minute); c != 200 {
		t.Errorf("все зарегистрированы — ждали 200, получили %d", c)
	}
	// Площадка без сети не держит приложение вечно: через grace открываемся и так.
	pending = 3
	if c := h(time.Now().Add(-2*time.Minute), time.Minute); c != 200 {
		t.Errorf("grace вышел — ждали 200, получили %d", c)
	}
}
