// Package account — пользователи, роли, приглашения, сессии.
// Пользовательская идентичность. Не путать с catalog-идентичностью
// (склейка одного тайтла из разных площадок) — та живёт в app/matching.
package account

import (
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

type User struct {
	ID          kernel.UserID
	Login       string
	DisplayName string
	Role        Role
	Disabled    bool
	TOTPEnabled bool
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin && !u.Disabled }

// CanEnterAdminSpace — кого пускать на первый шаг входа в админку: роль и
// незаблокированность. Второй фактор проверяется отдельным шагом уже внутри
// частичной сессии (Session.Elevated); без него сессия ни на что не годится.
func (u User) CanEnterAdminSpace() error {
	switch {
	case u.Disabled:
		return kernel.ErrForbidden
	case u.Role != RoleAdmin:
		return kernel.ErrForbidden
	}
	return nil
}

// CanEnrollAdminFactor — кому можно настроить второй фактор самому, при
// первом входе: администратору, у которого его ещё нет. Уже настроенный
// одним паролем не подменяется — иначе второй фактор ничего не защищал бы.
func (u User) CanEnrollAdminFactor() error {
	switch {
	case u.Disabled, u.Role != RoleAdmin:
		return kernel.ErrForbidden
	case u.TOTPEnabled:
		return kernel.ErrConflict
	}
	return nil
}

// Invite — единственный путь регистрации. Платформа не публичная.
type Invite struct {
	Code      string
	CreatedBy kernel.UserID
	ExpiresAt time.Time
	UsedBy    *kernel.UserID
	UsedAt    *time.Time
}

func (i Invite) Redeemable(now time.Time) error {
	if i.UsedBy != nil {
		return kernel.ErrConflict
	}
	if now.After(i.ExpiresAt) {
		return kernel.ErrExpired
	}
	return nil
}

type Session struct {
	ID         kernel.SessionID
	UserID     kernel.UserID
	Space      Space
	DeviceKind string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	// FactorAt — когда подтверждён второй фактор. Пусто у частичной сессии
	// админского пространства: пароль проверен, код ещё нет. Время, а не
	// флаг: опасные действия требуют кода, введённого недавно.
	FactorAt *time.Time
	// RefreshExpiresAt — до какого момента сессию можно продлить refresh-
	// токеном. Есть только у сессий устройств (утилита-загрузчик): они
	// живут без браузера и продлеваются сами. У браузерной сессии пусто.
	RefreshExpiresAt *time.Time
}

// Elevated — второй фактор подтверждён. Частичная сессия годится только
// на то, чтобы этот код ввести или настроить.
func (s Session) Elevated() bool { return s.FactorAt != nil }

// Renewable — сессия устройства, при ней refresh-токен.
func (s Session) Renewable() bool { return s.RefreshExpiresAt != nil }

// CanRefresh — можно ли обменять refresh-токен этой сессии на новую пару.
// Сама сессия при этом может быть уже истёкшей: ради этого refresh и нужен.
func (s Session) CanRefresh(now time.Time) error {
	switch {
	case s.RevokedAt != nil:
		return kernel.ErrUnauthorized
	case s.RefreshExpiresAt == nil:
		return kernel.ErrForbidden
	case now.After(*s.RefreshExpiresAt):
		return kernel.ErrExpired
	}
	return nil
}

// DeviceAuth — запрос устройства на вход: код, который человек подтверждает
// на сайте своей сессией. Живёт минуты.
type DeviceAuth struct {
	UserCode   string
	Device     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ApprovedBy *kernel.UserID
	Denied     bool
}

// Space — три пространства с разной аутентификацией и разной поверхностью.
// Сессия одного пространства не действует в другом: сессия пользователя
// физически не может быть предъявлена админке.
type Space string

const (
	SpaceUser    Space = "user"    // публичный API, cookie-сессия
	SpaceAdmin   Space = "admin"   // отдельный listener, роль + TOTP, tailnet
	SpaceService Space = "service" // gateway/crawler -> core, gRPC, машинные креды
)

func (s Session) Valid(now time.Time, want Space) error {
	switch {
	case s.RevokedAt != nil:
		return kernel.ErrUnauthorized
	case now.After(s.ExpiresAt):
		return kernel.ErrExpired
	case s.Space != want:
		return kernel.ErrForbidden
	}
	return nil
}
