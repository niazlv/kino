package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dshare "github.com/niazleushkin/kino/internal/domain/share"
)

// ---- история поиска (порт app/searchhistory.Repo) ----

type SearchHistoryRepo struct{ db *DB }

func NewSearchHistoryRepo(db *DB) *SearchHistoryRepo { return &SearchHistoryRepo{db} }

func (r *SearchHistoryRepo) Record(ctx context.Context, u kernel.UserID, q string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO search_history (user_id, query, searched_at) VALUES (?,?,?)
		ON CONFLICT (user_id, query) DO UPDATE SET searched_at = excluded.searched_at`,
		u, q, nowMs())
	return MapError(err)
}

func (r *SearchHistoryRepo) List(ctx context.Context, u kernel.UserID, limit int) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT query FROM search_history WHERE user_id = ?
		ORDER BY searched_at DESC LIMIT ?`, u, limit)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			return nil, MapError(err)
		}
		out = append(out, q)
	}
	return out, MapError(rows.Err())
}

func (r *SearchHistoryRepo) Delete(ctx context.Context, u kernel.UserID, q string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM search_history WHERE user_id = ? AND query = ?`, u, q)
	return MapError(err)
}

func (r *SearchHistoryRepo) Clear(ctx context.Context, u kernel.UserID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM search_history WHERE user_id = ?`, u)
	return MapError(err)
}

// ---- ссылки-разрешения (порт app/share.Repo) ----

type ShareRepo struct{ db *DB }

func NewShareRepo(db *DB) *ShareRepo { return &ShareRepo{db} }

func (r *ShareRepo) Create(ctx context.Context, l dshare.Link) error {
	sel, _ := json.Marshal(l.Selection)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO share_links (token, kind, episode_id, created_by, selection, expires_at, revoked_at, hits, last_hit_at, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		l.Token, l.Kind, l.EpisodeID, l.CreatedBy, string(sel),
		ms(l.ExpiresAt), msPtrOf(l.RevokedAt), l.Hits, msPtrOf(l.LastHitAt), ms(l.CreatedAt))
	return MapError(err)
}

func msPtrOf(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ms(*t)
}

func (r *ShareRepo) Get(ctx context.Context, token string) (dshare.Link, error) {
	var l dshare.Link
	var sel sql.NullString
	var expires, revoked, lastHit, created sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT token, kind, episode_id, created_by, selection, expires_at, revoked_at,
		       hits, last_hit_at, created_at
		FROM share_links WHERE token = ?`, token).
		Scan(&l.Token, &l.Kind, &l.EpisodeID, &l.CreatedBy, &sel, &expires, &revoked,
			&l.Hits, &lastHit, &created)
	if err != nil {
		return dshare.Link{}, MapError(err)
	}
	_ = json.Unmarshal([]byte(sel.String), &l.Selection)
	l.ExpiresAt = timeOf(expires)
	l.RevokedAt = timePtrOf(revoked)
	l.LastHitAt = timePtrOf(lastHit)
	l.CreatedAt = timeOf(created)
	return l, nil
}

func (r *ShareRepo) Revoke(ctx context.Context, u kernel.UserID, token string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE share_links SET revoked_at = ? WHERE token = ? AND created_by = ? AND revoked_at IS NULL`,
		nowMs(), token, u)
	if err != nil {
		return MapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return kernel.ErrNotFound
	}
	return nil
}

func (r *ShareRepo) ListMine(ctx context.Context, u kernel.UserID) ([]dshare.Link, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT token, kind, episode_id, created_by, selection, expires_at, revoked_at,
		       hits, last_hit_at, created_at
		FROM share_links WHERE created_by = ?
		ORDER BY created_at DESC`, u)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()

	var out []dshare.Link
	for rows.Next() {
		var l dshare.Link
		var sel sql.NullString
		var expires, revoked, lastHit, created sql.NullInt64
		if err := rows.Scan(&l.Token, &l.Kind, &l.EpisodeID, &l.CreatedBy, &sel, &expires, &revoked,
			&l.Hits, &lastHit, &created); err != nil {
			return nil, MapError(err)
		}
		_ = json.Unmarshal([]byte(sel.String), &l.Selection)
		l.ExpiresAt = timeOf(expires)
		l.RevokedAt = timePtrOf(revoked)
		l.LastHitAt = timePtrOf(lastHit)
		l.CreatedAt = timeOf(created)
		out = append(out, l)
	}
	return out, MapError(rows.Err())
}

// SearchMemoryRepo — постоянная память о запросах к площадкам
// (app/catalog.SearchMemory).
type SearchMemoryRepo struct{ db *DB }

func NewSearchMemoryRepo(db *DB) *SearchMemoryRepo { return &SearchMemoryRepo{db} }

// Asked — когда запрос спрашивали последний раз и сколько нашли; нуль — не спрашивали.
func (r *SearchMemoryRepo) Asked(ctx context.Context, key string) (time.Time, int, error) {
	var at int64
	var hits int
	err := r.db.QueryRowContext(ctx, `SELECT asked_at, hits FROM search_queries WHERE query_key = ?`, key).Scan(&at, &hits)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, 0, nil
	}
	if err != nil {
		return time.Time{}, 0, err
	}
	return time.UnixMilli(at), hits, nil
}

func (r *SearchMemoryRepo) MarkAsked(ctx context.Context, key string, at time.Time, hits int) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO search_queries (query_key, asked_at, hits) VALUES (?, ?, ?)
		ON CONFLICT (query_key) DO UPDATE SET asked_at = excluded.asked_at, hits = excluded.hits`,
		key, at.UnixMilli(), hits)
	return err
}
