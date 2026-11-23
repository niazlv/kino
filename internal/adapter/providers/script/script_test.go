package script

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/providers/providertest"
	"github.com/niazleushkin/kino/internal/adapter/providers/sitekit"
	"github.com/niazleushkin/kino/internal/adapter/providers/source"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// site — площадка-подделка: JSON-API с поиском, карточкой и сериями.
func site(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "дюна" {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		_, _ = io.WriteString(w, `[{"id": 7, "name": "Дюна &amp; <b>песок</b>", "year": "2021", "kp": 409424, "imdb": "1160419"}]`)
	})
	mux.HandleFunc("/title/7", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<html><h1> Дюна </h1><p class="plot">Пряность &amp; черви</p><img src="/p/7.jpg"></html>`)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/", http.StatusFound)
	})
	return httptest.NewServer(mux)
}

func pack(t *testing.T, srv *httptest.Server, body string) *Pack {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	src := fmt.Sprintf(`var SITE = %q;
source({
  id: "fake", name: "Подделка", version: "1.0.0", kind: "general",
  hosts: [%q], home: SITE + "/",
  settings: [{env: "FAKE_TOKEN", label: "Токен"}],
%s
});`, srv.URL, u.Hostname(), body)
	p, err := Parse("fake.js", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const direct = `
  search: function (q, limit) {
    return http.json(SITE + "/search", {query: {q: q}}).map(function (x) {
      return {id: x.id, title: html.text(x.name), year: +x.year, kp: x.kp, imdb: x.imdb};
    });
  },
  title: function (id) {
    if (id !== "7") throw NotFound("нет " + id);
    var doc = html.parse(http.text(SITE + "/title/" + id), SITE);
    return {id: id, title: doc.first("h1").text(), plot: doc.first(".plot").text(),
            poster: doc.first("img").abs("src"), kind: "movie", kp: 409424};
  },
  episodes: function (id) { return [{season: 0, episode: 1}]; },
  resolve: function (req) {
    return {url: "https://cdn.example.net/v/" + req.id + "/master.m3u8", headers: {Referer: SITE}};
  },
`

func env(t *testing.T) source.Env {
	return source.Env{Ctx: context.Background(), HTTP: http.DefaultClient, Log: slog.New(slog.DiscardHandler),
		Get: func(k string) string {
			if k == "FAKE_TOKEN" {
				return "секрет"
			}
			return ""
		}}
}

func TestDirectPath(t *testing.T) {
	srv := site(t)
	defer srv.Close()
	p, err := pack(t, srv, direct).New(env(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hits, err := p.Search(ctx, "дюна", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search: %v %+v", err, hits)
	}
	h := hits[0]
	if h.Ref.ItemID != "7" || h.IMDB != "tt1160419" || h.Kinopoisk != "409424" || h.Title != "Дюна & песок" {
		t.Fatalf("тайтл не в канонической форме: %+v", h)
	}
	ti, err := p.Title(ctx, h.Ref)
	if err != nil || ti.Kind != "movie" || ti.Plot != "Пряность & черви" || ti.Poster != srv.URL+"/p/7.jpg" {
		t.Fatalf("title: %v %+v", err, ti)
	}
	if _, err := p.Title(ctx, kernel.ProviderRef{ProviderID: "fake", ItemID: "8"}); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("NotFound не дошёл: %v", err)
	}
	src, err := p.Resolve(ctx, dp.ResolveRequest{Ref: h.Ref, Episode: 1})
	if err != nil {
		t.Fatal(err)
	}
	if src.Protocol != "HLS" || src.AllowedOrigins[0] != "example.net" || src.ExpiresAt.Before(time.Now().Add(time.Hour)) ||
		src.Headers["Referer"] != srv.URL {
		t.Fatalf("source: %+v", src)
	}
}

// TestContract — подделка проходит тот же набор проверок, что и настоящие
// площадки (кроме скачивания потока: CDN выдуман).
func TestContract(t *testing.T) {
	srv := site(t)
	defer srv.Close()
	p, err := pack(t, srv, direct).New(env(t))
	if err != nil {
		t.Fatal(err)
	}
	rep := providertest.Run(context.Background(), p, providertest.Case{ID: "fake", Query: "дюна"})
	if rep.Failed() {
		t.Fatal(rep.String())
	}
}

func TestSandbox(t *testing.T) {
	srv := site(t)
	defer srv.Close()
	cases := map[string]string{
		"чужой хост":              `http.text("https://evil.example/")`,
		"редирект на чужой хост":  `http.text(SITE + "/redirect")`,
		"необъявленная настройка": `settings.get("DATABASE_URL")`,
		"не http": `http.text("file:///etc/passwd")`,
	}
	for name, expr := range cases {
		t.Run(name, func(t *testing.T) {
			body := strings.Replace(direct, `if (id !== "7")`, expr+`; if (id !== "7")`, 1)
			p, err := pack(t, srv, body).New(env(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.Title(context.Background(), kernel.ProviderRef{ProviderID: "fake", ItemID: "7"}); err == nil {
				t.Fatal("песочница пропустила")
			}
		})
	}
	t.Run("объявленная настройка читается", func(t *testing.T) {
		body := strings.Replace(direct, `return {id: id, title: doc.first("h1").text()`,
			`return {id: id, title: settings.get("FAKE_TOKEN")`, 1)
		p, _ := pack(t, srv, body).New(env(t))
		ti, err := p.Title(context.Background(), kernel.ProviderRef{ProviderID: "fake", ItemID: "7"})
		if err != nil || ti.Title != "секрет" {
			t.Fatalf("%v %q", err, ti.Title)
		}
	})
}

func TestTimeoutInterruptsLoop(t *testing.T) {
	srv := site(t)
	defer srv.Close()
	body := strings.Replace(direct, `if (id !== "7")`, `while (true) {} if (id !== "7")`, 1)
	p, _ := pack(t, srv, body).New(env(t))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := p.Title(ctx, kernel.ProviderRef{ProviderID: "fake", ItemID: "7"})
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("бесконечный цикл не прерван: %v за %v", err, time.Since(start))
	}
	// Исполнитель вернулся в пул живым: следующий вызов работает.
	body2 := direct
	p2, _ := pack(t, srv, body2).New(env(t))
	if _, err := p2.Title(context.Background(), kernel.ProviderRef{ProviderID: "fake", ItemID: "7"}); err != nil {
		t.Fatal(err)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"нет source":       `var x = 1;`,
		"дважды source":    `source({}); source({});`,
		"нет методов":      `source({id: "a1", name: "A", version: "1.0", hosts: ["a.ru"], home: "https://a.ru/"})`,
		"плохой id":        `source({id: "Bad-ID", name: "A", version: "1.0", hosts: ["a.ru"], home: "https://a.ru/", search: function(){}, title: function(){}, players: function(){}})`,
		"home не из hosts": `source({id: "a1", name: "A", version: "1.0", hosts: ["a.ru"], home: "https://b.ru/", search: function(){}, title: function(){}, players: function(){}})`,
		"нет версии":       `source({id: "a1", name: "A", hosts: ["a.ru"], home: "https://a.ru/", search: function(){}, title: function(){}, players: function(){}})`,
		"сеть при разборе": `http.text("https://a.ru/"); source({})`,
		"синтаксис":        `source({`,
	}
	for name, src := range cases {
		if _, err := Parse(name+".js", []byte(src)); err == nil {
			t.Errorf("%s: разобрался", name)
		}
	}
}

// fakePlayers — порт чужих плееров для теста: площадка поверх скрипта,
// которая отвечает тайтлом прямо со страницы скрипта.
type fakePlayers struct{ site sitekit.Site }

type fakeSite struct {
	SiteProvider // остальное в тесте не зовётся
	s            sitekit.Site
}

func (f fakeSite) Describe(context.Context) (dp.Descriptor, error) { return dp.Descriptor{}, nil }

func (f fakeSite) Title(ctx context.Context, ref kernel.ProviderRef) (dp.Title, error) {
	pg, err := f.s.Page(ctx, ref.ItemID)
	return dp.Title{Title: pg.Title, SeasonNo: pg.Season, MAL: pg.MAL}, err
}

func (p *fakePlayers) NewSite(_ *http.Client, _ sitekit.SiteOptions, s sitekit.Site) SiteProvider {
	p.site = s
	return fakeSite{s: s}
}

func (*fakePlayers) Sibnet(context.Context, *sitekit.Client, string, string) (sitekit.Stream, error) {
	return sitekit.Stream{}, nil
}

func (*fakePlayers) AniBoom(context.Context, *sitekit.Client, string, string) (sitekit.Stream, error) {
	return sitekit.Stream{}, nil
}

const playersPack = `
  search: function () { return []; },
  title: function (id) { return {id: id, title: "Аниме", season: 1, mal: 1}; },
  players: function (id, ep) { return {kodik: "//kodik.info/serial/1/abc/720p"}; },
`

// Путь players собирается поверх общего кода чужих плееров (порт Players):
// хост отдаёт ему скрипт как sitekit.Site и добавляет своё сверху.
func TestPlayersPathIsSite(t *testing.T) {
	fp := &fakePlayers{}
	SetPlayers(fp)
	t.Cleanup(func() { SetPlayers(nil) })
	srv := site(t)
	defer srv.Close()
	p, err := pack(t, srv, playersPack).New(env(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*siteProvider); !ok {
		t.Fatalf("путь players собран как %T", p)
	}
	var _ appprov.Voices = p.(*siteProvider)
	ti, err := p.Title(context.Background(), kernel.ProviderRef{ProviderID: "fake", ItemID: "5"})
	if err != nil || ti.SeasonNo != 1 || ti.MAL != "1" || fp.site == nil {
		t.Fatalf("%v %+v", err, ti)
	}
	pl, err := fp.site.Players(context.Background(), sitekit.SitePage{ID: "5"}, 1)
	if err != nil || pl.Kodik != "https://kodik.info/serial/1/abc/720p" {
		t.Fatalf("плееры: %+v %v", pl, err)
	}
}

// Без разбора чужих плееров (сборка без площадок) путь players — честная
// ошибка при сборке площадки, а не паника: движок покажет её в статусе,
// сервер упадёт на старте.
func TestPlayersPathWithoutBridge(t *testing.T) {
	SetPlayers(nil)
	srv := site(t)
	defer srv.Close()
	if _, err := pack(t, srv, playersPack).New(env(t)); !errors.Is(err, ErrNoPlayers) {
		t.Fatalf("без моста: %v", err)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"1.0.1", "1.0.0", true}, {"1.0", "1.0.0", false}, {"2.0", "1.9.9", true}, {"1.0.0", "1.0.1", false}} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}
