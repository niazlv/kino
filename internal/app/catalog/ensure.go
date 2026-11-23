package catalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// EpisodeSource — та часть провайдера, что умеет отдать серии тайтла.
type EpisodeSource interface {
	Episodes(ctx context.Context, ref kernel.ProviderRef) ([]dp.Episode, error)
}

// Reader — чтение каталога.
type Reader interface {
	Title(ctx context.Context, id kernel.TitleID) (catalog.Title, error)
	Episode(ctx context.Context, id kernel.EpisodeID) (catalog.Episode, error)
	Episodes(ctx context.Context, id kernel.TitleID) ([]catalog.Episode, error)
	Search(ctx context.Context, q string, limit, offset int) ([]catalog.Title, int, error)
}

// EpisodeWriter записывает догруженные серии.
type EpisodeWriter interface {
	UpsertEpisodes(ctx context.Context, titleID kernel.TitleID, eps []dp.Episode) error
}

// TitleWriter кладёт в каталог тайтл, найденный живым поиском.
type TitleWriter interface {
	UpsertTitle(ctx context.Context, t dp.Title) (kernel.TitleID, error)
}

// LiveSearch — поиск на площадке. Ничего не знает о том, какая она:
// кто именно ищет, решает композиционный корень.
type LiveSearch func(ctx context.Context, q string, limit int) ([]dp.Title, error)

// LazyCatalog отдаёт каталог, догружая серии при первом обращении.
//
// Обход площадки собирает только метаданные: серии для всех 53 тысяч тайтлов
// это столько же лишних запросов и часы работы, при том что большинство
// тайтлов никто никогда не откроет. Серии подтягиваются, когда тайтл
// действительно открыли, и дальше лежат в каталоге — медленным будет только
// первый заход.
type LazyCatalog struct {
	Reader
	writer   EpisodeWriter
	provider func(providerID string) (EpisodeSource, error)
	titles   TitleWriter
	live     LiveSearch
	offers   OfferStatsReader

	discovery      LiveSearch
	discoveryIDs   map[string]bool
	discoveryMarks DiscoveryMarks

	lookup    IDLookup
	lookupIDs map[string]bool

	thumbSrc   ThumbnailSource
	thumbStore ThumbnailStore
	thumbBusy  map[kernel.EpisodeID]chan struct{}

	health ProviderHealth

	// stale — через сколько перечитывать серии незавершённого сериала при
	// открытии; 0 — стандартные сутки (staleAfter). См. WithStaleAfter.
	stale time.Duration
	// searchMem и searchRepeat — постоянная память о запросах. См. WithSearchMemory.
	searchMem         SearchMemory
	searchRepeat      time.Duration
	searchEmptyRepeat time.Duration

	mu    sync.Mutex
	tried map[kernel.ProviderRef]time.Time
	asked map[string]time.Time
	lives map[string]*liveRun
}

// liveRun — один поход на площадки за запросом. Параллельные запросы с той
// же строкой ждут его, а не идут вторыми и не отвечают пустотой.
type liveRun struct {
	done chan struct{}
	ids  []kernel.TitleID
	ok   bool
	at   time.Time
}

func anyEpisodeThumbnail(eps []dp.Episode) bool {
	for _, e := range eps {
		if e.Thumbnail != "" {
			return true
		}
	}
	return false
}

// hasAnyThumbnail: хотя бы один кадр означает, что тайтл уже подтягивали
// после появления этого поля. Проверять все — лишнее: они приходят пачкой.
func hasAnyThumbnail(eps []catalog.Episode) bool {
	for _, e := range eps {
		if e.Thumbnail != "" {
			return true
		}
	}
	return false
}

// namesKnown: хотя бы одна серия с известным названием (пусть и пустым)
// означает, что тайтл перечитывали после появления поля. Как и с кадрами,
// проверять все незачем — они приходят пачкой.
func namesKnown(eps []catalog.Episode) bool {
	for _, e := range eps {
		if e.TitleKnown {
			return true
		}
	}
	return false
}

func NewLazyCatalog(r Reader, w EpisodeWriter,
	provider func(string) (EpisodeSource, error)) *LazyCatalog {
	return &LazyCatalog{Reader: r, writer: w, provider: provider}
}

// WithLiveSearch включает поход на площадку, когда у нас ничего не нашлось.
func (c *LazyCatalog) WithLiveSearch(tw TitleWriter, ls LiveSearch) *LazyCatalog {
	c.titles, c.live = tw, ls
	return c
}

// SearchMemory — память о том, какие запросы уже спрашивали у площадок.
//
// Отметка «уже спрашивали» в самом каталоге живёт в оперативной памяти десять
// минут: серверу этого хватает, он не перезапускается. На устройстве — наоборот:
// процесс живёт недолго, а площадки бережём, поэтому то, что уже спрашивали,
// помнится на диске и переживает перезапуск.
type SearchMemory interface {
	// Asked — когда запрос последний раз успешно спрашивали у площадок и сколько
	// нашли; нуль — не спрашивали. Сколько нашли, нужно потому, что «ничего» помнится
	// короче, чем находки (см. WithEmptySearchRepeat).
	Asked(ctx context.Context, key string) (at time.Time, hits int, err error)
	MarkAsked(ctx context.Context, key string, at time.Time, hits int) error
}

// WithSearchMemory включает постоянную память о запросах: один и тот же
// запрос (после нормализации) на площадки не уходит, пока с прошлого раза не
// прошло repeat — даже если он ничего не нашёл и даже после перезапуска.
// Найденное при первом разе лежит в каталоге и отдаётся оттуда.
func (c *LazyCatalog) WithSearchMemory(m SearchMemory, repeat time.Duration) *LazyCatalog {
	c.searchMem, c.searchRepeat = m, repeat
	return c
}

// emptySearchRepeat — как долго помнится запрос, на который ничего не нашли,
// если не задано иное: полдня. Находки помнятся неделю, а пустота — недолго: один
// случайный пустой ответ площадки (антибот, лимит, сбой разбора) иначе закрыл бы
// запрос на всю неделю.
const emptySearchRepeat = 12 * time.Hour

// WithEmptySearchRepeat задаёт, сколько помнится запрос без находок (по умолчанию —
// не дольше emptySearchRepeat и не дольше обычного срока).
func (c *LazyCatalog) WithEmptySearchRepeat(d time.Duration) *LazyCatalog {
	c.searchEmptyRepeat = d
	return c
}

func (c *LazyCatalog) emptyRepeat() time.Duration {
	if c.searchEmptyRepeat > 0 {
		return c.searchEmptyRepeat
	}
	return min(c.searchRepeat, emptySearchRepeat)
}

// WithStaleAfter задаёт, через сколько часов при открытии тайтла перечитывать
// его серии у площадки (для незавершённого сериала). Сервер живёт на
// стандартных сутках; устройство ставит своё — по настройке человека.
func (c *LazyCatalog) WithStaleAfter(d time.Duration) *LazyCatalog {
	c.stale = d
	return c
}

func (c *LazyCatalog) staleAfterOrDefault() time.Duration {
	if c.stale > 0 {
		return c.stale
	}
	return staleAfter
}

// SearchPage — страница поиска для витрины: окно находок, общее число и
// где листать дальше.
//
// Next — именно позиция в каталоге, а не «сколько показано»: живой поиск
// добавляет находки площадок к первой странице СВЕРХ окна, и следующий
// запрос должен продолжать список каталога, а не перескакивать через
// найденное живым поиском.
type SearchPage struct {
	Items []catalog.Title
	Total int
	Next  int
	// HasMore — есть ли находки за концом страницы.
	HasMore bool
}

// Search ищет у нас, а если у нас нет подходящего — на площадках.
//
// Каталог обходится целиком редко (а новая площадка проходится впервые
// часами), и свежее могло к нам ещё не доехать. «Нет подходящего» — это
// не только пустой ответ: на «Принцесса Медуза» у нас найдутся другие
// принцессы, но ни одной, в чьём названии есть запрос. Живой поиск — по
// запросу к площадке, поэтому не чаще раза в несколько минут на один и тот
// же запрос: иначе каждое обновление страницы тратило бы общий с
// просмотром лимит kinopub. Найденное кладётся в каталог (склейка решает,
// новый это тайтл или уже известный) и отдаётся первым, уже с нашими id.
// Спрашивать площадки имеет смысл только на первой странице: дальше человек
// листает то, что уже найдено, и находки вдобавок сдвигали бы окно.
//
// Отказ площадки (нет сети, ограничение темпа) не превращается в ошибку:
// человек видит то, что нашлось у нас.
func (c *LazyCatalog) Search(ctx context.Context, q string, limit, offset int) (SearchPage, error) {
	local, total, err := c.Reader.Search(ctx, q, limit, offset)
	if err != nil {
		return SearchPage{}, err
	}
	page := SearchPage{Items: local, Total: total, Next: offset + len(local),
		HasMore: offset+len(local) < total}
	if offset > 0 || c.live == nil || c.titles == nil || relevant(local, q) {
		return page, nil
	}
	ids := c.liveSearch(ctx, q, limit)
	if len(ids) == 0 {
		return page, nil
	}
	out := make([]catalog.Title, 0, len(ids)+len(local))
	seen := map[kernel.TitleID]bool{}
	for _, id := range ids {
		if seen[id] {
			// Несколько площадок нашли одно произведение: склейка отдала
			// один и тот же тайтл, и дважды показывать его незачем.
			continue
		}
		seen[id] = true
		t, err := c.Reader.Title(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	// Первыми — находки, в чьём названии есть запрос: площадки ищут
	// нечётко, и по приоритету площадки вперёд вставали чужие «Любовь и
	// продюсер», а искомое оказывалось шестым.
	key := catalog.NormalizeName(q)
	sort.SliceStable(out, func(i, j int) bool {
		return nameHas(out[i], key) && !nameHas(out[j], key)
	})
	// Находки площадок считаются в общее число сверх каталога: цифра у
	// заголовка — сколько нашлось всего, включая досмотренное у площадок.
	for _, t := range local {
		if !seen[t.ID] {
			out = append(out, t)
		}
	}
	page.Items = out
	page.Total = total + (len(out) - len(local))
	return page, nil
}

// Suggest — подсказки под строкой поиска, пока человек набирает.
//
// Только наш каталог, без похода на площадки: запрос летит на каждую
// букву, и живой поиск тратил бы общий с просмотром лимит kinopub на «д»,
// «дю», «дюн». Чего у нас нет, найдёт сам поиск — по Enter.
func (c *LazyCatalog) Suggest(ctx context.Context, q string, limit int) ([]catalog.Title, error) {
	items, _, err := c.Reader.Search(ctx, q, limit, 0)
	return items, err
}

// liveTimeout — сколько идёт живой поиск вместе с записью находок.
const liveTimeout = 30 * time.Second

// liveSearch спрашивает площадки и кладёт находки в каталог; отдаёт их id.
//
// Поход не привязан к запросу человека. Страница витрины перезагружается
// (приложение, телевизор, «назад»), и обрыв соединения отменял бы и
// поиск, и запись находок, а отметка «уже спрашивали» держала бы следующие
// десять минут пустой ответ. Поэтому поход доводится до конца, находки ложатся в каталог, а запрос с той же
// строкой, пришедший раньше, чем он закончился, ждёт его.
func (c *LazyCatalog) liveSearch(ctx context.Context, q string, limit int) []kernel.TitleID {
	key := catalog.NormalizeName(q)
	// Этот запрос уже спрашивали у площадок не так давно — второй раз не идём,
	// сколько бы он ни дал в первый: найденное уже в каталоге.
	if c.searchMem != nil && key != "" {
		if at, hits, err := c.searchMem.Asked(ctx, key); err == nil && !at.IsZero() {
			ttl := c.searchRepeat
			if hits == 0 {
				ttl = c.emptyRepeat()
			}
			if time.Since(at) < ttl {
				return nil
			}
		}
	}
	c.mu.Lock()
	if c.lives == nil {
		c.lives = map[string]*liveRun{}
	}
	run, ok := c.lives[key]
	switch {
	case ok && run.at.IsZero():
		// Идёт прямо сейчас — ждём его.
	case ok && time.Since(run.at) < searchRepeat:
		c.mu.Unlock()
		return run.ids
	default:
		if len(c.lives) > 10000 {
			c.lives = map[string]*liveRun{}
		}
		run = &liveRun{done: make(chan struct{})}
		c.lives[key] = run
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveTimeout)
		go func() {
			defer cancel()
			ids, ok := c.liveNow(bg, q, limit)
			if ok && c.searchMem != nil && key != "" {
				// Площадки ответили (пусть и пустотой) — запомнить на диске.
				// Если не ответили (нет сети), не помним: иначе офлайн-запрос
				// пропал бы на неделю.
				_ = c.searchMem.MarkAsked(bg, key, time.Now(), len(ids))
			}
			c.mu.Lock()
			run.ids, run.ok, run.at = ids, ok, time.Now()
			if !ok {
				// Площадки не ответили — следующий запрос вправе спросить
				// снова, а не ждать десять минут.
				delete(c.lives, key)
			}
			c.mu.Unlock()
			close(run.done)
		}()
	}
	c.mu.Unlock()

	select {
	case <-run.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return run.ids
	case <-ctx.Done():
		return nil
	}
}

// liveNow — сам поход: находки площадок в каталог. ok — площадки ответили
// (пусть и пустотой), а не оборвались.
func (c *LazyCatalog) liveNow(ctx context.Context, q string, limit int) ([]kernel.TitleID, bool) {
	found, err := c.live(ctx, q, limit)
	if err != nil {
		return nil, false
	}
	var ids []kernel.TitleID
	for _, ft := range found {
		id, err := c.titles.UpsertTitle(ctx, ft)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, ctx.Err() == nil
}

func nameHas(t catalog.Title, key string) bool {
	return key != "" && strings.Contains(catalog.NormalizeName(t.Title), key)
}

// searchRepeat — как часто спрашивать площадки об одном и том же запросе.
const searchRepeat = 10 * time.Minute

// relevant — есть ли среди найденного у нас тайтл, в чьём названии есть
// запрос целиком (без учёта регистра, дефисов и «ё»).
func relevant(local []catalog.Title, q string) bool {
	key := catalog.NormalizeName(q)
	if key == "" {
		return true
	}
	for _, t := range local {
		if strings.Contains(catalog.NormalizeName(t.Title), key) {
			return true
		}
	}
	return false
}

// WithDiscovery включает досбор площадок при открытии тайтла.
//
// Аниме-тайтл, заведённый kinopub, получает аниме-площадки обходом — но
// первый обход новой площадки идёт часами, а у свежего тайтла её страница
// может появиться позже нашего прохода. При открытии такого тайтла площадки
// спрашиваются по названию, и найденное кладётся в каталог обычной
// склейкой: совпало — тайтл дополнился, серии подтянутся тут же. Спрашиваются
// только аниме-площадки (ids): лимит kinopub на это не тратится.
func (c *LazyCatalog) WithDiscovery(ls LiveSearch, marks DiscoveryMarks, ids ...string) *LazyCatalog {
	c.discovery, c.discoveryMarks = ls, marks
	c.discoveryIDs = map[string]bool{}
	for _, id := range ids {
		c.discoveryIDs[id] = true
	}
	return c
}

// DiscoveryMarks — где помнить, что площадки о тайтле уже спрашивали.
type DiscoveryMarks interface {
	MarkSourcesChecked(ctx context.Context, id kernel.TitleID, at time.Time) error
}

// discoverRepeat — не чаще, чем раз в столько, на один тайтл: у площадок
// его может просто не быть, и спрашивать при каждом открытии незачем.
// Остальное досбирает обход.
const discoverRepeat = 72 * time.Hour

// discoverTimeout — сколько идёт досбор. Страница его не ждёт: площадки без
// API медленные (страница animego.fr порой грузится секунды), и ожидание
// стоило секунд пустой страницы при почти нулевой пользе — так и было.
// Найденное досбирается в фоне вместе с сериями и видно при следующем
// открытии.
const (
	discoverTimeout = time.Minute
	discoverLimit   = 6
)

// IDLookup — произведение у площадок по внешним номерам. skip — площадки,
// у которых тайтл уже есть: спрашивать их незачем. Отказ площадки — не
// ошибка, а пустой ответ от неё.
type IDLookup func(ctx context.Context, ids dp.ExternalIDs, skip map[string]bool) []dp.Title

// WithIDLookup включает досбор площадок по номерам IMDb и Кинопоиска при
// открытии тайтла — любого, а не только аниме.
//
// Балансер (Alloha) ключует каталог номером Кинопоиска: спросить его о
// конкретном тайтле — один точный запрос, без склейки по названиям и без
// обхода его каталога целиком. Так у фильма, заведённого kinopub, появляется
// запасная площадка, как только его открыли. Спрашиваются только площадки
// ids — те, что умеют такой поиск.
func (c *LazyCatalog) WithIDLookup(fn IDLookup, ids ...string) *LazyCatalog {
	c.lookup = fn
	c.lookupIDs = map[string]bool{}
	for _, id := range ids {
		c.lookupIDs[id] = true
	}
	return c
}

// discover запускает досбор площадок тайтла в фоне, если он нужен.
func (c *LazyCatalog) discover(ctx context.Context, t catalog.Title) {
	c.discoverByIDs(ctx, t)
	c.discoverByName(ctx, t)
}

// discoverByIDs — досбор по внешним номерам.
//
// Отметка в базе (SourcesCheckedAt) здесь не нужна: запрос точный и
// дешёвый, и после перезапуска core спросить заново — один запрос на
// открытый тайтл. Зато площадка, подключённая позже, находится сразу, а не
// через трое суток после прошлого досбора аниме-площадок.
func (c *LazyCatalog) discoverByIDs(ctx context.Context, t catalog.Title) {
	if c.lookup == nil || c.titles == nil || len(c.lookupIDs) == 0 {
		return
	}
	ids := dp.ExternalIDs{
		IMDB: t.IDs.IMDB, Kinopoisk: t.IDs.Kinopoisk,
		Title: t.Title, Original: t.OriginalOf(), Year: t.Year, Kind: string(t.Kind),
	}
	if ids.Empty() {
		return
	}
	have := map[string]bool{}
	for _, s := range t.Sources {
		if c.lookupIDs[s.Ref.ProviderID] {
			have[s.Ref.ProviderID] = true
		}
	}
	if len(have) == len(c.lookupIDs) {
		return
	}
	if !c.firstAsk("ids:"+strconv.FormatInt(int64(t.ID), 10), discoverRepeat) {
		return
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoverTimeout)
	go func() {
		defer cancel()
		var refs []kernel.ProviderRef
		for _, ft := range c.lookup(bg, ids, have) {
			if !sameWork(ids, ft) {
				continue
			}
			if id, err := c.titles.UpsertTitle(bg, ft); err == nil && id == t.ID {
				refs = append(refs, ft.Ref)
			}
		}
		c.fetchEpisodes(bg, t.ID, refs)
	}()
}

// sameWork — находка по номеру действительно про наш тайтл: хотя бы один
// номер совпал, и ни один не противоречит. Площадка с чужим IMDb у того же
// номера Кинопоиска — ошибка в её данных; такой находке склейка поверила бы
// и привязала её к другому тайтлу, а то и завела бы новый.
func sameWork(ids dp.ExternalIDs, ft dp.Title) bool {
	if ft.IMDB != "" && ids.IMDB != "" && ft.IMDB != ids.IMDB {
		return false
	}
	if ft.Kinopoisk != "" && ids.Kinopoisk != "" && ft.Kinopoisk != ids.Kinopoisk {
		return false
	}
	return ft.IMDB != "" && ft.IMDB == ids.IMDB || ft.Kinopoisk != "" && ft.Kinopoisk == ids.Kinopoisk
}

// discoverByName — досбор аниме-площадок по названию.
func (c *LazyCatalog) discoverByName(ctx context.Context, t catalog.Title) {
	if c.discovery == nil || c.titles == nil || !catalog.IsAnime(t.Genres) {
		return
	}
	// Досбираем, пока у тайтла есть не все аниме-площадки: одна могла
	// найтись раньше (обходом или прошлым досбором), а другая — нет.
	have := map[string]bool{}
	for _, s := range t.Sources {
		if c.discoveryIDs[s.Ref.ProviderID] {
			have[s.Ref.ProviderID] = true
		}
	}
	if len(have) == len(c.discoveryIDs) {
		return
	}
	if !t.SourcesCheckedAt.IsZero() && time.Since(t.SourcesCheckedAt) < discoverRepeat {
		return
	}
	if !c.firstAsk("t:"+strconv.FormatInt(int64(t.ID), 10), discoverRepeat) {
		return
	}
	if c.discoveryMarks != nil {
		// Отмечаем до поиска: два открытия подряд не должны искать дважды,
		// а перезапуск core — начинать заново.
		_ = c.discoveryMarks.MarkSourcesChecked(ctx, t.ID, time.Now())
	}
	// Контекст запроса не годится: страница уйдёт раньше, чем досбор
	// закончится.
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoverTimeout)
	go func() {
		defer cancel()
		refs := c.discoverNow(bg, t, have)
		c.fetchEpisodes(bg, t.ID, refs)
	}()
}

// discoverNow ищет тайтл у площадок и отдаёт привязавшиеся элементы.
func (c *LazyCatalog) discoverNow(ctx context.Context, t catalog.Title, have map[string]bool) []kernel.ProviderRef {
	// Оригинальное название у аниме-площадок пишется одинаково чаще
	// русского; русское — второй попыткой.
	queries := []string{t.OriginalOf()}
	if ru, _, _ := strings.Cut(t.Title, " / "); ru != "" && ru != queries[0] {
		queries = append(queries, ru)
	}
	// Площадки ищут по-разному: одна находит по оригиналу, другая — только
	// по-русски. Поэтому второй запрос нужен, пока хоть одна не нашлась.
	attached := map[string]bool{}
	for id := range have {
		attached[id] = true
	}
	var refs []kernel.ProviderRef
	seen := map[kernel.ProviderRef]bool{}
	for _, q := range queries {
		if strings.TrimSpace(q) == "" || len(attached) == len(c.discoveryIDs) {
			continue
		}
		found, err := c.discovery(ctx, q, discoverLimit)
		if err != nil {
			continue
		}
		// Все найденные, а не до первого совпадения: у аниме-площадки
		// на тайтл по странице на сезон, и каждая из них — его часть.
		for _, ft := range found {
			if seen[ft.Ref] {
				// Та же страница нашлась и вторым запросом.
				continue
			}
			seen[ft.Ref] = true
			if id, err := c.titles.UpsertTitle(ctx, ft); err == nil && id == t.ID {
				attached[ft.Ref.ProviderID] = true
				refs = append(refs, ft.Ref)
			}
		}
	}
	return refs
}

// fetchEpisodes подтягивает серии привязавшихся площадок: без них площадка
// у тайтла есть, а сыграть с неё нечего до следующего открытия.
func (c *LazyCatalog) fetchEpisodes(ctx context.Context, id kernel.TitleID, refs []kernel.ProviderRef) {
	if c.provider == nil || c.writer == nil {
		return
	}
	for _, ref := range refs {
		p, err := c.provider(ref.ProviderID)
		if err != nil {
			continue
		}
		eps, err := p.Episodes(ctx, ref)
		if err != nil || len(eps) == 0 {
			continue
		}
		_ = c.writer.UpsertEpisodes(ctx, id, eps)
	}
}

// firstAsk — не спрашивали ли уже недавно; отмечает, что спросили.
func (c *LazyCatalog) firstAsk(key string, every time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.asked == nil {
		c.asked = map[string]time.Time{}
	}
	if at, ok := c.asked[key]; ok && time.Since(at) < every {
		return false
	}
	if len(c.asked) > 10000 {
		c.asked = map[string]time.Time{}
	}
	c.asked[key] = time.Now()
	return true
}

// OfferStat — что известно о сериях одной площадки у тайтла.
type OfferStat struct {
	Episodes int
	SeenAt   time.Time
}

// OfferStatsReader — сводка по площадкам тайтла.
type OfferStatsReader interface {
	OfferStats(ctx context.Context, id kernel.TitleID) (map[kernel.ProviderRef]OfferStat, error)
}

// WithOfferStats включает догрузку серий со ВСЕХ площадок тайтла, а не
// только с лучшей: площадки дополняют друг друга — у одной нет последней
// серии онгоинга, у другой нет первого сезона.
func (c *LazyCatalog) WithOfferStats(r OfferStatsReader) *LazyCatalog {
	c.offers = r
	return c
}

// staleAfter — через сколько перечитывать серии незавершённого сериала при
// открытии. Отслеживаемое добирает фоновый проход; это — для остального.
const staleAfter = 24 * time.Hour

// retryAfter — сколько не спрашивать площадку, не отдавшую серий. Иначе
// каждое открытие страницы тайтла шло бы на площадку заново.
const retryAfter = 10 * time.Minute

// fetchTimeout — сколько ждать одну площадку при открытии страницы.
// Страница с метаданными важнее полного списка серий.
const fetchTimeout = 12 * time.Second

// Episodes возвращает серии, догрузив их при отсутствии.
func (c *LazyCatalog) Episodes(ctx context.Context, id kernel.TitleID) ([]catalog.Episode, error) {
	eps, err := c.Reader.Episodes(ctx, id)
	if err != nil {
		return nil, err
	}
	t, err := c.Reader.Title(ctx, id)
	if err != nil {
		return nil, err
	}
	c.discover(ctx, t)

	refs := c.skipDown(c.refsToFetch(ctx, t, eps))
	if len(refs) == 0 {
		return eps, nil
	}

	// Площадки спрашиваются параллельно: страница ждёт самую медленную, а
	// не их сумму.
	type result struct {
		ref kernel.ProviderRef
		eps []dp.Episode
	}
	ch := make(chan result, len(refs))
	for _, ref := range refs {
		go func(ref kernel.ProviderRef) {
			p, err := c.provider(ref.ProviderID)
			if err != nil {
				ch <- result{ref: ref}
				return
			}
			fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
			defer cancel()
			fresh, err := p.Episodes(fctx, ref)
			c.noteOutcome(ctx, fctx, ref.ProviderID, err)
			if err != nil {
				fresh = nil
			}
			ch <- result{ref: ref, eps: fresh}
		}(ref)
	}
	wrote := false
	for range refs {
		res := <-ch
		// Не отдала серий — или отдала, но без кадров (у аниме-площадок
		// кадры приходят по запросу, а не со списком): в обоих случаях
		// перечитывать площадку на каждом открытии страницы незачем.
		if len(res.eps) == 0 || !anyEpisodeThumbnail(res.eps) {
			c.markTried(res.ref)
		}
		if len(res.eps) == 0 {
			continue
		}
		// Площадка недоступна — не ошибка страницы: она должна открыться
		// хотя бы с тем, что уже есть.
		if err := c.writer.UpsertEpisodes(ctx, id, res.eps); err != nil {
			return nil, err
		}
		wrote = true
	}
	if !wrote {
		return eps, nil
	}
	out, err := c.Reader.Episodes(ctx, id)
	if err != nil && !errors.Is(err, kernel.ErrNotFound) {
		return nil, err
	}
	return out, nil
}

// refsToFetch — у каких площадок тайтла спросить серии сейчас.
func (c *LazyCatalog) refsToFetch(ctx context.Context, t catalog.Title, eps []catalog.Episode) []kernel.ProviderRef {
	best, ok := t.BestSource()
	if !ok {
		return nil
	}
	var out []kernel.ProviderRef
	// Лучшая площадка перечитывается и когда серии сохранены без кадров
	// или без названий: и то и другое начали хранить позже, и иначе у
	// старых тайтлов они не появились бы никогда.
	if len(eps) == 0 || !hasAnyThumbnail(eps) || !namesKnown(eps) {
		out = append(out, best.Ref)
	}
	if c.offers == nil {
		return c.untried(out)
	}
	stats, err := c.offers.OfferStats(ctx, t.ID)
	if err != nil {
		return c.untried(out)
	}
	now := time.Now()
	for _, s := range t.Sources {
		if s.Disabled || s.Ref == best.Ref && len(out) > 0 {
			continue
		}
		st, known := stats[s.Ref]
		switch {
		case !known || st.Episodes == 0:
			out = append(out, s.Ref)
		case !t.Finished && now.Sub(st.SeenAt) > c.staleAfterOrDefault():
			out = append(out, s.Ref)
		}
	}
	return c.untried(out)
}

// ProviderHealth — какие площадки сейчас не отвечают (app/health).
type ProviderHealth interface {
	Down(id string) bool
	Failed(id string, cause error)
	Recovered(id string)
}

// WithHealth — не держать страницу тайтла ради площадки, которая только что
// не ответила: страница откроется с тем, что есть, а серии этой площадки
// догрузятся при следующем открытии, когда она оживёт. И наоборот: что
// промолчало странице, того не ждёт и плеер.
func (c *LazyCatalog) WithHealth(h ProviderHealth) *LazyCatalog {
	c.health = h
	return c
}

// noteOutcome сообщает памяти о площадках, чем кончился поход за сериями.
// Отказ засчитывается, только если площадка молчала или до неё не дошли:
// «нет такого тайтла» — не про её здоровье, а ушедший человек — не её вина.
func (c *LazyCatalog) noteOutcome(parent, fctx context.Context, id string, err error) {
	switch {
	case c.health == nil || parent.Err() != nil:
	case err == nil:
		c.health.Recovered(id)
	case errors.Is(fctx.Err(), context.DeadlineExceeded):
		c.health.Failed(id, fmt.Errorf("серии не пришли за %v: %w", fetchTimeout, err))
	case errors.Is(err, kernel.ErrUnreachable), errors.Is(err, kernel.ErrBusy), errors.Is(err, kernel.ErrReauth):
		c.health.Failed(id, err)
	}
}

// skipDown отбрасывает лежащие площадки. В «спрашивали впустую» их не
// записываем: они не отказали в сериях, их просто не спрашивали.
func (c *LazyCatalog) skipDown(refs []kernel.ProviderRef) []kernel.ProviderRef {
	if c.health == nil {
		return refs
	}
	out := refs[:0]
	for _, r := range refs {
		if !c.health.Down(r.ProviderID) {
			out = append(out, r)
		}
	}
	return out
}

// untried отбрасывает площадки, которые недавно спрашивали впустую.
func (c *LazyCatalog) untried(refs []kernel.ProviderRef) []kernel.ProviderRef {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := refs[:0]
	for _, r := range refs {
		if at, ok := c.tried[r]; ok && time.Since(at) < retryAfter {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (c *LazyCatalog) markTried(r kernel.ProviderRef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tried == nil {
		c.tried = map[kernel.ProviderRef]time.Time{}
	}
	c.tried[r] = time.Now()
}

// ThumbnailSource — кадр серии у площадки, её номерами.
type ThumbnailSource func(ctx context.Context, ref kernel.ProviderRef, season, episode int) (string, error)

// ThumbnailStore — где кадры живут дальше: найденный однажды больше не
// спрашивается у площадки.
type ThumbnailStore interface {
	Offers(ctx context.Context, id kernel.EpisodeID) ([]catalog.Offer, error)
	SetEpisodeThumbnail(ctx context.Context, id kernel.EpisodeID, url string) error
}

// WithThumbnails включает кадры серий по запросу.
//
// Аниме-площадки отдают кадр отдельным запросом на серию (у CVH он в
// описании видео), и собирать их вместе со списком серий значило бы на
// длинном сериале сотни запросов при открытии страницы. Поэтому кадр
// спрашивается, когда его показывают, и запоминается в каталоге.
func (c *LazyCatalog) WithThumbnails(src ThumbnailSource, store ThumbnailStore) *LazyCatalog {
	c.thumbSrc, c.thumbStore = src, store
	return c
}

// thumbRetry — через сколько снова спрашивать кадр, которого не нашлось.
const thumbRetry = 6 * time.Hour

// EpisodeThumbnail — адрес кадра серии: известный или найденный сейчас.
func (c *LazyCatalog) EpisodeThumbnail(ctx context.Context, id kernel.EpisodeID) (string, error) {
	e, err := c.Reader.Episode(ctx, id)
	if err != nil {
		return "", err
	}
	if e.Thumbnail != "" || c.thumbSrc == nil || c.thumbStore == nil {
		if e.Thumbnail == "" {
			return "", kernel.ErrNotFound
		}
		return e.Thumbnail, nil
	}

	// Карточка серии и постер плеера просят один и тот же кадр разом:
	// второй ждёт первого, а не идёт на площадку повторно.
	c.mu.Lock()
	if c.thumbBusy == nil {
		c.thumbBusy = map[kernel.EpisodeID]chan struct{}{}
	}
	if wait, busy := c.thumbBusy[id]; busy {
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if e, err := c.Reader.Episode(ctx, id); err == nil && e.Thumbnail != "" {
			return e.Thumbnail, nil
		}
		return "", kernel.ErrNotFound
	}
	done := make(chan struct{})
	c.thumbBusy[id] = done
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.thumbBusy, id)
		c.mu.Unlock()
		close(done)
	}()

	if !c.firstAsk("thumb:"+strconv.FormatInt(int64(id), 10), thumbRetry) {
		return "", kernel.ErrNotFound
	}
	offers, err := c.thumbStore.Offers(ctx, id)
	if err != nil {
		return "", err
	}
	for _, o := range catalog.Rank(offers, catalog.Choice{}) {
		if c.health != nil && c.health.Down(o.Ref.ProviderID) {
			continue
		}
		// Кадр ждут карточка и постер плеера: площадка, молчащая дольше
		// fetchTimeout, не должна держать картинку минутами.
		tctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		url, err := c.thumbSrc(tctx, o.Ref, o.SeasonNo, o.EpisodeNo)
		cancel()
		if err != nil || url == "" {
			continue
		}
		if err := c.thumbStore.SetEpisodeThumbnail(ctx, id, url); err != nil {
			return "", err
		}
		return url, nil
	}
	return "", kernel.ErrNotFound
}
