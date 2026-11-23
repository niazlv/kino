package main

import (
	"os"

	"github.com/niazleushkin/kino/internal/bootstrap/core"
	"github.com/niazleushkin/kino/internal/platform/config"
	"github.com/niazleushkin/kino/internal/platform/logging"
)

// main тонкий по замыслу: вся сборка зависимостей в internal/bootstrap.
func main() {
	log := logging.New("core")
	if err := core.NewCore(config.LoadCore(), log).Run(); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}
