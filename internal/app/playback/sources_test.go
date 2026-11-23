package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/domain/provider"
)

// Серия есть у двух площадок, и у каждой — под своим номером: у kinopub
// это второй сезон, у аниме-площадки — страница второго сезона с сериями
// от единицы.
type offerTitles struct{ fakeTitles }

func (offerTitles) Episode(_ context.Context, id kernel.EpisodeID) (catalog.Episode, error) {
	return catalog.Episode{ID: id, TitleID: 1, SeasonNo: 2, EpisodeNo: 3}, nil
}
func (offerTitles) Offers(context.Context, kernel.EpisodeID) ([]catalog.Offer, error) {
	return []catalog.Offer{
		{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "900"}, SeasonNo: 1, EpisodeNo: 3, Priority: 50},
		{Ref: kernel.ProviderRef{ProviderID: "kinopub", ItemID: "42"}, SeasonNo: 2, EpisodeNo: 3, Priority: 100},
	}, nil
}

type scriptedResolver struct {
	fail  map[string]error
	calls []provider.ResolveRequest
}

func (r *scriptedResolver) Resolve(_ context.Context, req provider.ResolveRequest) (provider.Source, error) {
	r.calls = append(r.calls, req)
	if err := r.fail[req.Ref.ProviderID]; err != nil {
		return provider.Source{}, err
	}
	return provider.Source{
		Protocol: "HLS", URL: "https://cdn.example/" + req.Ref.ProviderID + ".m3u8",
		AllowedOrigins: []string{"cdn.example"}, Passthrough: true,
		ExpiresAt: time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC),
		Egress:    "route-" + req.Ref.ProviderID, Variant: req.Variant,
		DropOutsideOrigins: req.Ref.ProviderID == "animego_me",
	}, nil
}

func newSourcesSvc(r *scriptedResolver) (*Service, fakeTickets) {
	tk := fakeTickets{store: map[kernel.TicketID]playback.Ticket{}}
	return NewService(offerTitles{}, tk, r, fakeSessions{},
		fixedClock{time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)}, fixedIDs{}), tk
}

// Площадку спрашивают её же номерами, а маршрут и фильтр рекламы доезжают
// до тикета — иначе шлюз пошёл бы не тем выходом.
func TestIssue_AsksProviderInItsOwnNumbering(t *testing.T) {
	r := &scriptedResolver{}
	svc, _ := newSourcesSvc(r)
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 100,
		Choice: catalog.Choice{Source: "animego_me"}, Variant: "v7"})
	if err != nil {
		t.Fatal(err)
	}
	c := r.calls[0]
	if c.Ref.ProviderID != "animego_me" || c.Season != 1 || c.Episode != 3 || c.Variant != "v7" {
		t.Fatalf("резолв спрошен так: %+v", c)
	}
	up := got.Ticket.Upstream
	if up.Egress != "route-animego_me" || !up.DropOutsideOrigins {
		t.Fatalf("маршрут или фильтр потерялись: %+v", up)
	}
	if got.Ticket.Choice.Source != "animego_me" || got.Ticket.Choice.Variant != "v7" {
		t.Fatalf("выбор не записан в тикет: %+v", got.Ticket.Choice)
	}
	if len(got.Offers) != 2 {
		t.Fatalf("плееру нужны все площадки серии: %+v", got.Offers)
	}
}

// Запомненной площадки нет серии — играет следующая, без ошибки.
func TestIssue_FallsBackWhenRememberedSourceLacksEpisode(t *testing.T) {
	r := &scriptedResolver{fail: map[string]error{"animego_me": kernel.ErrNotFound}}
	svc, _ := newSourcesSvc(r)
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 100,
		Choice: catalog.Choice{Source: "animego_me"}, Variant: "v7"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Offer.Ref.ProviderID != "kinopub" || len(r.calls) != 2 {
		t.Fatalf("запасная площадка не сработала: %+v", r.calls)
	}
	// Номер варианта чужой площадки kinopub не понять — он не передаётся.
	if r.calls[1].Variant != "" || r.calls[1].Season != 2 {
		t.Fatalf("kinopub спрошен так: %+v", r.calls[1])
	}
}

// Руками выбранная площадка не подменяется молча.
func TestIssue_StrictChoiceReportsFailure(t *testing.T) {
	r := &scriptedResolver{fail: map[string]error{"animego_me": kernel.ErrUnavailable}}
	svc, _ := newSourcesSvc(r)
	_, err := svc.Issue(context.Background(), Request{User: 1, Episode: 100,
		Choice: catalog.Choice{Source: "animego_me", Strict: true}})
	if !errors.Is(err, kernel.ErrUnavailable) || len(r.calls) != 1 {
		t.Fatalf("ошибка %v, вызовы %+v", err, r.calls)
	}
}

// Обновление протухшей ссылки сохраняет площадку и озвучку.
func TestRefresh_KeepsSourceAndVariant(t *testing.T) {
	r := &scriptedResolver{}
	svc, _ := newSourcesSvc(r)
	first, err := svc.Issue(context.Background(), Request{User: 1, Episode: 100,
		Choice: catalog.Choice{Source: "animego_me"}, Variant: "v7"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RefreshTicket(context.Background(), first.Ticket.ID); err != nil {
		t.Fatal(err)
	}
	last := r.calls[len(r.calls)-1]
	if last.Ref.ProviderID != "animego_me" || last.Variant != "v7" {
		t.Fatalf("обновление ушло не туда: %+v", last)
	}
}
