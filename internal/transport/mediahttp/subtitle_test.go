package mediahttp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/pkg/signedtoken"
)

type memTickets struct {
	mu sync.Mutex
	m  map[kernel.TicketID]playback.Ticket
}

func (s *memTickets) Get(_ context.Context, id kernel.TicketID) (playback.Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[id]
	if !ok {
		return playback.Ticket{}, kernel.ErrNotFound
	}
	return t, nil
}

func (s *memTickets) put(t playback.Ticket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[t.ID] = t
}

type oneRoute struct{ c *http.Client }

func (e oneRoute) Client(route string) (*http.Client, bool) { return e.c, route == "" }

// Субтитры отдельным файлом: шлюз тянет их по номеру из тикета, с его
// заголовками, и отдаёт как text/vtt, какой бы тип ни назвал CDN.
func TestSubtitle(t *testing.T) {
	const vtt = "WEBVTT\n\n00:01.000 --> 00:02.000\nПривет\n"
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Как vkvideo.cloud: без Origin плеера — 403.
		if r.Header.Get("Origin") != "https://player.example" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/fresh/sub_rus.vtt":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(w, vtt)
		case "/stale/sub_rus.vtt":
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cdn.Close()

	store := &memTickets{m: map[kernel.TicketID]playback.Ticket{}}
	ticket := func(id kernel.TicketID, subs ...string) playback.Ticket {
		return playback.Ticket{
			V: playback.TicketSchemaVersion, ID: id, ExpiresAt: time.Now().Add(time.Hour),
			Mode: playback.DeliveryProxy,
			Upstream: playback.Upstream{
				Protocol: playback.ProtocolHLS, URL: cdn.URL + "/master.m3u8",
				Headers:        map[string]string{"Origin": "https://player.example"},
				AllowedOrigins: []string{"127.0.0.1"},
				Subtitles:      subs,
			},
		}
	}
	store.put(ticket("ok", cdn.URL+"/fresh/sub_rus.vtt"))
	store.put(ticket("stale", cdn.URL+"/stale/sub_rus.vtt"))
	store.put(ticket("foreign", "https://evil.example/sub.vtt"))

	// core на обновлении кладёт свежую ссылку под тем же номером.
	refreshed := 0
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/tickets/stale/refresh" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		refreshed++
		store.put(ticket("stale", cdn.URL+"/fresh/sub_rus.vtt"))
	}))
	defer core.Close()

	sealer, err := signedtoken.NewSealer("0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	gw := httptest.NewServer(New(store, sealer, oneRoute{cdn.Client()},
		NewTicketRefresher(core.URL, "svc"), nil, nil, slog.New(slog.DiscardHandler)).Routes())
	defer gw.Close()

	get := func(path string) (int, string, string) {
		t.Helper()
		resp, err := http.Get(gw.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
	}

	code, ct, body := get("/stream/ok/sub/0.vtt")
	if code != 200 || !strings.HasPrefix(ct, "text/vtt") || body != vtt {
		t.Fatalf("файл: %d %q %q", code, ct, body)
	}
	if code, _, _ := get("/stream/ok/sub/1.vtt"); code != http.StatusNotFound {
		t.Fatalf("номера нет в тикете: %d", code)
	}
	if code, _, _ := get("/stream/ok/sub/x.vtt"); code != http.StatusBadRequest {
		t.Fatalf("не номер: %d", code)
	}
	if code, _, _ := get("/stream/foreign/sub/0.vtt"); code != http.StatusForbidden {
		t.Fatalf("хост вне тикета: %d", code)
	}
	if code, _, _ := get("/stream/nope/sub/0.vtt"); code != http.StatusNotFound {
		t.Fatalf("нет тикета: %d", code)
	}
	// Протухшая ссылка: тикет обновляется, и тот же номер отдаёт свежий файл.
	code, _, body = get("/stream/stale/sub/0.vtt")
	if code != 200 || body != vtt || refreshed != 1 {
		t.Fatalf("после обновления: %d %q, обновлений %d", code, body, refreshed)
	}
}
