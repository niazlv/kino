package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// movingClock — часы, которые тест двигает сам: сроки кодов и токенов
// проверяются без ожидания.
type movingClock struct{ t time.Time }

func (c *movingClock) Now() time.Time { return c.t }

var member = account.User{ID: 7, Login: "mama", Role: account.RoleUser}

func newDeviceService(u account.User) (*Service, *fakeSessions, *fakeAudit, *movingClock) {
	sessions := &fakeSessions{}
	audit := &fakeAudit{}
	clock := &movingClock{t: time.Unix(1700000000, 0)}
	return &Service{
		users: &fakeUsers{u: u}, sessions: sessions, tokens: &fakeTokens{},
		audit: audit, clock: clock,
	}, sessions, audit, clock
}

func userActor(id kernel.UserID) Actor {
	return Actor{UserID: id, Role: account.RoleUser, Space: account.SpaceUser, SessionID: "browser"}
}

func TestDeviceAuthApprovedYieldsRenewableSession(t *testing.T) {
	s, sessions, audit, clock := newDeviceService(member)
	ctx := context.Background()

	start, err := s.BeginDeviceAuth(ctx, "  kinopub 0.8.0\n on MacBook ")
	if err != nil {
		t.Fatal(err)
	}
	if start.DeviceCode == "" || start.UserCode == "" || start.Interval != DevicePollInterval {
		t.Fatalf("неполный старт: %+v", start)
	}

	// До подтверждения — «ещё нет», а не ошибка.
	if _, _, err := s.PollDeviceAuth(ctx, start.DeviceCode); !errors.Is(err, ErrDeviceAuthPending) {
		t.Fatalf("до подтверждения ждали pending, получили %v", err)
	}
	// Слишком частый опрос осаживается.
	clock.t = clock.t.Add(time.Second)
	if _, _, err := s.PollDeviceAuth(ctx, start.DeviceCode); !errors.Is(err, ErrDeviceSlowDown) {
		t.Fatalf("частый опрос должен получать slow_down, получили %v", err)
	}

	// Человек видит, кто просит, и разрешает. Код можно ввести как угодно.
	info, err := s.DeviceAuthByUserCode(ctx, userActor(7), " "+start.UserCode+" ")
	if err != nil || info.Device != "kinopub 0.8.0 on MacBook" {
		t.Fatalf("описание запроса: %+v, %v", info, err)
	}
	loose := NormalizeUserCode(start.UserCode)
	if err := s.ApproveDeviceAuth(ctx, userActor(7), loose); err != nil {
		t.Fatal(err)
	}
	if len(audit.actions) != 1 || audit.actions[0] != "session.device.approve" {
		t.Fatalf("подтверждение не в аудите: %v", audit.actions)
	}
	// Повторный ответ на тот же код ничего не меняет.
	if err := s.ApproveDeviceAuth(ctx, userActor(7), start.UserCode); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("второе подтверждение: %v", err)
	}

	clock.t = clock.t.Add(DevicePollInterval)
	sess, tokens, err := s.PollDeviceAuth(ctx, start.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if sess.UserID != 7 || sess.Space != account.SpaceUser || sess.DeviceKind != "kinopub 0.8.0 on MacBook" {
		t.Fatalf("сессия не та: %+v", sess)
	}
	if !sess.Renewable() || tokens.Token == "" || tokens.RefreshToken == "" || tokens.Token == tokens.RefreshToken {
		t.Fatalf("сессия устройства обязана быть с refresh-токеном: %+v / %+v", sess, tokens)
	}
	if got := sess.RefreshExpiresAt.Sub(sess.IssuedAt); got != DeviceRefreshTTL {
		t.Fatalf("refresh живёт %v, ждали %v", got, DeviceRefreshTTL)
	}
	if len(sessions.created) != 1 || sessions.refresh[tokens.RefreshToken] == "" {
		t.Fatalf("сессия не записана с refresh-хешем: %+v", sessions)
	}
	// Код одноразовый.
	if _, _, err := s.PollDeviceAuth(ctx, start.DeviceCode); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("использованный код должен быть забыт, получили %v", err)
	}
}

func TestDeviceAuthDeniedAndExpired(t *testing.T) {
	s, sessions, _, clock := newDeviceService(member)
	ctx := context.Background()

	start, _ := s.BeginDeviceAuth(ctx, "tv")
	if err := s.DenyDeviceAuth(ctx, userActor(7), start.UserCode); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PollDeviceAuth(ctx, start.DeviceCode); !errors.Is(err, ErrDeviceAuthDenied) {
		t.Fatalf("отказ должен дойти до устройства, получили %v", err)
	}

	start, _ = s.BeginDeviceAuth(ctx, "tv")
	clock.t = clock.t.Add(DeviceCodeTTL + time.Second)
	if err := s.ApproveDeviceAuth(ctx, userActor(7), start.UserCode); !errors.Is(err, kernel.ErrExpired) {
		t.Fatalf("истёкший код нельзя подтвердить: %v", err)
	}
	if _, _, err := s.PollDeviceAuth(ctx, start.DeviceCode); !errors.Is(err, kernel.ErrExpired) && !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("истёкший код при опросе: %v", err)
	}
	if len(sessions.created) != 0 {
		t.Fatal("сессия выдана без подтверждения")
	}
}

func TestDeviceAuthApprovalNeedsAUserSession(t *testing.T) {
	s, _, _, _ := newDeviceService(member)
	ctx := context.Background()
	start, _ := s.BeginDeviceAuth(ctx, "tv")

	adminUI := Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, SessionID: "a"}
	if err := s.ApproveDeviceAuth(ctx, adminUI, start.UserCode); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("админская сессия не должна подтверждать устройства: %v", err)
	}
	if _, err := s.BeginDeviceAuth(ctx, "   "); !errors.Is(err, kernel.ErrInvalidInput) {
		t.Fatalf("безымянное устройство: %v", err)
	}
	if _, err := s.DeviceAuthByUserCode(ctx, userActor(7), "NOPE-NOPE"); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("неизвестный код: %v", err)
	}
}

// Админ вводит код с чужого телефона в карточке пользователя: сессию
// получает этот пользователь, а не админ.
func TestDeviceApprovedByAdminGoesToTargetUser(t *testing.T) {
	s, sessions, audit, clock := newDeviceService(member)
	ctx := context.Background()
	start, _ := s.BeginDeviceAuth(ctx, "Android · Pixel 8")

	fresh := clock.t
	admin := Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, SessionID: "a", FactorAt: &fresh}
	info, err := s.ApproveDeviceFor(ctx, admin, member.ID, " "+start.UserCode+" ")
	if err != nil {
		t.Fatal(err)
	}
	if info.Device != "Android · Pixel 8" {
		t.Fatalf("админ должен видеть, что подключил: %+v", info)
	}
	if len(audit.actions) != 1 || audit.actions[0] != "session.device.approve_for" {
		t.Fatalf("подключение не в аудите: %v", audit.actions)
	}

	clock.t = clock.t.Add(DevicePollInterval)
	sess, _, err := s.PollDeviceAuth(ctx, start.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if sess.UserID != member.ID || sess.Space != account.SpaceUser || len(sessions.created) != 1 {
		t.Fatalf("сессия должна достаться пользователю из карточки: %+v", sess)
	}
}

func TestDeviceApprovalForOthersNeedsFreshFactor(t *testing.T) {
	s, _, _, clock := newDeviceService(member)
	ctx := context.Background()
	start, _ := s.BeginDeviceAuth(ctx, "tv")

	stale := clock.t.Add(-SudoWindow - time.Minute)
	admin := Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, FactorAt: &stale}
	if _, err := s.ApproveDeviceFor(ctx, admin, member.ID, start.UserCode); !errors.Is(err, ErrStepUp) {
		t.Fatalf("старый код должен просить повтор: %v", err)
	}
	if _, err := s.ApproveDeviceFor(ctx, userActor(7), 8, start.UserCode); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("пользователь подключил устройство к чужому аккаунту: %v", err)
	}

	blocked := member
	blocked.Disabled = true
	s2, _, _, clock2 := newDeviceService(blocked)
	start2, _ := s2.BeginDeviceAuth(ctx, "tv")
	now := clock2.t
	admin2 := Actor{UserID: 1, Role: account.RoleAdmin, Space: account.SpaceAdmin, FactorAt: &now}
	if _, err := s2.ApproveDeviceFor(ctx, admin2, blocked.ID, start2.UserCode); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("заблокированному устройство не подключают: %v", err)
	}
}

func TestRefreshRotatesAndRevokes(t *testing.T) {
	s, sessions, _, clock := newDeviceService(member)
	ctx := context.Background()

	_, tokens, err := s.issueDevice(ctx, member, "kinopub")
	if err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(20 * 24 * time.Hour)

	sess, fresh, err := s.RefreshSession(ctx, tokens.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Token == tokens.Token || fresh.RefreshToken == tokens.RefreshToken {
		t.Fatal("продление обязано выдавать новую пару")
	}
	if sess.DeviceKind != "kinopub" || !sess.Renewable() {
		t.Fatalf("продлённая сессия: %+v", sess)
	}
	if got := sess.RefreshExpiresAt.Sub(clock.t); got != DeviceRefreshTTL {
		t.Fatalf("refresh скользящий: ждали %v, получили %v", DeviceRefreshTTL, got)
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != sessions.created[0].ID {
		t.Fatalf("старая сессия не отозвана: %v", sessions.revoked)
	}
	// Старый refresh-токен мёртв.
	if _, _, err := s.RefreshSession(ctx, tokens.RefreshToken); !errors.Is(err, kernel.ErrUnauthorized) {
		t.Fatalf("повтор старого refresh: %v", err)
	}
}

func TestRefreshRefusesWhatItShould(t *testing.T) {
	s, sessions, _, clock := newDeviceService(member)
	ctx := context.Background()

	// Браузерная сессия не продлевается: у неё нет refresh-токена.
	if _, _, err := s.RefreshSession(ctx, ""); !errors.Is(err, kernel.ErrUnauthorized) {
		t.Fatalf("пустой токен: %v", err)
	}
	if _, _, err := s.RefreshSession(ctx, "unknown"); !errors.Is(err, kernel.ErrUnauthorized) {
		t.Fatalf("неизвестный токен: %v", err)
	}

	// Истёкший refresh — конец: только новый вход.
	_, tokens, _ := s.issueDevice(ctx, member, "kinopub")
	clock.t = clock.t.Add(DeviceRefreshTTL + time.Second)
	if _, _, err := s.RefreshSession(ctx, tokens.RefreshToken); !errors.Is(err, kernel.ErrExpired) {
		t.Fatalf("истёкший refresh: %v", err)
	}

	// Заблокированный пользователь не продлевает ничего.
	blocked := member
	blocked.Disabled = true
	s2, _, _, _ := newDeviceService(blocked)
	s2.sessions = sessions
	_, tokens, _ = s.issueDevice(ctx, member, "kinopub")
	if _, _, err := s2.RefreshSession(ctx, tokens.RefreshToken); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("заблокированный: %v", err)
	}
}

// Сессия устройства протухшая, но refresh живой — ровно тот случай, ради
// которого refresh существует.
func TestRefreshWorksAfterTheSessionItselfExpired(t *testing.T) {
	s, _, _, clock := newDeviceService(member)
	ctx := context.Background()
	_, tokens, _ := s.issueDevice(ctx, member, "kinopub")
	clock.t = clock.t.Add(DeviceSessionTTL + 24*time.Hour)
	if _, _, err := s.RefreshSession(ctx, tokens.RefreshToken); err != nil {
		t.Fatalf("продление после истечения сессии: %v", err)
	}
}

func TestNormalizeUserCode(t *testing.T) {
	for in, want := range map[string]string{
		"kj7q-3mtr": "KJ7Q-3MTR", " KJ7Q 3MTR ": "KJ7Q-3MTR", "KJ7Q3MTR": "KJ7Q-3MTR", "abc": "ABC",
	} {
		if got := NormalizeUserCode(in); got != want {
			t.Errorf("NormalizeUserCode(%q) = %q, want %q", in, got, want)
		}
	}
}
