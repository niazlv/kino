// Package sitekit — общее для кода площадок, не привязанное ни к одной из
// них: вежливый HTTP-клиент (пауза по хосту, повтор на 429), разбор
// HTML-фрагментов, описание найденного потока и мост «площадка, которая
// знает только, где чужие плееры» (Site).
//
// Часть публичного протокола плагинов (ADR-0018): им пользуются и хост
// площадок-скриптов (script), и адаптеры площадок на Go (providers/).
// Пакет ничего не знает о платформе: ни тайтлов, ни тикетов.
package sitekit

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// UserAgent — один на весь путь: CDNVideoHub привязывает ссылку к семейству
// браузера, выдавшего её (srcAg=CHROME_MAC), и другой UA получает 400.
// Семейство совпадает с TLS-отпечатком транспорта — это тоже Chrome.
const UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

// Stream — найденный поток.
type Stream struct {
	Protocol string // HLS | DASH | PROGRESSIVE
	URL      string
	// Headers нужны и при выкачке сегментов, поэтому едут в тикет.
	Headers        map[string]string
	AllowedOrigins []string
	ExpiresAt      time.Time
	// Heights — какие качества есть, для витрины.
	Heights []int
	// Skips — что плеер площадки предлагает пропустить в этом файле.
	Skips []Span
}

// maxBody — страницы плееров бывают большими (у Kodik с тысячей серий —
// под мегабайт), но не больше этого.
const maxBody = 8 << 20

// Client — HTTP к плеерам и площадкам с вежливой паузой между запросами
// к одному хосту: площадок без API не долбят залпом.
type Client struct {
	hc  *http.Client
	gap time.Duration

	mu      sync.Mutex
	next    map[string]time.Time
	hostGap map[string]time.Duration // пауза, увеличенная после 429
	own     map[string]time.Duration // своя пауза хоста, см. SetHostGap
}

// SetHostGap задаёт хосту свою паузу. API плеера (CVH) рассчитано на то,
// что его дёргает каждая открытая страница, и пауза сайта ему не нужна:
// с ней кадры серий приходили по одному в полсекунды.
func (c *Client) SetHostGap(host string, gap time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.own == nil {
		c.own = map[string]time.Duration{}
	}
	c.own[host] = gap
}

// NewClient: hc приходит СНАРУЖИ — у площадки свой маршрут наружу (VPN,
// прокси, российский адрес), и выбирает его композиционный корень.
func NewClient(hc *http.Client, gap time.Duration) *Client {
	return &Client{hc: hc, gap: gap, next: map[string]time.Time{}}
}

// Response — ответ целиком: страницы маленькие, а разбор всё равно по
// всему телу.
type Response struct {
	Status int
	Body   []byte
	URL    *url.URL
	Header http.Header
}

func (r Response) Text() string { return string(r.Body) }

// Get — GET со своими заголовками поверх обязательного UA.
func (c *Client) Get(ctx context.Context, rawURL string, hdr map[string]string) (Response, error) {
	return c.Do(ctx, http.MethodGet, rawURL, hdr, nil)
}

// PostForm — POST формы.
func (c *Client) PostForm(ctx context.Context, rawURL string, form url.Values, hdr map[string]string) (Response, error) {
	h := map[string]string{"Content-Type": "application/x-www-form-urlencoded; charset=UTF-8"}
	for k, v := range hdr {
		h[k] = v
	}
	return c.Do(ctx, http.MethodPost, rawURL, h, strings.NewReader(form.Encode()))
}

func (c *Client) Do(ctx context.Context, method, rawURL string, hdr map[string]string, body io.Reader) (Response, error) {
	return c.doWith(ctx, c.hc, method, rawURL, hdr, body)
}

// GetNoRedirect — GET без перехода по редиректу: нужен, когда сам адрес
// редиректа и есть ответ (Sibnet отдаёт ссылку на файл именно так).
func (c *Client) GetNoRedirect(ctx context.Context, rawURL string, hdr map[string]string) (Response, error) {
	hc := *c.hc
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c.doWith(ctx, &hc, http.MethodGet, rawURL, hdr, nil)
}

// retries429 — сколько раз переждать «слишком много запросов». Площадка
// без API так говорит, что её обходят слишком часто: пережидаем и
// замедляемся, а не бросаем страницу — обход иначе молча терял бы тайтлы.
const retries429 = 2

func (c *Client) doWith(ctx context.Context, hc *http.Client, method, rawURL string,
	hdr map[string]string, body io.Reader) (Response, error) {
	// Тело запроса перечитать нельзя, поэтому повтор — только для запросов
	// без тела: все обходы и резолвы — GET, POST у Kodik дешёвый и единичный.
	for attempt := 0; ; attempt++ {
		resp, err := c.doOnce(ctx, hc, method, rawURL, hdr, body)
		if err != nil || resp.Status != http.StatusTooManyRequests || body != nil || attempt >= retries429 {
			return resp, err
		}
		wait := retryAfter(resp.Header.Get("Retry-After"))
		c.slowDown(resp.URL.Hostname())
		select {
		case <-ctx.Done():
			return resp, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// retryAfter — сколько ждать по заголовку; без него 20 с, не больше минуты.
func retryAfter(h string) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && n > 0 {
		d := time.Duration(n) * time.Second
		if d > time.Minute {
			d = time.Minute
		}
		return d
	}
	return 20 * time.Second
}

// slowDown удваивает паузу для хоста, ответившего 429 (до 3 с).
func (c *Client) slowDown(host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hostGap == nil {
		c.hostGap = map[string]time.Duration{}
	}
	g := c.hostGap[host]
	if g < c.gap {
		g = c.gap
	}
	if o, ok := c.own[host]; ok && g < o {
		g = o
	}
	if g <= 0 {
		g = 100 * time.Millisecond
	}
	g *= 2
	if g > 3*time.Second {
		g = 3 * time.Second
	}
	c.hostGap[host] = g
}

func (c *Client) doOnce(ctx context.Context, hc *http.Client, method, rawURL string,
	hdr map[string]string, body io.Reader) (Response, error) {
	u, err := url.Parse(AbsURL(rawURL))
	if err != nil {
		return Response{}, fmt.Errorf("embeds: адрес %q: %w", rawURL, kernel.ErrInvalidInput)
	}
	if err := c.wait(ctx, u.Hostname()); err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	// Не дошли или оборвалось на полпути — площадка молчит целиком, а не
	// отказала в конкретной странице.
	resp, err := hc.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("embeds: %s %s: %w: %w", method, u.Host, kernel.ErrUnreachable, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Response{}, fmt.Errorf("embeds: чтение %s: %w: %w", u.Host, kernel.ErrUnreachable, err)
	}
	return Response{Status: resp.StatusCode, Body: b, URL: resp.Request.URL, Header: resp.Header}, nil
}

// wait держит паузу между запросами к одному хосту.
func (c *Client) wait(ctx context.Context, host string) error {
	c.mu.Lock()
	idle := c.gap <= 0 && c.own[host] <= 0 && c.hostGap[host] <= 0
	c.mu.Unlock()
	if idle {
		return nil
	}
	c.mu.Lock()
	now := time.Now()
	at := c.next[host]
	if at.Before(now) {
		at = now
	}
	gap := c.gap
	if g, ok := c.own[host]; ok {
		gap = g
	}
	if g, ok := c.hostGap[host]; ok && g > gap {
		gap = g
	}
	c.next[host] = at.Add(gap)
	c.mu.Unlock()
	if d := time.Until(at); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return nil
}

// StatusErr переводит статус в доменную ошибку: 404 — «нет такого»,
// 429 — «притормози», прочее — «площадка недоступна».
func StatusErr(what string, status int) error {
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		return fmt.Errorf("%s: HTTP %d: %w", what, status, kernel.ErrNotFound)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%s: HTTP %d: %w", what, status, kernel.ErrBusy)
	default:
		return fmt.Errorf("%s: HTTP %d: %w", what, status, kernel.ErrUnavailable)
	}
}

// AbsURL — адрес без схемы («//host/…») в https.
func AbsURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

var attrRe = regexp.MustCompile(`([a-zA-Z0-9_:-]+)="([^"]*)"`)

// Attrs разбирает атрибуты тега. Значения раскодированы из HTML-сущностей.
func Attrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
		out[m[1]] = html.UnescapeString(m[2])
	}
	return out
}

// Text — текст фрагмента без тегов и лишних пробелов.
func Text(fragment string) string {
	s := tagRe.ReplaceAllString(fragment, " ")
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

// OriginOf — домен второго уровня хоста: узлы CDN меняются (vd368.okcdn.ru,
// flora.cloud.solodcdn.com), а домен держится. Сравнение в тикете идёт по
// суффиксу на границе метки, поэтому этого достаточно и не шире нужного.
func OriginOf(rawURL string) string {
	u, err := url.Parse(AbsURL(rawURL))
	if err != nil {
		return ""
	}
	labels := strings.Split(strings.TrimSuffix(u.Hostname(), "."), ".")
	if len(labels) <= 2 {
		return u.Hostname()
	}
	return strings.Join(labels[len(labels)-2:], ".")
}

// Span — отрезок, который плеер площадки предлагает пропустить: «от-до» от
// начала файла. Что это — опенинг или титры, плееры не говорят.
type Span struct {
	StartMS, EndMS int64
}
