// Package config — чтение окружения. Единственное место, знающее про env.
package config

import "os"

type Core struct {
	HTTPAddr    string // публичный API
	AdminAddr   string // админка: отдельный слушатель, наружу через Caddy под /admin
	GRPCAddr    string // служебное пространство: gateway -> core
	PostgresDSN string
	RedisAddr   string
	// За Caddy всегда TLS; выключается только для локальной разработки,
	// иначе браузер не примет cookie по http://localhost.
	SecureCookies bool
	// GatewayPublicURL — префикс ссылки на медиа-шлюз.
	//
	// По умолчанию ПУСТОЙ: ссылка отдаётся относительной, и браузер идёт
	// на тот же origin, что и страница. Абсолютный адрес на другой порт
	// сделал бы запрос кросс-доменным — браузер отбросил бы ответ без
	// CORS-заголовков, а нативный HLS в iOS вдобавок не донёс бы cookie.
	// Задавать стоит только если шлюз действительно живёт на другом домене.
	GatewayPublicURL string
}

type Gateway struct {
	HTTPAddr     string
	CoreGRPC     string
	RedisAddr    string
	XAccelPrefix string
}

type Crawler struct {
	GRPCAddr string
	// "kinopub=inproc,other=provider-other:9200" — режим хостинга плагина
	// это строка конфига, а не архитектурное решение (ADR-0001).
	Providers string
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func LoadCore() Core {
	return Core{
		HTTPAddr:         env("CORE_HTTP_ADDR", ":8080"),
		AdminAddr:        env("CORE_ADMIN_ADDR", ":8081"),
		GRPCAddr:         env("CORE_GRPC_ADDR", ":9090"),
		PostgresDSN:      env("POSTGRES_DSN", ""),
		RedisAddr:        env("REDIS_ADDR", "localhost:6379"),
		SecureCookies:    env("CORE_SECURE_COOKIES", "true") == "true",
		GatewayPublicURL: env("GATEWAY_PUBLIC_URL", ""),
	}
}

func LoadGateway() Gateway {
	return Gateway{
		HTTPAddr:     env("GATEWAY_HTTP_ADDR", ":8082"),
		CoreGRPC:     env("GATEWAY_CORE_GRPC", "localhost:9090"),
		RedisAddr:    env("REDIS_ADDR", "localhost:6379"),
		XAccelPrefix: env("GATEWAY_XACCEL_PREFIX", "/__upstream"),
	}
}

func LoadCrawler() Crawler {
	return Crawler{
		GRPCAddr:  env("CRAWLER_GRPC_ADDR", ":9091"),
		Providers: env("CRAWLER_PROVIDERS", "kinopub=inproc"),
	}
}
