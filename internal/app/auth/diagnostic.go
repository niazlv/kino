package auth

import (
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// DiagnosticGrant — единственная легальная дырка к чужим данным: временный,
// адресный, обязательно попадающий в журнал админских действий доступ для
// разбора конкретной жалобы. Не «роль суперадмина», а выданное разрешение
// с датой окончания.
type DiagnosticGrant struct {
	Subject   kernel.UserID
	GrantedTo kernel.UserID
	Reason    string
	ExpiresAt time.Time
	now       func() time.Time
}

func NewDiagnosticGrant(subject, to kernel.UserID, reason string, ttl time.Duration) DiagnosticGrant {
	return DiagnosticGrant{
		Subject: subject, GrantedTo: to, Reason: reason,
		ExpiresAt: time.Now().Add(ttl), now: time.Now,
	}
}

func (g DiagnosticGrant) Covers(u kernel.UserID) bool {
	if g.Subject == 0 || g.Subject != u {
		return false
	}
	now := time.Now
	if g.now != nil {
		now = g.now
	}
	return now().Before(g.ExpiresAt)
}
