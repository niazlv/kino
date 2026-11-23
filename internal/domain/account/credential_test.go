package account

import (
	"strings"
	"testing"
)

func TestValidatePasswordCountsRunesNotBytes(t *testing.T) {
	// Шесть кириллических букв — это 12 байт. Проверка по байтам пропускала
	// такой пароль, хотя минимум объявлен в 10 СИМВОЛОВ.
	if err := ValidatePassword("коротк"); err == nil {
		t.Fatal("шестисимвольный кириллический пароль принят")
	}
	if err := ValidatePassword(strings.Repeat("я", MinPasswordLen)); err != nil {
		t.Fatalf("десять символов должны проходить: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("a", MinPasswordLen)); err != nil {
		t.Fatalf("десять латинских должны проходить: %v", err)
	}
}
