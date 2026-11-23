// Package health — какие площадки сейчас не отвечают.
//
// Площадка, до которой не дошли по сети или которая не ответила вовремя,
// какое-то время считается лежащей: выпуск тикета не тратит на неё время
// человека и идёт сразу к запасным, страница тайтла не ждёт её серий.
// Совсем её не выбрасывают — у части тайтлов других площадок нет, — а
// перепроверяют в фоне: первый же удачный ответ возвращает её в строй.
//
// Память — в процессе, не в базе: после рестарта первый же запрос узнает
// всё заново, а общая память на несколько процессов здесь не нужна.
//
// Особый случай — «нужна переавторизация» (kernel.ErrReauth): площадка
// отвергла наш вход, и сама это не пройдёт. Такая площадка лежит без срока,
// пока не ответит или пока хранитель входа не скажет, что вход снова
// принят, а админка и плеер называют причину, а не «не отвечает».
package health

import (
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

const (
	// DownFor — сколько площадка считается лежащей после отказа. Каждый
	// новый отказ продлевает срок. Без запросов она к концу срока снова
	// «живая», и первый человек после этого потратит на неё одну попытку.
	DownFor = 3 * time.Minute
	// ProbeEvery — как часто перепроверять лежащую площадку в фоне. Проверку
	// запускает очередной запрос, а не таймер: площадку, которая никому не
	// нужна, незачем и дёргать.
	ProbeEvery = 20 * time.Second
)

// Tracker — состояние площадок. Нулевое значение не годится: New.
type Tracker struct {
	now func() time.Time
	log *slog.Logger

	mu   sync.Mutex
	down map[string]*state
}

type state struct {
	until     time.Time
	probing   bool
	lastProbe time.Time

	// reauth — с какого момента площадка ждёт переавторизации; ноль — нет.
	reauth time.Time
	cause  string
}

func (s *state) down(now time.Time) bool { return !s.reauth.IsZero() || now.Before(s.until) }

// Reauth — площадка, которой нужна переавторизация, для админки.
type Reauth struct {
	Provider string
	Since    time.Time
	Cause    string
}

// New — пустой трекер: все площадки считаются живыми.
func New(log *slog.Logger) *Tracker {
	return &Tracker{now: time.Now, log: log, down: map[string]*state{}}
}

// Down — лежит ли площадка сейчас.
func (t *Tracker) Down(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.down[id]
	return ok && s.down(t.now())
}

// NeedsReauth — ждёт ли площадка переавторизации.
func (t *Tracker) NeedsReauth(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.down[id]
	return ok && !s.reauth.IsZero()
}

// Reauths — все площадки, ждущие переавторизации, по имени.
func (t *Tracker) Reauths() []Reauth {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Reauth
	for id, s := range t.down {
		if !s.reauth.IsZero() {
			out = append(out, Reauth{Provider: id, Since: s.reauth, Cause: s.cause})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// ReauthCleared — хранитель входа сообщил, что вход снова принят. Снимает
// только «нужна переавторизация»: сетевой срок, если он идёт, остаётся.
func (t *Tracker) ReauthCleared(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.down[id]
	if !ok || s.reauth.IsZero() {
		return
	}
	s.reauth, s.cause = time.Time{}, ""
	if t.log != nil {
		t.log.Info("площадка снова принимает вход", "provider", id)
	}
}

// Failed — площадка не ответила: лежит ещё DownFor с этого момента.
func (t *Tracker) Failed(id string, cause error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	s, ok := t.down[id]
	if !ok {
		s = &state{}
		t.down[id] = s
	}
	if errors.Is(cause, kernel.ErrReauth) {
		if s.reauth.IsZero() {
			s.reauth = now
			if t.log != nil {
				t.log.Error("площадке нужна переавторизация — пока берём запасные", "provider", id, "err", cause)
			}
		}
		s.cause = cause.Error()
		return
	}
	fresh := !s.down(now)
	s.until = now.Add(DownFor)
	if fresh && t.log != nil {
		t.log.Warn("площадка не отвечает — пока берём запасные", "provider", id, "err", cause)
	}
}

// Recovered — площадка ответила: снова в строю.
func (t *Tracker) Recovered(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.down[id]
	if !ok {
		return
	}
	delete(t.down, id)
	if s.down(t.now()) && t.log != nil {
		t.log.Info("площадка снова отвечает", "provider", id)
	}
}

// TryProbe занимает фоновую перепроверку лежащей площадки: одну за раз и
// не чаще ProbeEvery. ok=false — перепроверять не нужно (площадка жива)
// или рано. Иначе вызывающий обязан вызвать done с исходом.
func (t *Tracker) TryProbe(id string) (done func(err error), ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	s, found := t.down[id]
	if !found || !s.down(now) || s.probing || now.Sub(s.lastProbe) < ProbeEvery {
		return nil, false
	}
	s.probing, s.lastProbe = true, now
	return func(err error) {
		t.mu.Lock()
		s.probing = false
		t.mu.Unlock()
		if err != nil {
			t.Failed(id, err)
			return
		}
		t.Recovered(id)
	}, true
}
