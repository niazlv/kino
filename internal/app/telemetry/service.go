// Package telemetry — приём телеметрии воспроизведения.
//
// Работает с ОДНОЙ записью на просмотр. Пологовые логи в БД не попадают
// никогда: двухчасовой фильм это ~2400 обращений за сегментами, и разница
// с посессионной записью — три порядка при одинаковой пользе (ADR-0005).
package telemetry

import (
	"context"
	"time"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

type Repo interface {
	Open(ctx context.Context, s playback.Session) error
	Close(ctx context.Context, s playback.Session) error
}

type Clock interface{ Now() time.Time }

type Service struct {
	repo   Repo
	clock  Clock
	policy auth.Policy
}

func NewService(repo Repo, clock Clock) *Service {
	return &Service{repo: repo, clock: clock}
}

// Report принимает итог сессии от плеера.
//
// user_id берётся из АКТОРА, а не из тела запроса: иначе клиент мог бы
// приписать свою статистику другому человеку.
func (s *Service) Report(ctx context.Context, a auth.Actor, in playback.Session) error {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return err
	}
	if in.ID == "" {
		return kernel.ErrInvalidInput
	}
	in.UserID = a.UserID
	if in.StartedAt.IsZero() {
		in.StartedAt = s.clock.Now()
	}
	now := s.clock.Now()
	in.EndedAt = &now
	if in.EndReason == "" {
		in.EndReason = playback.EndAbandoned
	}
	return s.repo.Close(ctx, in)
}

// Begin отмечает начало просмотра — при выпуске тикета.
//
// Без него прерванная сессия не оставляет следа, и «ушёл, не досмотрев»
// становится неотличимо от «не запускал».
func (s *Service) Begin(ctx context.Context, sess playback.Session) error {
	if sess.StartedAt.IsZero() {
		sess.StartedAt = s.clock.Now()
	}
	return s.repo.Open(ctx, sess)
}
