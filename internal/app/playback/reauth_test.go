package playback

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Токен kinopub протух и не продлился. Серия играет с запасной, а
// плеер говорит «kinopub: нужна переавторизация», а не «не отвечает», —
// и следующий человек kinopub уже не ждёт, причина та же.
func TestIssue_ReauthFallsBackWithReason(t *testing.T) {
	reauth := fmt.Errorf("kinopub: токен отвергнут: %w", kernel.ErrReauth)
	r := &flakyResolver{fail: map[string]error{"kinopub": reauth}}
	svc, h := newFailoverSvc(r)

	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12847})
	if err != nil || got.Offer.Ref.ProviderID != "animego_me" {
		t.Fatalf("играет %s, ошибка %v", got.Offer.Ref.ProviderID, err)
	}
	if len(got.Passed) != 1 || got.Passed[0] != (Pass{Provider: "kinopub", Reason: PassReauth}) {
		t.Fatalf("причина обхода: %+v", got.Passed)
	}
	if !h.NeedsReauth("kinopub") {
		t.Fatal("отказ входа не запомнен")
	}

	r.set(nil, map[string]error{"kinopub": reauth})
	got, err = svc.Issue(context.Background(), Request{User: 1, Episode: 12847})
	if err != nil || got.Offer.Ref.ProviderID != "animego_me" {
		t.Fatalf("играет %s, ошибка %v", got.Offer.Ref.ProviderID, err)
	}
	if calls := r.called(); len(calls) == 0 || calls[0] != "animego_me" {
		t.Fatalf("на ждущую переавторизации площадку снова потратили попытку: %v", calls)
	}
	if len(got.Passed) != 1 || got.Passed[0] != (Pass{Provider: "kinopub", Reason: PassReauth}) {
		t.Fatalf("причина обхода по памяти: %+v", got.Passed)
	}
}

// Когда не отдала ни одна площадка, наружу уходит «недоступно» — транспорт
// ответит 503, а не 500.
func TestIssue_ReauthAloneIsUnavailable(t *testing.T) {
	reauth := fmt.Errorf("kinopub: %w", kernel.ErrReauth)
	r := &flakyResolver{fail: map[string]error{"kinopub": reauth, "animego_me": kernel.ErrNotFound}}
	svc, _ := newFailoverSvc(r)
	_, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12847})
	if err == nil || !isUnavailable(err) {
		t.Fatalf("ошибка %v", err)
	}
}

func isUnavailable(err error) bool {
	return errors.Is(err, kernel.ErrReauth) && errors.Is(err, kernel.ErrUnavailable)
}
