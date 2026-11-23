// Package mediahttp — медиа-шлюз: отдача байт клиенту.
//
// Не знает ни одного провайдера и не может узнать: всё, что ему нужно,
// приезжает в тикете (ADR-0002). Проверяется в CI — cmd/gateway не должен
// транзитивно тянуть провайдерский код.
package mediahttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"

	"github.com/niazleushkin/kino/internal/adapter/segcache"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/httpx"
	"github.com/niazleushkin/kino/pkg/signedtoken"
)

// TicketStore — то немногое, что шлюзу нужно от остальной системы.
type TicketStore interface {
	Get(ctx context.Context, id kernel.TicketID) (playback.Ticket, error)
}

// Egress — выходы наружу по именам маршрутов. Какой маршрут нужен, пишет
// тикет; шлюз знает только имена, а не площадки (ADR-0002).
type Egress interface {
	Client(route string) (*http.Client, bool)
}

type Server struct {
	tickets TicketStore
	sealer  *signedtoken.Sealer
	egress  Egress
	refresh *TicketRefresher
	// Обновления тикетов: одно на тикет, сколько бы запросов его ни ждали,
	// и заранее — не чаще раза в aheadEvery.
	refreshes singleflight.Group
	ahead     aheadGate
	limit     *limiter
	segs      *segcache.Cache
	// progress — восстановление прогресса внешнего плеера. Может отсутствовать:
	// тогда шлюз просто не докладывает core, просмотр этого не замечает.
	progress ProgressObserver
	watch    *progressTracker
	log      *slog.Logger
}

// New собирает шлюз.
//
// egress приходит СНАРУЖИ, и его клиенты обязаны быть с браузерным
// TLS-отпечатком: CDN режет по JA3/JA4, и стандартный клиент Go под это
// попадает (ADR-0003).
func New(tickets TicketStore, sealer *signedtoken.Sealer, egress Egress,
	refresh *TicketRefresher, segs *segcache.Cache, progress ProgressObserver, log *slog.Logger) *Server {
	return &Server{
		tickets: tickets, sealer: sealer, egress: egress, refresh: refresh,
		// Четыре одновременных сегмента: столько же держит загрузчик-утилита,
		// и этого хватает, чтобы не голодал плейлист на критическом пути.
		limit: newLimiter(4),
		segs:  segs,
		// Прогресс по сегментам — опциональный контур: nil значит «не докладывать».
		progress: progress,
		watch:    newProgressTracker(),
		log:      log,
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.Recover(s.log), httpx.Log(s.log))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Вход в сессию: мастер-плейлист. Тикет в пути, а не в заголовке —
	// нативный HLS-плеер в iOS заголовки не подставляет (ADR-0003).
	r.Get("/stream/{ticketID}/master.m3u8", s.handleMaster)
	// Всё остальное — варианты, дорожки, ключи, сегменты — по запечатанному
	// адресу, привязанному к этому же тикету.
	r.Get("/stream/{ticketID}/r/{sealed}", s.handleResource)
	// Прямое скачивание одним файлом — без сборки из сегментов и без утилиты.
	r.Get("/stream/{ticketID}/file", s.handleFile)
	// Субтитры отдельными файлами — по номеру в тикете.
	r.Get("/stream/{ticketID}/sub/{n}.vtt", s.handleSubtitle)

	return r
}

// upstreamTimeout щедрый: сегменты бывают крупными, а канал идёт через
// VPN. Для сегмента HLS это срок всего похода; для файла целиком
// (PROGRESSIVE) — срок ожидания ответа и простоя: файл в сотню мегабайт
// по медленному каналу честно идёт дольше двух минут (proxy.go, idleWatch).
// Переменная — ради теста.
var upstreamTimeout = 120 * time.Second

const maxPlaylistSize = 8 << 20

func (s *Server) ticket(r *http.Request) (playback.Ticket, error) {
	id := kernel.TicketID(chi.URLParam(r, "ticketID"))
	if id == "" {
		return playback.Ticket{}, kernel.ErrInvalidInput
	}
	t, err := s.tickets.Get(r.Context(), id)
	if err != nil {
		return playback.Ticket{}, err
	}
	now := time.Now()
	if t.Expired(now) {
		// Срок ссылок вышел, пока серия стояла на паузе или шла дольше
		// него (у Alloha ссылки живут три часа). Тикет ещё лежит — значит,
		// его можно обновить под тем же id, и плеер ничего не заметит.
		if s.refresh == nil || !t.Refreshable {
			return playback.Ticket{}, kernel.ErrExpired
		}
		return s.refreshed(r.Context(), id)
	}
	if s.refresh != nil && t.NeedsRefresh(now) {
		s.refreshAhead(id)
	}
	return t, nil
}

// sealURL запечатывает адрес в путь, привязывая его к тикету.
// Клиент не видит источник и не может подменить адрес — иначе прокси
// превратился бы в открытый SSRF.
//
// Адрес внутри каталога потока из тикета запечатывается ХВОСТОМ (relRef), а
// не целиком. У Alloha токен лежит в пути и занимает ~4 КБ — у мастера, у
// вариантов и у каждого сегмента один и тот же; после шифрования и base64
// путь выходил за 8 КБ, и nginx перед сайтом отвечал 414. Хвост — десятки
// байт, а каталог шлюз и так знает из тикета.
func (s *Server) sealURL(t playback.Ticket, u *url.URL) (string, error) {
	if !t.Upstream.OriginAllowed(u.Hostname()) {
		s.log.Warn("origin вне списка тикета", "host", u.Hostname(), "ticket", string(t.ID))
		return "", kernel.ErrForbidden
	}
	return "/stream/" + string(t.ID) + "/r/" + s.sealer.Seal(relRef(t, u.String()), string(t.ID)), nil
}

// relPrefix помечает запечатанную ссылку «относительно каталога потока».
// Абсолютные начинаются со схемы, поэтому ссылки, выданные до появления
// хвостов, открываются как прежде.
const relPrefix = "~"

// upstreamDir — каталог адреса потока из тикета: всё до последней «/» пути,
// без запроса. Пусто, если адрес не разбирается или у пути нет каталога.
// Адрес проходит через url.Parse/String, как и адреса из плейлиста
// (m3u8.Rewriter), — иначе разное экранирование сломало бы сравнение
// префиксов (сломало бы безопасно: ссылка осталась бы абсолютной).
func upstreamDir(t playback.Ticket) string {
	u, err := url.Parse(t.Upstream.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
	abs := u.String()
	head := len(u.Scheme) + len("://") + len(u.Host)
	i := strings.LastIndexByte(abs, '/')
	if i < head {
		return ""
	}
	return abs[:i+1]
}

// relRef сворачивает абсолютный адрес в хвост от каталога потока, если он
// там лежит; иначе возвращает адрес как есть.
func relRef(t playback.Ticket, abs string) string {
	if dir := upstreamDir(t); dir != "" && len(abs) > len(dir) && strings.HasPrefix(abs, dir) {
		return relPrefix + abs[len(dir):]
	}
	return abs
}

// isRelRef — ссылка-хвост: она разворачивается от ТЕКУЩЕГО тикета, поэтому
// после обновления тикета ведёт уже по свежей подписи.
func isRelRef(ref string) bool { return strings.HasPrefix(ref, relPrefix) }

// resolveRef разворачивает запечатанную ссылку в абсолютный адрес по
// текущему тикету. Хвост без каталога (адрес тикета вдруг без пути) —
// отказ: угадывать, куда он вёл, нельзя.
func resolveRef(t playback.Ticket, ref string) (string, bool) {
	if !isRelRef(ref) {
		return ref, true
	}
	dir := upstreamDir(t)
	if dir == "" {
		return "", false
	}
	return dir + ref[len(relPrefix):], true
}
