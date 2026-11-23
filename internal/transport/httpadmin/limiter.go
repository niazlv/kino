package httpadmin

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// limiter — скользящее окно попыток входа.
//
// Два счётчика: по ключу (адрес) и общий. Общий нужен потому, что адрес
// приходит из заголовков прокси и в обход прокси подделывается; сорок
// попыток за четверть часа на всех — потолок, который живой админ не
// заметит, а перебор упрётся в него первым же ходом.
type limiter struct {
	mu     sync.Mutex
	window time.Duration
	perKey int
	global int
	hits   map[string][]time.Time
}

func newLimiter(window time.Duration, perKey, global int) *limiter {
	return &limiter{window: window, perKey: perKey, global: global, hits: map[string][]time.Time{}}
}

// allow регистрирует попытку и говорит, пропускать ли её.
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cut := now.Add(-l.window)
	all := l.prune("", cut)
	mine := l.prune(key, cut)
	if len(all) >= l.global || len(mine) >= l.perKey {
		return false
	}
	l.hits[""] = append(all, now)
	l.hits[key] = append(mine, now)

	// Забытые ключи подчищаются оптом, чтобы карта не росла от каждого
	// адреса, постучавшегося один раз.
	if len(l.hits) > 1024 {
		for k := range l.hits {
			l.prune(k, cut)
		}
	}
	return true
}

func (l *limiter) prune(key string, cut time.Time) []time.Time {
	ts := l.hits[key]
	i := 0
	for i < len(ts) && !ts[i].After(cut) {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = ts
	}
	return ts
}

func randomKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("httpadmin: нет источника случайности: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
