// Package providers — сборка реестра площадок.
//
// Здесь только механика: конфиг из окружения, маршрут каждой площадки,
// подключение в фоне с повторами. Самих площадок ядро не знает: они
// регистрируются в source сами, а пакет со всеми площадками (providers/all)
// бинарник подключает одним импортом в cmd/*/providers.go (ADR-0018). Без
// него ядро собирается и работает с пустым реестром.
//
// Отдельный пакет намеренно: его импортируют core, crawler и engine, но НЕ
// gateway.
package providers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/providers/script"
	"github.com/niazleushkin/kino/internal/adapter/providers/source"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/bootstrap/kit"
)

// Config — какие площадки поднимать и как.
//
//	PROVIDERS          "a,b,c" — id площадок, порядок не важен; пусто — те, что
//	                   объявлены с Default
//	PROVIDER_EGRESS    "a=default,b=direct" — маршрут наружу
//	PROVIDER_PRIORITY  "a=100,b=60"
//	SOURCES_DIR        каталог площадок-скриптов сверх вшитых (script.Bundle): на
//	                   устройстве — поставленные человеком, на сервере —
//	                   принятые правки до следующей сборки (ADR-0017)
//
// Ключи и адреса самих площадок (FILMIX_TOKEN, LORDFILM_ALLOHA_TOKEN…)
// описаны в их Spec (spec.go в пакете площадки) и видны списком:
// `go run ./cmd/sourcekit list`.
//
// Площадка без маршрута ходит маршрутом по умолчанию; без приоритета —
// с приоритетом 10, как стояло у всех привязок до второй площадки.
type Config struct {
	IDs      []string
	Egress   map[string]string
	Priority map[string]int
	// Cartoon — склейка аниме с «Мультфильмом» хозяина метаданных (MATCH_ANIME_CARTOON):
	// off — нет (по умолчанию), new — для новых элементов, merge — ещё и
	// слияние уже заведённых дублей реконсилиатором.
	Cartoon string

	// OnAuth — доклад о смене состояния входа площадки, у которой он есть
	// (source.EventAuth): «нужна переавторизация» и обратно. Задаёт тот, кому
	// есть кому показать (ядро — админке и плееру); nil — только журнал.
	OnAuth func(provider string, st appprov.AuthStatus)

	// ClientWrap — по желанию: свой клиент для каждой площадки поверх клиента её
	// маршрута (локальный движок ставит так User-Agent площадки). nil — клиент
	// маршрута как есть, как на сервере.
	ClientWrap func(id string, hc *http.Client) *http.Client

	// Getenv — откуда площадки берут ключи и адреса; nil — окружение
	// процесса. Тестам и движку — свой источник.
	Getenv func(string) string

	// Scripts — площадки-скрипты: вшитые и из SOURCES_DIR, с ошибками
	// разбора. Показываются человеку (/engine/sources), а сломанный файл
	// объясняет, почему площадки из PROVIDERS нет.
	Scripts []script.Loaded
	// SourcesDir — каталог SOURCES_DIR.
	SourcesDir string
}

// LoadScripts разбирает площадки-скрипты и регистрирует их рядом с
// площадками на Go. Вызывается из LoadConfig; повторный вызов в том же
// процессе ничего не делает.
func LoadScripts(dir string) []script.Loaded {
	scriptsOnce.Do(func() {
		loaded = script.Install(
			script.LoadFS(script.BundleFS(), source.OriginBundled),
			script.LoadDir(dir, source.OriginLocal),
		)
	})
	return loaded
}

var (
	scriptsOnce sync.Once
	loaded      []script.Loaded
)

// scriptIssue — почему скрипт с этим id не стал площадкой.
func (c Config) scriptIssue(id string) (string, bool) {
	for _, l := range c.Scripts {
		if l.Err != nil && l.ID() == id {
			first, _, _ := strings.Cut(l.Err.Error(), "\n")
			return "файл площадки: " + first, true
		}
	}
	return "", false
}

// GetenvFunc — откуда площадки берут настройки: Getenv или окружение.
func (c Config) GetenvFunc() func(string) string { return c.getenv() }

func (c Config) getenv() func(string) string {
	if c.Getenv != nil {
		return c.Getenv
	}
	return os.Getenv
}

// MatchCartoon — склеивать новые элементы аниме-площадок с мультфильмами.
func (c Config) MatchCartoon() bool { return c.Cartoon == "new" || c.Cartoon == "merge" }

// MergeCartoon — сливать уже заведённые пары «аниме + мультфильм».
func (c Config) MergeCartoon() bool { return c.Cartoon == "merge" }

// LoadConfig читает конфиг площадок из окружения.
func LoadConfig() (Config, error) {
	c := Config{Egress: map[string]string{}, Priority: map[string]int{}}
	ids := os.Getenv("PROVIDERS")
	if ids == "" {
		ids = strings.Join(source.DefaultIDs(), ",")
	}
	for _, id := range strings.Split(ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			c.IDs = append(c.IDs, id)
		}
	}
	for k, v := range pairs(os.Getenv("PROVIDER_EGRESS")) {
		c.Egress[k] = v
	}
	switch c.Cartoon = strings.TrimSpace(os.Getenv("MATCH_ANIME_CARTOON")); c.Cartoon {
	case "", "off":
		c.Cartoon = "off"
	case "new", "merge":
	default:
		return Config{}, fmt.Errorf("MATCH_ANIME_CARTOON=%q: ждём off, new или merge", c.Cartoon)
	}
	c.SourcesDir = strings.TrimSpace(os.Getenv("SOURCES_DIR"))
	c.Scripts = LoadScripts(c.SourcesDir)
	for k, v := range pairs(os.Getenv("PROVIDER_PRIORITY")) {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("PROVIDER_PRIORITY: %s=%q — не число", k, v)
		}
		c.Priority[k] = n
	}
	return c, nil
}

func pairs(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.TrimSpace(k) != "" {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// AnimeProviders — площадки, у которых весь каталог — аниме. Склейка
// считает их тайтлы аниме, даже если у хозяина метаданных нет такого жанра.
// Выводится из описаний площадок (source.Spec.Kind), включая скрипты.
func AnimeProviders() []string { return source.AnimeIDs() }

// LookupProviders — площадки, которые находят произведение по номерам IMDb и
// Кинопоиска (досбор при открытии любого тайтла).
func LookupProviders() []string { return source.LookupIDs() }

// VoiceNameProviders — площадки, чьи подписи озвучек — образец написания.
func VoiceNameProviders() []string { return source.VoiceNameIDs() }

// HistoryProvider — площадка, из аккаунта которой переносится история
// (перенос в админке); "" — такой нет.
func HistoryProvider() string { return source.HistoryID() }

// TitleBridgeProvider — площадка без номеров MAL, которую с аниме склеивает
// мост по названиям; "" — такой нет.
func TitleBridgeProvider() string { return source.TitleBridgeID() }

// Logins — входы по коду всех площадок, у которых он есть, — включены они в
// PROVIDERS или нет: человек входит, чтобы потом включить. Клиент — маршрут
// площадки (с ClientWrap), nil — маршрута нет.
func Logins(ctx context.Context, cfg Config, routes *kit.Routes, log *slog.Logger) map[string]appprov.DeviceLogin {
	out := map[string]appprov.DeviceLogin{}
	for _, s := range source.All() {
		if s.Login == nil {
			continue
		}
		hc, ok := routes.Client(cfg.Egress[s.ID])
		if !ok {
			hc = nil
		} else if cfg.ClientWrap != nil {
			hc = cfg.ClientWrap(s.ID, hc)
		}
		out[s.ID] = s.Login(source.Env{Ctx: ctx, HTTP: hc, Log: log.With("provider", s.ID), Get: cfg.getenv()})
	}
	return out
}

// Enabled — те из ids, что включены в PROVIDERS: досбор не должен ждать
// площадку, которой в этой сборке нет, — иначе он шёл бы на каждом
// открытии тайтла.
func (c Config) Enabled(ids []string) []string {
	on := map[string]bool{}
	for _, id := range c.IDs {
		on[id] = true
	}
	var out []string
	for _, id := range ids {
		if on[id] {
			out = append(out, id)
		}
	}
	return out
}

// BuildRegistry поднимает провайдеров, НЕ блокируя старт.
//
// Регистрация требует живого запроса к площадке — она проверяет, что
// площадка отвечает. Делать это синхронно нельзя: площадка периодически
// недоступна, и тогда сервис не начал бы слушать порты вовсе. Падал бы не
// только резолв, а всё сразу — вход, каталог, админка, — хотя площадка им
// не нужна.
//
// Поэтому провайдер добавляется в реестр в фоне и с повторами. До этого
// момента Registry.Get отвечает «недоступен», что честно и локально:
// не работает ровно то, чему нужна площадка. Каждая площадка — отдельно:
// недоступная площадка не мешает остальным.
func BuildRegistry(ctx context.Context, cfg Config, routes *kit.Routes, log *slog.Logger) (*appprov.Registry, error) {
	reg, _, err := buildRegistry(ctx, cfg, routes, log, false)
	return reg, err
}

// Issue — площадка, которую не удалось подключить: почему, одной строкой.
type Issue struct {
	ID     string
	Reason string
}

// BuildRegistryLenient — для устройства, где площадки выбирает человек
// (локальный движок, ADR-0015). Неверная настройка одной площадки — неизвестное
// имя, маршрут без прокси, нет ключа — отключает только её: остальные живут, а
// причина уходит в Issue, чтобы показать её человеку. Строгий BuildRegistry
// остаётся для сервера: там опечатка в конфиге должна ронять запуск, а не
// тихо оставлять каталог без площадки.
func BuildRegistryLenient(ctx context.Context, cfg Config, routes *kit.Routes, log *slog.Logger) (*appprov.Registry, []Issue) {
	reg, issues, _ := buildRegistry(ctx, cfg, routes, log, true)
	return reg, issues
}

func buildRegistry(ctx context.Context, cfg Config, routes *kit.Routes, log *slog.Logger,
	lenient bool) (*appprov.Registry, []Issue, error) {
	reg := appprov.NewRegistry()
	var issues []Issue
	// fail — единственное место, где различаются строгий и мягкий режим.
	fail := func(id, reason string, err error) error {
		if !lenient {
			return err
		}
		log.Warn("площадка пропущена", "id", id, "причина", reason)
		issues = append(issues, Issue{ID: id, Reason: reason})
		return nil
	}
	for _, id := range cfg.IDs {
		spec, ok := source.Get(id)
		if !ok {
			reason := "площадка неизвестна"
			if why, bad := cfg.scriptIssue(id); bad {
				reason = why
			}
			if err := fail(id, reason, fmt.Errorf("PROVIDERS: площадка %q: %s", id, reason)); err != nil {
				return nil, nil, err
			}
			continue
		}
		route := cfg.Egress[id]
		if route == "" {
			route = kit.DefaultRoute
		}
		hc, ok := routes.Client(route)
		if !ok {
			if err := fail(id, fmt.Sprintf("маршрут %q не задан", route),
				fmt.Errorf("PROVIDER_EGRESS: у площадки %s маршрут %q, а в EGRESS_ROUTES его нет", id, route)); err != nil {
				return nil, nil, err
			}
			continue
		}
		if cfg.ClientWrap != nil {
			hc = cfg.ClientWrap(id, hc)
		}
		p, err := spec.New(source.Env{
			Ctx: ctx, HTTP: hc, Log: log.With("provider", id), Get: cfg.getenv(),
			Report: func(event string, payload any) {
				if st, ok := payload.(appprov.AuthStatus); ok && event == source.EventAuth && cfg.OnAuth != nil {
					cfg.OnAuth(id, st)
				}
			},
		})
		if err != nil {
			// Причина — первая строка: за ней бывает подсказка, что делать.
			first, _, _ := strings.Cut(err.Error(), "\n")
			if ferr := fail(id, first, err); ferr != nil {
				return nil, nil, ferr
			}
			continue
		}
		log.Info("площадка подключается", "id", id, "route", route)
		go registerWithRetry(ctx, reg, p, route, log.With("provider", id))
	}
	return reg, issues, nil
}

// registerWithRetry повторяет регистрацию, пока площадка не ответит.
//
// Интервал растёт: заблокированная площадка может молчать долго, и долбить
// её раз в секунду и бесполезно, и невежливо.
func registerWithRetry(ctx context.Context, reg *appprov.Registry, p appprov.Provider, route string, log *slog.Logger) {
	delay := 5 * time.Second
	const maxDelay = 5 * time.Minute
	for attempt := 1; ; attempt++ {
		regCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		d, err := reg.Register(regCtx, p, route)
		cancel()
		if err == nil {
			log.Info("провайдер зарегистрирован",
				"id", d.ProviderID, "version", d.ProviderVersion, "api", d.APIMajor,
				"попыток", attempt)
			return
		}
		log.Warn("провайдер пока недоступен, повторю",
			"err", err, "попытка", attempt, "через", delay.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < maxDelay {
			delay *= 2
		}
	}
}
