// Package marks — намеренные отметки пользователя о тайтлах.
//
// Существует отдельно от прогресса, потому что это разные по природе вещи:
// позиция ВЫВОДИТСЯ из того, что человек включил, а «слежу» и «буду
// смотреть» — его решение. Смешать их значит объявить слежением случайно
// открытую серию.
//
// Одно исключение: досмотренная или отмеченная серия идущего сериала
// ставит «Слежу» сама (progress.Follower) — иначе «Новые эпизоды» молчали
// бы о том, что человек смотрит. Открытая серия слежения не ставит, а
// снятое здесь руками автоматика не возвращает.
package marks

import (
	"context"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type Repo interface {
	Get(ctx context.Context, u kernel.UserID, t kernel.TitleID) (account.Marks, error)
	Set(ctx context.Context, m account.Marks) error
	List(ctx context.Context, u kernel.UserID, kind account.MarkKind,
		limit, offset int) ([]catalog.Title, int, error)
	Prefs(ctx context.Context, u kernel.UserID, t kernel.TitleID) (account.TitlePrefs, error)
	SetPrefs(ctx context.Context, p account.TitlePrefs) error
}

type Service struct {
	repo   Repo
	policy auth.Policy
}

func NewService(repo Repo) *Service { return &Service{repo: repo} }

// Get отдаёт отметки действующего пользователя. Чужие недоступны: отметки
// личные, в этом весь смысл платформы.
func (s *Service) Get(ctx context.Context, a auth.Actor, t kernel.TitleID) (account.Marks, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return account.Marks{}, err
	}
	return s.repo.Get(ctx, a.UserID, t)
}

func (s *Service) Set(ctx context.Context, a auth.Actor, m account.Marks) (account.Marks, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return account.Marks{}, err
	}
	// Пользователь берётся из сессии, а не из тела запроса: иначе можно
	// было бы проставить отметки другому человеку.
	m.UserID = a.UserID
	if err := s.repo.Set(ctx, m); err != nil {
		return account.Marks{}, err
	}
	return m, nil
}

// Prefs — выбранные в плеере озвучка и субтитры этого тайтла.
func (s *Service) Prefs(ctx context.Context, a auth.Actor, t kernel.TitleID) (account.TitlePrefs, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return account.TitlePrefs{}, err
	}
	return s.repo.Prefs(ctx, a.UserID, t)
}

// SetPrefs запоминает выбор дорожки. nil-поле — «не менять»: плеер сообщает
// об одном переключении за раз и не обязан знать про второе поле.
func (s *Service) SetPrefs(ctx context.Context, a auth.Actor, p account.TitlePrefs) (account.TitlePrefs, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return account.TitlePrefs{}, err
	}
	if !p.Valid() {
		return account.TitlePrefs{}, kernel.ErrInvalidInput
	}
	p.UserID = a.UserID
	if err := s.repo.SetPrefs(ctx, p); err != nil {
		return account.TitlePrefs{}, err
	}
	return s.repo.Prefs(ctx, a.UserID, p.TitleID)
}

// List отдаёт страницу списка по виду отметки и общее число в нём.
// Страница, а не весь список: слежение живёт годами, и окно без прокрутки
// прятало бы хвост за капом. Размер страницы задаёт витрина.
func (s *Service) List(ctx context.Context, a auth.Actor, kind account.MarkKind,
	limit, offset int) ([]catalog.Title, int, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return nil, 0, err
	}
	if !kind.Valid() {
		return nil, 0, kernel.ErrInvalidInput
	}
	return s.repo.List(ctx, a.UserID, kind, limit, offset)
}
