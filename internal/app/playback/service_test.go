package playback

import (
	"context"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/domain/provider"
)

// Смысл слоя app в одной строке: сервис поднимается фейками на 30 строк.
// Ни Postgres, ни Redis, ни gRPC, ни docker-compose для запуска теста.

type fakeTitles struct{}

func (fakeTitles) Episode(_ context.Context, id kernel.EpisodeID) (catalog.Episode, error) {
	return catalog.Episode{ID: id, TitleID: 1, SeasonNo: 1, EpisodeNo: 3, DurationSec: 2700}, nil
}
func (fakeTitles) Offers(context.Context, kernel.EpisodeID) ([]catalog.Offer, error) {
	return nil, nil
}
func (fakeTitles) Title(_ context.Context, id kernel.TitleID) (catalog.Title, error) {
	return catalog.Title{ID: id, Sources: []catalog.Source{
		{Ref: kernel.ProviderRef{ProviderID: "kinopub", ItemID: "42"}, Priority: 10},
		{Ref: kernel.ProviderRef{ProviderID: "other", ItemID: "7"}, Priority: 1},
	}}, nil
}

type fakeProgress struct {
	store map[kernel.EpisodeID]playback.Progress
}

func (f fakeProgress) Get(_ context.Context, _ kernel.UserID, e kernel.EpisodeID) (playback.Progress, error) {
	p, ok := f.store[e]
	if !ok {
		return playback.Progress{}, kernel.ErrNotFound
	}
	return p, nil
}
func (f fakeProgress) Upsert(_ context.Context, p playback.Progress) error {
	f.store[p.EpisodeID] = p
	return nil
}

type fakeTickets struct {
	store map[kernel.TicketID]playback.Ticket
}

func (f fakeTickets) Put(_ context.Context, t playback.Ticket, _ time.Duration) error {
	f.store[t.ID] = t
	return nil
}
func (f fakeTickets) Get(_ context.Context, id kernel.TicketID) (playback.Ticket, error) {
	t, ok := f.store[id]
	if !ok {
		return playback.Ticket{}, kernel.ErrNotFound
	}
	return t, nil
}

type fakeResolver struct {
	gotRef      kernel.ProviderRef
	passthrough bool
	subtitles   []provider.TextTrack
}

func (f *fakeResolver) Resolve(_ context.Context, req provider.ResolveRequest) (provider.Source, error) {
	f.gotRef = req.Ref
	return provider.Source{
		Protocol:       "HLS",
		URL:            "https://cdn7.example/master.m3u8?sig=abc",
		Headers:        map[string]string{"User-Agent": "kino/1"},
		AllowedOrigins: []string{"cdn7.example"},
		ExpiresAt:      time.Date(2026, 9, 4, 12, 30, 0, 0, time.UTC),
		Passthrough:    f.passthrough,
		Subtitles:      f.subtitles,
	}, nil
}

type fakeSessions struct{}

func (fakeSessions) Open(context.Context, playback.Session) error  { return nil }
func (fakeSessions) Close(context.Context, playback.Session) error { return nil }

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type fixedIDs struct{}

func (fixedIDs) NewTicketID() kernel.TicketID { return "tkt_test" }

func newSvc(passthrough bool) (*Service, *fakeResolver, fakeTickets) {
	r := &fakeResolver{passthrough: passthrough}
	tk := fakeTickets{store: map[kernel.TicketID]playback.Ticket{}}
	s := NewService(
		fakeTitles{},
		tk, r, fakeSessions{},
		fixedClock{time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)},
		fixedIDs{},
	)
	return s, r, tk
}

func TestIssueTicket_PicksHighestPrioritySource(t *testing.T) {
	svc, res, _ := newSvc(true)
	if _, _, err := svc.IssueTicket(context.Background(), 1, 100, provider.ClientProfile{MaxHeight: 1080}); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if res.gotRef.ProviderID != "kinopub" {
		t.Fatalf("got provider %q, want kinopub (наибольший приоритет)", res.gotRef.ProviderID)
	}
}

func TestIssueTicket_NonPassthroughFallsBackToLocal(t *testing.T) {
	svc, _, _ := newSvc(false)
	got, _, err := svc.IssueTicket(context.Background(), 1, 100, provider.ClientProfile{})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Источник, не укладывающийся в generic-перенос, уходит в локальную
	// доставку — вместо протечки его особенностей в gateway.
	if got.Mode != playback.DeliveryLocal {
		t.Fatalf("mode = %q, want %q", got.Mode, playback.DeliveryLocal)
	}
}

func TestIssueTicket_AlwaysCarriesOriginAllowlist(t *testing.T) {
	svc, _, _ := newSvc(true)
	got, _, _ := svc.IssueTicket(context.Background(), 1, 100, provider.ClientProfile{})
	// Инвариант против SSRF и против протечки списка CDN в конфиг gateway.
	if len(got.Upstream.AllowedOrigins) == 0 {
		t.Fatal("тикет без allowed_origins не должен выпускаться")
	}
	if got.Upstream.OriginAllowed("evil.example") {
		t.Fatal("посторонний хост прошёл проверку")
	}
}

func TestRefreshTicket_KeepsSameID(t *testing.T) {
	svc, _, _ := newSvc(true)
	orig, _, _ := svc.IssueTicket(context.Background(), 1, 100, provider.ClientProfile{})
	fresh, err := svc.RefreshTicket(context.Background(), orig.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// Тот же ID — иначе клиенту пришлось бы переоткрывать поток.
	if fresh.ID != orig.ID {
		t.Fatalf("id changed: %q -> %q", orig.ID, fresh.ID)
	}
}

func TestSegmentProgressOnlyForExternalPlayerAndSurvivesRefresh(t *testing.T) {
	for _, external := range []bool{false, true} {
		svc, _, tickets := newSvc(true)
		got, err := svc.Issue(context.Background(), Request{
			User: 1, Episode: 100, TrackProgressFromSegments: external,
		})
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := svc.RefreshTicket(context.Background(), got.Ticket.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Ticket.TrackProgressFromSegments != external || fresh.TrackProgressFromSegments != external ||
			tickets.store[fresh.ID].TrackProgressFromSegments != external {
			t.Fatalf("external=%v lost during issue/refresh", external)
		}
	}
}

// Субтитры отдельными файлами едут в тикет, и только они: дорожки потока шлюз
// и так отдаёт через плейлист. Порядок — тот же, что у Sidecars: по нему
// клиент просит файл у шлюза.
func TestIssueTicket_CarriesSidecarSubtitles(t *testing.T) {
	svc, res, _ := newSvc(true)
	res.subtitles = []provider.TextTrack{
		{ID: "0", URL: "https://cdn7.example/in-stream.srt"},
		{ID: "s0", URL: "https://cdn7.example/sub_rus.vtt", Sidecar: true},
		{ID: "s1", URL: "https://cdn7.example/sub_eng.vtt", Sidecar: true},
	}
	got, src, err := svc.IssueTicket(context.Background(), 1, 100, provider.ClientProfile{})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	want := []string{"https://cdn7.example/sub_rus.vtt", "https://cdn7.example/sub_eng.vtt"}
	if len(got.Upstream.Subtitles) != 2 || got.Upstream.Subtitles[0] != want[0] || got.Upstream.Subtitles[1] != want[1] {
		t.Fatalf("субтитры в тикете: %v", got.Upstream.Subtitles)
	}
	if side := src.Sidecars(); len(side) != 2 || side[0].ID != "s0" {
		t.Fatalf("Sidecars: %+v", side)
	}
	// Обновление протухшей ссылки кладёт свежие файлы под теми же номерами.
	fresh, err := svc.RefreshTicket(context.Background(), got.ID)
	if err != nil || len(fresh.Upstream.Subtitles) != 2 {
		t.Fatalf("после обновления: %v %v", err, fresh.Upstream.Subtitles)
	}
}
