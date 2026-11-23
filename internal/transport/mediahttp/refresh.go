package mediahttp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

// TicketRefresher просит core перевыпустить протухший тикет.
//
// Контракт умышленно нищий: один идентификатор на входе. Ни одного
// провайдерского понятия здесь нет и быть не может — иначе шлюз узнал бы
// про площадки, и добавление новой потребовало бы его передеплоя (ADR-0002).
type TicketRefresher struct {
	client *http.Client
	base   string
	token  string
}

func NewTicketRefresher(base, token string) *TicketRefresher {
	return &TicketRefresher{
		client: &http.Client{Timeout: 30 * time.Second},
		base:   base,
		token:  token,
	}
}

// Refresh просит обновить тикет. Возвращается ошибка, а не новый тикет:
// core кладёт его в хранилище под тем же id, и шлюз просто перечитывает
// оттуда — так исключается расхождение между двумя копиями.
func (t *TicketRefresher) Refresh(ctx context.Context, id kernel.TicketID) error {
	if t.base == "" {
		return fmt.Errorf("обновление тикета не настроено")
	}
	url := fmt.Sprintf("%s/internal/tickets/%s/refresh", t.base, string(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("X-Service-Token", t.token)

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("обновление тикета %s: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("обновление тикета %s: код %d: %w", id, resp.StatusCode, kernel.ErrUnavailable)
	}
	return nil
}

// refreshTimeout — сколько ждать обновления тикета. core сам укладывает
// выбор площадки в 25 с, клиент обновления сдаётся через 30.
const refreshTimeout = 35 * time.Second

// aheadEvery — не чаще этого обновлять заранее один и тот же тикет: core
// мог не достучаться до площадки, и каждый сегмент повторял бы попытку.
const aheadEvery = time.Minute

// refreshed просит core перевыпустить тикет и перечитывает его из хранилища.
// Любой отказ для клиента один — ссылка протухла.
//
// Одновременные запросы одного тикета (звук, картинка, соседний сегмент)
// ждут одно обновление, а не идут к площадке каждый своим. Обновление живёт
// своим сроком, а не запросом, который его начал: ушёл первый — остальные
// всё равно его дождутся.
func (s *Server) refreshed(ctx context.Context, id kernel.TicketID) (playback.Ticket, error) {
	ch := s.refreshes.DoChan(string(id), func() (any, error) {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		defer cancel()
		if err := s.refresh.Refresh(rctx, id); err != nil {
			s.log.Error("тикет не обновлён", "ticket", string(id), "err", err)
			return nil, kernel.ErrExpired
		}
		fresh, err := s.tickets.Get(rctx, id)
		if err != nil {
			return nil, kernel.ErrExpired
		}
		s.log.Info("тикет обновлён", "ticket", string(id), "до", fresh.ExpiresAt.Format(time.RFC3339))
		return fresh, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return playback.Ticket{}, res.Err
		}
		return res.Val.(playback.Ticket), nil
	case <-ctx.Done():
		return playback.Ticket{}, kernel.ErrExpired
	}
}

// refreshAhead обновляет тикет в фоне, пока его ссылки ещё живы: запрос,
// который это заметил, обслуживается старым тикетом и площадку не ждёт.
// Не вышло — тикет доживает свой срок, а следующая попытка будет не раньше
// чем через aheadEvery.
func (s *Server) refreshAhead(id kernel.TicketID) {
	if !s.ahead.claim(id, time.Now()) {
		return
	}
	go func() { _, _ = s.refreshed(context.Background(), id) }()
}

// aheadGate помнит, когда тикет последний раз обновляли заранее.
type aheadGate struct {
	mu   sync.Mutex
	last map[kernel.TicketID]time.Time
}

func (g *aheadGate) claim(id kernel.TicketID, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		g.last = map[kernel.TicketID]time.Time{}
	}
	if t, ok := g.last[id]; ok && now.Sub(t) < aheadEvery {
		return false
	}
	// Память о давних попытках не нужна: тикетов за день — десятки.
	if len(g.last) > 256 {
		for k, t := range g.last {
			if now.Sub(t) > aheadEvery {
				delete(g.last, k)
			}
		}
	}
	g.last[id] = now
	return true
}
