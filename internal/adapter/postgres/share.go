package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/share"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

type ShareRepo struct{ db *pgxpool.Pool }

func NewShareRepo(db *pgxpool.Pool) *ShareRepo { return &ShareRepo{db} }

func (r *ShareRepo) Create(ctx context.Context, l share.Link) error {
	sel, err := json.Marshal(l.Selection)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO share_links (token, kind, episode_id, created_by, selection, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		l.Token, l.Kind, l.EpisodeID, l.CreatedBy, sel, l.ExpiresAt)
	return pg.MapError(err)
}

// Get читает ссылку и СРАЗУ отмечает обращение.
//
// Одним запросом: раздавший ссылку должен видеть, пользуются ли ею, а
// отдельный апдейт после чтения потерялся бы при обрыве.
func (r *ShareRepo) Get(ctx context.Context, token string) (share.Link, error) {
	var l share.Link
	var sel []byte
	err := r.db.QueryRow(ctx, `
		UPDATE share_links
		SET hits = hits + 1, last_hit_at = now()
		WHERE token = $1
		RETURNING token, kind, episode_id, created_by, selection,
		          expires_at, revoked_at, hits, last_hit_at, created_at`, token).
		Scan(&l.Token, &l.Kind, &l.EpisodeID, &l.CreatedBy, &sel,
			&l.ExpiresAt, &l.RevokedAt, &l.Hits, &l.LastHitAt, &l.CreatedAt)
	if err != nil {
		return share.Link{}, pg.MapError(err)
	}
	if len(sel) > 0 {
		_ = json.Unmarshal(sel, &l.Selection)
	}
	return l, nil
}

// Revoke отзывает ссылку. Только свою: чужие раздачи отзывает их автор.
func (r *ShareRepo) Revoke(ctx context.Context, u kernel.UserID, token string) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE share_links SET revoked_at = now()
		WHERE token = $1 AND created_by = $2 AND revoked_at IS NULL`, token, u)
	if err != nil {
		return pg.MapError(err)
	}
	if ct.RowsAffected() == 0 {
		return kernel.ErrNotFound
	}
	return nil
}

// ListMine — действующие ссылки пользователя, чтобы было что отзывать.
func (r *ShareRepo) ListMine(ctx context.Context, u kernel.UserID) ([]share.Link, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.token, s.kind, s.episode_id, s.created_by,
		       s.expires_at, s.revoked_at, s.hits, s.last_hit_at, s.created_at
		FROM share_links s
		WHERE s.created_by = $1 AND s.revoked_at IS NULL AND s.expires_at > now()
		ORDER BY s.created_at DESC LIMIT 100`, u)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []share.Link
	for rows.Next() {
		var l share.Link
		if err := rows.Scan(&l.Token, &l.Kind, &l.EpisodeID, &l.CreatedBy,
			&l.ExpiresAt, &l.RevokedAt, &l.Hits, &l.LastHitAt, &l.CreatedAt); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, l)
	}
	return out, pg.MapError(rows.Err())
}

// PurgeExpired убирает протухшие ссылки. Вызывается уборкой: смысла хранить
// их нет, а таблица иначе растёт от каждого нажатия «поделиться».
func (r *ShareRepo) PurgeExpired(ctx context.Context, graceDays int) (int64, error) {
	ct, err := r.db.Exec(ctx, `
		DELETE FROM share_links
		WHERE expires_at < now() - make_interval(days => $1)`, graceDays)
	if err != nil {
		return 0, pg.MapError(err)
	}
	return ct.RowsAffected(), nil
}

var _ = time.Time{}
