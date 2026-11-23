package postgres

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	appcatalog "github.com/niazleushkin/kino/internal/app/catalog"
	"github.com/niazleushkin/kino/internal/app/matching"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

type CatalogRepo struct {
	db *pgxpool.Pool
	// prio — приоритет площадок по умолчанию, см. WithPriorities.
	prio map[string]int
	// matchOpts — отдельно включаемые правила склейки.
	matchOpts matching.Options
	// animeProviders — площадки, у которых весь каталог аниме.
	animeProviders map[string]bool
}

func NewCatalogRepo(db *pgxpool.Pool) *CatalogRepo { return &CatalogRepo{db: db} }

func (r *CatalogRepo) Episode(ctx context.Context, id kernel.EpisodeID) (catalog.Episode, error) {
	var e catalog.Episode
	// thumbnail_url тут обязателен: по нему отдаётся кадр серии, и без
	// него прокси картинки честно отвечает 404 на каждую карточку.
	var title *string
	err := r.db.QueryRow(ctx, `
		SELECT id, title_id, season_no, episode_no, absolute_no, COALESCE(duration_sec, 0),
		       COALESCE(thumbnail_url, ''), title
		FROM canonical_episodes WHERE id = $1`, id).
		Scan(&e.ID, &e.TitleID, &e.SeasonNo, &e.EpisodeNo, &e.AbsoluteNo, &e.DurationSec,
			&e.Thumbnail, &title)
	e.Title, e.TitleKnown = derefTitle(title)
	return e, pg.MapError(err)
}

// derefTitle разводит NULL и пустую строку: первое — «не спрашивали»,
// второе — «площадка не даёт». Для ленивого каталога это разные состояния.
func derefTitle(t *string) (string, bool) {
	if t == nil {
		return "", false
	}
	return *t, true
}

// Title поднимает тайтл ВМЕСТЕ с источниками: без них резолв невозможен,
// а отдельный запрос за ними — лишний round-trip на горячем пути.
func (r *CatalogRepo) Title(ctx context.Context, id kernel.TitleID) (catalog.Title, error) {
	var t catalog.Title
	var checked *time.Time
	err := r.db.QueryRow(ctx, `
		SELECT id, kind, COALESCE(imdb_id,''), COALESCE(tmdb_id,''), COALESCE(kp_id,''),
		       title, COALESCE(title_original,''), COALESCE(year, 0), COALESCE(poster_url,''),
		       COALESCE(plot,''), COALESCE(genres, '{}'), COALESCE(countries, '{}'), finished, sources_checked_at
		FROM canonical_titles WHERE id = $1`, id).
		Scan(&t.ID, &t.Kind, &t.IDs.IMDB, &t.IDs.TMDB, &t.IDs.Kinopoisk, &t.Title, &t.Original,
			&t.Year, &t.Poster, &t.Plot, &t.Genres, &t.Countries, &t.Finished, &checked)
	if err != nil {
		return catalog.Title{}, pg.MapError(err)
	}
	if checked != nil {
		t.SourcesCheckedAt = *checked
	}

	t.Sources, err = r.Sources(ctx, id)
	return t, err
}

// Sources — площадки тайтла, по убыванию приоритета.
func (r *CatalogRepo) Sources(ctx context.Context, id kernel.TitleID) ([]catalog.Source, error) {
	rows, err := r.db.Query(ctx, `
		SELECT provider_id, provider_ref, priority, priority_manual, season_no, disabled
		FROM external_refs WHERE title_id = $1`, id)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []catalog.Source
	for rows.Next() {
		var s catalog.Source
		var manual bool
		if err := rows.Scan(&s.Ref.ProviderID, &s.Ref.ItemID, &s.Priority, &manual,
			&s.SeasonNo, &s.Disabled); err != nil {
			return nil, pg.MapError(err)
		}
		s.Priority = r.effectivePriority(s.Ref.ProviderID, s.Priority, manual)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, pg.MapError(err)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].SeasonNo < out[j].SeasonNo
	})
	return out, nil
}

// Offers — какие площадки отдают серию и под какими номерами у себя.
// Выключенные источники сюда не попадают: их не резолвят.
func (r *CatalogRepo) Offers(ctx context.Context, ep kernel.EpisodeID) ([]catalog.Offer, error) {
	rows, err := r.db.Query(ctx, `
		SELECT o.provider_id, o.provider_ref, o.season_no, o.episode_no, x.priority, x.priority_manual,
		       COALESCE(x.mal_id, '')
		FROM episode_offers o
		JOIN external_refs x ON x.provider_id = o.provider_id AND x.provider_ref = o.provider_ref
		JOIN canonical_episodes e ON e.id = o.episode_id AND e.title_id = x.title_id
		WHERE o.episode_id = $1 AND NOT x.disabled`, ep)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []catalog.Offer
	for rows.Next() {
		var o catalog.Offer
		var manual bool
		if err := rows.Scan(&o.Ref.ProviderID, &o.Ref.ItemID, &o.SeasonNo, &o.EpisodeNo,
			&o.Priority, &manual, &o.MAL); err != nil {
			return nil, pg.MapError(err)
		}
		o.Priority = r.effectivePriority(o.Ref.ProviderID, o.Priority, manual)
		out = append(out, o)
	}
	return out, pg.MapError(rows.Err())
}

// Search — страница поиска по каталогу: limit/offset задают окно, вторым
// значением идёт общее число находок — витрине оно нужно, чтобы показать,
// сколько есть, и предложить листать дальше.
//
// Находки делятся на три ступени по тому, КАК подошло название:
//
//  0. название или слово в нём начинается с запроса («дюна» — «Дюна»,
//     «Дюна: Пророчество»; «престол» — «Игра престолов»). Так набирают;
//  1. запрос внутри слова или в чужом написании («уна» в «Дюна»);
//  2. только похоже — опечатка (word_similarity).
//
// Внутри ступени первыми идут популярные: на «игра» человек ждёт «Игру
// престолов», а не свежий корейский сериал с тем же словом. Точное
// совпадение названия весит как втрое большая популярность: набравший
// название целиком ждёт его, но безвестный однофамилец не заслоняет хит —
// а «игра» чаще всего недописанная «Игра престолов», а не фильм «Игра».
// Популярность неизвестна (ноль) — решают точность и похожесть, как до неё.
//
// Оператор % (similarity) здесь НЕ годится: он сравнивает строки целиком,
// и короткий запрос против длинного названия даёт низкое сходство —
// similarity('бегущая / the runner', 'бег') = 0.15 при пороге 0.3. Нужен
// word_similarity (<%, порог pg_trgm по умолчанию — 0.6): он сравнивает
// запрос с ЛУЧШИМ участком строки. Для запроса короче трёх букв похожесть
// не ищется: опечатка в двух буквах — это уже другое слово, а считалась бы
// она на каждой строке каталога, индекс такой запрос не сужает.
//
// Ключи названий (title_names) — та же нормализация, что у склейки:
// «принцесса медуза» находит «Принцесса-медуза», а алиасы площадок
// («Большой куш») — тайтл, у которого витринное название другое. Ключей
// короче трёх букв нет, поэтому «Мы» узнаётся по витринному названию.
func (r *CatalogRepo) Search(ctx context.Context, q string, limit, offset int) ([]catalog.Title, int, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, 0, nil
	}
	key := catalog.NormalizeName(q)
	rows, err := r.db.Query(ctx, `
		WITH named AS (
			SELECT title_id,
			       bool_or(name_key = $3) AS exact,
			       bool_or(name_key LIKE $3 || '%' OR name_key LIKE '% ' || $3 || '%') AS word
			FROM title_names
			WHERE $3 <> '' AND (name_key = $3 OR (length($3) >= 3 AND name_key LIKE '%' || $3 || '%'))
			GROUP BY title_id
		), cand AS (
			-- Объединение, а не OR в одном WHERE: так оба условия по
			-- витринному названию идут триграммным индексом.
			SELECT title_id AS id FROM named
			UNION
			SELECT id FROM canonical_titles
			WHERE title_norm LIKE '%' || $1 || '%' OR (length($1) >= 3 AND $1 <% title_norm)
		), hits AS (
			SELECT t.id, t.kind, t.title, t.year, t.poster_url, t.popularity,
			       COALESCE(n.exact, false) OR t.title_norm = $1 OR t.title_norm LIKE $1 || ' / %' AS exact,
			       COALESCE(n.word, false) OR t.title_norm LIKE $1 || '%' AS word,
			       n.title_id IS NOT NULL OR t.title_norm LIKE '%' || $1 || '%' AS inside,
			       word_similarity($1, t.title_norm) AS sim
			FROM cand c
			JOIN canonical_titles t ON t.id = c.id
			LEFT JOIN named n ON n.title_id = t.id
		)
		SELECT id, kind, title, COALESCE(year, 0), COALESCE(poster_url,''),
		       count(*) OVER() AS total
		FROM hits
		ORDER BY
			CASE WHEN exact OR word THEN 0 WHEN inside THEN 1 ELSE 2 END,
			popularity * CASE WHEN exact THEN 3 ELSE 1 END DESC,
			exact DESC,
			sim DESC,
			year DESC NULLS LAST,
			-- Однозначный порядок: страницы листаются смещением, и при
			-- равенстве всего остального одна находка иначе могла бы
			-- попасть на две страницы, а другая — ни на одну.
			id DESC
		LIMIT $2 OFFSET $4`, q, limit, key, offset)
	if err != nil {
		return nil, 0, pg.MapError(err)
	}
	defer rows.Close()

	var out []catalog.Title
	total := 0
	for rows.Next() {
		var t catalog.Title
		if err := rows.Scan(&t.ID, &t.Kind, &t.Title, &t.Year, &t.Poster, &total); err != nil {
			return nil, 0, pg.MapError(err)
		}
		out = append(out, t)
	}
	return out, total, pg.MapError(rows.Err())
}

// EpisodeIDByProviderRef переводит адрес серии у площадки в канонический id.
//
// Нужен импорту истории: площадка знает свои идентификаторы, а прогресс
// ключуется только на канонические — иначе подключение второго провайдера
// расколет перенесённую историю просмотров.
func (r *CatalogRepo) EpisodeIDByProviderRef(ctx context.Context,
	ref kernel.ProviderRef, season, episode int) (kernel.EpisodeID, error) {
	var id kernel.EpisodeID
	// Через предложения, а не через номер канонической серии: у площадки
	// своя нумерация, и совпадать с канонической она не обязана.
	err := r.db.QueryRow(ctx, `
		SELECT o.episode_id
		FROM episode_offers o
		WHERE o.provider_id = $1 AND o.provider_ref = $2
		  AND o.season_no = $3 AND o.episode_no = $4
		LIMIT 1`,
		ref.ProviderID, ref.ItemID, season, episode).Scan(&id)
	return id, pg.MapError(err)
}

// TitleIDByProviderRef — канонический тайтл по адресу у площадки. Нужен
// переносу журнала: поставить слежение за сериалом, который смотрели.
func (r *CatalogRepo) TitleIDByProviderRef(ctx context.Context, ref kernel.ProviderRef) (kernel.TitleID, error) {
	var id kernel.TitleID
	err := r.db.QueryRow(ctx, `
		SELECT title_id FROM external_refs
		WHERE provider_id = $1 AND provider_ref = $2
		ORDER BY season_no LIMIT 1`, ref.ProviderID, ref.ItemID).Scan(&id)
	return id, pg.MapError(err)
}

// Episodes возвращает серии тайтла по порядку.
func (r *CatalogRepo) Episodes(ctx context.Context, id kernel.TitleID) ([]catalog.Episode, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, title_id, season_no, episode_no, COALESCE(duration_sec, 0),
		       COALESCE(thumbnail_url, ''), title
		FROM canonical_episodes WHERE title_id = $1
		ORDER BY season_no, episode_no`, id)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []catalog.Episode
	for rows.Next() {
		var e catalog.Episode
		var title *string
		if err := rows.Scan(&e.ID, &e.TitleID, &e.SeasonNo, &e.EpisodeNo,
			&e.DurationSec, &e.Thumbnail, &title); err != nil {
			return nil, pg.MapError(err)
		}
		e.Title, e.TitleKnown = derefTitle(title)
		out = append(out, e)
	}
	return out, pg.MapError(rows.Err())
}

// BrowseQuery — фильтры раздела каталога.
type BrowseQuery struct {
	Kind  string // movie | series; пусто — любой
	Genre string // жанр целиком, как он лежит в каталоге
	Sort  string // new | updated | year | title
	Page  int
	Limit int
}

// BrowsePage — страница раздела вместе с общим числом.
type BrowsePage struct {
	Items []catalog.Title
	Total int
}

// Browse отдаёт раздел каталога.
//
// Аниме — это ЖАНР, а не вид: у площадки оно приходит в genres, а kind
// остаётся movie или series. Отдельный вид завёл бы тайтлы, которые
// одновременно и сериал, и аниме, в два несовместимых состояния.
func (r *CatalogRepo) Browse(ctx context.Context, q BrowseQuery) (BrowsePage, error) {
	if q.Limit <= 0 || q.Limit > 120 {
		q.Limit = 60
	}
	if q.Page < 1 {
		q.Page = 1
	}
	order := "source_created_at DESC NULLS LAST, id DESC"
	switch q.Sort {
	case "updated":
		// «Свежие серии»: последнее обновление хоть на одной площадке —
		// у сериала это почти всегда вышедшая серия.
		order = "source_updated_at DESC NULLS LAST, id DESC"
	case "popular":
		// «Популярное»: та же популярность, что ранжирует поиск (оценки КП,
		// просмотры на площадке — миграция 00022); свежее — при равенстве.
		order = "popularity DESC, source_created_at DESC NULLS LAST, id DESC"
	case "year":
		order = "year DESC NULLS LAST, id DESC"
	case "title":
		order = "title_norm ASC"
	}

	// Условия строятся из ограниченного набора: подстановка идёт значениями,
	// а порядок сортировки выбирается из списка выше, а не приходит извне.
	const where = `
		WHERE ($1 = '' OR kind = $1)
		  AND ($2 = '' OR $2 = ANY(genres))`

	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM canonical_titles`+where, q.Kind, q.Genre).Scan(&total); err != nil {
		return BrowsePage{}, pg.MapError(err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, kind, title, COALESCE(year,0), COALESCE(poster_url,'')
		FROM canonical_titles`+where+`
		ORDER BY `+order+`
		LIMIT $3 OFFSET $4`, q.Kind, q.Genre, q.Limit, (q.Page-1)*q.Limit)
	if err != nil {
		return BrowsePage{}, pg.MapError(err)
	}
	defer rows.Close()

	page := BrowsePage{Total: total}
	for rows.Next() {
		var t catalog.Title
		if err := rows.Scan(&t.ID, &t.Kind, &t.Title, &t.Year, &t.Poster); err != nil {
			return BrowsePage{}, pg.MapError(err)
		}
		page.Items = append(page.Items, t)
	}
	return page, pg.MapError(rows.Err())
}

// Genres отдаёт жанры каталога с числом тайтлов — для навигации.
func (r *CatalogRepo) Genres(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g, count(*) AS n
		FROM canonical_titles, unnest(genres) AS g
		GROUP BY g
		HAVING count(*) >= 20
		ORDER BY n DESC`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var g string
		var n int
		if err := rows.Scan(&g, &n); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, g)
	}
	return out, pg.MapError(rows.Err())
}

// OfferStats — сколько серий отдаёт каждая площадка тайтла и когда их
// последний раз перечитывали. По этому ленивый каталог решает, у кого
// серии ещё не спрашивали, а у кого пора перечитать.
func (r *CatalogRepo) OfferStats(ctx context.Context, id kernel.TitleID) (map[kernel.ProviderRef]appcatalog.OfferStat, error) {
	rows, err := r.db.Query(ctx, `
		SELECT o.provider_id, o.provider_ref, count(*), max(o.seen_at)
		FROM episode_offers o
		JOIN canonical_episodes e ON e.id = o.episode_id
		JOIN external_refs x ON x.provider_id = o.provider_id AND x.provider_ref = o.provider_ref
		WHERE e.title_id = $1 AND x.title_id = $1
		GROUP BY o.provider_id, o.provider_ref`, id)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	out := map[kernel.ProviderRef]appcatalog.OfferStat{}
	for rows.Next() {
		var ref kernel.ProviderRef
		var st appcatalog.OfferStat
		if err := rows.Scan(&ref.ProviderID, &ref.ItemID, &st.Episodes, &st.SeenAt); err != nil {
			return nil, pg.MapError(err)
		}
		out[ref] = st
	}
	return out, pg.MapError(rows.Err())
}

// MarkSourcesChecked запоминает, что площадки об этом тайтле спрашивали.
func (r *CatalogRepo) MarkSourcesChecked(ctx context.Context, id kernel.TitleID, at time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE canonical_titles SET sources_checked_at = $2 WHERE id = $1`, id, at)
	return pg.MapError(err)
}
