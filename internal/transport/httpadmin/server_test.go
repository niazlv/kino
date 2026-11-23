package httpadmin

import (
	"bytes"
	"html/template"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/postgres"
	"github.com/niazleushkin/kino/internal/app/voicenames"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(nil, nil, nil, nil, nil, nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	if err != nil {
		t.Fatalf("шаблоны не разобрались: %v", err)
	}
	return s
}

// Шаблоны проверяются исполнением, а не только разбором: неверное имя поля
// html/template находит лишь при рендере, и находить его должен тест,
// а не админ на боевом сервере.
func TestPagesRender(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	me := account.User{ID: 1, Login: "niaz", DisplayName: "Нияз", Role: account.RoleAdmin,
		TOTPEnabled: true, CreatedAt: now, LastLoginAt: &now}
	other := account.User{ID: 2, Login: "anna", DisplayName: "Аня", Role: account.RoleUser, CreatedAt: now}
	errText := "площадка недоступна"
	stale := now.Add(-5 * time.Hour)
	provs := []providerView{
		providerStatus(postgres.ProviderState{ProviderID: "sleepy", LastRunAt: &stale}),
		providerStatus(postgres.ProviderState{ProviderID: "kinopub", LastRunAt: &now, LastSyncedAt: &now, TitlesSeen: 53000}),
		providerStatus(postgres.ProviderState{ProviderID: "broken", LastRunAt: &now, LastError: &errText}),
		providerStatus(postgres.ProviderState{ProviderID: "fresh"}),
		{ProviderState: postgres.ProviderState{ProviderID: "animego_me"}, Status: "first",
			StatusLabel: "первый обход идёт", Name: "AnimeGO.me", Refs: 681},
	}
	audit := []postgres.AuditEntry{{ID: 7, ActorLogin: "niaz", Action: "user.set_role", Target: "anna",
		Reason: "просил", Before: `{"Role":"user"}`, After: `{"Role":"admin"}`, CreatedAt: now}}
	sessions := []account.Session{{Space: account.SpaceAdmin, DeviceKind: "admin-ui", IssuedAt: now, ExpiresAt: now.Add(time.Hour)},
		{Space: account.SpaceUser, DeviceKind: "ios", IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour)}}

	cases := []struct {
		page string
		data map[string]any
		want string
	}{
		{"dashboard", map[string]any{
			"Users": 2, "Active7": 1, "Disabled": 1, "Admins": 1, "Invites": 1,
			"Catalog":   postgres.CatalogCounts{Titles: 10, Episodes: 200, Series: 4, Movies: 6, WithIMDB: 9},
			"Providers": provs, "Sessions7": 3, "Errors7": 1, "Bytes7": int64(3 << 30), "Audit": audit,
		}, "давно не запускался"},
		{"users", map[string]any{"Users": []userRow{{User: me, Sessions: 2}, {User: other}}}, "anna"},
		{"user", map[string]any{"User": other, "Sessions": sessions, "IsSelf": false, "Sudo": false,
			"Passkeys": []account.Passkey{{ID: 1, Name: "iPhone", CreatedAt: now, LastUsedAt: &now, BackedUp: true}},
			"Reveal":   &reveal{Kind: "password", Value: "abcd-efgh-jkmn-pqrs"}}, "iPhone"},
		{"user", map[string]any{"User": me, "Sessions": sessions, "IsSelf": true, "Sudo": true, "Passkeys": []account.Passkey{},
			"Reveal":   &reveal{Kind: "totp", Value: "otpauth://totp/kino:niaz?secret=ABC&issuer=kino"},
			"RevealQR": template.URL("data:image/png;base64,AAAA")}, "data:image/png;base64,AAAA"},
		{"user", map[string]any{"User": other, "Sessions": sessions, "IsSelf": false, "Sudo": true, "Passkeys": []account.Passkey{},
			"Import": importJob{What: "папка «Данис» в «Буду смотреть»", Started: now, Finished: now, Summary: "перенесено 419 из 419"},
			"Reveal": &reveal{Kind: "created", Value: "abcd-efgh-jkmn-pqrs"}}, "перенесено 419 из 419"},
		{"user", map[string]any{"User": other, "Sessions": sessions, "IsSelf": false, "Sudo": true, "Passkeys": []account.Passkey{},
			"Import": nil, "Reveal": &reveal{Kind: "device", Value: "Android · Pixel 8"}}, "Android · Pixel 8"},
		{"import", map[string]any{"User": other,
			"Job":     importJob{What: "«я смотрю» и позиции", Started: now, Err: "kinopub: rate limited", Finished: now},
			"Folders": []dp.BookmarkFolder{{ID: "893479", Title: "Данис", Count: 419}}}, "419 тайтлов"},
		{"import", map[string]any{"User": other, "Job": importJob{What: "«я смотрю» и позиции", Started: now},
			"FoldersErr": "площадка недоступна"}, "Идёт перенос"},
		{"invites", map[string]any{
			"Open": []inviteRow{{Invite: account.Invite{Code: "ABCDE-FGHJK", ExpiresAt: now.Add(48 * time.Hour)},
				Link: "https://kino.example/#/invite/ABCDE-FGHJK", CreatedBy: "niaz"}},
			"Recent": []inviteRow{{Invite: account.Invite{Code: "QQQQQ-WWWWW", UsedAt: &now}, Used: true, UsedBy: "anna", CreatedBy: "niaz"}},
		}, "ABCDE-FGHJK"},
		{"providers", map[string]any{"Providers": provs, "Catalog": postgres.CatalogCounts{Titles: 1}}, "первый обход идёт"},
		{"playback", map[string]any{
			"Stats": []postgres.Stats{{Day: "2026-09-05", ByImpl: "hlsjs", Sessions: 2, Errors: 1, RebufferAvg: .5, TTFFP50: 900}},
			"Bars":  []bar{{Day: "2026-09-05", Label: "5", Sessions: 2, Errors: 1, Pct: 100}}, "Total": 2, "Errors": 1,
		}, "hlsjs"},
		{"audit", map[string]any{"Audit": audit, "Next": int64(7), "NextURL": Prefix + "/audit?before=7",
			"Actions": []string{"title.merge", "source.priority"}, "Users": []account.User{{ID: 1, Login: "niaz"}},
			"F": postgres.AuditFilter{}}, "before=7"},
		{"titles", map[string]any{"Q": "принцесса", "Kind": "recent", "Total": 1, "NextOffset": 0,
			"Tabs": []queueTab{{Kind: "recent", Label: "Новые"}, {Kind: "one", Label: "Одна площадка"}},
			"Rows": []postgres.TitleRow{
			{ID: 38219, Kind: "series", Title: "Принцесса-шпионка / Princess Principal", Year: 2017, Sources: 2}}}, "/titles/38219"},
		{"title", map[string]any{
			"T": catalog.Title{ID: 38219, Kind: catalog.KindSeries, Title: "Принцесса-шпионка", Year: 2017,
				IDs: catalog.ExternalIDs{IMDB: "tt6692956"}, Genres: []string{"Аниме"}},
			"Sources": []sourceView{
				{SourceRow: postgres.SourceRow{ProviderID: "kinopub", ItemID: "21267", Priority: 100, Episodes: 12}, Name: "kinopub"},
				{SourceRow: postgres.SourceRow{ProviderID: "animego_me", ItemID: "princessa-shpionka-421", SeasonNo: 1,
					Priority: 80, Manual: true, Disabled: true, Pinned: true, MatchedBy: "name", MAL: "35240",
					Episodes: 12, SeenAt: &now}, Name: "AnimeGO.me"},
			},
			"Providers": []sourceView{{SourceRow: postgres.SourceRow{ProviderID: "animego_fr"}, Name: "AnimeGO.fr"}},
		}, "MAL 35240"},
		{"voices", map[string]any{"Q": "", "Seen": 3,
			"Aliases": []voicenames.AliasView{{Alias: voicenames.Alias{From: "мегааниме", To: "megaanime",
				Origin: voicenames.OriginAuto, Note: "по транслиту", CreatedAt: now}, FromTitle: "Мега-Аниме", ToTitle: "Mega-Anime"}},
			"Suggestions": []suggestionView{{
				A:   studioView{Key: "anistar", Title: "AniStar", Names: "AnimeGO.fr", Hits: 2, Link: Prefix + "/titles/7"},
				B:   studioView{Key: "anistarмногоголосый", Title: "AniStar Многоголосый", Hits: 1},
				Why: "одно — начало другого"}},
			"Studios": []studioView{{Key: "anistar", Title: "AniStar", Others: []string{"Anistar"}}},
		}, "Склеить в «AniStar»"},
	}
	for _, c := range cases {
		c.data["Title"], c.data["Active"], c.data["Me"] = "t", c.page, me
		c.data["V"], c.data["Prefix"], c.data["Flash"] = "v", Prefix, "готово"
		var buf bytes.Buffer
		if err := s.tpl[c.page].ExecuteTemplate(&buf, "layout", c.data); err != nil {
			t.Fatalf("%s: %v", c.page, err)
		}
		out := buf.String()
		if !strings.Contains(out, "</html>") {
			t.Fatalf("%s: страница оборвана", c.page)
		}
		if !strings.Contains(out, c.want) {
			t.Fatalf("%s: нет %q", c.page, c.want)
		}
	}

	for _, page := range standalone {
		var buf bytes.Buffer
		data := map[string]any{"V": "v", "Prefix": Prefix, "Error": "нет", "Flash": "ф", "Login": "x", "Code": 404, "Text": "нет",
			"Next": Prefix + "/users/2", "URL": "otpauth://totp/kino:niaz?secret=ABC", "QR": template.URL("data:image/png;base64,AAAA"),
			"HasTOTP": true, "HasPasskeys": true}
		if err := s.tpl[page].ExecuteTemplate(&buf, page, data); err != nil {
			t.Fatalf("%s: %v", page, err)
		}
	}
}

func TestQRFromOTPAuth(t *testing.T) {
	uri, err := qrDataURI("otpauth://totp/kino:niaz?secret=JBSWY3DPEHPK3PXP&issuer=kino")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(uri), "data:image/png;base64,") {
		t.Fatalf("не data-URI: %.40s", uri)
	}
}

func TestLimiterCountsPerKeyAndGlobally(t *testing.T) {
	l := newLimiter(time.Minute, 2, 3)
	now := time.Now()
	if !l.allow("a", now) || !l.allow("a", now) {
		t.Fatal("первые попытки с адреса должны проходить")
	}
	if l.allow("a", now) {
		t.Fatal("третья попытка с адреса прошла — лимит по ключу не работает")
	}
	if !l.allow("b", now) {
		t.Fatal("другой адрес должен проходить")
	}
	// a, a, b — общий потолок в три достигнут: подмена адреса не помогает.
	if l.allow("c", now) {
		t.Fatal("общий лимит не работает — перебор с подменой адреса пройдёт")
	}
	if !l.allow("c", now.Add(2*time.Minute)) {
		t.Fatal("после окна попытки должны снова проходить")
	}
}

func TestReveals(t *testing.T) {
	s := newTestServer(t)
	key := s.stash("password", "секрет")
	if rv := s.take(key); rv == nil || rv.Value != "секрет" {
		t.Fatal("секрет не вернулся")
	}
	if s.take(key) != nil {
		t.Fatal("секрет читается второй раз")
	}
	if s.take("нет-такого") != nil {
		t.Fatal("чужой ключ что-то вернул")
	}
}

func TestSameSite(t *testing.T) {
	r := httptest.NewRequest("POST", "http://kino.example/admin/login", nil)
	r.Header.Set("Origin", "https://kino.example")
	if !sameSite(r) {
		t.Fatal("свой Origin отвергнут")
	}
	r.Header.Set("Origin", "https://evil.example")
	if sameSite(r) {
		t.Fatal("чужой Origin пропущен")
	}
	r.Header.Del("Origin")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if sameSite(r) {
		t.Fatal("cross-site без Origin пропущен")
	}
}

func TestClientIPPrefersProxyHeaders(t *testing.T) {
	r := httptest.NewRequest("POST", "/admin/login", nil)
	r.RemoteAddr = "172.18.0.5:4242"
	if ip := clientIP(r); ip != "172.18.0.5" {
		t.Fatalf("без заголовков ждали RemoteAddr, получили %q", ip)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.60")
	if ip := clientIP(r); ip != "203.0.113.7" {
		t.Fatalf("ждали первый адрес из X-Forwarded-For, получили %q", ip)
	}
	r.Header.Set("X-Real-IP", "198.51.100.9")
	if ip := clientIP(r); ip != "198.51.100.9" {
		t.Fatalf("X-Real-IP должен быть главнее, получили %q", ip)
	}
}

func TestSafeNextRejectsForeignAndProtocolRelative(t *testing.T) {
	for in, want := range map[string]string{
		Prefix + "/users/2":          Prefix + "/users/2",
		Prefix + "/users/2?m=role":   Prefix + "/users/2?m=role",
		"https://evil.example/admin": "",
		"//evil.example":             "",
		"/api/v1/me":                 "",
		"":                           "",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
