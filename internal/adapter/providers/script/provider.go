package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/niazleushkin/kino/internal/adapter/providers/sitekit"
	"github.com/niazleushkin/kino/internal/adapter/providers/source"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

type prov = appprov.Provider

// poolSize — сколько вызовов одной площадки исполняются одновременно.
// Больше не нужно: площадку и так не долбят залпом (пауза на хост).
const poolSize = 4

// runner — пул исполнителей скрипта одной площадки.
type runner struct {
	pack *Pack
	h    *host
	free chan *vm
	made chan struct{} // жетоны на создание: не больше poolSize
}

func newRunner(p *Pack, env source.Env) *runner {
	r := &runner{pack: p, h: newHost(p, env), free: make(chan *vm, poolSize), made: make(chan struct{}, poolSize)}
	for range poolSize {
		r.made <- struct{}{}
	}
	return r
}

func (r *runner) acquire(ctx context.Context) (*vm, error) {
	select {
	case v := <-r.free:
		return v, nil
	default:
	}
	select {
	case v := <-r.free:
		return v, nil
	case <-r.made:
		v, err := newVM(r.pack, r.h, nil)
		if err != nil {
			r.made <- struct{}{}
			return nil, err
		}
		return v, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// call — метод площадки, ответ разобран в dst (JSON-форма значения).
func (r *runner) call(ctx context.Context, dst any, method string, args ...any) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	v, err := r.acquire(ctx)
	if err != nil {
		return err
	}
	defer func() { r.free <- v }()
	res, err := v.call(ctx, method, args...)
	if err != nil {
		return err
	}
	if dst == nil || res == nil || goja.IsUndefined(res) || goja.IsNull(res) {
		if dst != nil {
			return fmt.Errorf("%s.%s вернул пусто: %w", r.pack.Meta.ID, method, kernel.ErrNotFound)
		}
		return nil
	}
	raw, err := json.Marshal(exportPlain(res.Export()))
	if err != nil {
		return fmt.Errorf("%s.%s: ответ: %w", r.pack.Meta.ID, method, err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%s.%s: ответ не той формы: %v: %w", r.pack.Meta.ID, method, err, kernel.ErrUnavailable)
	}
	return nil
}

// New собирает провайдера площадки поверх окружения её маршрута.
func (p *Pack) New(env source.Env) (prov, error) {
	for _, s := range p.Meta.Settings {
		if s.Required && env.Setting(s.Env) == "" {
			return nil, fmt.Errorf("нужна настройка %s (%s)", s.Env, s.Label)
		}
	}
	r := newRunner(p, env)
	if p.Players() {
		pl, err := currentPlayers()
		if err != nil {
			return nil, fmt.Errorf("%s: путь players: %w", p.Meta.ID, err)
		}
		return &siteProvider{SiteProvider: pl.NewSite(env.HTTP, sitekit.SiteOptions{
			ID: p.Meta.ID, Name: p.Meta.Name, Version: p.Meta.Version, Home: p.Meta.Home,
			General: p.Meta.Kind == string(source.KindGeneral),
			Thumbs:  p.Meta.Thumbs, Subtitles: p.Meta.Subtitles,
			Gap: time.Duration(p.Meta.GapMS) * time.Millisecond,
		}, siteScript{r}), r: r}, nil
	}
	return &Provider{r: r, meta: p.Meta}, nil
}

// ── путь «свои потоки» ─────────────────────────────────────────────────

// Provider — площадка-скрипт, сама отдающая потоки.
type Provider struct {
	r    *runner
	meta Meta
}

func (p *Provider) Describe(ctx context.Context) (dp.Descriptor, error) {
	if err := health(ctx, p.r); err != nil {
		return dp.Descriptor{}, err
	}
	caps := []dp.Capability{dp.CapSearch, dp.CapHLS, dp.CapProgressive}
	if p.r.pack.has("voices") {
		caps = append(caps, dp.CapVariants, dp.CapVoices)
	}
	if p.r.pack.has("lookup") {
		caps = append(caps, dp.CapLookup)
	}
	if p.r.pack.has("changed") {
		caps = append(caps, dp.CapSyncDelta)
	}
	if p.meta.Subtitles {
		caps = append(caps, dp.CapSubtitles)
	}
	return dp.Descriptor{APIMajor: dp.APIMajor, ProviderID: p.meta.ID, ProviderVersion: p.meta.Version,
		Name: p.meta.Name, Capabilities: caps}, nil
}

// health — живость реальным запросом: метод health() скрипта или GET home.
func health(ctx context.Context, r *runner) error {
	if r.pack.has("health") {
		return r.call(ctx, nil, "health")
	}
	resp, err := r.h.c.Get(ctx, r.pack.Meta.Home, map[string]string{"Accept": "text/html, application/json"})
	if err != nil {
		return err
	}
	if resp.Status >= 400 {
		return fmt.Errorf("%s: проверка живости HTTP %d: %w", r.pack.Meta.Name, resp.Status, kernel.ErrUnavailable)
	}
	return nil
}

func (p *Provider) Search(ctx context.Context, query string, limit int) ([]dp.Title, error) {
	return search(ctx, p.r, p.meta, query, limit)
}

func search(ctx context.Context, r *runner, m Meta, query string, limit int) ([]dp.Title, error) {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	var got []jsTitle
	if err := r.call(ctx, &got, "search", query, limit); err != nil {
		if errors.Is(err, kernel.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]dp.Title, 0, len(got))
	for _, t := range got {
		if t.ID == "" {
			continue
		}
		out = append(out, t.title(m))
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (p *Provider) Title(ctx context.Context, ref kernel.ProviderRef) (dp.Title, error) {
	var t jsTitle
	if err := p.r.call(ctx, &t, "title", ref.ItemID); err != nil {
		return dp.Title{}, err
	}
	// Номер — тот, по которому спросили: скрипт мог отдать его иначе
	// записанным (числом, с ведущими нулями), а привязка уже есть.
	t.ID = str(ref.ItemID)
	return t.title(p.meta), nil
}

func (p *Provider) Episodes(ctx context.Context, ref kernel.ProviderRef) ([]dp.Episode, error) {
	var got []jsEpisode
	if err := p.r.call(ctx, &got, "episodes", ref.ItemID); err != nil {
		return nil, err
	}
	out := make([]dp.Episode, 0, len(got))
	seen := map[[2]int]bool{}
	for _, e := range got {
		k := [2]int{e.Season, e.Episode}
		if seen[k] || e.Episode < 0 {
			continue
		}
		seen[k] = true
		out = append(out, dp.Episode{Ref: ref, SeasonNo: e.Season, EpisodeNo: e.Episode, Title: e.Title,
			DurationSec: e.Duration, DurationApprox: e.Approx, Thumbnail: e.Thumb})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SeasonNo != out[j].SeasonNo {
			return out[i].SeasonNo < out[j].SeasonNo
		}
		return out[i].EpisodeNo < out[j].EpisodeNo
	})
	return out, nil
}

func (p *Provider) ChangedSince(ctx context.Context, since time.Time, fn func(dp.Title) error) error {
	return changed(ctx, p.r, p.meta, since, fn)
}

// changed — обход площадки для сервера: changed(since, emit). Нет метода —
// площадка живёт только поиском (как движок на устройстве всегда).
func changed(ctx context.Context, r *runner, m Meta, since time.Time, fn func(dp.Title) error) error {
	if !r.pack.has("changed") {
		return nil
	}
	// Обход идёт минутами (полный каталог — тысячи тайтлов, на каждый —
	// серии), поэтому общий срок вызова ему не подходит: срок — на простой
	// скрипта. Он сбрасывается каждой находкой, а пока платформа сохраняет
	// находку и спрашивает её серии (fn), часы стоят: это не время скрипта.
	// Зависший скрипт (бесконечный цикл, вечный запрос) по-прежнему прерван.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle := time.AfterFunc(callTimeout, cancel)
	defer idle.Stop()
	v, err := r.acquire(ctx)
	if err != nil {
		return err
	}
	defer func() { r.free <- v }()
	var fnErr error
	emit := func(c goja.FunctionCall) goja.Value {
		if fnErr != nil {
			// Платформа сказала «хватит» (потолок обхода, остановка) — скрипт
			// не ходит дальше по страницам впустую.
			panic(v.rt.NewGoError(fnErr))
		}
		var t jsTitle
		raw, _ := json.Marshal(exportPlain(c.Argument(0).Export()))
		if err := json.Unmarshal(raw, &t); err != nil || t.ID == "" {
			return goja.Undefined()
		}
		idle.Stop()
		fnErr = fn(t.title(m))
		idle.Reset(callTimeout)
		if fnErr != nil {
			panic(v.rt.NewGoError(fnErr))
		}
		return goja.Undefined()
	}
	sinceArg := ""
	if !since.IsZero() {
		sinceArg = since.UTC().Format(time.RFC3339)
	}
	_, err = v.call(ctx, "changed", sinceArg, emit)
	if fnErr != nil {
		return fnErr
	}
	return err
}

func (p *Provider) Resolve(ctx context.Context, req dp.ResolveRequest) (dp.Source, error) {
	var st jsStream
	if err := p.r.call(ctx, &st, "resolve", resolveArg(req)); err != nil {
		return dp.Source{}, err
	}
	return st.source(time.Now())
}

func (p *Provider) Voices(ctx context.Context, ref kernel.ProviderRef, season, episode int) ([]dp.Voice, error) {
	var got []jsVariant
	if err := p.r.call(ctx, &got, "voices", map[string]any{"id": ref.ItemID, "season": season, "episode": episode}); err != nil {
		return nil, err
	}
	out := make([]dp.Voice, 0, len(got))
	for _, v := range got {
		out = append(out, dp.Voice{Title: v.Title, Variant: string(v.ID), Player: v.Player})
	}
	return out, nil
}

func (p *Provider) Lookup(ctx context.Context, ids dp.ExternalIDs) (dp.Title, error) {
	return lookup(ctx, p.r, p.meta, ids)
}

func lookup(ctx context.Context, r *runner, m Meta, ids dp.ExternalIDs) (dp.Title, error) {
	var t jsTitle
	err := r.call(ctx, &t, "lookup", map[string]any{
		"imdb": ids.IMDB, "kp": ids.Kinopoisk, "title": ids.Title, "original": ids.Original,
		"year": ids.Year, "kind": ids.Kind,
	})
	if err != nil {
		return dp.Title{}, err
	}
	if t.ID == "" {
		return dp.Title{}, fmt.Errorf("%s.lookup: %w", m.ID, kernel.ErrNotFound)
	}
	return t.title(m), nil
}

func resolveArg(req dp.ResolveRequest) map[string]any {
	return map[string]any{
		"id": req.Ref.ItemID, "season": req.Season, "episode": req.Episode,
		"variant": req.Variant, "variantLabel": req.VariantLabel, "maxHeight": req.Client.MaxHeight,
	}
}

// ── путь «чужие плееры» ────────────────────────────────────────────────

// siteProvider — площадка общего кода чужих плееров (Players.NewSite)
// поверх скрипта, плюс досбор по номерам, если скрипт его умеет.
type siteProvider struct {
	SiteProvider
	r *runner
}

func (p *siteProvider) Describe(ctx context.Context) (dp.Descriptor, error) {
	if p.r.pack.has("health") {
		if err := health(ctx, p.r); err != nil {
			return dp.Descriptor{}, err
		}
	}
	d, err := p.SiteProvider.Describe(ctx)
	if err == nil && p.r.pack.has("lookup") {
		d.Capabilities = append(d.Capabilities, dp.CapLookup)
	}
	return d, err
}

func (p *siteProvider) Lookup(ctx context.Context, ids dp.ExternalIDs) (dp.Title, error) {
	if !p.r.pack.has("lookup") {
		return dp.Title{}, kernel.ErrNotFound
	}
	t, err := lookup(ctx, p.r, p.r.pack.Meta, ids)
	if err != nil {
		return dp.Title{}, err
	}
	// Тайтл в форме общего кода (жанр «Аниме», сезон) — через страницу.
	return p.SiteProvider.Title(ctx, t.Ref)
}

// siteScript — скрипт как sitekit.Site.
type siteScript struct{ r *runner }

func (s siteScript) Page(ctx context.Context, id string) (sitekit.SitePage, error) {
	var t jsTitle
	if err := s.r.call(ctx, &t, "title", id); err != nil {
		return sitekit.SitePage{}, err
	}
	t.ID = str(id)
	return t.page(), nil
}

func (s siteScript) Search(ctx context.Context, query string, limit int) ([]sitekit.SitePage, error) {
	var got []jsTitle
	if err := s.r.call(ctx, &got, "search", query, limit); err != nil {
		if errors.Is(err, kernel.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]sitekit.SitePage, 0, len(got))
	for _, t := range got {
		if t.ID != "" {
			out = append(out, t.page())
		}
	}
	return out, nil
}

func (s siteScript) Walk(ctx context.Context, full bool, fn func(sitekit.SitePage) error) error {
	since := time.Time{}
	if !full {
		since = time.Now().Add(-24 * time.Hour)
	}
	return changed(ctx, s.r, s.r.pack.Meta, since, func(t dp.Title) error {
		return fn(sitekit.SitePage{ID: t.Ref.ItemID, Title: t.Title, Original: t.Original, AltTitles: t.AltTitles,
			Season: t.SeasonNo, Movie: t.Kind == "movie", Year: t.Year, Poster: t.Poster, Plot: t.Plot,
			Genres: t.Genres, Countries: t.Countries, MAL: t.MAL, KP: t.Kinopoisk, IMDB: t.IMDB,
			Finished: t.Finished, Popularity: t.Popularity})
	})
}

func (s siteScript) Players(ctx context.Context, pg sitekit.SitePage, episode int) (sitekit.SitePlayers, error) {
	var pl jsPlayers
	if err := s.r.call(ctx, &pl, "players", pg.ID, episode); err != nil {
		return sitekit.SitePlayers{}, err
	}
	out := sitekit.SitePlayers{Kodik: httpsURL(pl.Kodik), Alloha: httpsURL(pl.Alloha), Referer: pl.Referer}
	if pl.CVH != nil {
		out.CVHPub, out.MAL = string(pl.CVH.Pub), string(pl.CVH.MAL)
	}
	if out.Kodik == "" && out.Alloha == "" && out.CVHPub == "" {
		return out, fmt.Errorf("%s: у серии %d нет плееров: %w", s.r.pack.Meta.ID, episode, kernel.ErrNotFound)
	}
	return out, nil
}

func httpsURL(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

// ── формы ответов скрипта ──────────────────────────────────────────────

// str — строка, которую скрипт вправе отдать и числом (id, kp, mal).
type str string

func (s *str) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var x string
		if err := json.Unmarshal(b, &x); err != nil {
			return err
		}
		*s = str(strings.TrimSpace(x))
		return nil
	}
	if string(b) == "null" {
		*s = ""
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = str(n.String())
	return nil
}

type jsTitle struct {
	ID         str            `json:"id"`
	URL        string         `json:"url"`
	Kind       string         `json:"kind"`
	Movie      bool           `json:"movie"`
	Title      string         `json:"title"`
	Original   string         `json:"original"`
	Alt        []string       `json:"alt"`
	Season     int            `json:"season"`
	Year       int            `json:"year"`
	Plot       string         `json:"plot"`
	Poster     string         `json:"poster"`
	Genres     []string       `json:"genres"`
	Countries  []string       `json:"countries"`
	Finished   bool           `json:"finished"`
	IMDB       str            `json:"imdb"`
	KP         str            `json:"kp"`
	MAL        str            `json:"mal"`
	Aired      int            `json:"aired"`
	Episodes   []int          `json:"episodes"`
	Minutes    int            `json:"minutes"`
	Durations  map[string]int `json:"durations"`
	Popularity int64          `json:"popularity"`
	Created    string         `json:"created"`
	Updated    string         `json:"updated"`
}

func (t jsTitle) movie() bool { return t.Movie || t.Kind == "movie" }

func (t jsTitle) title(m Meta) dp.Title {
	kind := "series"
	if t.movie() {
		kind = "movie"
	}
	genres := t.Genres
	if m.Kind == string(source.KindAnime) && !hasFold(genres, "Аниме") {
		genres = append([]string{"Аниме"}, genres...)
	}
	return dp.Title{
		Ref:  kernel.ProviderRef{ProviderID: m.ID, ItemID: string(t.ID)},
		Kind: kind, IMDB: imdb(string(t.IMDB)), Kinopoisk: digits(string(t.KP)), MAL: digits(string(t.MAL)),
		Title: t.Title, Original: t.Original, AltTitles: t.Alt, SeasonNo: t.Season, Year: t.Year,
		Plot: t.Plot, Genres: genres, Countries: t.Countries, Poster: t.Poster, Finished: t.Finished,
		CreatedAt: when(t.Created), UpdatedAt: when(t.Updated), Popularity: t.Popularity,
	}
}

func (t jsTitle) page() sitekit.SitePage {
	secs := map[int]int{}
	for k, v := range t.Durations {
		if n, err := strconv.Atoi(k); err == nil && v > 0 {
			secs[n] = v
		}
	}
	return sitekit.SitePage{
		ID: string(t.ID), URL: t.URL, Title: t.Title, Season: t.Season, Original: t.Original,
		AltTitles: t.Alt, Movie: t.movie(), Aired: t.Aired, Finished: t.Finished, Genres: t.Genres,
		Countries: t.Countries, Year: t.Year, Poster: t.Poster, Plot: t.Plot, Minutes: t.Minutes,
		MAL: digits(string(t.MAL)), KP: digits(string(t.KP)), IMDB: imdb(string(t.IMDB)),
		EpisodeNums: t.Episodes, EpisodeSecs: secs, Popularity: t.Popularity,
	}
}

type jsEpisode struct {
	Season   int    `json:"season"`
	Episode  int    `json:"episode"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
	Approx   bool   `json:"approx"`
	Thumb    string `json:"thumb"`
}

type jsVariant struct {
	ID     str    `json:"id"`
	Title  string `json:"title"`
	Player string `json:"player"`
}

type jsPlayers struct {
	Kodik  string `json:"kodik"`
	Alloha string `json:"alloha"`
	CVH    *struct {
		Pub str `json:"pub"`
		MAL str `json:"mal"`
	} `json:"cvh"`
	Referer string `json:"referer"`
}

type jsStream struct {
	Protocol  string            `json:"protocol"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Origins   []string          `json:"origins"`
	Expires   str               `json:"expires"`
	TTL       int               `json:"ttl"`
	IPBound   bool              `json:"ipBound"`
	Variants  []jsVariant       `json:"variants"`
	Variant   str               `json:"variant"`
	Heights   []int             `json:"heights"`
	DropHosts []string          `json:"dropHosts"`
	CacheKey  string            `json:"cacheKey"`
	Audios    []struct {
		ID       str    `json:"id"`
		Language string `json:"language"`
		Title    string `json:"title"`
		Codec    string `json:"codec"`
	} `json:"audios"`
	Subtitles []struct {
		URL      string `json:"url"`
		Language string `json:"language"`
		Title    string `json:"title"`
		Format   string `json:"format"`
		Default  bool   `json:"default"`
	} `json:"subtitles"`
	Segments []struct {
		Kind  string  `json:"kind"`
		Start float64 `json:"start"` // секунды
		End   float64 `json:"end"`
	} `json:"segments"`
}

// source — поток в модель платформы. Без origins — домен адреса потока;
// без срока — шесть часов (потом тикет обновится резолвом).
func (st jsStream) source(now time.Time) (dp.Source, error) {
	u, err := url.Parse(st.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return dp.Source{}, fmt.Errorf("resolve: адрес потока %q — не http(s): %w", st.URL, kernel.ErrUnavailable)
	}
	proto := strings.ToUpper(st.Protocol)
	if proto == "" {
		proto = "PROGRESSIVE"
		if strings.Contains(strings.ToLower(u.Path), ".m3u8") {
			proto = "HLS"
		}
	}
	if proto == "MP4" {
		proto = "PROGRESSIVE"
	}
	if proto != "HLS" && proto != "PROGRESSIVE" {
		return dp.Source{}, fmt.Errorf("resolve: protocol %q — ждём HLS или PROGRESSIVE: %w", st.Protocol, kernel.ErrUnavailable)
	}
	origins := st.Origins
	if len(origins) == 0 {
		origins = []string{sitekit.OriginOf(st.URL)}
	}
	exp := when(string(st.Expires))
	if exp.IsZero() {
		ttl := time.Duration(st.TTL) * time.Second
		if ttl <= 0 {
			ttl = 6 * time.Hour
		}
		exp = now.Add(ttl)
	}
	src := dp.Source{
		Protocol: proto, URL: st.URL, Headers: st.Headers, AllowedOrigins: origins, ExpiresAt: exp,
		IPBound: st.IPBound, CacheKeyPattern: st.CacheKey, Passthrough: true, DropHosts: st.DropHosts,
		Variant: string(st.Variant),
	}
	if st.CacheKey != "" {
		if _, err := regexp.Compile(st.CacheKey); err != nil {
			return dp.Source{}, fmt.Errorf("resolve: cacheKey: %v: %w", err, kernel.ErrUnavailable)
		}
	}
	for _, v := range st.Variants {
		src.Variants = append(src.Variants, dp.Variant{ID: string(v.ID), Title: v.Title, Player: v.Player})
	}
	for _, a := range st.Audios {
		src.Audios = append(src.Audios, dp.AudioTrack{ID: string(a.ID), Language: a.Language, Title: a.Title, Codec: a.Codec})
	}
	for i, t := range st.Subtitles {
		format := t.Format
		if format == "" {
			format = "vtt"
		}
		src.Subtitles = append(src.Subtitles, dp.TextTrack{ID: strconv.Itoa(i), Language: t.Language, Title: t.Title,
			URL: t.URL, Format: format, Sidecar: t.URL != "", Default: t.Default})
		if o := sitekit.OriginOf(t.URL); o != "" && !contains(src.AllowedOrigins, o) {
			src.AllowedOrigins = append(src.AllowedOrigins, o)
		}
	}
	for _, h := range st.Heights {
		src.Renditions = append(src.Renditions, dp.Rendition{Height: h})
	}
	for _, s := range st.Segments {
		kind := dp.SegmentKind(s.Kind)
		if kind == "" {
			kind = dp.SegSkip
		}
		if s.End > s.Start {
			src.Segments = append(src.Segments, dp.Segment{Kind: kind, StartMS: int64(s.Start * 1000), EndMS: int64(s.End * 1000)})
		}
	}
	return src, nil
}

var digitsRe = regexp.MustCompile(`\d+`)

func digits(s string) string { return digitsRe.FindString(s) }

// imdb — номер IMDb в канонической форме tt0898266: скрипт вправе отдать и
// голое число (так его отдаёт kinopub).
func imdb(s string) string {
	d := digits(s)
	if d == "" {
		return ""
	}
	for len(d) < 7 {
		d = "0" + d
	}
	return "tt" + d
}

func when(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, f := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func hasFold(xs []string, x string) bool {
	for _, v := range xs {
		if strings.EqualFold(v, x) {
			return true
		}
	}
	return false
}
