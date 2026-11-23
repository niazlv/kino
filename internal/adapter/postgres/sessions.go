package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// SessionRepo пишет посессионные записи воспроизведения.
//
// Одна строка на ПРОСМОТР, а не на запрос: двухчасовой фильм — это ~2400
// обращений за сегментами, и пологовое логирование в БД дало бы девять
// миллионов строк в год ради вопросов, которых никто не задаёт (ADR-0005).
type PlaybackSessionRepo struct{ db *pgxpool.Pool }

func NewPlaybackSessionRepo(db *pgxpool.Pool) *PlaybackSessionRepo {
	return &PlaybackSessionRepo{db}
}

func (r *PlaybackSessionRepo) Open(ctx context.Context, s playback.Session) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO playback_sessions
			(id, user_id, episode_id, provider_id, started_at, device_kind, app_version,
			 player_impl, delivery)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO NOTHING`,
		s.ID, s.UserID, nullEpisode(s.EpisodeID), nullText(s.Provider), s.StartedAt,
		nullText(s.DeviceKind), nullText(s.AppVersion),
		nullText(string(s.PlayerImpl)), nullText(string(s.Delivery)))
	return pg.MapError(err)
}

// Close дописывает метрики качества. Клиент присылает их один раз, в конце
// сессии, поэтому запись идёт по идентификатору и повторный отчёт её
// перезаписывает, а не плодит дубли.
func (r *PlaybackSessionRepo) Close(ctx context.Context, s playback.Session) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO playback_sessions
			(id, user_id, episode_id, provider_id, started_at, ended_at,
			 device_kind, app_version, player_impl, delivery,
			 ttff_ms, rebuffer_count, rebuffer_ms_total, bitrate_switches, max_height,
			 bytes_served, segments_served, end_reason, error_code)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT (id) DO UPDATE SET
			ended_at          = EXCLUDED.ended_at,
			player_impl       = COALESCE(EXCLUDED.player_impl, playback_sessions.player_impl),
			delivery          = COALESCE(EXCLUDED.delivery, playback_sessions.delivery),
			-- При открытии сессии тикетом их ещё не знает сервер: приносит
			-- только отчёт плеера в конце.
			device_kind       = COALESCE(EXCLUDED.device_kind, playback_sessions.device_kind),
			app_version       = COALESCE(EXCLUDED.app_version, playback_sessions.app_version),
			ttff_ms           = EXCLUDED.ttff_ms,
			rebuffer_count    = EXCLUDED.rebuffer_count,
			rebuffer_ms_total = EXCLUDED.rebuffer_ms_total,
			bitrate_switches  = EXCLUDED.bitrate_switches,
			max_height        = EXCLUDED.max_height,
			bytes_served      = EXCLUDED.bytes_served,
			segments_served   = EXCLUDED.segments_served,
			end_reason        = EXCLUDED.end_reason,
			error_code        = EXCLUDED.error_code`,
		s.ID, s.UserID, nullEpisode(s.EpisodeID), nullText(s.Provider), s.StartedAt, s.EndedAt,
		nullText(s.DeviceKind), nullText(s.AppVersion),
		nullText(string(s.PlayerImpl)), nullText(string(s.Delivery)),
		s.TTFFMS, s.RebufferCount, s.RebufferMSTotal, s.BitrateSwitches, s.MaxHeight,
		s.BytesServed, s.SegmentsServed, nullText(string(s.EndReason)), nullText(s.ErrorCode))
	return pg.MapError(err)
}

// Stats — суточный агрегат. Именно он переживает ретенцию: детали живут
// 90 дней, агрегаты вечно, потому что весят килобайты (ADR-0005).
type Stats struct {
	Day         string
	Sessions    int
	Errors      int
	RebufferAvg float64
	TTFFP50     int64
	ByImpl      string
}

// RecentStats даёт админке разбивку по ДВИЖКУ ПЛЕЕРА.
//
// Без неё жалоба «на айфоне тормозит» неотлаживаема: не отличить проблему
// сети от бага в одной из двух реализаций плеера.
func (r *PlaybackSessionRepo) RecentStats(ctx context.Context, days int) ([]Stats, error) {
	rows, err := r.db.Query(ctx, `
		SELECT to_char(started_at::date, 'YYYY-MM-DD') AS day,
		       COALESCE(player_impl, 'неизвестен') AS impl,
		       count(*)::int,
		       count(*) FILTER (WHERE end_reason = 'error')::int,
		       COALESCE(avg(rebuffer_count), 0)::float8,
		       COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY ttff_ms), 0)::bigint
		FROM playback_sessions
		WHERE started_at > now() - make_interval(days => $1)
		GROUP BY 1, 2
		ORDER BY 1 DESC, 2`, days)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []Stats
	for rows.Next() {
		var s Stats
		if err := rows.Scan(&s.Day, &s.ByImpl, &s.Sessions, &s.Errors,
			&s.RebufferAvg, &s.TTFFP50); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, s)
	}
	return out, pg.MapError(rows.Err())
}

// DayTotal — просмотры за сутки без разбивки по движку: для графика
// «стало ли хуже» и для сводки на обзоре.
type DayTotal struct {
	Day      string
	Sessions int
	Errors   int
	Bytes    int64
}

func (r *PlaybackSessionRepo) DailyTotals(ctx context.Context, days int) ([]DayTotal, error) {
	rows, err := r.db.Query(ctx, `
		SELECT to_char(started_at::date, 'YYYY-MM-DD'),
		       count(*)::int,
		       count(*) FILTER (WHERE end_reason = 'error')::int,
		       COALESCE(sum(bytes_served), 0)::bigint
		FROM playback_sessions
		WHERE started_at > now() - make_interval(days => $1)
		GROUP BY 1 ORDER BY 1`, days)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []DayTotal
	for rows.Next() {
		var d DayTotal
		if err := rows.Scan(&d.Day, &d.Sessions, &d.Errors, &d.Bytes); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, d)
	}
	return out, pg.MapError(rows.Err())
}

func nullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullEpisode(id kernel.EpisodeID) *int64 {
	if id == 0 {
		return nil
	}
	v := int64(id)
	return &v
}
