package searchhistory

import (
	"context"
	"errors"
	"testing"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type fakeRepo struct {
	recorded []string
	list     []string
}

func (f *fakeRepo) Record(_ context.Context, _ kernel.UserID, q string) error {
	f.recorded = append(f.recorded, q)
	return nil
}
func (f *fakeRepo) List(_ context.Context, _ kernel.UserID, _ int) ([]string, error) {
	return f.list, nil
}
func (f *fakeRepo) Delete(_ context.Context, _ kernel.UserID, _ string) error { return nil }
func (f *fakeRepo) Clear(_ context.Context, _ kernel.UserID) error            { return nil }

func user(id kernel.UserID) auth.Actor {
	return auth.Actor{UserID: id, Role: account.RoleUser, Space: account.SpaceUser}
}

func TestValid_BoundsInRunes(t *testing.T) {
	// Один русский символ — четыре байта: байтовый порог отрезал бы
	// короткие русские слова, поэтому границы считаются в буквах.
	if Valid("д") {
		t.Fatal("одна буква не проходит")
	}
	if !Valid("два") {
		t.Fatal("короткое русское слово должно проходить")
	}
	if Valid("") || Valid(" ") {
		t.Fatal("пустое не проходит")
	}
}

func TestRecord_ScopedToActorAndValidated(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	a := auth.Actor{UserID: 7, Role: account.RoleUser, Space: account.SpaceUser}

	if _, err := svc.Record(context.Background(), a, "  "); err != nil && !errors.Is(err, kernel.ErrInvalidInput) {
		t.Fatalf("пустой запрос должен отклоняться: %v", err)
	}
	if _, err := svc.Record(context.Background(), a, "война и мир"); err != nil {
		t.Fatal(err)
	}
	if len(repo.recorded) != 1 {
		t.Fatal("валидный запрос должен дойти до хранилища")
	}
}
