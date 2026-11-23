package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Подделки минимальные: смена пароля трогает только эти четыре зависимости.
type fakeCreds struct {
	hash    string
	written string
	totp    string
}

func (f *fakeCreds) ByUser(context.Context, kernel.UserID) (account.Credential, error) {
	return account.Credential{UserID: 1, Hash: f.hash}, nil
}
func (f *fakeCreds) SetPassword(_ context.Context, _ kernel.UserID, h string) error {
	f.written = h
	return nil
}
func (f *fakeCreds) TOTPSecret(context.Context, kernel.UserID) (string, error) { return f.totp, nil }
func (f *fakeCreds) SetTOTPSecret(_ context.Context, _ kernel.UserID, secret string) error {
	f.totp = secret
	return nil
}
func (f *fakeCreds) ClearTOTPSecret(context.Context, kernel.UserID) error {
	f.totp = ""
	return nil
}

type fakeSessions struct {
	revokedAll bool
	created    []account.Session
	revoked    []kernel.SessionID
	// refresh — хеш refresh-токена -> id сессии устройства. Ключ тестам
	// удобнее держать сырым токеном: фейковый хешер обратим.
	refresh map[string]kernel.SessionID
}

func (f *fakeSessions) Create(_ context.Context, s account.Session, _ string) error {
	f.created = append(f.created, s)
	return nil
}
func (f *fakeSessions) CreateRenewable(_ context.Context, s account.Session, _, refreshHash string) error {
	f.created = append(f.created, s)
	if f.refresh == nil {
		f.refresh = make(map[string]kernel.SessionID)
	}
	// Фейковый хеш — "hash-<raw>-0000…": по нему сырой токен восстановим.
	raw := refreshHash[len("hash-") : len(refreshHash)-len("-0000000000000000")]
	f.refresh[raw] = s.ID
	return nil
}
func (f *fakeSessions) ByTokenHash(context.Context, string) (account.Session, error) {
	return account.Session{}, kernel.ErrNotFound
}
func (f *fakeSessions) ByRefreshHash(_ context.Context, hash string) (account.Session, error) {
	raw := hash[len("hash-") : len(hash)-len("-0000000000000000")]
	id, ok := f.refresh[raw]
	if !ok {
		return account.Session{}, kernel.ErrNotFound
	}
	for _, s := range f.created {
		if s.ID != id {
			continue
		}
		for _, r := range f.revoked {
			if r == id {
				at := time.Unix(0, 0)
				s.RevokedAt = &at
			}
		}
		return s, nil
	}
	return account.Session{}, kernel.ErrNotFound
}
func (f *fakeSessions) Revoke(_ context.Context, id kernel.SessionID, _ time.Time) error {
	f.revoked = append(f.revoked, id)
	return nil
}
func (f *fakeSessions) RevokeAllForUser(context.Context, kernel.UserID, time.Time) error {
	f.revokedAll = true
	return nil
}
func (f *fakeSessions) ListForUser(context.Context, kernel.UserID) ([]account.Session, error) {
	return nil, nil
}

// Хешер-заглушка: "h:" + пароль. Настоящий argon2 здесь не нужен — проверяем
// решения сервиса, а не криптографию.
type fakeHasher struct{}

func (fakeHasher) Hash(p string) (string, error) { return "h:" + p, nil }
func (fakeHasher) Verify(p, h string) bool       { return h != "" && h == "h:"+p }

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(1700000000, 0) }

func newPasswordService(c *fakeCreds, s *fakeSessions) *Service {
	return &Service{creds: c, sessions: s, hasher: fakeHasher{}, clock: fixedClock{}}
}

func TestChangePasswordRejectsWrongOldPassword(t *testing.T) {
	// Угнанная сессия не должна давать возможность запереть хозяина: старый
	// пароль спрашивается даже у вошедшего.
	c := &fakeCreds{hash: "h:пароль-настоящий"}
	s := &fakeSessions{}
	err := newPasswordService(c, s).ChangePassword(
		context.Background(), Actor{UserID: 1}, "пароль-чужой", "новый-длинный-пароль")
	if !errors.Is(err, kernel.ErrUnauthorized) {
		t.Fatalf("хотели ErrUnauthorized, получили %v", err)
	}
	if c.written != "" {
		t.Fatal("пароль изменён при неверном старом")
	}
	if s.revokedAll {
		t.Fatal("сессии погашены при неудачной смене")
	}
}

func TestChangePasswordRejectsShortNewPassword(t *testing.T) {
	c := &fakeCreds{hash: "h:пароль-настоящий"}
	s := &fakeSessions{}
	err := newPasswordService(c, s).ChangePassword(
		context.Background(), Actor{UserID: 1}, "пароль-настоящий", "коротк")
	if !errors.Is(err, kernel.ErrInvalidInput) {
		t.Fatalf("хотели ErrInvalidInput, получили %v", err)
	}
	if c.written != "" {
		t.Fatal("короткий пароль всё же записан")
	}
}

func TestChangePasswordStoresHashAndRevokesSessions(t *testing.T) {
	c := &fakeCreds{hash: "h:пароль-настоящий"}
	s := &fakeSessions{}
	err := newPasswordService(c, s).ChangePassword(
		context.Background(), Actor{UserID: 1}, "пароль-настоящий", "новый-длинный-пароль")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if c.written != "h:новый-длинный-пароль" {
		t.Fatalf("записан %q", c.written)
	}
	// Записывается ХЕШ, а не сам пароль: дамп базы не должен его выдавать.
	if strings.Contains(c.written, "новый-длинный-пароль") && !strings.HasPrefix(c.written, "h:") {
		t.Fatal("пароль записан в открытом виде")
	}
	if !s.revokedAll {
		t.Fatal("смена пароля не погасила остальные сессии — угнанная выживет")
	}
}
