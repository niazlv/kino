package auth

import (
	"context"
	"errors"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Второй шаг входа в админку.
//
// Пароль (Login) выдал частичную сессию. Здесь она либо повышается верным
// кодом, либо — если фактора у администратора ещё нет — фактор настраивается
// по QR и повышение происходит тем же подтверждением. В обоих случаях
// повышение выдаёт НОВЫЙ токен, а частичный отзывается: смена уровня
// доступа без смены токена — это фиксация сессии.

// ErrEnrollTOTP — второго фактора нет: вместо ввода кода нужна настройка.
var ErrEnrollTOTP = errors.New("второй фактор ещё не настроен")

// MaxFactorAttempts — неверных кодов на одну частичную сессию. Дальше она
// отзывается: шестизначный код перебирается, если давать перебирать.
const MaxFactorAttempts = 5

type Enrollment struct {
	OTPAuthURL string
	ExpiresAt  time.Time
}

type pendingEnrollment struct {
	secret    string
	url       string
	expiresAt time.Time
}

type factorFailures struct {
	n  int
	at time.Time
}

// ConfirmFactor повышает частичную сессию по верному коду. Работает и для
// уже повышенной — это sudo-режим: свежий код перед опасным действием.
func (s *Service) ConfirmFactor(ctx context.Context, a Actor, code, device string) (account.Session, string, error) {
	if a.Space != account.SpaceAdmin || a.SessionID == "" {
		return account.Session{}, "", kernel.ErrForbidden
	}
	u, err := s.users.ByID(ctx, a.UserID)
	if err != nil {
		return account.Session{}, "", err
	}
	if err := u.CanEnterAdminSpace(); err != nil {
		return account.Session{}, "", err
	}
	secret, err := s.creds.TOTPSecret(ctx, u.ID)
	if err != nil {
		return account.Session{}, "", err
	}
	if secret == "" {
		return account.Session{}, "", ErrEnrollTOTP
	}
	if !s.totp.Validate(code, secret) {
		return account.Session{}, "", s.wrongCode(ctx, a.SessionID)
	}
	return s.elevate(ctx, a, u, device)
}

// BeginEnrollment выдаёт секрет на показ. Повторный вызов в той же сессии
// возвращает тот же секрет: обновление страницы не должно менять QR под
// рукой у человека, который уже сканирует.
func (s *Service) BeginEnrollment(ctx context.Context, a Actor) (Enrollment, error) {
	if a.Space != account.SpaceAdmin || a.SessionID == "" {
		return Enrollment{}, kernel.ErrForbidden
	}
	u, err := s.users.ByID(ctx, a.UserID)
	if err != nil {
		return Enrollment{}, err
	}
	if err := u.CanEnrollAdminFactor(); err != nil {
		return Enrollment{}, err
	}
	// По самой БД, а не по User: уже настроенный фактор из частичной
	// сессии не подменяется, иначе второй фактор ничего не защищал бы.
	// Passkey — тоже фактор: с ним настраивать TOTP паролем нельзя.
	if has, err := s.hasSecondFactor(ctx, u); err != nil {
		return Enrollment{}, err
	} else if has {
		return Enrollment{}, kernel.ErrConflict
	}

	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = map[kernel.SessionID]pendingEnrollment{}
	}
	for k, p := range s.pending {
		if !now.Before(p.expiresAt) {
			delete(s.pending, k)
		}
	}
	if p, ok := s.pending[a.SessionID]; ok {
		return Enrollment{OTPAuthURL: p.url, ExpiresAt: p.expiresAt}, nil
	}
	secret, url, err := s.totp.Provision(u.Login)
	if err != nil {
		return Enrollment{}, err
	}
	p := pendingEnrollment{secret: secret, url: url, expiresAt: now.Add(PartialSessionTTL)}
	s.pending[a.SessionID] = p
	return Enrollment{OTPAuthURL: p.url, ExpiresAt: p.expiresAt}, nil
}

// CompleteEnrollment сохраняет секрет по верному коду и повышает сессию:
// код только что доказал, что приложение приняло именно этот секрет.
func (s *Service) CompleteEnrollment(ctx context.Context, a Actor, code, device string) (account.Session, string, error) {
	if a.Space != account.SpaceAdmin || a.SessionID == "" {
		return account.Session{}, "", kernel.ErrForbidden
	}
	s.mu.Lock()
	p, ok := s.pending[a.SessionID]
	s.mu.Unlock()
	if !ok || !s.clock.Now().Before(p.expiresAt) {
		return account.Session{}, "", kernel.ErrExpired
	}
	if !s.totp.Validate(code, p.secret) {
		return account.Session{}, "", s.wrongCode(ctx, a.SessionID)
	}
	// Между шагами могли заблокировать, разжаловать или настроить фактор
	// из другой вкладки.
	u, err := s.users.ByID(ctx, a.UserID)
	if err != nil {
		return account.Session{}, "", err
	}
	if err := u.CanEnrollAdminFactor(); err != nil {
		return account.Session{}, "", err
	}
	if has, err := s.hasSecondFactor(ctx, u); err != nil {
		return account.Session{}, "", err
	} else if has {
		return account.Session{}, "", kernel.ErrConflict
	}
	if err := s.creds.SetTOTPSecret(ctx, u.ID, p.secret); err != nil {
		return account.Session{}, "", err
	}
	s.mu.Lock()
	delete(s.pending, a.SessionID)
	s.mu.Unlock()
	_ = s.audit.Record(ctx, u.ID, "user.enroll_totp", u.Login, "первый вход", nil, nil)
	return s.elevate(ctx, a, u, device)
}

// elevate — повышенная сессия вместо текущей: новый токен, прежняя
// отзывается. Так повышение прав всегда меняет токен.
func (s *Service) elevate(ctx context.Context, a Actor, u account.User, device string) (account.Session, string, error) {
	now := s.clock.Now()
	sess, raw, err := s.issue(ctx, u, account.SpaceAdmin, device, &now)
	if err != nil {
		return account.Session{}, "", err
	}
	if err := s.sessions.Revoke(ctx, a.SessionID, now); err != nil {
		return account.Session{}, "", err
	}
	s.mu.Lock()
	delete(s.failures, a.SessionID)
	delete(s.pending, a.SessionID)
	s.mu.Unlock()
	return sess, raw, nil
}

// wrongCode считает промахи по сессии. Пятый гасит её: дальше — только
// заново с паролем, и это уже под лимитом по адресу.
func (s *Service) wrongCode(ctx context.Context, id kernel.SessionID) error {
	now := s.clock.Now()
	s.mu.Lock()
	if s.failures == nil {
		s.failures = map[kernel.SessionID]factorFailures{}
	}
	for k, f := range s.failures {
		if now.Sub(f.at) > AdminSessionTTL {
			delete(s.failures, k)
		}
	}
	f := s.failures[id]
	f.n++
	f.at = now
	s.failures[id] = f
	s.mu.Unlock()

	if f.n >= MaxFactorAttempts {
		_ = s.sessions.Revoke(ctx, id, now)
		s.mu.Lock()
		delete(s.failures, id)
		delete(s.pending, id)
		s.mu.Unlock()
		return kernel.ErrExpired
	}
	return kernel.ErrUnauthorized
}
