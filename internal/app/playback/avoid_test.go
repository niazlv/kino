package playback

import (
	"context"
	"testing"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/provider"
)

// Площадка отдаёт сегменты медленнее, чем они играют (kinopub через VPN),
// и витрина просит другую. kinopub не спрашивают вовсе, плееру объяснено
// почему, а в общую память о лежащих он не попадает: у другого человека
// он может идти ровно.
func TestIssue_AvoidedProviderGoesLast(t *testing.T) {
	r := &flakyResolver{}
	svc, h := newFailoverSvc(r)
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12849,
		Choice: catalog.Choice{Source: "kinopub"}, Avoid: []string{"kinopub"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Offer.Ref.ProviderID != "animego_me" {
		t.Fatalf("играет %s", got.Offer.Ref.ProviderID)
	}
	if c := r.called(); len(c) != 1 || c[0] != "animego_me" {
		t.Fatalf("спрашивали %v", c)
	}
	if len(got.Passed) != 1 || got.Passed[0] != (Pass{Provider: "kinopub", Reason: PassSlow}) {
		t.Fatalf("почему не kinopub: %+v", got.Passed)
	}
	if h.Down("kinopub") {
		t.Fatal("мнение одного устройства записано в общую память")
	}
}

// Тормозящая площадка — последняя, но не выброшенная: если больше никто
// серию не отдаёт, играет она.
func TestIssue_AvoidedProviderStillPlaysWhenAlone(t *testing.T) {
	r := &flakyResolver{fail: map[string]error{"animego_me": kernel.ErrNotFound}}
	svc, _ := newFailoverSvc(r)
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12849,
		Avoid: []string{"kinopub"}})
	if err != nil || got.Offer.Ref.ProviderID != "kinopub" {
		t.Fatalf("играет %s, ошибка %v", got.Offer.Ref.ProviderID, err)
	}
	if c := r.called(); len(c) != 2 || c[0] != "animego_me" {
		t.Fatalf("спрашивали %v", c)
	}
}

// Площадку, нажатую руками только что, просьба обойти не отменяет.
func TestIssue_StrictChoiceIgnoresAvoid(t *testing.T) {
	r := &flakyResolver{}
	svc, _ := newFailoverSvc(r)
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12849,
		Choice: catalog.Choice{Source: "kinopub", Strict: true}, Avoid: []string{"kinopub"}})
	if err != nil || got.Offer.Ref.ProviderID != "kinopub" {
		t.Fatalf("строгий выбор подменён: %s, %v", got.Offer.Ref.ProviderID, err)
	}
}

// Та же студия с другой площадки: тормозившая AnimeGO.me уходит в конец,
// AniLibria играет с AnimeGO.fr.
func TestIssue_AvoidKeepsVoice(t *testing.T) {
	r := &voiceResolver{}
	svc, _ := newVoiceSvc(r, psyVoices())
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 3264606,
		Choice: catalog.Choice{Source: "animego_me"}, Voice: "anilibria", Avoid: []string{"animego_me"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Offer.Ref.ProviderID != "animego_fr" || got.Source.Variant != "cvh:AnilibriaTV" {
		t.Fatalf("играет %s/%s, спрашивали %v", got.Offer.Ref.ProviderID, got.Source.Variant, r.asked())
	}
}

// Запомненная студия ищется сначала там, где не тормозит: у kinopub её
// нет, AnimeGO.fr просили обойти — играет AnimeGO.me.
func TestIssue_FollowVoicePrefersNotAvoided(t *testing.T) {
	r := &voiceResolver{audios: []provider.AudioTrack{{Title: "AAC 2.0", Language: "rus"}, {Title: "Orig", Language: "jpn"}}}
	svc, _ := newVoiceSvc(r, psyVoices())
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 3264606,
		Choice: catalog.Choice{Source: "kinopub"}, VariantLabel: "AniLibria", Avoid: []string{"animego_fr"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Offer.Ref.ProviderID != "animego_me" {
		t.Fatalf("играет %s/%s, спрашивали %v", got.Offer.Ref.ProviderID, got.Source.Variant, r.asked())
	}
}
