// Package crypto — реализации криптографических портов app/auth.
// Криптография это инфраструктура: домен и use cases про argon2 не знают.
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type params struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
	saltLen uint32
}

var defaults = params{memory: 64 * 1024, time: 1, threads: 4, keyLen: 32, saltLen: 16}

type Argon2Hasher struct{ p params }

func NewArgon2Hasher() *Argon2Hasher { return &Argon2Hasher{p: defaults} }

func (h *Argon2Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return h.encode(password, salt, h.p), nil
}

// Verify постоянного времени. Ключевое свойство: на пустом или битом хеше
// работа НЕ пропускается — иначе «пользователь не найден» отличается от
// «неверный пароль» по времени ответа, и логины перебираются.
func (h *Argon2Hasher) Verify(password, hash string) bool {
	p, salt, want, err := decode(hash)
	if err != nil {
		// Считаем впустую с дефолтными параметрами и отбрасываем результат.
		_ = argon2.IDKey([]byte(password), make([]byte, defaults.saltLen),
			defaults.time, defaults.memory, defaults.threads, defaults.keyLen)
		return false
	}
	got := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (h *Argon2Hasher) encode(password string, salt []byte, p params) string {
	key := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads, b64(salt), b64(key))
}

var errBadHash = errors.New("malformed argon2 hash")

func decode(s string) (params, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return params{}, nil, nil, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return params{}, nil, nil, errBadHash
	}
	var p params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return params{}, nil, nil, errBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return params{}, nil, nil, errBadHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return params{}, nil, nil, errBadHash
	}
	p.keyLen = uint32(len(key))
	p.saltLen = uint32(len(salt))
	return p, salt, key, nil
}

// ── Токены ─────────────────────────────────────────────────────────────────

type TokenGen struct{}

func NewTokenGen() TokenGen { return TokenGen{} }

// NewToken возвращает сырой токен клиенту и его хеш для хранения.
// В БД едет только хеш: утечка дампа не должна давать чужие сессии.
func (TokenGen) NewToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw), nil
}

func (TokenGen) HashToken(raw string) string { return hashToken(raw) }

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// NewInviteCode — короткий, но неугадываемый; читается вслух по телефону.
func (TokenGen) NewInviteCode() string {
	out := readable("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 10) // без похожих символов
	return out[:5] + "-" + out[5:]
}

// NewUserCode — код для человека при входе устройства. Восемь знаков из
// 32 — 40 бит: за десять минут жизни кода не перебирается, а с экрана
// телевизора набирается без ошибок. Дефис посередине — для глаз.
func (TokenGen) NewUserCode() string {
	out := readable("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 8)
	return out[:4] + "-" + out[4:]
}

// NewPassword — временный пароль, который админ передаёт человеку голосом
// или сообщением. Строчные без l/o/i и цифры, группами по четыре: диктуется
// без ошибок, а 16 знаков из 32 дают 80 бит — с запасом для временного.
func (TokenGen) NewPassword() string {
	out := readable("abcdefghjkmnpqrstuvwxyz123456789", 16)
	return out[:4] + "-" + out[4:8] + "-" + out[8:12] + "-" + out[12:]
}

// readable набирает n символов из алфавита. Алфавит обязан быть длиной
// ровно 32: тогда остаток от деления байта не смещает распределение.
func readable(alphabet string, n int) string {
	if len(alphabet) != 32 {
		panic("crypto: алфавит должен быть из 32 символов")
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i, v := range b {
		b[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(b)
}
