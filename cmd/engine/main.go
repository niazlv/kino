// Движок: вся платформа в одном процессе — /api/v1, медиашлюз, SQLite.
// Без Postgres, Redis, Docker и обходчика (ADR-0015). Один пользователь,
// доступ по токену владельца (guard.go). Проба «серверлесс»-режима на маке.
package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/niazleushkin/kino/internal/bootstrap/engine"
	"github.com/niazleushkin/kino/internal/platform/logging"
)

func main() {
	log := logging.New("engine")
	// Площадки и их умолчания — в providers.go (ADR-0018).
	// Площадки-скрипты человека — рядом с базой (ADR-0017): поставил файл —
	// перезапуск движка — площадка в поиске.
	if os.Getenv("SOURCES_DIR") == "" {
		os.Setenv("SOURCES_DIR", filepath.Join(filepath.Dir(envOr("ENGINE_DB", "./kino-engine.db")), "sources"))
	}
	cfg := engine.Config{
		Addr:          envOr("ENGINE_ADDR", "127.0.0.1:8080"),
		DBPath:        envOr("ENGINE_DB", "./kino-engine.db"),
		SecureCookies: os.Getenv("ENGINE_SECURE_COOKIES") == "1",
		PublicURL:     os.Getenv("PUBLIC_URL"),
		GatewayBase:   os.Getenv("GATEWAY_PUBLIC_URL"),
		// Токен владельца задаёт приложение, запуская движок; без него он
		// берётся из файла рядом с базой (ADR-0015, «Доступ»).
		Token: os.Getenv("ENGINE_TOKEN"),
		// Имена хоста сверх петли, через запятую: например 10.0.2.2 для
		// эмулятора Android.
		AllowedHosts: splitList(os.Getenv("ENGINE_ALLOWED_HOSTS")),
		// Запуск подпроцессом из приложения: умерло оно — уходим и мы.
		ExitOnStdinEOF: os.Getenv("ENGINE_EXIT_ON_STDIN_EOF") == "1",
		// Бережность к чужим сервисам: по умолчанию — подчёркнуто скромно.
		RefreshHours:           envInt("ENGINE_REFRESH_HOURS", 12),
		SearchRepeatHours:      envInt("ENGINE_SEARCH_REPEAT_HOURS", 168),
		SearchEmptyRepeatHours: envInt("ENGINE_SEARCH_EMPTY_REPEAT_HOURS", 12),
		SearchMinHits:          envInt("ENGINE_SEARCH_MIN_HITS", 3),
		SearchPerSource:        envInt("ENGINE_SEARCH_PER_SOURCE", 4),
		Thumbs:                 os.Getenv("ENGINE_THUMBS") == "1",
		// Кого спрашивать о тайтле: first — первую нашедшую (по умолчанию), all — все
		// включённые (больше серий и озвучек ценой запросов).
		SourcesMode: sourcesMode(os.Getenv("ENGINE_SOURCES_MODE")),
		// User-Agent: подпись приложения (его версия) и то, что человек задал сам.
		AppUA:       os.Getenv("ENGINE_APP_UA"),
		UserAgent:   os.Getenv("ENGINE_USER_AGENT"),
		ProviderUAs: providerUAs(os.Environ()),
	}
	if err := engine.New(cfg, log).Run(); err != nil {
		log.Error("engine failed", "err", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

// sourcesMode — режим опроса площадок; неизвестное — бережный режим по умолчанию.
func sourcesMode(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), engine.SourcesAll) {
		return engine.SourcesAll
	}
	return engine.SourcesFirst
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// providerUAs — подписи площадок из окружения: ENGINE_UA_ANILIBRIA=… даёт
// anilibria. Имя площадки — в нижнем регистре, как её id.
func providerUAs(environ []string) map[string]string {
	out := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, engine.EnvUAPrefix) || strings.TrimSpace(v) == "" {
			continue
		}
		if id := strings.ToLower(strings.TrimPrefix(k, engine.EnvUAPrefix)); id != "" {
			out[id] = v
		}
	}
	return out
}
