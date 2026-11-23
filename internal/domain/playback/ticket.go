package playback

import (
	"strings"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type Protocol string

const (
	ProtocolHLS         Protocol = "HLS"
	ProtocolDASH        Protocol = "DASH"
	ProtocolProgressive Protocol = "PROGRESSIVE"
)

type DeliveryMode string

const (
	DeliveryProxy    DeliveryMode = "proxy"    // байты через нас — дефолт
	DeliveryRedirect DeliveryMode = "redirect" // 302 на CDN
	DeliveryLocal    DeliveryMode = "local"    // скачано и ремукснуто локально
)

// Upstream описывает ПЕРЕНОС, а не источник: URL, заголовки, куда можно
// редиректиться. Ни одного провайдерского понятия — благодаря этому gateway
// не требует изменений при добавлении площадки.
type Upstream struct {
	Protocol Protocol
	URL      string
	// Sensitive. Логгер gateway обязан вырезать это поле по имени, а не по
	// дисциплине разработчика.
	Headers        map[string]string
	AllowedOrigins []string
	IPBound        bool

	// CacheKeyPattern — регулярное выражение с одной группой захвата,
	// выделяющей УСТОЙЧИВУЮ часть адреса сегмента.
	//
	// Нужно потому, что подпись у разных площадок живёт в разных местах:
	// у одной в query, у другой отдельным сегментом пути. Без правила
	// кеш сегментов промахивается на каждом новом тикете, складывая один
	// и тот же файл заново.
	//
	// Правило задаёт ПРОВАЙДЕР — он знает форму своих адресов. Шлюз лишь
	// применяет его и остаётся без знания о площадках (ADR-0002).
	// Пусто — ключом служит хост и путь целиком.
	CacheKeyPattern string

	// Egress — имя маршрута наружу: VPN, прокси, напрямую. Шлюз знает
	// маршруты по именам, но не знает, какой площадке какой нужен — это
	// решает core и пишет сюда. Новая площадка на уже известном маршруте
	// не требует ни строчки в конфиге шлюза. Пусто — маршрут по умолчанию.
	Egress string `json:",omitempty"`

	// DropOutsideOrigins — сегмент с хоста вне AllowedOrigins вырезается из
	// плейлиста, а не делает отказом весь плейлист. Для площадок, вклеивающих
	// рекламу прямо в поток с чужих доменов.
	DropOutsideOrigins bool `json:",omitempty"`
	// DropHosts — сегменты с этих хостов вырезаются всегда (суффиксом на
	// границе метки, как и AllowedOrigins).
	DropHosts []string `json:",omitempty"`

	// Subtitles — субтитры отдельными файлами (WebVTT), которых нет в
	// плейлисте. Шлюз отдаёт их по номеру в этом списке тем же маршрутом и
	// с теми же заголовками, что и поток: CDN площадки пускает к файлу
	// только с ними. Номер, а не запечатанный адрес — после обновления
	// тикета тот же номер ведёт на свежую ссылку.
	Subtitles []string `json:",omitempty"`
}

// OriginAllowed — защита от SSRF. Тикет буквально приказывает нашему серверу
// сходить по URL, поэтому список разрешённых хостов едет в самом тикете
// (не в конфиге gateway — иначе новая площадка потребовала бы его передеплоя).
//
// Сравнение по СУФФИКСУ на границе метки, а не точное. Измерено на живом
// kinopub: мастер-плейлист лежит на api.service-kp.com, а сегменты — на
// <uuid>.ams-static-NN.cdntogo.net, где меняются и номер сервера, и uuid
// контента. Точное сравнение давало бы битый плеер при внешне живом тикете.
//
// Граница метки обязательна: суффикс "cdntogo.net" не должен пропускать
// "evil-cdntogo.net". Разрешено либо точное совпадение, либо совпадение
// с предшествующей точкой.
func (u Upstream) OriginAllowed(host string) bool {
	return hostMatches(host, u.AllowedOrigins)
}

// SegmentDropped — вырезать ли сегмент с этого хоста из плейлиста.
//
// Рекламный сегмент выбрасывается, а не проксируется и не отдаётся как
// отказ: иначе плеер спотыкался бы на каждой вставке.
func (u Upstream) SegmentDropped(host string) bool {
	if hostMatches(host, u.DropHosts) {
		return true
	}
	return u.DropOutsideOrigins && !u.OriginAllowed(host)
}

func hostMatches(host string, list []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i:], "]") {
		host = host[:i] // отбрасываем порт
	}
	for _, o := range list {
		o = strings.ToLower(strings.TrimSuffix(o, "."))
		if o == "" {
			continue
		}
		if host == o {
			return true
		}
		if strings.HasSuffix(host, "."+o) {
			return true
		}
	}
	return false
}

type Ticket struct {
	V         uint32
	ID        kernel.TicketID
	UserID    kernel.UserID
	EpisodeID kernel.EpisodeID

	Upstream     Upstream
	MaxHeight    int
	MaxBitrateKB int

	ExpiresAt   time.Time
	Refreshable bool
	Mode        DeliveryMode

	// Только внешний плеер без обратной связи требует оценки по сегментам.
	// У встроенного плеера скачанное вперёд — буфер, а не просмотр.
	TrackProgressFromSegments bool `json:",omitempty"`

	// Choice — что выбрал человек: площадку и озвучку. Шлюзу не нужно и им
	// не читается; нужно core, чтобы обновление протухшей ссылки выдало ту
	// же озвучку с той же площадки, а не приоритетную.
	Choice TicketChoice `json:",omitempty"`
}

// TicketChoice — непрозрачные для шлюза строки выбора.
type TicketChoice struct {
	Source  string `json:",omitempty"`
	Variant string `json:",omitempty"`
}

// TicketSchemaVersion — короткий TTL тикетов делает миграции этой оси
// бесплатными: через час после раскатки старых тикетов в системе не остаётся.
const TicketSchemaVersion uint32 = 1

const refreshLead = 5 * time.Minute

func (t Ticket) Expired(now time.Time) bool { return now.After(t.ExpiresAt) }

// NeedsRefresh — проактивное обновление: ссылка живёт 30 минут, фильм 2.5 часа.
// Реактивный путь (gateway ловит 403 от CDN) остаётся страховкой.
func (t Ticket) NeedsRefresh(now time.Time) bool {
	return t.Refreshable && now.Add(refreshLead).After(t.ExpiresAt)
}

func (t Ticket) Validate() error {
	if t.V != TicketSchemaVersion {
		return kernel.ErrInvalidInput
	}
	if t.Upstream.URL == "" || len(t.Upstream.AllowedOrigins) == 0 {
		return kernel.ErrInvalidInput
	}
	if t.ExpiresAt.IsZero() {
		return kernel.ErrInvalidInput
	}
	return nil
}
