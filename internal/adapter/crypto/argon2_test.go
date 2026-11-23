package crypto

import (
	"strings"
	"testing"
)

func TestArgon2_RoundTrip(t *testing.T) {
	h := NewArgon2Hasher()
	hash, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !h.Verify("correct horse battery", hash) {
		t.Fatal("верный пароль не прошёл")
	}
	if h.Verify("wrong", hash) {
		t.Fatal("неверный пароль прошёл")
	}
}

func TestArgon2_SaltIsRandom(t *testing.T) {
	h := NewArgon2Hasher()
	a, _ := h.Hash("same")
	b, _ := h.Hash("same")
	if a == b {
		t.Fatal("одинаковые хеши для одного пароля — соль не случайна")
	}
}

// Свойство, ради которого Verify не выходит рано: иначе «логина нет»
// отличается по времени от «пароль неверен», и логины перебираются.
func TestArgon2_VerifyDoesNotShortCircuitOnGarbage(t *testing.T) {
	h := NewArgon2Hasher()
	for _, bad := range []string{"", "garbage", "$argon2id$broken"} {
		if h.Verify("whatever", bad) {
			t.Fatalf("мусорный хеш %q прошёл проверку", bad)
		}
	}
}

func TestTokenGen(t *testing.T) {
	g := NewTokenGen()
	raw, hash, err := g.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw == hash {
		t.Fatal("в БД поехал бы сам токен, а не его хеш")
	}
	if g.HashToken(raw) != hash {
		t.Fatal("HashToken не воспроизводит хеш")
	}
	code := g.NewInviteCode()
	if len(code) != 11 || !strings.Contains(code, "-") {
		t.Fatalf("инвайт-код: %q", code)
	}
	if strings.ContainsAny(code, "OI01") {
		t.Fatalf("в коде похожие символы: %q", code)
	}
}
