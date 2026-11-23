// Package share — выдача и разбор ссылок-разрешений.
//
// Две разные вещи под одной кнопкой в интерфейсе, и их важно не смешивать:
// обычная ссылка на страницу требует входа и безопасна, а ссылка-разрешение
// открывается без него. Вторая существует только потому, что иначе не
// объяснить загрузчику, что качать, — и потому у неё есть срок, отзыв и учёт.
package share

import (
	"context"
	"fmt"
	"time"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	dshare "github.com/niazleushkin/kino/internal/domain/share"
)

type Repo interface {
	Create(ctx context.Context, l dshare.Link) error
	Get(ctx context.Context, token string) (dshare.Link, error)
	Revoke(ctx context.Context, u kernel.UserID, token string) error
	ListMine(ctx context.Context, u kernel.UserID) ([]dshare.Link, error)
}

// Tickets выпускает тикет на воспроизведение. Скачивание — ещё один его
// потребитель, поэтому отдельного механизма доставки не заводится.
type Tickets interface {
	IssueTicket(ctx context.Context, u kernel.UserID, e kernel.EpisodeID,
		cp dp.ClientProfile) (ticket TicketInfo, src dp.Source, err error)
}

// TicketInfo — то немногое, что нужно ссылке от тикета.
type TicketInfo struct {
	ID        kernel.TicketID
	ExpiresAt time.Time
}

type Tokens interface{ NewShareToken() string }

type Clock interface{ Now() time.Time }

type Service struct {
	repo    Repo
	tickets Tickets
	tokens  Tokens
	clock   Clock
	policy  auth.Policy
}

func NewService(r Repo, t Tickets, tok Tokens, clock Clock) *Service {
	return &Service{repo: r, tickets: t, tokens: tok, clock: clock}
}

// Create выдаёт ссылку-разрешение.
//
// Срок ограничен сверху жёстко: «бессрочно поделиться» доступом к платной
// подписке — не та возможность, которую стоит давать одним нажатием.
func (s *Service) Create(ctx context.Context, a auth.Actor, e kernel.EpisodeID,
	kind dshare.Kind, sel dshare.Selection, ttl time.Duration) (dshare.Link, error) {
	if err := s.policy.CanIssueTicket(a, a.UserID); err != nil {
		return dshare.Link{}, err
	}
	if !kind.Valid() {
		return dshare.Link{}, kernel.ErrInvalidInput
	}
	if ttl <= 0 {
		ttl = dshare.DefaultTTL(kind)
	}
	if ttl > dshare.MaxTTL {
		ttl = dshare.MaxTTL
	}

	l := dshare.Link{
		Token:     s.tokens.NewShareToken(),
		Kind:      kind,
		EpisodeID: e,
		CreatedBy: a.UserID,
		Selection: sel,
		ExpiresAt: s.clock.Now().Add(ttl),
	}
	if err := s.repo.Create(ctx, l); err != nil {
		return dshare.Link{}, err
	}
	return l, nil
}

// Resolved — всё, что нужно отдать по ссылке.
type Resolved struct {
	Link   dshare.Link
	Ticket TicketInfo
	Source dp.Source
}

// Resolve разбирает ссылку и выпускает тикет от имени ВЫДАВШЕГО.
//
// Именно от него: открывший ссылку может быть вообще не зарегистрирован,
// а смотреть он будет по подписке того, кто поделился. Прогресс при этом
// не пишется никому — гость не должен двигать чужие позиции.
func (s *Service) Resolve(ctx context.Context, token string, cp dp.ClientProfile) (Resolved, error) {
	l, err := s.repo.Get(ctx, token)
	if err != nil {
		return Resolved{}, err
	}
	if err := l.Usable(s.clock.Now()); err != nil {
		return Resolved{}, fmt.Errorf("ссылка %s: %w", token[:6], err)
	}
	if sel := l.Selection; sel.MaxHeight > 0 {
		cp.MaxHeight = sel.MaxHeight
	}
	t, src, err := s.tickets.IssueTicket(ctx, l.CreatedBy, l.EpisodeID, cp)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Link: l, Ticket: t, Source: src}, nil
}

func (s *Service) Revoke(ctx context.Context, a auth.Actor, token string) error {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return err
	}
	return s.repo.Revoke(ctx, a.UserID, token)
}

func (s *Service) ListMine(ctx context.Context, a auth.Actor) ([]dshare.Link, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return nil, err
	}
	return s.repo.ListMine(ctx, a.UserID)
}
