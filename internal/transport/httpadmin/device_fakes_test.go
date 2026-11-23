package httpadmin

import (
	"context"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Вход устройства админку не касается: фейки лишь удовлетворяют порты.

func (m *memSessions) CreateRenewable(ctx context.Context, s account.Session, tokenHash, _ string) error {
	return m.Create(ctx, s, tokenHash)
}

func (m *memSessions) ByRefreshHash(context.Context, string) (account.Session, error) {
	return account.Session{}, kernel.ErrNotFound
}

func (*stubTokens) NewUserCode() string { return "CODE-0001" }
