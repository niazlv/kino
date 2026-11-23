package engine

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const macChrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

func TestUADefaultIsBrowserLikeAndSaysWhoWeAre(t *testing.T) {
	p := newUAPolicy("KinoPlatform-Android/0.2.14", "", nil, quietLog())
	d := p.Default()
	if !strings.HasPrefix(d, "Mozilla/5.0 (Linux; Android") || !strings.Contains(d, "Chrome/140.") {
		t.Errorf("подпись не похожа на браузер: %q", d)
	}
	if !strings.HasSuffix(d, " KinoPlatform-Android/0.2.14") {
		t.Errorf("нет пометки приложения с версией: %q", d)
	}
	if strings.Contains(d, "Macintosh") {
		t.Errorf("представляемся Маком: %q", d)
	}
}

func TestUAWithoutAppMarkerFallsBackToEngine(t *testing.T) {
	for _, app := range []string{"", "  ", "плохо\n"} {
		if d := newUAPolicy(app, "", nil, quietLog()).Default(); !strings.HasSuffix(d, " "+defaultAppUA) {
			t.Errorf("app=%q: %q", app, d)
		}
	}
}

func TestUAForRules(t *testing.T) {
	p := newUAPolicy("KinoPlatform-Android/1.0", "", map[string]string{"filmix": "MyOwn/1"}, quietLog())
	def := p.Default()
	cases := []struct{ id, cur, want string }{
		// Чужой «Chrome на Маке», зашитый в провайдере, заменяется честной подписью.
		{"anilibria", macChrome, def},
		{"anilibria", "", def},
		// Библиотечная подпись, нужная площадке, сохраняется — плюс пометку приложения.
		{"kinopub", "okhttp/4.12.0", "okhttp/4.12.0 KinoPlatform-Android/1.0"},
		// Помет не дублируется.
		{"kinopub", "okhttp/4.12.0 KinoPlatform-Android/1.0", "okhttp/4.12.0 KinoPlatform-Android/1.0"},
		// Заданное человеком — как есть, что бы ни поставил провайдер.
		{"filmix", "okhttp/4.12.0", "MyOwn/1"},
		{"filmix", macChrome, "MyOwn/1"},
	}
	for _, c := range cases {
		if got := p.For(c.id, c.cur); got != c.want {
			t.Errorf("For(%s, %q) = %q, ждали %q", c.id, c.cur, got, c.want)
		}
	}
}

func TestUAForEveryoneYieldsToPerProvider(t *testing.T) {
	p := newUAPolicy("A/1", "All/1", map[string]string{"yummy": "Own/2"}, quietLog())
	if got := p.For("anilibria", macChrome); got != "All/1" {
		t.Errorf("общая: %q", got)
	}
	if got := p.For("yummy", macChrome); got != "Own/2" {
		t.Errorf("своя площадки сильнее общей: %q", got)
	}
}

func TestUAInvalidUserValuesAreDropped(t *testing.T) {
	long := strings.Repeat("a", maxUALen+1)
	p := newUAPolicy("A/1", "плохо\r\nX: y", map[string]string{
		"anilibria":  "с\nпереводом",
		"yummy":      long,
		"jutsu_net":  "   ",
		"animego_me": "Ok/1",
	}, quietLog())
	for _, id := range []string{"anilibria", "yummy", "jutsu_net"} {
		if got := p.For(id, macChrome); got != p.Default() {
			t.Errorf("%s: негодная подпись просочилась: %q", id, got)
		}
	}
	if got := p.For("animego_me", macChrome); got != "Ok/1" {
		t.Errorf("годная подпись потеряна: %q", got)
	}
	if got := strings.Join(p.Custom(), ","); got != "animego_me" {
		t.Errorf("Custom = %q, ждали только animego_me", got)
	}
}

func TestUARejectsNonASCII(t *testing.T) {
	p := newUAPolicy("A/1", "", map[string]string{"anilibria": "Моя подпись"}, quietLog())
	if got := p.For("anilibria", macChrome); got != p.Default() {
		t.Errorf("не-ASCII просочилось в заголовок: %q", got)
	}
	if len(p.Custom()) != 0 {
		t.Errorf("Custom = %v", p.Custom())
	}
}

func TestUACustomListsEveryoneFirst(t *testing.T) {
	p := newUAPolicy("A/1", "All/1", map[string]string{"b": "B/1", "a": "A/1"}, quietLog())
	if got := strings.Join(p.Custom(), ","); got != "*,a,b" {
		t.Errorf("Custom = %q", got)
	}
	if (*uaPolicy)(nil).Custom() != nil {
		t.Error("nil-политика должна молчать")
	}
}

// uaServer запоминает User-Agent, с которым к нему пришли.
type uaServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newUAServer(t *testing.T) *uaServer {
	s := &uaServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.Header.Get("User-Agent"))
		s.mu.Unlock()
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *uaServer) last(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) == 0 {
		t.Fatal("запроса не было")
	}
	return s.seen[len(s.seen)-1]
}

func get(t *testing.T, c *http.Client, url, ua string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func TestUATransportRewritesOnTheWire(t *testing.T) {
	srv := newUAServer(t)
	p := newUAPolicy("KinoPlatform-Android/0.2.14", "", map[string]string{"yummy": "Own/2"}, quietLog())

	// Общий слой — запросы без площадки.
	def := &http.Client{Transport: p.WrapDefault(http.DefaultTransport)}
	get(t, def, srv.URL, macChrome)
	if got := srv.last(t); got != p.Default() {
		t.Errorf("общий слой: %q", got)
	}

	// Слой площадки: у anilibria — подпись по умолчанию, у yummy — заданная человеком.
	for id, want := range map[string]string{"anilibria": p.Default(), "yummy": "Own/2"} {
		c := p.WrapClient(id, def)
		get(t, c, srv.URL, macChrome)
		if got := srv.last(t); got != want {
			t.Errorf("%s: на проводе %q, ждали %q", id, got, want)
		}
	}
}

// Заданное человеком не должно переписываться общим слоем, лежащим под слоем
// площадки: вложенные слои — именно так собран клиент площадки в движке.
func TestUACustomSurvivesNestedDefaultLayer(t *testing.T) {
	srv := newUAServer(t)
	p := newUAPolicy("A/1", "", map[string]string{"anilibria": "Mozilla/5.0 Mine"}, quietLog())
	shared := &http.Client{Transport: p.WrapDefault(http.DefaultTransport)}
	get(t, p.WrapClient("anilibria", shared), srv.URL, macChrome)
	if got := srv.last(t); got != "Mozilla/5.0 Mine" {
		// «Mozilla/» в начале не должно вызывать подмену: это выбор человека.
		t.Errorf("заданная подпись переписана: %q", got)
	}
}

// Шлюз тянет сегменты клиентом без площадки, а UA в тикете уже решён на резолве:
// общий слой не вправе переписать ни заданную человеком подпись, ни нашу.
func TestUADefaultLayerKeepsTicketUserAgentFinal(t *testing.T) {
	srv := newUAServer(t)
	p := newUAPolicy("A/1", "", map[string]string{
		"animego_me": "Mozilla/5.0 (X11; Linux x86_64) Custom/9.9",
		"filmix":     "okhttp/4.12.0",
	}, quietLog())
	def := &http.Client{Transport: p.WrapDefault(http.DefaultTransport)}
	for _, ua := range []string{
		"Mozilla/5.0 (X11; Linux x86_64) Custom/9.9", // человек задал «браузерную» — не заменять
		"okhttp/4.12.0", // человек задал библиотечную — не дописывать пометку
		p.Default(),     // уже наша — как есть
	} {
		get(t, def, srv.URL, ua)
		if got := srv.last(t); got != ua {
			t.Errorf("окончательная подпись %q переписана в %q", ua, got)
		}
	}
	// А чужая подпись без нашей пометки и не заданная человеком — заменяется.
	get(t, def, srv.URL, macChrome)
	if got := srv.last(t); got != p.Default() {
		t.Errorf("чужой браузер не заменён: %q", got)
	}
}

func TestUAWrapClientLeavesSharedClientAndRequestAlone(t *testing.T) {
	srv := newUAServer(t)
	p := newUAPolicy("A/1", "", map[string]string{"anilibria": "Mine/1"}, quietLog())
	shared := &http.Client{Transport: http.DefaultTransport}
	wrapped := p.WrapClient("anilibria", shared)
	if wrapped == shared {
		t.Fatal("WrapClient вернул тот же клиент")
	}
	// Общий клиент маршрута у других площадок остаётся прежним.
	get(t, shared, srv.URL, macChrome)
	if got := srv.last(t); got != macChrome {
		t.Errorf("общий клиент задет: %q", got)
	}
	// Исходный запрос не изменяется (контракт RoundTripper).
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("User-Agent", macChrome)
	resp, err := wrapped.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := req.Header.Get("User-Agent"); got != macChrome {
		t.Errorf("исходный запрос изменён: %q", got)
	}
	if p.WrapClient("x", nil) != nil {
		t.Error("nil-клиент должен остаться nil")
	}
}

func TestStatusReportsUserAgent(t *testing.T) {
	p := newUAPolicy("KinoPlatform-Android/9.9", "", map[string]string{"anilibria": "Mine/1"}, quietLog())
	var _ = slog.Default
	st := userAgentStatus{Default: p.Default(), Custom: nonNil(p.Custom())}
	if !strings.HasSuffix(st.Default, "KinoPlatform-Android/9.9") || len(st.Custom) != 1 || st.Custom[0] != "anilibria" {
		t.Errorf("%+v", st)
	}
	if got := nonNil(nil); got == nil || len(got) != 0 {
		t.Error("nonNil(nil) должен быть пустым списком, не nil")
	}
}
