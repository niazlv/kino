package mediahttp

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/pkg/signedtoken"
)

// slowFile — CDN, отдающий файл кусками с паузой: весь файл идёт дольше
// срока похода, но байты не замолкают дольше паузы.
func slowFile(t *testing.T, chunks int, pause time.Duration, stallAfter int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(chunks*1024))
		chunk := make([]byte, 1024)
		for i := 0; i < chunks; i++ {
			if stallAfter > 0 && i == stallAfter {
				// Замолкли: сторож простоя обязан оборвать.
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
				return
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(pause)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fileGateway(t *testing.T, cdn *httptest.Server, proto playback.Protocol) *httptest.Server {
	t.Helper()
	store := &memTickets{m: map[kernel.TicketID]playback.Ticket{}}
	store.put(playback.Ticket{
		V: playback.TicketSchemaVersion, ID: "t", ExpiresAt: time.Now().Add(time.Hour),
		Mode:     playback.DeliveryProxy,
		Upstream: playback.Upstream{Protocol: proto, URL: cdn.URL + "/v.mp4", AllowedOrigins: []string{"127.0.0.1"}},
	})
	sealer, err := signedtoken.NewSealer("0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	gw := httptest.NewServer(New(store, sealer, oneRoute{cdn.Client()}, nil, nil, nil,
		slog.New(slog.DiscardHandler)).Routes())
	t.Cleanup(gw.Close)
	return gw
}

// Файл целиком идёт дольше срока похода — и доходит: у PROGRESSIVE срок
// считается от последнего куска, а не от начала (иначе рвался бы на 120-й
// секунде посреди файла).
func TestFileOutlivesUpstreamTimeout(t *testing.T) {
	old := upstreamTimeout
	upstreamTimeout = 300 * time.Millisecond
	t.Cleanup(func() { upstreamTimeout = old })

	cdn := slowFile(t, 12, 80*time.Millisecond, 0) // ~1 с при сроке 0,3 с
	gw := fileGateway(t, cdn, playback.ProtocolProgressive)
	resp, err := http.Get(gw.URL + "/stream/t/file")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil || n != 12*1024 {
		t.Fatalf("дошло %d байт из %d: %v", n, 12*1024, err)
	}
}

// Замолкший CDN всё равно обрывается — сторожем простоя.
func TestFileStallIsCut(t *testing.T) {
	old := upstreamTimeout
	upstreamTimeout = 300 * time.Millisecond
	t.Cleanup(func() { upstreamTimeout = old })

	cdn := slowFile(t, 12, 10*time.Millisecond, 3)
	gw := fileGateway(t, cdn, playback.ProtocolProgressive)
	start := time.Now()
	resp, err := http.Get(gw.URL + "/stream/t/file")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, resp.Body)
	if n >= 12*1024 || time.Since(start) > 3*time.Second {
		t.Fatalf("простой не оборван: %d байт за %v", n, time.Since(start))
	}
}
