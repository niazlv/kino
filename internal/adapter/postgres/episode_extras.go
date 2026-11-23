package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// Лишние серии в закрытом сезоне.
//
// Серии разных площадок склеиваются по номеру «сезон × серия». Площадки
// с каталогом «как у телевизора» (kinopub, ЛордФильм, Filmix) нумеруют
// сезоны одинаково, а спецвыпуски — каждая по-своему: у балансера Alloha
// в пятом сезоне «Рика и Морти» серии 0–13, у kinopub — 1–12. Тринадцатая
// заводила каноническую серию, которой у главной площадки нет, и сезон,
// досмотренный целиком, навсегда оставался «недосмотренным на одну серию»:
// счётчик в «Новых», нет отметки сезона.
//
// Правило: площадка не заводит серию за концом сезона, который у более
// приоритетной площадки того же рода уже закрыт следующим сезоном. Новые
// серии последнего сезона и новые сезоны заводятся как прежде — так
// ЛордФильм и дальше приносит то, чего kinopub ещё не выложил.
//
// Аниме-площадки правило не трогает ни с какой стороны: их элемент
// покрывает один сезон, нумерация у них своя (сквозная у длинных
// сериалов), и сравнивать её с телевизионной нельзя.

// seasonLayout — где у каждой площадки тайтла кончаются сезоны.
type seasonLayout struct {
	prio map[string]int
	// tail[площадка][сезон] — последняя серия площадки в сезоне.
	tail map[string]map[int]int
	// last[площадка] — последний сезон площадки.
	last map[string]int
}

// extra — серия площадки p за концом сезона, который у более приоритетной
// площадки q уже закрыт. anime — площадки, к которым правило не относится.
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

// tvProviders — сколько у тайтла площадок, к которым правило относится.
// Меньше двух — сравнивать не с кем.
func (l seasonLayout) tvProviders(anime map[string]bool) int {
	n := 0
	for p := range l.tail {
		if !anime[p] {
			n++
		}
	}
	return n
}

// layoutOf читает раскладку сезонов тайтла по площадкам. Отключённые
// привязки не в счёт: их нумерация больше ничего не обещает.
func (r *CatalogRepo) layoutOf(ctx context.Context, tx pgx.Tx, titleID kernel.TitleID) (seasonLayout, error) {
	l := seasonLayout{prio: map[string]int{}, tail: map[string]map[int]int{}, last: map[string]int{}}
	rows, err := tx.Query(ctx, `
		SELECT provider_id, priority, priority_manual FROM external_refs
		WHERE title_id = $1 AND NOT disabled`, titleID)
	if err != nil {
		return l, pg.MapError(err)
	}
	for rows.Next() {
		var p string
		var stored int
		var manual bool
		if err := rows.Scan(&p, &stored, &manual); err != nil {
			rows.Close()
			return l, pg.MapError(err)
		}
		// У аниме-площадки привязок к тайтлу бывает несколько (по
		// сезону) — берётся старшая.
		v := r.effectivePriority(p, stored, manual)
		if cur, ok := l.prio[p]; !ok || v > cur {
			l.prio[p] = v
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return l, pg.MapError(err)
	}

	rows, err = tx.Query(ctx, `
		SELECT o.provider_id, e.season_no, max(e.episode_no)
		FROM episode_offers o
		JOIN canonical_episodes e ON e.id = o.episode_id
		JOIN external_refs x ON x.provider_id = o.provider_id AND x.provider_ref = o.provider_ref
		WHERE e.title_id = $1 AND x.title_id = $1 AND NOT x.disabled
		GROUP BY 1, 2`, titleID)
	if err != nil {
		return l, pg.MapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var s, end int
		if err := rows.Scan(&p, &s, &end); err != nil {
			return l, pg.MapError(err)
		}
		if l.tail[p] == nil {
			l.tail[p] = map[int]int{}
		}
		l.tail[p][s] = end
		if s > l.last[p] {
			l.last[p] = s
		}
	}
	return l, pg.MapError(rows.Err())
}

// pruneExtras убирает уже заведённые лишние серии тайтла: те, что
// предлагают только площадки, у которых эта серия — лишняя (см. extra).
//
// Нужно не только для старых данных: площадки приходят в любом порядке, и
// если ЛордФильм успел раньше kinopub, лишняя серия заводится до того, как
// правилу есть с чем сравнить. Серия, у которой есть чей-то прогресс или
// ссылка «поделиться», остаётся: удаление стёрло бы чужую историю.
func (r *CatalogRepo) pruneExtras(ctx context.Context, tx pgx.Tx, titleID kernel.TitleID) error {
	l, err := r.layoutOf(ctx, tx, titleID)
	if err != nil {
		return err
	}
	if l.tvProviders(r.animeProviders) < 2 {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT e.id, e.season_no, e.episode_no, array_agg(DISTINCT o.provider_id)
		FROM canonical_episodes e
		JOIN episode_offers o ON o.episode_id = e.id
		WHERE e.title_id = $1
		  AND NOT EXISTS (SELECT 1 FROM watch_progress w WHERE w.episode_id = e.id)
		  AND NOT EXISTS (SELECT 1 FROM share_links s WHERE s.episode_id = e.id)
		GROUP BY e.id`, titleID)
	if err != nil {
		return pg.MapError(err)
	}
	var drop []kernel.EpisodeID
	for rows.Next() {
		var id kernel.EpisodeID
		var s, ep int
		var provs []string
		if err := rows.Scan(&id, &s, &ep, &provs); err != nil {
			rows.Close()
			return pg.MapError(err)
		}
		all := len(provs) > 0
		for _, p := range provs {
			if !l.extra(p, s, ep, r.animeProviders) {
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
		return pg.MapError(err)
	}
	if len(drop) == 0 {
		return nil
	}
	// Условия про прогресс и ссылки — ещё раз в самом удалении: между
	// чтением и удалением кто-то мог начать смотреть.
	_, err = tx.Exec(ctx, `
		DELETE FROM canonical_episodes e
		WHERE e.id = ANY($1)
		  AND NOT EXISTS (SELECT 1 FROM watch_progress w WHERE w.episode_id = e.id)
		  AND NOT EXISTS (SELECT 1 FROM share_links s WHERE s.episode_id = e.id)`, drop)
	return pg.MapError(err)
}
