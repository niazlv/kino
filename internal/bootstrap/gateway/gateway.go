package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/redis"
	"github.com/niazleushkin/kino/internal/adapter/segcache"
	"github.com/niazleushkin/kino/internal/bootstrap/kit"
	"github.com/niazleushkin/kino/internal/platform/config"
	"github.com/niazleushkin/kino/internal/transport/mediahttp"
	"github.com/niazleushkin/kino/pkg/signedtoken"
)

// GatewayApp собирается ИЗ ЧЕГО УГОДНО, КРОМЕ провайдеров.
// Отсутствие здесь провайдерских импортов — инвариант ADR-0002,
// проверяемый в `make boundaries`, а не память разработчика.
type GatewayApp struct {
	Cfg config.Gateway
	Log *slog.Logger
}

func NewGateway(cfg config.Gateway, log *slog.Logger) *GatewayApp {
	return &GatewayApp{Cfg: cfg, Log: log}
}

func (a *GatewayApp) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rdb, err := redis.NewClient(ctx, a.Cfg.RedisAddr)
	if err != nil {
		return err
	}
	defer rdb.Close()

	sealer, err := signedtoken.NewSealer(os.Getenv("TICKET_SIGNING_KEY"))
	if err != nil {
		return fmt.Errorf("TICKET_SIGNING_KEY: %w", err)
	}

	// Выходы наружу — с браузерным отпечатком. Байты тянет сам шлюз, а не
	// nginx: тот работает поверх OpenSSL и предъявит CDN отпечаток, который
	// душат (ADR-0003). Маршруты именованные: какой нужен, пишет тикет.
	routes, err := kit.LoadRoutes(120 * time.Second)
	if err != nil {
		return err
	}
	a.Log.Info("маршруты наружу", "routes", routes.Names())

	// Обновление протухшего тикета: единственная связь шлюза с core.
	// Контракт generic — один идентификатор, ни одного упоминания площадок.
	refresher := mediahttp.NewTicketRefresher(
		os.Getenv("GATEWAY_CORE_SERVICE_URL"), os.Getenv("SERVICE_TOKEN"))

	// Кеш сегментов выключен по умолчанию: они весят мегабайты, и включать
	// такое молча нельзя. Потолок задаётся явно, вытеснение по давности.
	segs, err := segcache.New(env("SEGMENT_CACHE_DIR", "/var/cache/kino/segments"),
		int64(envInt("SEGMENT_CACHE_MB", 0))<<20)
	if err != nil {
		return fmt.Errorf("кеш сегментов: %w", err)
	}
	if segs.Enabled() {
		segs.StartSweeper(ctx.Done(), func(freed int64) {
			a.Log.Info("кеш сегментов подчищен", "освобождено_мб", freed>>20)
		})
		a.Log.Info("кеш сегментов включён", "потолок_мб", envInt("SEGMENT_CACHE_MB", 0))
	}

	// Доклад о ходе внешнего плеера: тот же канал служебного пространства,
	// что и обновление тикета. Без адреса core контур просто не включается —
	// шлюз работает, но прогресс по сегментам не восстанавливает.
	var progressObs mediahttp.ProgressObserver
	if core := os.Getenv("GATEWAY_CORE_SERVICE_URL"); core != "" {
		progressObs = mediahttp.NewProgressReporter(core, os.Getenv("SERVICE_TOKEN"))
	}

	srv := mediahttp.New(redis.NewTicketStore(rdb), sealer, routes, refresher, segs, progressObs, a.Log)
	h := &http.Server{
		Addr:              a.Cfg.HTTPAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := h.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	a.Log.Info("gateway запущен", "addr", a.Cfg.HTTPAddr, "delivery", "proxy")

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		a.Log.Info("gateway останавливается")
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return h.Shutdown(shutCtx)
}

func env(k, def string) string {
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
