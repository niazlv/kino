// Package catalog — use cases каталога: инкрементальная синхронизация
// с провайдерами и склейка произведений между площадками.
package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Writer — запись каталога. Реализуется adapter/postgres.
type Writer interface {
	UpsertTitle(ctx context.Context, t dp.Title) (kernel.TitleID, error)
	UpsertEpisodes(ctx context.Context, titleID kernel.TitleID, eps []dp.Episode) error
}

// SyncState — курсор обхода, чтобы не тянуть каталог целиком каждый раз.
type SyncState interface {
	LastSynced(ctx context.Context, providerID string) (time.Time, error)
	SaveProgress(ctx context.Context, providerID string, at time.Time, seen int64, err error) error
}

// Source — та часть провайдера, что нужна синку.
type Source interface {
	Describe(ctx context.Context) (dp.Descriptor, error)
	Episodes(ctx context.Context, ref kernel.ProviderRef) ([]dp.Episode, error)
	ChangedSince(ctx context.Context, since time.Time, fn func(dp.Title) error) error
}

type Clock interface{ Now() time.Time }

type SyncService struct {
	w     Writer
	state SyncState
	clock Clock
	log   *slog.Logger
}

func NewSyncService(w Writer, state SyncState, clock Clock, log *slog.Logger) *SyncService {
	return &SyncService{w: w, state: state, clock: clock, log: log}
}

// Options ограничивают один прогон.
type Options struct {
	// MaxTitles > 0 останавливает прогон после N тайтлов. Нужен для первого
	// запуска: полный каталог это десятки тысяч элементов, и разработчику
	// на ноутбуке столько не нужно.
	MaxTitles int
	// FullResync игнорирует курсор и идёт с начала.
	FullResync bool
	// WithEpisodes догружает серии. Это отдельный запрос на тайтл, поэтому
	// при массовом обходе выключается.
	WithEpisodes bool
	// Spread — растянуть полный обход примерно на столько, чтобы не бить
	// площадку залпом: пятьдесят тысяч тайтлов за четверть часа она считает
	// перебором и отвечает ограничением всем, включая просмотр. Пауза
	// считается от ExpectedTitles — сколько тайтлов ждём увидеть (размер
	// нашего каталога и есть хорошая оценка). Действует только при FullResync.
	Spread         time.Duration
	ExpectedTitles int64
}

// pacePerTitle — пауза после каждого тайтла, чтобы обход занял около spread.
// Сверху ограничена: если оценка числа тайтлов сильно занижена, обход всё
// равно не должен растягиваться на сутки.
func pacePerTitle(spread time.Duration, expected int64) time.Duration {
	if spread <= 0 || expected <= 0 {
		return 0
	}
	p := spread / time.Duration(expected)
	if p > 5*time.Second {
		p = 5 * time.Second
	}
	return p
}

// Run выполняет один проход синхронизации.
func (s *SyncService) Run(ctx context.Context, src Source, opts Options) (int64, error) {
	d, err := src.Describe(ctx)
	if err != nil {
		return 0, fmt.Errorf("describe: %w", err)
	}
	if err := d.Compatible(); err != nil {
		return 0, fmt.Errorf("provider %q: %w", d.ProviderID, err)
	}

	var since time.Time
	if !opts.FullResync {
		if since, err = s.state.LastSynced(ctx, d.ProviderID); err != nil {
			return 0, err
		}
	}
	if !d.Has(dp.CapSyncDelta) {
		// Провайдер не умеет дельту — честно идём полностью, а не делаем
		// вид, что курсор работает.
		since = time.Time{}
	}

	started := s.clock.Now()
	var seen int64
	var newest time.Time

	var pace time.Duration
	if opts.FullResync {
		pace = pacePerTitle(opts.Spread, opts.ExpectedTitles)
		if pace > 0 {
			s.log.InfoContext(ctx, "полный обход растянут",
				"spread", opts.Spread.String(), "expected", opts.ExpectedTitles, "pace", pace.String())
		}
	}

	walkErr := src.ChangedSince(ctx, since, func(t dp.Title) error {
		id, err := s.w.UpsertTitle(ctx, t)
		if err != nil {
			// Один битый тайтл не должен рвать весь обход: логируем и идём
			// дальше, иначе синк никогда не доходит до конца.
			s.log.WarnContext(ctx, "тайтл не сохранён", "ref", t.Ref.String(), "err", err)
			return nil
		}
		if opts.WithEpisodes {
			eps, err := src.Episodes(ctx, t.Ref)
			if err != nil {
				s.log.WarnContext(ctx, "серии не получены", "ref", t.Ref.String(), "err", err)
			} else if err := s.w.UpsertEpisodes(ctx, id, eps); err != nil {
				s.log.WarnContext(ctx, "серии не сохранены", "ref", t.Ref.String(), "err", err)
			}
		}
		seen++
		if t.UpdatedAt.After(newest) {
			newest = t.UpdatedAt
		}
		if opts.MaxTitles > 0 && seen >= int64(opts.MaxTitles) {
			return errStop
		}
		if pace > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pace):
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != errStop {
		_ = s.state.SaveProgress(ctx, d.ProviderID, since, seen, walkErr)
		return seen, fmt.Errorf("обход: %w", walkErr)
	}

	// Курсор двигаем на время НАЧАЛА прогона, а не на самый свежий
	// увиденный тайтл: между ними могли появиться изменения, и по времени
	// тайтла мы бы их пропустили навсегда.
	cursor := started
	if seen == 0 {
		cursor = since
	}
	if err := s.state.SaveProgress(ctx, d.ProviderID, cursor, seen, nil); err != nil {
		return seen, err
	}
	s.log.InfoContext(ctx, "синк завершён",
		"provider", d.ProviderID, "titles", seen,
		"since", since.Format(time.RFC3339), "dur", s.clock.Now().Sub(started).String())
	return seen, nil
}

var errStop = fmt.Errorf("stop")
