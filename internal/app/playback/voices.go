package playback

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/provider"
)

// VoiceLister — озвучки серии у площадки без выдачи потока (реестр
// провайдеров). Площадка, которая так не умеет, отвечает «не найдено».
type VoiceLister interface {
	Voices(ctx context.Context, ref kernel.ProviderRef, season, episode int) ([]provider.Voice, error)
}

// WithVoices включает общий список озвучек серии: озвучки всех площадок
// одним списком, переключение по студии и следование запомненной студии
// с площадки на площадку.
func (s *Service) WithVoices(v VoiceLister) *Service {
	s.voices = v
	s.voiceCache = map[kernel.EpisodeID]cachedVoices{}
	return s
}

// SpellingSink — кому сообщать, как площадки написали озвучки: по этим
// написаниям учатся псевдонимы студий и строятся подсказки в админке
// (app/voicenames). Вызов не должен ждать базу.
type SpellingSink interface {
	Observe(ep kernel.EpisodeID, providerID, raw string)
}

// WithSpellings включает запись написаний озвучек.
func (s *Service) WithSpellings(sink SpellingSink) *Service {
	s.spellings = sink
	return s
}

// ResetVoices забывает собранные списки озвучек: таблица псевдонимов
// поменялась, и студии, которые теперь одно, должны слиться сразу, а не
// через срок памяти.
func (s *Service) ResetVoices() {
	s.voiceMu.Lock()
	defer s.voiceMu.Unlock()
	if s.voiceCache != nil {
		s.voiceCache = map[kernel.EpisodeID]cachedVoices{}
	}
}

// VoiceOption — одна озвучка в общем списке: студия один раз, сколько бы
// площадок её ни отдавали.
type VoiceOption struct {
	// Key — студия, сведённая к одному написанию (provider.VoiceKey): по
	// нему озвучку выбирают и запоминают.
	Key   string
	Title string
	// Ways — где она есть, по приоритету площадок.
	Ways []VoiceWay
}

// VoiceWay — где взять озвучку: площадка, её номер потока (пусто —
// дорожка общего потока) и плеер.
type VoiceWay struct {
	Offer   catalog.Offer
	Variant string
	Player  string
}

// VoiceList — озвучки серии и площадки, которые о своих не рассказали.
type VoiceList struct {
	Voices []VoiceOption
	// Silent — площадки этой серии, чьих озвучек в списке нет: лежат, не
	// ответили или просят притормозить. Человеку стоит знать, что список
	// может быть неполным.
	Silent []Pass
}

// Find — озвучка по ключу или по любому написанию студии.
func (l VoiceList) Find(keyOrTitle string) (VoiceOption, bool) {
	k := provider.VoiceKey(keyOrTitle)
	for _, v := range l.Voices {
		if v.Key == k || v.Key == keyOrTitle {
			return v, true
		}
	}
	return VoiceOption{}, false
}

// Сколько помнить список озвучек серии. Площадки сами держат свои ответы
// минуты (страницы, плейлисты CVH), а список меняется, когда выходит новая
// озвучка, — часы, не минуты. Неполный (кто-то промолчал) — недолго: площадка
// могла ожить.
const (
	voicesTTL        = 10 * time.Minute
	voicesPartialTTL = time.Minute
	voicesCacheMax   = 2000
	// followWait — сколько ждать списка озвучек, когда выдавшая поток
	// площадка не знает запомненной студии. Человек в это время ждёт
	// первый кадр, поэтому меньше срока площадке.
	followWait = 3 * time.Second
	// followBudget — сколько всего искать запомненную студию у других
	// площадок, когда поток уже выдан. Не нашлась за это время — играет
	// выданное: ждать первый кадр дольше обидно.
	followBudget = 8 * time.Second
)

type cachedVoices struct {
	list VoiceList
	till time.Time
	// offers — с каких элементов площадок собран список (offersKey). К серии
	// привязалась новая площадка («Обновить», досбор) — список собирается заново,
	// не дожидаясь срока: иначе её озвучки не видны до десяти минут.
	offers string
}

// offersKey — набор элементов площадок серии, без учёта порядка.
func offersKey(offers []catalog.Offer) string {
	keys := make([]string, 0, len(offers))
	for _, o := range offers {
		keys = append(keys, o.Ref.ProviderID+"\x00"+o.Ref.ItemID+"\x00"+
			strconv.Itoa(o.SeasonNo)+"\x00"+strconv.Itoa(o.EpisodeNo))
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x01")
}

// Voices — озвучки серии по всем её площадкам, одним списком.
//
// Площадки спрашиваются параллельно и не дольше срока попытки; лежащие по
// свежей памяти не спрашиваются вовсе. Ответ живёт в памяти: им пользуются
// и меню плеера, и выбор площадки под запомненную студию.
func (s *Service) Voices(ctx context.Context, ep kernel.EpisodeID) (VoiceList, error) {
	return s.voicesWithin(ctx, ep, s.attempt)
}

func (s *Service) voicesWithin(ctx context.Context, ep kernel.EpisodeID, wait time.Duration) (VoiceList, error) {
	if s.voices == nil {
		return VoiceList{}, nil
	}
	now := s.clock.Now()
	// Площадки серии читаются и при живой памяти: это запрос к своей базе, а не к
	// площадкам, и только так видно, что к серии привязалась новая.
	offers, err := s.titles.Offers(ctx, ep)
	if err != nil {
		return VoiceList{}, err
	}
	key := offersKey(offers)
	s.voiceMu.Lock()
	c, ok := s.voiceCache[ep]
	s.voiceMu.Unlock()
	if ok && now.Before(c.till) && c.offers == key {
		return c.list, nil
	}

	ranked := catalog.Rank(offers, catalog.Choice{})
	answers := make([][]provider.Voice, len(ranked))
	errs := make([]error, len(ranked))
	var wg sync.WaitGroup
	for i, o := range ranked {
		if s.health != nil && s.health.Down(o.Ref.ProviderID) {
			errs[i] = errDown
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			actx, cancel := context.WithTimeout(ctx, wait)
			defer cancel()
			answers[i], errs[i] = s.voices.Voices(actx, o.Ref, o.SeasonNo, o.EpisodeNo)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return VoiceList{}, err
	}

	if s.spellings != nil {
		for i, o := range ranked {
			for _, v := range answers[i] {
				s.spellings.Observe(ep, o.Ref.ProviderID, v.Title)
			}
		}
	}
	list := VoiceList{Voices: mergeVoices(ranked, answers)}
	seen := map[string]bool{}
	for i, o := range ranked {
		id, err := o.Ref.ProviderID, errs[i]
		if err == nil || errors.Is(err, kernel.ErrNotFound) || seen[id] {
			continue // «серии нет» или «не умеет» — ей и нечего сказать
		}
		seen[id] = true
		reason := passReason(err)
		if errors.Is(err, errDown) {
			reason = s.downReason(id)
		}
		list.Silent = append(list.Silent, Pass{Provider: id, Reason: reason})
	}

	ttl := voicesTTL
	if len(list.Silent) > 0 {
		ttl = voicesPartialTTL
	}
	s.voiceMu.Lock()
	if len(s.voiceCache) >= voicesCacheMax {
		for k, v := range s.voiceCache {
			if !now.Before(v.till) {
				delete(s.voiceCache, k)
			}
		}
		if len(s.voiceCache) >= voicesCacheMax {
			s.voiceCache = map[kernel.EpisodeID]cachedVoices{}
		}
	}
	s.voiceCache[ep] = cachedVoices{list: list, till: now.Add(ttl), offers: key}
	s.voiceMu.Unlock()
	return list, nil
}

// errDown — площадку не спрашивали: лежит по свежей памяти.
var errDown = errors.New("площадка лежит")

// mergeVoices сводит озвучки площадок в один список: студия один раз (в
// самом коротком из написаний), пути к ней — по приоритету площадок. Порядок списка: озвучки, затем оригинал
// и чужие языки, затем субтитры; внутри — чем у большего числа площадок
// студия есть, тем выше (так AniLibria не тонет среди редких студий), при
// равенстве — как у главной площадки. От переключения порядок не зависит:
// список не прыгает под пальцем.
func mergeVoices(ranked []catalog.Offer, answers [][]provider.Voice) []VoiceOption {
	var out []VoiceOption
	idx := map[string]int{}
	for i, o := range ranked {
		for _, v := range answers[i] {
			k := provider.VoiceKey(v.Title)
			if k == "" {
				continue
			}
			title := provider.VoiceTitle(v.Title)
			j, ok := idx[k]
			if !ok {
				j = len(out)
				idx[k] = j
				out = append(out, VoiceOption{Key: k, Title: title})
			}
			// Написания одной студии различаются регистром, точками и
			// хвостом «TV»: «AniLibria» читается лучше «AnilibriaTV».
			if len([]rune(title)) < len([]rune(out[j].Title)) {
				out[j].Title = title
			}
			out[j].Ways = append(out[j].Ways, VoiceWay{Offer: o, Variant: v.Variant, Player: v.Player})
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		ga, gb := voiceGroup(out[a]), voiceGroup(out[b])
		if ga != gb {
			return ga < gb
		}
		return providersOf(out[a]) > providersOf(out[b])
	})
	return out
}

// voiceGroup: 0 — озвучка, 1 — оригинал или дорожка на чужом языке,
// 2 — субтитры.
func voiceGroup(v VoiceOption) int {
	switch {
	case provider.Subtitled(v.Title):
		return 2
	case v.Key == provider.VoiceKey("Оригинал"):
		return 1
	}
	for _, lang := range []string{"eng", "jpn", "kor", "chi", "ukr"} {
		if v.Key == provider.VoiceKey(provider.LanguageVoice(lang)) {
			return 1
		}
	}
	return 0
}

func providersOf(v VoiceOption) int {
	seen := map[string]bool{}
	for _, w := range v.Ways {
		seen[w.Offer.Ref.ProviderID] = true
	}
	return len(seen)
}
