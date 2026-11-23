package auth

import (
	"fmt"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// SudoWindow — сколько введённый код считается свежим для опасных действий.
const SudoWindow = 15 * time.Minute

// ErrStepUp — действие разрешено, но код вводился давно: транспорт просит
// его заново и возвращает к действию. Оборачивает ErrForbidden, чтобы вне
// админки читалось как обычный отказ.
var ErrStepUp = fmt.Errorf("%w: код подтверждён слишком давно", kernel.ErrForbidden)

// Policy — все решения «кому что можно» в одном месте, а не размазаны
// по хендлерам. Чистая функция от Actor и цели: тестируется таблицей.
type Policy struct{}

// CanReadProgress: свой прогресс — да. Чужой — НЕТ, включая администратора.
//
// Это не забытая ветка, а следствие цели проекта: платформа существует, чтобы
// домашние не лезли в состояние друг друга. Админ с правом читать чужую
// историю просмотров воспроизводит исходную проблему в худшем виде.
// Для отладки есть DiagnosticGrant — явный, срочный и попадающий в аудит.
func (Policy) CanReadProgress(a Actor, owner kernel.UserID, grant DiagnosticGrant) error {
	if a.UserID == owner && a.Space == account.SpaceUser {
		return nil
	}
	if a.isAdmin() && grant.Covers(owner) {
		return nil
	}
	return kernel.ErrForbidden
}

func (Policy) CanWriteProgress(a Actor, owner kernel.UserID) error {
	if a.UserID != owner || a.Space != account.SpaceUser {
		return kernel.ErrForbidden
	}
	return nil
}

// CanApproveDevice — подтвердить вход устройства может только человек из
// своей пользовательской сессии: устройство получит доступ к ЕГО аккаунту.
// Админская сессия сюда не годится по той же причине, что и к тикетам.
func (Policy) CanApproveDevice(a Actor) error {
	if a.Space != account.SpaceUser || a.UserID == 0 {
		return kernel.ErrForbidden
	}
	return nil
}

// CanManageOwnSessions — свои сессии витрины: где вошёл и выход на одном
// устройстве. Только из пользовательской сессии и только над своими:
// чужими доступами и админскими сессиями распоряжается админка.
func (Policy) CanManageOwnSessions(a Actor) error {
	if a.Space != account.SpaceUser || a.UserID == 0 {
		return kernel.ErrForbidden
	}
	return nil
}

// CanApproveDeviceFor — администратор подключает устройство к чужому
// аккаунту: человек на телефоне показывает код, админ вводит его в карточке
// пользователя. По силе это то же, что сброс пароля — вход от чужого имени, —
// поэтому и требование то же: свежий код (CanActSensitive), запись в журнал.
func (p Policy) CanApproveDeviceFor(a Actor, target kernel.UserID, now time.Time) error {
	if target == 0 {
		return kernel.ErrForbidden
	}
	return p.CanActSensitive(a, now)
}

// CanImportFor — однократный перенос с площадки на аккаунт пользователя.
//
// Только администратор и только из админки: перенос — это сотни запросов к
// общему аккаунту площадки, тому же, что держит просмотр у всей семьи, и
// кнопка у каждого на главной оборачивалась лишней нагрузкой. Перенос
// пишет, но не читает: админ видит счётчики, а не чужую историю, поэтому
// запрет CanReadProgress он не обходит.
func (Policy) CanImportFor(a Actor, target kernel.UserID) error {
	if !a.isAdmin() || target == 0 {
		return kernel.ErrForbidden
	}
	return nil
}

// CanIssueTicket — тикет выпускается только в пользовательском пространстве.
// Админская сессия не должна уметь смотреть от чужого имени.
func (Policy) CanIssueTicket(a Actor, owner kernel.UserID) error {
	if a.Space != account.SpaceUser || a.UserID != owner {
		return kernel.ErrForbidden
	}
	return nil
}

// CanAdminister — служебные операции: инвайты, провайдеры, матчинг, очередь.
func (Policy) CanAdminister(a Actor) error {
	if !a.isAdmin() {
		return kernel.ErrForbidden
	}
	return nil
}

// CanActSensitive — sudo-режим: сброс пароля, роль, второй фактор. Угнанная
// на час сессия не должна давать возможность переписать чужой доступ, не
// зная кода ещё раз.
func (Policy) CanActSensitive(a Actor, now time.Time) error {
	if !a.isAdmin() {
		return kernel.ErrForbidden
	}
	if now.Sub(*a.FactorAt) > SudoWindow {
		return ErrStepUp
	}
	return nil
}

// CanServe — машинные вызовы (gateway -> core). Отдельное пространство:
// служебные креды не дают доступа ни к пользовательскому API, ни к админке.
func (Policy) CanServe(a Actor) error {
	if a.Space != account.SpaceService {
		return kernel.ErrForbidden
	}
	return nil
}
