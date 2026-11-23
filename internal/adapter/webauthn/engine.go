// Package webauthn — адаптер протокола WebAuthn (passkey) поверх
// go-webauthn. Наружу отдаёт только доменные типы и непрозрачное состояние
// церемонии; библиотечные типы за пределы пакета не выходят, чтобы слой app
// и транспорт не зависели от конкретной реализации.
package webauthn

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type Engine struct{ w *webauthn.WebAuthn }

// New настраивает сторону сервера. Ключ обязан быть discoverable (иначе
// вход без имени невозможен) и с проверкой пользователя (иначе это только
// владение устройством, то есть один фактор, а не два).
func New(rpID, displayName string, origins []string) (*Engine, error) {
	w, err := webauthn.New(&webauthn.Config{
		RPID: rpID, RPDisplayName: displayName, RPOrigins: origins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn: %w", err)
	}
	return &Engine{w: w}, nil
}

// Owner — кто владеет ключами: библиотеке нужны пользователь и его ключи,
// чтобы проверить подпись.
type Owner func(kernel.UserID) (account.User, []account.Passkey, error)

// Assertion — результат проверенной подписи: чей ключ и его счётчик.
type Assertion struct {
	UserID       kernel.UserID
	CredentialID []byte
	SignCount    uint32
}

// user — обёртка для библиотеки. Идентификатор для authenticator'а —
// восемь байт из числового ID: он не меняется, в отличие от логина.
type user struct {
	u    account.User
	keys []account.Passkey
}

func (x user) WebAuthnID() []byte { return handle(x.u.ID) }
func (x user) WebAuthnName() string {
	return x.u.Login
}
func (x user) WebAuthnDisplayName() string {
	if x.u.DisplayName != "" {
		return x.u.DisplayName
	}
	return x.u.Login
}
func (x user) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, 0, len(x.keys))
	for _, k := range x.keys {
		out = append(out, toCredential(k))
	}
	return out
}

func handle(id kernel.UserID) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(id))
	return b
}

func fromHandle(b []byte) (kernel.UserID, error) {
	if len(b) != 8 {
		return 0, kernel.ErrInvalidInput
	}
	return kernel.UserID(binary.BigEndian.Uint64(b)), nil
}

func toCredential(p account.Passkey) webauthn.Credential {
	var t []protocol.AuthenticatorTransport
	for _, s := range p.Transports {
		t = append(t, protocol.AuthenticatorTransport(s))
	}
	return webauthn.Credential{
		ID: p.CredentialID, PublicKey: p.PublicKey, Transport: t,
		Flags: webauthn.CredentialFlags{
			UserPresent: true, UserVerified: true,
			BackupEligible: p.BackupEligible, BackupState: p.BackedUp,
		},
		Authenticator: webauthn.Authenticator{AAGUID: p.AAGUID, SignCount: p.SignCount},
	}
}

func fromCredential(c *webauthn.Credential, name string) account.Passkey {
	var t []string
	for _, x := range c.Transport {
		t = append(t, string(x))
	}
	return account.Passkey{
		CredentialID: c.ID, PublicKey: c.PublicKey, AAGUID: c.Authenticator.AAGUID,
		SignCount: c.Authenticator.SignCount, Transports: t,
		BackupEligible: c.Flags.BackupEligible, BackedUp: c.Flags.BackupState, Name: name,
	}
}

func descriptors(keys []account.Passkey) []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(keys))
	for _, k := range keys {
		c := toCredential(k)
		out = append(out, protocol.CredentialDescriptor{
			Type: protocol.PublicKeyCredentialType, CredentialID: c.ID, Transport: c.Transport,
		})
	}
	return out
}

func unmarshalState(state []byte) (webauthn.SessionData, error) {
	var sd webauthn.SessionData
	if err := json.Unmarshal(state, &sd); err != nil {
		return sd, kernel.ErrInvalidInput
	}
	return sd, nil
}

// BeginRegistration — параметры для navigator.credentials.create и
// состояние, которое надо сохранить до FinishRegistration. Уже имеющиеся
// ключи исключаются: второй раз тот же authenticator не зарегистрировать.
func (e *Engine) BeginRegistration(u account.User, keys []account.Passkey) (options any, state []byte, err error) {
	opts, sd, err := e.w.BeginRegistration(user{u, keys}, webauthn.WithExclusions(descriptors(keys)))
	if err != nil {
		return nil, nil, err
	}
	state, err = json.Marshal(sd)
	return opts, state, err
}

func (e *Engine) FinishRegistration(u account.User, keys []account.Passkey, state []byte,
	r *http.Request, name string) (account.Passkey, error) {
	sd, err := unmarshalState(state)
	if err != nil {
		return account.Passkey{}, err
	}
	c, err := e.w.FinishRegistration(user{u, keys}, sd, r)
	if err != nil {
		return account.Passkey{}, fmt.Errorf("%w: %v", kernel.ErrInvalidInput, err)
	}
	if !c.Flags.UserVerified {
		return account.Passkey{}, fmt.Errorf("%w: ключ без проверки пользователя", kernel.ErrInvalidInput)
	}
	return fromCredential(c, name), nil
}

// BeginLogin — вход без имени: ключ сам скажет, чей он.
func (e *Engine) BeginLogin() (options any, state []byte, err error) {
	opts, sd, err := e.w.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, nil, err
	}
	state, err = json.Marshal(sd)
	return opts, state, err
}

func (e *Engine) FinishLogin(state []byte, r *http.Request, owner Owner) (Assertion, error) {
	sd, err := unmarshalState(state)
	if err != nil {
		return Assertion{}, err
	}
	var who kernel.UserID
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		id, err := fromHandle(userHandle)
		if err != nil {
			return nil, err
		}
		u, keys, err := owner(id)
		if err != nil {
			return nil, err
		}
		who = id
		return user{u, keys}, nil
	}
	c, err := e.w.FinishDiscoverableLogin(handler, sd, r)
	if err != nil {
		return Assertion{}, fmt.Errorf("%w: %v", kernel.ErrUnauthorized, err)
	}
	return assertion(who, c)
}

// BeginAssertion и FinishAssertion — подпись известного пользователя:
// второй шаг входа и sudo-режим.
func (e *Engine) BeginAssertion(u account.User, keys []account.Passkey) (options any, state []byte, err error) {
	opts, sd, err := e.w.BeginLogin(user{u, keys}, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, nil, err
	}
	state, err = json.Marshal(sd)
	return opts, state, err
}

func (e *Engine) FinishAssertion(u account.User, keys []account.Passkey, state []byte, r *http.Request) (Assertion, error) {
	sd, err := unmarshalState(state)
	if err != nil {
		return Assertion{}, err
	}
	c, err := e.w.FinishLogin(user{u, keys}, sd, r)
	if err != nil {
		return Assertion{}, fmt.Errorf("%w: %v", kernel.ErrUnauthorized, err)
	}
	return assertion(u.ID, c)
}

func assertion(who kernel.UserID, c *webauthn.Credential) (Assertion, error) {
	// Счётчик подписей не вырос — ключ скопирован; без проверки
	// пользователя — это только владение, один фактор из двух.
	if c.Authenticator.CloneWarning || !c.Flags.UserVerified {
		return Assertion{}, kernel.ErrUnauthorized
	}
	return Assertion{UserID: who, CredentialID: c.ID, SignCount: c.Authenticator.SignCount}, nil
}
