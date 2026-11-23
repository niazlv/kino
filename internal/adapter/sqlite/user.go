package sqlite

import (
	"context"
	"database/sql"
	"time"

	appcatalog "github.com/niazleushkin/kino/internal/app/catalog"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

// ---- отметки и prefs (порт app/marks.Repo и соседние) ----

type MarksRepo struct{ db *DB }

func NewMarksRepo(db *DB) *MarksRepo { return &MarksRepo{db} }

func (r *MarksRepo) Get(ctx context.Context, u kernel.UserID, t kernel.TitleID) (account.Marks, error) {
	m := account.Marks{UserID: u, TitleID: t}
	var following, watchlist, hidden int
	err := r.db.QueryRowContext(ctx, `
		SELECT following, watchlist, hidden
		FROM user_titles WHERE user_id = ? AND title_id = ?`, u, t).
		Scan(&following, &watchlist, &hidden)
	if err != nil {
		if mapped := MapError(err); mapped == kernel.ErrNotFound {
			// Отметок нет — обычное состояние, а не ошибка.
			return m, nil
		} else {
			return account.Marks{}, mapped
		}
	}
	m.Following, m.Watchlist, m.Hidden = following != 0, watchlist != 0, hidden != 0
	return m, nil
}

// Set записывает отметки целиком; снятое «Слежу» запоминается (unfollowed_at),
// включённое — стирает память о снятии.
func (r *MarksRepo) Set(ctx context.Context, m account.Marks) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_titles (user_id, title_id, following, watchlist, hidden, updated_at, unfollowed_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (user_id, title_id) DO UPDATE SET
			following = excluded.following,
			watchlist = excluded.watchlist,
			hidden    = excluded.hidden,
			updated_at = excluded.updated_at,
			unfollowed_at = CASE
				WHEN excluded.following THEN NULL
				WHEN user_titles.following THEN excluded.updated_at
				ELSE user_titles.unfollowed_at END`,
		m.UserID, m.TitleID, boolInt(m.Following), boolInt(m.Watchlist), boolInt(m.Hidden),
		nowMs(), nil)
	return MapError(err)
}

func (r *MarksRepo) Prefs(ctx context.Context, u kernel.UserID, t kernel.TitleID) (account.TitlePrefs, error) {
	p := account.TitlePrefs{UserID: u, TitleID: t}
	err := r.db.QueryRowContext(ctx, `
		SELECT audio_pref, text_pref, source_pref
		FROM user_titles WHERE user_id = ? AND title_id = ?`, u, t).
		Scan(&p.Audio, &p.Subtitle, &p.Source)
	if err != nil {
		if mapped := MapError(err); mapped == kernel.ErrNotFound {
			return p, nil
		} else {
			return account.TitlePrefs{}, mapped
		}
	}
	return p, nil
}

// SetPrefs пишет только переданные поля; отметки и updated_at не трогаются.
func (r *MarksRepo) SetPrefs(ctx context.Context, p account.TitlePrefs) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_titles (user_id, title_id, audio_pref, text_pref, source_pref)
		VALUES (?,?,?,?)
		ON CONFLICT (user_id, title_id) DO UPDATE SET
			audio_pref  = COALESCE(excluded.audio_pref,  user_titles.audio_pref),
			text_pref   = COALESCE(excluded.text_pref,   user_titles.text_pref),
			source_pref = COALESCE(excluded.source_pref, user_titles.source_pref)`,
		p.UserID, p.TitleID, nullStrPtr(p.Audio), nullStrPtr(p.Subtitle), nullStrPtr(p.Source))
	return MapError(err)
}

func nullStrPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func (r *MarksRepo) UnhideByEpisode(ctx context.Context, u kernel.UserID, e kernel.EpisodeID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE user_titles SET hidden = 0, updated_at = ?
		WHERE user_id = ? AND hidden AND title_id =
			(SELECT title_id FROM canonical_episodes WHERE id = ?)`, nowMs(), u, e)
	return MapError(err)
}

func (r *MarksRepo) SetFollowing(ctx context.Context, u kernel.UserID, t kernel.TitleID, on bool, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_titles (user_id, title_id, following, updated_at, unfollowed_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT (user_id, title_id) DO UPDATE SET
			following = excluded.following, updated_at = excluded.updated_at,
			unfollowed_at = CASE
				WHEN excluded.following THEN NULL
				WHEN user_titles.following THEN excluded.updated_at
				ELSE user_titles.unfollowed_at END`,
		u, t, boolInt(on), ms(at), nil)
	return MapError(err)
}

// FollowByEpisode ставит «Слежу» идущему сериалу досмотревшего: снятое
// руками (unfollowed_at) не возвращается, стоящее не переставляется.
func (r *MarksRepo) FollowByEpisode(ctx context.Context, u kernel.UserID, e kernel.EpisodeID) error {
	return r.followWatching(ctx, u, `(SELECT title_id FROM canonical_episodes WHERE id = ?)`, e)
}

func (r *MarksRepo) FollowByTitle(ctx context.Context, u kernel.UserID, t kernel.TitleID) error {
	return r.followWatching(ctx, u, `?`, t)
}

func (r *MarksRepo) followWatching(ctx context.Context, u kernel.UserID, titleExpr string, arg any) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_titles (user_id, title_id, following, updated_at)
		SELECT ?, t.id, 1, ?
		FROM canonical_titles t
		WHERE t.id = `+titleExpr+` AND t.kind = 'series' AND t.finished = 0
		ON CONFLICT (user_id, title_id) DO UPDATE SET following = 1, updated_at = excluded.updated_at
		WHERE user_titles.following = 0 AND user_titles.unfollowed_at IS NULL`,
		u, nowMs(), arg)
	return MapError(err)
}

func (r *MarksRepo) SetWatchlist(ctx context.Context, u kernel.UserID, t kernel.TitleID, on bool, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_titles (user_id, title_id, watchlist, updated_at)
		VALUES (?,?,?,?)
		ON CONFLICT (user_id, title_id) DO UPDATE SET
			watchlist = excluded.watchlist, updated_at = excluded.updated_at`,
		u, t, boolInt(on), ms(at))
	return MapError(err)
}

// List — тайтлы по виду отметки, страницей с прокруткой.
func (r *MarksRepo) List(ctx context.Context, u kernel.UserID,
	kind account.MarkKind, limit, offset int) ([]catalog.Title, int, error) {
	var where string
	switch kind {
	case account.MarkFollowing:
		where = "m.following"
	case account.MarkWatchlist:
		where = "m.watchlist"
	default:
		return nil, 0, kernel.ErrInvalidInput
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.kind, t.title, COALESCE(t.year,0), COALESCE(t.poster_url,''),
		       count(*) OVER ()
		FROM user_titles m
		JOIN canonical_titles t ON t.id = m.title_id
		WHERE m.user_id = ? AND `+where+` AND m.hidden = 0
		ORDER BY m.updated_at DESC, t.id DESC
		LIMIT ? OFFSET ?`, u, limit, offset)
	if err != nil {
		return nil, 0, MapError(err)
	}
	defer rows.Close()

	var out []catalog.Title
	var total int
	for rows.Next() {
		var t catalog.Title
		if err := rows.Scan(&t.ID, &t.Kind, &t.Title, &t.Year, &t.Poster, &total); err != nil {
			return nil, 0, MapError(err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, MapError(err)
	}
	if len(out) == 0 && offset > 0 {
		err = r.db.QueryRowContext(ctx, `
			SELECT count(*) FROM user_titles m
			WHERE m.user_id = ? AND `+where+` AND m.hidden = 0`, u).Scan(&total)
		if err != nil {
			return nil, 0, MapError(err)
		}
	}
	return out, total, nil
}

// FollowedRefs — всё, за чем следит хоть кто-то: каталог общий.
func (r *MarksRepo) FollowedRefs(ctx context.Context) ([]appcatalog.FollowedRef, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT m.title_id, x.provider_id, x.provider_ref
		FROM user_titles m
		JOIN external_refs x ON x.title_id = m.title_id AND x.disabled = 0
		WHERE m.following
		ORDER BY m.title_id`)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()

	var out []appcatalog.FollowedRef
	for rows.Next() {
		var f appcatalog.FollowedRef
		if err := rows.Scan(&f.TitleID, &f.Provider, &f.ItemID); err != nil {
			return nil, MapError(err)
		}
		out = append(out, f)
	}
	return out, MapError(rows.Err())
}

// ---- прогресс (порт app/progress.Repo/ContinueRepo) ----

type ProgressRepo struct{ db *DB }

func NewProgressRepo(db *DB) *ProgressRepo { return &ProgressRepo{db} }

func (r *ProgressRepo) Get(ctx context.Context, u kernel.UserID, e kernel.EpisodeID) (playback.Progress, error) {
	var p playback.Progress
	var updated, client sql.NullInt64
	var device sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts,
		       COALESCE(device_id, '')
		FROM watch_progress WHERE user_id = ? AND episode_id = ?`, u, e).
		Scan(&p.UserID, &p.EpisodeID, &p.PositionMS, &p.DurationMS, &p.Status,
			&updated, &client, &device)
	if err != nil {
		return p, MapError(err)
	}
	p.UpdatedAt = timeOf(updated)
	p.ClientTS = timeOf(client)
	p.DeviceID = device.String
	return p, nil
}

// Upsert — те же правила слияния, что в проде: свой девайс не откатывает
// свой же более свежий отчёт.
func (r *ProgressRepo) Upsert(ctx context.Context, p playback.Progress) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO watch_progress
			(user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts, device_id)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (user_id, episode_id) DO UPDATE SET
			position_ms = excluded.position_ms,
			duration_ms = excluded.duration_ms,
			status      = excluded.status,
			updated_at  = excluded.updated_at,
			client_ts   = excluded.client_ts,
			device_id   = excluded.device_id
		WHERE NOT (COALESCE(watch_progress.device_id, '') <> ''
			AND watch_progress.device_id = excluded.device_id
			AND watch_progress.client_ts > excluded.client_ts)`,
		p.UserID, p.EpisodeID, p.PositionMS, p.DurationMS, p.Status,
		ms(p.UpdatedAt), ms(p.ClientTS), nullStr(p.DeviceID))
	return MapError(err)
}

// ContinueItem — строка «продолжить смотреть»; в progress.Item её переводит
// клеевой тип композиционного корня.
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

// Continue — тайтлы, которые человек начинал, и первая непросмотренная
// серия каждого. DISTINCT ON из продового запроса здесь — группировка по
// ключу «сезон×серия»: SQLite оконных функций первой серии не имеет.
func (r *ProgressRepo) Continue(ctx context.Context, u kernel.UserID, limit, offset int) ([]ContinueItem, int, error) {
	rows, err := r.db.QueryContext(ctx, `
		WITH touched AS (
			SELECT e.title_id, max(p.updated_at) AS last_seen
			FROM watch_progress p
			JOIN canonical_episodes e ON e.id = p.episode_id
			WHERE p.user_id = ?
			GROUP BY e.title_id
		),
		next_ep AS (
			SELECT e.title_id, e.id AS episode_id, e.season_no, e.episode_no,
			       COALESCE(p.position_ms, 0) AS position_ms,
			       COALESCE(NULLIF(p.duration_ms, 0), e.duration_sec * 1000, 0) AS duration_ms
			FROM canonical_episodes e
			JOIN touched t ON t.title_id = e.title_id
			LEFT JOIN watch_progress p ON p.episode_id = e.id AND p.user_id = ?
			WHERE p.status IS NOT 'completed'
		),
		first_ep AS (
			SELECT title_id, min(season_no * 1000000 + episode_no) AS firstkey
			FROM next_ep GROUP BY title_id
		)
		SELECT n.episode_id, ti.id, ti.title, ti.kind, COALESCE(ti.year,0),
		       (ti.poster_url IS NOT NULL),
		       n.season_no, n.episode_no, n.position_ms, n.duration_ms,
		       count(*) OVER ()
		FROM next_ep n
		JOIN first_ep f ON f.title_id = n.title_id
		                AND f.firstkey = n.season_no * 1000000 + n.episode_no
		JOIN touched t          ON t.title_id = n.title_id
		JOIN canonical_titles ti ON ti.id = n.title_id
		LEFT JOIN user_titles m ON m.user_id = ? AND m.title_id = n.title_id
		WHERE NOT COALESCE(m.hidden, 0)
		ORDER BY t.last_seen DESC, n.episode_id DESC
		LIMIT ? OFFSET ?`, u, u, u, limit, offset)
	if err != nil {
		return nil, 0, MapError(err)
	}
	defer rows.Close()

	var out []ContinueItem
	var total int
	for rows.Next() {
		var it ContinueItem
		if err := rows.Scan(&it.EpisodeID, &it.TitleID, &it.Title, &it.Kind, &it.Year,
			&it.HasArt, &it.SeasonNo, &it.EpisodeNo, &it.PositionMS, &it.DurationMS,
			&total); err != nil {
			return nil, 0, MapError(err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, MapError(err)
	}
	if len(out) == 0 && offset > 0 {
		err = r.db.QueryRowContext(ctx, `
			SELECT count(*) FROM (
				SELECT n.episode_id
				FROM (
					SELECT e.title_id, e.id AS episode_id, e.season_no, e.episode_no
					FROM canonical_episodes e
					JOIN (
						SELECT e.title_id
						FROM watch_progress p
						JOIN canonical_episodes e ON e.id = p.episode_id
						WHERE p.user_id = ?
						GROUP BY e.title_id
					) t ON t.title_id = e.title_id
					LEFT JOIN watch_progress p ON p.episode_id = e.id AND p.user_id = ?
					WHERE p.status IS NOT 'completed'
				) n
				LEFT JOIN user_titles m ON m.user_id = ? AND m.title_id = n.title_id
				WHERE NOT COALESCE(m.hidden, 0)
			) q`, u, u, u).Scan(&total)
		if err != nil {
			return nil, 0, MapError(err)
		}
	}
	return out, total, nil
}

// ForTitle — прогресс по всем сериям тайтла одним запросом.
func (r *ProgressRepo) ForTitle(ctx context.Context, u kernel.UserID,
	titleID kernel.TitleID) (map[kernel.EpisodeID]playback.Progress, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.episode_id, p.position_ms, p.duration_ms, p.status
		FROM watch_progress p
		JOIN canonical_episodes e ON e.id = p.episode_id
		WHERE p.user_id = ? AND e.title_id = ?`, u, titleID)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()

	out := map[kernel.EpisodeID]playback.Progress{}
	for rows.Next() {
		var p playback.Progress
		if err := rows.Scan(&p.EpisodeID, &p.PositionMS, &p.DurationMS, &p.Status); err != nil {
			return nil, MapError(err)
		}
		out[p.EpisodeID] = p
	}
	return out, MapError(rows.Err())
}

// SetWatched помечает просмотренной или УДАЛЯЕТ строку: «не смотрел» и
// «смотрел нисколько» — одно и то же (см. postgres-версию).
func (r *ProgressRepo) SetWatched(ctx context.Context, u kernel.UserID,
	e kernel.EpisodeID, watched bool) error {
	if !watched {
		_, err := r.db.ExecContext(ctx,
			`DELETE FROM watch_progress WHERE user_id = ? AND episode_id = ?`, u, e)
		return MapError(err)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO watch_progress
			(user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts, device_id)
		SELECT ?, e.id, COALESCE(e.duration_sec,0)*1000, COALESCE(e.duration_sec,0)*1000,
		       'completed', ?, ?, 'manual'
		FROM canonical_episodes e WHERE e.id = ?
		ON CONFLICT (user_id, episode_id) DO UPDATE SET
			position_ms = excluded.position_ms,
			duration_ms = max(excluded.duration_ms, watch_progress.duration_ms),
			status      = 'completed',
			updated_at  = excluded.updated_at,
			client_ts   = excluded.client_ts,
			device_id   = excluded.device_id`, u, nowMs(), nowMs(), e)
	return MapError(err)
}

// SetSeasonWatched помечает весь сезон разом; возвращает число серий.
func (r *ProgressRepo) SetSeasonWatched(ctx context.Context, u kernel.UserID,
	t kernel.TitleID, season int, watched bool) (int64, error) {
	if !watched {
		res, err := r.db.ExecContext(ctx, `
			DELETE FROM watch_progress
			WHERE user_id = ? AND episode_id IN
				(SELECT id FROM canonical_episodes WHERE title_id = ? AND season_no = ?)`,
			u, t, season)
		if err != nil {
			return 0, MapError(err)
		}
		n, _ := res.RowsAffected()
		return n, nil
	}
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO watch_progress
			(user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts, device_id)
		SELECT ?, e.id, COALESCE(e.duration_sec,0)*1000, COALESCE(e.duration_sec,0)*1000,
		       'completed', ?, ?, 'manual'
		FROM canonical_episodes e
		WHERE e.title_id = ? AND e.season_no = ?
		ON CONFLICT (user_id, episode_id) DO UPDATE SET
			position_ms = excluded.position_ms,
			duration_ms = max(excluded.duration_ms, watch_progress.duration_ms),
			status      = 'completed',
			updated_at  = excluded.updated_at,
			client_ts   = excluded.client_ts,
			device_id   = excluded.device_id`,
		u, nowMs(), nowMs(), t, season)
	if err != nil {
		return 0, MapError(err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ---- производные ленты: непросмотренное по отслеживаемому и история ----

type NewEpisodeItem struct {
	TitleID   kernel.TitleID
	Title     string
	Kind      string
	Year      int
	HasArt    bool
	Unwatched int
	Total     int
}

// NewEpisodes — «новые эпизоды» и «я смотрю»: они отличаются одним условием.
func (r *ProgressRepo) NewEpisodes(ctx context.Context, u kernel.UserID, limit, offset int,
	onlyUnwatched bool) ([]NewEpisodeItem, int, int, error) {
	having := ""
	if onlyUnwatched {
		having = " HAVING unwatched > 0"
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH grouped AS (
			SELECT t.id, t.title, t.kind, COALESCE(t.year,0) AS year,
			       (t.poster_url IS NOT NULL) AS has_art,
			       count(*) FILTER (WHERE p.status IS NOT 'completed') AS unwatched,
			       count(*) AS total
			FROM user_titles m
			JOIN canonical_titles   t ON t.id = m.title_id
			JOIN canonical_episodes e ON e.title_id = t.id
			LEFT JOIN watch_progress p ON p.episode_id = e.id AND p.user_id = m.user_id
			WHERE m.user_id = ? AND m.following AND m.hidden = 0
			GROUP BY t.id`+having+`
		)
		SELECT id, title, kind, year, has_art, unwatched, total,
		       count(*) OVER () AS grand,
		       sum(CASE WHEN unwatched > 0 THEN 1 ELSE 0 END) OVER () AS active
		FROM grouped
		ORDER BY unwatched DESC, title, id
		LIMIT ? OFFSET ?`, u, limit, offset)
	if err != nil {
		return nil, 0, 0, MapError(err)
	}
	defer rows.Close()

	var out []NewEpisodeItem
	var grand, active int
	for rows.Next() {
		var it NewEpisodeItem
		if err := rows.Scan(&it.TitleID, &it.Title, &it.Kind, &it.Year,
			&it.HasArt, &it.Unwatched, &it.Total, &grand, &active); err != nil {
			return nil, 0, 0, MapError(err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, MapError(err)
	}
	// Пустая страница за концом ленты не видит оконных счётчиков.
	if len(out) == 0 && offset > 0 {
		err = r.db.QueryRowContext(ctx, `
			SELECT count(*),
			       COALESCE(sum(CASE WHEN unwatched > 0 THEN 1 ELSE 0 END), 0)
			FROM (
				SELECT count(*) FILTER (WHERE p.status IS NOT 'completed') AS unwatched
				FROM user_titles m
				JOIN canonical_titles   t ON t.id = m.title_id
				JOIN canonical_episodes e ON e.title_id = t.id
				LEFT JOIN watch_progress p ON p.episode_id = e.id AND p.user_id = m.user_id
				WHERE m.user_id = ? AND m.following AND m.hidden = 0
				GROUP BY t.id
			) g`, u).Scan(&grand, &active)
		if err != nil {
			return nil, 0, 0, MapError(err)
		}
	}
	return out, grand, active, nil
}

type HistoryItem struct {
	EpisodeID  kernel.EpisodeID
	TitleID    kernel.TitleID
	Title      string
	Kind       string
	SeasonNo   int
	EpisodeNo  int
	PositionMS int64
	DurationMS int64
	Status     string
	HasArt     bool
	HasShot    bool
	UpdatedAt  time.Time
}

type HistoryFilter struct {
	Kind  string
	Query string
}

// History — что и когда смотрели; поиск по подстроке — по title_lower,
// сведённому кодом (lower() в SQLite не знает кириллицы).
func (r *ProgressRepo) History(ctx context.Context, u kernel.UserID, limit, offset int,
	f HistoryFilter) ([]HistoryItem, int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.episode_id, t.id, t.title, t.kind, e.season_no, e.episode_no,
		       p.position_ms, p.duration_ms, p.status,
		       (t.poster_url IS NOT NULL), (e.thumbnail_url IS NOT NULL), p.updated_at,
		       count(*) OVER ()
		FROM watch_progress p
		JOIN canonical_episodes e ON e.id = p.episode_id
		JOIN canonical_titles   t ON t.id = e.title_id
		WHERE p.user_id = ?
		  AND (? = '' OR t.kind = ?)
		  AND (? = '' OR t.title_lower LIKE '%' || ? || '%')
		ORDER BY p.updated_at DESC, p.episode_id DESC
		LIMIT ? OFFSET ?`, u, f.Kind, f.Kind, f.Query, f.Query, limit, offset)
	if err != nil {
		return nil, 0, MapError(err)
	}
	defer rows.Close()

	var out []HistoryItem
	var total int
	for rows.Next() {
		var it HistoryItem
		var updated sql.NullInt64
		if err := rows.Scan(&it.EpisodeID, &it.TitleID, &it.Title, &it.Kind, &it.SeasonNo, &it.EpisodeNo,
			&it.PositionMS, &it.DurationMS, &it.Status, &it.HasArt, &it.HasShot,
			&updated, &total); err != nil {
			return nil, 0, MapError(err)
		}
		it.UpdatedAt = timeOf(updated)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, MapError(err)
	}
	if len(out) == 0 && offset > 0 {
		err = r.db.QueryRowContext(ctx, `
			SELECT count(*) FROM watch_progress p
			JOIN canonical_episodes e ON e.id = p.episode_id
			JOIN canonical_titles   t ON t.id = e.title_id
			WHERE p.user_id = ? AND (? = '' OR t.kind = ?)
			  AND (? = '' OR t.title_lower LIKE '%' || ? || '%')`,
			u, f.Kind, f.Kind, f.Query, f.Query).Scan(&total)
		if err != nil {
			return nil, 0, MapError(err)
		}
	}
	return out, total, nil
}
