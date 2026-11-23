package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// Ручная правка склейки — для админки.
//
// Автоматика склеивает осторожно, но ошибается в обе стороны: дубль
// раскалывает историю просмотров, ложная склейка играет чужие серии.
// Здесь — то, чем человек это чинит. Каждая правка ставит pinned, и обход
// её больше не пересматривает.

// SourceRow — привязка площадки к тайтлу, как её показывает админка.
type SourceRow struct {
	ProviderID string
	ItemID     string
	SeasonNo   int
	Priority   int
	Manual     bool // приоритет задан руками
	Disabled   bool
	Pinned     bool
	MatchedBy  string
	MAL        string
	Episodes   int
	SeenAt     *time.Time
}

// TitleRow — строка поиска тайтлов в админке.
type TitleRow struct {
	ID      kernel.TitleID
	Kind    string
	Title   string
	Year    int
	Sources int
}

// AdminSearchTitles ищет по номеру или по названию, страницей с общим
// числом находок.
func (r *CatalogRepo) AdminSearchTitles(ctx context.Context, q string, limit, offset int) ([]TitleRow, int, error) {
	var id int64
	if n, err := strconv.ParseInt(q, 10, 64); err == nil {
		id = n
	}
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.kind, t.title, COALESCE(t.year,0),
		       (SELECT count(*) FROM external_refs x WHERE x.title_id = t.id),
		       count(*) OVER ()
		FROM canonical_titles t
		WHERE t.id = $1
		   OR ($2 <> '' AND t.title_norm ILIKE '%' || lower($2) || '%')
		   OR (length($4) >= 3 AND t.id IN (SELECT title_id FROM title_names WHERE name_key LIKE '%' || $4 || '%'))
		ORDER BY (t.id = $1) DESC, (SELECT count(*) FROM external_refs x WHERE x.title_id = t.id) DESC, t.year DESC NULLS LAST
		LIMIT $3 OFFSET $5`, id, q, limit, catalog.NormalizeName(q), offset)
	if err != nil {
		return nil, 0, pg.MapError(err)
	}
	defer rows.Close()
	var out []TitleRow
	var total int
	for rows.Next() {
		var t TitleRow
		if err := rows.Scan(&t.ID, &t.Kind, &t.Title, &t.Year, &t.Sources, &total); err != nil {
			return nil, 0, pg.MapError(err)
		}
		out = append(out, t)
	}
	return out, total, pg.MapError(rows.Err())
}

// AdminSearchRefs ищет элементы площадок по номеру или по названию их
// тайтла: подсказка для формы привязки, где нужен именно номер у площадки.
func (r *CatalogRepo) AdminSearchRefs(ctx context.Context, q string, limit int) ([]RefRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT x.provider_id, x.provider_ref, t.id, t.title
		FROM external_refs x
		JOIN canonical_titles t ON t.id = x.title_id
		WHERE x.provider_ref ILIKE '%' || $1 || '%'
		   OR ($2 <> '' AND t.title_norm ILIKE '%' || lower($2) || '%')
		ORDER BY t.id DESC
		LIMIT $3`, q, q, limit)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []RefRow
	for rows.Next() {
		var ref RefRow
		if err := rows.Scan(&ref.ProviderID, &ref.ItemID, &ref.TitleID, &ref.Title); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, ref)
	}
	return out, pg.MapError(rows.Err())
}

// RefRow — привязка площадки для подсказок: голый номер элемента рядом
// с тайтлом, к которому он прицеплен сейчас.
type RefRow struct {
	ProviderID string
	ItemID     string
	TitleID    kernel.TitleID
	Title      string
}

// Очереди дедупликации — что чинить руками, когда автоматика не справилась.
//
//	recent   — новые тайтлы: список, с которого начинают, когда искать
//	           пока нечего;
//	one      — тайтл с единственной площадкой: возможно, дубль на другой
//	           площадке живёт отдельным тайтлом, и их пора слить;
//	conflict — две привязки ОДНОЙ площадки на тайтле: обычно ошибка склейки
//	           или две страницы одного сезона;
//	nomal    — аниме без номера MAL ни у одной привязки: без номера дубль
//	           по реестру не узнается никогда.
func queueWhere(kind string) string {
	switch kind {
	case "recent":
		return `
			SELECT t.id, t.kind, t.title, COALESCE(t.year,0),
			       (SELECT count(*) FROM external_refs x WHERE x.title_id = t.id)
			FROM canonical_titles t`
	case "one":
		return `
			SELECT t.id, t.kind, t.title, COALESCE(t.year,0),
			       (SELECT count(*) FROM external_refs x WHERE x.title_id = t.id)
			FROM canonical_titles t
			WHERE (SELECT count(*) FROM external_refs x
			       WHERE x.title_id = t.id AND NOT x.disabled) = 1
			  AND t.id IN (SELECT title_id FROM external_refs)`
	case "conflict":
		return `
			SELECT t.id, t.kind, t.title, COALESCE(t.year,0),
			       (SELECT count(*) FROM external_refs x WHERE x.title_id = t.id)
			FROM canonical_titles t
			WHERE EXISTS (SELECT 1 FROM external_refs x
			              WHERE x.title_id = t.id AND NOT x.disabled
			              GROUP BY x.provider_id HAVING count(*) >= 2)`
	case "nomal":
		return `
			SELECT t.id, t.kind, t.title, COALESCE(t.year,0),
			       (SELECT count(*) FROM external_refs x WHERE x.title_id = t.id)
			FROM canonical_titles t
			WHERE EXISTS (SELECT 1 FROM unnest(t.genres) g
			              WHERE lower(trim(g)) IN ('аниме', 'anime'))
			  AND EXISTS (SELECT 1 FROM external_refs x
			              WHERE x.title_id = t.id AND NOT x.disabled)
			  AND NOT EXISTS (SELECT 1 FROM external_refs x
			                  WHERE x.title_id = t.id AND x.mal_id IS NOT NULL)`
	}
	return ""
}

// AdminTitleQueue отдаёт страницу очереди с общим числом в ней.
func (r *CatalogRepo) AdminTitleQueue(ctx context.Context, kind string,
	limit, offset int) ([]TitleRow, int, error) {
	where := queueWhere(kind)
	if where == "" {
		return nil, 0, kernel.ErrInvalidInput
	}
	rows, err := r.db.Query(ctx, where+`
		ORDER BY t.id DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, pg.MapError(err)
	}
	defer rows.Close()
	var out []TitleRow
	for rows.Next() {
		var t TitleRow
		if err := rows.Scan(&t.ID, &t.Kind, &t.Title, &t.Year, &t.Sources); err != nil {
			return nil, 0, pg.MapError(err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, pg.MapError(err)
	}
	var total int
	err = r.db.QueryRow(ctx, `SELECT count(*) FROM (`+where+`) q`).Scan(&total)
	if err != nil {
		return nil, 0, pg.MapError(err)
	}
	return out, total, nil
}

// AdminTitleQueueCounts — размеры всех очередей разом: их видно в табах,
// и это единственное место, где нужен именно счётчик.
func (r *CatalogRepo) AdminTitleQueueCounts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	for _, kind := range []string{"one", "conflict", "nomal"} {
		var n int
		err := r.db.QueryRow(ctx, `SELECT count(*) FROM (`+queueWhere(kind)+`) q`).Scan(&n)
		if err != nil {
			return nil, pg.MapError(err)
		}
		out[kind] = n
	}
	return out, nil
}

// AdminSources — привязки тайтла со счётчиком серий.
func (r *CatalogRepo) AdminSources(ctx context.Context, id kernel.TitleID) ([]SourceRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT x.provider_id, x.provider_ref, x.season_no, x.priority, x.priority_manual,
		       x.disabled, x.pinned, COALESCE(x.matched_by,''), COALESCE(x.mal_id,''),
		       (SELECT count(*) FROM episode_offers o JOIN canonical_episodes e ON e.id = o.episode_id
		        WHERE o.provider_id = x.provider_id AND o.provider_ref = x.provider_ref AND e.title_id = x.title_id),
		       (SELECT max(o.seen_at) FROM episode_offers o
		        WHERE o.provider_id = x.provider_id AND o.provider_ref = x.provider_ref)
		FROM external_refs x WHERE x.title_id = $1
		ORDER BY x.season_no, x.provider_id`, id)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []SourceRow
	for rows.Next() {
		var s SourceRow
		if err := rows.Scan(&s.ProviderID, &s.ItemID, &s.SeasonNo, &s.Priority, &s.Manual,
			&s.Disabled, &s.Pinned, &s.MatchedBy, &s.MAL, &s.Episodes, &s.SeenAt); err != nil {
			return nil, pg.MapError(err)
		}
		s.Priority = r.effectivePriority(s.ProviderID, s.Priority, s.Manual)
		out = append(out, s)
	}
	return out, pg.MapError(rows.Err())
}

// SetSourcePriority: nil — вернуть приоритет площадки из конфига.
func (r *CatalogRepo) SetSourcePriority(ctx context.Context, provider, item string, prio *int) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE external_refs SET priority = COALESCE($3, priority), priority_manual = $3 IS NOT NULL
		WHERE provider_id = $1 AND provider_ref = $2`, provider, item, prio)
	return affected(tag.RowsAffected(), err)
}

// SetSourceDisabled выключает площадку у тайтла, не забывая привязку.
func (r *CatalogRepo) SetSourceDisabled(ctx context.Context, provider, item string, off bool) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE external_refs SET disabled = $3, pinned = true
		WHERE provider_id = $1 AND provider_ref = $2`, provider, item, off)
	return affected(tag.RowsAffected(), err)
}

// AttachSource привязывает элемент площадки к тайтлу и сезону руками.
//
// Серии этого элемента пересчитываются заново при следующем открытии
// тайтла: старые предложения удаляются, а канонические серии, которые
// после этого никто не отдаёт и никто не смотрел, — тоже, иначе в списке
// остались бы серии без площадки. Тайтл, лишившийся последней площадки и
// ничьих отметок, удаляется.
func (r *CatalogRepo) AttachSource(ctx context.Context, provider, item string, to kernel.TitleID, season int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return pg.MapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM canonical_titles WHERE id = $1)`, to).Scan(&exists); err != nil {
		return pg.MapError(err)
	}
	if !exists {
		return fmt.Errorf("тайтл %d: %w", to, kernel.ErrNotFound)
	}
	var from kernel.TitleID
	err = tx.QueryRow(ctx, `SELECT title_id FROM external_refs WHERE provider_id = $1 AND provider_ref = $2`,
		provider, item).Scan(&from)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return pg.MapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO external_refs (provider_id, provider_ref, title_id, priority, season_no, matched_by, pinned)
		VALUES ($1, $2, $3, $4, $5, 'manual', true)
		ON CONFLICT (provider_id, provider_ref) DO UPDATE SET
			title_id = EXCLUDED.title_id, season_no = EXCLUDED.season_no,
			matched_by = 'manual', pinned = true`,
		provider, item, to, r.priorityOf(provider), season); err != nil {
		return pg.MapError(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM episode_offers WHERE provider_id = $1 AND provider_ref = $2`,
		provider, item); err != nil {
		return pg.MapError(err)
	}
	if err := dropOrphans(ctx, tx, to); err != nil {
		return err
	}
	if from != 0 && from != to {
		if err := dropOrphans(ctx, tx, from); err != nil {
			return err
		}
		if err := dropEmptyTitle(ctx, tx, from); err != nil {
			return err
		}
	}
	return pg.MapError(tx.Commit(ctx))
}

// DetachSource уводит элемент площадки в отдельный новый тайтл — отмена
// ошибочной склейки. Метаданные нового тайтла — копия текущего; обход
// площадки перепишет их своими, потому что у нового тайтла он единственный
// источник.
func (r *CatalogRepo) DetachSource(ctx context.Context, provider, item string) (kernel.TitleID, error) {
	var from kernel.TitleID
	var season int
	if err := r.db.QueryRow(ctx, `
		SELECT title_id, season_no FROM external_refs WHERE provider_id = $1 AND provider_ref = $2`,
		provider, item).Scan(&from, &season); err != nil {
		return 0, pg.MapError(err)
	}
	var id kernel.TitleID
	if err := r.db.QueryRow(ctx, `
		INSERT INTO canonical_titles (kind, title, title_norm, title_original, year, poster_url, plot, genres, finished)
		SELECT kind, title, title_norm, title_original, year, poster_url, plot, genres, finished
		FROM canonical_titles WHERE id = $1
		RETURNING id`, from).Scan(&id); err != nil {
		return 0, pg.MapError(err)
	}
	if err := r.AttachSource(ctx, provider, item, id, season); err != nil {
		return 0, err
	}
	return id, nil
}

// MergeTitles сливает тайтл from в into: площадки, серии, прогресс и отметки.
//
// Для дубля, заведённого склейкой по осторожным правилам. История
// просмотров не должна пропасть: прогресс переносится на ту же серию
// (сезон, номер) другого тайтла, а при встрече двух позиций остаётся более
// свежая.
func (r *CatalogRepo) MergeTitles(ctx context.Context, from, into kernel.TitleID) error {
	return r.mergeTitles(ctx, from, into, "manual", true, 0)
}

// MergeTitlesReconciled — то же слияние, но решением реконсилиации.
//
// Отличия от ручного: след «reconciled» в matched_by и БЕЗ pinned —
// закрепление остановило бы сходимость дублей из трёх площадок (первое
// слияние закрепило бы пару, и третий участник уже не приклеился бы).
// Стабильность привязки pin тут и не нужен: строка external_refs и так
// указывает на новый тайтл, а обход не пересматривает известные привязки.
func (r *CatalogRepo) MergeTitlesReconciled(ctx context.Context, from, into kernel.TitleID) error {
	return r.mergeTitles(ctx, from, into, "reconciled", false, 0)
}

// AdoptSeason переносит тайтл-сезон from в сериал into сезоном season
// (решение matching.Adopt): всё, что было у from, — площадки, серии,
// прогресс, отметки — ложится в этот сезон. from — одна работа, поэтому
// все его серии и привязки уходят в один сезон, какой бы номер у них ни
// стоял раньше.
func (r *CatalogRepo) AdoptSeason(ctx context.Context, from, into kernel.TitleID, season int) error {
	if season <= 0 {
		return kernel.ErrInvalidInput
	}
	return r.mergeTitles(ctx, from, into, "season", false, season)
}

// mergeTitles сливает from в into. season больше нуля — все серии и
// привязки from ложатся в этот сезон into (AdoptSeason), ноль — остаются
// в своих.
func (r *CatalogRepo) mergeTitles(ctx context.Context, from, into kernel.TitleID, matchedBy string, pin bool, season int) error {
	if from == into {
		return kernel.ErrInvalidInput
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return pg.MapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM canonical_titles WHERE id IN ($1, $2)`, from, into).Scan(&n); err != nil {
		return pg.MapError(err)
	}
	if n != 2 {
		return fmt.Errorf("слияние %d -> %d: %w", from, into, kernel.ErrNotFound)
	}

	// Литералы, а не параметры: шаги исполняются единым списком с $1/$2,
	// и оба значения порождаются кодом, а не запросом. Без закрепления
	// колонка pinned просто не упоминается — «SET pinned» без присваивания
	// не является валидным SQL.
	pinSQL := ", pinned = true"
	if !pin {
		pinSQL = ""
	}
	// Сезон серии у into: свой или назначенный переносом ($3).
	const toSeason = `COALESCE(NULLIF($3::int, 0), f.season_no)`
	steps := []string{
		// Серии, которых у into нет, заводятся.
		`INSERT INTO canonical_episodes (title_id, season_no, episode_no, duration_sec, thumbnail_url, title)
		 SELECT $2, ` + toSeason + `, f.episode_no, f.duration_sec, f.thumbnail_url, f.title
		 FROM canonical_episodes f WHERE f.title_id = $1
		 ON CONFLICT (title_id, season_no, episode_no) DO NOTHING`,
		// Предложения площадок — на серии into.
		`INSERT INTO episode_offers (episode_id, provider_id, provider_ref, season_no, episode_no, seen_at)
		 SELECT t.id, o.provider_id, o.provider_ref, o.season_no, o.episode_no, o.seen_at
		 FROM episode_offers o
		 JOIN canonical_episodes f ON f.id = o.episode_id AND f.title_id = $1
		 JOIN canonical_episodes t ON t.title_id = $2 AND t.season_no = ` + toSeason + ` AND t.episode_no = f.episode_no
		 ON CONFLICT DO NOTHING`,
		// Прогресс: при встрече остаётся более свежий. При переносе в один
		// сезон две серии from могут прийти в одну серию into — из них
		// тоже берётся свежая, иначе ON CONFLICT споткнулся бы о строку,
		// которую сам же вставил.
		`INSERT INTO watch_progress (user_id, episode_id, position_ms, duration_ms, status, updated_at, client_ts, device_id)
		 SELECT DISTINCT ON (p.user_id, t.id)
		        p.user_id, t.id, p.position_ms, p.duration_ms, p.status, p.updated_at, p.client_ts, p.device_id
		 FROM watch_progress p
		 JOIN canonical_episodes f ON f.id = p.episode_id AND f.title_id = $1
		 JOIN canonical_episodes t ON t.title_id = $2 AND t.season_no = ` + toSeason + ` AND t.episode_no = f.episode_no
		 ORDER BY p.user_id, t.id, p.updated_at DESC
		 ON CONFLICT (user_id, episode_id) DO UPDATE SET
		   position_ms = EXCLUDED.position_ms, duration_ms = EXCLUDED.duration_ms, status = EXCLUDED.status,
		   updated_at = EXCLUDED.updated_at, client_ts = EXCLUDED.client_ts, device_id = EXCLUDED.device_id
		 WHERE EXCLUDED.updated_at > watch_progress.updated_at`,
		// Ссылки-разрешения — на ту же серию into.
		`UPDATE share_links s SET episode_id = t.id
		 FROM canonical_episodes f, canonical_episodes t
		 WHERE s.episode_id = f.id AND f.title_id = $1
		   AND t.title_id = $2 AND t.season_no = ` + toSeason + ` AND t.episode_no = f.episode_no`,
		// Отметки: «слежу» у любого из двух — слежу; оценка и выбор
		// дорожек — если у into их не было. Снятое руками слежение
		// помнится, только пока ни одна из половин не следит.
		`INSERT INTO user_titles (user_id, title_id, following, watchlist, rating, hidden, updated_at,
		                          audio_pref, text_pref, source_pref, unfollowed_at)
		 SELECT user_id, $2, following, watchlist, rating, hidden, updated_at, audio_pref, text_pref, source_pref,
		        unfollowed_at
		 FROM user_titles WHERE title_id = $1
		 ON CONFLICT (user_id, title_id) DO UPDATE SET
		   following = user_titles.following OR EXCLUDED.following,
		   unfollowed_at = CASE WHEN user_titles.following OR EXCLUDED.following THEN NULL
		                        ELSE GREATEST(user_titles.unfollowed_at, EXCLUDED.unfollowed_at) END,
		   watchlist = user_titles.watchlist OR EXCLUDED.watchlist,
		   rating = COALESCE(user_titles.rating, EXCLUDED.rating),
		   hidden = user_titles.hidden AND EXCLUDED.hidden,
		   updated_at = GREATEST(user_titles.updated_at, EXCLUDED.updated_at),
		   audio_pref = COALESCE(user_titles.audio_pref, EXCLUDED.audio_pref),
		   text_pref = COALESCE(user_titles.text_pref, EXCLUDED.text_pref),
		   source_pref = COALESCE(user_titles.source_pref, EXCLUDED.source_pref)`,
		`INSERT INTO title_names (title_id, name_key) SELECT $2, name_key FROM title_names WHERE title_id = $1
		 ON CONFLICT DO NOTHING`,
		// Площадки — к into; ручное слияние закрепляет решение,
		// реконсилиация оставляет дорогу для третьего участника дубля.
		`UPDATE external_refs SET title_id = $2, season_no = COALESCE(NULLIF($3::int, 0), season_no)` +
			pinSQL + `, matched_by = '` + matchedBy + `' WHERE title_id = $1`,
	}
	// imdb уникален: у from он снимается до того, как попасть к into.
	var imdb *string
	if err := tx.QueryRow(ctx, `SELECT imdb_id FROM canonical_titles WHERE id = $1`, from).Scan(&imdb); err != nil {
		return pg.MapError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE canonical_titles SET imdb_id = NULL WHERE id = $1`, from); err != nil {
		return pg.MapError(err)
	}
	for i, q := range steps {
		args := []any{from, into}
		if strings.Contains(q, "$3") {
			args = append(args, season)
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return fmt.Errorf("слияние, шаг %d: %w", i+1, pg.MapError(err))
		}
	}
	// Недостающие идентификаторы и метаданные — у into, если их не было.
	if _, err := tx.Exec(ctx, `
		UPDATE canonical_titles t SET
		  imdb_id = COALESCE(t.imdb_id, $3::text),
		  kp_id = COALESCE(t.kp_id, f.kp_id), title_original = COALESCE(t.title_original, f.title_original),
		  poster_url = COALESCE(t.poster_url, f.poster_url), plot = COALESCE(t.plot, f.plot)
		FROM canonical_titles f WHERE t.id = $2 AND f.id = $1`, from, into, imdb); err != nil {
		return pg.MapError(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM canonical_titles WHERE id = $1`, from); err != nil {
		return pg.MapError(err)
	}
	return pg.MapError(tx.Commit(ctx))
}

// dropOrphans удаляет серии тайтла, которые никто не отдаёт и никто не
// смотрел. Просмотренные остаются: история дороже аккуратного списка.
func dropOrphans(ctx context.Context, tx pgx.Tx, id kernel.TitleID) error {
	_, err := tx.Exec(ctx, `
		DELETE FROM canonical_episodes e
		WHERE e.title_id = $1
		  AND NOT EXISTS (SELECT 1 FROM episode_offers o WHERE o.episode_id = e.id)
		  AND NOT EXISTS (SELECT 1 FROM watch_progress p WHERE p.episode_id = e.id)
		  AND NOT EXISTS (SELECT 1 FROM share_links s WHERE s.episode_id = e.id)
		  AND EXISTS (SELECT 1 FROM external_refs x WHERE x.title_id = $1)`, id)
	return pg.MapError(err)
}

// dropEmptyTitle удаляет тайтл без площадок, серий и чьих-либо отметок.
func dropEmptyTitle(ctx context.Context, tx pgx.Tx, id kernel.TitleID) error {
	_, err := tx.Exec(ctx, `
		DELETE FROM canonical_titles t
		WHERE t.id = $1
		  AND NOT EXISTS (SELECT 1 FROM external_refs x WHERE x.title_id = t.id)
		  AND NOT EXISTS (SELECT 1 FROM user_titles u WHERE u.title_id = t.id)
		  AND NOT EXISTS (SELECT 1 FROM canonical_episodes e
		                  JOIN watch_progress p ON p.episode_id = e.id WHERE e.title_id = t.id)`, id)
	return pg.MapError(err)
}

func affected(n int64, err error) error {
	if err != nil {
		return pg.MapError(err)
	}
	if n == 0 {
		return kernel.ErrNotFound
	}
	return nil
}
