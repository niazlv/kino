package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	appcatalog "github.com/niazleushkin/kino/internal/app/catalog"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

type MarksRepo struct{ db *pgxpool.Pool }

func NewMarksRepo(db *pgxpool.Pool) *MarksRepo { return &MarksRepo{db} }

func (r *MarksRepo) Get(ctx context.Context, u kernel.UserID, t kernel.TitleID) (account.Marks, error) {
	m := account.Marks{UserID: u, TitleID: t}
	err := r.db.QueryRow(ctx, `
		SELECT following, watchlist, hidden
		FROM user_titles WHERE user_id = $1 AND title_id = $2`, u, t).
		Scan(&m.Following, &m.Watchlist, &m.Hidden)
	if err != nil {
		if e := pg.MapError(err); isNotFound(e) {
			// Отметок нет — это не ошибка, а обычное состояние: человек
			// просто ещё ничего не решил про этот тайтл.
			return m, nil
		}
		return account.Marks{}, pg.MapError(err)
	}
	return m, nil
}

// Set записывает отметки целиком. Частичное обновление не нужно: интерфейс
// всегда знает текущее состояние, а «обнови только это поле» породило бы
// гонки между вкладками.
//
// Оценки в записи нет: UI оценок убран, и сохранение отметок не должно
// затирать то, что было проставлено, пока они существовали. Колонка в базе
// остаётся — её судьба решается отдельно, а не молчаливой перезаписью.
//
// Снятое здесь «Слежу» запоминается (unfollowed_at): автоматика, что ставит
// слежение досмотревшему серию, обязана уважать решение «не слежу».
// Включённое — стирает память о снятии.
func (r *MarksRepo) Set(ctx context.Context, m account.Marks) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_titles (user_id, title_id, following, watchlist, hidden, updated_at)
		VALUES ($1,$2,$3,$4,$5, now())
		ON CONFLICT (user_id, title_id) DO UPDATE SET
			following = EXCLUDED.following,
			watchlist = EXCLUDED.watchlist,
			hidden    = EXCLUDED.hidden,
			updated_at = now(),
			unfollowed_at = `+unfollowedSQL("now()"),
		m.UserID, m.TitleID, m.Following, m.Watchlist, m.Hidden)
	return pg.MapError(err)
}

// unfollowedSQL — новое unfollowed_at при записи following поверх строки:
// включили — снятия больше нет; сняли включённое — снято в момент at;
// осталось как было — память прежняя.
func unfollowedSQL(at string) string {
	return `CASE WHEN EXCLUDED.following THEN NULL
	             WHEN user_titles.following THEN ` + at + `
	             ELSE user_titles.unfollowed_at END`
}

// Prefs отдаёт выбранные в плеере озвучку и субтитры.
func (r *MarksRepo) Prefs(ctx context.Context, u kernel.UserID, t kernel.TitleID) (account.TitlePrefs, error) {
	p := account.TitlePrefs{UserID: u, TitleID: t}
	err := r.db.QueryRow(ctx, `
		SELECT audio_pref, text_pref, source_pref
		FROM user_titles WHERE user_id = $1 AND title_id = $2`, u, t).
		Scan(&p.Audio, &p.Subtitle, &p.Source)
	if err != nil {
		if e := pg.MapError(err); isNotFound(e) {
			// Строки нет — человек ничего не выбирал. Обычное состояние.
			return p, nil
		}
		return account.TitlePrefs{}, pg.MapError(err)
	}
	return p, nil
}

// SetPrefs записывает только переданные поля: nil — «не менять».
//
// Отметки при этом не трогаются, и наоборот — Set их не затирает. Иначе
// «Слежу» на странице стирало бы выбранную в плеере дорожку.
// updated_at не двигается: выбор дорожки — не то событие, по которому
// стоит переставлять тайтл в списках.
func (r *MarksRepo) SetPrefs(ctx context.Context, p account.TitlePrefs) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_titles (user_id, title_id, audio_pref, text_pref, source_pref)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (user_id, title_id) DO UPDATE SET
			audio_pref  = COALESCE(EXCLUDED.audio_pref,  user_titles.audio_pref),
			text_pref   = COALESCE(EXCLUDED.text_pref,   user_titles.text_pref),
			source_pref = COALESCE(EXCLUDED.source_pref, user_titles.source_pref)`,
		p.UserID, p.TitleID, p.Audio, p.Subtitle, p.Source)
	return pg.MapError(err)
}

// UnhideByEpisode снимает «скрыт» с тайтла, серию которого включили.
//
// Скрытие — решение «не показывать», а включённая серия — решение
// обратное и более позднее. Оставить тайтл скрытым значило бы прятать
// то, что человек прямо сейчас смотрит.
func (r *MarksRepo) UnhideByEpisode(ctx context.Context, u kernel.UserID, e kernel.EpisodeID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE user_titles m SET hidden = false, updated_at = now()
		FROM canonical_episodes ep
		WHERE ep.id = $2 AND m.user_id = $1 AND m.title_id = ep.title_id AND m.hidden`, u, e)
	return pg.MapError(err)
}

// SetFollowing включает слежение, не трогая остальные отметки.
// Нужен импорту: список «я смотрю» у площадки — это именно слежение.
//
// at — когда отметка поставлена: по этому времени сортируются списки.
// Перенос передаёт дату у площадки или время, выстроенное в её порядке;
// ставь он всем «сейчас», список вышел бы перевёрнутым.
func (r *MarksRepo) SetFollowing(ctx context.Context, u kernel.UserID, t kernel.TitleID, on bool, at time.Time) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_titles (user_id, title_id, following, updated_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (user_id, title_id) DO UPDATE SET
			following = EXCLUDED.following, updated_at = EXCLUDED.updated_at,
			unfollowed_at = `+unfollowedSQL("EXCLUDED.updated_at"), u, t, on, at)
	return pg.MapError(err)
}

// FollowByEpisode ставит «Слежу» сериалу, серию которого человек досмотрел
// или отметил просмотренной: смотрит — значит, ждёт следующую. Без этого
// «Новые эпизоды» молчали бы о сериале, который человек смотрит, пока тот
// сам не нажмёт «Слежу».
//
// Только идущий сериал: у завершённого новых серий не будет, а слежение
// поставило бы его досмотр первым в «Новые» с сотнями «новых» серий.
// Снятое руками (unfollowed_at) не возвращается, стоящее — не трогается:
// иначе каждая досмотренная серия переставляла бы тайтл в списке.
func (r *MarksRepo) FollowByEpisode(ctx context.Context, u kernel.UserID, e kernel.EpisodeID) error {
	return r.followWatching(ctx, u, `(SELECT title_id FROM canonical_episodes WHERE id = $2)`, e)
}

// FollowByTitle — то же для отметки сезона целиком.
func (r *MarksRepo) FollowByTitle(ctx context.Context, u kernel.UserID, t kernel.TitleID) error {
	return r.followWatching(ctx, u, `$2`, t)
}

// followWatching — общее тело: title — выражение, дающее id тайтла.
func (r *MarksRepo) followWatching(ctx context.Context, u kernel.UserID, title string, arg any) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_titles (user_id, title_id, following, updated_at)
		SELECT $1, t.id, true, now()
		FROM canonical_titles t
		WHERE t.id = `+title+` AND t.kind = 'series' AND NOT t.finished
		ON CONFLICT (user_id, title_id) DO UPDATE SET following = true, updated_at = now()
		WHERE NOT user_titles.following AND user_titles.unfollowed_at IS NULL`, u, arg)
	return pg.MapError(err)
}

// SetWatchlist ставит «буду смотреть», не трогая остальные отметки.
// Нужен переносу папки закладок площадки; at — как у SetFollowing.
func (r *MarksRepo) SetWatchlist(ctx context.Context, u kernel.UserID, t kernel.TitleID, on bool, at time.Time) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_titles (user_id, title_id, watchlist, updated_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (user_id, title_id) DO UPDATE SET
			watchlist = EXCLUDED.watchlist, updated_at = EXCLUDED.updated_at`, u, t, on, at)
	return pg.MapError(err)
}

// List отдаёт тайтлы по виду отметки страницей и общее число в списке.
//
// Список, который нельзя докрутить, — сломанная витрина, а не лимит:
// окно без прокрутки прятало бы всё за двухсотой карточкой. Общее число
// берётся оконной функцией в том же запросе, как в History.
func (r *MarksRepo) List(ctx context.Context, u kernel.UserID,
	kind account.MarkKind, limit, offset int) ([]catalog.Title, int, error) {
	var where string
	switch kind {
	case account.MarkFollowing:
		where = "m.following"
	case account.MarkWatchlist:
		where = "m.watchlist"
	default:
		return nil, 0, kernel.ErrInvalidInput
	}

	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.kind, t.title, COALESCE(t.year,0), COALESCE(t.poster_url,''),
		       count(*) OVER ()
		FROM user_titles m
		JOIN canonical_titles t ON t.id = m.title_id
		WHERE m.user_id = $1 AND `+where+` AND NOT m.hidden
		-- id в хвосте сортировки обязателен: без уникального разрубщика
		-- равные updated_at меняются местами между страницами, и окно
		-- возвращает то, что уже показано.
		ORDER BY m.updated_at DESC, t.id DESC
		LIMIT $2 OFFSET $3`, u, limit, offset)
	if err != nil {
		return nil, 0, pg.MapError(err)
	}
	defer rows.Close()

	var out []catalog.Title
	var total int
	for rows.Next() {
		var t catalog.Title
		if err := rows.Scan(&t.ID, &t.Kind, &t.Title, &t.Year, &t.Poster, &total); err != nil {
			return nil, 0, pg.MapError(err)
		}
		out = append(out, t)
	}
	// Пустая страница за концом списка не видит оконный счётчик: тогда
	// общее число спрашивается отдельно — редкий путь, не горячий.
	if len(out) == 0 && offset > 0 {
		err = r.db.QueryRow(ctx, `
			SELECT count(*) FROM user_titles m
			WHERE m.user_id = $1 AND `+where+` AND NOT m.hidden`, u).Scan(&total)
		if err != nil {
			return nil, 0, pg.MapError(err)
		}
	}
	return out, total, pg.MapError(rows.Err())
}

// FollowedRefs отдаёт всё, за чем следит хоть кто-то из семьи.
//
// Без привязки к пользователю намеренно: каталог общий, и добирать серии
// дважды для двух людей, следящих за одним сериалом, незачем.
// Личным остаётся прогресс, а не наличие серий в каталоге.
func (r *MarksRepo) FollowedRefs(ctx context.Context) ([]appcatalog.FollowedRef, error) {
	// Все включённые площадки тайтла, а не лучшая: новая серия онгоинга
	// часто появляется у аниме-площадки раньше, чем у kinopub.
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT m.title_id, x.provider_id, x.provider_ref
		FROM user_titles m
		JOIN external_refs x ON x.title_id = m.title_id AND NOT x.disabled
		WHERE m.following
		ORDER BY m.title_id`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []appcatalog.FollowedRef
	for rows.Next() {
		var f appcatalog.FollowedRef
		if err := rows.Scan(&f.TitleID, &f.Provider, &f.ItemID); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, f)
	}
	return out, pg.MapError(rows.Err())
}
