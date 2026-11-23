// Package share — ссылки-разрешения.
//
// Открываются без входа: токен в адресе и есть право доступа. Поэтому у них
// обязательно есть срок, отзыв и учёт обращений — раздача доступа к платному
// контенту без возможности передумать была бы безответственной.
package share

import (
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Kind — что именно разрешает ссылка.
type Kind string

const (
	// KindWatch — посмотреть серию в браузере, без входа.
	KindWatch Kind = "watch"
	// KindDownload — забрать манифест для скачивания утилитой.
	KindDownload Kind = "download"
)

func (k Kind) Valid() bool { return k == KindWatch || k == KindDownload }

// Selection — что скачивать. Пустые списки означают «всё»: по умолчанию
// человек хочет полный файл, а сужение — осознанное действие.
type Selection struct {
	MaxHeight int      `json:"maxHeight,omitempty"`
	Audios    []string `json:"audios,omitempty"`
	Subtitles []string `json:"subtitles,omitempty"`
}

// All сообщает, что ничего не сужали.
func (s Selection) All() bool {
	return s.MaxHeight == 0 && len(s.Audios) == 0 && len(s.Subtitles) == 0
}

// Link — выданная ссылка.
type Link struct {
	Token     string
	Kind      Kind
	EpisodeID kernel.EpisodeID
	CreatedBy kernel.UserID
	Selection Selection
	ExpiresAt time.Time
	RevokedAt *time.Time
	Hits      int
	LastHitAt *time.Time
	CreatedAt time.Time
}

// Сроки жизни по умолчанию.
//
// У скачивания он короткий: манифест нужен один раз, в момент запуска
// утилиты. У просмотра длиннее — ссылку могут открыть не сразу.
const (
	DownloadTTL = 2 * time.Hour
	WatchTTL    = 48 * time.Hour
	MaxTTL      = 30 * 24 * time.Hour
)

// Usable проверяет, можно ли ещё воспользоваться ссылкой.
func (l Link) Usable(now time.Time) error {
	switch {
	case l.RevokedAt != nil:
		return kernel.ErrForbidden
	case now.After(l.ExpiresAt):
		return kernel.ErrExpired
	}
	return nil
}

// DefaultTTL для вида ссылки.
func DefaultTTL(k Kind) time.Duration {
	if k == KindWatch {
		return WatchTTL
	}
	return DownloadTTL
}
