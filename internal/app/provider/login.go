package provider

import (
	"context"
	"errors"
	"time"
)

// AuthStatus — состояние входа площадки, у которой есть свой вход (аккаунт,
// токен). Площадка докладывает его сама, когда оно меняется: протух токен и
// не продлился — NeedsReauth; ядро показывает это в админке и плеере.
type AuthStatus struct {
	NeedsReauth bool
	Since       time.Time
	Reason      string
	ExpiresAt   time.Time
	// CanRefresh — площадке есть чем продлевать вход без человека.
	CanRefresh  bool
	RefreshedAt time.Time
}

// DeviceLogin — вход человека в площадку по коду (device flow, RFC 8628):
// приложение показывает код, человек подтверждает его на сайте площадки.
// Необязательная часть площадки — нужна там, где аккаунт заводит сам
// человек (локальный движок, ADR-0015). Как и где хранится выданная сессия —
// дело площадки: ядро только ведёт вход и показывает его состояние.
type DeviceLogin interface {
	// Site — где человек вводит код, для сообщений: «kino.pub».
	Site() string
	// Ready — входить есть чем: у площадки есть ключ клиента и куда положить
	// сессию. Нет — вход не начинается (ErrLoginNotConfigured).
	Ready() bool
	// HasSession — человек уже вошёл.
	HasSession() bool
	// Start начинает вход: новый код.
	Start(ctx context.Context) (DeviceCode, error)
	// Poll — один опрос авторизации. nil — вход выполнен и сессия сохранена.
	// ErrLoginPending и ErrLoginSlowDown — ждать дальше; ErrLoginExpired,
	// ErrLoginDenied, ErrLoginRejected и ErrLoginSave — конец входа; любая
	// другая ошибка — сбой связи, код ещё жив.
	Poll(ctx context.Context, code string) error
	// Logout стирает сессию.
	Logout() error
}

// DeviceCode — начатый вход: что показать человеку и как часто спрашивать.
type DeviceCode struct {
	// Code — секрет опроса, человеку не показывается.
	Code                    string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresAt               time.Time
	Interval                time.Duration
}

var (
	ErrLoginNotConfigured = errors.New("вход не настроен: нет ключа клиента")
	ErrLoginPending       = errors.New("код ещё не подтверждён")
	ErrLoginSlowDown      = errors.New("просят опрашивать реже")
	ErrLoginExpired       = errors.New("код истёк")
	ErrLoginDenied        = errors.New("вход отклонён")
	ErrLoginRejected      = errors.New("клиент не признан — проверьте секрет")
	// ErrLoginSave — сессия выдана, но не сохранилась.
	ErrLoginSave = errors.New("сессия не сохранилась")
)
