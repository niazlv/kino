package httpadmin

import (
	"sort"
	"time"

	"github.com/niazleushkin/kino/internal/app/health"
)

// AuthInfo — состояние входа площадки без секретов (хранитель kpauth).
type AuthInfo struct {
	NeedsReauth bool
	Reason      string
	ExpiresAt   time.Time
	RefreshedAt time.Time
	// CanRefresh — токен продлевается сам: есть refresh-токен и ключи.
	CanRefresh bool
}

// WithProviderAuth — показывать состояние входов площадок.
//
// reauths — память о площадках (app/health), ждущих переавторизации: по ней
// на КАЖДОЙ странице висит баннер. Протухший вход молча роняет просмотр,
// поэтому админка кричит об этом сама, а не ждёт, пока кто-то откроет
// нужный раздел. info — сроки и продление токенов для
// раздела «Площадки».
func (s *Server) WithProviderAuth(reauths func() []health.Reauth, info func() map[string]AuthInfo) *Server {
	s.reauths, s.authInfo = reauths, info
	return s
}

type reauthView struct {
	Provider, Name, Cause string
	Since                 time.Time
}

func (s *Server) reauthBanner() []reauthView {
	if s.reauths == nil {
		return nil
	}
	var out []reauthView
	for _, r := range s.reauths() {
		out = append(out, reauthView{Provider: r.Provider, Name: s.providerName(r.Provider), Cause: r.Cause, Since: r.Since})
	}
	return out
}

type authView struct {
	AuthInfo
	Provider, Name string
}

func (s *Server) authViews() []authView {
	if s.authInfo == nil {
		return nil
	}
	var out []authView
	for id, a := range s.authInfo() {
		out = append(out, authView{AuthInfo: a, Provider: id, Name: s.providerName(id)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}
