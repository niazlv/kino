package httpadmin

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/postgres"
	"github.com/niazleushkin/kino/internal/app/health"
	"github.com/niazleushkin/kino/internal/domain/account"
)

// Протухший вход kinopub виден на любой странице админки — баннер с
// инструкцией живёт в общем каркасе (layout.html), — а в «Площадках» видно,
// продлевается ли токен сам.
func TestReauthBannerAndTokenStatus(t *testing.T) {
	s := newTestServer(t)
	since := time.Date(2026, 11, 13, 16, 3, 0, 0, time.UTC)
	s.WithProviderAuth(
		func() []health.Reauth {
			return []health.Reauth{{Provider: "kinopub", Since: since, Cause: "refresh-токен уже отвергнут авторизацией"}}
		},
		func() map[string]AuthInfo {
			return map[string]AuthInfo{"kinopub": {NeedsReauth: true, Reason: "свежий токен тоже отвергнут",
				ExpiresAt: since}}
		})
	now := time.Now()
	me := account.User{ID: 1, Login: "niaz", DisplayName: "Нияз", Role: account.RoleAdmin, CreatedAt: now}
	for _, page := range []string{"providers"} {
		data := map[string]any{
			"Title": "t", "Active": page, "Me": me, "V": "v", "Prefix": Prefix,
			"Reauths": s.reauthBanner(), "Auth": s.authViews(),
			"Catalog": postgres.CatalogCounts{}, "Providers": []providerView{},
		}
		var buf bytes.Buffer
		if err := s.tpl[page].ExecuteTemplate(&buf, "layout", data); err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		out := buf.String()
		for _, want := range []string{"kinopub: нужна переавторизация", "make kinopub-token", "KINOPUB_REFRESH_TOKEN",
			"refresh-токен уже отвергнут авторизацией"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s: нет %q", page, want)
			}
		}
		if page == "providers" && (!strings.Contains(out, "Вход на площадки") || !strings.Contains(out, "выключено")) {
			t.Fatal("в «Площадках» нет состояния токена")
		}
	}

	// Всё хорошо — баннера нет.
	s.WithProviderAuth(func() []health.Reauth { return nil }, nil)
	var buf bytes.Buffer
	data := map[string]any{"Title": "t", "Active": "providers", "Me": me, "V": "v", "Prefix": Prefix,
		"Reauths": s.reauthBanner(), "Catalog": postgres.CatalogCounts{}, "Providers": []providerView{}}
	if err := s.tpl["providers"].ExecuteTemplate(&buf, "layout", data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "переавторизация") {
		t.Fatal("баннер без повода")
	}
}
