package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type fakePasskeys struct {
	keys    []account.Passkey
	touched []int64
}

func (f *fakePasskeys) Create(_ context.Context, p account.Passkey) (account.Passkey, error) {
	p.ID = int64(len(f.keys) + 1)
	f.keys = append(f.keys, p)
	return p, nil
}
func (f *fakePasskeys) ByCredentialID(_ context.Context, id []byte) (account.Passkey, error) {
	for _, k := range f.keys {
		if string(k.CredentialID) == string(id) {
			return k, nil
		}
	}
	return account.Passkey{}, kernel.ErrNotFound
}
func (f *fakePasskeys) ListForUser(_ context.Context, u kernel.UserID) ([]account.Passkey, error) {
	var out []account.Passkey
	for _, k := range f.keys {
		if k.UserID == u {
			out = append(out, k)
		}
	}
	return out, nil
}
func (f *fakePasskeys) Touch(_ context.Context, id int64, _ uint32, _ time.Time) error {
	f.touched = append(f.touched, id)
	return nil
}
func (f *fakePasskeys) Delete(_ context.Context, id int64, u kernel.UserID) error {
	for i, k := range f.keys {
		if k.ID == id && k.UserID == u {
			f.keys = append(f.keys[:i], f.keys[i+1:]...)
			return nil
		}
	}
	return kernel.ErrNotFound
}

func newPasskeyService(u account.User, creds *fakeCreds, keys *fakePasskeys) (*Service, *fakeSessions, *fakeAudit) {
	sessions := &fakeSessions{}
	audit := &fakeAudit{}
	return &Service{
		users: &fakeUsers{u: u}, creds: creds, sessions: sessions, passkeys: keys,
		hasher: fakeHasher{}, tokens: &fakeTokens{}, totp: fakeTOTP{secret: "SECRET", code: "123456"},
		audit: audit, clock: fixedClock{},
	}, sessions, audit
}

func elevated(id string, ago time.Duration) Actor {
	at := fixedClock{}.Now().Add(-ago)
	return Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, SessionID: kernel.SessionID(id), FactorAt: &at}
}

func TestPasskeyRegistrationRules(t *testing.T) {
	ctx := context.Background()

	// Частичная сессия без единого фактора — можно: это первая настройка.
	s, _, _ := newPasskeyService(admin, &fakeCreds{hash: "h:x"}, &fakePasskeys{})
	if _, _, err := s.CanRegisterPasskey(ctx, partial("p")); err != nil {
		t.Fatalf("первая настройка отвергнута: %v", err)
	}

	// Частичная при уже настроенном TOTP — нельзя: пароль не добавляет фактор.
	withTOTP := admin
	withTOTP.TOTPEnabled = true
	s, _, _ = newPasskeyService(withTOTP, &fakeCreds{hash: "h:x", totp: "SECRET"}, &fakePasskeys{})
	if _, _, err := s.CanRegisterPasskey(ctx, partial("p")); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("хотели ErrConflict, получили %v", err)
	}

	// Повышенная со свежим кодом — можно, со старым — просит код заново.
	s, _, _ = newPasskeyService(withTOTP, &fakeCreds{hash: "h:x", totp: "SECRET"}, &fakePasskeys{})
	if _, _, err := s.CanRegisterPasskey(ctx, elevated("e", time.Minute)); err != nil {
		t.Fatalf("свежая повышенная отвергнута: %v", err)
	}
	if _, _, err := s.CanRegisterPasskey(ctx, elevated("e", SudoWindow+time.Minute)); !errors.Is(err, ErrStepUp) {
		t.Fatalf("старый код должен просить повтор, получили %v", err)
	}
}

func TestAddPasskeyFromPartialSessionElevates(t *testing.T) {
	keys := &fakePasskeys{}
	s, sessions, audit := newPasskeyService(admin, &fakeCreds{hash: "h:x"}, keys)
	sess, token, err := s.AddPasskey(context.Background(), partial("p"),
		account.Passkey{CredentialID: []byte("cred"), PublicKey: []byte("pk"), Name: "iPhone"}, "admin-ui")
	if err != nil {
		t.Fatal(err)
	}
	if !sess.Elevated() || token == "" {
		t.Fatal("регистрация ключа в частичной сессии должна повысить её")
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != "p" {
		t.Fatal("частичная сессия не отозвана при повышении")
	}
	if len(keys.keys) != 1 || keys.keys[0].UserID != 1 {
		t.Fatalf("ключ не сохранён за пользователем: %+v", keys.keys)
	}
	if len(audit.actions) != 1 || audit.actions[0] != "user.add_passkey" {
		t.Fatalf("журнал: %v", audit.actions)
	}

	// После этого TOTP паролем уже не настроить: фактор есть.
	if _, err := s.BeginEnrollment(context.Background(), partial("p2")); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("с ключом настройка TOTP должна отвергаться: %v", err)
	}
}

func TestLoginWithPasskeyIssuesElevatedSession(t *testing.T) {
	keys := &fakePasskeys{keys: []account.Passkey{{ID: 7, UserID: 1, CredentialID: []byte("cred")}}}
	s, _, _ := newPasskeyService(admin, &fakeCreds{hash: "h:x"}, keys)
	sess, token, err := s.LoginWithPasskey(context.Background(), []byte("cred"), 5, "admin-ui")
	if err != nil {
		t.Fatal(err)
	}
	if !sess.Elevated() || token == "" || sess.Space != account.SpaceAdmin {
		t.Fatal("вход по ключу должен выдавать повышенную админскую сессию")
	}
	if len(keys.touched) != 1 || keys.touched[0] != 7 {
		t.Fatal("счётчик подписей не обновлён")
	}
	if _, _, err := s.LoginWithPasskey(context.Background(), []byte("nope"), 1, ""); !errors.Is(err, kernel.ErrUnauthorized) {
		t.Fatalf("чужой ключ должен давать ErrUnauthorized: %v", err)
	}

	// Заблокированному и не-админу ключ не помогает.
	blocked := admin
	blocked.Disabled = true
	s, _, _ = newPasskeyService(blocked, &fakeCreds{hash: "h:x"}, keys)
	if _, _, err := s.LoginWithPasskey(context.Background(), []byte("cred"), 6, ""); !errors.Is(err, kernel.ErrUnauthorized) {
		t.Fatalf("заблокированный вошёл по ключу: %v", err)
	}
}

func TestElevateWithPasskeyRejectsForeignKey(t *testing.T) {
	keys := &fakePasskeys{keys: []account.Passkey{
		{ID: 1, UserID: 1, CredentialID: []byte("mine")},
		{ID: 2, UserID: 2, CredentialID: []byte("theirs")},
	}}
	s, sessions, _ := newPasskeyService(admin, &fakeCreds{hash: "h:x"}, keys)
	if _, _, err := s.ElevateWithPasskey(context.Background(), partial("p"), []byte("theirs"), 1, ""); !errors.Is(err, kernel.ErrUnauthorized) {
		t.Fatalf("чужой ключ повысил сессию: %v", err)
	}
	sess, _, err := s.ElevateWithPasskey(context.Background(), partial("p"), []byte("mine"), 1, "")
	if err != nil || !sess.Elevated() {
		t.Fatalf("свой ключ не повысил: %v", err)
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != "p" {
		t.Fatal("частичная сессия не отозвана")
	}
}

func TestRemovePasskeyKeepsLastFactor(t *testing.T) {
	keys := &fakePasskeys{keys: []account.Passkey{{ID: 1, UserID: 1, CredentialID: []byte("only"), Name: "единственный"}}}
	s, _, audit := newPasskeyService(admin, &fakeCreds{hash: "h:x"}, keys)
	if err := s.RemovePasskey(context.Background(), elevated("e", time.Minute), 1, 1, ""); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("последний фактор удалён: %v", err)
	}
	keys.keys = append(keys.keys, account.Passkey{ID: 2, UserID: 1, CredentialID: []byte("second"), Name: "второй"})
	if err := s.RemovePasskey(context.Background(), elevated("e", time.Minute), 1, 1, "потерян"); err != nil {
		t.Fatal(err)
	}
	if len(keys.keys) != 1 || keys.keys[0].ID != 2 {
		t.Fatalf("удалён не тот ключ: %+v", keys.keys)
	}
	if audit.actions[len(audit.actions)-1] != "user.remove_passkey" {
		t.Fatalf("журнал: %v", audit.actions)
	}
	if err := s.RemovePasskey(context.Background(), elevated("e", SudoWindow+time.Minute), 1, 2, ""); !errors.Is(err, ErrStepUp) {
		t.Fatalf("удаление без свежего кода прошло: %v", err)
	}
}
