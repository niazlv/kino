// Package searchhistory — история поисковых запросов пользователя.
//
// Функциональные данные пользователя (ADR-0005): своя у каждого, живёт на
// сервере — телевизор, телефон и браузер видят один и тот же список, а не
// свои локальные обрывки. Отдельно от каталога: это не находки, а след
// вопросов, которые человек уже задавал.
package searchhistory

import (
	"context"
	"strings"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type Repo interface {
	Record(ctx context.Context, u kernel.UserID, q string) error
	List(ctx context.Context, u kernel.UserID, limit int) ([]string, error)
	Delete(ctx context.Context, u kernel.UserID, q string) error
	Clear(ctx context.Context, u kernel.UserID) error
}

// listLimit — сколько подсказок отдаётся разом; храним столько же.
const listLimit = 20

// queryMin/queryMax — границы запроса в буквах, а не байтах: кириллица
// занимает четыре байта, и байтовый порог резал бы короткие русские слова.
const (
	queryMin = 2
	queryMax = 200
)

type Service struct {
	repo   Repo
	policy auth.Policy
}

func NewService(repo Repo) *Service { return &Service{repo: repo} }

// Valid проверяет запрос на осмысленность: пустые и односимвольные
// подсказками не бывают, слишком длинные — не запрос, а сочинение.
func Valid(q string) bool {
	n := len([]rune(q))
	return n >= queryMin && n <= queryMax
}

func (s *Service) List(ctx context.Context, a auth.Actor) ([]string, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, a.UserID, listLimit)
}

// Record — пользователь берётся из сессии: чужую историю наполнять нельзя.
func (s *Service) Record(ctx context.Context, a auth.Actor, q string) ([]string, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if !Valid(q) {
		return nil, kernel.ErrInvalidInput
	}
	if err := s.repo.Record(ctx, a.UserID, q); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, a.UserID, listLimit)
}

func (s *Service) Delete(ctx context.Context, a auth.Actor, q string) ([]string, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return nil, err
	}
	if err := s.repo.Delete(ctx, a.UserID, q); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, a.UserID, listLimit)
}

func (s *Service) Clear(ctx context.Context, a auth.Actor) ([]string, error) {
	if err := s.policy.CanWriteProgress(a, a.UserID); err != nil {
		return nil, err
	}
	if err := s.repo.Clear(ctx, a.UserID); err != nil {
		return nil, err
	}
	return []string{}, nil
}
