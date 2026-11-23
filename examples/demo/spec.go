package demo

import (
	"github.com/niazleushkin/kino/internal/adapter/providers/source"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
)

// Площадка описывает себя в одном месте (ADR-0017): id, имя, пометка для
// экрана настроек, вид каталога, настройки и фабрика. Импорт пакета
// регистрирует описание; подключает пакет бинарник — cmd/*/demo.go.
func init() {
	source.Register(source.Spec{
		ID: ProviderID, Name: "Открытые фильмы (пример)", Kind: source.KindGeneral,
		Note: "Пример площадки: четыре открытых фильма Blender Foundation (CC BY). " +
			"Ни ключей, ни VPN не нужно.",
		// Default — включена, когда PROVIDERS не задан: в сборке без
		// площадок (публичной) это и есть каталог по умолчанию.
		Default: true,
		// Hidden — в списке площадок на телефоне её нет, пока она не
		// включена: в сборке с настоящими площадками пример не мешает.
		Hidden: true,
		// Настроек у примера нет. У настоящей площадки — ключи и адреса:
		// Settings: []source.Setting{{Env: "MYSITE_TOKEN", Label: "Токен", Secret: true}},
		// а New читает их через env.Setting("MYSITE_TOKEN").
		New: func(source.Env) (appprov.Provider, error) { return New(), nil },
	})
}
