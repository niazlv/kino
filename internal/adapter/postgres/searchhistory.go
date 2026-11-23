package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// keepLast — сколько последних запросов хранится. История — подсказка,
// а не архив: длинный хвост никому не виден.
const keepLast = 20

type SearchHistoryRepo struct{ db *pgxpool.Pool }

func NewSearchHistoryRepo(db *pgxpool.Pool) *SearchHistoryRepo {
	return &SearchHistoryRepo{db}
}

// Record запоминает запрос и подрезает хвост: история — короткий список
// недавнего, а не журнал всего, что когда-либо набирали.
func (r *SearchHistoryRepo) Record(ctx context.Context, u kernel.UserID, q string) error {
	if _, err := r.db.Exec(ctx, `
		INSERT INTO search_history (user_id, query, searched_at)
		VALUES ($1,$2, now())
		ON CONFLICT (user_id, query) DO UPDATE SET searched_at = now()`, u, q); err != nil {
		return pg.MapError(err)
	}
	_, err := r.db.Exec(ctx, `
		DELETE FROM search_history
		WHERE user_id = $1 AND query NOT IN (
			SELECT query FROM search_history WHERE user_id = $1
			ORDER BY searched_at DESC LIMIT $2)`, u, keepLast)
	return pg.MapError(err)
}

func (r *SearchHistoryRepo) List(ctx context.Context, u kernel.UserID, limit int) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT query FROM search_history WHERE user_id = $1
		ORDER BY searched_at DESC LIMIT $2`, u, limit)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, q)
	}
	return out, pg.MapError(rows.Err())
}

func (r *SearchHistoryRepo) Delete(ctx context.Context, u kernel.UserID, q string) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM search_history WHERE user_id = $1 AND query = $2`, u, q)
	return pg.MapError(err)
}

func (r *SearchHistoryRepo) Clear(ctx context.Context, u kernel.UserID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM search_history WHERE user_id = $1`, u)
	return pg.MapError(err)
}
