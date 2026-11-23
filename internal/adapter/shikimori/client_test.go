package shikimori

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Контракт с реестром проверяется на заглушке: разбор ответа, приведение
// вида к термины платформы и год из даты выхода.
func TestSearch_ParsesRegistryAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Fatal("без подписи приложения реестр ограничивает скорость")
		}
		if got := r.URL.Query().Get("search"); got != "юки юна" {
			t.Fatalf("search = %q", got)
		}
		_, _ = w.Write([]byte(`[
			{"id":25537,"name":"Yuuki Yuuna wa Yuusha de Aru","russian":"Герой Юна Юки","kind":"tv","aired_on":"2014-10-16"},
			{"id":33895,"name":"Washio Sumi no Shou","russian":"","kind":"movie","year":2017},
			{"id":1,"name":"Neko Ramen","kind":"music"},
			{"id":2,"name":"No Year","kind":"tv"}
		]`))
	}))
	defer srv.Close()

	c := New(srv.Client(), srv.URL)
	cands, err := c.Search(context.Background(), "юки юна")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("кандидатов %d, want 3 (музыка отсеивается)", len(cands))
	}
	if cands[0].ID != "25537" || cands[0].Kind != "series" || cands[0].Year != 2014 {
		t.Fatalf("сериал разобран неверно: %+v", cands[0])
	}
	if len(cands[0].Names) != 2 {
		t.Fatalf("названия: %+v", cands[0].Names)
	}
	if cands[1].Kind != "movie" || cands[1].Year != 2017 {
		t.Fatalf("фильм разобран неверно: %+v", cands[1])
	}
	if cands[2].Year != 0 {
		t.Fatalf("кандидат без года должен остаться без года: %+v", cands[2])
	}
}
