// Package logging — структурный логгер с обязательной редакцией секретов.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// redactKeys — поля, которые не должны попасть в лог никогда.
// Реализовано в логгере, а не в дисциплине разработчика: рано или поздно
// кто-то включит debug-логирование запросов в gateway (ADR-0004).
var redactKeys = []string{"authorization", "cookie", "upstream_headers", "headers", "token", "secret"}

func replace(_ []string, a slog.Attr) slog.Attr {
	k := strings.ToLower(a.Key)
	for _, bad := range redactKeys {
		if strings.Contains(k, bad) {
			return slog.String(a.Key, "[REDACTED]")
		}
	}
	return a
}

func New(service string) *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "DEBUG" {
		level = slog.LevelDebug
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replace,
	})
	return slog.New(h).With("service", service)
}
