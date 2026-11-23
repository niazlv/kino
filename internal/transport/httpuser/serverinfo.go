package httpuser

import (
	"net/http"

	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// ServerKind — признак сервера kino platform в ответе /api/v1/server.
// По нему приложение отличает «тот самый» сервер от любого сайта,
// случайно ответившего 200 на введённый человеком адрес.
const ServerKind = "kino-platform"

// serverInfoDTO — визитка сервера для клиента, который ещё не вошёл:
// приложение спрашивает её, когда человек вводит адрес своего сервера,
// и рисует по ней экран входа (есть ли регистрация, нужен ли код).
//
// Только то, что и так видно на странице входа: ни версии сборки, ни
// площадок, ни числа пользователей.
type serverInfoDTO struct {
	Kind string `json:"kind"`
	// API — версия публичного API (путь /api/v1).
	API int `json:"api"`
	// Name — как сервер назвал себя (SERVER_NAME); пусто — клиент
	// покажет адрес.
	Name string `json:"name,omitempty"`
	// Registration — invite | open | closed, см. auth.Registration.
	Registration string `json:"registration"`
	// Local — локальный движок на самом устройстве (ADR-0015).
	Local bool `json:"local,omitempty"`
}

// WithServerName задаёт имя сервера для визитки (SERVER_NAME).
func (s *Server) WithServerName(name string) *Server {
	s.serverName = name
	return s
}

func (s *Server) handleServerInfo(w http.ResponseWriter, _ *http.Request) {
	reg := string(s.auth.Registration())
	if s.local {
		// У движка один хозяин: ни входа, ни регистрации (их ручки закрыты
		// в его композиционном корне).
		reg = "closed"
	}
	httpx.JSON(w, http.StatusOK, serverInfoDTO{
		Kind: ServerKind, API: 1, Name: s.serverName, Registration: reg, Local: s.local,
	})
}
