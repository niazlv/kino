package postgres

// Поиск и лечение развалившейся склейки. Правила — в app/matching, здесь —
// только данные: пары-кандидаты, полные данные тайтла для решения и слияние.

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/app/catalog"
	"github.com/niazleushkin/kino/internal/app/matching"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// scanStringsPool — scanStrings для пула, а не транзакции (см. catalog_write).
func scanStringsPool(ctx context.Context, db *pgxpool.Pool, dst *[]string, sql string, args ...any) error {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return pg.MapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return pg.MapError(err)
		}
		*dst = append(*dst, s)
	}
	return pg.MapError(rows.Err())
}

// BackfillVariantKeys дописывает варианты ключей (matching.Variants) к уже
// сохранённым точным ключам.
//
// Варианты появились позже самих ключей, и без этого прохода старые тайтлы
// различали бы только точную форму. Пары «база — вариант» считаются в коде
// (нормализация одна и не повторяется в SQL), а вставка — одним запросом
// на пачку.
func (r *CatalogRepo) BackfillVariantKeys(ctx context.Context) (int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT name_key FROM title_names
		WHERE name_key NOT LIKE 'f:%' AND name_key NOT LIKE 's:%'`)
	if err != nil {
		return 0, pg.MapError(err)
	}
	var bases []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return 0, pg.MapError(err)
		}
		bases = append(bases, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, pg.MapError(err)
	}

	total := 0
	const batch = 500
	for start := 0; start < len(bases); start += batch {
		end := start + batch
		if end > len(bases) {
			end = len(bases)
		}
		var args []any
		for _, k := range bases[start:end] {
			for _, v := range matching.Variants(k) {
				args = append(args, k, v)
			}
		}
		if len(args) == 0 {
			continue
		}
		values := placeholders(len(args) / 2)
		tag, err := r.db.Exec(ctx, `
			INSERT INTO title_names (title_id, name_key)
			SELECT n.title_id, v.variant FROM (VALUES `+values+`) AS v(base, variant)
			JOIN title_names n ON n.name_key = v.base
			ON CONFLICT DO NOTHING`, args...)
		if err != nil {
			return total, pg.MapError(err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

// placeholders: «($1,$2),($3,$4)...» для пар VALUES.
func placeholders(pairs int) string {
	s := ""
	for i := 0; i < pairs; i++ {
		if i > 0 {
			s += ","
		}
		a, b := i*2+1, i*2+2
		s += "($" + itoa(a) + ",$" + itoa(b) + ")"
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// DuplicatePairs достаёт пары-кандидаты на слияние. Тип пары —
// app/catalog.DuplicatePair: интерфейс реконсилиатора живёт там.
//
// Ключ, общий у десятков тайтлов, — не признак одного произведения, а
// мусорный (концертные серии kinopub: «kexp», «tiny desk» делят один ключ
// на полсотни записей, и одни только они дали бы тысячи пар). Такие ключи
// из пар исключаются: решающим остаётся ключ, который и должен узнавать
// произведение, — редкий.
//
// Вид и годы (±30, самое широкое окно правил) отсекаются в SQL: пара
// «кино 1940 против сериала 2026» не дойдёт до загрузки полных данных.
//
// Разваливается склейка почти всегда на аниме-площадках (ромадзи,
// переводы), поэтому пары с их привязками идут первыми. Дальше — по
// убыванию приоритета площадок: первая сторона пары — вероятный хозяин
// метаданных (у kinopub постер и описание лучше), и слияние «в него»
// сохраняет витрину.
//
// Лимит — запасной клапан объёма, а не окно: реконсилиатор должен увидеть
// все пары за проход, иначе отказанные в начале очереди навсегда закрывали
// бы от него тех, кто дальше.
func (r *CatalogRepo) DuplicatePairs(ctx context.Context, limit int) ([]catalog.DuplicatePair, error) {
	var anime []string
	for id := range r.animeProviders {
		anime = append(anime, id)
	}
	rows, err := r.db.Query(ctx, `
		WITH rare AS (
			SELECT name_key FROM title_names GROUP BY name_key HAVING count(*) < 8
		),
		mal AS (
			SELECT a.title_id AS a, b.title_id AS b
			FROM external_refs a
			JOIN external_refs b ON b.mal_id = a.mal_id AND b.title_id > a.title_id
			WHERE a.mal_id <> ''
		),
		named AS (
			SELECT a.title_id AS a, b.title_id AS b
			FROM title_names a
			JOIN title_names b ON b.name_key = a.name_key AND b.title_id > a.title_id
			WHERE a.name_key IN (SELECT name_key FROM rare)
		),
		pairs AS (SELECT a, b FROM mal UNION SELECT a, b FROM named)
		SELECT p.a, p.b FROM pairs p
		JOIN canonical_titles t1 ON t1.id = p.a
		JOIN canonical_titles t2 ON t2.id = p.b
		WHERE t1.kind = t2.kind
		  AND t1.year IS NOT NULL AND t2.year IS NOT NULL
		  AND abs(t1.year - t2.year) <= 30
		ORDER BY
			(EXISTS (SELECT 1 FROM external_refs x
			         WHERE x.title_id IN (p.a, p.b) AND x.provider_id = ANY($2))) DESC,
			(SELECT max(x.priority) FROM external_refs x WHERE x.title_id = p.a) DESC,
			(SELECT max(x.priority) FROM external_refs x WHERE x.title_id = p.b) DESC,
			p.a, p.b
		LIMIT $1`, limit, anime)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []catalog.DuplicatePair
	for rows.Next() {
		var p catalog.DuplicatePair
		if err := rows.Scan(&p.A, &p.B); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, p)
	}
	return out, pg.MapError(rows.Err())
}

// Candidate — данные одного тайтла для matching.ShouldMerge.
func (r *CatalogRepo) Candidate(ctx context.Context, id kernel.TitleID) (matching.Candidate, error) {
	var c matching.Candidate
	var genres []string
	err := r.db.QueryRow(ctx, `
		SELECT id, kind, COALESCE(year, 0), COALESCE(imdb_id,''), COALESCE(kp_id,''), COALESCE(genres,'{}')
		FROM canonical_titles WHERE id = $1`, id).
		Scan(&c.ID, &c.Kind, &c.Year, &c.IMDB, &c.KP, &genres)
	if err != nil {
		return c, pg.MapError(err)
	}
	c.Anime = matching.IsAnime(genres)
	c.Cartoon = matching.IsCartoon(genres)
	if err := scanStringsPool(ctx, r.db, &c.Keys,
		`SELECT name_key FROM title_names WHERE title_id = $1`, id); err != nil {
		return c, err
	}
	refs, err := r.db.Query(ctx, `
		SELECT provider_id, provider_ref, season_no, COALESCE(mal_id,''), pinned
		FROM external_refs WHERE title_id = $1 AND NOT disabled`, id)
	if err != nil {
		return c, pg.MapError(err)
	}
	for refs.Next() {
		var ref matching.Ref
		if err := refs.Scan(&ref.ProviderID, &ref.ItemID, &ref.SeasonNo, &ref.MAL, &ref.Pinned); err != nil {
			refs.Close()
			return c, pg.MapError(err)
		}
		c.Refs = append(c.Refs, ref)
		if r.animeProviders[ref.ProviderID] {
			c.Anime = true
		}
	}
	refs.Close()
	return c, pg.MapError(refs.Err())
}

// RefsWithoutMAL — привязки аниме-площадки, у которых номер MAL не
// сохранился. Обход пишет его только тогда, когда плеер площадки ответил;
// здесь добираем остальное.
func (r *CatalogRepo) RefsWithoutMAL(ctx context.Context, providerID string, limit int) ([]kernel.ProviderRef, error) {
	rows, err := r.db.Query(ctx, `
		SELECT provider_id, provider_ref FROM external_refs
		WHERE provider_id = $1 AND mal_id IS NULL
		ORDER BY title_id
		LIMIT $2`, providerID, limit)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []kernel.ProviderRef
	for rows.Next() {
		var ref kernel.ProviderRef
		if err := rows.Scan(&ref.ProviderID, &ref.ItemID); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, ref)
	}
	return out, pg.MapError(rows.Err())
}

// SetRefMAL сохраняет номер MAL, добытый повторным запросом. Уже известный
// не затирается: у обхода и у ручной правки приоритет.
func (r *CatalogRepo) SetRefMAL(ctx context.Context, ref kernel.ProviderRef, mal string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE external_refs SET mal_id = $3
		WHERE provider_id = $1 AND provider_ref = $2 AND mal_id IS NULL`,
		ref.ProviderID, ref.ItemID, mal)
	return pg.MapError(err)
}

// AnimeTitlesWithoutMAL — тайтлы площадки, чьим привязкам не хватает номера
// MAL и чей жанр — аниме: мостить через реестр имеет смысл только их.
//
// Многосезонные тайтлы площадки не мостятся: реестр даёт номер на ОДИН
// сезон, и наклеить его на привязки всех сезонов — перепутать сезоны между
// собой. Таких у площадки мало, а ошибочный номер выглядел бы правдой.
func (r *CatalogRepo) AnimeTitlesWithoutMAL(ctx context.Context,
	providerID string, limit int) ([]catalog.MALTitle, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.title, COALESCE(t.title_original,''), COALESCE(t.year,0), t.kind
		FROM canonical_titles t
		JOIN external_refs x ON x.title_id = t.id AND x.provider_id = $1
			AND x.mal_id IS NULL AND NOT x.disabled
		WHERE EXISTS (SELECT 1 FROM unnest(t.genres) g
		              WHERE lower(trim(g)) IN ('аниме', 'anime'))
		  AND NOT EXISTS (SELECT 1 FROM external_refs y
		                  WHERE y.title_id = t.id AND y.provider_id = $1
		                    AND y.season_no > 1)
		ORDER BY t.id
		LIMIT $2`, providerID, limit)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []catalog.MALTitle
	for rows.Next() {
		var t catalog.MALTitle
		if err := rows.Scan(&t.TitleID, &t.Title, &t.Original, &t.Year, &t.Kind); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, t)
	}
	return out, pg.MapError(rows.Err())
}

// SetTitleRefsMAL пишет номер MAL всем привязкам тайтла без номера: мост
// узнал произведение целиком, а не один его элемент.
func (r *CatalogRepo) SetTitleRefsMAL(ctx context.Context,
	titleID kernel.TitleID, providerID, mal string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE external_refs SET mal_id = $3
		WHERE title_id = $1 AND provider_id = $2 AND mal_id IS NULL`,
		titleID, providerID, mal)
	return pg.MapError(err)
}
