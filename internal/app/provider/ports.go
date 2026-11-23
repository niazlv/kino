// Package provider — оркестрация провайдеров на стороне use cases.
//
// Здесь объявлены интерфейсы двух плоскостей и реестр живых провайдеров.
// Конкретные реализации живут в providers/* (ADR-0018) и сюда не
// импортируются: depguard это запрещает, и в этом смысл.
package provider

import (
	"context"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Catalog — медленная плоскость: метаданные, кешируются надолго.
type Catalog interface {
	Describe(ctx context.Context) (dp.Descriptor, error)
	Search(ctx context.Context, query string, limit int) ([]dp.Title, error)
	Title(ctx context.Context, ref kernel.ProviderRef) (dp.Title, error)
	Episodes(ctx context.Context, ref kernel.ProviderRef) ([]dp.Episode, error)
	// ChangedSince обходит изменения с момента since. Останавливается сам,
	// как только вышел за границу: полный обход ради вчерашних правок —
	// это часы работы и лишняя нагрузка на площадку.
	ChangedSince(ctx context.Context, since time.Time, fn func(dp.Title) error) error
}

// Playback — быстрая плоскость: результат живёт часы, не кешируется надолго.
type Playback interface {
	Resolve(ctx context.Context, req dp.ResolveRequest) (dp.Source, error)
}

// Provider — обе плоскости вместе. Плагин вправе реализовать только одну,
// объявив это через Capabilities.
type Provider interface {
	Catalog
	Playback
}

// Thumbnails — кадр серии по запросу. Необязательная часть провайдера:
// kinopub отдаёт кадры вместе с сериями, аниме-площадкам нужен запрос на
// каждую серию.
type Thumbnails interface {
	EpisodeThumbnail(ctx context.Context, ref kernel.ProviderRef, season, episode int) (string, error)
}

// Lookup — произведение у площадки по внешним номерам. Необязательная часть
// провайдера: балансеры (Alloha) ключуют каталог номером Кинопоиска, и
// такой поиск точен и стоит один запрос — в отличие от поиска по названию.
// Нет такого произведения — kernel.ErrNotFound.
type Lookup interface {
	Lookup(ctx context.Context, ids dp.ExternalIDs) (dp.Title, error)
}

// Voices — озвучки серии без выдачи потока. Необязательная часть провайдера
// (CapVoices): у площадок, где каждая озвучка — свой поток, это первая,
// дешёвая половина резолва; у kinopub — дорожки из того же ответа.
// Серии нет — kernel.ErrNotFound.
type Voices interface {
	Voices(ctx context.Context, ref kernel.ProviderRef, season, episode int) ([]dp.Voice, error)
}

// Segments — разметка пропуска серии по запросу (CapSegments).
// Необязательная часть провайдера: у кого разметка приходит вместе с
// потоком, отдают её в Source.Segments. req — тот же запрос, что и на
// резолв: разметка привязана к потоку, а значит и к варианту (озвучке).
// Разметки нет — kernel.ErrNotFound.
type Segments interface {
	Segments(ctx context.Context, req dp.ResolveRequest) ([]dp.Segment, error)
}
