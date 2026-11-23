package skips

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

type fakeOffers []catalog.Offer

func (f fakeOffers) Offers(context.Context, kernel.EpisodeID) ([]catalog.Offer, error) {
	return f, nil
}

type fakeCommunity struct {
	mu    sync.Mutex
	calls map[string]int
	segs  map[string][]dp.Segment
	err   error
}

func (f *fakeCommunity) Segments(_ context.Context, mal string, ep int) ([]dp.Segment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := mal + "/" + strconv.Itoa(ep)
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[key]++
	if f.err != nil {
		return nil, f.err
	}
	s, ok := f.segs[key]
	if !ok {
		return nil, kernel.ErrNotFound
	}
	return s, nil
}

var offers = fakeOffers{
	{Ref: kernel.ProviderRef{ProviderID: "kinopub", ItemID: "1"}, SeasonNo: 1, EpisodeNo: 2},
	{Ref: kernel.ProviderRef{ProviderID: "animego_fr", ItemID: "653"}, SeasonNo: 1, EpisodeNo: 2, MAL: "52991"},
	{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "x"}, SeasonNo: 1, EpisodeNo: 2, MAL: "52991"},
}

const len24 = 1_440_000

func TestEpisode_MergesProviderAndCommunity(t *testing.T) {
	var gotReq dp.ResolveRequest
	prov := func(_ context.Context, r dp.ResolveRequest) ([]dp.Segment, error) {
		gotReq = r
		return []dp.Segment{{Kind: dp.SegSkip, StartMS: 1_375_000, EndMS: 1_445_000},
			{Kind: dp.SegSkip, StartMS: 0, EndMS: 110_000}}, nil
	}
	com := &fakeCommunity{segs: map[string][]dp.Segment{"52991/2": {
		{Kind: dp.SegIntro, StartMS: 6_576, EndMS: 96_576, ForDurationMS: len24},
		// Эндинг в начале серии — чужая ошибка разметки, отсеивается.
		{Kind: dp.SegOutro, StartMS: 7_000, EndMS: 97_000, ForDurationMS: len24},
	}}}
	s := New(offers, prov, com, nil)

	got, err := s.Episode(context.Background(), Request{Episode: 1, Source: "animego_fr", Variant: "kodik:610"})
	if err != nil {
		t.Fatal(err)
	}
	if gotReq.Ref.ItemID != "653" || gotReq.Episode != 2 || gotReq.Variant != "kodik:610" {
		t.Fatalf("запрос к площадке: %+v", gotReq)
	}
	want := []Mark{
		{Segment: dp.Segment{Kind: dp.SegSkip, StartMS: 0, EndMS: 110_000}, Origin: "animego_fr"},
		{Segment: dp.Segment{Kind: dp.SegSkip, StartMS: 1_375_000, EndMS: 1_445_000}, Origin: "animego_fr"},
		{Segment: dp.Segment{Kind: dp.SegIntro, StartMS: 6_576, EndMS: 96_576, ForDurationMS: len24}, Origin: OriginCommunity},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}
	// Один номер MAL у двух площадок — один запрос.
	if com.calls["52991/2"] != 1 {
		t.Fatalf("AniSkip спрошен %d раз", com.calls["52991/2"])
	}
}

func TestEpisode_CachesAndForgetsFailures(t *testing.T) {
	com := &fakeCommunity{err: kernel.ErrUnreachable}
	s := New(offers, nil, com, nil)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	if got, err := s.Episode(ctx, Request{Episode: 1}); err != nil || len(got) != 0 {
		t.Fatalf("отказ AniSkip — не ошибка: %v, %v", got, err)
	}
	if _, err := s.Episode(ctx, Request{Episode: 1}); err != nil {
		t.Fatal(err)
	}
	if com.calls["52991/2"] != 1 {
		t.Fatalf("отказ не запомнен: %d запросов", com.calls["52991/2"])
	}

	// Через несколько минут — снова спрашиваем; ненайденное помним дольше.
	now = now.Add(failTTL + time.Second)
	com.err = nil
	if _, err := s.Episode(ctx, Request{Episode: 1}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if _, err := s.Episode(ctx, Request{Episode: 1}); err != nil {
		t.Fatal(err)
	}
	if com.calls["52991/2"] != 2 {
		t.Fatalf("запросов %d, want 2", com.calls["52991/2"])
	}
}

// Площадку без варианта не спрашиваем: разметка привязана к потоку.
func TestEpisode_ProviderNeedsVariant(t *testing.T) {
	called := false
	prov := func(context.Context, dp.ResolveRequest) ([]dp.Segment, error) {
		called = true
		return nil, errors.New("не должно звучать")
	}
	s := New(offers, prov, nil, nil)
	if _, err := s.Episode(context.Background(), Request{Episode: 1, Source: "animego_fr"}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("площадку спросили без варианта")
	}
}

func TestPlausible(t *testing.T) {
	cases := []struct {
		s    dp.Segment
		want bool
	}{
		{dp.Segment{Kind: dp.SegIntro, StartMS: 0, EndMS: 90_000, ForDurationMS: len24}, true},
		{dp.Segment{Kind: dp.SegIntro, StartMS: 0, EndMS: 5_000, ForDurationMS: len24}, false},         // слишком короткий
		{dp.Segment{Kind: dp.SegIntro, StartMS: 0, EndMS: 400_000, ForDurationMS: len24}, false},       // полсерии
		{dp.Segment{Kind: dp.SegIntro, StartMS: 900_000, EndMS: 990_000, ForDurationMS: len24}, false}, // во второй половине
		{dp.Segment{Kind: dp.SegOutro, StartMS: 1_350_000, EndMS: 1_440_000, ForDurationMS: len24}, true},
		{dp.Segment{Kind: dp.SegOutro, StartMS: 7_000, EndMS: 97_000, ForDurationMS: len24}, false},
		{dp.Segment{Kind: dp.SegRecap, StartMS: 0, EndMS: 60_000, ForDurationMS: len24}, true},
		{dp.Segment{Kind: dp.SegIntro, StartMS: 0, EndMS: 90_000}, false}, // без длины файла
	}
	for i, c := range cases {
		if got := plausible(c.s); got != c.want {
			t.Errorf("[%d] plausible(%+v) = %v", i, c.s, got)
		}
	}
}
