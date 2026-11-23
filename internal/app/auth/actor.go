// Package auth — аутентификация и авторизация.
//
// Линия между слоями проведена так:
//
//	transport  — АУТЕНТИФИКАЦИЯ: достать сессию из cookie/заголовка, собрать
//	             Actor, отсечь чужое пространство на уровне маршрута.
//	app/auth   — АВТОРИЗАЦИЯ РЕСУРСА: может ли ЭТОТ actor трогать ЭТУ строку.
//
// Частая ошибка — сложить всё в middleware. Тогда «может ли Петя видеть
// прогресс Маши» становится непроверяемым в юнит-тесте и незаметно
// расходится между хендлерами.
package auth

import (
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Actor — кто действует. Собирается транспортом, дальше течёт по слою app
// явным аргументом, а не через context-магию: невидимую зависимость
// невозможно проверить компилятором.
type Actor struct {
	UserID kernel.UserID
	Role   account.Role
	Space  account.Space
	// SessionID — чтобы повышать и отзывать именно эту сессию.
	SessionID kernel.SessionID
	// FactorAt — когда подтверждён второй фактор; nil у частичной сессии.
	FactorAt *time.Time
}

// Elevated — второй фактор подтверждён.
func (a Actor) Elevated() bool { return a.FactorAt != nil }

// isAdmin — администратор в админском пространстве С подтверждённым
// фактором. Частичная сессия администратором не считается: она годится
// только на ввод кода, и Policy не даёт ей ничего больше.
func (a Actor) isAdmin() bool {
	return a.Role == account.RoleAdmin && a.Space == account.SpaceAdmin && a.Elevated()
}
