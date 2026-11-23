// Package progress — прогресс просмотра как отдельный модуль, а не часть
// доставки. Это функциональные данные пользователя (ADR-0005): у них другой
// жизненный цикл, другая приватность и другие зависимости, чем у тикетов.
package progress

import (
	"context"
	"errors"
	"time"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

type Repo interface {
	Get(ctx context.Context, u kernel.UserID, e kernel.EpisodeID) (playback.Progress, error)
	Upsert(ctx context.Context, p playback.Progress) error
	SetWatched(ctx context.Context, u kernel.UserID, e kernel.EpisodeID, watched bool) error
	SetSeasonWatched(ctx context.Context, u kernel.UserID, t kernel.TitleID,
		season int, watched bool) (int64, error)
}

// ContinueRepo вынесен отдельным портом: список «продолжить смотреть» —
// это запрос со связями, а не чтение одной строки.
type ContinueRepo interface {
	Continue(ctx context.Context, u kernel.UserID, limit, offset int) ([]Item, int, error)
}

// Unhider снимает «скрыт» с тайтла, серию которого включили снова.
//
// Порт, а не вызов сервиса отметок: прогресс не должен знать, как устроены
// отметки, ему нужно одно действие с понятным смыслом.
type Unhider interface {
	UnhideByEpisode(ctx context.Context, u kernel.UserID, e kernel.EpisodeID) error
}

// Follower ставит «Слежу» сериалу, который человек смотрит: досмотрел
// серию или отметил её — ждёт следующую, и «Новые эпизоды» должны о ней
// сказать. Какой сериал годится и не снимал ли человек слежение сам, решает
// реализация; прогрессу нужен только повод.
type Follower interface {
	FollowByEpisode(ctx context.Context, u kernel.UserID, e kernel.EpisodeID) error
	FollowByTitle(ctx context.Context, u kernel.UserID, t kernel.TitleID) error
}

type Item struct {
	EpisodeID  kernel.EpisodeID
	TitleID    kernel.TitleID
	Title      string
	Kind       string
	Year       int
	HasArt     bool
	SeasonNo   int
	EpisodeNo  int
	PositionMS int64
	DurationMS int64
}

type Clock interface{ Now() time.Time }

type Service struct {
	repo   Repo
	list   ContinueRepo
	unhide Unhider
	follow Follower
	clock  Clock
	policy auth.Policy
}

// NewService: unhide может быть nil — тогда скрытие живёт, пока его не
// снимут руками.
func NewService(repo Repo, list ContinueRepo, unhide Unhider, clock Clock) *Service {
	return &Service{repo: repo, list: list, unhide: unhide, clock: clock}
}

// WithFollower включает «Слежу» само по досмотренной серии. Без него
// слежение ставится только руками.
func (s *Service) WithFollower(f Follower) *Service {
	s.follow = f
	return s
}

// followDone ставит слежение, если серия досмотрена именно этим отчётом.
// Повод — переход в «досмотрено», а не каждый отчёт: досмотренная серия
// шлёт отчёты до конца титров, и пересмотр старой серии — тоже не повод.
// Открытая и брошенная серия слежения не ставит: случайно включённое
// слежением не объявляется.
func (s *Service) followDone(ctx context.Context, wasDone bool, p playback.Progress) error {
	if s.follow == nil || wasDone || p.Status != playback.StatusCompleted {
		return nil
	}
	return s.follow.FollowByEpisode(ctx, p.UserID, p.EpisodeID)
}

// Report — слияние конфликта делает домен, сервис оркеструет и проверяет права.
func (s *Service) Report(ctx context.Context, a auth.Actor, in playback.Progress) (playback.Progress, error) {
	if err := s.policy.CanWriteProgress(a, in.UserID); err != nil {
		return playback.Progress{}, err
	}
	// Свежесть отчёта ставится ДО слияния и по серверным часам: слияние
	// сравнивает моменты приёма отчётов, а не показания client_ts устройств.
	in.UpdatedAt = s.clock.Now()
	cur, err := s.repo.Get(ctx, in.UserID, in.EpisodeID)
	wasDone := false
	switch {
	case err == nil:
		wasDone = cur.Status == playback.StatusCompleted
		in = cur.Merge(in)
	case errors.Is(err, kernel.ErrNotFound):
		in.Status = playback.DeriveStatus(in)
	default:
		return playback.Progress{}, err
	}
	// Слияние могло вернуть хранившуюся запись целиком — штамп обновляется
	// в любом случае: «трогали только что» не должно зависеть от ветки.
	in.UpdatedAt = s.clock.Now()
	if err := s.repo.Upsert(ctx, in); err != nil {
		return playback.Progress{}, err
	}
	// Включённая серия возвращает тайтл на главную: «скрыть» было решением
	// не видеть, а просмотр — более поздним решением обратного.
	if s.unhide != nil {
		if err := s.unhide.UnhideByEpisode(ctx, in.UserID, in.EpisodeID); err != nil {
			return playback.Progress{}, err
		}
	}
	if err := s.followDone(ctx, wasDone, in); err != nil {
		return playback.Progress{}, err
	}
	return in, nil
}

// ReportFromGateway — прогресс, восстановленный медиа-шлюзом из фактов
// отдачи сегментов.
//
// Внешний плеер (playExternal) назад ничего не присылает, а его байты идут
// через шлюз: докачал до сегмента — досмотрел до него. Actor'а у шлюза нет,
// подлинность держится на сервис-токене служебного пространства, а пользователь
// и серия приезжают из тикета. Правила слияния те же: внешний плеер и встроенный
// смотрят один и тот же просмотр, а не конкурируют.
//
// durationMS с плейлиста: у позиции, восстановленной по сегментам, иначе нет
// шанса стать «досмотрено» — долю длительности не вычислить. Хранившаяся
// длительность точнее (её мерил сам плеер), поэтому здесь она не затирает.
func (s *Service) ReportFromGateway(ctx context.Context,
	u kernel.UserID, e kernel.EpisodeID, positionMS, durationMS int64) error {
	in := playback.Progress{
		UserID: u, EpisodeID: e, PositionMS: positionMS, DurationMS: durationMS,
		UpdatedAt: s.clock.Now(),
	}
	cur, err := s.repo.Get(ctx, u, e)
	wasDone := false
	switch {
	case err == nil:
		wasDone = cur.Status == playback.StatusCompleted
		if in.DurationMS <= 0 {
			in.DurationMS = cur.DurationMS
		}
		in = cur.Merge(in)
	case errors.Is(err, kernel.ErrNotFound):
		in.Status = playback.DeriveStatus(in)
	default:
		return err
	}
	in.UpdatedAt = s.clock.Now()
	if err := s.repo.Upsert(ctx, in); err != nil {
		return err
	}
	// Включённая серия возвращает тайтл на главную — тем же правилом, что и
	// для отчётов встроенного плеера.
	if s.unhide != nil {
		if err := s.unhide.UnhideByEpisode(ctx, in.UserID, in.EpisodeID); err != nil {
			return err
		}
	}
	return s.followDone(ctx, wasDone, in)
}

// Get — чужой прогресс недоступен даже администратору без адресного гранта.
func (s *Service) Get(ctx context.Context, a auth.Actor, owner kernel.UserID,
	e kernel.EpisodeID, grant auth.DiagnosticGrant) (playback.Progress, error) {
	if err := s.policy.CanReadProgress(a, owner, grant); err != nil {
		return playback.Progress{}, err
	}
	return s.repo.Get(ctx, owner, e)
}

// Continue — то, ради чего затевалась платформа: свой список у каждого,
// и никто его не сбивает. Страницей с общим числом: список, который нельзя
// докрутить, — сломанная витрина, а не лимит.
func (s *Service) Continue(ctx context.Context, a auth.Actor, limit, offset int) ([]Item, int, error) {
	if err := s.policy.CanReadProgress(a, a.UserID, auth.DiagnosticGrant{}); err != nil {
		return nil, 0, err
	}
	return s.list.Continue(ctx, a.UserID, limit, offset)
}

// SetWatched — ручная отметка серии.
//
// Нужна потому, что автоматика знает только «докуда доиграл плеер».
// Человек может посмотреть серию не здесь, бросить на середине как
// неинтересную или наоборот вернуть в непросмотренные — и это его решение,
// а не следствие позиции.
func (s *Service) SetWatched(ctx context.Context, a auth.Actor,
	e kernel.EpisodeID, watched bool) error {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return err
	}
	if err := s.repo.SetWatched(ctx, a.UserID, e, watched); err != nil {
		return err
	}
	// Отметил просмотренной — смотрел, пусть и не здесь: так догоняют
	// сериал, начатый в другом приложении.
	if watched && s.follow != nil {
		return s.follow.FollowByEpisode(ctx, a.UserID, e)
	}
	return nil
}

// SetSeasonWatched — отметка сезона целиком.
//
// Отдельной операцией, а не циклом по сериям на клиенте: у сезона бывает
// под сотню серий, и сотня запросов ради одного нажатия — это и медленно,
// и наполовину применится при обрыве связи.
func (s *Service) SetSeasonWatched(ctx context.Context, a auth.Actor,
	t kernel.TitleID, season int, watched bool) (int64, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return 0, err
	}
	n, err := s.repo.SetSeasonWatched(ctx, a.UserID, t, season, watched)
	if err != nil {
		return 0, err
	}
	if watched && n > 0 && s.follow != nil {
		if err := s.follow.FollowByTitle(ctx, a.UserID, t); err != nil {
			return 0, err
		}
	}
	return n, nil
}
