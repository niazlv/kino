package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	appcatalog "github.com/niazleushkin/kino/internal/app/catalog"
	"github.com/niazleushkin/kino/internal/app/matching"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// CatalogRepo — ленивый каталог движка на SQLite. Реализует те же порты,
// что postgres.CatalogRepo: catalog.Reader/TitleWriter/EpisodeWriter,
// playback.TitleRepo, history.Catalog, DiscoveryMarks, ThumbnailStore.
// Отличие SQL-диалекта и сжатые правила — в ADR-0015.
type CatalogRepo struct {
	db     *DB
	prio   map[string]int
	opts   matching.Options
	anime  map[string]bool
	cartOn bool
}

func NewCatalogRepo(db *DB) *CatalogRepo { return &CatalogRepo{db: db} }

func (r *CatalogRepo) WithPriorities(p map[string]int) *CatalogRepo {
	r.prio = p
	return r
}

func (r *CatalogRepo) WithAnimeProviders(ids ...string) *CatalogRepo {
	if r.anime == nil {
		r.anime = map[string]bool{}
	}
	for _, id := range ids {
		r.anime[id] = true
	}
	return r
}

func (r *CatalogRepo) WithCartoonMatching(on bool) *CatalogRepo {
	r.cartOn = on
	r.opts.CrossCartoon = on
	return r
}

func (r *CatalogRepo) priorityOf(provider string) int {
	if p, ok := r.prio[provider]; ok {
		return p
	}
	return defaultPriority
}

func (r *CatalogRepo) effectivePriority(provider string, stored int, manual bool) int {
	if manual {
		return stored
	}
	return r.priorityOf(provider)
}

const defaultPriority = 10

// Episode — одна серия для резолва и страницы.
func (r *CatalogRepo) Episode(ctx context.Context, id kernel.EpisodeID) (catalog.Episode, error) {
	var e catalog.Episode
	var title sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, title_id, season_no, episode_no, absolute_no, COALESCE(duration_sec, 0),
		       COALESCE(thumbnail_url, ''), title
		FROM canonical_episodes WHERE id = ?`, id).
		Scan(&e.ID, &e.TitleID, &e.SeasonNo, &e.EpisodeNo, &e.AbsoluteNo, &e.DurationSec,
			&e.Thumbnail, &title)
	if err != nil {
		return e, MapError(err)
	}
	e.Title, e.TitleKnown = derefTitle(title)
	return e, nil
}

func derefTitle(t sql.NullString) (string, bool) {
	if !t.Valid {
		return "", false
	}
	return t.String, true
}

// Title поднимает тайтл вместе с источниками.
func (r *CatalogRepo) Title(ctx context.Context, id kernel.TitleID) (catalog.Title, error) {
	var t catalog.Title
	var checked sql.NullInt64
	var genres, countries sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, kind, COALESCE(imdb_id,''), COALESCE(tmdb_id,''), COALESCE(kp_id,''),
		       title, COALESCE(title_original,''), COALESCE(year, 0), COALESCE(poster_url,''),
		       COALESCE(plot,''), genres, countries, finished, sources_checked_at
		FROM canonical_titles WHERE id = ?`, id).
		Scan(&t.ID, &t.Kind, &t.IDs.IMDB, &t.IDs.TMDB, &t.IDs.Kinopoisk, &t.Title, &t.Original,
			&t.Year, &t.Poster, &t.Plot, &genres, &countries, &t.Finished, &checked)
	if err != nil {
		return catalog.Title{}, MapError(err)
	}
	t.Genres = scanStrings(genres)
	t.Countries = scanStrings(countries)
	t.SourcesCheckedAt = timeOf(checked)

	t.Sources, err = r.Sources(ctx, id)
	return t, err
}

// Sources — площадки тайтла, по убыванию приоритета.
func (r *CatalogRepo) Sources(ctx context.Context, id kernel.TitleID) ([]catalog.Source, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT provider_id, provider_ref, priority, priority_manual, season_no, disabled
		FROM external_refs WHERE title_id = ?`, id)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()

	var out []catalog.Source
	for rows.Next() {
		var s catalog.Source
		var manual, disabled int
		if err := rows.Scan(&s.Ref.ProviderID, &s.Ref.ItemID, &s.Priority, &manual,
			&s.SeasonNo, &disabled); err != nil {
			return nil, MapError(err)
		}
		s.Disabled = disabled != 0
		s.Priority = r.effectivePriority(s.Ref.ProviderID, s.Priority, manual != 0)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, MapError(err)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].SeasonNo < out[j].SeasonNo
	})
	return out, nil
}

// Offers — кто отдаёт серию и под какими номерами у себя.
func (r *CatalogRepo) Offers(ctx context.Context, ep kernel.EpisodeID) ([]catalog.Offer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.provider_id, o.provider_ref, o.season_no, o.episode_no, x.priority, x.priority_manual,
		       COALESCE(x.mal_id, '')
		FROM episode_offers o
		JOIN external_refs x ON x.provider_id = o.provider_id AND x.provider_ref = o.provider_ref
		JOIN canonical_episodes e ON e.id = o.episode_id AND e.title_id = x.title_id
		WHERE o.episode_id = ? AND x.disabled = 0`, ep)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()

	var out []catalog.Offer
	for rows.Next() {
		var o catalog.Offer
		var manual int
		if err := rows.Scan(&o.Ref.ProviderID, &o.Ref.ItemID, &o.SeasonNo, &o.EpisodeNo,
			&o.Priority, &manual, &o.MAL); err != nil {
			return nil, MapError(err)
		}
		o.Priority = r.effectivePriority(o.Ref.ProviderID, o.Priority, manual != 0)
		out = append(out, o)
	}
	return out, MapError(rows.Err())
}

// Episodes — серии тайтла по порядку.
func (r *CatalogRepo) Episodes(ctx context.Context, id kernel.TitleID) ([]catalog.Episode, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title_id, season_no, episode_no, COALESCE(duration_sec, 0),
		       COALESCE(thumbnail_url, ''), title
		FROM canonical_episodes WHERE title_id = ?
		ORDER BY season_no, episode_no`, id)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()

	var out []catalog.Episode
	for rows.Next() {
		var e catalog.Episode
		var title sql.NullString
		if err := rows.Scan(&e.ID, &e.TitleID, &e.SeasonNo, &e.EpisodeNo,
			&e.DurationSec, &e.Thumbnail, &title); err != nil {
			return nil, MapError(err)
		}
		e.Title, e.TitleKnown = derefTitle(title)
		out = append(out, e)
	}
	return out, MapError(rows.Err())
}

// Search — как в проде: сначала точное и по слову, потом «внутри».
// Триграммы движку не нужны: каталог в базе исчисляется десятками, а не
// десятками тысяч (ADR-0015), поэтому ступень «похоже на опечатку» здесь
// не ищется вовсе.
func (r *CatalogRepo) Search(ctx context.Context, q string, limit, offset int) ([]catalog.Title, int, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, 0, nil
	}
	key := catalog.NormalizeName(q)
	rows, err := r.db.QueryContext(ctx, `
		WITH named AS (
			SELECT title_id,
			       max(CASE WHEN name_key = ?2 THEN 1 ELSE 0 END) AS exact,
			       max(CASE WHEN name_key LIKE ?2 || '%' OR name_key LIKE '% ' || ?2 || '%' THEN 1 ELSE 0 END) AS word
			FROM title_names
			WHERE ?2 <> '' AND (name_key = ?2 OR (length(?2) >= 3 AND name_key LIKE '%' || ?2 || '%'))
			GROUP BY title_id
		), cand AS (
			SELECT title_id AS id FROM named
			UNION
			SELECT id FROM canonical_titles
			WHERE title_norm LIKE '%' || ?1 || '%' OR title_lower LIKE '%' || ?1 || '%'
		), hits AS (
			SELECT t.id, t.kind, t.title, COALESCE(t.year, 0) AS year, COALESCE(t.poster_url, '') AS poster,
			       t.popularity, t.source_created_at,
			       max(COALESCE(n.exact, 0), t.title_norm = ?1, t.title_lower = ?1) AS exact,
			       max(COALESCE(n.word, 0), t.title_lower LIKE ?1 || '%') AS word,
			       (n.title_id IS NOT NULL OR t.title_lower LIKE '%' || ?1 || '%') AS inside
			FROM cand c
			JOIN canonical_titles t ON t.id = c.id
			LEFT JOIN named n ON n.title_id = t.id
			GROUP BY t.id
		)
		SELECT id, kind, title, year, poster, count(*) OVER () AS total
		FROM hits
		ORDER BY
			CASE WHEN exact OR word THEN 0 WHEN inside THEN 1 ELSE 2 END,
			popularity * CASE WHEN exact THEN 3 ELSE 1 END DESC,
			exact DESC,
			source_created_at DESC,
			year DESC,
			id DESC
		LIMIT ?3 OFFSET ?4`, q, key, limit, offset)
	if err != nil {
		return nil, 0, MapError(err)
	}
	defer rows.Close()

	var out []catalog.Title
	total := 0
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
	// Пустая страница за концом не видит оконного счётчика — считаем отдельно.
	if len(out) == 0 && offset > 0 {
		err = r.db.QueryRowContext(ctx, `
			SELECT count(*) FROM canonical_titles
			WHERE title_norm LIKE '%' || ?1 || '%' OR title_lower LIKE '%' || ?1 || '%'`, q).Scan(&total)
		if err != nil {
			return nil, 0, MapError(err)
		}
	}
	return out, total, nil
}

// EpisodeIDByProviderRef переводит адрес серии у площадки в канонический id.
func (r *CatalogRepo) EpisodeIDByProviderRef(ctx context.Context,
	ref kernel.ProviderRef, season, episode int) (kernel.EpisodeID, error) {
	var id kernel.EpisodeID
	err := r.db.QueryRowContext(ctx, `
		SELECT o.episode_id
		FROM episode_offers o
		WHERE o.provider_id = ? AND o.provider_ref = ? AND o.season_no = ? AND o.episode_no = ?
		LIMIT 1`, ref.ProviderID, ref.ItemID, season, episode).Scan(&id)
	return id, MapError(err)
}

// TitleIDByProviderRef — канонический тайтл по адресу у площадки.
func (r *CatalogRepo) TitleIDByProviderRef(ctx context.Context, ref kernel.ProviderRef) (kernel.TitleID, error) {
	var id kernel.TitleID
	err := r.db.QueryRowContext(ctx, `
		SELECT title_id FROM external_refs
		WHERE provider_id = ? AND provider_ref = ?
		ORDER BY season_no LIMIT 1`, ref.ProviderID, ref.ItemID).Scan(&id)
	return id, MapError(err)
}

// UpsertTitle кладёт тайтл провайдера в каталог, склеивая по тем же правилам
// app/matching, что и в проде.
func (r *CatalogRepo) UpsertTitle(ctx context.Context, t dp.Title) (kernel.TitleID, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, MapError(err)
	}
	defer func() { _ = tx.Rollback() }()

	var id kernel.TitleID
	var season int
	by := "ref"
	err = tx.QueryRowContext(ctx, `
		SELECT title_id, season_no FROM external_refs WHERE provider_id = ? AND provider_ref = ?`,
		t.Ref.ProviderID, t.Ref.ItemID).Scan(&id, &season)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, MapError(err)
	}

	if !known {
		cands, err := r.candidates(ctx, tx, t)
		if err != nil {
			return 0, err
		}
		d := matching.DecideWith(r.opts, t, cands)
		id, season, by = d.TitleID, d.SeasonNo, d.By
	}

	if id == 0 {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO canonical_titles
				(kind, imdb_id, kp_id, title, title_norm, title_lower, title_original, year, poster_url, plot,
				 genres, countries, finished, source_created_at, source_updated_at, popularity)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			RETURNING id`,
			t.Kind, nullStr(t.IMDB), nullStr(t.Kinopoisk), t.Title, normalizeTitle(t.Title), strings.ToLower(t.Title),
			nullStr(t.Original), nullInt(t.Year), nullStr(t.Poster), nullStr(t.Plot),
			putStringsPtr(t.Genres), putStringsPtr(t.Countries), boolInt(t.Finished),
			msPtr(appearedAt(t)), msPtr(t.UpdatedAt), maxInt64(t.Popularity, 0)).Scan(&id); err != nil {
			return 0, fmt.Errorf("insert title: %w", MapError(err))
		}
	} else if err := r.refreshTitle(ctx, tx, id, t); err != nil {
		return 0, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO external_refs (provider_id, provider_ref, title_id, priority, season_no, mal_id, matched_by)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (provider_id, provider_ref) DO UPDATE SET
			title_id  = CASE WHEN external_refs.pinned THEN external_refs.title_id ELSE excluded.title_id END,
			season_no = CASE WHEN external_refs.pinned THEN external_refs.season_no ELSE excluded.season_no END,
			mal_id    = COALESCE(excluded.mal_id, external_refs.mal_id)`,
		t.Ref.ProviderID, t.Ref.ItemID, id, r.priorityOf(t.Ref.ProviderID), season,
		nullStr(t.MAL), by); err != nil {
		return 0, fmt.Errorf("upsert ref: %w", MapError(err))
	}
	if err := insertNameKeys(ctx, tx, id, append([]string{t.Title, t.Original}, t.AltTitles...)); err != nil {
		return 0, err
	}
	return id, MapError(tx.Commit())
}

// refreshTitle дополняет известный тайтл данными площадки: хозяин
// метаданных — площадка с наибольшим приоритетом (см. postgres-версию).
func (r *CatalogRepo) refreshTitle(ctx context.Context, tx *sql.Tx, id kernel.TitleID, t dp.Title) error {
	owner := true
	rows, err := tx.QueryContext(ctx, `
		SELECT provider_id, priority, priority_manual FROM external_refs
		WHERE title_id = ? AND disabled = 0 AND NOT (provider_id = ? AND provider_ref = ?)`,
		id, t.Ref.ProviderID, t.Ref.ItemID)
	if err != nil {
		return MapError(err)
	}
	mine := r.priorityOf(t.Ref.ProviderID)
	for rows.Next() {
		var p string
		var stored, manual int
		if err := rows.Scan(&p, &stored, &manual); err != nil {
			rows.Close()
			return MapError(err)
		}
		if r.effectivePriority(p, stored, manual != 0) > mine {
			owner = false
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MapError(err)
	}

	// imdb уникален: писать можно только свободный номер (см. freeIMDB в проде).
	if owner {
		_, err = tx.ExecContext(ctx, `
			UPDATE canonical_titles SET
				imdb_id    = COALESCE(imdb_id, ?2),
				kp_id      = COALESCE(kp_id,   ?3),
				poster_url = COALESCE(NULLIF(?4,''), poster_url),
				plot       = COALESCE(NULLIF(?5,''), plot),
				genres     = COALESCE(?6, genres),
				countries  = COALESCE(?7, countries),
				finished   = max(finished, ?8),
				source_created_at = min(COALESCE(source_created_at, ?9), COALESCE(?9, source_created_at)),
				source_updated_at = max(COALESCE(source_updated_at, ?10), COALESCE(?10, source_updated_at)),
				title_original    = COALESCE(NULLIF(?11,''), title_original),
				popularity        = max(popularity, ?12)
			WHERE id = ?1`,
			id, nullStr(t.IMDB), nullStr(t.Kinopoisk), t.Poster, t.Plot,
			putStringsPtr(t.Genres), putStringsPtr(t.Countries), boolInt(t.Finished),
			msPtr(appearedAt(t)), msPtr(t.UpdatedAt), t.Original, maxInt64(t.Popularity, 0))
		return MapError(err)
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE canonical_titles SET
			imdb_id    = COALESCE(imdb_id, ?2),
			kp_id      = COALESCE(kp_id,   ?3),
			poster_url = COALESCE(poster_url, NULLIF(?4,'')),
			plot       = COALESCE(plot, NULLIF(?5,'')),
			genres     = COALESCE(genres, ?6),
			countries  = COALESCE(countries, ?7),
			source_updated_at = max(COALESCE(source_updated_at, ?8), COALESCE(?8, source_updated_at)),
			title_original    = COALESCE(title_original, NULLIF(?9,'')),
			popularity        = max(popularity, ?10),
			source_created_at = min(COALESCE(source_created_at, ?11), COALESCE(?11, source_created_at))
		WHERE id = ?1`,
		id, nullStr(t.IMDB), nullStr(t.Kinopoisk), t.Poster, t.Plot,
		putStringsPtr(t.Genres), putStringsPtr(t.Countries),
		msPtr(t.UpdatedAt), t.Original, maxInt64(t.Popularity, 0), msPtr(appearedAt(t)))
	return MapError(err)
}

// candidates достаёт тайтлы, с которыми входящий может совпасть.
func (r *CatalogRepo) candidates(ctx context.Context, tx *sql.Tx, t dp.Title) ([]matching.Candidate, error) {
	keys := matching.Keys(append([]string{t.Title, t.Original}, t.AltTitles...)...)
	ph := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	// В проде идентификатор переиспользуется одним $N; у каждого `?` здесь
	// свой индекс, поэтому каждый идентификатор передаётся дважды.
	args := []any{t.IMDB, t.IMDB, t.Kinopoisk, t.Kinopoisk, t.MAL, t.MAL}
	for _, k := range keys {
		args = append(args, k)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT t.id, t.kind, COALESCE(t.year, 0), COALESCE(t.imdb_id,''), COALESCE(t.kp_id,''), genres
		FROM canonical_titles t
		WHERE (? <> '' AND t.imdb_id = ?)
		   OR (? <> '' AND t.kp_id = ?)
		   OR (? <> '' AND t.id IN (SELECT title_id FROM external_refs WHERE mal_id = ?))
		   OR t.id IN (SELECT title_id FROM title_names WHERE name_key IN (`+ph+`))
		LIMIT 50`, args...)

	if err != nil {
		return nil, MapError(err)
	}
	type row struct {
		c      matching.Candidate
		genres string
	}
	var out []matching.Candidate
	var ids []kernel.TitleID
	for rows.Next() {
		var c matching.Candidate
		var genres sql.NullString
		if err := rows.Scan(&c.ID, &c.Kind, &c.Year, &c.IMDB, &c.KP, &genres); err != nil {
			rows.Close()
			return nil, MapError(err)
		}
		g := scanStrings(genres)
		c.Anime = matching.IsAnime(g)
		c.Cartoon = matching.IsCartoon(g)
		out = append(out, c)
		ids = append(ids, c.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, MapError(err)
	}

	// Ключи названий и привязки кандидатов — пачкой, без запроса на строку.
	type keyRow struct {
		titleID kernel.TitleID
		key     string
	}
	keysByTitle := map[kernel.TitleID][]string{}
	if len(ids) > 0 {
		krows, err := tx.QueryContext(ctx, `
			SELECT title_id, name_key FROM title_names WHERE title_id IN (`+ph2(len(ids))+`)`,
			toAny(ids)...)
		if err != nil {
			return nil, MapError(err)
		}
		for krows.Next() {
			var kr keyRow
			if err := krows.Scan(&kr.titleID, &kr.key); err != nil {
				krows.Close()
				return nil, MapError(err)
			}
			keysByTitle[kr.titleID] = append(keysByTitle[kr.titleID], kr.key)
		}
		krows.Close()
		if err := krows.Err(); err != nil {
			return nil, MapError(err)
		}
	}
	type refRow struct {
		titleID kernel.TitleID
		ref     matching.Ref
	}
	refsByTitle := map[kernel.TitleID][]matching.Ref{}
	if len(ids) > 0 {
		rrows, err := tx.QueryContext(ctx, `
			SELECT title_id, provider_id, provider_ref, season_no, COALESCE(mal_id,''), pinned
			FROM external_refs WHERE title_id IN (`+ph2(len(ids))+`)`,
			toAny(ids)...)
		if err != nil {
			return nil, MapError(err)
		}
		for rrows.Next() {
			var rr refRow
			var pinned int
			if err := rrows.Scan(&rr.titleID, &rr.ref.ProviderID, &rr.ref.ItemID,
				&rr.ref.SeasonNo, &rr.ref.MAL, &pinned); err != nil {
				rrows.Close()
				return nil, MapError(err)
			}
			rr.ref.Pinned = pinned != 0
			refsByTitle[rr.titleID] = append(refsByTitle[rr.titleID], rr.ref)
		}
		rrows.Close()
		if err := rrows.Err(); err != nil {
			return nil, MapError(err)
		}
	}
	for i := range out {
		out[i].Keys = keysByTitle[out[i].ID]
		out[i].Refs = refsByTitle[out[i].ID]
		// Тайтл, у которого уже есть аниме-источник, — аниме, даже если у
		// хозяина метаданных жанр не проставлен.
		if !out[i].Anime {
			for _, ref := range out[i].Refs {
				if r.anime[ref.ProviderID] {
					out[i].Anime = true
				}
			}
		}
	}
	return out, nil
}

// insertNameKeys пишет ключи названий; нормализация — код (matching.Keys),
// не SQL: одна и та же нормализация при записи и при поиске.
func insertNameKeys(ctx context.Context, tx *sql.Tx, id kernel.TitleID, names []string) error {
	keys := matching.Keys(names...)
	if len(keys) == 0 {
		return nil
	}
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO title_names (title_id, name_key) VALUES (?, ?)
			ON CONFLICT (title_id, name_key) DO NOTHING`, id, k); err != nil {
			return MapError(err)
		}
	}
	return nil
}

// UpsertEpisodes синхронизирует серии тайтла с одной площадки: номер у
// площадки переводится в канонический один раз, пара хранится в offers.
func (r *CatalogRepo) UpsertEpisodes(ctx context.Context, titleID kernel.TitleID, eps []dp.Episode) error {
	if len(eps) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return MapError(err)
	}
	defer func() { _ = tx.Rollback() }()

	seasonOf := map[kernel.ProviderRef]int{}
	for _, e := range eps {
		if e.Ref.Zero() {
			continue
		}
		if _, ok := seasonOf[e.Ref]; ok {
			continue
		}
		var s int
		err := tx.QueryRowContext(ctx, `
			SELECT season_no FROM external_refs
			WHERE provider_id = ? AND provider_ref = ? AND title_id = ?`,
			e.Ref.ProviderID, e.Ref.ItemID, titleID).Scan(&s)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return MapError(err)
		}
		if errors.Is(err, sql.ErrNoRows) {
			s = -1 // площадка не привязана — её серии сюда класть нельзя
		}
		seasonOf[e.Ref] = s
	}

	// Лишние серии в закрытом сезоне: правило считает только не-аниме
	// площадки, и если их меньше двух — сравнивать не с кем.
	var layout seasonLayout
	needLayout := false
	for _, e := range eps {
		if !e.Ref.Zero() && !r.anime[e.Ref.ProviderID] {
			needLayout = true
			break
		}
	}
	if needLayout {
		if layout, err = r.layoutOf(ctx, tx, titleID); err != nil {
			return err
		}
	}

	for _, e := range eps {
		cs, ce := e.SeasonNo, e.EpisodeNo
		if !e.Ref.Zero() {
			switch m := seasonOf[e.Ref]; {
			case m < 0:
				continue
			case m > 0:
				cs = m
			}
			if layout.extra(e.Ref.ProviderID, cs, ce, r.anime) {
				var exists int
				if err := tx.QueryRowContext(ctx, `
					SELECT EXISTS (SELECT 1 FROM canonical_episodes
					               WHERE title_id = ? AND season_no = ? AND episode_no = ?)`,
					titleID, cs, ce).Scan(&exists); err != nil {
					return MapError(err)
				}
				if exists == 0 {
					continue
				}
			}
		}
		var epID kernel.EpisodeID
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO canonical_episodes
				(title_id, season_no, episode_no, duration_sec, thumbnail_url, title)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT (title_id, season_no, episode_no) DO UPDATE SET
				duration_sec  = CASE WHEN ?
					THEN COALESCE(canonical_episodes.duration_sec, excluded.duration_sec)
					ELSE COALESCE(excluded.duration_sec, canonical_episodes.duration_sec) END,
				thumbnail_url = COALESCE(excluded.thumbnail_url, canonical_episodes.thumbnail_url),
				title         = COALESCE(NULLIF(excluded.title, ''), canonical_episodes.title)
			RETURNING id`,
			titleID, cs, ce, nullInt(e.DurationSec), nullStr(e.Thumbnail),
			strings.TrimSpace(e.Title), boolInt(e.DurationApprox)).Scan(&epID); err != nil {
			return MapError(err)
		}
		if e.Ref.Zero() {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO episode_offers (episode_id, provider_id, provider_ref, season_no, episode_no, seen_at)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT (episode_id, provider_id, provider_ref) DO UPDATE SET
				season_no = excluded.season_no, episode_no = excluded.episode_no, seen_at = excluded.seen_at`,
			epID, e.Ref.ProviderID, e.Ref.ItemID, e.SeasonNo, e.EpisodeNo, nowMs()); err != nil {
			return MapError(err)
		}
	}
	if err := r.pruneExtras(ctx, tx, titleID); err != nil {
		return err
	}
	return MapError(tx.Commit())
}

// SetEpisodeThumbnail запоминает кадр серии; известный не затирается.
func (r *CatalogRepo) SetEpisodeThumbnail(ctx context.Context, id kernel.EpisodeID, url string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE canonical_episodes SET thumbnail_url = ? WHERE id = ? AND thumbnail_url IS NULL`, url, id)
	return MapError(err)
}

// OfferStats — сколько серий отдаёт каждая площадка и когда их перечитывали.
func (r *CatalogRepo) OfferStats(ctx context.Context, id kernel.TitleID) (map[kernel.ProviderRef]appcatalog.OfferStat, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.provider_id, o.provider_ref, count(*), max(o.seen_at)
		FROM episode_offers o
		JOIN canonical_episodes e ON e.id = o.episode_id
		JOIN external_refs x ON x.provider_id = o.provider_id AND x.provider_ref = o.provider_ref
		WHERE e.title_id = ? AND x.title_id = ?
		GROUP BY o.provider_id, o.provider_ref`, id, id)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	out := map[kernel.ProviderRef]appcatalog.OfferStat{}
	for rows.Next() {
		var ref kernel.ProviderRef
		var seen sql.NullInt64
		var st appcatalog.OfferStat
		if err := rows.Scan(&ref.ProviderID, &ref.ItemID, &st.Episodes, &seen); err != nil {
			return nil, MapError(err)
		}
		st.SeenAt = timeOf(seen)
		out[ref] = st
	}
	return out, MapError(rows.Err())
}

// MarkSourcesChecked запоминает, что площадки об этом тайтле спрашивали.
func (r *CatalogRepo) MarkSourcesChecked(ctx context.Context, id kernel.TitleID, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE canonical_titles SET sources_checked_at = ? WHERE id = ?`, ms(at), id)
	return MapError(err)
}

// ---- разделы каталога ----

// BrowsePageInternal — страница раздела; в httpuser её переводит клеевой
// тип bootstrap/engine (транспорт типизирован DTO postgres).
type BrowsePageInternal struct {
	Items []catalog.Title
	Total int
}

// Browse отдаёт раздел каталога — только то, что уже в базе (ADR-0015).
func (r *CatalogRepo) Browse(ctx context.Context, kind, genre, sort string, page, limit int) (BrowsePageInternal, error) {
	if limit <= 0 || limit > 120 {
		limit = 60
	}
	if page < 1 {
		page = 1
	}
	order := "source_created_at DESC, id DESC"
	switch sort {
	case "updated":
		order = "source_updated_at DESC, id DESC"
	case "popular":
		order = "popularity DESC, source_created_at DESC, id DESC"
	case "year":
		order = "year DESC, id DESC"
	case "title":
		order = "title_norm ASC"
	}
	genreCond := ""
	args := []any{kind, kind} // условие «(? = '' OR kind = ?)» берёт вид дважды
	if genre != "" {
		genreCond = " AND EXISTS (SELECT 1 FROM json_each(genres) WHERE value = ?)"
		args = append(args, genre)
	}

	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM canonical_titles WHERE (? = '' OR kind = ?)`+genreCond,
		args...).Scan(&total); err != nil {
		return BrowsePageInternal{}, MapError(err)
	}
	selectArgs := append(append([]any{}, args...), limit, (page-1)*limit)
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, kind, title, COALESCE(year,0), COALESCE(poster_url,'')
		FROM canonical_titles WHERE (? = '' OR kind = ?)`+genreCond+`
		ORDER BY `+order+`
		LIMIT ? OFFSET ?`, selectArgs...)
	if err != nil {
		return BrowsePageInternal{}, MapError(err)
	}
	defer rows.Close()

	out := BrowsePageInternal{Total: total}
	for rows.Next() {
		var t catalog.Title
		if err := rows.Scan(&t.ID, &t.Kind, &t.Title, &t.Year, &t.Poster); err != nil {
			return BrowsePageInternal{}, MapError(err)
		}
		out.Items = append(out.Items, t)
	}
	return out, MapError(rows.Err())
}

// Genres — жанры каталога с числом тайтлов.
func (r *CatalogRepo) Genres(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT j.value, count(*) AS n
		FROM canonical_titles, json_each(genres) AS j
		GROUP BY j.value
		HAVING count(*) >= 20
		ORDER BY n DESC`)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var g string
		var n int
		if err := rows.Scan(&g, &n); err != nil {
			return nil, MapError(err)
		}
		out = append(out, g)
	}
	return out, MapError(rows.Err())
}

// ---- лишние серии закрытого сезона (порт episode_extras.go) ----

type seasonLayout struct {
	prio map[string]int
	tail map[string]map[int]int
	last map[string]int
}

func (l seasonLayout) extra(p string, season, ep int, anime map[string]bool) bool {
	if anime[p] {
		return false
	}
	for q, seasons := range l.tail {
		if q == p || anime[q] || l.prio[q] <= l.prio[p] {
			continue
		}
		end, ok := seasons[season]
		if ok && ep > end && l.last[q] > season {
			return true
		}
	}
	return false
}

func (l seasonLayout) tvProviders(anime map[string]bool) int {
	n := 0
	for p := range l.tail {
		if !anime[p] {
			n++
		}
	}
	return n
}

func (r *CatalogRepo) layoutOf(ctx context.Context, tx *sql.Tx, titleID kernel.TitleID) (seasonLayout, error) {
	l := seasonLayout{prio: map[string]int{}, tail: map[string]map[int]int{}, last: map[string]int{}}
	rows, err := tx.QueryContext(ctx, `
		SELECT provider_id, priority, priority_manual FROM external_refs
		WHERE title_id = ? AND disabled = 0`, titleID)
	if err != nil {
		return l, MapError(err)
	}
	for rows.Next() {
		var p string
		var stored, manual int
		if err := rows.Scan(&p, &stored, &manual); err != nil {
			rows.Close()
			return l, MapError(err)
		}
		v := r.effectivePriority(p, stored, manual != 0)
		if cur, ok := l.prio[p]; !ok || v > cur {
			l.prio[p] = v
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return l, MapError(err)
	}

	rows, err = tx.QueryContext(ctx, `
		SELECT o.provider_id, e.season_no, max(e.episode_no)
		FROM episode_offers o
		JOIN canonical_episodes e ON e.id = o.episode_id
		JOIN external_refs x ON x.provider_id = o.provider_id AND x.provider_ref = o.provider_ref
		WHERE e.title_id = ? AND x.title_id = ? AND x.disabled = 0
		GROUP BY 1, 2`, titleID, titleID)
	if err != nil {
		return l, MapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var s, end int
		if err := rows.Scan(&p, &s, &end); err != nil {
			return l, MapError(err)
		}
		if l.tail[p] == nil {
			l.tail[p] = map[int]int{}
		}
		l.tail[p][s] = end
		if s > l.last[p] {
			l.last[p] = s
		}
	}
	return l, MapError(rows.Err())
}

// pruneExtras убирает серии, которые предлагают только площадки, у которых
// эта серия лишняя. У серии с прогрессом или ссылкой остаётся: удаление
// стёрло бы чужую историю.
func (r *CatalogRepo) pruneExtras(ctx context.Context, tx *sql.Tx, titleID kernel.TitleID) error {
	l, err := r.layoutOf(ctx, tx, titleID)
	if err != nil {
		return err
	}
	if l.tvProviders(r.anime) < 2 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT e.id, e.season_no, e.episode_no, group_concat(DISTINCT o.provider_id)
		FROM canonical_episodes e
		JOIN episode_offers o ON o.episode_id = e.id
		WHERE e.title_id = ?
		  AND NOT EXISTS (SELECT 1 FROM watch_progress w WHERE w.episode_id = e.id)
		  AND NOT EXISTS (SELECT 1 FROM share_links s WHERE s.episode_id = e.id)
		GROUP BY e.id`, titleID)
	if err != nil {
		return MapError(err)
	}
	var drop []kernel.EpisodeID
	for rows.Next() {
		var id kernel.EpisodeID
		var s, ep int
		var provs string
		if err := rows.Scan(&id, &s, &ep, &provs); err != nil {
			rows.Close()
			return MapError(err)
		}
		all := provs != ""
		for _, p := range strings.Split(provs, ",") {
			if !l.extra(strings.TrimSpace(p), s, ep, r.anime) {
				all = false
				break
			}
		}
		if all {
			drop = append(drop, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MapError(err)
	}
	for _, id := range drop {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM canonical_episodes
			WHERE id = ?
			  AND NOT EXISTS (SELECT 1 FROM watch_progress w WHERE w.episode_id = canonical_episodes.id)
			  AND NOT EXISTS (SELECT 1 FROM share_links s WHERE s.episode_id = canonical_episodes.id)`, id); err != nil {
			return MapError(err)
		}
	}
	return nil
}

// ---- помощники ----

func appearedAt(t dp.Title) time.Time {
	if !t.CreatedAt.IsZero() {
		return t.CreatedAt
	}
	return t.UpdatedAt
}

func normalizeTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func ph2(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func toAny[T any](xs []T) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}
