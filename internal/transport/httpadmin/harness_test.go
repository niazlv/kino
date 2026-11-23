package httpadmin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	webauthnadapter "github.com/niazleushkin/kino/internal/adapter/webauthn"
	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
)

// TestManualHarness поднимает админку на подделках хранилищ как обычный
// сервер — для проверки браузерных церемоний passkey руками или скриптом
// (виртуальный authenticator в Chrome через DevTools). Ни база, ни пароли
// не нужны: пользователь niaz с паролем «пароль-настоящий», код 123456.
//
//	KINO_ADMIN_HARNESS_ADDR=127.0.0.1:8088 go test ./internal/transport/httpadmin -run TestManualHarness -timeout 30m
func TestManualHarness(t *testing.T) {
	addr := os.Getenv("KINO_ADMIN_HARNESS_ADDR")
	if addr == "" {
		t.Skip("KINO_ADMIN_HARNESS_ADDR не задан")
	}
	users := &memUsers{u: account.User{ID: 1, Login: "niaz", DisplayName: "Нияз", Role: account.RoleAdmin}}
	creds := &memCreds{users: users, hash: "h:пароль-настоящий"}
	svc := auth.NewService(users, creds, memInvites{}, &memSessions{byHash: map[string]*account.Session{}},
		&memPasskeys{}, plainHasher{}, &stubTokens{}, stubTOTP{}, nopAudit{}, sysClock{})
	engine, err := webauthnadapter.New("localhost", "kino", []string{"http://localhost:8088", "http://" + addr})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(svc, nil, nil, nil, nil, engine, "", slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Addr: addr, Handler: s.Routes(), ReadHeaderTimeout: 5 * time.Second}
	t.Logf("админка на подделках: http://%s/admin/login", addr)
	go func() { _ = srv.ListenAndServe() }()
	<-time.After(25 * time.Minute)
	_ = srv.Shutdown(context.Background())
}
