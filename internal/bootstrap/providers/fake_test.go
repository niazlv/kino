package providers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/providers/source"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/bootstrap/kit"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// fakeProvider — площадка для тестов сборки: Describe отвечает «недоступна»,
// так что фоновая регистрация просто ждёт повтора. Остальное не зовётся.
type fakeProvider struct{ appprov.Provider }

func (fakeProvider) Describe(context.Context) (dp.Descriptor, error) {
	return dp.Descriptor{}, errors.New("fake: недоступна")
}

// fakeLogin — вход по коду, который запоминает, с каким клиентом собран.
type fakeLogin struct {
	appprov.DeviceLogin
	env source.Env
}

// Тестам сборки нужны известные площадки; настоящие сюда не попадают — ядро
// их не знает (ADR-0018).
func init() {
	newFake := func(source.Env) (appprov.Provider, error) { return fakeProvider{}, nil }
	source.Register(source.Spec{ID: "fake", Name: "Fake", Kind: source.KindGeneral, New: newFake,
		Default: true, History: true, VoiceNames: true,
		Login: func(env source.Env) appprov.DeviceLogin { return fakeLogin{env: env} }})
	source.Register(source.Spec{ID: "fake_anime", Name: "Fake anime", Kind: source.KindAnime, New: newFake,
		TitleBridge: true})
}

func TestRolesComeFromSpecs(t *testing.T) {
	if got := source.DefaultIDs(); !slices.Equal(got, []string{"fake"}) {
		t.Errorf("по умолчанию: %v", got)
	}
	if got := VoiceNameProviders(); !slices.Equal(got, []string{"fake"}) {
		t.Errorf("образец озвучек: %v", got)
	}
	if HistoryProvider() != "fake" || TitleBridgeProvider() != "fake_anime" {
		t.Errorf("история %q, мост %q", HistoryProvider(), TitleBridgeProvider())
	}
}

// PROVIDERS не задан — включаются площадки с Default.
func TestLoadConfigDefaultsToSpecs(t *testing.T) {
	t.Setenv("PROVIDERS", "")
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.IDs, []string{"fake"}) {
		t.Fatalf("IDs = %v", c.IDs)
	}
}

// Вход собирается для каждой площадки со входом, с клиентом её маршрута
// (через ClientWrap); маршрута нет — клиента нет, но вход есть.
func TestLoginsGetRouteClient(t *testing.T) {
	t.Setenv("EGRESS_ROUTES", "")
	t.Setenv("KINOPUB_PROXY", "")
	routes, err := kit.LoadRoutes(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	wrapped := 0
	cfg := Config{ClientWrap: func(id string, hc *http.Client) *http.Client { wrapped++; return hc }}
	got := Logins(context.Background(), cfg, routes, log)
	if l, ok := got["fake"].(fakeLogin); !ok || l.env.HTTP == nil || wrapped != 1 || len(got) != 1 {
		t.Fatalf("входы: %+v, обёрнуто %d", got, wrapped)
	}
	cfg.Egress = map[string]string{"fake": "призрак"}
	if l := Logins(context.Background(), cfg, routes, log)["fake"].(fakeLogin); l.env.HTTP != nil {
		t.Fatal("маршрута нет, а клиент есть")
	}
}
