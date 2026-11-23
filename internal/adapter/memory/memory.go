// Package memory — порты, которые в движке живут в памяти и ничего не
// переживают (ADR-0015): тикеты короткоживущие и нужны тому же процессу,
// а телеметрия и обучение псевдонимам студий на устройстве не ведутся.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

// Tickets — TicketStore на карте с TTL. В проде это Redis (core и gateway —
// разные процессы); в движке читает тот же процесс, и тикет по определению
// не переживает его.
type Tickets struct {
	mu  sync.RWMutex
	m   map[kernel.TicketID]ticketEntry
	now func() time.Time
}

type ticketEntry struct {
	ticket  playback.Ticket
	expires time.Time
}

func NewTickets() *Tickets {
	return &Tickets{m: map[kernel.TicketID]ticketEntry{}, now: time.Now}
}

func (s *Tickets) Put(_ context.Context, t playback.Ticket, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[t.ID] = ticketEntry{ticket: t, expires: s.now().Add(ttl)}
	return nil
}

func (s *Tickets) Get(_ context.Context, id kernel.TicketID) (playback.Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[id]
	if !ok || s.now().After(e.expires) {
		if ok {
			delete(s.m, id)
		}
		return playback.Ticket{}, kernel.ErrNotFound
	}
	return e.ticket, nil
}

// Audit — в админку некому писать и некому читать.
type Audit struct{}

func (Audit) Record(context.Context, kernel.UserID, string, string, string, any, any) error {
	return nil
}
