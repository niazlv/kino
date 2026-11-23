package catalog

import (
	"context"
	"log/slog"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Backfill добирает серии для тайтлов, за которыми следят.
//
// Ленивая догрузка наполняет каталог только при открытии тайтла — этого
// достаточно для просмотра, но не для списков: «новые эпизоды» считают по
// тому, что заведено, и у неоткрытых сериалов счётчик занижен. Кадры серий
// приезжают тем же запросом, поэтому добор закрывает и их.
//
// Только отслеживаемое: обходить ради этого весь каталог значит десятки
// тысяч запросов ради тайтлов, которые никто не смотрит.
type Backfill struct {
	titles FollowedTitles
	writer EpisodeWriter
	source func(providerID string) (EpisodeSource, error)
	log    *slog.Logger
}

// FollowedTitles отдаёт тайтлы, за которыми следит хоть кто-то из семьи,
// вместе с их адресом у площадки.
type FollowedTitles interface {
	FollowedRefs(ctx context.Context) ([]FollowedRef, error)
}

// FollowedRef описан плоско, а не через kernel.ProviderRef, чтобы адаптер
// не собирал вложенную структуру ради одного поля.
type FollowedRef struct {
	TitleID  kernel.TitleID
	Provider string
	ItemID   string
}

func (f FollowedRef) ref() kernel.ProviderRef {
	return kernel.ProviderRef{ProviderID: f.Provider, ItemID: f.ItemID}
}

func NewBackfill(t FollowedTitles, w EpisodeWriter,
	source func(string) (EpisodeSource, error), log *slog.Logger) *Backfill {
	return &Backfill{titles: t, writer: w, source: source, log: log}
}

// pause между тайтлами. Добор — фоновая работа, и вежливость к площадке
// стоит дороже пары сэкономленных минут: аккаунт у семьи один.
const pause = 400 * time.Millisecond

// Result — итог прохода.
type Result struct {
	Titles   int
	Episodes int
	Failed   int
}

// Run добирает серии по всем отслеживаемым тайтлам.
func (b *Backfill) Run(ctx context.Context) (Result, error) {
	refs, err := b.titles.FollowedRefs(ctx)
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, fr := range refs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		src, err := b.source(fr.Provider)
		if err != nil {
			res.Failed++
			continue
		}
		eps, err := src.Episodes(ctx, fr.ref())
		if err != nil || len(eps) == 0 {
			// Площадка недоступна или тайтл без серий — не повод рвать
			// весь проход: следующий запуск подберёт.
			res.Failed++
			b.sleep(ctx)
			continue
		}
		if err := b.writer.UpsertEpisodes(ctx, fr.TitleID, eps); err != nil {
			b.log.WarnContext(ctx, "серии не сохранены", "ref", fr.ref().String(), "err", err)
			res.Failed++
		} else {
			res.Titles++
			res.Episodes += len(eps)
		}
		b.sleep(ctx)
	}
	b.log.InfoContext(ctx, "добор серий завершён",
		"тайтлов", res.Titles, "серий", res.Episodes, "не удалось", res.Failed)
	return res, nil
}

func (b *Backfill) sleep(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(pause):
	}
}

var _ = dp.Episode{}
