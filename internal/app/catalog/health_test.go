package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/app/health"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

type unreachableSource struct{ calls *[]string }

func (u unreachableSource) Episodes(_ context.Context, ref kernel.ProviderRef) ([]dp.Episode, error) {
	*u.calls = append(*u.calls, ref.ProviderID)
	return nil, kernel.ErrUnreachable
}

// Площадка, промолчавшая странице тайтла, запоминается лежащей — и
// следующая страница (и плеер) её уже не ждут.
func TestLazyEpisodes_UnreachableSourceRememberedAndSkipped(t *testing.T) {
	kp := kernel.ProviderRef{ProviderID: "kinopub", ItemID: "124789"}
	r := &episodesReader{title: catalog.Title{ID: 434, Sources: []catalog.Source{{Ref: kp, Priority: 100}}}}
	h := health.New(nil)
	var calls []string
	src := func(string) (EpisodeSource, error) { return unreachableSource{&calls}, nil }

	if _, err := NewLazyCatalog(r, &recordingWriter{}, src).WithHealth(h).Episodes(context.Background(), 434); err != nil {
		t.Fatal(err)
	}
	if !h.Down("kinopub") {
		t.Fatal("молчание площадки не запомнено")
	}
	// Новый каталог — чтобы не сработала память «спрашивали впустую»:
	// проверяется именно обход лежащей.
	calls = nil
	start := time.Now()
	if _, err := NewLazyCatalog(r, &recordingWriter{}, src).WithHealth(h).Episodes(context.Background(), 434); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 || time.Since(start) > time.Second {
		t.Fatalf("лежащую площадку снова спросили: %v", calls)
	}
}
