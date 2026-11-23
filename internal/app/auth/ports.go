package auth

import (
	"context"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type UserRepo interface {
	ByID(ctx context.Context, id kernel.UserID) (account.User, error)
	ByLogin(ctx context.Context, login string) (account.User, error)
	List(ctx context.Context) ([]account.User, error)
	Create(ctx context.Context, u account.User, c account.Credential) (account.User, error)
	SetDisabled(ctx context.Context, id kernel.UserID, disabled bool) error
	SetRole(ctx context.Context, id kernel.UserID, role account.Role) error
	TouchLogin(ctx context.Context, id kernel.UserID, at time.Time) error
}

type CredentialRepo interface {
	ByUser(ctx context.Context, id kernel.UserID) (account.Credential, error)
	// TOTPSecret отделён от User: секрет второго фактора не должен
	// подниматься вместе с обычным чтением пользователя.
	// SetPassword заменяет хеш пароля. Отдельный метод, а не обновление
	// всего Credential: менять пароль и второй фактор — разные действия
	// с разными правами.
	SetPassword(ctx context.Context, id kernel.UserID, hash string) error
	TOTPSecret(ctx context.Context, id kernel.UserID) (string, error)
	SetTOTPSecret(ctx context.Context, id kernel.UserID, secret string) error
	// ClearTOTPSecret снимает фактор: следующий вход его настроит заново.
	ClearTOTPSecret(ctx context.Context, id kernel.UserID) error
}

type InviteRepo interface {
	ByCode(ctx context.Context, code string) (account.Invite, error)
	Create(ctx context.Context, i account.Invite) error
	MarkUsed(ctx context.Context, code string, by kernel.UserID, at time.Time) error
	// ListOpen — ещё пригодные: не использованные и не истёкшие.
	ListOpen(ctx context.Context) ([]account.Invite, error)
	// Revoke делает инвайт непригодным раньше срока. Уже использованный
	// трогать нельзя: он — часть истории появления пользователя.
	Revoke(ctx context.Context, code string, at time.Time) error
	// ListRecent — история: использованные и истёкшие, свежие первыми.
	ListRecent(ctx context.Context, limit int) ([]account.Invite, error)
}

// SessionRepo хранит ХЕШ токена, а не токен: утечка дампа БД не должна
// давать возможность предъявить чужую сессию.
type SessionRepo interface {
	Create(ctx context.Context, s account.Session, tokenHash string) error
	// CreateRenewable — сессия устройства: при ней хеш refresh-токена.
	CreateRenewable(ctx context.Context, s account.Session, tokenHash, refreshHash string) error
	ByTokenHash(ctx context.Context, hash string) (account.Session, error)
	ByRefreshHash(ctx context.Context, hash string) (account.Session, error)
	Revoke(ctx context.Context, id kernel.SessionID, at time.Time) error
	RevokeAllForUser(ctx context.Context, u kernel.UserID, at time.Time) error
	ListForUser(ctx context.Context, u kernel.UserID) ([]account.Session, error)
}

// PasskeyRepo — ключи WebAuthn. Протокол живёт в адаптере, здесь хранение.
type PasskeyRepo interface {
	Create(ctx context.Context, p account.Passkey) (account.Passkey, error)
	ByCredentialID(ctx context.Context, credentialID []byte) (account.Passkey, error)
	ListForUser(ctx context.Context, u kernel.UserID) ([]account.Passkey, error)
	// Touch — счётчик подписей и время использования после каждой проверки.
	Touch(ctx context.Context, id int64, signCount uint32, at time.Time) error
	Delete(ctx context.Context, id int64, u kernel.UserID) error
}

// Hasher — криптография это инфраструктура, домен про неё не знает.
type Hasher interface {
	Hash(password string) (string, error)
	// Verify обязан быть постоянного времени и не различать «нет юзера»
	// и «неверный пароль» на уровне таймингов.
	Verify(password, hash string) bool
}

// TOTP — второй фактор для служебного пространства.
type TOTP interface {
	Validate(code, secret string) bool
	Provision(login string) (secret, otpauthURL string, err error)
}

type TokenGen interface {
	NewToken() (raw string, hash string, err error)
	HashToken(raw string) string
	NewInviteCode() string
	// NewPassword — временный пароль для сброса. Генерируется, а не
	// придумывается админом: так он его не выбирает и не запоминает.
	NewPassword() string
	// NewUserCode — код для человека при входе устройства: короткий, чтобы
	// его можно было набрать, и неугадываемый за десять минут жизни.
	NewUserCode() string
}

// Audit — каждая мутация в служебном пространстве. Не ради формальностей:
// ради вопроса «почему у мамы вчера пропал доступ» через месяц.
type Audit interface {
	Record(ctx context.Context, actor kernel.UserID, action, target, reason string, before, after any) error
}

type Clock interface{ Now() time.Time }
