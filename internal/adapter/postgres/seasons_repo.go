package postgres

// Данные для лечения сезонов (app/catalog.Reconciler.FixSeasons): где
// сезоны разошлись с MAL, какие тайтлы похожи на отдельно заведённый сезон
// и кто им может быть хозяином. Решения — в app/matching.

import (
	"context"
	"fmt"

	"github.com/niazleushkin/kino/internal/app/catalog"
	"github.com/niazleushkin/kino/internal/app/matching"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// SeasonConflicts — тайтлы, где одна работа (номер MAL) стоит у двух
// привязок на разных сезонах. Привязка на весь тайтл (сезон 0) не в счёт.
func (r *CatalogRepo) SeasonConflicts(ctx context.Context) ([]kernel.TitleID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT a.title_id
		FROM external_refs a
		JOIN external_refs b ON b.title_id = a.title_id AND b.mal_id = a.mal_id
		WHERE a.mal_id <> '' AND a.season_no > 0 AND b.season_no > 0
		  AND a.season_no <> b.season_no
		ORDER BY a.title_id`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []kernel.TitleID
	for rows.Next() {
		var id kernel.TitleID
		if err := rows.Scan(&id); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, id)
	}
	return out, pg.MapError(rows.Err())
}

// ReseasonRefs переводит привязки тайтла на другие сезоны (matching.Realign).
//
// Серии этих привязок лежали в чужом сезоне, поэтому их предложения
// удаляются: ленивый каталог увидит площадку непрочитанной и разложит её
// серии заново при следующем открытии. Канонические серии, которые после
// этого никто не отдаёт и никто не смотрел, уходят (dropOrphans).
// Закреплённую человеком привязку не трогает.
func (r *CatalogRepo) ReseasonRefs(ctx context.Context, title kernel.TitleID, moves []matching.Move) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return pg.MapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, m := range moves {
		tag, err := tx.Exec(ctx, `
			UPDATE external_refs SET season_no = $4
			WHERE provider_id = $1 AND provider_ref = $2 AND title_id = $3 AND NOT pinned`,
			m.Ref.ProviderID, m.Ref.ItemID, title, m.To)
		if err != nil {
			return pg.MapError(err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `DELETE FROM episode_offers WHERE provider_id = $1 AND provider_ref = $2`,
			m.Ref.ProviderID, m.Ref.ItemID); err != nil {
			return pg.MapError(err)
		}
	}
	if err := dropOrphans(ctx, tx, title); err != nil {
		return err
	}
	return pg.MapError(tx.Commit(ctx))
}

// SeasonOrphans — сериалы, заведённые только аниме-площадками, которые
// могут оказаться сезоном другого тайтла: в названии голое число в конце,
// у привязки сезон позже первого или их номер MAL стоит у другого тайтла
// на другом сезоне. Закреплённые человеком не берутся.
//
// Регулярка в SQL — только грубое сито: цифра в конце названия или его
// половины («Гинтама 6 / Gintama. Slip Arc» — так названия и хранятся);
// что это номер сезона, решает matching. Порядок — по году: ранний сезон
// переносится раньше, и следующему уже есть что продолжать.
func (r *CatalogRepo) SeasonOrphans(ctx context.Context, animeProviders []string) ([]catalog.OrphanTitle, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.title, COALESCE(t.title_original, ''), COALESCE(t.year, 0)
		FROM canonical_titles t
		WHERE t.kind = 'series'
		  AND EXISTS (SELECT 1 FROM external_refs x WHERE x.title_id = t.id)
		  AND NOT EXISTS (SELECT 1 FROM external_refs x WHERE x.title_id = t.id
		                  AND (x.pinned OR x.season_no = 0 OR NOT (x.provider_id = ANY($1))))
		  AND (t.title ~ '\d\s*($| / )' OR t.title_original ~ '\d\s*($| / )'
		       OR EXISTS (SELECT 1 FROM external_refs x WHERE x.title_id = t.id AND x.season_no > 1)
		       OR EXISTS (SELECT 1 FROM external_refs x
		                  JOIN external_refs y ON y.mal_id = x.mal_id AND y.title_id <> x.title_id
		                  WHERE x.title_id = t.id AND x.mal_id <> ''
		                    AND y.season_no > 0 AND y.season_no <> x.season_no))
		ORDER BY t.year NULLS LAST, t.id`, animeProviders)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []catalog.OrphanTitle
	for rows.Next() {
		var o catalog.OrphanTitle
		if err := rows.Scan(&o.ID, &o.Title, &o.Original, &o.Year); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, o)
	}
	return out, pg.MapError(rows.Err())
}

// SeasonOwners — возможные хозяева тайтла-сезона: тайтлы с общим ключом
// названия или номером MAL. Ключ, общий у сотен тайтлов, отсекает LIMIT —
// у такого однофамильцев всё равно не разобрать.
func (r *CatalogRepo) SeasonOwners(ctx context.Context, self kernel.TitleID, keys, mals []string) ([]matching.Candidate, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT t.id FROM canonical_titles t
		WHERE t.id <> $1 AND t.kind = 'series'
		  AND (t.id IN (SELECT title_id FROM title_names WHERE name_key = ANY($2))
		       OR t.id IN (SELECT title_id FROM external_refs WHERE mal_id = ANY($3)))
		LIMIT 50`, self, keys, mals)
	if err != nil {
		return nil, pg.MapError(err)
	}
	var ids []kernel.TitleID
	for rows.Next() {
		var id kernel.TitleID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, pg.MapError(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, pg.MapError(err)
	}
	out := make([]matching.Candidate, 0, len(ids))
	for _, id := range ids {
		c, err := r.Candidate(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("хозяин %d: %w", id, err)
		}
		out = append(out, c)
	}
	return out, nil
}
