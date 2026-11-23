// Package retention — уборка данных, срок которых вышел.
//
// Без неё телеметрия растёт бесконечно ради строк, на которые никто не
// смотрит: детали нужны для разбора конкретной жалобы, а через три месяца
// от них полезны только суточные числа (ADR-0005).
package retention

import (
	"context"
	"log/slog"
	"time"
)

// Compactor — сама уборка. Реализуется adapter/postgres.
type Compactor interface {
	Compact(ctx context.Context, keepDays int) (days int64, sessions int64, err error)
	TrimAudit(ctx context.Context, keepDays int) (int64, error)
}

type Service struct {
	c   Compactor
	log *slog.Logger
}

func NewService(c Compactor, log *slog.Logger) *Service {
	return &Service{c: c, log: log}
}

// Сроки хранения.
const (
	// SessionDays — детали просмотров. Три месяца покрывают «стало хуже
	// после обновления» и сезонные сравнения; дальше хватает агрегатов.
	SessionDays = 90
	// AuditDays — журнал админских действий. Он маленький и нужен через
	// год, поэтому срок щедрый — но не бесконечный: «хранить вечно»
	// и «не думать об этом» разные вещи.
	AuditDays = 730
	// Раз в сутки: уборка не срочная, а гонять её чаще значит держать
	// нагрузку на базе ради нескольких удалённых строк.
	interval = 24 * time.Hour
)

// Run выполняет одну уборку.
func (s *Service) Run(ctx context.Context) {
	days, sessions, err := s.c.Compact(ctx, SessionDays)
	if err != nil {
		// Не удалось убрать — это состояние, а не повод падать: следующий
		// проход через сутки может пройти.
		s.log.ErrorContext(ctx, "телеметрия не свёрнута", "err", err)
	} else if sessions > 0 {
		s.log.InfoContext(ctx, "телеметрия свёрнута",
			"суток", days, "деталей_удалено", sessions, "хранить_дней", SessionDays)
	}

	if n, err := s.c.TrimAudit(ctx, AuditDays); err != nil {
		s.log.ErrorContext(ctx, "журнал не подрезан", "err", err)
	} else if n > 0 {
		s.log.InfoContext(ctx, "журнал подрезан", "удалено", n)
	}
}

// Start запускает уборку по расписанию до отмены контекста.
//
// Первый проход сразу: если сервис долго не поднимали, данные уже
// просрочены, и ждать сутки незачем.
func (s *Service) Start(ctx context.Context) {
	go func() {
		s.Run(ctx)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.Run(ctx)
			}
		}
	}()
}
