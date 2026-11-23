package kit

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/niazleushkin/kino/pkg/browserhttp"
)

// DefaultRoute — маршрут, которым ходят, когда иной не назначен.
const DefaultRoute = "default"

// DirectRoute — без прокси. Есть всегда: объявлять его руками незачем.
const DirectRoute = "direct"

// Routes — именованные выходы наружу.
//
// Площадкам нужны разные выходы: kinopub с домашнего сервера недоступен без
// VPN, а аниме-площадки могут, наоборот, требовать российский адрес или
// отдельный прокси. Имя маршрута — это инфраструктура, а не знание о
// площадке: шлюз знает «vpn» и «direct», но не знает, кому какой нужен.
// Поэтому новая площадка на известном маршруте не трогает конфиг шлюза.
type Routes struct {
	clients map[string]*http.Client
}

// LoadRoutes читает маршруты из окружения.
//
//	EGRESS_ROUTES  "vpn=http://172.18.0.1:10819,ru=socks5://10.0.0.5:1080"
//	               пустой адрес ("direct=") — без прокси
//	KINOPUB_PROXY  исторически единственный прокси; если маршрута default
//	               в EGRESS_ROUTES нет, он становится им
//
// Каждый клиент — с браузерным TLS-отпечатком: CDN режут по JA3/JA4 (ADR-0003).
func LoadRoutes(timeout time.Duration) (*Routes, error) {
	spec := map[string]string{}
	for _, part := range strings.Split(os.Getenv("EGRESS_ROUTES"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, proxy, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("EGRESS_ROUTES: %q — ожидалось имя=адрес", part)
		}
		spec[name] = strings.TrimSpace(proxy)
	}
	if _, ok := spec[DefaultRoute]; !ok {
		spec[DefaultRoute] = os.Getenv("KINOPUB_PROXY")
	}
	if _, ok := spec[DirectRoute]; !ok {
		spec[DirectRoute] = ""
	}

	r := &Routes{clients: make(map[string]*http.Client, len(spec))}
	for name, raw := range spec {
		var proxy *url.URL
		if raw != "" {
			u, err := url.Parse(raw)
			if err != nil || u.Host == "" {
				return nil, fmt.Errorf("маршрут %s: адрес прокси %q не разобран", name, raw)
			}
			proxy = u
		}
		c := browserhttp.NewBrowserClient(proxy)
		c.Timeout = timeout
		r.clients[name] = c
	}
	return r, nil
}

// Client отдаёт клиент маршрута. Пустое имя — маршрут по умолчанию.
//
// Незнакомый маршрут — отказ, а не тихий выход напрямую: площадка, которой
// нужен VPN, при прямом выходе даёт таймауты или чужую страну, и искать
// такую поломку потом приходится по косвенным признакам.
func (r *Routes) Client(name string) (*http.Client, bool) {
	if name == "" {
		name = DefaultRoute
	}
	c, ok := r.clients[name]
	return c, ok
}

// WrapTransport оборачивает транспорт каждого маршрута: так движок считает
// исходящие запросы к площадкам, не зная, кто именно и зачем их шлёт. Зовётся
// один раз при сборке, до первого запроса.
func (r *Routes) WrapTransport(wrap func(http.RoundTripper) http.RoundTripper) {
	for _, c := range r.clients {
		base := c.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		c.Transport = wrap(base)
	}
}

// Default — клиент маршрута по умолчанию.
func (r *Routes) Default() *http.Client {
	c, _ := r.Client(DefaultRoute)
	return c
}

// Names — известные маршруты, для журнала при старте.
func (r *Routes) Names() []string {
	out := make([]string, 0, len(r.clients))
	for n := range r.clients {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
