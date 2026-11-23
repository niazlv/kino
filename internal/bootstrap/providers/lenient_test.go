package providers

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/bootstrap/kit"
)

func TestBuildRegistryLenientSkipsBrokenProviders(t *testing.T) {
	t.Setenv("EGRESS_ROUTES", "")
	t.Setenv("KINOPUB_PROXY", "")
	routes, err := kit.LoadRoutes(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := Config{
		// Неизвестное имя и площадка с маршрутом, которого нет: обе не должны
		// ни ронять сборку, ни попадать в реестр (подключить их нечем).
		IDs:    []string{"нет-такой", "fake"},
		Egress: map[string]string{"fake": "призрак"},
	}

	reg, issues := BuildRegistryLenient(context.Background(), cfg, routes, log)
	if reg == nil {
		t.Fatal("реестр не собран")
	}
	if len(issues) != 2 {
		t.Fatalf("issues = %+v, ждали две", issues)
	}
	if issues[0].ID != "нет-такой" || issues[0].Reason != "площадка неизвестна" {
		t.Errorf("первая: %+v", issues[0])
	}
	if issues[1].ID != "fake" || issues[1].Reason != `маршрут "призрак" не задан` {
		t.Errorf("вторая: %+v", issues[1])
	}
}

// Сервер остаётся строгим: опечатка в конфиге должна ронять запуск, а не
// тихо оставлять каталог без площадки.
func TestBuildRegistryStaysStrict(t *testing.T) {
	routes, err := kit.LoadRoutes(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := BuildRegistry(context.Background(), Config{IDs: []string{"нет-такой"}}, routes, log); err == nil {
		t.Fatal("неизвестная площадка принята строгой сборкой")
	}
	cfg := Config{IDs: []string{"fake"}, Egress: map[string]string{"fake": "призрак"}}
	if _, err := BuildRegistry(context.Background(), cfg, routes, log); err == nil {
		t.Fatal("маршрут без прокси принят строгой сборкой")
	}
}
