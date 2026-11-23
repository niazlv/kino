package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/niazleushkin/kino/internal/app/matching"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// defaultPriority — приоритет площадки, не названной в конфиге. Совпадает
// с тем, что стояло у всех привязок до появления второй площадки.
const defaultPriority = 10

// WithPriorities задаёт приоритет площадок по умолчанию.
//
// Приоритет читается из конфига при каждом чтении, а не записывается в
// привязку: иначе смена порядка площадок действовала бы только на тайтлы,
// которые обход успел перечитать. Записывается он лишь тогда, когда его
// поставил человек для конкретного тайтла (priority_manual).
func (r *CatalogRepo) WithPriorities(p map[string]int) *CatalogRepo {
	r.prio = p
	return r
}

func (r *CatalogRepo) priorityOf(provider string) int {
	if p, ok := r.prio[provider]; ok {
		return p
	}
	return defaultPriority
}

// effectivePriority — ручной приоритет, если он есть, иначе из конфига.
func (r *CatalogRepo) effectivePriority(provider string, stored int, manual bool) int {
	if manual {
		return stored
	}
	return r.priorityOf(provider)
}

// UpsertTitle кладёт тайтл провайдера в каталог, склеивая его с уже
// известным произведением.
//
// Сначала — уже известная привязка (provider_id, provider_ref): самый
// точный ключ, и решение, однажды принятое (или поправленное человеком),
// не пересматривается на каждом обходе. Иначе — кандидаты по внешним
// идентификаторам, номеру MAL и ключам названий, а решение принимает
// app/matching: правила склейки проверяются там тестами без базы.
//
// Ошибка склейки дорогая в обе стороны: дубль раскалывает историю
// просмотров, ложная склейка играет серии чужого произведения.
func (r *CatalogRepo) UpsertTitle(ctx context.Context, t dp.Title) (kernel.TitleID, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, pg.MapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id kernel.TitleID
	var season int
	by := "ref"
	err = tx.QueryRow(ctx, `
		SELECT title_id, season_no FROM external_refs WHERE provider_id = $1 AND provider_ref = $2`,
		t.Ref.ProviderID, t.Ref.ItemID).Scan(&id, &season)
	known := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, pg.MapError(err)
	}

	if !known {
		cands, err := r.candidates(ctx, tx, t)
		if err != nil {
			return 0, err
		}
		d := matching.DecideWith(r.matchOpts, t, cands)
		id, season, by = d.TitleID, d.SeasonNo, d.By
	}

	if id == 0 {
		if err := tx.QueryRow(ctx, `
			INSERT INTO canonical_titles
				(kind, imdb_id, kp_id, title, title_norm, title_original, year, poster_url, plot, genres, countries,
				 finished, source_created_at, source_updated_at, popularity)
			VALUES ($1, NULLIF($2,''), NULLIF($3,''), $4, $5, NULLIF($6,''), NULLIF($7,0), NULLIF($8,''),
			        NULLIF($9,''), $10, NULLIF($11,'{}'::text[]), $12, $13, $14, GREATEST($15::bigint, 0))
			RETURNING id`,
			t.Kind, t.IMDB, t.Kinopoisk, t.Title, normalizeTitle(t.Title), t.Original, t.Year,
			t.Poster, t.Plot, t.Genres, t.Countries, t.Finished,
			nullTime(appearedAt(t)), nullTime(t.UpdatedAt), t.Popularity).Scan(&id); err != nil {
			return 0, pg.MapError(err)
		}
	} else if err := r.refreshTitle(ctx, tx, id, t); err != nil {
		return 0, err
	}

	// pinned-привязку поставил человек: обход не двигает её ни к другому
	// тайтлу, ни к другому сезону.
	if _, err := tx.Exec(ctx, `
		INSERT INTO external_refs (provider_id, provider_ref, title_id, priority, season_no, mal_id, matched_by)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6,''), $7)
		ON CONFLICT (provider_id, provider_ref) DO UPDATE SET
			title_id  = CASE WHEN external_refs.pinned THEN external_refs.title_id ELSE EXCLUDED.title_id END,
			season_no = CASE WHEN external_refs.pinned THEN external_refs.season_no ELSE EXCLUDED.season_no END,
			mal_id    = COALESCE(EXCLUDED.mal_id, external_refs.mal_id)`,
		t.Ref.ProviderID, t.Ref.ItemID, id, r.priorityOf(t.Ref.ProviderID), season, t.MAL, by); err != nil {
		return 0, pg.MapError(err)
	}
	if err := insertNameKeys(ctx, tx, id, append([]string{t.Title, t.Original}, t.AltTitles...)); err != nil {
		return 0, err
	}
	return id, pg.MapError(tx.Commit(ctx))
}

// refreshTitle дополняет известный тайтл данными площадки.
//
// Хозяин метаданных — площадка с наибольшим приоритетом: у kinopub постер
// и описание лучше, и аниме-площадка, дополнившая его сериями, не должна
// подменять их своими. Площадка ниже по приоритету только заполняет пустое.
//
// Популярность — не метаданные хозяина: шкала у площадок общая (число
// людей), и берётся наибольшая из известных. Каждая площадка недосчитывает,
// а сериал, у которого сезоны лежат отдельными элементами, иначе получал бы
// число последнего обойдённого сезона.
//
// Даты — тоже не хозяина. «Новинки» и «Свежие серии» — про произведение, а
// не про площадку: появилось оно тогда, когда его впервые выложил хоть
// кто-то (наименьшая дата), обновилось — когда его последний раз обновил
// хоть кто-то (наибольшая). Пока даты брались у хозяина, разделы держались
// на одном kinopub и с его уходом встали бы.
func (r *CatalogRepo) refreshTitle(ctx context.Context, tx pgx.Tx, id kernel.TitleID, t dp.Title) error {
	owner := true
	rows, err := tx.Query(ctx, `
		SELECT provider_id, priority, priority_manual FROM external_refs
		WHERE title_id = $1 AND NOT disabled AND NOT (provider_id = $2 AND provider_ref = $3)`,
		id, t.Ref.ProviderID, t.Ref.ItemID)
	if err != nil {
		return pg.MapError(err)
	}
	mine := r.priorityOf(t.Ref.ProviderID)
	for rows.Next() {
		var p string
		var stored int
		var manual bool
		if err := rows.Scan(&p, &stored, &manual); err != nil {
			rows.Close()
			return pg.MapError(err)
		}
		if r.effectivePriority(p, stored, manual) > mine {
			owner = false
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return pg.MapError(err)
	}

	if owner {
		// Пустое значение от одного источника не должно стирать то, что
		// дал другой, поэтому COALESCE, а не прямое присваивание.
		_, err = tx.Exec(ctx, `
			UPDATE canonical_titles SET
				imdb_id    = COALESCE(imdb_id, `+freeIMDB+`),
				kp_id      = COALESCE(kp_id,   NULLIF($3,'')),
				poster_url = COALESCE(NULLIF($4,''), poster_url),
				plot       = COALESCE(NULLIF($5,''), plot),
				genres     = COALESCE(NULLIF($6, '{}'::text[]), genres),
				countries  = COALESCE(NULLIF($12, '{}'::text[]), countries),
				finished   = $7 OR finished,
				source_created_at = LEAST(source_created_at, $8),
				source_updated_at = GREATEST(source_updated_at, $9),
				title_original    = COALESCE(NULLIF($10,''), title_original),
				popularity        = GREATEST(popularity, $11::bigint)
			WHERE id = $1`, id, t.IMDB, t.Kinopoisk, t.Poster, t.Plot, t.Genres, t.Finished,
			nullTime(appearedAt(t)), nullTime(t.UpdatedAt), t.Original, t.Popularity, t.Countries)
		return pg.MapError(err)
	}
	// Не хозяин: только пустое. Новый сезон на аниме-площадке — это ещё и
	// свежее обновление тайтла, поэтому source_updated_at берём наибольший.
	_, err = tx.Exec(ctx, `
		UPDATE canonical_titles SET
			imdb_id    = COALESCE(imdb_id, `+freeIMDB+`),
			kp_id      = COALESCE(kp_id,   NULLIF($3,'')),
			poster_url = COALESCE(poster_url, NULLIF($4,'')),
			plot       = COALESCE(plot, NULLIF($5,'')),
			genres     = COALESCE(genres, NULLIF($6, '{}'::text[])),
			countries  = COALESCE(countries, NULLIF($10, '{}'::text[])),
			source_updated_at = GREATEST(source_updated_at, $7),
			title_original    = COALESCE(title_original, NULLIF($8,'')),
			popularity        = GREATEST(popularity, $9::bigint),
			source_created_at = LEAST(source_created_at, $11)
		WHERE id = $1`, id, t.IMDB, t.Kinopoisk, t.Poster, t.Plot, t.Genres,
		nullTime(t.UpdatedAt), t.Original, t.Popularity, t.Countries, nullTime(appearedAt(t)))
	return pg.MapError(err)
}

// appearedAt — не позже какого момента произведение уже было у площадки:
// дата появления, а без неё — дата обновления (обновить можно только то,
// что уже выложено). Площадки без дат (часть аниме-сайтов) в «Новинки»
// своих тайтлов не приносят — лучше, чем записать в новинки весь их
// каталог при первом обходе.
func appearedAt(t dp.Title) time.Time {
	if !t.CreatedAt.IsZero() {
		return t.CreatedAt
	}
	return t.UpdatedAt
}

// freeIMDB — imdb площадки, если он ещё не занят другим тайтлом.
//
// imdb уникален в каталоге. Когда склейка прошла по названию, а у другого
// тайтла уже стоит этот imdb, прямое присваивание уронило бы запись целиком —
// и элемент площадки не попал бы в каталог вовсе. Такой случай — повод для
// админки, а не для отказа обхода.
const freeIMDB = `(SELECT NULLIF($2,'') WHERE NOT EXISTS (SELECT 1 FROM canonical_titles WHERE imdb_id = NULLIF($2,'')))`

// candidates достаёт тайтлы, с которыми входящий может совпасть: по imdb,
// kinopoisk, номеру MAL и ключам названий. Решение — не здесь.
func (r *CatalogRepo) candidates(ctx context.Context, tx pgx.Tx, t dp.Title) ([]matching.Candidate, error) {
	keys := matching.Keys(append([]string{t.Title, t.Original}, t.AltTitles...)...)
	rows, err := tx.Query(ctx, `
		SELECT t.id, t.kind, COALESCE(t.year, 0), COALESCE(t.imdb_id,''), COALESCE(t.kp_id,''),
		       COALESCE(t.genres, '{}')
		FROM canonical_titles t
		WHERE ($1 <> '' AND t.imdb_id = $1)
		   OR ($2 <> '' AND t.kp_id = $2)
		   OR ($3 <> '' AND t.id IN (SELECT title_id FROM external_refs WHERE mal_id = $3))
		   OR t.id IN (SELECT title_id FROM title_names WHERE name_key = ANY($4))
		LIMIT 50`, t.IMDB, t.Kinopoisk, t.MAL, keys)
	if err != nil {
		return nil, pg.MapError(err)
	}
	var out []matching.Candidate
	for rows.Next() {
		var c matching.Candidate
		var genres []string
		if err := rows.Scan(&c.ID, &c.Kind, &c.Year, &c.IMDB, &c.KP, &genres); err != nil {
			rows.Close()
			return nil, pg.MapError(err)
		}
		c.Anime = matching.IsAnime(genres)
		c.Cartoon = matching.IsCartoon(genres)
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, pg.MapError(err)
	}

	for i := range out {
		c := &out[i]
		if err := scanStrings(ctx, tx, &c.Keys,
			`SELECT name_key FROM title_names WHERE title_id = $1`, c.ID); err != nil {
			return nil, err
		}
		refs, err := tx.Query(ctx, `
			SELECT provider_id, provider_ref, season_no, COALESCE(mal_id,'')
			FROM external_refs WHERE title_id = $1`, c.ID)
		if err != nil {
			return nil, pg.MapError(err)
		}
		for refs.Next() {
			var ref matching.Ref
			if err := refs.Scan(&ref.ProviderID, &ref.ItemID, &ref.SeasonNo, &ref.MAL); err != nil {
				refs.Close()
				return nil, pg.MapError(err)
			}
			c.Refs = append(c.Refs, ref)
		}
		refs.Close()
		if err := refs.Err(); err != nil {
			return nil, pg.MapError(err)
		}
		// Тайтл, у которого уже есть аниме-источник, — аниме, даже если у
		// хозяина метаданных жанр не проставлен.
		if !c.Anime {
			for _, ref := range c.Refs {
				if r.animeProviders[ref.ProviderID] {
					c.Anime = true
				}
			}
		}
	}
	return out, nil
}

// WithCartoonMatching включает склейку аниме с «Мультфильмом» kinopub для
// новых элементов (matching.Options.CrossCartoon). Включается отдельно:
// правило новое, а склейку уже заведённых решает реконсилиатор.
func (r *CatalogRepo) WithCartoonMatching(on bool) *CatalogRepo {
	r.matchOpts.CrossCartoon = on
	return r
}

// WithAnimeProviders называет площадки, у которых весь каталог — аниме.
func (r *CatalogRepo) WithAnimeProviders(ids ...string) *CatalogRepo {
	if r.animeProviders == nil {
		r.animeProviders = map[string]bool{}
	}
	for _, id := range ids {
		r.animeProviders[id] = true
	}
	return r
}

func scanStrings(ctx context.Context, tx pgx.Tx, dst *[]string, sql string, args ...any) error {
	rows, err := tx.Query(ctx, sql, args...)
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

func insertNameKeys(ctx context.Context, tx pgx.Tx, id kernel.TitleID, names []string) error {
	keys := matching.Keys(names...)
	if len(keys) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO title_names (title_id, name_key)
		SELECT $1, k FROM unnest($2::text[]) AS k
		ON CONFLICT DO NOTHING`, id, keys)
	return pg.MapError(err)
}

// BackfillNameKeys строит ключи названий тайтлам, у которых их нет.
//
// Ключи строит код, а не миграция: нормализация одна, и её нельзя
// повторить в SQL без риска разойтись. Тайтлов десятки тысяч, поэтому
// пачками; повторный запуск ничего не делает.
func (r *CatalogRepo) BackfillNameKeys(ctx context.Context) (int, error) {
	total := 0
	for {
		rows, err := r.db.Query(ctx, `
			SELECT t.id, t.title, COALESCE(t.title_original, '')
			FROM canonical_titles t
			WHERE NOT EXISTS (SELECT 1 FROM title_names n WHERE n.title_id = t.id)
			ORDER BY t.id
			LIMIT 1000`)
		if err != nil {
			return total, pg.MapError(err)
		}
		type row struct {
			id              kernel.TitleID
			title, original string
		}
		var batch []row
		for rows.Next() {
			var x row
			if err := rows.Scan(&x.id, &x.title, &x.original); err != nil {
				rows.Close()
				return total, pg.MapError(err)
			}
			batch = append(batch, x)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, pg.MapError(err)
		}
		if len(batch) == 0 {
			return total, nil
		}
		progressed := false
		for _, x := range batch {
			keys := matching.Keys(x.title, x.original)
			if len(keys) == 0 {
				// Название из одних коротких слов: ключа не будет никогда, и
				// без заглушки этот тайтл выбирался бы на каждой итерации.
				keys = []string{"#" + fmt.Sprint(x.id)}
			}
			if _, err := r.db.Exec(ctx, `
				INSERT INTO title_names (title_id, name_key)
				SELECT $1, k FROM unnest($2::text[]) AS k
				ON CONFLICT DO NOTHING`, x.id, keys); err != nil {
				return total, pg.MapError(err)
			}
			progressed = true
			total++
		}
		if !progressed {
			return total, nil
		}
	}
}

// UpsertEpisodes синхронизирует серии тайтла, пришедшие с одной площадки.
//
// Номер серии у площадки переводится в канонический здесь, один раз: если
// элемент площадки покрывает только сезон N (так устроены аниме-площадки),
// его серии ложатся в сезон N канонического тайтла. Пара «канон — номер у
// площадки» сохраняется в episode_offers, и резолв потом спрашивает
// площадку её же номерами.
func (r *CatalogRepo) UpsertEpisodes(ctx context.Context, titleID kernel.TitleID, eps []dp.Episode) error {
	if len(eps) == 0 {
		return nil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return pg.MapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	seasonOf := map[kernel.ProviderRef]int{}
	for _, e := range eps {
		if e.Ref.Zero() {
			continue
		}
		if _, ok := seasonOf[e.Ref]; ok {
			continue
		}
		var s int
		err := tx.QueryRow(ctx, `
			SELECT season_no FROM external_refs
			WHERE provider_id = $1 AND provider_ref = $2 AND title_id = $3`,
			e.Ref.ProviderID, e.Ref.ItemID, titleID).Scan(&s)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return pg.MapError(err)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// Площадка не привязана к этому тайтлу (отвязали, пока серии
			// ехали) — её серии сюда класть нельзя.
			s = -1
		}
		seasonOf[e.Ref] = s
	}

	// Раскладка сезонов по площадкам — только если в пачке есть площадка,
	// к которой относится правило о лишних сериях (episode_extras.go).
	var layout seasonLayout
	for _, e := range eps {
		if !e.Ref.Zero() && !r.animeProviders[e.Ref.ProviderID] {
			if layout, err = r.layoutOf(ctx, tx, titleID); err != nil {
				return err
			}
			break
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
			// Лишняя серия закрытого сезона не заводится. Уже заведённая
			// (с чьим-то прогрессом) продолжает получать предложение
			// площадки — иначе её нечем было бы играть.
			if layout.extra(e.Ref.ProviderID, cs, ce, r.animeProviders) {
				var exists bool
				if err := tx.QueryRow(ctx, `
					SELECT EXISTS (SELECT 1 FROM canonical_episodes
					               WHERE title_id = $1 AND season_no = $2 AND episode_no = $3)`,
					titleID, cs, ce).Scan(&exists); err != nil {
					return pg.MapError(err)
				}
				if !exists {
					continue
				}
			}
		}
		// title пишется и пустым: пустая строка значит «площадка названия
		// не даёт», и это знание нужно, чтобы не перечитывать тайтл при
		// каждом открытии. Известное название пустым не затирается.
		//
		// Оценка длительности ложится только в пустую ячейку, точная
		// заменяет любую. Иначе побеждала бы площадка, записавшая последней,
		// и длина прыгала бы от прохода к проходу между точной и оценкой.
		var epID kernel.EpisodeID
		if err := tx.QueryRow(ctx, `
			INSERT INTO canonical_episodes
				(title_id, season_no, episode_no, duration_sec, thumbnail_url, title)
			VALUES ($1,$2,$3,NULLIF($4,0),NULLIF($5,''),$6)
			ON CONFLICT (title_id, season_no, episode_no) DO UPDATE SET
				duration_sec  = CASE WHEN $7::boolean
					THEN COALESCE(canonical_episodes.duration_sec, EXCLUDED.duration_sec)
					ELSE COALESCE(EXCLUDED.duration_sec, canonical_episodes.duration_sec) END,
				thumbnail_url = COALESCE(EXCLUDED.thumbnail_url, canonical_episodes.thumbnail_url),
				title         = COALESCE(NULLIF(EXCLUDED.title, ''), canonical_episodes.title, '')
			RETURNING id`,
			titleID, cs, ce, e.DurationSec, e.Thumbnail,
			strings.TrimSpace(e.Title), e.DurationApprox).Scan(&epID); err != nil {
			return pg.MapError(err)
		}
		if e.Ref.Zero() {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO episode_offers (episode_id, provider_id, provider_ref, season_no, episode_no, seen_at)
			VALUES ($1,$2,$3,$4,$5, now())
			ON CONFLICT (episode_id, provider_id, provider_ref) DO UPDATE SET
				season_no = EXCLUDED.season_no, episode_no = EXCLUDED.episode_no, seen_at = now()`,
			epID, e.Ref.ProviderID, e.Ref.ItemID, e.SeasonNo, e.EpisodeNo); err != nil {
			return pg.MapError(err)
		}
	}
	// После каждой пачки: она могла закрыть сезон (kinopub выложил
	// следующий) и сделать лишними серии, заведённые раньше другой площадкой.
	if err := r.pruneExtras(ctx, tx, titleID); err != nil {
		return err
	}
	return pg.MapError(tx.Commit(ctx))
}

// nullTime: нулевое время значит «площадка не сказала», а не «эпоха Unix».
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// normalizeTitle приводит название к виду, пригодному для нечёткого поиска.
// Площадки пишут «Название / Original Title» — берём обе половины, но
// нормализуем регистр и пробелы.
func normalizeTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// SetEpisodeThumbnail запоминает кадр серии, найденный по запросу. Уже
// известный не затирается: кадр, пришедший со списком серий, точнее.
func (r *CatalogRepo) SetEpisodeThumbnail(ctx context.Context, id kernel.EpisodeID, url string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE canonical_episodes SET thumbnail_url = $2
		WHERE id = $1 AND thumbnail_url IS NULL`, id, url)
	return pg.MapError(err)
}
