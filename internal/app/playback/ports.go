package playback

import (
	"context"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/domain/provider"
)

// Порты объявлены там, где ПОТРЕБЛЯЮТСЯ, а не там, где реализуются —
// «accept interfaces, return structs». Отдельного пакета port/ нет:
// он превратился бы в свалку интерфейсов без владельца.
//
// Всё, что ниже, реализуется в internal/adapter/*. Слой app об этом не знает
// и не может узнать: depguard запрещает импорт adapter из app.

type TitleRepo interface {
	Episode(ctx context.Context, id kernel.EpisodeID) (catalog.Episode, error)
	Title(ctx context.Context, id kernel.TitleID) (catalog.Title, error)
	// Offers — какие площадки отдают серию и под какими номерами у себя.
	Offers(ctx context.Context, id kernel.EpisodeID) ([]catalog.Offer, error)
}

// TicketStore — Redis. Единственный писатель core, единственный читатель
// gateway. Разделяемое состояние оправдано тем, что альтернатива —
// синхронный RPC на горячем пути воспроизведения.
type TicketStore interface {
	Put(ctx context.Context, t playback.Ticket, ttl time.Duration) error
	Get(ctx context.Context, id kernel.TicketID) (playback.Ticket, error)
}

// Resolver — фасад над реестром провайдеров. app/playback не знает ни одного
// конкретного провайдера и не импортирует сгенерированный gRPC-код.
type Resolver interface {
	Resolve(ctx context.Context, req provider.ResolveRequest) (provider.Source, error)
}

// Health — какие площадки сейчас не отвечают (app/health). Лежащая
// спрашивается последней, а исход каждого резолва возвращается сюда.
type Health interface {
	Down(id string) bool
	// NeedsReauth — лежит потому, что отвергла вход (kernel.ErrReauth).
	NeedsReauth(id string) bool
	Failed(id string, cause error)
	Recovered(id string)
	// TryProbe занимает фоновую перепроверку лежащей площадки; ok=false —
	// не нужно или рано. Иначе done обязателен.
	TryProbe(id string) (done func(err error), ok bool)
}

type SessionSink interface {
	Open(ctx context.Context, s playback.Session) error
	Close(ctx context.Context, s playback.Session) error
}

type Clock interface{ Now() time.Time }

type IDGen interface{ NewTicketID() kernel.TicketID }
