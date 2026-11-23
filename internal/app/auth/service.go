package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

const (
	UserSessionTTL  = 30 * 24 * time.Hour
	AdminSessionTTL = 12 * time.Hour // админская сессия живёт заметно меньше
	// PartialSessionTTL — между паролем и кодом. Хватает достать телефон;
	// не хватает, чтобы забытая вкладка осталась дырой.
	PartialSessionTTL = 10 * time.Minute
	InviteTTL         = 7 * 24 * time.Hour
)

type Service struct {
	users    UserRepo
	creds    CredentialRepo
	invites  InviteRepo
	sessions SessionRepo
	passkeys PasskeyRepo
	hasher   Hasher
	tokens   TokenGen
	totp     TOTP
	audit    Audit
	clock    Clock
	policy   Policy

	// pending — начатые, но не подтверждённые регистрации второго фактора,
	// failures — неверные коды по сессиям. В памяти процесса и на минуты:
	// секрет попадает в БД только после того, как человек доказал кодом,
	// что приложение его приняло, а пять промахов гасят частичную сессию.
	mu       sync.Mutex
	pending  map[kernel.SessionID]pendingEnrollment
	failures map[kernel.SessionID]factorFailures
	// devices — ожидающие входы устройств, по хешу кода устройства;
	// deviceByCode — тот же набор по коду человека. См. device.go.
	devices      map[string]*deviceAuth
	deviceByCode map[string]string

	// registration — кто может завести себе аккаунт сам (см. Registration).
	registration Registration
}

// Registration — режим самостоятельной регистрации на сервере.
//
// Семейному серверу хватает приглашений (так по умолчанию); тому, кто
// поднял свой сервер для друзей или сообщества, — открытой регистрации;
// закрытый сервер заводит людей только из админки.
type Registration string

const (
	// RegistrationInvite — только по коду приглашения (по умолчанию).
	RegistrationInvite Registration = "invite"
	// RegistrationOpen — любой, кто дошёл до сервера; приглашение не нужно,
	// но если его принесли, оно погашается как обычно.
	RegistrationOpen Registration = "open"
	// RegistrationClosed — самостоятельной регистрации нет вовсе.
	RegistrationClosed Registration = "closed"
)

// ParseRegistration читает режим из конфига. Пустое — приглашения;
// незнакомое — ошибка: опечатка не должна молча открыть сервер всем.
func ParseRegistration(s string) (Registration, error) {
	switch Registration(s) {
	case "", RegistrationInvite:
		return RegistrationInvite, nil
	case RegistrationOpen, RegistrationClosed:
		return Registration(s), nil
	}
	return "", fmt.Errorf("registration %q: want invite, open or closed: %w", s, kernel.ErrInvalidInput)
}

// WithRegistration задаёт режим регистрации (см. Registration).
func (s *Service) WithRegistration(r Registration) *Service {
	s.registration = r
	return s
}

// Registration — действующий режим: клиенты показывают по нему форму
// регистрации (с приглашением, без него или никакой).
func (s *Service) Registration() Registration {
	if s.registration == "" {
		return RegistrationInvite
	}
	return s.registration
}

func NewService(
	users UserRepo, creds CredentialRepo, invites InviteRepo, sessions SessionRepo,
	passkeys PasskeyRepo, hasher Hasher, tokens TokenGen, totp TOTP, audit Audit, clock Clock,
) *Service {
	return &Service{
		users: users, creds: creds, invites: invites, sessions: sessions, passkeys: passkeys,
		hasher: hasher, tokens: tokens, totp: totp, audit: audit, clock: clock,
	}
}

// ── Регистрация ────────────────────────────────────────────────────────────

// Register — появление пользователя по инвайту. По умолчанию публичной
// регистрации нет: платформа семейная; сервер, поднятый для многих, может
// открыть её (RegistrationOpen). Второй путь — CreateUser из админки.
func (s *Service) Register(ctx context.Context, code, login, password, displayName string) (account.User, error) {
	mode := s.Registration()
	if mode == RegistrationClosed {
		return account.User{}, fmt.Errorf("registration closed: %w", kernel.ErrForbidden)
	}
	// Открытая регистрация без кода: приглашение не ищем и не гасим.
	withInvite := mode == RegistrationInvite || code != ""
	if err := account.ValidateLogin(login); err != nil {
		return account.User{}, fmt.Errorf("login: %w", err)
	}
	if err := account.ValidatePassword(password); err != nil {
		return account.User{}, fmt.Errorf("password: %w", err)
	}

	if withInvite {
		inv, err := s.invites.ByCode(ctx, code)
		if err != nil {
			return account.User{}, fmt.Errorf("invite: %w", err)
		}
		if err := inv.Redeemable(s.clock.Now()); err != nil {
			return account.User{}, fmt.Errorf("invite %q: %w", code, err)
		}
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return account.User{}, err
	}
	if displayName == "" {
		displayName = login
	}

	u, err := s.users.Create(ctx,
		account.User{
			Login: login, DisplayName: displayName,
			Role: account.RoleUser, CreatedAt: s.clock.Now(),
		},
		account.Credential{Hash: hash},
	)
	if err != nil {
		return account.User{}, err
	}
	if withInvite {
		if err := s.invites.MarkUsed(ctx, code, u.ID, s.clock.Now()); err != nil {
			return account.User{}, err
		}
	}
	return u, nil
}

// CreateUser заводит пользователя из админки, без инвайта: для того, кто
// войдёт по коду с телефона (ApproveDeviceFor) и придумывать пароль не
// будет. Пароль всё же выдаётся — временный, один раз, как при сбросе, —
// чтобы при желании войти и с компьютера. Админ его не выбирает, в журнал
// он не попадает.
func (s *Service) CreateUser(ctx context.Context, a Actor, login, displayName string) (account.User, string, error) {
	if err := s.policy.CanAdminister(a); err != nil {
		return account.User{}, "", err
	}
	if err := account.ValidateLogin(login); err != nil {
		return account.User{}, "", fmt.Errorf("login: %w", err)
	}
	password := s.tokens.NewPassword()
	if err := account.ValidatePassword(password); err != nil {
		return account.User{}, "", err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return account.User{}, "", err
	}
	if displayName == "" {
		displayName = login
	}
	u, err := s.users.Create(ctx,
		account.User{
			Login: login, DisplayName: displayName,
			Role: account.RoleUser, CreatedAt: s.clock.Now(),
		},
		account.Credential{Hash: hash},
	)
	if err != nil {
		return account.User{}, "", err
	}
	if err := s.audit.Record(ctx, a.UserID, "user.create", u.Login, "", nil, nil); err != nil {
		return account.User{}, "", err
	}
	return u, password, nil
}

// ── Вход ───────────────────────────────────────────────────────────────────

type Credentials struct {
	Login    string
	Password string
	Device   string
}

// Login выдаёт сессию В КОНКРЕТНОМ пространстве. Сессия, выданная для
// SpaceUser, не будет принята админкой, и наоборот — проверка в Session.Valid.
//
// В админском пространстве это первый шаг из двух: сессия выдаётся
// ЧАСТИЧНАЯ, без подтверждённого фактора, и Policy не даёт ей ничего,
// кроме ввода кода (ConfirmFactor) или его настройки (BeginEnrollment).
func (s *Service) Login(ctx context.Context, c Credentials, space account.Space) (account.Session, string, error) {
	u, err := s.users.ByLogin(ctx, c.Login)
	if err != nil {
		if errors.Is(err, kernel.ErrNotFound) {
			// Пароль всё равно прогоняем через хешер: иначе существование
			// логина утекает через разницу во времени ответа. Verify обязан
			// потратить то же время и на заведомо непригодном хеше.
			s.hasher.Verify(c.Password, "")
			return account.Session{}, "", kernel.ErrUnauthorized
		}
		return account.Session{}, "", err
	}

	cred, err := s.creds.ByUser(ctx, u.ID)
	if err != nil {
		return account.Session{}, "", err
	}
	if !s.hasher.Verify(c.Password, cred.Hash) || u.Disabled {
		return account.Session{}, "", kernel.ErrUnauthorized
	}

	// В админское пространство пускает роль; второй фактор — следующий шаг.
	// Не-администратору частичная сессия не выдаётся вовсе.
	if space == account.SpaceAdmin {
		if err := u.CanEnterAdminSpace(); err != nil {
			return account.Session{}, "", err
		}
	}
	return s.issue(ctx, u, space, c.Device, nil)
}

// issue выдаёт сессию уже проверенному пользователю. factorAt задаёт
// уровень: nil — частичная (в админском пространстве) или обычная (в
// пользовательском), иначе повышенная.
func (s *Service) issue(ctx context.Context, u account.User, space account.Space,
	device string, factorAt *time.Time) (account.Session, string, error) {
	raw, hash, err := s.tokens.NewToken()
	if err != nil {
		return account.Session{}, "", err
	}
	now := s.clock.Now()
	ttl := UserSessionTTL
	if space == account.SpaceAdmin {
		ttl = PartialSessionTTL
		if factorAt != nil {
			ttl = AdminSessionTTL
		}
	}
	sess := account.Session{
		ID:         kernel.SessionID(hash[:16]),
		UserID:     u.ID,
		Space:      space,
		DeviceKind: device,
		IssuedAt:   now,
		ExpiresAt:  now.Add(ttl),
		FactorAt:   factorAt,
	}
	if err := s.sessions.Create(ctx, sess, hash); err != nil {
		return account.Session{}, "", err
	}
	_ = s.users.TouchLogin(ctx, u.ID, now)
	return sess, raw, nil
}

// Authenticate — из сырого токена в Actor. Вызывается middleware транспорта;
// решения «что этому актору можно» принимает Policy, а не middleware.
func (s *Service) Authenticate(ctx context.Context, rawToken string, want account.Space) (Actor, error) {
	if rawToken == "" {
		return Actor{}, kernel.ErrUnauthorized
	}
	sess, err := s.sessions.ByTokenHash(ctx, s.tokens.HashToken(rawToken))
	if err != nil {
		return Actor{}, kernel.ErrUnauthorized
	}
	if err := sess.Valid(s.clock.Now(), want); err != nil {
		return Actor{}, err
	}
	u, err := s.users.ByID(ctx, sess.UserID)
	if err != nil {
		return Actor{}, err
	}
	if u.Disabled {
		return Actor{}, kernel.ErrForbidden
	}
	return Actor{UserID: u.ID, Role: u.Role, Space: sess.Space,
		SessionID: sess.ID, FactorAt: sess.FactorAt}, nil
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	sess, err := s.sessions.ByTokenHash(ctx, s.tokens.HashToken(rawToken))
	if err != nil {
		return nil // разлогин идемпотентен
	}
	return s.sessions.Revoke(ctx, sess.ID, s.clock.Now())
}

// ── Служебные операции (пространство admin) ────────────────────────────────

func (s *Service) CreateInvite(ctx context.Context, a Actor) (account.Invite, error) {
	if err := s.policy.CanAdminister(a); err != nil {
		return account.Invite{}, err
	}
	inv := account.Invite{
		Code:      s.tokens.NewInviteCode(),
		CreatedBy: a.UserID,
		ExpiresAt: s.clock.Now().Add(InviteTTL),
	}
	if err := s.invites.Create(ctx, inv); err != nil {
		return account.Invite{}, err
	}
	_ = s.audit.Record(ctx, a.UserID, "invite.create", inv.Code, "", nil, inv)
	return inv, nil
}

func (s *Service) ListUsers(ctx context.Context, a Actor) ([]account.User, error) {
	if err := s.policy.CanAdminister(a); err != nil {
		return nil, err
	}
	return s.users.List(ctx)
}

func (s *Service) ListOpenInvites(ctx context.Context, a Actor) ([]account.Invite, error) {
	if err := s.policy.CanAdminister(a); err != nil {
		return nil, err
	}
	return s.invites.ListOpen(ctx)
}

func (s *Service) SetDisabled(ctx context.Context, a Actor, target kernel.UserID, disabled bool, reason string) error {
	if err := s.policy.CanAdminister(a); err != nil {
		return err
	}
	if a.UserID == target {
		return kernel.ErrInvalidInput // не дать заблокировать себя и потерять админку
	}
	before, err := s.users.ByID(ctx, target)
	if err != nil {
		return err
	}
	if err := s.users.SetDisabled(ctx, target, disabled); err != nil {
		return err
	}
	// Блокировка отзывает все сессии: иначе выключенный пользователь
	// продолжает смотреть до истечения токена.
	if disabled {
		if err := s.sessions.RevokeAllForUser(ctx, target, s.clock.Now()); err != nil {
			return err
		}
	}
	after := before
	after.Disabled = disabled
	return s.audit.Record(ctx, a.UserID, "user.set_disabled", before.Login, reason, before, after)
}

// Me возвращает профиль действующего пользователя.
//
// Actor намеренно несёт только идентификатор, роль и пространство — всё,
// что нужно для решений о доступе. Отображаемое имя к таким решениям
// отношения не имеет, поэтому за ним отдельный поход в хранилище.
func (s *Service) Me(ctx context.Context, a Actor) (account.User, error) {
	return s.users.ByID(ctx, a.UserID)
}

// ChangePassword меняет пароль самому себе.
//
// Старый пароль спрашивается всегда, даже у вошедшего: угнанная сессия не
// должна давать возможность запереть хозяина в его же учётной записи.
//
// После смены все ОСТАЛЬНЫЕ сессии гасятся. Смена пароля — обычная реакция
// на «кажется, меня взломали», и оставить чужую сессию живой значило бы
// сделать это действие бессмысленным. Текущая сессия остаётся, иначе
// человек выкидывал бы сам себя при каждой смене.
func (s *Service) ChangePassword(ctx context.Context, a Actor, old, new string) error {
	if err := account.ValidatePassword(new); err != nil {
		return err
	}
	cred, err := s.creds.ByUser(ctx, a.UserID)
	if err != nil {
		return err
	}
	if !s.hasher.Verify(old, cred.Hash) {
		return kernel.ErrUnauthorized
	}
	hash, err := s.hasher.Hash(new)
	if err != nil {
		return err
	}
	if err := s.creds.SetPassword(ctx, a.UserID, hash); err != nil {
		return err
	}
	// Гасим всё, затем текущая сессия останется недействительной — поэтому
	// вызывающий обязан выдать новую. Транспорт это и делает.
	return s.sessions.RevokeAllForUser(ctx, a.UserID, s.clock.Now())
}
