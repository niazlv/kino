package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// FeedsRepo — производные ленты: непросмотренное по отслеживаемому и история.
//
// Оба списка ВЫВОДЯТСЯ из уже имеющихся данных и ничего своего не хранят:
// отдельная таблица «новых эпизодов» рассинхронизировалась бы с прогрессом
// в первый же вечер.
type FeedsRepo struct{ db *pgxpool.Pool }

func NewFeedsRepo(db *pgxpool.Pool) *FeedsRepo { return &FeedsRepo{db} }

// NewEpisodeItem — сериал со счётчиком недосмотренного.
type NewEpisodeItem struct {
	TitleID   kernel.TitleID
	Title     string
	Kind      string
	Year      int
	HasArt    bool
	Unwatched int
	Total     int
}

// NewEpisodes — то, что человек ещё не видел в сериалах, за которыми следит.
//
// Только для following: непросмотренное во всём каталоге — это десятки тысяч
// строк, и такой список не значит ничего. Слежение и есть заявление
// «мне интересны новые серии именно здесь».
// onlyUnwatched=true даёт «новые эпизоды» — подмножество со свежим;
// false даёт всё слежение со счётчиками, то есть «я смотрю».
// Один запрос на оба списка: они отличаются одним условием, и вторая
// копия разъехалась бы с первой при первой же правке.
//
// Отдаётся страницей с прокруткой, а не обрезанным окном: сортировка
// ставит недосмотренное раньше досмотренного, поэтому активная часть
// списка всегда в первых страницах. Общее число групп и число активных
// считает оконной функцией поверх сгруппированного запроса.
func (r *FeedsRepo) NewEpisodes(ctx context.Context, u kernel.UserID, limit, offset int,
	onlyUnwatched bool) ([]NewEpisodeItem, int, int, error) {
	const grouped = `
		SELECT t.id, t.title, t.kind, COALESCE(t.year,0) AS year,
		       (t.poster_url IS NOT NULL) AS has_art,
		       count(*) FILTER (WHERE p.status IS DISTINCT FROM 'completed')::int AS unwatched,
		       count(*)::int AS total
		FROM user_titles m
		JOIN canonical_titles   t ON t.id = m.title_id
		JOIN canonical_episodes e ON e.title_id = t.id
		LEFT JOIN watch_progress p ON p.episode_id = e.id AND p.user_id = m.user_id
		WHERE m.user_id = $1 AND m.following AND NOT m.hidden
		GROUP BY t.id
		HAVING ($4 = false OR count(*) FILTER (WHERE p.status IS DISTINCT FROM 'completed') > 0)`
	rows, err := r.db.Query(ctx, `
		SELECT id, title, kind, year, has_art, unwatched, total,
		       count(*) OVER ()::int AS grand,
		       count(*) FILTER (WHERE unwatched > 0) OVER ()::int AS active
		FROM (`+grouped+`) g
		ORDER BY unwatched DESC, title, id
		LIMIT $2 OFFSET $3`, u, limit, offset, onlyUnwatched)
	if err != nil {
		return nil, 0, 0, pg.MapError(err)
	}
	defer rows.Close()

	var out []NewEpisodeItem
	var grand, active int
	for rows.Next() {
		var it NewEpisodeItem
		if err := rows.Scan(&it.TitleID, &it.Title, &it.Kind, &it.Year,
			&it.HasArt, &it.Unwatched, &it.Total, &grand, &active); err != nil {
			return nil, 0, 0, pg.MapError(err)
		}
		out = append(out, it)
	}
	// Пустая страница за концом ленты не видит оконных счётчиков: тогда
	// оба числа спрашиваются отдельно — редкий путь, не горячий.
	// grouped ссылается на $1 и $4, поэтому пустышки $2/$3 обязаны быть:
	// иначе запрос падает «there is no parameter $4».
	if len(out) == 0 && offset > 0 {
		err = r.db.QueryRow(ctx, `
			SELECT count(*)::int,
			       count(*) FILTER (WHERE unwatched > 0)::int
			FROM (`+grouped+`) g`, u, nil, nil, onlyUnwatched).Scan(&grand, &active)
		if err != nil {
			return nil, 0, 0, pg.MapError(err)
		}
	}
	return out, grand, active, pg.MapError(rows.Err())
}

// HistoryItem — строка истории просмотров.
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
	// HasShot — есть ли кадр у самой серии. Отдельно от постера тайтла:
	// в истории строк много и кадр серии узнаётся куда быстрее, чем
	// повторяющийся постер сериала.
	HasShot   bool
	UpdatedAt time.Time
}

// HistoryFilter — чем сузить историю. Пустое поле — не сужать.
type HistoryFilter struct {
	Kind  string // movie | series
	Query string // подстрока названия, без учёта регистра
}

// History — что и когда смотрели, страницей по номеру. Личное: фильтр по
// user_id стоит в SQL, а не только в политике.
//
// Смещение, а не курсор: человек хочет видеть номера страниц и прыгать по
// ним, а строк у одного пользователя тысячи, не миллионы — смещение здесь
// дешёвое. Общее число берётся тем же запросом оконной функцией: второй
// поход за count(*) удвоил бы стоимость каждой страницы.
//
// Фильтр по виду и подстроке названия — те же условия и в счётчике: число
// страниц считается по тому, что показывается.
func (r *FeedsRepo) History(ctx context.Context, u kernel.UserID, limit, offset int,
	f HistoryFilter) ([]HistoryItem, int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.episode_id, t.id, t.title, t.kind, e.season_no, e.episode_no,
		       p.position_ms, p.duration_ms, p.status,
		       (t.poster_url IS NOT NULL), (e.thumbnail_url IS NOT NULL), p.updated_at,
		       count(*) OVER ()
		FROM watch_progress p
		JOIN canonical_episodes e ON e.id = p.episode_id
		JOIN canonical_titles   t ON t.id = e.title_id
		WHERE p.user_id = $1
		  AND ($4 = '' OR t.kind = $4)
		  AND ($5 = '' OR t.title ILIKE '%' || $5 || '%')
		ORDER BY p.updated_at DESC, p.episode_id DESC
		LIMIT $2 OFFSET $3`, u, limit, offset, f.Kind, f.Query)
	if err != nil {
		return nil, 0, pg.MapError(err)
	}
	defer rows.Close()

	var out []HistoryItem
	var total int
	for rows.Next() {
		var it HistoryItem
		if err := rows.Scan(&it.EpisodeID, &it.TitleID, &it.Title, &it.Kind, &it.SeasonNo, &it.EpisodeNo,
			&it.PositionMS, &it.DurationMS, &it.Status, &it.HasArt, &it.HasShot,
			&it.UpdatedAt, &total); err != nil {
			return nil, 0, pg.MapError(err)
		}
		out = append(out, it)
	}
	// Страница за концом пуста, и оконный счётчик в ней не виден: тогда
	// общее число спрашивается отдельно — это редкий путь, не горячий.
	if len(out) == 0 && offset > 0 {
		err = r.db.QueryRow(ctx, `
			SELECT count(*) FROM watch_progress p
			JOIN canonical_episodes e ON e.id = p.episode_id
			JOIN canonical_titles   t ON t.id = e.title_id
			WHERE p.user_id = $1 AND ($2 = '' OR t.kind = $2)
			  AND ($3 = '' OR t.title ILIKE '%' || $3 || '%')`, u, f.Kind, f.Query).Scan(&total)
		if err != nil {
			return nil, 0, pg.MapError(err)
		}
	}
	return out, total, pg.MapError(rows.Err())
}
