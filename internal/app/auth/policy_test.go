package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

func TestPolicy_ProgressIsPrivateEvenFromAdmin(t *testing.T) {
	var p Policy
	now := time.Now()
	admin := Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, FactorAt: &now}

	// Без выданного диагностического доступа админ не читает чужой прогресс.
	if err := p.CanReadProgress(admin, 2, DiagnosticGrant{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("админ прочитал чужой прогресс без гранта: %v", err)
	}
	// С адресным грантом — читает, и это попадёт в аудит.
	g := NewDiagnosticGrant(2, 1, "жалоба на буферизацию", time.Hour)
	if err := p.CanReadProgress(admin, 2, g); err != nil {
		t.Fatalf("грант не сработал: %v", err)
	}
	// Грант адресный: на третьего пользователя не распространяется.
	if err := p.CanReadProgress(admin, 3, g); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("грант протёк на другого пользователя: %v", err)
	}
}

func TestPolicy_SpacesAreIsolated(t *testing.T) {
	var p Policy
	now := time.Now()
	cases := []struct {
		name  string
		actor Actor
		check func(Actor) error
		want  bool
	}{
		{"пользователь не админит",
			Actor{UserID: 1, Role: account.RoleUser, Space: account.SpaceUser}, p.CanAdminister, false},
		{"частичная сессия (пароль без кода) не админит",
			Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin}, p.CanAdminister, false},
		{"повышенная сессия админит",
			Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, FactorAt: &now}, p.CanAdminister, true},
		{"админская сессия не смотрит от своего имени",
			Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, FactorAt: &now},
			func(a Actor) error { return p.CanIssueTicket(a, 1) }, false},
		{"пользователь не переносит историю даже себе",
			Actor{UserID: 1, Role: account.RoleUser, Space: account.SpaceUser},
			func(a Actor) error { return p.CanImportFor(a, 1) }, false},
		{"админ переносит историю пользователю",
			Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, FactorAt: &now},
			func(a Actor) error { return p.CanImportFor(a, 2) }, true},
		{"пользователь не подключает устройство к чужому аккаунту",
			Actor{UserID: 1, Role: account.RoleUser, Space: account.SpaceUser},
			func(a Actor) error { return p.CanApproveDeviceFor(a, 2, now) }, false},
		{"админ со свежим кодом подключает устройство к чужому аккаунту",
			Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, FactorAt: &now},
			func(a Actor) error { return p.CanApproveDeviceFor(a, 2, now) }, true},
		{"служебные креды не дают админку",
			Actor{Space: account.SpaceService}, p.CanAdminister, false},
		{"служебные креды работают в своём пространстве",
			Actor{Space: account.SpaceService}, p.CanServe, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.check(c.actor) == nil; got != c.want {
				t.Fatalf("allowed = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPolicy_SensitiveActionsNeedFreshFactor(t *testing.T) {
	var p Policy
	now := time.Now()
	fresh := now.Add(-time.Minute)
	stale := now.Add(-SudoWindow - time.Minute)
	if err := p.CanActSensitive(Actor{Role: account.RoleAdmin, Space: account.SpaceAdmin, FactorAt: &fresh}, now); err != nil {
		t.Fatalf("свежий код отвергнут: %v", err)
	}
	err := p.CanActSensitive(Actor{Role: account.RoleAdmin, Space: account.SpaceAdmin, FactorAt: &stale}, now)
	if !errors.Is(err, ErrStepUp) || !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("старый код должен просить повтор и читаться как отказ, получили %v", err)
	}
	if err := p.CanActSensitive(Actor{Role: account.RoleAdmin, Space: account.SpaceAdmin}, now); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("частичная сессия допущена к опасному действию: %v", err)
	}
}
