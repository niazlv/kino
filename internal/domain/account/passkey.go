package account

import (
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Passkey — ключ WebAuthn, привязанный к пользователю.
//
// Второй фактор наравне с TOTP, а в паре с проверкой пользователя на
// устройстве (биометрия, PIN) — и самостоятельный вход: владение ключом
// плюс подтверждение личности и есть два фактора. Здесь только то, что
// нужно хранить; протокол живёт в адаптере.
type Passkey struct {
	ID             int64
	UserID         kernel.UserID
	CredentialID   []byte
	PublicKey      []byte
	AAGUID         []byte
	SignCount      uint32
	Transports     []string
	BackupEligible bool
	BackedUp       bool
	Name           string
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}
