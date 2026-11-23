package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// listedSessions — хранилище, которое знает сессии пользователя: у
// fakeSessions список всегда пуст, а здесь он и есть предмет проверки.
type listedSessions struct {
	fakeSessions
	all []account.Session
}

func (f *listedSessions) ListForUser(_ context.Context, u kernel.UserID) ([]account.Session, error) {
	var out []account.Session
	for _, s := range f.all {
		if s.UserID == u && s.RevokedAt == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func ownSessionsFixture() (*Service, *listedSessions, *fakeAudit) {
	now := fixedClock{}.Now()
	past := now.Add(-time.Hour)
	later := now.Add(time.Hour)
	refreshLater := now.Add(30 * 24 * time.Hour)
	refreshPast := now.Add(-time.Minute)
	revoked := now.Add(-time.Minute)
	repo := &listedSessions{all: []account.Session{
		{ID: "browser", UserID: 7, Space: account.SpaceUser, DeviceKind: "desktop", ExpiresAt: later},
		{ID: "tv", UserID: 7, Space: account.SpaceUser, DeviceKind: "tv", ExpiresAt: later, RefreshExpiresAt: &refreshLater},
		// Телевизор давно не включали: сессия истекла, но продлиться ещё может.
		{ID: "tv-sleeping", UserID: 7, Space: account.SpaceUser, DeviceKind: "tv", ExpiresAt: past, RefreshExpiresAt: &refreshLater},
		{ID: "tv-dead", UserID: 7, Space: account.SpaceUser, DeviceKind: "tv", ExpiresAt: past, RefreshExpiresAt: &refreshPast},
		{ID: "expired", UserID: 7, Space: account.SpaceUser, DeviceKind: "ios", ExpiresAt: past},
		{ID: "revoked", UserID: 7, Space: account.SpaceUser, DeviceKind: "ios", ExpiresAt: later, RevokedAt: &revoked},
		{ID: "admin", UserID: 7, Space: account.SpaceAdmin, DeviceKind: "admin-ui", ExpiresAt: later},
		{ID: "brother", UserID: 8, Space: account.SpaceUser, DeviceKind: "android", ExpiresAt: later},
	}}
	audit := &fakeAudit{}
	return &Service{sessions: repo, clock: fixedClock{}, audit: audit}, repo, audit
}

var me7 = Actor{UserID: 7, Role: account.RoleUser, Space: account.SpaceUser, SessionID: "browser"}

func ids(ss []account.Session) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = string(s.ID)
	}
	return out
}

func TestMySessions_OnlyLiveStorefrontSessionsOfMine(t *testing.T) {
	svc, _, _ := ownSessionsFixture()
	got, err := svc.MySessions(context.Background(), me7)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"browser", "tv", "tv-sleeping"}
	if g := ids(got); len(g) != len(want) || g[0] != want[0] || g[1] != want[1] || g[2] != want[2] {
		t.Fatalf("хотели %v, получили %v", want, g)
	}
}

func TestMySessions_AdminSpaceCannotList(t *testing.T) {
	// Админская сессия — не витрина: свои сессии там показывает админка.
	svc, _, _ := ownSessionsFixture()
	a := me7
	a.Space = account.SpaceAdmin
	if _, err := svc.MySessions(context.Background(), a); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("хотели ErrForbidden, получили %v", err)
	}
}

func TestSignOutSession_OwnDevice(t *testing.T) {
	svc, repo, audit := ownSessionsFixture()
	if err := svc.SignOutSession(context.Background(), me7, "tv-sleeping"); err != nil {
		t.Fatal(err)
	}
	if len(repo.revoked) != 1 || repo.revoked[0] != "tv-sleeping" {
		t.Fatalf("погашено %v", repo.revoked)
	}
	if len(audit.actions) != 1 || audit.actions[0] != "session.sign_out" {
		t.Fatalf("журнал %v", audit.actions)
	}
}

func TestSignOutSession_ForeignAdminAndDeadAreNotFound(t *testing.T) {
	// Чужая сессия брата, своя админская и уже погасшая — одинаково «нет
	// такой»: витрина не гасит ничего, кроме своих живых.
	for _, id := range []kernel.SessionID{"brother", "admin", "revoked", "tv-dead", "nope"} {
		svc, repo, _ := ownSessionsFixture()
		err := svc.SignOutSession(context.Background(), me7, id)
		if !errors.Is(err, kernel.ErrNotFound) {
			t.Fatalf("%s: хотели ErrNotFound, получили %v", id, err)
		}
		if len(repo.revoked) != 0 {
			t.Fatalf("%s: погашено %v", id, repo.revoked)
		}
	}
}

func TestSignOutOthers_KeepsCurrent(t *testing.T) {
	svc, repo, audit := ownSessionsFixture()
	n, err := svc.SignOutOthers(context.Background(), me7)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(repo.revoked) != 2 {
		t.Fatalf("погашено %d: %v", n, repo.revoked)
	}
	for _, id := range repo.revoked {
		if id == "browser" {
			t.Fatal("погашена текущая сессия")
		}
		if id == "admin" || id == "brother" {
			t.Fatalf("погашена не своя сессия витрины: %s", id)
		}
	}
	if len(audit.actions) != 1 || audit.actions[0] != "session.sign_out_others" {
		t.Fatalf("журнал %v", audit.actions)
	}
}

func TestSignOutOthers_NothingToDoIsQuiet(t *testing.T) {
	svc, repo, audit := ownSessionsFixture()
	repo.all = repo.all[:1] // только текущая
	n, err := svc.SignOutOthers(context.Background(), me7)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(audit.actions) != 0 {
		t.Fatalf("пустой выход попал в журнал: %v", audit.actions)
	}
}
