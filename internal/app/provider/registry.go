package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Registry — живые провайдеры по их id.
//
// Реализует Resolver из app/playback, поэтому слой воспроизведения знает
// только «резолвни эту ссылку», а какой провайдер за ней стоит и в каком
// режиме он хостится — не его дело.
type Registry struct {
	mu     sync.RWMutex
	byID   map[string]Provider
	desc   map[string]dp.Descriptor
	egress map[string]string
	// ua — по желанию: какой User-Agent несёт тикет площадки (см. WithUserAgent).
	ua func(providerID, current string) string
}

func NewRegistry() *Registry {
	return &Registry{
		byID:   make(map[string]Provider),
		desc:   make(map[string]dp.Descriptor),
		egress: make(map[string]string),
	}
}

// Register проверяет совместимость контракта ДО того, как провайдер станет
// доступен. Несовпадение мажора — отказ, без попыток договориться.
//
// egress — имя маршрута наружу, которым ходит этот провайдер. Провайдер его
// не знает (он не знает инфраструктуру), а тикет обязан его нести: шлюз
// тянет байты тем же выходом, каким шёл резолв.
func (r *Registry) Register(ctx context.Context, p Provider, egress string) (dp.Descriptor, error) {
	d, err := p.Describe(ctx)
	if err != nil {
		return dp.Descriptor{}, fmt.Errorf("describe: %w", err)
	}
	if err := d.Compatible(); err != nil {
		return dp.Descriptor{}, fmt.Errorf("provider %q api v%d: %w", d.ProviderID, d.APIMajor, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[d.ProviderID] = p
	r.desc[d.ProviderID] = d
	r.egress[d.ProviderID] = egress
	return d, nil
}

// Descriptor — хендшейк зарегистрированного провайдера.
func (r *Registry) Descriptor(id string) (dp.Descriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.desc[id]
	return d, ok
}

// Name — как назвать площадку человеку. Незарегистрированная (площадка
// недоступна) называется своим id: витрине лучше показать «animego_me»,
// чем пустое место.
func (r *Registry) Name(id string) string {
	if d, ok := r.Descriptor(id); ok && d.Name != "" {
		return d.Name
	}
	return id
}

// Searchers — провайдеры, умеющие искать; only сужает до названных.
func (r *Registry) Searchers(only ...string) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	allow := map[string]bool{}
	for _, id := range only {
		allow[id] = true
	}
	ids := make([]string, 0, len(r.byID))
	for id, d := range r.desc {
		if d.Has(dp.CapSearch) && (len(allow) == 0 || allow[id]) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]Provider, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.byID[id])
	}
	return out
}

// SearchersByPriority — те же искатели, но по убыванию приоритета площадки
// (при равных — по имени): для поиска ступенями, когда спрашивают лучшую
// площадку и идут к следующей, только если найденного мало.
func (r *Registry) SearchersByPriority(prio map[string]int, only ...string) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	allow := map[string]bool{}
	for _, id := range only {
		allow[id] = true
	}
	ids := make([]string, 0, len(r.byID))
	for id, d := range r.desc {
		if d.Has(dp.CapSearch) && (len(allow) == 0 || allow[id]) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if prio[ids[i]] != prio[ids[j]] {
			return prio[ids[i]] > prio[ids[j]]
		}
		return ids[i] < ids[j]
	})
	out := make([]Provider, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.byID[id])
	}
	return out
}

func (r *Registry) Get(id string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("provider %q: %w", id, kernel.ErrNotFound)
	}
	return p, nil
}

func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byID))
	for id := range r.byID {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// WithUserAgent задаёт, какой User-Agent ложится в тикет: шлюз потом предъявит его
// площадке, а площадки, привязывающие ссылку к подписи (CDNVideoHub), требуют ту же,
// с которой шёл резолв. fn получает площадку и подпись, которую поставил провайдер.
// Сервер этого не включает и живёт как прежде.
func (r *Registry) WithUserAgent(fn func(providerID, current string) string) *Registry {
	r.mu.Lock()
	r.ua = fn
	r.mu.Unlock()
	return r
}

// Resolve удовлетворяет playback.Resolver.
//
// Площадка, ещё не зарегистрированная (недоступна при старте), отвечает
// «недоступна»: для воспроизведения это повод спросить следующую площадку.
func (r *Registry) Resolve(ctx context.Context, req dp.ResolveRequest) (dp.Source, error) {
	p, err := r.Get(req.Ref.ProviderID)
	if err != nil {
		return dp.Source{}, fmt.Errorf("provider %q не зарегистрирован: %w",
			req.Ref.ProviderID, kernel.ErrUnavailable)
	}
	src, err := p.Resolve(ctx, req)
	if err != nil {
		return dp.Source{}, err
	}
	r.mu.RLock()
	src.Egress = r.egress[req.Ref.ProviderID]
	ua := r.ua
	r.mu.RUnlock()
	if ua != nil {
		src.Headers = withUserAgent(src.Headers, ua(req.Ref.ProviderID, headerValue(src.Headers, "User-Agent")))
	}
	return src, nil
}

// Lookups — площадки, умеющие искать по внешним номерам, по их id.
// Площадка, ещё не зарегистрированная, сюда не попадает.
func (r *Registry) Lookups() map[string]Lookup {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]Lookup{}
	for id, p := range r.byID {
		if l, ok := p.(Lookup); ok && r.desc[id].Has(dp.CapLookup) {
			out[id] = l
		}
	}
	return out
}

// CanThumb — отдаёт ли площадка кадры серий по запросу.
func (r *Registry) CanThumb(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.byID[id].(Thumbnails)
	return ok && r.desc[id].Has(dp.CapThumbnails)
}

// EpisodeThumbnail — кадр серии у площадки.
func (r *Registry) EpisodeThumbnail(ctx context.Context, ref kernel.ProviderRef, season, episode int) (string, error) {
	r.mu.RLock()
	t, ok := r.byID[ref.ProviderID].(Thumbnails)
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("кадры у %q: %w", ref.ProviderID, kernel.ErrNotFound)
	}
	return t.EpisodeThumbnail(ctx, ref, season, episode)
}

// Voices — озвучки серии у площадки. Площадка без CapVoices отвечает
// «не найдено»: в общем списке её озвучек просто нет, а играть она всё
// равно может. Незарегистрированная — «недоступна», как и при резолве.
func (r *Registry) Voices(ctx context.Context, ref kernel.ProviderRef, season, episode int) ([]dp.Voice, error) {
	r.mu.RLock()
	p, registered := r.byID[ref.ProviderID]
	v, ok := p.(Voices)
	ok = ok && r.desc[ref.ProviderID].Has(dp.CapVoices)
	r.mu.RUnlock()
	if !registered {
		return nil, fmt.Errorf("provider %q не зарегистрирован: %w", ref.ProviderID, kernel.ErrUnavailable)
	}
	if !ok {
		return nil, fmt.Errorf("озвучки у %q: %w", ref.ProviderID, kernel.ErrNotFound)
	}
	return v.Voices(ctx, ref, season, episode)
}

// Segments — разметка пропуска серии у площадки, умеющей отдавать её
// отдельным запросом. Не умеющая — kernel.ErrNotFound: для вызывающего это
// то же «разметки нет».
func (r *Registry) Segments(ctx context.Context, req dp.ResolveRequest) ([]dp.Segment, error) {
	r.mu.RLock()
	sg, ok := r.byID[req.Ref.ProviderID].(Segments)
	can := r.desc[req.Ref.ProviderID].Has(dp.CapSegments)
	r.mu.RUnlock()
	if !ok || !can {
		return nil, fmt.Errorf("разметка у %q: %w", req.Ref.ProviderID, kernel.ErrNotFound)
	}
	return sg.Segments(ctx, req)
}

// headerValue — значение заголовка без оглядки на регистр имени ("" — нет).
func headerValue(h map[string]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// withUserAgent — копия заголовков тикета с заданным User-Agent: карта провайдера
// не меняется (он может отдать общую), а прежняя запись любого регистра убирается.
func withUserAgent(h map[string]string, ua string) map[string]string {
	out := make(map[string]string, len(h)+1)
	for k, v := range h {
		if !strings.EqualFold(k, "User-Agent") {
			out[k] = v
		}
	}
	if ua != "" {
		out["User-Agent"] = ua
	}
	return out
}
