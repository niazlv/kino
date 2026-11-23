package mediahttp

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/pkg/signedtoken"
)

// refreshRig — шлюз, CDN с одним плейлистом и core, который на обновление
// кладёт свежий тикет под тем же номером.
type refreshRig struct {
	gw        *httptest.Server
	store     *memTickets
	refreshes atomic.Int32
	hold      chan struct{} // nil — core отвечает сразу
	ticket    func(id kernel.TicketID, exp time.Time, refreshable bool) playback.Ticket
}

func newRefreshRig(t *testing.T) *refreshRig {
	t.Helper()
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-ENDLIST\n")
	}))
	t.Cleanup(cdn.Close)

	rig := &refreshRig{store: &memTickets{m: map[kernel.TicketID]playback.Ticket{}}}
	rig.ticket = func(id kernel.TicketID, exp time.Time, refreshable bool) playback.Ticket {
		return playback.Ticket{
			V: playback.TicketSchemaVersion, ID: id, ExpiresAt: exp, Refreshable: refreshable,
			Mode: playback.DeliveryProxy,
			Upstream: playback.Upstream{
				Protocol: playback.ProtocolHLS, URL: cdn.URL + "/master.m3u8",
				AllowedOrigins: []string{"127.0.0.1"},
			},
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/tickets/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		id := kernel.TicketID(r.PathValue("id"))
		if rig.hold != nil {
			<-rig.hold
		}
		// Ответ core чуть задерживается: одновременные запросы шлюза
		// должны успеть встретиться на одном обновлении.
		time.Sleep(30 * time.Millisecond)
		rig.refreshes.Add(1)
		rig.store.put(rig.ticket(id, time.Now().Add(time.Hour), true))
	})
	core := httptest.NewServer(mux)
	t.Cleanup(core.Close)

	sealer, err := signedtoken.NewSealer("0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	rig.gw = httptest.NewServer(New(rig.store, sealer, oneRoute{cdn.Client()},
		NewTicketRefresher(core.URL, "svc"), nil, nil, slog.New(slog.DiscardHandler)).Routes())
	t.Cleanup(rig.gw.Close)
	return rig
}

func (rig *refreshRig) get(t *testing.T, id string) int {
	t.Helper()
	resp, err := http.Get(rig.gw.URL + "/stream/" + id + "/master.m3u8")
	if err != nil {
		t.Error(err)
		return 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// Серию оставили на паузе дольше срока ссылок и нажали «играть»: запросы
// не должны получать отказ, пока плеер доигрывает буфер.
// Тикет ещё лежит — шлюз обновляет его сам, под тем же номером, и одним
// обновлением на все одновременные запросы.
func TestExpiredTicketRefreshedOnce(t *testing.T) {
	rig := newRefreshRig(t)
	rig.store.put(rig.ticket("paused", time.Now().Add(-time.Minute), true))

	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func() { defer wg.Done(); codes[i] = rig.get(t, "paused") }()
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("запрос %d: %d", i, c)
		}
	}
	if n := rig.refreshes.Load(); n != 1 {
		t.Fatalf("обновлений %d, ждали одно на все запросы", n)
	}
	if rig.get(t, "paused") != http.StatusOK || rig.refreshes.Load() != 1 {
		t.Fatal("свежий тикет обновлён ещё раз")
	}
}

// Необновляемый протухший тикет — честный 410: витрина попросит новый.
func TestExpiredUnrefreshableTicketGone(t *testing.T) {
	rig := newRefreshRig(t)
	rig.store.put(rig.ticket("fixed", time.Now().Add(-time.Minute), false))
	if c := rig.get(t, "fixed"); c != http.StatusGone {
		t.Fatalf("код %d", c)
	}
	if rig.refreshes.Load() != 0 {
		t.Fatal("необновляемый тикет обновлялся")
	}
}

// Срок на подходе — тикет обновляется в фоне, а запрос, заметивший это,
// не ждёт площадку. Следующие запросы минуту обновление не повторяют.
func TestTicketRefreshedAhead(t *testing.T) {
	rig := newRefreshRig(t)
	rig.hold = make(chan struct{})
	rig.store.put(rig.ticket("soon", time.Now().Add(2*time.Minute), true))

	done := make(chan int, 1)
	go func() { done <- rig.get(t, "soon") }()
	select {
	case c := <-done:
		if c != http.StatusOK {
			t.Fatalf("код %d", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("запрос ждал обновления тикета")
	}
	if rig.get(t, "soon") != http.StatusOK {
		t.Fatal("второй запрос")
	}
	close(rig.hold)

	deadline := time.Now().Add(2 * time.Second)
	for rig.refreshes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := rig.refreshes.Load(); n != 1 {
		t.Fatalf("обновлений %d", n)
	}
	if tk, _ := rig.store.Get(t.Context(), "soon"); time.Until(tk.ExpiresAt) < 30*time.Minute {
		t.Fatalf("срок не продлён: %v", tk.ExpiresAt)
	}
}
