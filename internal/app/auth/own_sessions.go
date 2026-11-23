package auth

import (
	"context"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Свои сессии — «Где вы вошли» в профиле витрины.
//
// Устройство, подключённое по коду, живёт полгода и продлевается само.
// Человек должен уметь отключить его без администратора: телевизор у
// родителей, телефон, отданный на время. Админские сессии здесь не видны
// и не гасятся: ими распоряжается админка со своим вторым фактором.

// liveAt — сессией ещё можно пользоваться. Истёкшая сессия устройства с
// живым правом продлиться — это телевизор, который давно не включали:
// включат — он продлится сам, поэтому и выйти на нём должно быть можно.
func liveAt(s account.Session, now time.Time) bool {
	if s.RevokedAt != nil {
		return false
	}
	if now.Before(s.ExpiresAt) {
		return true
	}
	return s.RefreshExpiresAt != nil && now.Before(*s.RefreshExpiresAt)
}

// MySessions — живые сессии витрины действующего пользователя, свежие первыми.
func (s *Service) MySessions(ctx context.Context, a Actor) ([]account.Session, error) {
	if err := s.policy.CanManageOwnSessions(a); err != nil {
		return nil, err
	}
	all, err := s.sessions.ListForUser(ctx, a.UserID)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	out := make([]account.Session, 0, len(all))
	for _, sess := range all {
		if sess.Space == account.SpaceUser && liveAt(sess, now) {
			out = append(out, sess)
		}
	}
	return out, nil
}

// SignOutSession — выйти на одном устройстве. Чужая, админская или уже
// погасшая сессия отвечает «не найдено»: подтверждать, что такая где-то
// есть, незачем.
func (s *Service) SignOutSession(ctx context.Context, a Actor, id kernel.SessionID) error {
	mine, err := s.MySessions(ctx, a)
	if err != nil {
		return err
	}
	for _, sess := range mine {
		if sess.ID != id {
			continue
		}
		if err := s.sessions.Revoke(ctx, id, s.clock.Now()); err != nil {
			return err
		}
		_ = s.audit.Record(ctx, a.UserID, "session.sign_out", sess.DeviceKind, "", nil, nil)
		return nil
	}
	return kernel.ErrNotFound
}

// SignOutOthers — выйти везде, кроме этого устройства. Возвращает, сколько
// сессий погашено. В отличие от смены пароля, текущая остаётся живой: это
// «я отдал телефон», а не «меня взломали».
func (s *Service) SignOutOthers(ctx context.Context, a Actor) (int, error) {
	mine, err := s.MySessions(ctx, a)
	if err != nil {
		return 0, err
	}
	now := s.clock.Now()
	n := 0
	for _, sess := range mine {
		if sess.ID == a.SessionID {
			continue
		}
		if err := s.sessions.Revoke(ctx, sess.ID, now); err != nil {
			return n, err
		}
		n++
	}
	if n > 0 {
		_ = s.audit.Record(ctx, a.UserID, "session.sign_out_others", "", "",
			nil, map[string]int{"count": n})
	}
	return n, nil
}
