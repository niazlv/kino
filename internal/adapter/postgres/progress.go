package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

type ProgressRepo struct{ db *pgxpool.Pool }

func NewProgressRepo(db *pgxpool.Pool) *ProgressRepo { return &ProgressRepo{db} }

func (r *ProgressRepo) Get(ctx context.Context, u kernel.UserID, e kernel.EpisodeID) (playback.Progress, error) {
	var p playback.Progress
	err := r.db.QueryRow(ctx, `
		SELECT user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts,
		       COALESCE(device_id, '')
		FROM watch_progress WHERE user_id = $1 AND episode_id = $2`, u, e).
		Scan(&p.UserID, &p.EpisodeID, &p.PositionMS, &p.DurationMS, &p.Status,
			&p.UpdatedAt, &p.ClientTS, &p.DeviceID)
	return p, pg.MapError(err)
}

func (r *ProgressRepo) Upsert(ctx context.Context, p playback.Progress) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO watch_progress
			(user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts, device_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (user_id, episode_id) DO UPDATE SET
			position_ms = EXCLUDED.position_ms,
			duration_ms = EXCLUDED.duration_ms,
			status      = EXCLUDED.status,
			updated_at  = EXCLUDED.updated_at,
			client_ts   = EXCLUDED.client_ts,
			device_id   = EXCLUDED.device_id
		WHERE NOT (COALESCE(watch_progress.device_id, '') <> ''
			AND watch_progress.device_id = EXCLUDED.device_id
			AND watch_progress.client_ts > EXCLUDED.client_ts)`,
		p.UserID, p.EpisodeID, p.PositionMS, p.DurationMS, p.Status,
		p.UpdatedAt, p.ClientTS, p.DeviceID)
	return pg.MapError(err)
}

// ContinueItem — строка списка «продолжить смотреть». Это и есть основная
// ценность платформы: у каждого он свой и никем не сбивается.
type ContinueItem struct {
	EpisodeID  kernel.EpisodeID
	TitleID    kernel.TitleID
	Title      string
	Kind       string
	Year       int
	HasArt     bool
	SeasonNo   int
	EpisodeNo  int
	PositionMS int64
	DurationMS int64
}

// continueCTE — общая основа запросов «продолжить смотреть»: тайтлы, которые
// человек начинал, и первая непросмотренная серия каждого. Нужна и основной
// выборке, и резервному подсчёту за концом списка — вторая копия разъехалась
// бы с первой при первой же правке.
const continueCTE = `
	WITH touched AS (
		-- Тайтлы, которые человек вообще начинал, и когда трогал последний раз.
		SELECT e.title_id, max(p.updated_at) AS last_seen
		FROM watch_progress p
		JOIN canonical_episodes e ON e.id = p.episode_id
		WHERE p.user_id = $1
		GROUP BY e.title_id
	),
	next_ep AS (
		-- Первая непросмотренная серия каждого такого тайтла.
		SELECT DISTINCT ON (e.title_id)
		       e.title_id, e.id AS episode_id, e.season_no, e.episode_no,
		       COALESCE(p.position_ms, 0) AS position_ms,
		       COALESCE(NULLIF(p.duration_ms, 0), e.duration_sec * 1000, 0) AS duration_ms
		FROM canonical_episodes e
		JOIN touched t ON t.title_id = e.title_id
		LEFT JOIN watch_progress p ON p.episode_id = e.id AND p.user_id = $1
		WHERE p.status IS DISTINCT FROM 'completed'
		ORDER BY e.title_id, e.season_no, e.episode_no
	)`

func (r *ProgressRepo) Continue(ctx context.Context, u kernel.UserID, limit, offset int) ([]ContinueItem, int, error) {
	rows, err := r.db.Query(ctx, continueCTE+`
		SELECT n.episode_id, ti.id, ti.title, ti.kind, COALESCE(ti.year,0),
		       (ti.poster_url IS NOT NULL),
		       n.season_no, n.episode_no, n.position_ms, n.duration_ms,
		       count(*) OVER ()
		FROM next_ep n
		JOIN touched t         ON t.title_id = n.title_id
		JOIN canonical_titles ti ON ti.id = n.title_id
		LEFT JOIN user_titles m ON m.user_id = $1 AND m.title_id = n.title_id
		WHERE NOT COALESCE(m.hidden, false)
		ORDER BY t.last_seen DESC, n.episode_id DESC
		LIMIT $2 OFFSET $3`, u, limit, offset)
	if err != nil {
		return nil, 0, pg.MapError(err)
	}
	defer rows.Close()

	var out []ContinueItem
	var total int
	for rows.Next() {
		var it ContinueItem
		if err := rows.Scan(&it.EpisodeID, &it.TitleID, &it.Title, &it.Kind, &it.Year,
			&it.HasArt, &it.SeasonNo, &it.EpisodeNo, &it.PositionMS, &it.DurationMS,
			&total); err != nil {
			return nil, 0, pg.MapError(err)
		}
		out = append(out, it)
	}
	// Пустая страница за концом списка не видит оконного счётчика: тогда
	// общее число спрашивается отдельно — редкий путь, не горячий.
	if len(out) == 0 && offset > 0 {
		err = r.db.QueryRow(ctx, continueCTE+`
			SELECT count(*) FROM (
				SELECT n.episode_id
				FROM next_ep n
				JOIN touched t         ON t.title_id = n.title_id
				JOIN canonical_titles ti ON ti.id = n.title_id
				LEFT JOIN user_titles m ON m.user_id = $1 AND m.title_id = n.title_id
				WHERE NOT COALESCE(m.hidden, false)
			) q`, u).Scan(&total)
		if err != nil {
			return nil, 0, pg.MapError(err)
		}
	}
	return out, total, pg.MapError(rows.Err())
}

// ForTitle отдаёт прогресс пользователя по всем сериям тайтла.
//
// Одним запросом, а не по серии: у длинного сериала их под сотню, и
// отдельный поход за каждой превратил бы открытие страницы в шквал запросов.
func (r *ProgressRepo) ForTitle(ctx context.Context, u kernel.UserID,
	titleID kernel.TitleID) (map[kernel.EpisodeID]playback.Progress, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.episode_id, p.position_ms, p.duration_ms, p.status
		FROM watch_progress p
		JOIN canonical_episodes e ON e.id = p.episode_id
		WHERE p.user_id = $1 AND e.title_id = $2`, u, titleID)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	out := map[kernel.EpisodeID]playback.Progress{}
	for rows.Next() {
		var p playback.Progress
		if err := rows.Scan(&p.EpisodeID, &p.PositionMS, &p.DurationMS, &p.Status); err != nil {
			return nil, pg.MapError(err)
		}
		out[p.EpisodeID] = p
	}
	return out, pg.MapError(rows.Err())
}

// SetWatched помечает серию просмотренной или снимает отметку.
//
// Снятие УДАЛЯЕТ строку, а не обнуляет позицию: «не смотрел» и «смотрел
// нисколько» должны быть неотличимы, иначе серия навсегда останется
// в «продолжить смотреть» с нулевым прогрессом.
func (r *ProgressRepo) SetWatched(ctx context.Context, u kernel.UserID,
	e kernel.EpisodeID, watched bool) error {
	if !watched {
		_, err := r.db.Exec(ctx,
			`DELETE FROM watch_progress WHERE user_id = $1 AND episode_id = $2`, u, e)
		return pg.MapError(err)
	}
	// Позиция ставится в конец: иначе плеер при открытии попробует
	// продолжить с нуля, хотя серия отмечена просмотренной.
	//
	// Устройство — 'manual' и при обновлении строки: отметка — решение
	// человека, а не отчёт плеера. Оставленное устройство прежнего отчёта
	// отдавало отметку на милость этого же устройства: его следующий
	// отчёт с позицией до отметки (тот же 0 у открытой серии) заменял её
	// целиком, минуя правило «между устройствами — дальняя позиция».
	_, err := r.db.Exec(ctx, `
		INSERT INTO watch_progress
			(user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts, device_id)
		SELECT $1, e.id, COALESCE(e.duration_sec,0)*1000, COALESCE(e.duration_sec,0)*1000,
		       'completed', now(), now(), 'manual'
		FROM canonical_episodes e WHERE e.id = $2
		ON CONFLICT (user_id, episode_id) DO UPDATE SET
			position_ms = EXCLUDED.position_ms,
			duration_ms = GREATEST(EXCLUDED.duration_ms, watch_progress.duration_ms),
			status      = 'completed',
			updated_at  = now(),
			client_ts   = now(),
			device_id   = EXCLUDED.device_id`, u, e)
	return pg.MapError(err)
}

// SetSeasonWatched помечает весь сезон разом.
// Возвращает число затронутых серий.
func (r *ProgressRepo) SetSeasonWatched(ctx context.Context, u kernel.UserID,
	t kernel.TitleID, season int, watched bool) (int64, error) {
	if !watched {
		ct, err := r.db.Exec(ctx, `
			DELETE FROM watch_progress p
			USING canonical_episodes e
			WHERE p.episode_id = e.id AND p.user_id = $1
			  AND e.title_id = $2 AND e.season_no = $3`, u, t, season)
		if err != nil {
			return 0, pg.MapError(err)
		}
		return ct.RowsAffected(), nil
	}
	ct, err := r.db.Exec(ctx, `
		INSERT INTO watch_progress
			(user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts, device_id)
		SELECT $1, e.id, COALESCE(e.duration_sec,0)*1000, COALESCE(e.duration_sec,0)*1000,
		       'completed', now(), now(), 'manual'
		FROM canonical_episodes e
		WHERE e.title_id = $2 AND e.season_no = $3
		ON CONFLICT (user_id, episode_id) DO UPDATE SET
			position_ms = EXCLUDED.position_ms,
			duration_ms = GREATEST(EXCLUDED.duration_ms, watch_progress.duration_ms),
			status      = 'completed',
			updated_at  = now(),
			client_ts   = now(),
			device_id   = EXCLUDED.device_id`, u, t, season)
	if err != nil {
		return 0, pg.MapError(err)
	}
	return ct.RowsAffected(), nil
}
