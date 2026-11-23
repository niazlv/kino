package mediahttp

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/segcache"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/pkg/signedtoken"
)

// segBench — шлюз с кешем сегментов перед медленным CDN. CDN отдаёт первую
// половину сегмента сразу, а вторую — только по отмашке теста: так видно,
// идут ли байты клиенту до конца закачки.
type segBench struct {
	t    *testing.T
	gw   *httptest.Server
	segs *segcache.Cache
	dir  string
	path string // адрес сегмента на шлюзе
	key  string
	body []byte
	hits atomic.Int32
	rest chan struct{} // закрыть — CDN дописывает сегмент
	cdn  cdnMode
}

type cdnMode struct {
	breakMid bool // рвёт соединение на середине сегмента
	chunked  bool // без Content-Length
}

func newSegBench(t *testing.T, mode cdnMode) *segBench {
	t.Helper()
	b := &segBench{
		t: t, dir: t.TempDir(), rest: make(chan struct{}), cdn: mode,
		body: bytes.Repeat([]byte("0123456789abcdef"), 64<<10), // 1 МБ
	}
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hls/seg-1.ts" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b.hits.Add(1)
		half := len(b.body) / 2
		w.Header().Set("Content-Type", "video/mp2t")
		if !b.cdn.chunked {
			w.Header().Set("Content-Length", strconv.Itoa(len(b.body)))
		}
		_, _ = w.Write(b.body[:half])
		w.(http.Flusher).Flush()
		if b.cdn.breakMid {
			panic(http.ErrAbortHandler)
		}
		select {
		case <-b.rest:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(b.body[half:])
	}))
	t.Cleanup(cdn.Close)

	var err error
	if b.segs, err = segcache.New(b.dir, 1<<30); err != nil {
		t.Fatal(err)
	}
	store := &memTickets{m: map[kernel.TicketID]playback.Ticket{}}
	store.put(playback.Ticket{
		V: playback.TicketSchemaVersion, ID: "t", ExpiresAt: time.Now().Add(time.Hour),
		Mode: playback.DeliveryProxy,
		Upstream: playback.Upstream{
			Protocol: playback.ProtocolHLS, URL: cdn.URL + "/hls/master.m3u8",
			AllowedOrigins: []string{"127.0.0.1"},
		},
	})
	sealer, err := signedtoken.NewSealer("0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	b.gw = httptest.NewServer(New(store, sealer, oneRoute{cdn.Client()}, nil, b.segs, nil,
		slog.New(slog.DiscardHandler)).Routes())
	t.Cleanup(b.gw.Close)
	// Отмашка на случай упавшего теста: иначе CDN держал бы закрытие.
	t.Cleanup(func() { b.finish() })

	raw := cdn.URL + "/hls/seg-1.ts"
	b.path = "/stream/t/r/" + sealer.Seal(raw, "t")
	b.key, _ = segcache.Key(raw, "")
	return b
}

func (b *segBench) finish() {
	select {
	case <-b.rest:
	default:
		close(b.rest)
	}
}

// get запрашивает сегмент и ждёт заголовков не дольше двух секунд — CDN
// к этому моменту ещё не дописал сегмент.
func (b *segBench) get(ctx context.Context) *http.Response {
	b.t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, b.gw.URL+b.path, nil)
	type result struct {
		resp *http.Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		ch <- result{resp, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			b.t.Fatal(res.err)
		}
		if res.resp.StatusCode != http.StatusOK {
			b.t.Fatalf("статус %d", res.resp.StatusCode)
		}
		return res.resp
	case <-time.After(2 * time.Second):
		b.t.Fatal("ответа нет, пока CDN не дописал сегмент: шлюз ждёт конца закачки")
		return nil
	}
}

// firstHalf читает первую половину сегмента, пока CDN держит вторую.
func (b *segBench) firstHalf(r io.Reader) {
	b.t.Helper()
	half := len(b.body) / 2
	buf := make([]byte, half)
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(r, buf); done <- err }()
	select {
	case err := <-done:
		if err != nil || !bytes.Equal(buf, b.body[:half]) {
			b.t.Fatalf("первая половина: %v", err)
		}
	case <-time.After(2 * time.Second):
		b.t.Fatal("первые байты не дошли до конца закачки")
	}
}

// cached ждёт, пока закачка встанет в кеш, и отдаёт размер файла там.
//
// Сразу после чтения хвоста проверять рано: клиент с Content-Length
// дочитывает сегмент, как только пришёл последний байт, а закачка узнаёт о
// конце от CDN и переносит файл в кеш чуть позже: без ожидания тест
// падает примерно раз в сто прогонов.
func (b *segBench) cached() (int64, bool) {
	b.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if f, size, hit := b.segs.Open(b.key); hit {
			_ = f.Close()
			return size, true
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (b *segBench) files() []string {
	var out []string
	_ = filepath.WalkDir(b.dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, d.Name())
		}
		return nil
	})
	return out
}

// Сегмент идёт клиенту по мере закачки, а в кеш встаёт целиком: 4K с
// медленного маршрута не должен ждать первого байта всю закачку.
func TestSegment_StreamsWhileCaching(t *testing.T) {
	b := newSegBench(t, cdnMode{})

	resp := b.get(context.Background())
	defer resp.Body.Close()
	if resp.ContentLength != int64(len(b.body)) {
		t.Fatalf("Content-Length %d", resp.ContentLength)
	}
	b.firstHalf(resp.Body)
	if _, _, hit := b.segs.Open(b.key); hit {
		t.Fatal("недокачанный сегмент уже в кеше")
	}

	b.finish()
	rest, err := io.ReadAll(resp.Body)
	if err != nil || !bytes.Equal(rest, b.body[len(b.body)/2:]) {
		t.Fatalf("хвост: %v, %d байт", err, len(rest))
	}

	// Повтор — из кеша, без похода на CDN. Пока файл не перенесён, повтор
	// подхватил бы ещё идущую закачку — тоже без CDN, но без X-Cache.
	if _, hit := b.cached(); !hit {
		t.Fatal("докачанный сегмент не встал в кеш")
	}
	again := b.get(context.Background())
	all, _ := io.ReadAll(again.Body)
	_ = again.Body.Close()
	if again.Header.Get("X-Cache") != "HIT" || !bytes.Equal(all, b.body) {
		t.Fatalf("повтор: X-Cache=%q, %d байт", again.Header.Get("X-Cache"), len(all))
	}
	if n := b.hits.Load(); n != 1 {
		t.Fatalf("на CDN сходили %d раз", n)
	}
}

// Оборванная площадкой закачка не оставляет в кеше ни сегмента, ни
// временного файла, а клиент получает обрыв, а не обрубок под видом целого.
// Без Content-Length обрубок отличим только по оборванному соединению.
func TestSegment_UpstreamBreakLeavesNoFile(t *testing.T) {
	for name, chunked := range map[string]bool{"с длиной": false, "chunked": true} {
		t.Run(name, func(t *testing.T) {
			b := newSegBench(t, cdnMode{breakMid: true, chunked: chunked})

			resp := b.get(context.Background())
			all, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err == nil {
				t.Fatalf("клиент принял обрубок в %d байт за целый сегмент", len(all))
			}
			if got := b.files(); len(got) != 0 {
				t.Fatalf("после обрыва в кеше: %v", got)
			}
		})
	}
}

// Клиент оборвал сегмент на середине и повторил: повтор подхватывает
// идущую закачку, а не тянет сегмент с площадки заново.
func TestSegment_RetryJoinsDownload(t *testing.T) {
	b := newSegBench(t, cdnMode{})

	ctx, cancel := context.WithCancel(context.Background())
	first := b.get(ctx)
	b.firstHalf(first.Body)
	cancel()
	_ = first.Body.Close()
	// Даём шлюзу заметить уход клиента. Не заметит — повтор всё равно
	// должен подключиться к той же закачке.
	time.Sleep(100 * time.Millisecond)

	retry := b.get(context.Background())
	defer retry.Body.Close()
	b.firstHalf(retry.Body)
	b.finish()
	rest, err := io.ReadAll(retry.Body)
	if err != nil || !bytes.Equal(rest, b.body[len(b.body)/2:]) {
		t.Fatalf("хвост повтора: %v, %d байт", err, len(rest))
	}
	if n := b.hits.Load(); n != 1 {
		t.Fatalf("на CDN сходили %d раз: повтор не подхватил закачку", n)
	}
	if size, hit := b.cached(); !hit || size != int64(len(b.body)) {
		t.Fatalf("в кеше: %v, %d байт", hit, size)
	}
}
