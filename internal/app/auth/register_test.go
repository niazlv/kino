package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type fakeInvites struct {
	byCode map[string]account.Invite
	used   []string
}

func (f *fakeInvites) ByCode(_ context.Context, code string) (account.Invite, error) {
	i, ok := f.byCode[code]
	if !ok {
		return account.Invite{}, kernel.ErrNotFound
	}
	return i, nil
}
func (f *fakeInvites) Create(context.Context, account.Invite) error { return nil }
func (f *fakeInvites) MarkUsed(_ context.Context, code string, _ kernel.UserID, _ time.Time) error {
	f.used = append(f.used, code)
	return nil
}
func (f *fakeInvites) ListOpen(context.Context) ([]account.Invite, error) { return nil, nil }
func (f *fakeInvites) Revoke(context.Context, string, time.Time) error    { return nil }
func (f *fakeInvites) ListRecent(context.Context, int) ([]account.Invite, error) {
	return nil, nil
}

// createdUsers запоминает, кого завели: регистрация — это ровно один Create.
type createdUsers struct {
	fakeUsers
	created []account.User
}

func (f *createdUsers) Create(_ context.Context, u account.User, _ account.Credential) (account.User, error) {
	u.ID = kernel.UserID(len(f.created) + 1)
	f.created = append(f.created, u)
	return u, nil
}

func newRegisterService(mode Registration) (*Service, *fakeInvites, *createdUsers) {
	inv := &fakeInvites{byCode: map[string]account.Invite{
		"GOOD": {Code: "GOOD", ExpiresAt: time.Unix(1700000000, 0).Add(time.Hour)},
	}}
	users := &createdUsers{}
	s := &Service{users: users, invites: inv, hasher: fakeHasher{}, clock: fixedClock{}}
	if mode != "" {
		s.WithRegistration(mode)
	}
	return s, inv, users
}

func TestRegisterDefaultsToInvite(t *testing.T) {
	// Без настройки сервер остаётся семейным: без кода — никого.
	s, inv, users := newRegisterService("")
	if s.Registration() != RegistrationInvite {
		t.Fatalf("режим по умолчанию %q, ждали invite", s.Registration())
	}
	if _, err := s.Register(context.Background(), "", "anna", "password-1234", ""); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("без приглашения: %v, ждали not found", err)
	}
	if _, err := s.Register(context.Background(), "GOOD", "anna", "password-1234", ""); err != nil {
		t.Fatalf("с приглашением: %v", err)
	}
	if len(users.created) != 1 || len(inv.used) != 1 || inv.used[0] != "GOOD" {
		t.Fatalf("заведено %d, погашено %v", len(users.created), inv.used)
	}
}

func TestRegisterOpenWithoutInvite(t *testing.T) {
	s, inv, users := newRegisterService(RegistrationOpen)
	u, err := s.Register(context.Background(), "", "boris", "password-1234", "")
	if err != nil {
		t.Fatalf("открытая регистрация: %v", err)
	}
	if u.Login != "boris" || u.DisplayName != "boris" || u.Role != account.RoleUser {
		t.Fatalf("заведён %+v", u)
	}
	if len(users.created) != 1 || len(inv.used) != 0 {
		t.Fatalf("заведено %d, погашено %v", len(users.created), inv.used)
	}
}

func TestRegisterOpenStillChecksBroughtInvite(t *testing.T) {
	// Принесённый код проверяется и гасится: иначе одно приглашение
	// можно было бы «использовать» сколько угодно раз.
	s, inv, _ := newRegisterService(RegistrationOpen)
	if _, err := s.Register(context.Background(), "BAD", "vera", "password-1234", ""); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("чужой код: %v", err)
	}
	if _, err := s.Register(context.Background(), "GOOD", "vera", "password-1234", ""); err != nil {
		t.Fatalf("свой код: %v", err)
	}
	if len(inv.used) != 1 {
		t.Fatalf("погашено %v", inv.used)
	}
}

func TestRegisterClosed(t *testing.T) {
	s, _, users := newRegisterService(RegistrationClosed)
	if _, err := s.Register(context.Background(), "GOOD", "gleb", "password-1234", ""); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("закрытый сервер: %v, ждали forbidden", err)
	}
	if len(users.created) != 0 {
		t.Fatalf("заведено %d", len(users.created))
	}
}

func TestParseRegistration(t *testing.T) {
	for in, want := range map[string]Registration{"": RegistrationInvite, "invite": RegistrationInvite, "open": RegistrationOpen, "closed": RegistrationClosed} {
		got, err := ParseRegistration(in)
		if err != nil || got != want {
			t.Fatalf("%q: %q, %v", in, got, err)
		}
	}
	// Опечатка не должна молча открыть сервер всем.
	if _, err := ParseRegistration("opne"); !errors.Is(err, kernel.ErrInvalidInput) {
		t.Fatalf("опечатка: %v", err)
	}
}
