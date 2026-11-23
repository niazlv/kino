package playback

import (
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type PlayerImpl string

const (
	PlayerHLSJS  PlayerImpl = "hlsjs"
	PlayerNative PlayerImpl = "native_hls"
)

type EndReason string

const (
	EndCompleted EndReason = "completed"
	EndAbandoned EndReason = "abandoned"
	EndError     EndReason = "error"
)

// Session — одна строка на просмотр, а не на запрос. Двухчасовой фильм это
// ~2400 запросов сегментов; посессионная запись отвечает на те же вопросы
// в тысячи раз дешевле. IP здесь намеренно нет.
type Session struct {
	ID        kernel.SessionID
	UserID    kernel.UserID
	EpisodeID kernel.EpisodeID
	Provider  string

	StartedAt time.Time
	EndedAt   *time.Time

	// Поля, совпадающие с ветвлениями архитектуры: без них жалоба
	// «на айфоне тормозит» неотлаживаема — не отличить сеть от бага
	// в одной из двух реализаций плеера.
	DeviceKind string
	AppVersion string
	PlayerImpl PlayerImpl
	Delivery   DeliveryMode

	TTFFMS          int64
	RebufferCount   int
	RebufferMSTotal int64
	BitrateSwitches int
	MaxHeight       int

	BytesServed    int64
	SegmentsServed int64

	EndReason EndReason
	ErrorCode string
}

// Healthy — грубый признак «просмотр прошёл нормально», по которому
// строится суточный агрегат.
func (s Session) Healthy() bool {
	return s.EndReason != EndError && s.RebufferCount <= 3 && s.TTFFMS < 5000
}
