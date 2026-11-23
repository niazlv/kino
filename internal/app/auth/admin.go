package auth

import (
	"context"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Служебные операции над учётными записями.
//
// Все — только из пространства admin и все — с записью в журнал: через
// месяц вопрос «почему у мамы пропал доступ» обязан иметь ответ. Ни одна
// из них не читает пользовательские данные (прогресс, отметки): это
// операции над ДОСТУПОМ, а не над тем, что человек смотрит (ADR-0005).
//
// Три из них — сброс пароля, роль и второй фактор — переписывают чужой
// доступ, поэтому требуют свежего кода (Policy.CanActSensitive).

func (s *Service) GetUser(ctx context.Context, a Actor, id kernel.UserID) (account.User, error) {
	if err := s.policy.CanAdminister(a); err != nil {
		return account.User{}, err
	}
	return s.users.ByID(ctx, id)
}

// ListSessions — живые сессии пользователя: где вошёл и когда истечёт.
// Отозванные и просроченные не показываются: они уже ничего не дают.
func (s *Service) ListSessions(ctx context.Context, a Actor, id kernel.UserID) ([]account.Session, error) {
	if err := s.policy.CanAdminister(a); err != nil {
		return nil, err
	}
	all, err := s.sessions.ListForUser(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	live := make([]account.Session, 0, len(all))
	for _, sess := range all {
		if sess.RevokedAt == nil && now.Before(sess.ExpiresAt) {
			live = append(live, sess)
		}
	}
	return live, nil
}

// RevokeSessions — «выйти везде». Себе тоже можно: админ, потерявший
// телефон, хочет именно этого, и то, что текущая сессия тоже погаснет,
// ожидаемо.
func (s *Service) RevokeSessions(ctx context.Context, a Actor, target kernel.UserID, reason string) error {
	if err := s.policy.CanAdminister(a); err != nil {
		return err
	}
	u, err := s.users.ByID(ctx, target)
	if err != nil {
		return err
	}
	if err := s.sessions.RevokeAllForUser(ctx, target, s.clock.Now()); err != nil {
		return err
	}
	return s.audit.Record(ctx, a.UserID, "user.revoke_sessions", u.Login, reason, nil, nil)
}

// SetRole меняет роль. Себе — нельзя: единственный админ, разжаловавший
// себя, оставил бы платформу без администратора вовсе.
//
// Понижение гасит все сессии: админская у человека уже не должна работать,
// а различать их здесь незачем — войти заново дешевле, чем оставить дыру.
func (s *Service) SetRole(ctx context.Context, a Actor, target kernel.UserID, role account.Role, reason string) error {
	if err := s.policy.CanActSensitive(a, s.clock.Now()); err != nil {
		return err
	}
	if role != account.RoleUser && role != account.RoleAdmin {
		return kernel.ErrInvalidInput
	}
	if a.UserID == target {
		return kernel.ErrInvalidInput
	}
	before, err := s.users.ByID(ctx, target)
	if err != nil {
		return err
	}
	if before.Role == role {
		return nil
	}
	if err := s.users.SetRole(ctx, target, role); err != nil {
		return err
	}
	if role == account.RoleUser {
		if err := s.sessions.RevokeAllForUser(ctx, target, s.clock.Now()); err != nil {
			return err
		}
	}
	after := before
	after.Role = role
	return s.audit.Record(ctx, a.UserID, "user.set_role", before.Login, reason, before, after)
}

// ResetPassword выдаёт новый временный пароль вместо забытого.
//
// Пароль генерируется, а не вводится админом: он его не выбирает и не
// знает дольше, чем нужно, чтобы передать. В журнал пароль не попадает.
// Все сессии гасятся: смена пароля — это ещё и «выгнать того, кто вошёл».
func (s *Service) ResetPassword(ctx context.Context, a Actor, target kernel.UserID, reason string) (string, error) {
	if err := s.policy.CanActSensitive(a, s.clock.Now()); err != nil {
		return "", err
	}
	u, err := s.users.ByID(ctx, target)
	if err != nil {
		return "", err
	}
	password := s.tokens.NewPassword()
	if err := account.ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return "", err
	}
	if err := s.creds.SetPassword(ctx, target, hash); err != nil {
		return "", err
	}
	if err := s.sessions.RevokeAllForUser(ctx, target, s.clock.Now()); err != nil {
		return "", err
	}
	if err := s.audit.Record(ctx, a.UserID, "user.reset_password", u.Login, reason, nil, nil); err != nil {
		return "", err
	}
	return password, nil
}

// ProvisionTOTP выдаёт или перевыпускает второй фактор.
//
// Только администраторам: пользовательскому пространству он не нужен, а
// лишний секрет — лишний способ потерять доступ. Возвращает otpauth-адрес
// для authenticator'а; показывается один раз, в БД остаётся только секрет.
func (s *Service) ProvisionTOTP(ctx context.Context, a Actor, target kernel.UserID, reason string) (string, error) {
	if err := s.policy.CanActSensitive(a, s.clock.Now()); err != nil {
		return "", err
	}
	u, err := s.users.ByID(ctx, target)
	if err != nil {
		return "", err
	}
	if u.Role != account.RoleAdmin {
		return "", kernel.ErrInvalidInput
	}
	secret, url, err := s.totp.Provision(u.Login)
	if err != nil {
		return "", err
	}
	if err := s.creds.SetTOTPSecret(ctx, target, secret); err != nil {
		return "", err
	}
	after := u
	after.TOTPEnabled = true
	if err := s.audit.Record(ctx, a.UserID, "user.provision_totp", u.Login, reason, u, after); err != nil {
		return "", err
	}
	return url, nil
}

func (s *Service) RevokeInvite(ctx context.Context, a Actor, code string) error {
	if err := s.policy.CanAdminister(a); err != nil {
		return err
	}
	if err := s.invites.Revoke(ctx, code, s.clock.Now()); err != nil {
		return err
	}
	return s.audit.Record(ctx, a.UserID, "invite.revoke", code, "", nil, nil)
}

func (s *Service) ListRecentInvites(ctx context.Context, a Actor, limit int) ([]account.Invite, error) {
	if err := s.policy.CanAdminister(a); err != nil {
		return nil, err
	}
	return s.invites.ListRecent(ctx, limit)
}
