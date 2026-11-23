package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/platform/pg"
)

// RetentionRepo сворачивает старую телеметрию в суточные агрегаты.
//
// Детали нужны для разбора конкретной жалобы и живут 90 дней; после этого
// от них остаются суточные числа, которые весят килобайты и отвечают на те
// же вопросы (ADR-0005). Без этого таблица растёт бесконечно ради данных,
// на которые никто не смотрит.
type RetentionRepo struct{ db *pgxpool.Pool }

func NewRetentionRepo(db *pgxpool.Pool) *RetentionRepo { return &RetentionRepo{db} }

// Compact сворачивает всё старше keepDays и удаляет детали.
//
// Свёртка и удаление в ОДНОЙ транзакции: иначе обрыв между ними либо
// потеряет данные, либо задвоит агрегат при повторном запуске.
func (r *RetentionRepo) Compact(ctx context.Context, keepDays int) (int64, int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, 0, pg.MapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// ON CONFLICT со сложением: сутки могли частично свернуться раньше,
	// если уборка падала на середине.
	ct, err := tx.Exec(ctx, `
		INSERT INTO playback_daily
			(day, player_impl, sessions, errors, rebuffer_avg, ttff_p50_ms, bytes_served)
		SELECT started_at::date,
		       COALESCE(player_impl, 'неизвестен'),
		       count(*)::int,
		       count(*) FILTER (WHERE end_reason = 'error')::int,
		       COALESCE(avg(rebuffer_count), 0)::real,
		       COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY ttff_ms), 0)::bigint,
		       COALESCE(sum(bytes_served), 0)::bigint
		FROM playback_sessions
		WHERE started_at < now() - make_interval(days => $1)
		GROUP BY 1, 2
		ON CONFLICT (day, player_impl) DO UPDATE SET
			sessions     = playback_daily.sessions + EXCLUDED.sessions,
			errors       = playback_daily.errors + EXCLUDED.errors,
			rebuffer_avg = (playback_daily.rebuffer_avg + EXCLUDED.rebuffer_avg) / 2,
			ttff_p50_ms  = GREATEST(playback_daily.ttff_p50_ms, EXCLUDED.ttff_p50_ms),
			bytes_served = playback_daily.bytes_served + EXCLUDED.bytes_served`, keepDays)
	if err != nil {
		return 0, 0, pg.MapError(err)
	}
	days := ct.RowsAffected()

	del, err := tx.Exec(ctx, `
		DELETE FROM playback_sessions
		WHERE started_at < now() - make_interval(days => $1)`, keepDays)
	if err != nil {
		return 0, 0, pg.MapError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, pg.MapError(err)
	}
	return days, del.RowsAffected(), nil
}

// TrimAudit подрезает журнал админских действий.
//
// Он маленький и нужен через год, поэтому срок щедрый — но не бесконечный:
// «хранить вечно» и «не думать об этом» разные вещи.
func (r *RetentionRepo) TrimAudit(ctx context.Context, keepDays int) (int64, error) {
	ct, err := r.db.Exec(ctx, `
		DELETE FROM admin_audit WHERE created_at < now() - make_interval(days => $1)`, keepDays)
	if err != nil {
		return 0, pg.MapError(err)
	}
	return ct.RowsAffected(), nil
}
