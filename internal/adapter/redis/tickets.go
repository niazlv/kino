// Package redis — реализации портов поверх Redis. Здесь Redis используется
// только как КЕШ с временем жизни, а не как хранилище: всё, что тут лежит,
// можно потерять без последствий для данных пользователя.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

// TicketStore — граница между core и gateway (ADR-0002).
//
// Разделяемое состояние здесь оправдано: контракт узкий, писатель ровно один
// (core), читатель ровно один (gateway), а альтернатива — синхронный RPC на
// горячем пути воспроизведения. Чтение происходит один раз за сессию,
// а не на каждый сегмент.
type TicketStore struct{ c *goredis.Client }

func NewTicketStore(c *goredis.Client) *TicketStore { return &TicketStore{c: c} }

func key(id kernel.TicketID) string { return "ticket:" + string(id) }

func (s *TicketStore) Put(ctx context.Context, t playback.Ticket, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("redis: тикет %s с неположительным ttl", t.ID)
	}
	b, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("redis: сериализация тикета: %w", err)
	}
	if err := s.c.Set(ctx, key(t.ID), b, ttl).Err(); err != nil {
		return fmt.Errorf("redis: запись тикета: %w", err)
	}
	return nil
}

func (s *TicketStore) Get(ctx context.Context, id kernel.TicketID) (playback.Ticket, error) {
	b, err := s.c.Get(ctx, key(id)).Bytes()
	if err != nil {
		if err == goredis.Nil {
			// Истёкший тикет неотличим от несуществующего — и это правильно:
			// клиенту в обоих случаях надо просто попросить новый.
			return playback.Ticket{}, kernel.ErrNotFound
		}
		return playback.Ticket{}, fmt.Errorf("redis: чтение тикета: %w", err)
	}
	var t playback.Ticket
	if err := json.Unmarshal(b, &t); err != nil {
		return playback.Ticket{}, fmt.Errorf("redis: разбор тикета: %w", err)
	}
	return t, nil
}

// NewClient открывает соединение и сразу проверяет его: молчаливо неживой
// кеш даёт отказы воспроизведения без внятной причины.
func NewClient(ctx context.Context, addr string) (*goredis.Client, error) {
	c := goredis.NewClient(&goredis.Options{Addr: addr})
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redis %s: %w", addr, err)
	}
	return c, nil
}
