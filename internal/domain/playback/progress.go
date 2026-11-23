// Package playback — тикеты доставки, прогресс просмотра и телеметрия сессий.
package playback

import (
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type Status string

const (
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusDropped    Status = "dropped"
)

// Progress — ФУНКЦИОНАЛЬНЫЕ данные, не аналитика. Нужны продукту («продолжить
// с места»). Не дублируются в аналитический контур и не показываются в админке:
// экран «что смотрел пользователь X» воспроизвёл бы исходную проблему проекта
// в худшем виде. См. docs/adr/0005-telemetry-and-privacy.md
type Progress struct {
	UserID     kernel.UserID
	EpisodeID  kernel.EpisodeID
	PositionMS int64
	DurationMS int64
	Status     Status
	UpdatedAt  time.Time
	ClientTS   time.Time
	DeviceID   string
}

const (
	completedFraction = 0.90
	completedTailMS   = 2 * 60 * 1000
	mergeWindow       = 5 * time.Minute
)

// Completed: >90% длительности либо меньше двух минут до конца.
func (p Progress) Completed() bool {
	if p.DurationMS <= 0 {
		return false
	}
	if float64(p.PositionMS) >= float64(p.DurationMS)*completedFraction {
		return true
	}
	return p.DurationMS-p.PositionMS <= completedTailMS
}

// Merge разрешает конфликт двух устройств одного пользователя.
// С одного устройства сохраняется последнее положение, включая перемотку
// назад. Между разными устройствами в пределах окна берётся дальняя позиция.
//
// Свежесть меряет UpdatedAt, а он ставится сервером при приёме отчёта:
// client_ts с устройства может врать на часы (ушедшие часы телевизора
// иначе навсегда проигрывали бы более свежему просмотру с честных часов).
func (p Progress) Merge(in Progress) Progress {
	out := in
	if in.DeviceID != "" && (p.DeviceID == "" || in.DeviceID == p.DeviceID) {
		// Сравниваем клиентские часы только у одного устройства: поздно
		// долетевший запрос не должен отменить более свежую перемотку.
		// Пустой DeviceID — прежняя запись или оценка шлюза, точный отчёт
		// плеера заменяет её независимо от накопленного буфера.
		if in.DeviceID == p.DeviceID && in.ClientTS.Before(p.ClientTS) {
			out = p
		}
	} else if abs(in.UpdatedAt.Sub(p.UpdatedAt)) <= mergeWindow {
		if p.PositionMS > in.PositionMS {
			out.PositionMS = p.PositionMS
		}
	} else if p.UpdatedAt.After(in.UpdatedAt) {
		out = p
	}
	if out.DurationMS <= 0 {
		out.DurationMS = p.DurationMS
	}
	out.Status = DeriveStatus(out)
	return out
}

// DeriveStatus выводит статус из позиции, а не доверяет клиенту.
//
// Клиент присылает только позицию и длительность — поле статуса в теле
// запроса отсутствует, и слияние обязано его восстановить. Иначе отчёт
// со второго устройства затирал бы статус пустой строкой, и просмотр
// молча выпадал из «продолжить смотреть».
//
// Любой отчёт о позиции означает, что человек смотрит: снятый ранее
// «брошен» возвращается в работу сам, отдельного действия не требуя.
func DeriveStatus(p Progress) Status {
	if p.Completed() {
		return StatusCompleted
	}
	return StatusInProgress
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
