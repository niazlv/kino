// Package provider — доменная модель провайдера как понятия: что он умеет
// и что возвращает. Это НЕ клиент к провайдеру (тот в adapter/providerclient)
// и НЕ сам провайдер (тот в providers/*, ADR-0018).
package provider

import (
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// APIMajor — мажор контракта, который поддерживает этот хост.
// Политика: хост держит текущий мажор и предыдущий, иначе разнородные
// плагины невозможно обновлять независимо.
const (
	APIMajor         uint32 = 1
	APIMajorPrevious uint32 = 0
)

type Capability string

const (
	CapSearch      Capability = "search"
	CapSyncDelta   Capability = "sync_delta"
	CapMultiAudio  Capability = "multi_audio"
	CapSubtitles   Capability = "subtitles"
	CapHLS         Capability = "hls"
	CapProgressive Capability = "progressive"
	// CapVariants — озвучки у площадки не дорожки внутри одного потока,
	// а отдельные потоки: выбор делается резолвом, а не плеером.
	CapVariants Capability = "variants"
	// CapThumbnails — кадр серии отдаётся отдельным запросом на серию, а не
	// вместе со списком серий: спрашивать его стоит, лишь когда кадр
	// действительно показывают.
	CapThumbnails Capability = "thumbnails"
	// CapLookup — площадка находит произведение по внешним номерам (IMDb,
	// Кинопоиск) одним запросом. Так досбираются площадки для любого
	// открытого тайтла, а не только для найденных обходом.
	CapLookup Capability = "lookup"
	// CapVoices — площадка называет озвучки серии, не выдавая поток: так
	// озвучки всех площадок сводятся в один список, а нажатая студия
	// играет оттуда, где она есть.
	CapVoices Capability = "voices"
	// CapSegments — разметку пропуска (опенинг, эндинг) площадка отдаёт
	// отдельным запросом на серию. Резолв её не ждёт: лишняя страница
	// плеера задержала бы первый кадр, а кнопка «пропустить» нужна лишь
	// через секунды после старта.
	CapSegments Capability = "segments"
	// CapPagedCatalog — каталог площадка отдаёт страницами по многу тайтлов
	// из API, рассчитанного на массовые запросы. Растягивать полный обход
	// паузой на каждый тайтл незачем: запрос — один на страницу, а пауза,
	// посчитанная от прошлого размера каталога, растянула бы первый обход
	// выросшего каталога на сутки.
	CapPagedCatalog Capability = "paged_catalog"
)

// Descriptor — результат хендшейка.
type Descriptor struct {
	APIMajor        uint32
	APIMinor        uint32
	ProviderID      string
	ProviderVersion string
	Capabilities    []Capability
	// Name — как площадку называть человеку: в плеере при выборе источника
	// и в админке. Пусто — показывается ProviderID.
	Name string
}

func (d Descriptor) Has(c Capability) bool {
	for _, x := range d.Capabilities {
		if x == c {
			return true
		}
	}
	return false
}

// Compatible: несовпадение мажора -> плагин не регистрируется, без попыток
// «как-нибудь договориться». Минор меньше хостового — берём пересечение
// возможностей через Capabilities.
func (d Descriptor) Compatible() error {
	if d.APIMajor != APIMajor && d.APIMajor != APIMajorPrevious {
		return kernel.ErrConflict
	}
	if d.ProviderID == "" {
		return kernel.ErrInvalidInput
	}
	return nil
}

type AudioTrack struct{ ID, Language, Title, Codec string }

type TextTrack struct {
	ID, Language, Title, URL, Format string
	// Sidecar — субтитры отдельным файлом (WebVTT), которого нет в самом
	// потоке. URL такой дорожки уезжает в тикет, и файл отдаёт шлюз — тем
	// же маршрутом и с теми же заголовками, что и поток. Без флага URL —
	// справка: дорожка и так приходит в плейлисте.
	Sidecar bool
	// Default — включить сразу: вариант «с субтитрами» (оригинал плюс
	// файл субтитров) без них не имеет смысла.
	Default bool
}

type Rendition struct {
	Height    int
	BitrateKB int
	Codec     string
}

// Variant — отдельный поток той же серии у той же площадки.
//
// У kinopub озвучки — дорожки внутри одного HLS, и выбирает их плеер. У
// аниме-площадок каждая озвучка — свой плеер и свой поток, и выбрать её
// можно только новым резолвом. Вариант и есть такой выбор.
type Variant struct {
	// ID непрозрачен для платформы: его понимает только выдавший провайдер.
	ID string
	// Title — студия озвучки или «Субтитры»: то, что человек узнаёт.
	Title string
	// Player — чей плеер отдаёт поток, когда одна студия есть у нескольких.
	Player string
}

// ResolveRequest — что именно резолвить.
type ResolveRequest struct {
	Ref     kernel.ProviderRef
	Season  int // в нумерации ПЛОЩАДКИ, а не канонической
	Episode int
	Client  ClientProfile
	// Variant — ID из прошлого ответа этого же провайдера; пусто — по
	// VariantLabel, а без него — вариант по умолчанию.
	Variant string
	// VariantLabel — предпочтение по подписи (студии). Номера вариантов у
	// разных серий расходятся, подпись держится — как и у дорожек kinopub.
	VariantLabel string
}

// Source — то, что провайдер вернул на Resolve. Ещё не тикет: тикет
// выпускает core, добавляя пользователя, ограничения и подпись.
type Source struct {
	Protocol       string
	URL            string
	Headers        map[string]string
	AllowedOrigins []string
	ExpiresAt      time.Time
	IPBound        bool
	// CacheKeyPattern выделяет устойчивую часть адреса сегмента: подпись
	// у этой площадки сидит отдельным сегментом пути и меняется в каждом
	// тикете, поэтому без правила кеш промахивался бы всегда.
	CacheKeyPattern string

	// false -> источник не укладывается в generic-перенос (попозиционная
	// подпись сегментов, клиентские сертификаты). Уходит в download+remux,
	// а не протекает провайдерским кодом в gateway.
	Passthrough bool

	// Egress — имя маршрута наружу (VPN, прокси, напрямую). Провайдер его
	// НЕ знает и не ставит: маршрут — это инфраструктура, его приписывает
	// реестр по конфигу. Шлюз обязан тянуть байты тем же маршрутом, каким
	// шёл резолв: ссылка может быть привязана к адресу или к стране.
	Egress string

	// DropOutsideOrigins — сегмент с хоста вне AllowedOrigins вырезается из
	// плейлиста, а не валит его целиком. Так площадка, вклеивающая рекламу
	// с чужих доменов прямо в поток, всё равно играет — без рекламы.
	DropOutsideOrigins bool
	// DropHosts — сегменты с этих хостов (суффиксом, как и AllowedOrigins)
	// вырезаются, даже если хост разрешён: реклама бывает и на своём CDN.
	DropHosts []string

	// Variants — какие ещё потоки этой серии есть у площадки; Variant —
	// какой из них выдан.
	Variants []Variant
	Variant  string

	Audios     []AudioTrack
	Subtitles  []TextTrack
	Renditions []Rendition

	// Segments — разметка пропуска, пришедшая вместе с потоком (плеер
	// площадки отдал её в том же ответе). Снята с этого потока и точна.
	Segments []Segment
}

// Sidecars — субтитры отдельными файлами, по порядку. Их номер в этом
// списке — адрес на шлюзе (/stream/{тикет}/sub/{номер}.vtt): тикет хранит
// ссылки в том же порядке.
func (s Source) Sidecars() []TextTrack {
	var out []TextTrack
	for _, t := range s.Subtitles {
		if t.Sidecar && t.URL != "" {
			out = append(out, t)
		}
	}
	return out
}

// ClientProfile идёт в запрос резолва, чтобы выбор кодека делал провайдер,
// а не прибитая к одной площадке логика в core.
type ClientProfile struct {
	Codecs          []string
	MaxHeight       int
	SupportsHLS     bool
	SupportsDASH    bool
	BandwidthHintKB int
}
