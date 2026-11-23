package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type fakeUsers struct{ u account.User }

func (f *fakeUsers) ByID(context.Context, kernel.UserID) (account.User, error) { return f.u, nil }
func (f *fakeUsers) ByLogin(_ context.Context, login string) (account.User, error) {
	if login != f.u.Login {
		return account.User{}, kernel.ErrNotFound
	}
	return f.u, nil
}
func (f *fakeUsers) List(context.Context) ([]account.User, error) { return []account.User{f.u}, nil }
func (f *fakeUsers) Create(context.Context, account.User, account.Credential) (account.User, error) {
	return account.User{}, nil
}
func (f *fakeUsers) SetDisabled(context.Context, kernel.UserID, bool) error     { return nil }
func (f *fakeUsers) SetRole(context.Context, kernel.UserID, account.Role) error { return nil }
func (f *fakeUsers) TouchLogin(context.Context, kernel.UserID, time.Time) error { return nil }

// Приложение принимает ровно один код для данного секрета.
type fakeTOTP struct{ secret, code string }

func (f fakeTOTP) Validate(code, secret string) bool { return code == f.code && secret == f.secret }
func (f fakeTOTP) Provision(login string) (string, string, error) {
	return f.secret, "otpauth://totp/kino:" + login + "?secret=" + f.secret, nil
}

type fakeTokens struct{ n int }

func (f *fakeTokens) NewToken() (string, string, error) {
	f.n++
	raw := fmt.Sprintf("raw%d", f.n)
	return raw, "hash-" + raw + "-0000000000000000", nil
}
func (fakeTokens) HashToken(raw string) string { return "hash-" + raw + "-0000000000000000" }
func (fakeTokens) NewInviteCode() string       { return "ABCDE-FGHJK" }
func (fakeTokens) NewPassword() string         { return "abcd-efgh-jkmn-pqrs" }
func (f *fakeTokens) NewUserCode() string {
	f.n++
	return fmt.Sprintf("CODE-%04d", f.n)
}

type fakeAudit struct{ actions []string }

func (f *fakeAudit) Record(_ context.Context, _ kernel.UserID, action, _, _ string, _, _ any) error {
	f.actions = append(f.actions, action)
	return nil
}

var admin = account.User{ID: 1, Login: "niaz", Role: account.RoleAdmin}

func newFactorService(u account.User, creds *fakeCreds) (*Service, *fakeSessions, *fakeAudit) {
	sessions := &fakeSessions{}
	audit := &fakeAudit{}
	return &Service{
		users: &fakeUsers{u: u}, creds: creds, sessions: sessions, passkeys: &fakePasskeys{},
		hasher: fakeHasher{}, tokens: &fakeTokens{}, totp: fakeTOTP{secret: "SECRET", code: "123456"},
		audit: audit, clock: fixedClock{},
	}, sessions, audit
}

func partial(id string) Actor {
	return Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, SessionID: kernel.SessionID(id)}
}

func TestAdminLoginIssuesPartialSession(t *testing.T) {
	s, _, _ := newFactorService(admin, &fakeCreds{hash: "h:пароль-настоящий", totp: "SECRET"})
	sess, token, err := s.Login(context.Background(),
		Credentials{Login: "niaz", Password: "пароль-настоящий"}, account.SpaceAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Elevated() || token == "" {
		t.Fatal("после пароля сессия обязана быть частичной")
	}
	if got := sess.ExpiresAt.Sub(sess.IssuedAt); got != PartialSessionTTL {
		t.Fatalf("частичная сессия живёт %v, ждали %v", got, PartialSessionTTL)
	}

	// Обычному пользователю частичная сессия в админке не выдаётся.
	plain := admin
	plain.Role = account.RoleUser
	s, sessions, _ := newFactorService(plain, &fakeCreds{hash: "h:пароль-настоящий"})
	_, _, err = s.Login(context.Background(),
		Credentials{Login: "niaz", Password: "пароль-настоящий"}, account.SpaceAdmin)
	if !errors.Is(err, kernel.ErrForbidden) || len(sessions.created) != 0 {
		t.Fatalf("не-админ получил сессию в админке: %v", err)
	}
}

func TestConfirmFactorElevatesAndRotatesToken(t *testing.T) {
	s, sessions, _ := newFactorService(admin, &fakeCreds{hash: "h:x", totp: "SECRET"})
	ctx := context.Background()

	if _, _, err := s.ConfirmFactor(ctx, partial("old"), "000000", "admin-ui"); !errors.Is(err, kernel.ErrUnauthorized) {
		t.Fatalf("неверный код прошёл: %v", err)
	}
	if len(sessions.created) != 0 {
		t.Fatal("сессия выдана по неверному коду")
	}

	sess, token, err := s.ConfirmFactor(ctx, partial("old"), "123456", "admin-ui")
	if err != nil {
		t.Fatal(err)
	}
	if !sess.Elevated() || token == "" {
		t.Fatal("верный код должен повысить сессию")
	}
	if got := sess.ExpiresAt.Sub(sess.IssuedAt); got != AdminSessionTTL {
		t.Fatalf("повышенная сессия живёт %v, ждали %v", got, AdminSessionTTL)
	}
	// Ротация: новый токен выдан, частичная отозвана.
	if len(sessions.created) != 1 || sessions.created[0].ID == "old" {
		t.Fatal("повышение должно выдавать НОВУЮ сессию")
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != "old" {
		t.Fatalf("частичная сессия не отозвана: %v", sessions.revoked)
	}
}

func TestConfirmFactorLocksSessionAfterFiveFailures(t *testing.T) {
	s, sessions, _ := newFactorService(admin, &fakeCreds{hash: "h:x", totp: "SECRET"})
	ctx := context.Background()
	for i := 1; i < MaxFactorAttempts; i++ {
		if _, _, err := s.ConfirmFactor(ctx, partial("p"), "000000", ""); !errors.Is(err, kernel.ErrUnauthorized) {
			t.Fatalf("попытка %d: %v", i, err)
		}
	}
	if _, _, err := s.ConfirmFactor(ctx, partial("p"), "000000", ""); !errors.Is(err, kernel.ErrExpired) {
		t.Fatalf("пятый промах должен гасить сессию, получили %v", err)
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != "p" {
		t.Fatalf("сессия не отозвана после перебора: %v", sessions.revoked)
	}
}

func TestConfirmFactorAsksToEnrollWhenNoSecret(t *testing.T) {
	s, _, _ := newFactorService(admin, &fakeCreds{hash: "h:x"})
	if _, _, err := s.ConfirmFactor(context.Background(), partial("p"), "123456", ""); !errors.Is(err, ErrEnrollTOTP) {
		t.Fatalf("без секрета ждали ErrEnrollTOTP, получили %v", err)
	}
}

func TestEnrollmentSavesSecretOnlyAfterCodeConfirms(t *testing.T) {
	creds := &fakeCreds{hash: "h:x"}
	s, sessions, audit := newFactorService(admin, creds)
	ctx := context.Background()

	enr, err := s.BeginEnrollment(ctx, partial("p"))
	if err != nil {
		t.Fatal(err)
	}
	if enr.OTPAuthURL == "" {
		t.Fatal("без адреса регистрировать нечего")
	}
	again, _ := s.BeginEnrollment(ctx, partial("p"))
	if again.OTPAuthURL != enr.OTPAuthURL {
		t.Fatal("обновление страницы сменило QR под рукой")
	}
	if creds.totp != "" {
		t.Fatal("секрет записан до подтверждения кодом")
	}

	if _, _, err := s.CompleteEnrollment(ctx, partial("p"), "000000", ""); !errors.Is(err, kernel.ErrUnauthorized) {
		t.Fatalf("неверный код прошёл: %v", err)
	}
	if creds.totp != "" {
		t.Fatal("секрет записан по неверному коду")
	}

	sess, token, err := s.CompleteEnrollment(ctx, partial("p"), "123456", "admin-ui")
	if err != nil {
		t.Fatal(err)
	}
	if creds.totp != "SECRET" {
		t.Fatalf("сохранён не тот секрет: %q", creds.totp)
	}
	if !sess.Elevated() || token == "" {
		t.Fatal("после подтверждения сессия должна быть повышенной")
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != "p" {
		t.Fatal("частичная сессия не отозвана при повышении")
	}
	if len(audit.actions) != 1 || audit.actions[0] != "user.enroll_totp" {
		t.Fatalf("журнал: %v", audit.actions)
	}
	// Секрет одноразовый: второй раз той же сессией не подтвердить.
	if _, _, err := s.CompleteEnrollment(ctx, partial("p"), "123456", ""); !errors.Is(err, kernel.ErrExpired) {
		t.Fatalf("регистрация сработала второй раз: %v", err)
	}
}

func TestEnrollmentRefusedWhenFactorAlreadySet(t *testing.T) {
	// Пароль один не должен подменять уже настроенный фактор.
	withFactor := admin
	withFactor.TOTPEnabled = true
	s, _, _ := newFactorService(withFactor, &fakeCreds{hash: "h:x", totp: "OLD"})
	if _, err := s.BeginEnrollment(context.Background(), partial("p")); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("хотели ErrConflict, получили %v", err)
	}
	// И даже если User устарел, БД говорит своё.
	s, _, _ = newFactorService(admin, &fakeCreds{hash: "h:x", totp: "OLD"})
	if _, err := s.BeginEnrollment(context.Background(), partial("p")); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("хотели ErrConflict по БД, получили %v", err)
	}
}
