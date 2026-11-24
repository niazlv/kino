package catalog

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Досбор площадок тайтла на устройстве (локальный движок, ADR-0015).
//
// Каталог движка собирается только из того, что человек нашёл сам, а поиск
// ступенями останавливается на первой площадке, нашедшей достаточно. Обходчика,
// который привязал бы к тайтлу остальные площадки, нет. Поэтому тайтл живёт с той
// площадкой, что ответила первой, и включённые позже площадки — с их сериями и
// озвучками — о нём так и не узнают. Досбор спрашивает о тайтле каждую включённую
// площадку, которой у него ещё нет, и помнит на диске, кого уже спрашивали: новая
// площадка спрашивается один раз, а не на каждом открытии.

// ProviderSearch — поиск по названию у одной площадки. Ошибка — площадка не
// ответила: такую не помечаем спрошенной.
type ProviderSearch func(ctx context.Context, providerID, q string, limit int) ([]dp.Title, error)

// ProviderLookup — произведение у одной площадки по внешним номерам. Нет такого —
// kernel.ErrNotFound (площадка ответила); любая другая ошибка — не ответила.
type ProviderLookup func(ctx context.Context, providerID string, ids dp.ExternalIDs) (dp.Title, error)

// SourceAsks — память о том, каких площадок о тайтле уже спрашивали.
type SourceAsks interface {
	SourcesAsked(ctx context.Context, id kernel.TitleID) (map[string]time.Time, error)
	MarkSourcesAsked(ctx context.Context, id kernel.TitleID, providers []string, at time.Time) error
}

// Sweep — настройки досбора.
type Sweep struct {
	Search ProviderSearch
	// Lookup — по желанию: площадки, умеющие искать по номерам, спрашиваются так —
	// это точнее и дешевле поиска по названию.
	Lookup ProviderLookup
	// Providers — кого можно спросить сейчас: площадки с поиском и площадки с
	// поиском по номерам. Функция, а не список: площадки регистрируются в фоне.
	Providers func() (search, lookup []string)
	Asks      SourceAsks
	// Auto — спрашивать недостающие площадки при открытии тайтла (режим «все
	// площадки»). Без него досбор идёт только по кнопке «Обновить» (Refresh).
	Auto bool
	// Repeat — через сколько снова спрашивать площадку, у которой тайтла не нашлось
	// (0 — неделя). Кнопка «Обновить» этот срок не ждёт.
	Repeat time.Duration
	// Limit — сколько находок просить у площадки на запрос (0 — discoverLimit).
	Limit int
}

// WithSourceSweep включает досбор площадок тайтла (см. Sweep).
func (c *LazyCatalog) WithSourceSweep(s Sweep) *LazyCatalog {
	c.sweep = &s
	return c
}

const (
	// sweepRepeat — по умолчанию площадка, не нашедшая тайтла, спрашивается снова
	// через неделю: у неё он мог появиться позже.
	sweepRepeat = 7 * 24 * time.Hour
	// sweepTimeout — сколько ждать одну площадку при досборе.
	sweepTimeout = 20 * time.Second
	// sweepAutoEvery — повторное открытие тайтла не запускает второй досбор,
	// пока идёт или только что прошёл первый (между перезапусками помнит база).
	sweepAutoEvery = 10 * time.Minute
	// refreshCooldown — «Обновить» на одном тайтле не чаще: нажатие подряд не
	// должно стоить площадкам второго круга запросов.
	refreshCooldown = time.Minute
	// refreshTimeout — сколько идёт «Обновить» целиком.
	refreshTimeout = 90 * time.Second
)

// SweepReport — чем кончился досбор: каких площадок спросили, какие
// привязались к тайтлу, какие не ответили.
type SweepReport struct {
	Asked    []string
	Attached []string
	Failed   []string
}

func (c *LazyCatalog) sweepRepeatOrDefault() time.Duration {
	if c.sweep != nil && c.sweep.Repeat > 0 {
		return c.sweep.Repeat
	}
	return sweepRepeat
}

// sweepTargets — кого спросить о тайтле: включённые площадки, которых у тайтла
// нет и которых не спрашивали в пределах window.
func (c *LazyCatalog) sweepTargets(ctx context.Context, t catalog.Title, window time.Duration) (search, lookup []string) {
	if c.sweep == nil || c.sweep.Providers == nil {
		return nil, nil
	}
	// Выключенный человеком источник — тоже «есть»: связь с ним помнится, и
	// спрашивать площадку заново значит снова привязать то, от чего отказались.
	have := map[string]bool{}
	for _, s := range t.Sources {
		have[s.Ref.ProviderID] = true
	}
	var recent map[string]time.Time
	if c.sweep.Asks != nil {
		recent, _ = c.sweep.Asks.SourcesAsked(ctx, t.ID)
	}
	skip := func(id string) bool {
		if have[id] {
			return true
		}
		at, ok := recent[id]
		return ok && time.Since(at) < window
	}
	s, l := c.sweep.Providers()
	for _, id := range s {
		if !skip(id) {
			search = append(search, id)
		}
	}
	for _, id := range l {
		if !skip(id) {
			lookup = append(lookup, id)
		}
	}
	return search, lookup
}

// sweepNow спрашивает о тайтле недостающие площадки (не спрошенные в пределах
// window) и кладёт находки в каталог обычной склейкой; отдаёт отчёт и
// привязавшиеся элементы площадок.
func (c *LazyCatalog) sweepNow(ctx context.Context, t catalog.Title, window time.Duration) (SweepReport, []kernel.ProviderRef) {
	var rep SweepReport
	if c.sweep == nil || c.titles == nil {
		return rep, nil
	}
	release, ok := c.holdSweep(ctx, t.ID)
	if !ok {
		return rep, nil
	}
	defer release()
	// Пока ждали чужой досбор, к тайтлу могли привязаться площадки — смотрим свежий.
	if fresh, err := c.Reader.Title(ctx, t.ID); err == nil {
		t = fresh
	}
	search, lookup := c.sweepTargets(ctx, t, window)
	if len(search) == 0 && len(lookup) == 0 {
		return rep, nil
	}
	answered := map[string]bool{}
	failed := map[string]bool{}
	attached := map[string]bool{}
	var refs []kernel.ProviderRef
	seen := map[kernel.ProviderRef]bool{}
	attach := func(ft dp.Title) {
		if seen[ft.Ref] {
			return
		}
		seen[ft.Ref] = true
		if id, err := c.titles.UpsertTitle(ctx, ft); err == nil && id == t.ID {
			attached[ft.Ref.ProviderID] = true
			refs = append(refs, ft.Ref)
		}
	}

	// По номерам — у кого есть такой поиск и у тайтла есть номера: один точный
	// запрос вместо поиска по названию и склейки.
	ids := dp.ExternalIDs{
		IMDB: t.IDs.IMDB, Kinopoisk: t.IDs.Kinopoisk,
		Title: t.Title, Original: t.OriginalOf(), Year: t.Year, Kind: string(t.Kind),
	}
	byIDs := map[string]bool{}
	if c.sweep.Lookup != nil && ids.IMDB+ids.Kinopoisk != "" {
		type res struct {
			id  string
			ft  dp.Title
			err error
		}
		ch := make(chan res, len(lookup))
		for _, id := range lookup {
			byIDs[id] = true
			go func() {
				lctx, cancel := context.WithTimeout(ctx, sweepTimeout)
				defer cancel()
				ft, err := c.sweep.Lookup(lctx, id, ids)
				ch <- res{id, ft, err}
			}()
		}
		for range lookup {
			r := <-ch
			switch {
			case r.err == nil:
				answered[r.id] = true
				if sameWork(ids, r.ft) {
					attach(r.ft)
				}
			case errors.Is(r.err, kernel.ErrNotFound):
				answered[r.id] = true
			default:
				failed[r.id] = true
			}
		}
	}

	// По названию — остальные: оригинал (на аниме-площадках он пишется
	// одинаково чаще русского), затем русское — тем, кто не нашёлся по оригиналу.
	var byName []string
	for _, id := range search {
		if !byIDs[id] {
			byName = append(byName, id)
		}
	}
	queries := []string{}
	if o := strings.TrimSpace(t.OriginalOf()); o != "" {
		queries = append(queries, o)
	}
	if ru, _, _ := strings.Cut(t.Title, " / "); strings.TrimSpace(ru) != "" && !containsFold(queries, ru) {
		queries = append(queries, strings.TrimSpace(ru))
	}
	limit := c.sweep.Limit
	if limit <= 0 {
		limit = discoverLimit
	}
	for _, q := range queries {
		var ask []string
		for _, id := range byName {
			if !attached[id] && !failed[id] {
				ask = append(ask, id)
			}
		}
		if len(ask) == 0 {
			break
		}
		type res struct {
			id    string
			found []dp.Title
			err   error
		}
		ch := make(chan res, len(ask))
		for _, id := range ask {
			go func() {
				sctx, cancel := context.WithTimeout(ctx, sweepTimeout)
				defer cancel()
				found, err := c.sweep.Search(sctx, id, q, limit)
				ch <- res{id, found, err}
			}()
		}
		// Ответы — по порядку площадок, а не по скорости: склейка решает по
		// тому, что уже в каталоге, и порядок записи не должен зависеть от сети.
		got := map[string]res{}
		for range ask {
			r := <-ch
			got[r.id] = r
		}
		for _, id := range ask {
			r := got[id]
			if r.err != nil {
				// Не ответила на первый запрос — второй не шлём: она лежит.
				failed[id] = true
				continue
			}
			answered[id] = true
			// Все находки, а не до первого совпадения: у аниме-площадки на
			// тайтл по странице на сезон, и каждая из них — его часть.
			for _, ft := range r.found {
				attach(ft)
			}
		}
	}

	var marks []string
	for id := range answered {
		marks = append(marks, id)
		rep.Asked = append(rep.Asked, id)
		// Площадка ответила хоть раз — ответ засчитан, даже если второй запрос
		// не дошёл.
		delete(failed, id)
	}
	for id := range failed {
		rep.Failed = append(rep.Failed, id)
	}
	for id := range attached {
		rep.Attached = append(rep.Attached, id)
	}
	sort.Strings(rep.Asked)
	sort.Strings(rep.Failed)
	sort.Strings(rep.Attached)
	if c.sweep.Asks != nil && len(marks) > 0 {
		// Не ответившие не помечаются: при следующем открытии их спросят снова.
		_ = c.sweep.Asks.MarkSourcesAsked(ctx, t.ID, marks, time.Now())
	}
	return rep, refs
}

func containsFold(list []string, s string) bool {
	s = strings.TrimSpace(s)
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// autoSweep — досбор при открытии тайтла в режиме «все площадки». Страница его
// не ждёт: найденное привяжется в фоне вместе с сериями и будет видно при
// следующем открытии (или сразу — по «Обновить»).
func (c *LazyCatalog) autoSweep(ctx context.Context, t catalog.Title) {
	if c.sweep == nil || !c.sweep.Auto || c.titles == nil {
		return
	}
	if s, l := c.sweepTargets(ctx, t, c.sweepRepeatOrDefault()); len(s) == 0 && len(l) == 0 {
		return
	}
	if !c.firstAsk("sweep:"+strconv.FormatInt(int64(t.ID), 10), sweepAutoEvery) {
		return
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoverTimeout)
	go func() {
		defer cancel()
		_, refs := c.sweepNow(bg, t, c.sweepRepeatOrDefault())
		c.fetchEpisodes(bg, t.ID, refs)
	}()
}

// RefreshReport — что дало «Обновить».
type RefreshReport struct {
	SweepReport
	// Refetched — у каких площадок перечитаны серии; RefetchFailed — какие не
	// отдали их.
	Refetched     []string
	RefetchFailed []string
	// EpisodesBefore/After — сколько серий у тайтла было и стало.
	EpisodesBefore int
	EpisodesAfter  int
	// Wait — тайтл только что обновляли: столько секунд до следующего раза, площадки
	// не спрашивались.
	Wait int
}

// Refresh — «Обновить» по просьбе человека: спросить о тайтле все включённые
// площадки, которых у него нет (не дожидаясь срока Repeat), и перечитать серии
// всех его площадок (не дожидаясь срока свежести). Единственный путь, где
// движок идёт к площадкам о известном тайтле мимо своих сроков, — поэтому только по
// явному действию и не чаще refreshCooldown на тайтл.
//
// Поход не привязан к соединению: человек ушёл со страницы — серии всё равно
// допишутся.
func (c *LazyCatalog) Refresh(ctx context.Context, id kernel.TitleID) (RefreshReport, error) {
	var rep RefreshReport
	t, err := c.Reader.Title(ctx, id)
	if err != nil {
		return rep, err
	}
	key := "refresh:" + strconv.FormatInt(int64(id), 10)
	if wait := c.cooldownLeft(key, refreshCooldown); wait > 0 {
		rep.Wait = int((wait + time.Second - 1) / time.Second)
		return rep, nil
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()

	before, err := c.Reader.Episodes(bg, id)
	if err != nil && !errors.Is(err, kernel.ErrNotFound) {
		return rep, err
	}
	rep.EpisodesBefore = len(before)

	// Срок Repeat не ждём — только не спрашиваем тех, кого спросили минуту назад
	// (автодосбор при открытии этой же страницы).
	rep.SweepReport, _ = c.sweepNow(bg, t, refreshCooldown)
	// Площадки тайтла — свежие: привязались этим досбором или тем, которого ждали.
	if t, err = c.Reader.Title(bg, id); err != nil {
		return rep, err
	}

	// Серии — со всех площадок тайтла, свежие и нет: человек попросил обновить.
	var refs []kernel.ProviderRef
	seen := map[kernel.ProviderRef]bool{}
	for _, s := range t.Sources {
		if s.Disabled || seen[s.Ref] || c.provider == nil {
			continue
		}
		seen[s.Ref] = true
		refs = append(refs, s.Ref)
	}
	type result struct {
		ref kernel.ProviderRef
		eps []dp.Episode
		err error
	}
	ch := make(chan result, len(refs))
	for _, ref := range refs {
		go func() {
			p, err := c.provider(ref.ProviderID)
			if err != nil {
				ch <- result{ref: ref, err: err}
				return
			}
			fctx, cancel := context.WithTimeout(bg, sweepTimeout)
			defer cancel()
			eps, err := p.Episodes(fctx, ref)
			c.noteOutcome(bg, fctx, ref.ProviderID, err)
			ch <- result{ref, eps, err}
		}()
	}
	ok, bad := map[string]bool{}, map[string]bool{}
	for range refs {
		r := <-ch
		if r.err != nil || len(r.eps) == 0 {
			bad[r.ref.ProviderID] = true
			continue
		}
		if err := c.writer.UpsertEpisodes(bg, id, r.eps); err != nil {
			return rep, err
		}
		ok[r.ref.ProviderID] = true
		c.forgetTried(r.ref)
	}
	for p := range ok {
		// У аниме-площадки по элементу на сезон: один ответил — площадка отдала серии.
		delete(bad, p)
		rep.Refetched = append(rep.Refetched, p)
	}
	for p := range bad {
		rep.RefetchFailed = append(rep.RefetchFailed, p)
	}
	sort.Strings(rep.Refetched)
	sort.Strings(rep.RefetchFailed)

	after, err := c.Reader.Episodes(bg, id)
	if err != nil && !errors.Is(err, kernel.ErrNotFound) {
		return rep, err
	}
	rep.EpisodesAfter = len(after)
	return rep, nil
}

// cooldownLeft — сколько ещё ждать по ключу; 0 — можно, и время отмечено.
func (c *LazyCatalog) cooldownLeft(key string, every time.Duration) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.asked == nil {
		c.asked = map[string]time.Time{}
	}
	if at, ok := c.asked[key]; ok {
		if left := every - time.Since(at); left > 0 {
			return left
		}
	}
	c.asked[key] = time.Now()
	return 0
}

func (c *LazyCatalog) forgetTried(r kernel.ProviderRef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tried, r)
}

// holdSweep — один досбор на тайтл за раз: автодосбор при открытии и «Обновить»
// иначе спросили бы одни и те же площадки дважды. Второй ждёт первого и потом
// видит его отметки. false — не дождался (ушёл контекст).
func (c *LazyCatalog) holdSweep(ctx context.Context, id kernel.TitleID) (release func(), ok bool) {
	for {
		c.mu.Lock()
		if c.sweeping == nil {
			c.sweeping = map[kernel.TitleID]chan struct{}{}
		}
		busy, taken := c.sweeping[id]
		if !taken {
			done := make(chan struct{})
			c.sweeping[id] = done
			c.mu.Unlock()
			return func() {
				c.mu.Lock()
				delete(c.sweeping, id)
				c.mu.Unlock()
				close(done)
			}, true
		}
		c.mu.Unlock()
		select {
		case <-busy:
		case <-ctx.Done():
			return nil, false
		}
	}
}
