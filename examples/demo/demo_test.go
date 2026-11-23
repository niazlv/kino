package demo

import (
	"context"
	"errors"
	"testing"

	"github.com/niazleushkin/kino/internal/adapter/providers/providertest"
	"github.com/niazleushkin/kino/internal/adapter/providers/source"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Пример проходит исполняемый контракт — тот же набор, что `sourcekit check`
// и приём правки (ADR-0017). Поток не трогаем: тест без сети.
func TestContract(t *testing.T) {
	rep := providertest.Run(context.Background(), New(), providertest.Case{ID: ProviderID, Query: "sintel", Want: "Синтел"})
	if rep.Failed() {
		t.Fatal(rep.String())
	}
	for _, s := range rep.Steps {
		if s.Warn {
			t.Errorf("предупреждение: %s — %s", s.Name, s.Detail)
		}
	}
}

// Заявленный Lookup работает: тайтл находится по IMDb, чужой номер —
// NotFound.
func TestLookup(t *testing.T) {
	var _ appprov.Lookup = New()
	ti, err := New().Lookup(context.Background(), dp.ExternalIDs{IMDB: "tt1254207"})
	if err != nil || ti.Ref.ItemID != "big-buck-bunny" {
		t.Fatalf("%+v %v", ti, err)
	}
	if _, err := New().Lookup(context.Background(), dp.ExternalIDs{IMDB: "tt0000001"}); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("чужой номер: %v", err)
	}
}

// Обход отдаёт весь каталог с нуля и ничего — после последней правки.
func TestChangedSince(t *testing.T) {
	n := 0
	_ = New().ChangedSince(context.Background(), updated.AddDate(0, 0, -1), func(dp.Title) error { n++; return nil })
	if n != len(catalog) {
		t.Fatalf("с нуля: %d из %d", n, len(catalog))
	}
	n = 0
	_ = New().ChangedSince(context.Background(), updated, func(dp.Title) error { n++; return nil })
	if n != 0 {
		t.Fatalf("после правки: %d", n)
	}
}

// Описание зарегистрировано и годно: в публичной сборке это площадка по
// умолчанию, а на телефоне с настоящими площадками она не показывается.
func TestSpec(t *testing.T) {
	s, ok := source.Get(ProviderID)
	if !ok || !s.Default || !s.Hidden || s.Kind != source.KindGeneral {
		t.Fatalf("%+v %v", s, ok)
	}
	p, err := s.New(source.Env{})
	if err != nil || p == nil {
		t.Fatalf("New: %v", err)
	}
}
