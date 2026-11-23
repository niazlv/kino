package provider

import (
	"context"
	"testing"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// uaProv — провайдер, отдающий заданные заголовки тикета.
type uaProv struct {
	Provider
	id      string
	headers map[string]string
}

func (p uaProv) Describe(context.Context) (dp.Descriptor, error) {
	return dp.Descriptor{APIMajor: dp.APIMajor, ProviderID: p.id}, nil
}

func (p uaProv) Resolve(context.Context, dp.ResolveRequest) (dp.Source, error) {
	return dp.Source{Protocol: "HLS", URL: "https://cdn/x.m3u8", Headers: p.headers}, nil
}

func resolveVia(t *testing.T, r *Registry, id string) dp.Source {
	t.Helper()
	src, err := r.Resolve(context.Background(), dp.ResolveRequest{Ref: kernel.ProviderRef{ProviderID: id}})
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func TestRegistryWithoutUserAgentHookLeavesHeadersAsTheyAre(t *testing.T) {
	r := NewRegistry()
	shared := map[string]string{"User-Agent": "Mozilla/5.0 foreign"}
	if _, err := r.Register(context.Background(), uaProv{id: "a", headers: shared}, ""); err != nil {
		t.Fatal(err)
	}
	if got := resolveVia(t, r, "a").Headers["User-Agent"]; got != "Mozilla/5.0 foreign" {
		t.Errorf("сервер без хука: %q", got)
	}
}

func TestRegistryUserAgentHookRewritesTicketHeaders(t *testing.T) {
	r := NewRegistry()
	provHeaders := map[string]string{"user-agent": "Mozilla/5.0 foreign", "Referer": "https://x/"}
	for _, id := range []string{"a", "b"} {
		if _, err := r.Register(context.Background(), uaProv{id: id, headers: provHeaders}, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Провайдер без UA вовсе: тикету всё равно кладётся наша подпись.
	if _, err := r.Register(context.Background(), uaProv{id: "c"}, ""); err != nil {
		t.Fatal(err)
	}
	r.WithUserAgent(func(id, cur string) string { return "Our/" + id + "|" + cur })

	a := resolveVia(t, r, "a")
	if got := a.Headers["User-Agent"]; got != "Our/a|Mozilla/5.0 foreign" {
		t.Errorf("UA в тикете: %q (хук получает то, что поставил провайдер, в любом регистре ключа)", got)
	}
	if _, dup := a.Headers["user-agent"]; dup {
		t.Error("прежний ключ в другом регистре остался: два UA в одном тикете")
	}
	if a.Headers["Referer"] != "https://x/" {
		t.Errorf("прочие заголовки потеряны: %+v", a.Headers)
	}
	if got := resolveVia(t, r, "b").Headers["User-Agent"]; got != "Our/b|Mozilla/5.0 foreign" {
		t.Errorf("вторая площадка: %q", got)
	}
	if got := resolveVia(t, r, "c").Headers["User-Agent"]; got != "Our/c|" {
		t.Errorf("без UA у провайдера: %q", got)
	}
	// Карта провайдера не тронута: он вправе отдавать одну и ту же всем.
	if provHeaders["user-agent"] != "Mozilla/5.0 foreign" || len(provHeaders) != 2 {
		t.Errorf("карта провайдера изменена: %+v", provHeaders)
	}
}
