package main

import (
	"os"

	"github.com/niazleushkin/kino/internal/bootstrap/gateway"
	"github.com/niazleushkin/kino/internal/platform/config"
	"github.com/niazleushkin/kino/internal/platform/logging"
)

// Инвариант ADR-0002: в дереве импортов этого бинарника нет провайдерского
// кода. Проверяется `make boundaries`, а не памятью разработчика.
func main() {
	log := logging.New("gateway")
	if err := gateway.NewGateway(config.LoadGateway(), log).Run(); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}
