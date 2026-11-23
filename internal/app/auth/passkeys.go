package auth

import (
	"context"
	"errors"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Passkey (WebAuthn) в админском пространстве.
//
// Криптография и протокол — в адаптере; здесь решения: кому можно завести
// ключ, что даёт подпись и когда сессия повышается. Ключ с проверкой
// пользователя на устройстве — это владение плюс биометрия или PIN, то есть
// оба фактора сразу, поэтому вход по нему выдаёт повышенную сессию без
// пароля, а подпись в частичной сессии заменяет код.

// hasSecondFactor — есть ли хоть один фактор: TOTP или ключ. По самой БД,
// а не по флагу в User: тот мог устареть между шагами.
func (s *Service) hasSecondFactor(ctx context.Context, u account.User) (bool, error) {
	if u.TOTPEnabled {
		return true, nil
	}
	if secret, err := s.creds.TOTPSecret(ctx, u.ID); err != nil {
		return false, err
	} else if secret != "" {
		return true, nil
	}
	keys, err := s.passkeys.ListForUser(ctx, u.ID)
	if err != nil {
		return false, err
	}
	return len(keys) > 0, nil
}

// ListPasskeys — ключи пользователя. Свои видит любая сессия админского
// пространства: частичной нужно знать, чем подтверждаться. Чужие — только
// повышенная: карточке пользователя нужен список для удаления.
func (s *Service) ListPasskeys(ctx context.Context, a Actor, target kernel.UserID) ([]account.Passkey, error) {
	if a.Space != account.SpaceAdmin {
		return nil, kernel.ErrForbidden
	}
	if target != a.UserID {
		if err := s.policy.CanAdminister(a); err != nil {
			return nil, err
		}
	}
	return s.passkeys.ListForUser(ctx, target)
}

// CanRegisterPasskey — кому можно завести ключ, и какие ключи уже есть
// (их исключают из регистрации). Повышенной сессии — со свежим кодом
// (sudo); частичной — только если у человека нет НИ ОДНОГО фактора, как и
// у настройки TOTP: пароль один не должен добавлять фактор к уже
// защищённой учётной записи.
func (s *Service) CanRegisterPasskey(ctx context.Context, a Actor) (account.User, []account.Passkey, error) {
	if a.Space != account.SpaceAdmin || a.SessionID == "" {
		return account.User{}, nil, kernel.ErrForbidden
	}
	u, err := s.users.ByID(ctx, a.UserID)
	if err != nil {
		return account.User{}, nil, err
	}
	if err := u.CanEnterAdminSpace(); err != nil {
		return account.User{}, nil, err
	}
	keys, err := s.passkeys.ListForUser(ctx, u.ID)
	if err != nil {
		return account.User{}, nil, err
	}
	if a.Elevated() {
		if err := s.policy.CanActSensitive(a, s.clock.Now()); err != nil {
			return account.User{}, nil, err
		}
		return u, keys, nil
	}
	if u.TOTPEnabled || len(keys) > 0 {
		return account.User{}, nil, kernel.ErrConflict
	}
	return u, keys, nil
}

// AddPasskey сохраняет проверенный адаптером ключ. Частичную сессию
// повышает: ключ только что подтверждён на устройстве, и это второй
// фактор. Повышенной возвращает пустой токен — cookie менять незачем.
func (s *Service) AddPasskey(ctx context.Context, a Actor, p account.Passkey, device string) (account.Session, string, error) {
	u, _, err := s.CanRegisterPasskey(ctx, a)
	if err != nil {
		return account.Session{}, "", err
	}
	p.UserID = u.ID
	saved, err := s.passkeys.Create(ctx, p)
	if err != nil {
		return account.Session{}, "", err
	}
	_ = s.audit.Record(ctx, u.ID, "user.add_passkey", u.Login, saved.Name, nil, nil)
	if a.Elevated() {
		return account.Session{}, "", nil
	}
	return s.elevate(ctx, a, u, device)
}

// RemovePasskey — свой или чужой, только со свежим кодом. Последний фактор
// снять нельзя: иначе следующий вход паролем заведёт новый, и защита
// свелась бы к паролю. Сначала другой фактор, потом удаление.
func (s *Service) RemovePasskey(ctx context.Context, a Actor, target kernel.UserID, id int64, reason string) error {
	if err := s.policy.CanActSensitive(a, s.clock.Now()); err != nil {
		return err
	}
	u, err := s.users.ByID(ctx, target)
	if err != nil {
		return err
	}
	keys, err := s.passkeys.ListForUser(ctx, target)
	if err != nil {
		return err
	}
	var name string
	for _, k := range keys {
		if k.ID == id {
			name = k.Name
		}
	}
	if name == "" {
		return kernel.ErrNotFound
	}
	if !u.TOTPEnabled && len(keys) == 1 {
		return kernel.ErrConflict
	}
	if err := s.passkeys.Delete(ctx, id, target); err != nil {
		return err
	}
	return s.audit.Record(ctx, a.UserID, "user.remove_passkey", u.Login, reason, name, nil)
}

// PasskeyOwner — пользователь и его ключи по идентификатору из подписи.
// Без Policy: вызывается адаптером до того, как есть сессия, как ByLogin
// в Login. Наружу уходит только то, что нужно проверить подпись.
func (s *Service) PasskeyOwner(ctx context.Context, id kernel.UserID) (account.User, []account.Passkey, error) {
	u, err := s.users.ByID(ctx, id)
	if err != nil {
		return account.User{}, nil, err
	}
	keys, err := s.passkeys.ListForUser(ctx, id)
	return u, keys, err
}

// LoginWithPasskey — вход без пароля по уже проверенной подписи. Сессия
// сразу повышенная: ключ с проверкой пользователя — оба фактора.
func (s *Service) LoginWithPasskey(ctx context.Context, credentialID []byte, signCount uint32, device string) (account.Session, string, error) {
	p, err := s.passkeys.ByCredentialID(ctx, credentialID)
	if err != nil {
		if errors.Is(err, kernel.ErrNotFound) {
			return account.Session{}, "", kernel.ErrUnauthorized
		}
		return account.Session{}, "", err
	}
	u, err := s.users.ByID(ctx, p.UserID)
	if err != nil {
		return account.Session{}, "", err
	}
	if u.Disabled {
		return account.Session{}, "", kernel.ErrUnauthorized
	}
	if err := u.CanEnterAdminSpace(); err != nil {
		return account.Session{}, "", err
	}
	now := s.clock.Now()
	if err := s.passkeys.Touch(ctx, p.ID, signCount, now); err != nil {
		return account.Session{}, "", err
	}
	return s.issue(ctx, u, account.SpaceAdmin, device, &now)
}

// ElevateWithPasskey — второй шаг или sudo подписью вместо кода. Ключ
// обязан принадлежать той же учётной записи, что и сессия.
func (s *Service) ElevateWithPasskey(ctx context.Context, a Actor, credentialID []byte, signCount uint32, device string) (account.Session, string, error) {
	if a.Space != account.SpaceAdmin || a.SessionID == "" {
		return account.Session{}, "", kernel.ErrForbidden
	}
	p, err := s.passkeys.ByCredentialID(ctx, credentialID)
	if err != nil || p.UserID != a.UserID {
		return account.Session{}, "", kernel.ErrUnauthorized
	}
	u, err := s.users.ByID(ctx, a.UserID)
	if err != nil {
		return account.Session{}, "", err
	}
	if err := u.CanEnterAdminSpace(); err != nil {
		return account.Session{}, "", err
	}
	if err := s.passkeys.Touch(ctx, p.ID, signCount, s.clock.Now()); err != nil {
		return account.Session{}, "", err
	}
	return s.elevate(ctx, a, u, device)
}
