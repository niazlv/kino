package account

import "github.com/niazleushkin/kino/internal/domain/kernel"

// Marks — намеренные отметки пользователя о тайтле.
//
// Отдельно от позиции просмотра: позиция ВЫВОДИТСЯ из того, что человек
// включил, а слежение и закладка — это его решение. Случайно открытая
// серия не должна объявлять сериал отслеживаемым.
type Marks struct {
	UserID    kernel.UserID
	TitleID   kernel.TitleID
	Following bool
	Watchlist bool
	Hidden    bool
}

// TitlePrefs — что человек выбрал в плеере для этого тайтла.
//
// Отдельно от Marks и пишется отдельной операцией: отметки заменяются
// целиком, и если бы предпочтения ехали в том же теле, нажатие «Слежу»
// затирало бы только что выбранную в плеере дорожку.
type TitlePrefs struct {
	UserID  kernel.UserID
	TitleID kernel.TitleID
	// Audio — подпись дорожки (студия озвучки), а не её номер: номера у
	// серий одного сериала расходятся, подпись держится. nil — не выбирал.
	Audio *string
	// Subtitle: nil — не выбирал, пустая строка — выключил намеренно.
	Subtitle *string
	// Source — площадка, выбранная для тайтла (у аниме-площадок выбор
	// озвучки — это и выбор площадки). nil — не выбирал, пустая строка —
	// вернул «автоматически», по приоритету.
	Source *string
}

// prefMaxLen ограничивает подпись: это строка из манифеста, а не сочинение.
const prefMaxLen = 120

// Valid: подписи короткие; nil — законное «не менять».
func (p TitlePrefs) Valid() bool {
	for _, s := range []*string{p.Audio, p.Subtitle, p.Source} {
		if s != nil && len(*s) > prefMaxLen {
			return false
		}
	}
	return true
}

// MarkKind — вид списка, который просят показать.
type MarkKind string

const (
	MarkFollowing MarkKind = "following"
	MarkWatchlist MarkKind = "watchlist"
)

// Valid проверяет вид списка, пришедший снаружи.
func (k MarkKind) Valid() bool {
	switch k {
	case MarkFollowing, MarkWatchlist:
		return true
	}
	return false
}
