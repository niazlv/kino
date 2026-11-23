package engine

import (
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"
)

// egressCounter считает запросы движка к внешним сайтам: сколько всего и к
// кому. Нужен не ради красоты: локальный режим обязан быть бережным к чужим
// сервисам, а бережность без счётчика — слова. Цифры видны человеку в
// настройках движка и проверяются тестами сценариев «поиск», «повтор» и
// «простой».
type egressCounter struct {
	log *slog.Logger

	mu     sync.Mutex
	total  int
	byHost map[string]int
	since  time.Time
}

func newEgressCounter(log *slog.Logger) *egressCounter {
	return &egressCounter{log: log, byHost: map[string]int{}, since: time.Now()}
}

// Wrap — для kit.Routes.WrapTransport.
func (c *egressCounter) Wrap(next http.RoundTripper) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		c.mu.Lock()
		c.total++
		c.byHost[r.URL.Hostname()]++
		c.mu.Unlock()
		// Путь без запроса: в строке поиска бывают имена, журналу они ни к чему.
		c.log.Debug("запрос наружу", "host", r.URL.Hostname(), "path", r.URL.Path, "ua", r.Header.Get("User-Agent"))
		return next.RoundTrip(r)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type hostCount struct {
	Host  string `json:"host"`
	Count int    `json:"count"`
}

type egressSnapshot struct {
	Total int         `json:"total"`
	Since time.Time   `json:"since"`
	Hosts []hostCount `json:"hosts"`
}

func (c *egressCounter) Snapshot() egressSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := egressSnapshot{Total: c.total, Since: c.since, Hosts: make([]hostCount, 0, len(c.byHost))}
	for h, n := range c.byHost {
		s.Hosts = append(s.Hosts, hostCount{Host: h, Count: n})
	}
	sort.Slice(s.Hosts, func(i, j int) bool {
		if s.Hosts[i].Count != s.Hosts[j].Count {
			return s.Hosts[i].Count > s.Hosts[j].Count
		}
		return s.Hosts[i].Host < s.Hosts[j].Host
	})
	return s
}
