package script

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"sync"

	"github.com/niazleushkin/kino/internal/adapter/providers/sitekit"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
)

// Хост скриптов — ядро: он исполняет площадку, написанную кем угодно. Разбор
// чужих плееров (Kodik, CVH, Alloha, Sibnet, AniBoom) и вшитые в сборку
// скрипты — часть площадок, а не ядра (ADR-0018): их подключает сборка
// площадок (providers/all) через SetPlayers и Bundle. Без неё скрипты пути
// «свои потоки» работают как прежде, а путь players и функции players.*
// отвечают ErrNoPlayers.

// SiteProvider — площадка поверх общего кода чужих плееров: обе плоскости,
// озвучки, разметка пропуска и кадры серий.
type SiteProvider interface {
	appprov.Provider
	appprov.Voices
	appprov.Segments
	appprov.Thumbnails
}

// Players — общий код чужих плееров.
type Players interface {
	// NewSite — площадка, которая знает только, где на странице серии стоят
	// плееры; серии, озвучки и поток добывает этот код. hc — клиент маршрута.
	NewSite(hc *http.Client, o sitekit.SiteOptions, s sitekit.Site) SiteProvider
	// Sibnet и AniBoom — адрес файла из страницы плеера.
	Sibnet(ctx context.Context, c *sitekit.Client, shellURL, referer string) (sitekit.Stream, error)
	AniBoom(ctx context.Context, c *sitekit.Client, embedURL, referer string) (sitekit.Stream, error)
}

// ErrNoPlayers — в этой сборке нет разбора чужих плееров.
var ErrNoPlayers = errors.New("разбор чужих плееров не подключён в этой сборке (ADR-0018)")

var (
	portMu  sync.RWMutex
	players Players
	bundled fs.FS
)

// SetPlayers подключает разбор чужих плееров. Зовётся из init() сборки
// площадок; nil отключает (для тестов).
func SetPlayers(p Players) {
	portMu.Lock()
	defer portMu.Unlock()
	players = p
}

func currentPlayers() (Players, error) {
	portMu.RLock()
	defer portMu.RUnlock()
	if players == nil {
		return nil, ErrNoPlayers
	}
	return players, nil
}

// Bundle подключает скрипты, вшитые в сборку (OriginBundled). Зовётся из
// init() пакета со скриптами; повторный вызов заменяет прежний набор.
func Bundle(fsys fs.FS) {
	portMu.Lock()
	defer portMu.Unlock()
	bundled = fsys
}

// BundleFS — вшитые скрипты; без них — пустой набор.
func BundleFS() fs.FS {
	portMu.RLock()
	defer portMu.RUnlock()
	if bundled == nil {
		return embed.FS{}
	}
	return bundled
}
