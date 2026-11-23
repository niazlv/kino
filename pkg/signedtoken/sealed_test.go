package signedtoken

import (
	"errors"
	"strings"
	"testing"
)

func mustSealer(t *testing.T, secret string) *Sealer {
	t.Helper()
	s, err := NewSealer(secret)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSeal_RoundTrip(t *testing.T) {
	s := mustSealer(t, "достаточно длинный ключ")
	const url = "https://86feaca6.ams-static-01.cdntogo.net/seg/0001.ts?sig=abc"
	sealed := s.Seal(url, "tkt_1")
	got, err := s.Open(sealed, "tkt_1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != url {
		t.Fatalf("получено %q, ожидалось %q", got, url)
	}
}

// Адрес CDN не должен читаться из URL: иначе источник виден любому,
// кто откроет инструменты разработчика.
func TestSeal_HidesPlaintext(t *testing.T) {
	s := mustSealer(t, "достаточно длинный ключ")
	sealed := s.Seal("https://cdntogo.net/secret/seg.ts", "tkt_1")
	for _, leak := range []string{"cdntogo", "secret", "https"} {
		if strings.Contains(sealed, leak) {
			t.Fatalf("в запечатанной строке видно %q: %s", leak, sealed)
		}
	}
}

func TestSeal_NonceIsFresh(t *testing.T) {
	s := mustSealer(t, "достаточно длинный ключ")
	if s.Seal("x", "c") == s.Seal("x", "c") {
		t.Fatal("одинаковый результат для одного входа — nonce не случаен")
	}
}

// Привязка к тикету: сегмент из чужой сессии не должен открываться в своей.
func TestSeal_ContextBinding(t *testing.T) {
	s := mustSealer(t, "достаточно длинный ключ")
	sealed := s.Seal("https://cdn/seg.ts", "tkt_чужой")
	if _, err := s.Open(sealed, "tkt_мой"); !errors.Is(err, ErrSealed) {
		t.Fatalf("токен другого тикета открылся: %v", err)
	}
}

// Подделка обязана отвергаться: иначе прокси превращается в открытый SSRF.
func TestSeal_RejectsTampering(t *testing.T) {
	s := mustSealer(t, "достаточно длинный ключ")
	sealed := s.Seal("https://cdn/seg.ts", "tkt_1")
	bad := []byte(sealed)
	bad[len(bad)/2] ^= 0x01
	if _, err := s.Open(string(bad), "tkt_1"); !errors.Is(err, ErrSealed) {
		t.Fatal("испорченный токен принят")
	}
	for _, junk := range []string{"", "не-base64!!", "aaaa"} {
		if _, err := s.Open(junk, "tkt_1"); !errors.Is(err, ErrSealed) {
			t.Fatalf("мусор %q принят", junk)
		}
	}
}

func TestSeal_ForeignKeyCannotOpen(t *testing.T) {
	a := mustSealer(t, "первый достаточно длинный ключ")
	b := mustSealer(t, "второй достаточно длинный ключ")
	if _, err := b.Open(a.Seal("https://cdn/seg.ts", "t"), "t"); !errors.Is(err, ErrSealed) {
		t.Fatal("чужой ключ открыл токен")
	}
}

func TestNewSealer_RejectsShortSecret(t *testing.T) {
	if _, err := NewSealer("коротко"); err == nil {
		t.Fatal("короткий ключ должен отвергаться")
	}
}
