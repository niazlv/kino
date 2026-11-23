// Package providertest — исполняемый контракт площадки (ADR-0001, ADR-0017):
// один и тот же набор проверок для площадки на Go, площадки-скрипта и
// правки, присланной человеком.
//
// Проверки живые — по настоящей площадке: поиск находит, тайтл открывается,
// серии есть и без повторов, резолв отдаёт поток с адресом, сроком и
// разрешёнными доменами, а заявленные возможности (озвучки, поиск по
// номерам, кадры) действительно реализованы. Без сети проверяется только
// честность описания.
//
// Запуск — `go run ./cmd/sourcekit check <id|файл.js> -q "запрос"`, из
// живого теста площадки (Run + Report.Failed) или с телефона: движок
// проверяет площадку той же функцией (POST /engine/sources/{id}/check).
package providertest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Case — что проверять.
type Case struct {
	// ID — какой id площадка обязана назвать в Describe.
	ID string
	// Query — запрос, на который у площадки точно есть находки.
	Query string
	// Want — подстрока названия, которая должна быть среди находок (без
	// учёта регистра); пусто — годится любая находка.
	Want string
	// Fetch — клиент, которым скачать начало потока (плейлист или первые
	// байты файла); nil — поток не трогать. Сегменты на устройстве идут
	// через шлюз, но адрес должен открываться и напрямую с теми же
	// заголовками — иначе шлюз тоже не откроет.
	Fetch *http.Client
	// Timeout — потолок одного шага; 0 — 60 с.
	Timeout time.Duration
}

// Step — результат одного шага.
type Step struct {
	Name    string        `json:"name"`
	OK      bool          `json:"ok"`
	Skipped bool          `json:"skipped,omitempty"`
	Warn    bool          `json:"warn,omitempty"` // не провал, но стоит посмотреть
	Detail  string        `json:"detail,omitempty"`
	Took    time.Duration `json:"took"`
}

// Report — все шаги по порядку.
type Report struct {
	Provider string    `json:"provider"`
	Version  string    `json:"version,omitempty"`
	At       time.Time `json:"at"`
	Steps    []Step    `json:"steps"`
}

// Failed — хоть один шаг провален.
func (r Report) Failed() bool {
	for _, s := range r.Steps {
		if !s.OK && !s.Skipped && !s.Warn {
			return true
		}
	}
	return false
}

// String — отчёт для человека: по строке на шаг.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "площадка %s %s — %s\n", r.Provider, r.Version, r.At.Format("2006-01-02 15:04"))
	for _, s := range r.Steps {
		mark := "ok  "
		switch {
		case s.Skipped:
			mark = "--  "
		case s.Warn:
			mark = "warn"
		case !s.OK:
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "  %s %-22s %6s  %s\n", mark, s.Name, s.Took.Round(time.Millisecond), s.Detail)
	}
	if r.Failed() {
		b.WriteString("ИТОГ: есть провалы — площадку в таком виде не принимаем\n")
	} else {
		b.WriteString("ИТОГ: годится\n")
	}
	return b.String()
}

type runner struct {
	ctx context.Context
	c   Case
	r   *Report
}

func (x *runner) step(name string, fn func(ctx context.Context) (string, error)) bool {
	ctx, cancel := context.WithTimeout(x.ctx, x.c.Timeout)
	defer cancel()
	start := time.Now()
	detail, err := fn(ctx)
	s := Step{Name: name, OK: err == nil, Detail: detail, Took: time.Since(start)}
	var w warn
	if errors.As(err, &w) {
		s.OK, s.Warn, s.Detail = false, true, w.Error()
	} else if err != nil {
		s.Detail = err.Error()
	}
	x.r.Steps = append(x.r.Steps, s)
	return err == nil || s.Warn
}

func (x *runner) skip(name, why string) {
	x.r.Steps = append(x.r.Steps, Step{Name: name, Skipped: true, Detail: why})
}

// warn — замечание, а не провал.
type warn struct{ msg string }

func (w warn) Error() string { return w.msg }

func warnf(f string, a ...any) error { return warn{fmt.Sprintf(f, a...)} }

// Run гоняет проверки по живой площадке.
func Run(ctx context.Context, p appprov.Provider, c Case) Report {
	if c.Timeout == 0 {
		c.Timeout = 60 * time.Second
	}
	rep := Report{Provider: c.ID, At: time.Now()}
	x := &runner{ctx: ctx, c: c, r: &rep}

	var d dp.Descriptor
	if !x.step("describe", func(ctx context.Context) (string, error) {
		var err error
		d, err = p.Describe(ctx)
		if err != nil {
			return "", err
		}
		rep.Version = d.ProviderVersion
		return describeIssues(p, d, c.ID)
	}) {
		return rep
	}
	if c.Query == "" {
		x.skip("search", "не задан запрос (-q)")
		return rep
	}

	var hits []dp.Title
	if !x.step("search", func(ctx context.Context) (string, error) {
		var err error
		hits, err = p.Search(ctx, c.Query, 5)
		if err != nil {
			return "", err
		}
		if len(hits) == 0 {
			return "", fmt.Errorf("по запросу %q ничего не найдено", c.Query)
		}
		names := make([]string, 0, len(hits))
		for _, t := range hits {
			if err := titleIssues(t, d.ProviderID); err != nil {
				return "", err
			}
			names = append(names, t.Title)
		}
		if c.Want != "" {
			found := -1
			for i, t := range hits {
				if strings.Contains(strings.ToLower(t.Title+" "+t.Original), strings.ToLower(c.Want)) {
					found = i
					break
				}
			}
			if found < 0 {
				return "", fmt.Errorf("среди находок нет %q: %s", c.Want, strings.Join(names, "; "))
			}
			hits[0], hits[found] = hits[found], hits[0]
		}
		return fmt.Sprintf("%d: %s", len(hits), strings.Join(names, "; ")), nil
	}) {
		return rep
	}

	hit := hits[0]
	var full dp.Title
	if !x.step("title", func(ctx context.Context) (string, error) {
		var err error
		full, err = p.Title(ctx, hit.Ref)
		if err != nil {
			return "", err
		}
		if full.Ref != hit.Ref {
			return "", fmt.Errorf("Title(%s) вернул чужую ссылку %s", hit.Ref.ItemID, full.Ref.ItemID)
		}
		if err := titleIssues(full, d.ProviderID); err != nil {
			return "", err
		}
		ids := []string{}
		for k, v := range map[string]string{"imdb": full.IMDB, "kp": full.Kinopoisk, "mal": full.MAL} {
			if v != "" {
				ids = append(ids, k+"="+v)
			}
		}
		detail := fmt.Sprintf("%s (%s, %d, сезон %d) %s", full.Title, full.Kind, full.Year, full.SeasonNo, strings.Join(ids, " "))
		if full.Poster == "" {
			return detail, warnf("%s — нет постера", detail)
		}
		return detail, nil
	}) {
		return rep
	}

	x.step("unknown-id", func(ctx context.Context) (string, error) {
		_, err := p.Title(ctx, kernel.ProviderRef{ProviderID: d.ProviderID, ItemID: "9999999999"})
		switch {
		case err == nil:
			return "", warnf("несуществующий номер открылся без ошибки")
		case errors.Is(err, kernel.ErrNotFound):
			return "NotFound, как надо", nil
		}
		return "", warnf("на несуществующий номер не NotFound, а %v — плеер будет считать площадку сломанной", err)
	})

	var eps []dp.Episode
	if !x.step("episodes", func(ctx context.Context) (string, error) {
		var err error
		eps, err = p.Episodes(ctx, full.Ref)
		if err != nil {
			return "", err
		}
		if len(eps) == 0 {
			return "", errors.New("серий нет")
		}
		seen := map[[2]int]bool{}
		for _, e := range eps {
			k := [2]int{e.SeasonNo, e.EpisodeNo}
			if seen[k] {
				return "", fmt.Errorf("серия s%de%d повторяется", e.SeasonNo, e.EpisodeNo)
			}
			seen[k] = true
			if e.Ref != full.Ref {
				return "", fmt.Errorf("у серии s%de%d чужая ссылка", e.SeasonNo, e.EpisodeNo)
			}
		}
		if full.Kind == "movie" && (len(eps) != 1 || eps[0].SeasonNo != 0) {
			return "", fmt.Errorf("у фильма ждём одну «серию» с сезоном 0, а их %d (первая s%de%d)",
				len(eps), eps[0].SeasonNo, eps[0].EpisodeNo)
		}
		return fmt.Sprintf("%d, первая s%de%d, последняя s%de%d", len(eps),
			eps[0].SeasonNo, eps[0].EpisodeNo, eps[len(eps)-1].SeasonNo, eps[len(eps)-1].EpisodeNo), nil
	}) {
		return rep
	}

	first := eps[0]
	if d.Has(dp.CapVoices) {
		x.step("voices", func(ctx context.Context) (string, error) {
			v, ok := p.(appprov.Voices)
			if !ok {
				return "", errors.New("заявлен voices, а метода нет")
			}
			got, err := v.Voices(ctx, full.Ref, first.SeasonNo, first.EpisodeNo)
			if err != nil {
				return "", err
			}
			names := make([]string, 0, len(got))
			for _, x := range got {
				names = append(names, x.Title)
			}
			return fmt.Sprintf("%d: %s", len(got), strings.Join(names, ", ")), nil
		})
	}

	var src dp.Source
	if !x.step("resolve", func(ctx context.Context) (string, error) {
		var err error
		src, err = p.Resolve(ctx, dp.ResolveRequest{Ref: full.Ref, Season: first.SeasonNo, Episode: first.EpisodeNo,
			Client: dp.ClientProfile{SupportsHLS: true, MaxHeight: 1080}})
		if err != nil {
			return "", err
		}
		if err := sourceIssues(src); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s, до %s, домены %s, вариантов %d", src.Protocol, host(src.URL),
			src.ExpiresAt.Format("15:04"), strings.Join(src.AllowedOrigins, ","), len(src.Variants)), nil
	}) {
		return rep
	}

	if c.Fetch == nil {
		x.skip("stream", "поток не скачивается (-fetch)")
		return rep
	}
	x.step("stream", func(ctx context.Context) (string, error) {
		return fetchStart(ctx, c.Fetch, src)
	})
	return rep
}

// describeIssues — честность описания: заявленное реализовано.
func describeIssues(p appprov.Provider, d dp.Descriptor, wantID string) (string, error) {
	if err := d.Compatible(); err != nil {
		return "", fmt.Errorf("описание несовместимо (api v%d, id %q): %w", d.APIMajor, d.ProviderID, err)
	}
	if wantID != "" && d.ProviderID != wantID {
		return "", fmt.Errorf("Describe называет id %q, а площадка зарегистрирована как %q", d.ProviderID, wantID)
	}
	if strings.TrimSpace(d.Name) == "" {
		return "", errors.New("нет имени для человека (Descriptor.Name)")
	}
	checks := []struct {
		c  dp.Capability
		ok bool
	}{
		{dp.CapLookup, implements[appprov.Lookup](p)},
		{dp.CapVoices, implements[appprov.Voices](p)},
		{dp.CapThumbnails, implements[appprov.Thumbnails](p)},
		{dp.CapSegments, implements[appprov.Segments](p)},
	}
	for _, ch := range checks {
		if d.Has(ch.c) && !ch.ok {
			return "", fmt.Errorf("заявлена возможность %s, а метода нет", ch.c)
		}
	}
	caps := make([]string, 0, len(d.Capabilities))
	for _, c := range d.Capabilities {
		caps = append(caps, string(c))
	}
	return fmt.Sprintf("%s «%s» %s [%s]", d.ProviderID, d.Name, d.ProviderVersion, strings.Join(caps, " ")), nil
}

func implements[T any](p any) bool {
	_, ok := p.(T)
	return ok
}

var (
	imdbRe   = regexp.MustCompile(`^tt\d{7,}$`)
	digitsRe = regexp.MustCompile(`^\d+$`)
	rawRe    = regexp.MustCompile(`<[a-zA-Z/][^>]*>|&[a-z]+;|&#\d+;`)
)

// titleIssues — каноническая форма тайтла: сырые структуры площадки
// (теги, сущности, голые номера IMDb) не протекают в модель платформы.
func titleIssues(t dp.Title, id string) error {
	switch {
	case t.Ref.ProviderID != id:
		return fmt.Errorf("тайтл %q: ссылка на площадку %q, а не %q", t.Title, t.Ref.ProviderID, id)
	case t.Ref.ItemID == "":
		return fmt.Errorf("тайтл %q без номера у площадки", t.Title)
	case strings.TrimSpace(t.Title) == "":
		return fmt.Errorf("тайтл %s без названия", t.Ref.ItemID)
	case t.Kind != "movie" && t.Kind != "series":
		return fmt.Errorf("тайтл %q: вид %q — ждём movie или series", t.Title, t.Kind)
	case t.IMDB != "" && !imdbRe.MatchString(t.IMDB):
		return fmt.Errorf("тайтл %q: IMDb %q — ждём tt0898266", t.Title, t.IMDB)
	case t.Kinopoisk != "" && !digitsRe.MatchString(t.Kinopoisk):
		return fmt.Errorf("тайтл %q: Кинопоиск %q — ждём число", t.Title, t.Kinopoisk)
	case t.MAL != "" && !digitsRe.MatchString(t.MAL):
		return fmt.Errorf("тайтл %q: MAL %q — ждём число", t.Title, t.MAL)
	case rawRe.MatchString(t.Title) || rawRe.MatchString(t.Plot):
		return fmt.Errorf("тайтл %q: в названии или описании HTML — снимите теги и сущности", t.Title)
	case t.Year != 0 && (t.Year < 1890 || t.Year > time.Now().Year()+3):
		return fmt.Errorf("тайтл %q: год %d", t.Title, t.Year)
	}
	return nil
}

// sourceIssues — поток пригоден к тикету.
func sourceIssues(s dp.Source) error {
	u, err := url.Parse(s.URL)
	switch {
	case err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "":
		return fmt.Errorf("адрес потока %q — не http(s)", s.URL)
	case s.Protocol != "HLS" && s.Protocol != "PROGRESSIVE":
		return fmt.Errorf("protocol %q — ждём HLS или PROGRESSIVE", s.Protocol)
	case s.ExpiresAt.IsZero() || s.ExpiresAt.Before(time.Now().Add(5*time.Minute)):
		return fmt.Errorf("срок ссылки %v — пуст или меньше пяти минут", s.ExpiresAt)
	case len(s.AllowedOrigins) == 0:
		return errors.New("allowed_origins пуст: шлюз не пустит ни одного сегмента")
	}
	if !originOK(u.Hostname(), s.AllowedOrigins) {
		return fmt.Errorf("хост потока %s не входит в allowed_origins %v", u.Hostname(), s.AllowedOrigins)
	}
	if s.Variant != "" && len(s.Variants) > 0 {
		found := false
		for _, v := range s.Variants {
			found = found || v.ID == s.Variant
		}
		if !found {
			return fmt.Errorf("выданный вариант %q не из списка вариантов", s.Variant)
		}
	}
	return nil
}

func originOK(h string, origins []string) bool {
	for _, o := range origins {
		o = strings.TrimPrefix(strings.TrimPrefix(o, "https://"), "http://")
		if h == o || strings.HasSuffix(h, "."+o) {
			return true
		}
	}
	return false
}

func host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Host
}

// fetchStart — начало потока с заголовками источника: HLS — плейлист,
// файл — первые байты.
func fetchStart(ctx context.Context, hc *http.Client, s dp.Source) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return "", err
	}
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}
	if s.Protocol == "PROGRESSIVE" {
		req.Header.Set("Range", "bytes=0-65535")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if s.Protocol == "HLS" && !strings.HasPrefix(strings.TrimSpace(string(body)), "#EXTM3U") {
		return "", errors.New("ответ — не плейлист HLS")
	}
	return fmt.Sprintf("HTTP %d, %s, %d байт", resp.StatusCode, resp.Header.Get("Content-Type"), len(body)), nil
}
