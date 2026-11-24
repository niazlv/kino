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

// Серия «Великого из бродячих псов» 3×1 так, как её видели площадки
// 03.10.2026:
// у kinopub безымянная русская дорожка и оригинал, у AnimeGO.fr — CVH и
// Kodik, у AnimeGO.me — Kodik и Sibnet.
type voiceTitles struct{ fakeTitles }

func (voiceTitles) Episode(_ context.Context, id kernel.EpisodeID) (catalog.Episode, error) {
	return catalog.Episode{ID: id, TitleID: 1, SeasonNo: 3, EpisodeNo: 1}, nil
}
func (voiceTitles) Offers(context.Context, kernel.EpisodeID) ([]catalog.Offer, error) {
	return []catalog.Offer{
		{Ref: kernel.ProviderRef{ProviderID: "animego_me", ItemID: "psy-3-924"}, SeasonNo: 1, EpisodeNo: 1, Priority: 60},
		{Ref: kernel.ProviderRef{ProviderID: "kinopub", ItemID: "16739"}, SeasonNo: 3, EpisodeNo: 1, Priority: 100},
		{Ref: kernel.ProviderRef{ProviderID: "animego_fr", ItemID: "39"}, SeasonNo: 1, EpisodeNo: 1, Priority: 80},
	}, nil
}

type fakeVoices struct {
	mu    sync.Mutex
	by    map[string][]provider.Voice
	fail  map[string]error
	calls int
}

func (f *fakeVoices) Voices(_ context.Context, ref kernel.ProviderRef, _, _ int) ([]provider.Voice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := f.fail[ref.ProviderID]; err != nil {
		return nil, err
	}
	return f.by[ref.ProviderID], nil
}

func psyVoices() *fakeVoices {
	return &fakeVoices{by: map[string][]provider.Voice{
		"kinopub": {{Title: "Русская дорожка"}, {Title: "Оригинал"}},
		"animego_fr": {
			{Title: "Amazing Dubbing", Variant: "cvh:Amazing Dubbing", Player: "CVH"},
			{Title: "AnilibriaTV", Variant: "cvh:AnilibriaTV", Player: "CVH"},
			{Title: "Субтитры", Variant: "kodik:9", Player: "Kodik"},
		},
		"animego_me": {
			{Title: "AniLibria", Variant: "2:19", Player: "Kodik"},
			{Title: "AniLibria", Variant: "2:1", Player: "Sibnet"},
			{Title: "AniDUB", Variant: "1:19", Player: "Kodik"},
		},
	}}
}

// voiceResolver: что площадка отдаёт и что у неё спросили.
type voiceResolver struct {
	mu   sync.Mutex
	fail map[string]error // по «площадка» или «площадка/вариант»
	got  []provider.ResolveRequest
	// audios — дорожки потока kinopub, как их называет его API.
	audios []provider.AudioTrack
}

func (r *voiceResolver) Resolve(_ context.Context, req provider.ResolveRequest) (provider.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, req)
	id := req.Ref.ProviderID
	if err := r.fail[id]; err != nil {
		return provider.Source{}, err
	}
	if err := r.fail[id+"/"+req.Variant]; err != nil {
		return provider.Source{}, err
	}
	src := provider.Source{Protocol: "HLS", URL: "https://cdn.example/" + id + ".m3u8",
		AllowedOrigins: []string{"cdn.example"}, Passthrough: true,
		ExpiresAt: time.Date(2026, 10, 3, 23, 0, 0, 0, time.UTC)}
	if id == "kinopub" {
		src.Audios = r.audios
		return src, nil
	}
	// Аниме-площадка: без номера варианта — первая озвучка списка.
	vs := psyVoices().by[id]
	src.Variant = vs[0].Variant
	for _, v := range vs {
		if v.Variant == req.Variant || req.Variant == "" && provider.VoiceKey(v.Title) == provider.VoiceKey(req.VariantLabel) {
			src.Variant = v.Variant
			break
		}
	}
	for _, v := range vs {
		src.Variants = append(src.Variants, provider.Variant{ID: v.Variant, Title: v.Title, Player: v.Player})
	}
	return src, nil
}

func (r *voiceResolver) asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, q := range r.got {
		out = append(out, q.Ref.ProviderID+"/"+q.Variant)
	}
	return out
}

func newVoiceSvc(r *voiceResolver, v *fakeVoices) (*Service, *health.Tracker) {
	tk := fakeTickets{store: map[kernel.TicketID]playback.Ticket{}}
	h := health.New(nil)
	svc := NewService(voiceTitles{}, tk, r, fakeSessions{},
		fixedClock{time.Date(2026, 10, 3, 19, 26, 0, 0, time.UTC)}, fixedIDs{}).WithHealth(h).WithVoices(v)
	svc.attempt, svc.lastAttempt, svc.budget, svc.probe =
		50*time.Millisecond, 100*time.Millisecond, 300*time.Millisecond, 100*time.Millisecond
	return svc, h
}

func titles(l VoiceList) []string {
	var out []string
	for _, v := range l.Voices {
		out = append(out, v.Title)
	}
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Студия — один пункт, сколько бы площадок и плееров её ни отдавали;
// пути к ней — по приоритету площадок. Порядок: озвучки (чем больше
// площадок, тем выше), оригинал, субтитры.
func TestVoices_MergedAcrossProviders(t *testing.T) {
	v := psyVoices()
	svc, _ := newVoiceSvc(&voiceResolver{}, v)
	l, err := svc.Voices(context.Background(), 3264606)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"AniLibria", "Русская дорожка", "Amazing Dubbing", "AniDUB", "Оригинал", "Субтитры"}
	if !same(titles(l), want) {
		t.Fatalf("список: %v, ждали %v", titles(l), want)
	}
	lib, _ := l.Find("AniLibria")
	var ways []string
	for _, w := range lib.Ways {
		ways = append(ways, w.Offer.Ref.ProviderID+"/"+w.Variant)
	}
	if !same(ways, []string{"animego_fr/cvh:AnilibriaTV", "animego_me/2:19", "animego_me/2:1"}) {
		t.Fatalf("пути к AniLibria: %v", ways)
	}
	if len(l.Silent) != 0 {
		t.Fatalf("молчавшие: %+v", l.Silent)
	}
	// Второй раз — из памяти, площадки не дёргаются.
	calls := v.calls
	if _, err := svc.Voices(context.Background(), 3264606); err != nil || v.calls != calls {
		t.Fatalf("список не запомнен: %d → %d, %v", calls, v.calls, err)
	}
}

// growingTitles — серия, к которой по ходу привязывается новая площадка.
type growingTitles struct {
	voiceTitles
	mu     sync.Mutex
	offers []catalog.Offer
}

func (g *growingTitles) Offers(context.Context, kernel.EpisodeID) ([]catalog.Offer, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]catalog.Offer(nil), g.offers...), nil
}

// Привязалась новая площадка («Обновить», досбор) — её озвучки видны сразу, а не
// через срок памяти списка; пока площадки серии те же, список берётся из памяти.
func TestVoices_NewOfferRebuildsCachedList(t *testing.T) {
	v := psyVoices()
	all, _ := voiceTitles{}.Offers(context.Background(), 0)
	g := &growingTitles{offers: all[1:]} // kinopub и animego_fr, без animego_me
	tk := fakeTickets{store: map[kernel.TicketID]playback.Ticket{}}
	svc := NewService(g, tk, &voiceResolver{}, fakeSessions{},
		fixedClock{time.Date(2026, 10, 3, 19, 26, 0, 0, time.UTC)}, fixedIDs{}).WithVoices(v)
	svc.attempt = 50 * time.Millisecond

	l, err := svc.Voices(context.Background(), 3264606)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.Find("AniDUB"); ok {
		t.Fatal("AniDUB — только у animego_me, которой у серии ещё нет")
	}
	calls := v.calls
	if _, err := svc.Voices(context.Background(), 3264606); err != nil || v.calls != calls {
		t.Fatalf("те же площадки — список не из памяти: %d → %d", calls, v.calls)
	}

	g.mu.Lock()
	g.offers = all
	g.mu.Unlock()
	l, err = svc.Voices(context.Background(), 3264606)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.Find("AniDUB"); !ok {
		t.Fatalf("новая площадка не попала в список: %v", titles(l))
	}
}

// Лежащая площадка не спрашивается, не ответившая — называется: список
// неполон, и человек должен это видеть.
func TestVoices_SilentProviders(t *testing.T) {
	v := psyVoices()
	v.fail = map[string]error{"animego_fr": kernel.ErrUnreachable}
	svc, h := newVoiceSvc(&voiceResolver{}, v)
	h.Failed("kinopub", kernel.ErrUnreachable)
	l, err := svc.Voices(context.Background(), 3264606)
	if err != nil {
		t.Fatal(err)
	}
	if !same(titles(l), []string{"AniLibria", "AniDUB"}) {
		t.Fatalf("список: %v", titles(l))
	}
	if len(l.Silent) != 2 || l.Silent[0] != (Pass{Provider: "kinopub", Reason: PassDown}) ||
		l.Silent[1] != (Pass{Provider: "animego_fr", Reason: PassFailed}) {
		t.Fatalf("молчавшие: %+v", l.Silent)
	}
}

// Выбор студии: сначала площадка, с которой смотрят, дальше — где ещё она
// есть. Нет её на той площадке — играет с другой, а не другая студия.
func TestIssue_VoiceFollowsWays(t *testing.T) {
	r := &voiceResolver{fail: map[string]error{"animego_me/2:19": kernel.ErrNotFound}}
	svc, _ := newVoiceSvc(r, psyVoices())
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 3264606,
		Choice: catalog.Choice{Source: "animego_me"}, Voice: "anilibria"})
	if err != nil {
		t.Fatal(err)
	}
	if !same(r.asked(), []string{"animego_me/2:19", "animego_me/2:1"}) {
		t.Fatalf("спрашивали: %v", r.asked())
	}
	if got.Offer.Ref.ProviderID != "animego_me" || got.Source.Variant != "2:1" {
		t.Fatalf("играет %s/%s", got.Offer.Ref.ProviderID, got.Source.Variant)
	}
	if r.got[0].VariantLabel != "AniLibria" {
		t.Fatalf("студия не передана площадке: %q", r.got[0].VariantLabel)
	}

	// Студии нет ни у одной площадки — честный отказ.
	if _, err := svc.Issue(context.Background(), Request{User: 1, Episode: 3264606,
		Voice: "shizaproject"}); !errors.Is(err, kernel.ErrNotFound) || !errors.Is(err, ErrNoVoice) {
		t.Fatalf("несуществующая студия: %v", err)
	}
}

// Запомненная студия важнее запомненной площадки: у kinopub AniLibria нет,
// а у AnimeGO.fr есть — играет AnimeGO.fr, и плееру сказано почему.
func TestIssue_RememberedVoiceMovesToAnotherProvider(t *testing.T) {
	r := &voiceResolver{audios: []provider.AudioTrack{{Title: "AAC 2.0", Language: "rus"}, {Title: "Orig", Language: "jpn"}}}
	svc, _ := newVoiceSvc(r, psyVoices())
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 3264606,
		Choice: catalog.Choice{Source: "kinopub"}, VariantLabel: "AniLibria"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Offer.Ref.ProviderID != "animego_fr" || got.Source.Variant != "cvh:AnilibriaTV" {
		t.Fatalf("играет %s/%s, спрашивали %v", got.Offer.Ref.ProviderID, got.Source.Variant, r.asked())
	}
	if len(got.Passed) != 1 || got.Passed[0] != (Pass{Provider: "kinopub", Reason: PassNoVoice}) {
		t.Fatalf("почему не kinopub: %+v", got.Passed)
	}
	if tk, _ := svc.tickets.Get(context.Background(), got.Ticket.ID); tk.Choice.Source != "animego_fr" {
		t.Fatalf("тикет обновится не там: %+v", tk.Choice)
	}
}

// Студия есть у выдавшей площадки дорожкой общего потока — её выберет
// плеер; другие площадки не трогаются. Подпись API («AAC 2.0») с ней не
// совпадает, но список озвучек знает её как «Русская дорожка».
func TestIssue_RememberedTrackStaysOnKinopub(t *testing.T) {
	r := &voiceResolver{audios: []provider.AudioTrack{{Title: "AAC 2.0", Language: "rus"}}}
	svc, _ := newVoiceSvc(r, psyVoices())
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 3264606,
		Choice: catalog.Choice{Source: "kinopub"}, VariantLabel: "Track 1 (RUS)"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Offer.Ref.ProviderID != "kinopub" || len(r.asked()) != 1 || len(got.Passed) != 0 {
		t.Fatalf("играет %s, спрашивали %v, обошли %+v", got.Offer.Ref.ProviderID, r.asked(), got.Passed)
	}
}

// Выбор, сделанный руками только что, следование студии не трогает.
func TestIssue_StrictChoiceIsNotFollowed(t *testing.T) {
	r := &voiceResolver{audios: []provider.AudioTrack{{Title: "Orig", Language: "jpn"}}}
	svc, _ := newVoiceSvc(r, psyVoices())
	got, err := svc.Issue(context.Background(), Request{User: 1, Episode: 3264606,
		Choice: catalog.Choice{Source: "kinopub", Strict: true}, VariantLabel: "AniLibria"})
	if err != nil || got.Offer.Ref.ProviderID != "kinopub" {
		t.Fatalf("строгий выбор подменён: %v %v", got.Offer.Ref.ProviderID, err)
	}
}

// «Звёздная принцесса» 1×1: kinopub зовёт дубляж «DUB», ЛордФильм —
// «Дублированный». Двумя студиями они сделали бы обход тормозящего kinopub
// невозможным («DUB больше нигде нет»), поэтому это один пункт с двумя
// путями.
func TestVoices_KindSpellingsMerge(t *testing.T) {
	ranked := []catalog.Offer{
		{Ref: kernel.ProviderRef{ProviderID: "kinopub", ItemID: "10890"}, Priority: 100},
		{Ref: kernel.ProviderRef{ProviderID: "lordfilm", ItemID: "79bf"}, Priority: 50},
	}
	got := mergeVoices(ranked, [][]provider.Voice{
		{{Title: "DUB"}, {Title: "MVO"}, {Title: "Оригинал"}},
		{{Title: "Дублированный", Variant: "t66", Player: "Alloha"},
			{Title: "Многоголосый закадровый", Variant: "t1", Player: "Alloha"},
			{Title: "Оригинальный", Variant: "t2", Player: "Alloha"}},
	})
	if len(got) != 3 {
		t.Fatalf("пунктов %d, ждали 3: %+v", len(got), got)
	}
	dub := got[0]
	if dub.Key != "dub" || dub.Title != "Дубляж" || len(dub.Ways) != 2 ||
		dub.Ways[0].Offer.Ref.ProviderID != "kinopub" || dub.Ways[1].Variant != "t66" {
		t.Fatalf("дубляж: %+v", dub)
	}
	if got[1].Title != "Многоголосый" || len(got[1].Ways) != 2 || got[2].Title != "Оригинал" || len(got[2].Ways) != 2 {
		t.Fatalf("список: %+v", got)
	}
}
