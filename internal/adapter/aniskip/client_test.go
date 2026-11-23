package aniskip

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Ответы сняты 03.10.2026: «Фрирен» (MAL 52991) серия 2.
func TestParse(t *testing.T) {
	got, err := Parse(fixture(t, "frieren_2.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := []dp.Segment{
		{Kind: dp.SegIntro, StartMS: 6_576, EndMS: 96_576, ForDurationMS: 1_566_565},
		{Kind: dp.SegOutro, StartMS: 1_460_000, EndMS: 1_550_000, ForDurationMS: 1_561_000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// «Смешанные» отрезки не берутся: пропустив их, пропустишь и сюжет.
func TestParse_SkipsMixed(t *testing.T) {
	got, err := Parse(fixture(t, "mixed_57334_1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.Kind != dp.SegIntro && s.Kind != dp.SegOutro {
			t.Fatalf("лишний отрезок %+v", s)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d отрезков, want 2", len(got))
	}
}

func TestClient(t *testing.T) {
	body := fixture(t, "frieren_2.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/skip-times/52991/2":
			if got := r.URL.Query()["types"]; !reflect.DeepEqual(got, []string{"op", "ed", "recap"}) {
				t.Errorf("types = %v", got)
			}
			_, _ = w.Write(body)
		case "/v2/skip-times/918/100":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"found":false,"results":[],"message":"No skip times found","statusCode":404}`))
		default:
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer srv.Close()
	c := New(srv.Client(), srv.URL)
	ctx := context.Background()

	if got, err := c.Segments(ctx, "52991", 2); err != nil || len(got) != 2 {
		t.Fatalf("found: %v, %v", got, err)
	}
	if _, err := c.Segments(ctx, "918", 100); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("missing: err = %v", err)
	}
	if _, err := c.Segments(ctx, "1", 1); !errors.Is(err, kernel.ErrBusy) {
		t.Fatalf("429: err = %v", err)
	}
	if _, err := c.Segments(ctx, "abc", 1); !errors.Is(err, kernel.ErrInvalidInput) {
		t.Fatalf("bad MAL: err = %v", err)
	}
}
