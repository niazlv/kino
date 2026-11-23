package account

import (
	"unicode/utf8"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Credential живёт отдельно от User намеренно: список пользователей,
// админские экраны и любой обычный запрос никогда не поднимают хеш пароля
// из БД просто потому, что его нет в структуре User.
type Credential struct {
	UserID kernel.UserID
	Hash   string // формат задаёт адаптер (argon2id), домен в него не смотрит
}

const (
	MinPasswordLen = 10
	MinLoginLen    = 3
	MaxLoginLen    = 32
)

func ValidatePassword(p string) error {
	// Считаем СИМВОЛЫ, а не байты. len() на кириллице даёт вдвое больше:
	// пароль из шести русских букв — это двенадцать байт, и правило
	// «минимум десять» пропускало его, хотя должно было отвергнуть.
	if utf8.RuneCountInString(p) < MinPasswordLen {
		return kernel.ErrInvalidInput
	}
	return nil
}

func ValidateLogin(l string) error {
	if len(l) < MinLoginLen || len(l) > MaxLoginLen {
		return kernel.ErrInvalidInput
	}
	for _, r := range l {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return kernel.ErrInvalidInput
		}
	}
	return nil
}
