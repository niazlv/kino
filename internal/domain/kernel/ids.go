package kernel

import "fmt"

// Типизированные ID: перепутать UserID и TitleID не даёт компилятор.
type (
	UserID    int64
	TitleID   int64
	EpisodeID int64
	SessionID string
	TicketID  string
)

// ProviderRef — адрес сущности внутри конкретной площадки.
// Пользовательское состояние на него НЕ ключуется: только на канонические ID,
// иначе подключение второго провайдера уничтожит историю просмотров.
type ProviderRef struct {
	ProviderID string
	ItemID     string
}

func (r ProviderRef) String() string { return fmt.Sprintf("%s:%s", r.ProviderID, r.ItemID) }
func (r ProviderRef) Zero() bool     { return r.ProviderID == "" || r.ItemID == "" }
