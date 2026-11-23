package httpuser

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func image(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Request: r,
		Header: http.Header{"Content-Type": {"image/jpeg"}},
		Body:   io.NopCloser(strings.NewReader("jpeg"))}, nil
}

func hang(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

// Мёртвый узел VPN не гасит постеры: запасной маршрут подхватывает сразу
// при отказе и через artHedge — при молчании.
func TestFetchArt_FallbackRoute(t *testing.T) {
	old := artHedge
	artHedge = 30 * time.Millisecond
	defer func() { artHedge = old }()
	direct := &http.Client{Transport: roundTrip(image)}

	cases := map[string]roundTrip{
		"отказ":  func(*http.Request) (*http.Response, error) { return nil, errors.New("proxyconnect: refused") },
		"молчит": hang,
	}
	for name, main := range cases {
		s := &Server{art: &http.Client{Transport: main}, artFallback: direct}
		start := time.Now()
		b, ct, err := s.fetchArt(context.Background(), "https://m.staticpop.net/poster.jpg")
		if err != nil || string(b) != "jpeg" || ct != "image/jpeg" {
			t.Fatalf("%s: %q %q %v", name, b, ct, err)
		}
		if took := time.Since(start); took > 500*time.Millisecond {
			t.Fatalf("%s: ждали %v", name, took)
		}
	}

	// Без запасного маршрута отказ основного — отказ.
	s := &Server{art: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("refused")
	})}}
	if _, _, err := s.fetchArt(context.Background(), "https://x/y.jpg"); err == nil {
		t.Fatal("отказ единственного маршрута потерялся")
	}
}
