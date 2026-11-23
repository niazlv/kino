package providers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/bootstrap/kit"
)

// ClientWrap зовётся для каждой подключаемой площадки со своим id, а клиент,
// который он вернул, — тот, с которым площадка собирается.
func TestClientWrapIsCalledPerProvider(t *testing.T) {
	t.Setenv("EGRESS_ROUTES", "")
	t.Setenv("KINOPUB_PROXY", "")
	routes, err := kit.LoadRoutes(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	cfg := Config{
		IDs: []string{"fake"},
		ClientWrap: func(id string, hc *http.Client) *http.Client {
			ids = append(ids, id)
			return hc
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, issues := BuildRegistryLenient(context.Background(), cfg, routes, log); len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	if len(ids) != 1 || ids[0] != "fake" {
		t.Errorf("ClientWrap звали для %v", ids)
	}
}
