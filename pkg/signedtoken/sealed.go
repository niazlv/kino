package signedtoken

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Sealer запечатывает короткие строки для передачи через URL.
//
// Шифрование, а не подпись, и вот почему обе половины нужны:
//
//	секретность — клиент не должен видеть адрес CDN. Подписанный, но
//	              открытый URL раскрывает источник каждому, кто откроет
//	              инструменты разработчика;
//	целостность — сервер по этой строке САМ ходит наружу. Возможность
//	              подделать её превращает прокси в открытый SSRF.
//
// AES-GCM даёт и то и другое разом. Состояние не нужно: расшифровать может
// только владелец ключа, поэтому хранить нечего.
type Sealer struct{ aead cipher.AEAD }

// ErrSealed возвращается на любой непригодной строке: подделанной,
// испорченной или зашифрованной другим ключом. Причина намеренно одна —
// различать их для вызывающего незачем, а для атакующего полезно.
var ErrSealed = errors.New("signedtoken: непригодный запечатанный токен")

// NewSealer выводит ключ из произвольной секретной строки.
func NewSealer(secret string) (*Sealer, error) {
	if len(secret) < 16 {
		return nil, fmt.Errorf("signedtoken: ключ короче 16 символов")
	}
	sum := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal шифрует value, привязывая результат к context.
//
// context входит в аутентифицированные данные, а не в шифротекст: строка,
// запечатанная для одного тикета, не расшифруется в другом. Без этого
// сегмент из чужой сессии можно было бы предъявить в своей.
func (s *Sealer) Seal(value, context string) string {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic("signedtoken: нет источника случайности: " + err.Error())
	}
	ct := s.aead.Seal(nonce, nonce, []byte(value), []byte(context))
	return base64.RawURLEncoding.EncodeToString(ct)
}

// Open расшифровывает строку, проверяя привязку к тому же context.
func (s *Sealer) Open(sealed, context string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(sealed))
	if err != nil || len(raw) < s.aead.NonceSize() {
		return "", ErrSealed
	}
	nonce, ct := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	out, err := s.aead.Open(nil, nonce, ct, []byte(context))
	if err != nil {
		return "", ErrSealed
	}
	return string(out), nil
}
