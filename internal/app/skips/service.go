// Package skips — разметка пропуска серии: опенинг, эндинг, пересказ.
//
// Своей разметки у платформы нет и руками её никто не ведёт. Она
// собирается из двух мест:
//   - площадка, у которой разметка лежит отдельно от потока (Kodik — на
//     странице каждой серии): точна, потому что снята с того же файла;
//   - AniSkip по номеру MAL: общая база зрителей аниме, но размечен чужой
//     файл, и годится она лишь при совпадении длины — это решает плеер.
//
// Разметка, пришедшая вместе с потоком (Alloha, Kodik на странице серии),
// сюда не попадает: она уже в ответе на выпуск тикета.
package skips

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Offers — какие площадки отдают серию и под какими номерами.
type Offers interface {
	Offers(ctx context.Context, ep kernel.EpisodeID) ([]catalog.Offer, error)
}

// ProviderSegments — разметка у площадки по запросу (реестр площадок).
type ProviderSegments func(ctx context.Context, req dp.ResolveRequest) ([]dp.Segment, error)

// Community — база разметки по номеру MAL (AniSkip).
type Community interface {
	Segments(ctx context.Context, mal string, episode int) ([]dp.Segment, error)
}

// OriginCommunity — откуда разметка AniSkip; у разметки площадки — её id.
const OriginCommunity = "aniskip"

// Mark — отрезок и откуда он.
type Mark struct {
	dp.Segment
	Origin string
}

// Request — разметка какой серии и какого потока.
type Request struct {
	Episode kernel.EpisodeID
	// Source и Variant — площадка и вариант (озвучка), которые сейчас
	// играют: разметка привязана к файлу. Пусто — только AniSkip.
	Source  string
	Variant string
}

// Сроки памяти. Найденное меняется редко; ненайденное у онгоинга
// появляется — ему полдня. Отказ — не ответ, его помним минуты: иначе
// каждая серия заново ждала бы лежащий сервис.
const (
	foundTTL   = 24 * time.Hour
	missingTTL = 6 * time.Hour
	failTTL    = 5 * time.Minute
	// maxEntries — потолок памяти. На семью это тысячи серий за месяцы.
	maxEntries = 20000
	// providerTimeout — страница серии Kodik весит под мегабайт и идёт
	// после вежливой паузы хоста.
	providerTimeout = 10 * time.Second
	// communityTimeout — AniSkip отвечает за десятые доли секунды.
	communityTimeout = 5 * time.Second
	// maxMALs — сколько записей MAL спрашивать: у серии бывает две
	// аниме-площадки, но номер у них обычно общий.
	maxMALs = 2
)

type entry struct {
	segs  []dp.Segment
	until time.Time
}

// Service собирает разметку.
type Service struct {
	offers    Offers
	provider  ProviderSegments
	community Community
	now       func() time.Time
	log       *slog.Logger

	mu    sync.Mutex
	cache map[string]entry
}

// New: provider и community могут быть nil — тогда этой части нет.
func New(offers Offers, provider ProviderSegments, community Community, log *slog.Logger) *Service {
	return &Service{offers: offers, provider: provider, community: community,
		now: time.Now, log: log, cache: map[string]entry{}}
}

// Episode — вся разметка серии, какую удалось собрать. Отказ источника —
// не ошибка: без разметки серия смотрится как раньше.
//
// Поход наружу не зависит от запроса: человек переключил серию — найденное
// всё равно ляжет в память и пригодится при следующем открытии.
func (s *Service) Episode(ctx context.Context, req Request) ([]Mark, error) {
	offers, err := s.offers.Offers(ctx, req.Episode)
	if err != nil {
		return nil, fmt.Errorf("offers %d: %w", req.Episode, err)
	}
	bg := context.WithoutCancel(ctx)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		out  []Mark
		take = func(origin string, segs []dp.Segment) {
			mu.Lock()
			defer mu.Unlock()
			for _, sg := range segs {
				out = append(out, Mark{Segment: sg, Origin: origin})
			}
		}
	)
	if o, ok := offerOf(offers, req.Source); ok && s.provider != nil && req.Variant != "" {
		r := dp.ResolveRequest{Ref: o.Ref, Season: o.SeasonNo, Episode: o.EpisodeNo, Variant: req.Variant}
		key := "p|" + o.Ref.ProviderID + "|" + o.Ref.ItemID + "|" + strconv.Itoa(o.SeasonNo) +
			"|" + strconv.Itoa(o.EpisodeNo) + "|" + req.Variant
		wg.Add(1)
		go func() {
			defer wg.Done()
			take(o.Ref.ProviderID, s.cached(bg, key, providerTimeout, func(ctx context.Context) ([]dp.Segment, error) {
				return s.provider(ctx, r)
			}, exactOK))
		}()
	}
	if s.community != nil {
		for _, m := range malsOf(offers) {
			key := "a|" + m.mal + "|" + strconv.Itoa(m.episode)
			wg.Add(1)
			go func() {
				defer wg.Done()
				take(OriginCommunity, s.cached(bg, key, communityTimeout, func(ctx context.Context) ([]dp.Segment, error) {
					return s.community.Segments(ctx, m.mal, m.episode)
				}, plausible))
			}()
		}
	}
	wg.Wait()
	return dedupe(out), nil
}

// cached — разметка из памяти или от источника, с отсевом.
func (s *Service) cached(ctx context.Context, key string, limit time.Duration,
	fetch func(context.Context) ([]dp.Segment, error), keep func(dp.Segment) bool) []dp.Segment {
	now := s.now()
	s.mu.Lock()
	e, ok := s.cache[key]
	s.mu.Unlock()
	if ok && now.Before(e.until) {
		return e.segs
	}

	fctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	segs, err := fetch(fctx)
	ttl := foundTTL
	switch {
	case errors.Is(err, kernel.ErrNotFound):
		ttl, segs = missingTTL, nil
	case err != nil:
		ttl, segs = failTTL, nil
		if s.log != nil {
			s.log.Info("разметка пропуска не получена", "key", key, "err", err)
		}
	}
	var kept []dp.Segment
	for _, sg := range segs {
		if keep(sg) {
			kept = append(kept, sg)
		}
	}
	if len(kept) == 0 && err == nil {
		ttl = missingTTL
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) >= maxEntries {
		for k, v := range s.cache {
			if !now.Before(v.until) {
				delete(s.cache, k)
			}
		}
		if len(s.cache) >= maxEntries {
			s.cache = map[string]entry{}
		}
	}
	s.cache[key] = entry{segs: kept, until: now.Add(ttl)}
	return kept
}

func offerOf(offers []catalog.Offer, source string) (catalog.Offer, bool) {
	for _, o := range offers {
		if source != "" && o.Ref.ProviderID == source {
			return o, true
		}
	}
	return catalog.Offer{}, false
}

type malEpisode struct {
	mal     string
	episode int
}

// malsOf — адреса серии в нумерации MAL: номер записи у элемента площадки
// и номер серии у той же площадки (элемент аниме-площадки — один сезон,
// как и запись MAL).
func malsOf(offers []catalog.Offer) []malEpisode {
	var out []malEpisode
	seen := map[malEpisode]bool{}
	for _, o := range offers {
		m := malEpisode{mal: o.MAL, episode: o.EpisodeNo}
		if m.mal == "" || m.episode < 1 || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
		if len(out) == maxMALs {
			break
		}
	}
	return out
}

// exactOK — разметке с того же потока верим, если она вообще осмысленна.
func exactOK(s dp.Segment) bool { return s.Valid() }

// plausible — отсев чужой разметки: её ведут люди, и попадаются эндинги
// в начале серии и «опенинги» на полсерии. Пропуск такого съел бы сюжет.
func plausible(s dp.Segment) bool {
	if !s.Valid() || s.ForDurationMS <= 0 {
		return false
	}
	length, dur := s.EndMS-s.StartMS, s.ForDurationMS
	switch s.Kind {
	case dp.SegIntro:
		return length >= 15_000 && length <= 200_000 && s.StartMS < dur/2
	case dp.SegOutro:
		return length >= 15_000 && length <= 240_000 && s.StartMS > dur/2
	case dp.SegRecap:
		return length >= 10_000 && length <= 300_000 && s.StartMS < dur*2/5
	}
	return false
}

// dedupe — одна и та же разметка от двух записей MAL — один отрезок.
// Порядок постоянный: сперва площадка (точная), дальше по времени.
func dedupe(in []Mark) []Mark {
	var out []Mark
	seen := map[Mark]bool{}
	for _, m := range in {
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := out[i].Origin == OriginCommunity, out[j].Origin == OriginCommunity
		if ci != cj {
			return !ci
		}
		return out[i].StartMS < out[j].StartMS
	})
	return out
}
