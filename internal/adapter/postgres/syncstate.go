package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/platform/pg"
)

type SyncStateRepo struct{ db *pgxpool.Pool }

func NewSyncStateRepo(db *pgxpool.Pool) *SyncStateRepo { return &SyncStateRepo{db} }

// LastSynced отдаёт нулевое время, если провайдер ещё не синхронизировался:
// «никогда» и «ошибка» здесь разные вещи, и первый запуск не должен падать.
func (r *SyncStateRepo) LastSynced(ctx context.Context, providerID string) (time.Time, error) {
	var t *time.Time
	err := r.db.QueryRow(ctx,
		`SELECT last_synced_at FROM provider_sync_state WHERE provider_id = $1`,
		providerID).Scan(&t)
	if err != nil {
		if e := pg.MapError(err); isNotFound(e) {
			return time.Time{}, nil
		}
		return time.Time{}, pg.MapError(err)
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

func (r *SyncStateRepo) SaveProgress(ctx context.Context, providerID string,
	at time.Time, seen int64, runErr error) error {
	var errText *string
	if runErr != nil {
		s := runErr.Error()
		errText = &s
	}
	var syncedAt *time.Time
	if !at.IsZero() {
		syncedAt = &at
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO provider_sync_state (provider_id, last_synced_at, last_run_at, last_error, titles_seen)
		VALUES ($1, $2, now(), $3, $4)
		ON CONFLICT (provider_id) DO UPDATE SET
			last_synced_at = COALESCE(EXCLUDED.last_synced_at, provider_sync_state.last_synced_at),
			last_run_at    = EXCLUDED.last_run_at,
			last_error     = EXCLUDED.last_error,
			titles_seen    = provider_sync_state.titles_seen + EXCLUDED.titles_seen`,
		providerID, syncedAt, errText, seen)
	return pg.MapError(err)
}

// LastFull — когда каталог в последний раз обходили целиком; нулевое время,
// если ещё ни разу.
func (r *SyncStateRepo) LastFull(ctx context.Context, providerID string) (time.Time, error) {
	var t *time.Time
	err := r.db.QueryRow(ctx,
		`SELECT last_full_at FROM provider_sync_state WHERE provider_id = $1`,
		providerID).Scan(&t)
	if err != nil {
		if e := pg.MapError(err); isNotFound(e) {
			return time.Time{}, nil
		}
		return time.Time{}, pg.MapError(err)
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

// MarkFull запоминает завершённый полный обход. Зовётся после SaveProgress,
// поэтому строка уже есть; на всякий случай создаётся.
func (r *SyncStateRepo) MarkFull(ctx context.Context, providerID string, at time.Time) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO provider_sync_state (provider_id, last_full_at, last_run_at, titles_seen)
		VALUES ($1, $2, now(), 0)
		ON CONFLICT (provider_id) DO UPDATE SET last_full_at = EXCLUDED.last_full_at`,
		providerID, at)
	return pg.MapError(err)
}

// RequestRescan — заявка из админки «пересканировать сейчас»: ближайший
// проход по площадке станет полным обходом. Заявка живёт в базе до тех пор,
// пока её не возьмёт обходчик.
func (r *SyncStateRepo) RequestRescan(ctx context.Context, providerID string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO provider_sync_state (provider_id, rescan_requested)
		VALUES ($1, true)
		ON CONFLICT (provider_id) DO UPDATE SET rescan_requested = true`, providerID)
	return pg.MapError(err)
}

// ConsumeRescan забирает заявку на полный обход — атомарно снимая её:
// две гонки за один проход не должны сделать работу дважды.
func (r *SyncStateRepo) ConsumeRescan(ctx context.Context, providerID string) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE provider_sync_state SET rescan_requested = false
		WHERE provider_id = $1 AND rescan_requested`, providerID)
	if err != nil {
		return false, pg.MapError(err)
	}
	return tag.RowsAffected() > 0, nil
}

// RescanRequested — площадки, чьи заявки ещё не взяты: короткий цикл
// обходчика будит их сразу, не дожидаясь получасового прохода.
func (r *SyncStateRepo) RescanRequested(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT provider_id FROM provider_sync_state WHERE rescan_requested ORDER BY provider_id`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, id)
	}
	return out, pg.MapError(rows.Err())
}

// ProviderState — строка для админского экрана площадок.
type ProviderState struct {
	ProviderID   string
	LastSyncedAt *time.Time
	LastRunAt    *time.Time
	LastError    *string
	TitlesSeen   int64
}

func (r *SyncStateRepo) List(ctx context.Context) ([]ProviderState, error) {
	rows, err := r.db.Query(ctx, `
		SELECT provider_id, last_synced_at, last_run_at, last_error, titles_seen
		FROM provider_sync_state ORDER BY provider_id`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []ProviderState
	for rows.Next() {
		var s ProviderState
		if err := rows.Scan(&s.ProviderID, &s.LastSyncedAt, &s.LastRunAt,
			&s.LastError, &s.TitlesSeen); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, s)
	}
	return out, pg.MapError(rows.Err())
}

// CatalogCounts — сводка по каталогу.
//
// WithIMDB важен не как цифра, а как риск: тайтлы без внешнего
// идентификатора склеиваются со вторым провайдером только по названию
// и году, что ненадёжно.
type CatalogCounts struct {
	Titles, Episodes, WithIMDB, Series, Movies int64
}

func (r *SyncStateRepo) CatalogCounts(ctx context.Context) (CatalogCounts, error) {
	var c CatalogCounts
	err := r.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM canonical_titles),
		       (SELECT count(*) FROM canonical_episodes),
		       (SELECT count(*) FROM canonical_titles WHERE imdb_id IS NOT NULL),
		       (SELECT count(*) FROM canonical_titles WHERE kind = 'series'),
		       (SELECT count(*) FROM canonical_titles WHERE kind = 'movie')`).
		Scan(&c.Titles, &c.Episodes, &c.WithIMDB, &c.Series, &c.Movies)
	return c, pg.MapError(err)
}

// RefCount — сколько элементов площадки уже привязано к каталогу. Оценка
// объёма полного обхода именно этой площадки: общий размер каталога у
// аниме-площадки завысил бы её в десятки раз.
func (r *SyncStateRepo) RefCount(ctx context.Context, providerID string) (int64, error) {
	var n int64
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM external_refs WHERE provider_id = $1`, providerID).Scan(&n)
	return n, pg.MapError(err)
}

// RefCounts — сколько элементов каждой площадки привязано к каталогу.
// Растёт во время обхода, в отличие от строки состояния, которая пишется
// только по его окончании: по нему видно, что первый обход идёт.
func (r *SyncStateRepo) RefCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.Query(ctx, `SELECT provider_id, count(*) FROM external_refs GROUP BY provider_id`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, pg.MapError(err)
		}
		out[id] = n
	}
	return out, pg.MapError(rows.Err())
}
