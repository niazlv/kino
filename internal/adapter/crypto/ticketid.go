package crypto

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// TicketIDGen выдаёт непрозрачные идентификаторы тикетов.
//
// Случайные, а не последовательные: id попадает в URL плеера, и по нему
// не должно угадываться ни количество выданных тикетов, ни чужой.
type TicketIDGen struct{}

func NewTicketIDGen() TicketIDGen { return TicketIDGen{} }

func (TicketIDGen) NewTicketID() kernel.TicketID {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		panic("crypto: нет источника случайности: " + err.Error())
	}
	return kernel.TicketID("tkt_" + base64.RawURLEncoding.EncodeToString(b))
}

// NewShareToken — непрозрачный токен ссылки-разрешения.
//
// Длиннее идентификатора тикета: этот попадает в переписку и живёт дольше,
// так что перебор должен быть безнадёжен.
func (TicketIDGen) NewShareToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("crypto: нет источника случайности: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
