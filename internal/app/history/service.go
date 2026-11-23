// Package history — однократный перенос накопленной истории просмотров
// с площадки на аккаунт платформы.
//
// Существует потому, что семья смотрит через площадку годами: если при
// переезде история обнулится, все вернутся в старое приложение в первый же
// вечер. Читаем один раз — это не нарушает правила «не писать в площадку»,
// на котором держится весь проект.
//
// Запускает перенос администратор из карточки пользователя, а не сам
// пользователь с главной (auth.Policy.CanImportFor): аккаунт площадки
// общий на всю семью, и лишний перенос тратит тот же лимит, что просмотр.
package history

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Source — та часть провайдера, что нужна переносу.
//
// Кроме самих позиций нужны Title и Episodes: история почти всегда содержит
// тайтлы, до которых обход каталога ещё не дошёл, и ждать полного обхода
// ради них бессмысленно — мы уже точно знаем, какие тайтлы нужны.
type Source interface {
	ImportWatchHistory(ctx context.Context, fn func(dp.WatchRecord) error) error
	Title(ctx context.Context, ref kernel.ProviderRef) (dp.Title, error)
	Episodes(ctx context.Context, ref kernel.ProviderRef) ([]dp.Episode, error)

	// Watchlist — список «я смотрю» за два запроса, без позиций.
	Watchlist(ctx context.Context) ([]dp.Title, error)
	// WatchHistoryFor — позиции одного тайтла, по требованию.
	WatchHistoryFor(ctx context.Context, ref kernel.ProviderRef) ([]dp.WatchRecord, error)

	// BookmarkFolders — папки закладок аккаунта; BookmarkFolder — тайтлы
	// одной папки. У семейной подписки папка — единственное личное.
	BookmarkFolders(ctx context.Context) ([]dp.BookmarkFolder, error)
	BookmarkFolder(ctx context.Context, id string) ([]dp.Title, error)

	// RecentHistory — журнал просмотров площадки не старше since, с датами.
	RecentHistory(ctx context.Context, since time.Time) ([]dp.WatchRecord, error)
}

// Catalog переводит адреса площадки в канонические идентификаторы и
// умеет завести недостающий тайтл.
type Catalog interface {
	EpisodeIDByProviderRef(ctx context.Context, ref kernel.ProviderRef,
		season, episode int) (kernel.EpisodeID, error)
	UpsertTitle(ctx context.Context, t dp.Title) (kernel.TitleID, error)
	TitleIDByProviderRef(ctx context.Context, ref kernel.ProviderRef) (kernel.TitleID, error)
	UpsertEpisodes(ctx context.Context, titleID kernel.TitleID, eps []dp.Episode) error
}

// Progress — запись прогресса. Тот же порт, что у обычного просмотра:
// импортированная позиция ничем не отличается от заработанной.
type Progress interface {
	Get(ctx context.Context, u kernel.UserID, e kernel.EpisodeID) (playback.Progress, error)
	Upsert(ctx context.Context, p playback.Progress) error
}

// Marks — намеренные отметки. Список «я смотрю» у площадки означает именно
// слежение, поэтому перенос его так и записывает: иначе человек потерял бы
// не только позиции, но и сам факт, что он за сериалом следит.
//
// Папка закладок — это «буду смотреть»: человек откладывал туда то, до чего
// хотел добраться, а не то, за чем следит.
type Marks interface {
	// at — время отметки, по нему сортируются списки.
	SetFollowing(ctx context.Context, u kernel.UserID, t kernel.TitleID, on bool, at time.Time) error
	SetWatchlist(ctx context.Context, u kernel.UserID, t kernel.TitleID, on bool, at time.Time) error
}

type Clock interface{ Now() time.Time }

type Service struct {
	catalog  Catalog
	progress Progress
	marks    Marks
	clock    Clock
	log      *slog.Logger
	policy   auth.Policy
}

func NewService(c Catalog, p Progress, m Marks, clock Clock, log *slog.Logger) *Service {
	return &Service{catalog: c, progress: p, marks: m, clock: clock, log: log}
}

// Result — итог переноса.
type Result struct {
	Seen      int // записей у площадки
	Imported  int // легло в прогресс
	Fetched   int // тайтлов дотянуто в каталог по ходу
	NoEpisode int // серии не нашлось даже после дотягивания
	Skipped   int // у пользователя уже есть своя позиция
}

// WatchlistResult — итог быстрого переноса списка.
type WatchlistResult struct {
	Seen  int // тайтлов во вкладке «я смотрю»
	Added int // заведено в каталоге
}

// FolderResult — итог переноса папки закладок.
type FolderResult struct {
	Seen    int // записей в папке
	Added   int // разных тайтлов легло в «буду смотреть»
	Repeats int // записи-повторы: площадка держит один тайтл в папке дважды
}

// ImportWatchlist переносит СПИСОК просматриваемого, без позиций.
//
// Два запроса и секунды вместо минуты: позиции внутри серий сюда не входят
// и подтягиваются, когда тайтл действительно открыли. Полный перенос
// остаётся отдельным действием для тех, кому нужна вся история разом.
func (s *Service) ImportWatchlist(ctx context.Context, a auth.Actor, target kernel.UserID, src Source) (WatchlistResult, error) {
	if err := s.policy.CanImportFor(a, target); err != nil {
		return WatchlistResult{}, err
	}
	titles, err := src.Watchlist(ctx)
	if err != nil {
		return WatchlistResult{}, err
	}
	res := WatchlistResult{Seen: len(titles)}
	base := s.clock.Now()
	for i, t := range titles {
		id, err := s.catalog.UpsertTitle(ctx, t)
		if err != nil {
			s.log.WarnContext(ctx, "тайтл списка не сохранён", "ref", t.Ref.String(), "err", err)
			continue
		}
		// Список «я смотрю» — это слежение, а не просто наличие позиции.
		// Дат у площадки нет, есть порядок: первый — выше всех.
		if err := s.marks.SetFollowing(ctx, target, id, true, inOrder(base, i)); err != nil {
			s.log.WarnContext(ctx, "слежение не проставлено", "ref", t.Ref.String(), "err", err)
		}
		res.Added++
	}
	s.log.InfoContext(ctx, "список просматриваемого перенесён",
		"user", target, "видно", res.Seen, "заведено", res.Added)
	return res, nil
}

// Folders — папки закладок площадки, чтобы админ выбрал нужную.
func (s *Service) Folders(ctx context.Context, a auth.Actor, src Source) ([]dp.BookmarkFolder, error) {
	if err := s.policy.CanAdminister(a); err != nil {
		return nil, err
	}
	return src.BookmarkFolders(ctx)
}

// ImportFolder переносит папку закладок в «буду смотреть» пользователя.
//
// Запрос на каждые двадцать тайтлов и ни одного на тайтл: элементы папки
// несут то же, что страница каталога, и недостающий тайтл заводится из
// них; серии догрузятся, когда его откроют. Повторный перенос ничего не
// портит — отметка просто ставится ещё раз.
func (s *Service) ImportFolder(ctx context.Context, a auth.Actor, target kernel.UserID, src Source,
	folderID string) (FolderResult, error) {
	if err := s.policy.CanImportFor(a, target); err != nil {
		return FolderResult{}, err
	}
	titles, err := src.BookmarkFolder(ctx, folderID)
	if err != nil {
		return FolderResult{}, err
	}
	res := FolderResult{Seen: len(titles)}
	added := map[kernel.TitleID]bool{}
	base := s.clock.Now()
	for i, t := range titles {
		id, err := s.catalog.UpsertTitle(ctx, t)
		if err != nil {
			s.log.WarnContext(ctx, "тайтл папки не сохранён", "ref", t.Ref.String(), "err", err)
			continue
		}
		if added[id] {
			res.Repeats++
			continue
		}
		// Даты добавления в папку площадка не отдаёт, только порядок — от
		// последних добавленных к первым. Его и держим: верхний в папке
		// будет верхним в «Буду смотреть».
		if err := s.marks.SetWatchlist(ctx, target, id, true, inOrder(base, i)); err != nil {
			return res, fmt.Errorf("буду смотреть %s: %w", t.Ref, err)
		}
		added[id] = true
		res.Added++
	}
	s.log.InfoContext(ctx, "папка закладок перенесена",
		"user", target, "папка", folderID, "видно", res.Seen, "перенесено", res.Added, "повторов", res.Repeats)
	return res, nil
}

// ImportRecent переносит журнал просмотров площадки за последние дни — с
// настоящими датами, поэтому «История» и «Продолжить смотреть» встают в
// том порядке, в каком человек смотрел. Сериалы из журнала ставятся на
// слежение: что смотрели эти дни, за тем и следят.
//
// Нужен новичку, который до сих пор смотрел через общее приложение
// площадки: общий «я смотрю» за годы — это вся семья, а журнал последних
// недель — в основном он сам (остальные уже смотрят здесь, а платформа в
// площадку не пишет).
func (s *Service) ImportRecent(ctx context.Context, a auth.Actor, target kernel.UserID, src Source,
	since time.Time) (Result, error) {
	if err := s.policy.CanImportFor(a, target); err != nil {
		return Result{}, err
	}
	records, err := src.RecentHistory(ctx, since)
	if err != nil {
		return Result{}, err
	}
	var res Result
	fetched := map[kernel.ProviderRef]bool{}
	followed := map[kernel.ProviderRef]bool{}
	for _, w := range records {
		if err := s.apply(ctx, target, src, fetched, w, &res); err != nil {
			return res, err
		}
		if w.SeasonNo == 0 || followed[w.Ref] {
			continue
		}
		followed[w.Ref] = true
		if id, err := s.catalog.TitleIDByProviderRef(ctx, w.Ref); err == nil {
			// Журнал идёт от свежих: первая запись сериала — последний
			// просмотр, его дата и становится датой слежения.
			at := w.WatchedAt
			if at.IsZero() {
				at = s.clock.Now()
			}
			if err := s.marks.SetFollowing(ctx, target, id, true, at); err != nil {
				s.log.WarnContext(ctx, "слежение не проставлено", "ref", w.Ref.String(), "err", err)
			}
		}
	}
	s.log.InfoContext(ctx, "журнал просмотров перенесён",
		"user", target, "с", since, "видно", res.Seen, "перенесено", res.Imported,
		"дотянуто_тайтлов", res.Fetched, "не_нашлось", res.NoEpisode, "пропущено", res.Skipped)
	return res, nil
}

// ImportFor переносит позиции ОДНОГО тайтла. Ленивая половина: вызывается,
// когда тайтл открыли, и стоит один запрос вместо обхода всего списка.
func (s *Service) ImportFor(ctx context.Context, a auth.Actor, target kernel.UserID, src Source,
	ref kernel.ProviderRef) (Result, error) {
	if err := s.policy.CanImportFor(a, target); err != nil {
		return Result{}, err
	}
	records, err := src.WatchHistoryFor(ctx, ref)
	if err != nil {
		return Result{}, err
	}
	var res Result
	fetched := map[kernel.ProviderRef]bool{}
	for _, w := range records {
		if err := s.apply(ctx, target, src, fetched, w, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// Import переносит позиции всего списка «я смотрю» на аккаунт target.
func (s *Service) Import(ctx context.Context, a auth.Actor, target kernel.UserID, src Source) (Result, error) {
	if err := s.policy.CanImportFor(a, target); err != nil {
		return Result{}, err
	}
	var res Result
	fetched := map[kernel.ProviderRef]bool{}

	err := src.ImportWatchHistory(ctx, func(w dp.WatchRecord) error {
		return s.apply(ctx, target, src, fetched, w, &res)
	})
	if err != nil {
		return res, err
	}
	s.log.InfoContext(ctx, "история перенесена",
		"user", target, "видно", res.Seen, "перенесено", res.Imported,
		"дотянуто_тайтлов", res.Fetched, "не_нашлось", res.NoEpisode,
		"пропущено", res.Skipped)
	return res, nil
}

// apply переносит ОДНУ позицию. Общая середина обоих путей — полного
// переноса и точечного: дублировать её значило бы получить два слегка
// разных поведения там, где нужно одно.
func (s *Service) apply(ctx context.Context, user kernel.UserID, src Source,
	fetched map[kernel.ProviderRef]bool, w dp.WatchRecord, res *Result) error {
	res.Seen++

	ep, err := s.catalog.EpisodeIDByProviderRef(ctx, w.Ref, w.SeasonNo, w.EpisodeNo)
	if errors.Is(err, kernel.ErrNotFound) {
		// Тайтла ещё нет в каталоге. Дотягиваем именно его, а не ждём
		// полного обхода: история сама говорит, что нужно. Кешируем по
		// ссылке — у сериала десятки серий, и тянуть его на каждую
		// было бы расточительно.
		if fetched != nil && !fetched[w.Ref] {
			fetched[w.Ref] = true
			if s.ensureTitle(ctx, src, w.Ref) {
				res.Fetched++
			}
		}
		ep, err = s.catalog.EpisodeIDByProviderRef(ctx, w.Ref, w.SeasonNo, w.EpisodeNo)
	}
	if err != nil {
		if errors.Is(err, kernel.ErrNotFound) {
			// Серии нет даже у площадки — например, расходится нумерация.
			res.NoEpisode++
			return nil
		}
		return err
	}

	// Уже заработанная позиция важнее импортированной: перенос не должен
	// откатывать назад то, что человек посмотрел уже здесь.
	if cur, err := s.progress.Get(ctx, user, ep); err == nil && cur.PositionMS > 0 {
		res.Skipped++
		return nil
	} else if err != nil && !errors.Is(err, kernel.ErrNotFound) {
		return err
	}

	// Дата просмотра у площадки, если она известна: по ней сортируются
	// «История» и «Продолжить смотреть».
	at := s.clock.Now()
	if !w.WatchedAt.IsZero() && w.WatchedAt.Before(at) {
		at = w.WatchedAt
	}
	p := playback.Progress{
		UserID:     user,
		EpisodeID:  ep,
		PositionMS: int64(w.PositionSec) * 1000,
		DurationMS: int64(w.DurationSec) * 1000,
		UpdatedAt:  at,
		ClientTS:   at,
		DeviceID:   "import:" + w.Ref.ProviderID,
	}
	if w.Completed {
		p.Status = playback.StatusCompleted
	} else {
		p.Status = playback.DeriveStatus(p)
	}
	if err := s.progress.Upsert(ctx, p); err != nil {
		return fmt.Errorf("перенос %s s%02de%02d: %w", w.Ref, w.SeasonNo, w.EpisodeNo, err)
	}
	res.Imported++
	return nil
}

// inOrder — время отметки i-го элемента списка площадки, у которого есть
// порядок, но нет дат: каждый следующий на миллисекунду раньше, и список
// встаёт так же, как у площадки. Миллисекунда, а не секунда: папка в
// четыреста тайтлов укладывается в полсекунды и не перемешивается с тем,
// что перенесли минутой раньше.
func inOrder(base time.Time, i int) time.Time {
	return base.Add(-time.Duration(i) * time.Millisecond)
}

// ensureTitle заводит тайтл и его серии в каталоге.
//
// Отказ по одному тайтлу не отменяет перенос остальных: площадка может
// быть недоступна на середине, и половина истории лучше, чем ничего.
func (s *Service) ensureTitle(ctx context.Context, src Source, ref kernel.ProviderRef) bool {
	t, err := src.Title(ctx, ref)
	if err != nil {
		s.log.WarnContext(ctx, "тайтл не получен", "ref", ref.String(), "err", err)
		return false
	}
	id, err := s.catalog.UpsertTitle(ctx, t)
	if err != nil {
		s.log.WarnContext(ctx, "тайтл не сохранён", "ref", ref.String(), "err", err)
		return false
	}
	eps, err := src.Episodes(ctx, ref)
	if err != nil {
		s.log.WarnContext(ctx, "серии не получены", "ref", ref.String(), "err", err)
		return false
	}
	if err := s.catalog.UpsertEpisodes(ctx, id, eps); err != nil {
		s.log.WarnContext(ctx, "серии не сохранены", "ref", ref.String(), "err", err)
		return false
	}
	return true
}
