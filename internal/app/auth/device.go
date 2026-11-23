package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Вход устройства без браузера — для утилиты-загрузчика.
//
// Схема та же, что у телевизоров и консолей (device authorization): устройство
// получает короткий код, человек открывает ссылку на телефоне, где уже вошёл,
// и подтверждает. Устройство тем временем опрашивает платформу и, дождавшись
// подтверждения, получает сессию с refresh-токеном. Дальше она продлевается
// сама: живой refresh-токен обменивается на новую пару, старая отзывается.
// Так утилита не зависит от куки браузера, которые протухают, а сессия
// остаётся обычной сессией — её видно в админке и её можно отозвать.
//
// Ожидающие коды живут в памяти core, как и незавершённые регистрации
// второго фактора: им жить десять минут, и перезапуск лишь заставит
// повторить `login`.

const (
	// DeviceSessionTTL — срок токена сессии устройства. Как у браузера:
	// если устройство не продлевало сессию месяц, токен протух.
	DeviceSessionTTL = UserSessionTTL
	// DeviceRefreshTTL — срок refresh-токена. Скользящий: каждое продление
	// выдаёт новый на тот же срок, так что устройство, которым пользуются
	// хотя бы раз в полгода, входит один раз.
	DeviceRefreshTTL = 180 * 24 * time.Hour
	// DeviceCodeTTL — сколько ждать подтверждения. Хватает достать телефон.
	DeviceCodeTTL = 10 * time.Minute
	// DevicePollInterval — чаще устройству спрашивать незачем.
	DevicePollInterval = 5 * time.Second
	// MaxPendingDeviceAuths — потолок ожидающих кодов. Запрос кода не требует
	// входа, и без потолка память core росла бы от чужих запросов.
	MaxPendingDeviceAuths = 200
	// maxDeviceName — имя устройства показывается человеку и в админке.
	maxDeviceName = 80
)

var (
	// ErrDeviceAuthPending — человек ещё не подтвердил. Не ошибка, а ответ
	// «спроси позже»; транспорт отдаёт его кодом authorization_pending.
	ErrDeviceAuthPending = errors.New("подтверждение ещё не получено")
	// ErrDeviceSlowDown — устройство спрашивает чаще DevicePollInterval.
	ErrDeviceSlowDown = errors.New("слишком частый опрос")
	// ErrDeviceAuthDenied — человек отклонил запрос.
	ErrDeviceAuthDenied = fmt.Errorf("%w: запрос отклонён", kernel.ErrForbidden)
	// ErrTooManyDeviceAuths — потолок ожидающих кодов достигнут.
	ErrTooManyDeviceAuths = fmt.Errorf("%w: слишком много ожидающих запросов", kernel.ErrConflict)
)

// DeviceStart — что устройство показывает человеку и чем потом опрашивает.
type DeviceStart struct {
	// DeviceCode — секрет устройства, им оно опрашивает. Никогда не
	// показывается.
	DeviceCode string
	// UserCode — короткий код для человека: он в ссылке и его можно ввести
	// руками на странице подтверждения.
	UserCode  string
	ExpiresAt time.Time
	Interval  time.Duration
}

// DeviceTokens — пара, которую получает устройство: токен сессии и
// refresh-токен для её продления.
type DeviceTokens struct {
	Token            string
	RefreshToken     string
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
}

// deviceAuth — ожидающий запрос. Ключ карты — хеш кода устройства, чтобы
// дамп памяти не содержал секретов, по которым выдаётся сессия.
type deviceAuth struct {
	account.DeviceAuth
	lastPoll time.Time
}

// BeginDeviceAuth выдаёт устройству пару кодов. Входа не требует: это и есть
// начало входа.
func (s *Service) BeginDeviceAuth(ctx context.Context, device string) (DeviceStart, error) {
	device = normalizeDevice(device)
	if device == "" {
		return DeviceStart{}, fmt.Errorf("%w: устройство должно назваться", kernel.ErrInvalidInput)
	}
	raw, hash, err := s.tokens.NewToken()
	if err != nil {
		return DeviceStart{}, err
	}
	now := s.clock.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepDevices(now)
	if len(s.devices) >= MaxPendingDeviceAuths {
		return DeviceStart{}, ErrTooManyDeviceAuths
	}
	if s.devices == nil {
		s.devices = make(map[string]*deviceAuth)
		s.deviceByCode = make(map[string]string)
	}
	// Код для человека короткий, и совпадение с ещё живым — вопрос
	// вероятности, а не невозможности; перевыдать дешевле, чем разбирать.
	userCode := s.tokens.NewUserCode()
	for _, taken := s.deviceByCode[userCode]; taken; _, taken = s.deviceByCode[userCode] {
		userCode = s.tokens.NewUserCode()
	}
	s.devices[hash] = &deviceAuth{DeviceAuth: account.DeviceAuth{
		UserCode: userCode, Device: device, CreatedAt: now, ExpiresAt: now.Add(DeviceCodeTTL),
	}}
	s.deviceByCode[userCode] = hash
	return DeviceStart{
		DeviceCode: raw, UserCode: userCode, ExpiresAt: now.Add(DeviceCodeTTL), Interval: DevicePollInterval,
	}, nil
}

// DeviceAuthByUserCode — что показать человеку перед подтверждением: какое
// устройство просит и сколько у него осталось времени.
func (s *Service) DeviceAuthByUserCode(ctx context.Context, a Actor, userCode string) (account.DeviceAuth, error) {
	if err := s.policy.CanApproveDevice(a); err != nil {
		return account.DeviceAuth{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.pendingByUserCode(userCode)
	if err != nil {
		return account.DeviceAuth{}, err
	}
	return d.DeviceAuth, nil
}

// ApproveDeviceAuth — человек разрешил. Сессия выдаётся не здесь, а когда
// устройство придёт за ней: токен, выданный заранее, лежал бы в памяти
// без хозяина.
func (s *Service) ApproveDeviceAuth(ctx context.Context, a Actor, userCode string) error {
	if err := s.policy.CanApproveDevice(a); err != nil {
		return err
	}
	s.mu.Lock()
	d, err := s.pendingByUserCode(userCode)
	if err == nil {
		by := a.UserID
		d.ApprovedBy = &by
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	_ = s.audit.Record(ctx, a.UserID, "session.device.approve", d.Device, "", nil, nil)
	return nil
}

// ApproveDeviceFor — администратор подключает устройство к аккаунту
// пользователя: человек показывает код с экрана телефона, админ вводит его в
// карточке пользователя. Нужен, когда у человека ещё нет устройства, где он
// вошёл, и подтверждать ему не с чего. Подтвердить такой код из своей
// сессии нельзя: устройство получило бы аккаунт подтвердившего, и брат
// сбивал бы прогресс тому, кто его пустил.
//
// Возвращает запрос: имя устройства показывается админу.
func (s *Service) ApproveDeviceFor(ctx context.Context, a Actor, target kernel.UserID, userCode string) (account.DeviceAuth, error) {
	if err := s.policy.CanApproveDeviceFor(a, target, s.clock.Now()); err != nil {
		return account.DeviceAuth{}, err
	}
	u, err := s.users.ByID(ctx, target)
	if err != nil {
		return account.DeviceAuth{}, err
	}
	if u.Disabled {
		// Опрос всё равно отказал бы, но админ должен узнать это сейчас,
		// а не гадать, почему телефон так и не вошёл.
		return account.DeviceAuth{}, fmt.Errorf("%w: пользователь заблокирован", kernel.ErrConflict)
	}
	s.mu.Lock()
	d, err := s.pendingByUserCode(userCode)
	var info account.DeviceAuth
	if err == nil {
		by := target
		d.ApprovedBy = &by
		info = d.DeviceAuth
	}
	s.mu.Unlock()
	if err != nil {
		return account.DeviceAuth{}, err
	}
	_ = s.audit.Record(ctx, a.UserID, "session.device.approve_for", u.Login, "",
		nil, map[string]string{"device": info.Device})
	return info, nil
}

// DenyDeviceAuth — человек отклонил. Устройство узнает об этом при опросе,
// а не просто дождётся истечения: «отказано» и «не успел» — разные ответы.
func (s *Service) DenyDeviceAuth(ctx context.Context, a Actor, userCode string) error {
	if err := s.policy.CanApproveDevice(a); err != nil {
		return err
	}
	s.mu.Lock()
	d, err := s.pendingByUserCode(userCode)
	if err == nil {
		d.Denied = true
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	_ = s.audit.Record(ctx, a.UserID, "session.device.deny", d.Device, "", nil, nil)
	return nil
}

// PollDeviceAuth — устройство спрашивает, подтвердили ли его. Подтверждённый
// код одноразовый: сессия выдаётся и код забывается.
func (s *Service) PollDeviceAuth(ctx context.Context, deviceCode string) (account.Session, DeviceTokens, error) {
	now := s.clock.Now()
	hash := s.tokens.HashToken(deviceCode)

	s.mu.Lock()
	d, ok := s.devices[hash]
	switch {
	case !ok:
		s.mu.Unlock()
		return account.Session{}, DeviceTokens{}, kernel.ErrNotFound
	case now.After(d.ExpiresAt):
		s.forgetDevice(hash)
		s.mu.Unlock()
		return account.Session{}, DeviceTokens{}, kernel.ErrExpired
	case !d.lastPoll.IsZero() && now.Sub(d.lastPoll) < DevicePollInterval:
		s.mu.Unlock()
		return account.Session{}, DeviceTokens{}, ErrDeviceSlowDown
	}
	d.lastPoll = now
	switch {
	case d.Denied:
		s.forgetDevice(hash)
		s.mu.Unlock()
		return account.Session{}, DeviceTokens{}, ErrDeviceAuthDenied
	case d.ApprovedBy == nil:
		s.mu.Unlock()
		return account.Session{}, DeviceTokens{}, ErrDeviceAuthPending
	}
	approved := *d.ApprovedBy
	device := d.Device
	s.forgetDevice(hash)
	s.mu.Unlock()

	u, err := s.users.ByID(ctx, approved)
	if err != nil {
		return account.Session{}, DeviceTokens{}, err
	}
	if u.Disabled {
		return account.Session{}, DeviceTokens{}, kernel.ErrForbidden
	}
	return s.issueDevice(ctx, u, device)
}

// RefreshSession продлевает сессию устройства: живой refresh-токен
// обменивается на новую пару, прежняя сессия отзывается. Ротация — а не
// продление того же токена — значит, что украденный старый токен после
// первого же продления мёртв. Имя устройства остаётся прежним: продление
// не повод переименоваться.
func (s *Service) RefreshSession(ctx context.Context, refreshToken string) (account.Session, DeviceTokens, error) {
	if refreshToken == "" {
		return account.Session{}, DeviceTokens{}, kernel.ErrUnauthorized
	}
	old, err := s.sessions.ByRefreshHash(ctx, s.tokens.HashToken(refreshToken))
	if err != nil {
		return account.Session{}, DeviceTokens{}, kernel.ErrUnauthorized
	}
	now := s.clock.Now()
	if err := old.CanRefresh(now); err != nil {
		return account.Session{}, DeviceTokens{}, err
	}
	u, err := s.users.ByID(ctx, old.UserID)
	if err != nil {
		return account.Session{}, DeviceTokens{}, err
	}
	if u.Disabled {
		return account.Session{}, DeviceTokens{}, kernel.ErrForbidden
	}
	sess, tokens, err := s.issueDevice(ctx, u, old.DeviceKind)
	if err != nil {
		return account.Session{}, DeviceTokens{}, err
	}
	if err := s.sessions.Revoke(ctx, old.ID, now); err != nil {
		return account.Session{}, DeviceTokens{}, err
	}
	return sess, tokens, nil
}

// issueDevice выдаёт сессию устройства: обычную пользовательскую, но с
// refresh-токеном.
func (s *Service) issueDevice(ctx context.Context, u account.User, device string) (account.Session, DeviceTokens, error) {
	raw, hash, err := s.tokens.NewToken()
	if err != nil {
		return account.Session{}, DeviceTokens{}, err
	}
	rawRefresh, refreshHash, err := s.tokens.NewToken()
	if err != nil {
		return account.Session{}, DeviceTokens{}, err
	}
	now := s.clock.Now()
	refreshExpires := now.Add(DeviceRefreshTTL)
	sess := account.Session{
		ID:               kernel.SessionID(hash[:16]),
		UserID:           u.ID,
		Space:            account.SpaceUser,
		DeviceKind:       device,
		IssuedAt:         now,
		ExpiresAt:        now.Add(DeviceSessionTTL),
		RefreshExpiresAt: &refreshExpires,
	}
	if err := s.sessions.CreateRenewable(ctx, sess, hash, refreshHash); err != nil {
		return account.Session{}, DeviceTokens{}, err
	}
	_ = s.users.TouchLogin(ctx, u.ID, now)
	return sess, DeviceTokens{
		Token: raw, RefreshToken: rawRefresh,
		ExpiresAt: sess.ExpiresAt, RefreshExpiresAt: refreshExpires,
	}, nil
}

// pendingByUserCode находит живой запрос по коду человека. Вызывать под
// замком.
func (s *Service) pendingByUserCode(userCode string) (*deviceAuth, error) {
	hash, ok := s.deviceByCode[NormalizeUserCode(userCode)]
	if !ok {
		return nil, kernel.ErrNotFound
	}
	d := s.devices[hash]
	if s.clock.Now().After(d.ExpiresAt) {
		s.forgetDevice(hash)
		return nil, kernel.ErrExpired
	}
	if d.ApprovedBy != nil || d.Denied {
		// Ответ уже дан; второй ничего не меняет.
		return nil, kernel.ErrConflict
	}
	return d, nil
}

// forgetDevice удаляет запрос. Вызывать под замком.
func (s *Service) forgetDevice(hash string) {
	if d, ok := s.devices[hash]; ok {
		delete(s.deviceByCode, d.UserCode)
		delete(s.devices, hash)
	}
}

// sweepDevices выбрасывает истёкшие запросы. Вызывать под замком.
func (s *Service) sweepDevices(now time.Time) {
	for hash, d := range s.devices {
		if now.After(d.ExpiresAt) {
			s.forgetDevice(hash)
		}
	}
}

// NormalizeUserCode приводит код, введённый человеком, к виду, в котором он
// выдан: регистр, пробелы и дефисы — не часть кода.
func NormalizeUserCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) == 8 {
		return out[:4] + "-" + out[4:]
	}
	return out
}

// normalizeDevice оставляет от имени устройства то, что можно показать:
// одну строку без управляющих символов, разумной длины.
func normalizeDevice(device string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(device) {
		switch {
		case unicode.IsControl(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(out); len(r) > maxDeviceName {
		out = string(r[:maxDeviceName])
	}
	return out
}
