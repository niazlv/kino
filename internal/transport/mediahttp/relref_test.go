package mediahttp

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/pkg/signedtoken"
)

// tokenCDN — CDN в духе Alloha: длинный токен в ПУТИ, плейлисты и сегменты
// лежат рядом с мастером относительными адресами. Принимает только текущий
// токен, остальные — 403, как протухшая подпись.
type tokenCDN struct {
	srv   *httptest.Server
	mu    sync.Mutex
	token string
	segs  atomic.Int32
}

func newTokenCDN(t *testing.T) *tokenCDN {
	t.Helper()
	c := &tokenCDN{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		tok := c.token
		c.mu.Unlock()
		rest, ok := strings.CutPrefix(r.URL.Path, "/1/"+tok+"/")
		if !ok {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch rest {
		case "master.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080\nindex-v1-a1.m3u8\n")
		case "index-v1-a1.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-MAP:URI=\"init-v1-a1.mp4\"\n"+
				"#EXTINF:6,\nseg-1-v1-a1.m4s\n#EXTINF:6,\nseg-2-v1-a1.m4s\n#EXT-X-ENDLIST\n")
		case "init-v1-a1.mp4", "seg-1-v1-a1.m4s", "seg-2-v1-a1.m4s":
			c.segs.Add(1)
			_, _ = io.WriteString(w, "bytes:"+rest)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *tokenCDN) rotate(tok string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = tok
	return c.srv.URL + "/1/" + tok + "/master.m3u8"
}

// longToken — токен размером с настоящий у Alloha (~4 КБ).
func longToken(seed string) string {
	return strings.Repeat(seed+"NAqVHKCjhA93NMt2JMV-XkT8Pa72LufGdaz4", 4200/(len(seed)+36))
}

type relRig struct {
	cdn       *tokenCDN
	gw        *httptest.Server
	store     *memTickets
	refreshes atomic.Int32
}

func newRelRig(t *testing.T) *relRig {
	t.Helper()
	rig := &relRig{cdn: newTokenCDN(t), store: &memTickets{m: map[kernel.TicketID]playback.Ticket{}}}
	ticket := func(master string) playback.Ticket {
		return playback.Ticket{
			V: playback.TicketSchemaVersion, ID: "tkt_rel", ExpiresAt: time.Now().Add(time.Hour),
			Refreshable: true, Mode: playback.DeliveryProxy,
			Upstream: playback.Upstream{
				Protocol: playback.ProtocolHLS, URL: master,
				AllowedOrigins: []string{"127.0.0.1"},
			},
		}
	}
	rig.store.put(ticket(rig.cdn.rotate(longToken("a"))))

	// core на обновление выдаёт ссылку с новым токеном — как площадка,
	// у которой подпись протухла.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/tickets/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		rig.refreshes.Add(1)
		rig.store.put(ticket(rig.cdn.rotate(longToken("b"))))
	})
	core := httptest.NewServer(mux)
	t.Cleanup(core.Close)

	sealer, err := signedtoken.NewSealer("0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	rig.gw = httptest.NewServer(New(rig.store, sealer, oneRoute{rig.cdn.srv.Client()},
		NewTicketRefresher(core.URL, "svc"), nil, nil, slog.New(slog.DiscardHandler)).Routes())
	t.Cleanup(rig.gw.Close)
	return rig
}

func (rig *relRig) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(rig.gw.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// refs — адреса из переписанного плейлиста: строки ресурсов и URI в тегах.
func refs(playlist string) []string {
	var out []string
	for _, l := range strings.Split(playlist, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if !strings.HasPrefix(l, "#") {
			out = append(out, l)
		} else if _, after, ok := strings.Cut(l, `URI="`); ok {
			out = append(out, after[:strings.IndexByte(after, '"')])
		}
	}
	return out
}

// «Во все тяжкие» 1×2 с ЛордФильма через внешний адрес: путь сегмента
// выходил ~8,5 КБ, nginx перед сайтом отвечал 414. Адреса внутри каталога
// мастера должны запечатываться коротким хвостом и при этом играть.
func TestLongUpstreamTokenGivesShortPaths(t *testing.T) {
	rig := newRelRig(t)
	code, master := rig.get(t, "/stream/tkt_rel/master.m3u8")
	if code != http.StatusOK {
		t.Fatalf("мастер: %d", code)
	}
	variants := refs(master)
	if len(variants) != 1 {
		t.Fatalf("варианты: %q", master)
	}
	code, media := rig.get(t, variants[0])
	if code != http.StatusOK {
		t.Fatalf("вариант: %d", code)
	}
	all := append(variants, refs(media)...)
	if len(all) != 4 {
		t.Fatalf("ресурсы: %q", media)
	}
	for _, p := range all {
		// Было ~5,7 КБ на этом токене; nginx по умолчанию держит 8 КБ на
		// всю строку запроса вместе с заголовками.
		if len(p) > 200 {
			t.Fatalf("путь %d байт: %.80s…", len(p), p)
		}
	}
	for _, p := range all[1:] {
		code, body := rig.get(t, p)
		if code != http.StatusOK || !strings.HasPrefix(body, "bytes:") {
			t.Fatalf("%s: %d %q", p, code, body)
		}
	}
}

// Подпись площадки протухла посреди серии: сегмент из старого плейлиста
// разворачивается от обновлённого тикета и идёт уже по новому токену — без
// перечитывания плейлиста и без ошибки у плеера.
func TestRelativeRefFollowsRefreshedTicket(t *testing.T) {
	rig := newRelRig(t)
	_, master := rig.get(t, "/stream/tkt_rel/master.m3u8")
	_, media := rig.get(t, refs(master)[0])
	seg := refs(media)[1]

	// CDN сменил токен — старые адреса отвечают 403.
	rig.cdn.rotate(longToken("c"))
	code, body := rig.get(t, seg)
	if code != http.StatusOK || body != "bytes:seg-1-v1-a1.m4s" {
		t.Fatalf("после смены подписи: %d %q", code, body)
	}
	if n := rig.refreshes.Load(); n != 1 {
		t.Fatalf("обновлений %d", n)
	}
}

// Ссылки, выданные до хвостов (абсолютный адрес внутри), продолжают
// открываться: выкатка шлюза не рвёт идущие просмотры.
func TestAbsoluteSealedRefStillServed(t *testing.T) {
	rig := newRelRig(t)
	tk, _ := rig.store.Get(t.Context(), "tkt_rel")
	abs := strings.TrimSuffix(tk.Upstream.URL, "master.m3u8") + "seg-2-v1-a1.m4s"
	sealer, _ := signedtoken.NewSealer("0123456789abcdef0123")
	code, body := rig.get(t, "/stream/tkt_rel/r/"+sealer.Seal(abs, "tkt_rel"))
	if code != http.StatusOK || body != "bytes:seg-2-v1-a1.m4s" {
		t.Fatalf("%d %q", code, body)
	}
}

func TestRelRef(t *testing.T) {
	tk := playback.Ticket{Upstream: playback.Upstream{URL: "https://cdn.example/a/b/master.m3u8?sig=1"}}
	for _, c := range []struct{ abs, want string }{
		{"https://cdn.example/a/b/seg-1.ts", "~seg-1.ts"},
		{"https://cdn.example/a/b/v/idx.m3u8?x=2", "~v/idx.m3u8?x=2"},
		{"https://cdn.example/a/seg-1.ts", "https://cdn.example/a/seg-1.ts"},
		{"https://other.example/a/b/seg-1.ts", "https://other.example/a/b/seg-1.ts"},
		// Хост-префикс без границы пути не должен совпасть.
		{"https://cdn.example.evil/a/b/x", "https://cdn.example.evil/a/b/x"},
	} {
		got := relRef(tk, c.abs)
		if got != c.want {
			t.Errorf("relRef(%q) = %q, ждали %q", c.abs, got, c.want)
		}
		if back, ok := resolveRef(tk, got); !ok || back != c.abs {
			t.Errorf("resolveRef(%q) = %q %v", got, back, ok)
		}
	}
	// Адрес тикета без каталога — хвост не разворачивается.
	if _, ok := resolveRef(playback.Ticket{Upstream: playback.Upstream{URL: "https://cdn.example"}}, "~x"); ok {
		t.Error("хвост развернулся без каталога")
	}
}
