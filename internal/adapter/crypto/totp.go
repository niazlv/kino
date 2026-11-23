package crypto

import (
	"github.com/pquerna/otp/totp"
)

// TOTP — второй фактор для админского пространства. Реализация в adapter:
// use case знает только «код валиден или нет».
type TOTP struct{ issuer string }

func NewTOTP(issuer string) TOTP { return TOTP{issuer: issuer} }

func (t TOTP) Validate(code, secret string) bool {
	if code == "" || secret == "" {
		return false
	}
	return totp.Validate(code, secret)
}

func (t TOTP) Provision(login string) (string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: t.issuer, AccountName: login})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}
