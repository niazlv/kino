package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/app/health"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/domain/provider"
)

// Серия у kinopub (главный) и у AnimeGO (запасной): главный за мёртвым
// маршрутом не отвечает, и плеер не должен ждать его до таймаута HTTP.
type failoverTitles struct{ fakeTitles }

func (failoverTitles) Episode(_ context.Context, id kernel.EpisodeID) (catalog.Episode, error) {
	return catalog.Episode{ID: id, TitleID: 1, SeasonNo: 1, EpisodeNo: 1}, nil
}
func (failoverTitles) Offers(context.Context, kernel.EpisodeID) ([]catalog.Offer, error) {
	return []catalog.Offer{
		{Ref: kernel.ProviderRef{ProviderID: "kinopub", ItemID: "124789"}, SeasonNo: 1, EpisodeNo: 1, Priority: 100},
		{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "tabakoshka-3442"}, SeasonNo: 1, EpisodeNo: 1, Priority: 60},
	}, nil
}

// flakyResolver: площадки из hang висят до отмены, из fail отказывают
// сразу. Потокобезопасен — фоновая перепроверка зовёт его из горутины.
type flakyResolver struct {
	mu    sync.Mutex
	hang  map[string]bool
	fail  map[string]error
	calls []string
	probe chan string
}

func (r *flakyResolver) Resolve(ctx context.Context, req provider.ResolveRequest) (provider.Source, error) {
	id := req.Ref.ProviderID
	r.mu.Lock()
	r.calls = append(r.calls, id)
	hang, fail := r.hang[id], r.fail[id]
	r.mu.Unlock()
	defer func() {
		if r.probe != nil {
			select {
			case r.probe <- id:
			default:
			}
		}
	}()
	if hang {
		<-ctx.Done()
		return provider.Source{}, ctx.Err()
	}
	if fail != nil {
		return provider.Source{}, fail
	}
	return provider.Source{
		Protocol: "HLS", URL: "https://cdn.example/" + id + ".m3u8",
		AllowedOrigins: []string{"cdn.example"}, Passthrough: true,
		ExpiresAt: time.Date(2026, 9, 27, 7, 0, 0, 0, time.UTC),
	}, nil
}

func (r *flakyResolver) set(hang map[string]bool, fail map[string]error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hang, r.fail, r.calls = hang, fail, nil
}

func (r *flakyResolver) called() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func newFailoverSvc(r *flakyResolver) (*Service, *health.Tracker) {
	tk := fakeTickets{store: map[kernel.TicketID]playback.Ticket{}}
	h := health.New(nil)
	svc := NewService(failoverTitles{}, tk, r, fakeSessions{},
		fixedClock{time.Date(2026, 9, 26, 7, 25, 0, 0, time.UTC)}, fixedIDs{}).WithHealth(h)
	svc.attempt, svc.lastAttempt, svc.budget, svc.probe =
		50*time.Millisecond, 100*time.Millisecond, 300*time.Millisecond, 100*time.Millisecond
	return svc, h
}

// Главная площадка висит — через срок попытки играет запасная, а не
// крутилка на две минуты. И площадка запоминается лежащей.
func TestIssue_HangingProviderFallsBackAfterAttemptTimeout(t *testing.T) {
	r := &flakyResolver{hang: map[string]bool{"kinopub": true}}
	svc, h := newFailoverSvc(r)
	start := time.Now()
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12847})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Fatalf("ждали %v — дольше срока попытки", took)
	}
	if got.Offer.Ref.ProviderID != "animego_me" {
		t.Fatalf("играет %s", got.Offer.Ref.ProviderID)
	}
	if len(got.Passed) != 1 || got.Passed[0] != (Pass{Provider: "kinopub", Reason: PassFailed}) {
		t.Fatalf("плееру не объяснили, почему не kinopub: %+v", got.Passed)
	}
	if !h.Down("kinopub") {
		t.Fatal("зависшая площадка не запомнена лежащей")
	}
}

// Быстрый сетевой отказ — тоже повод взять запасную, а не
// «Internal Server Error».
func TestIssue_UnreachableFallsBack(t *testing.T) {
	r := &flakyResolver{fail: map[string]error{"kinopub": kernel.ErrUnreachable}}
	svc, h := newFailoverSvc(r)
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12847})
	if err != nil || got.Offer.Ref.ProviderID != "animego_me" {
		t.Fatalf("играет %s, ошибка %v", got.Offer.Ref.ProviderID, err)
	}
	if !h.Down("kinopub") {
		t.Fatal("недоступная площадка не запомнена лежащей")
	}
}

// Лежащую площадку следующий человек не ждёт: сразу запасная, а лежащую
// перепроверяют в фоне — и ответ возвращает её в строй.
func TestIssue_DownProviderSkippedThenProbedBack(t *testing.T) {
	r := &flakyResolver{hang: map[string]bool{"kinopub": true}, probe: make(chan string, 4)}
	svc, h := newFailoverSvc(r)
	if _, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12847}); err != nil {
		t.Fatal(err)
	}
	drain(r.probe)

	// kinopub ожил, но память ещё говорит «лежит».
	r.set(nil, nil)
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12847,
		Choice: catalog.Choice{Source: "kinopub"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Offer.Ref.ProviderID != "animego_me" {
		t.Fatalf("на лежащую площадку снова потратили попытку: играет %s", got.Offer.Ref.ProviderID)
	}
	if len(got.Passed) != 1 || got.Passed[0] != (Pass{Provider: "kinopub", Reason: PassDown}) {
		t.Fatalf("обход не объяснён: %+v", got.Passed)
	}
	// Фоновая перепроверка: сначала выдача (animego_me), затем kinopub.
	waitFor(t, r.probe, "animego_me")
	waitFor(t, r.probe, "kinopub")
	deadline := time.Now().Add(time.Second)
	for h.Down("kinopub") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if h.Down("kinopub") {
		t.Fatal("перепроверка не вернула площадку в строй")
	}
	got, err = svc.Issue(context.Background(), Request{User: 1, Episode: 12847})
	if err != nil || got.Offer.Ref.ProviderID != "kinopub" {
		t.Fatalf("вернувшаяся площадка не первая: %s, %v", got.Offer.Ref.ProviderID, err)
	}
}

// Руками выбранную только что площадку не обходят, даже лежащую, — но и
// ждут её не бесконечно.
func TestIssue_StrictDownProviderStillAskedButBounded(t *testing.T) {
	r := &flakyResolver{hang: map[string]bool{"kinopub": true}}
	svc, h := newFailoverSvc(r)
	h.Failed("kinopub", errors.New("раньше не ответила"))
	start := time.Now()
	_, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12847,
		Choice: catalog.Choice{Source: "kinopub", Strict: true}})
	if !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("ошибка %v — транспорт не скажет «площадка недоступна»", err)
	}
	if took := time.Since(start); took > 250*time.Millisecond {
		t.Fatalf("ждали %v — дольше срока последней попытки", took)
	}
	if c := r.called(); len(c) != 1 || c[0] != "kinopub" {
		t.Fatalf("строгий выбор подменён: %v", c)
	}
}

// Не отдал никто: «kinopub лежит, у AnimeGO серии нет» — это «недоступна,
// попробуйте позже», а не «не найдено».
func TestIssue_AllFailedReportsUnavailableOverNotFound(t *testing.T) {
	r := &flakyResolver{fail: map[string]error{
		"kinopub": kernel.ErrUnreachable, "animego_me": kernel.ErrNotFound}}
	svc, _ := newFailoverSvc(r)
	_, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12847})
	if !errors.Is(err, kernel.ErrUnavailable) || errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("ошибка %v", err)
	}
}

// Человек ушёл — площадку не винят и следующую не спрашивают.
func TestIssue_CallerCancelIsNotProviderFault(t *testing.T) {
	r := &flakyResolver{hang: map[string]bool{"kinopub": true}}
	svc, h := newFailoverSvc(r)
	svc.attempt = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := svc.Issue(ctx, Request{User: 1, Episode: 12847})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка %v", err)
	}
	if h.Down("kinopub") || len(r.called()) != 1 {
		t.Fatalf("отмена человеком засчитана площадке: лежит=%v, вызовы %v", h.Down("kinopub"), r.called())
	}
}

// Весь выбор не дольше потолка, сколько бы площадок ни висело.
func TestIssue_BudgetCapsWholeSelection(t *testing.T) {
	r := &flakyResolver{hang: map[string]bool{"kinopub": true, "animego_me": true}}
	svc, _ := newFailoverSvc(r)
	svc.attempt, svc.lastAttempt, svc.budget = 80*time.Millisecond, time.Second, 120*time.Millisecond
	start := time.Now()
	_, err := svc.Issue(context.Background(), Request{User: 1, Episode: 12847})
	if !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("ошибка %v", err)
	}
	if took := time.Since(start); took > 250*time.Millisecond {
		t.Fatalf("ждали %v при потолке 120 мс", took)
	}
}

func drain(ch chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func waitFor(t *testing.T, ch chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("ждали вызов %s, пришёл %s", want, got)
		}
	case <-time.After(time.Second):
		t.Fatalf("вызова %s не было", want)
	}
}
