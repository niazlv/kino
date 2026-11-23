package sitekit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestOriginOf(t *testing.T) {
	for in, want := range map[string]string{
		"//flora.cloud.solodcdn.com/a": "solodcdn.com",
		"https://vd368.okcdn.ru/x":     "okcdn.ru",
		"https://example.com/":         "example.com",
	} {
		if got := OriginOf(in); got != want {
			t.Errorf("%s -> %s, ожидалось %s", in, got, want)
		}
	}
}

// 429 — переждать и повторить, а не потерять страницу; хост замедляется.
func TestClient_Retries429(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), 10*time.Millisecond)
	start := time.Now()
	resp, err := c.Get(context.Background(), srv.URL+"/x", nil)
	if err != nil || resp.Status != 200 || hits != 2 {
		t.Fatalf("status=%d hits=%d err=%v", resp.Status, hits, err)
	}
	if time.Since(start) < time.Second {
		t.Fatal("Retry-After не выдержан")
	}
	u, _ := url.Parse(srv.URL)
	if c.hostGap[u.Hostname()] <= 10*time.Millisecond {
		t.Fatal("хост после 429 не замедлен")
	}
}
