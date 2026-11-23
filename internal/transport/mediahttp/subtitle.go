package mediahttp

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// maxSubtitleSize — субтитры трёхчасового фильма занимают сотни килобайт;
// всё, что сильно больше, — не субтитры.
const maxSubtitleSize = 8 << 20

// handleSubtitle отдаёт субтитры отдельным файлом (WebVTT) по номеру в тикете.
//
// Часть площадок кладёт субтитры рядом с потоком, а не в плейлист, и их CDN
// пускает к файлу только с заголовками плеера — браузер их не подставит.
// Поэтому файл идёт через шлюз тем же маршрутом и с теми же заголовками,
// что и поток. Какие файлы есть, пишет тикет: о площадках шлюз по-прежнему
// ничего не знает (ADR-0002).
func (s *Server) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	t, err := s.ticket(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if t.Mode == playback.DeliveryLocal {
		httpx.Fail(w, r, s.log, kernel.ErrUnavailable)
		return
	}
	n, err := strconv.Atoi(chi.URLParam(r, "n"))
	if err != nil || n < 0 {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	s.serveSubtitle(w, r, t, n, true)
}

// serveSubtitle тянет файл номер n. mayRetry разрешает одну попытку после
// обновления тикета: адрес берётся из тикета по номеру, и у свежего тикета
// он уже свежий — в отличие от запечатанного адреса сегмента.
func (s *Server) serveSubtitle(w http.ResponseWriter, r *http.Request, t playback.Ticket, n int, mayRetry bool) {
	if n >= len(t.Upstream.Subtitles) {
		httpx.Fail(w, r, s.log, kernel.ErrNotFound)
		return
	}
	u, err := url.Parse(t.Upstream.Subtitles[n])
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	// Тикет приказывает шлюзу сходить по адресу — та же защита от SSRF,
	// что и у потока.
	if !t.Upstream.OriginAllowed(u.Hostname()) {
		s.log.Warn("субтитры вне списка тикета", "host", u.Hostname(), "ticket", string(t.ID))
		httpx.Fail(w, r, s.log, kernel.ErrForbidden)
		return
	}

	// Мимо очереди сегментов, как и плейлист: файл мелкий, а без него первые
	// реплики прошли бы без подписей. И мимо кеша сегментов — его читают раз
	// на просмотр.
	ctx, cancel := context.WithTimeout(r.Context(), upstreamTimeout)
	defer cancel()
	resp, err := s.fetch(ctx, t, u, "")
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	defer resp.Body.Close()

	switch code := resp.StatusCode; {
	case code == http.StatusOK:
	case code == http.StatusForbidden || code == http.StatusGone:
		resp.Body.Close()
		s.log.Warn("апстрим отверг ссылку на субтитры", "code", code, "ticket", string(t.ID))
		if !mayRetry || s.refresh == nil {
			httpx.Fail(w, r, s.log, kernel.ErrExpired)
			return
		}
		fresh, err := s.refreshed(r.Context(), t.ID)
		if err != nil {
			httpx.Fail(w, r, s.log, err)
			return
		}
		s.serveSubtitle(w, r, fresh, n, false)
		return
	case code == http.StatusNotFound:
		httpx.Fail(w, r, s.log, kernel.ErrNotFound)
		return
	default:
		s.log.Warn("апстрим не отдал субтитры", "code", code, "ticket", string(t.ID))
		httpx.Fail(w, r, s.log, kernel.ErrUnavailable)
		return
	}

	h := w.Header()
	// CDN отдаёт VTT как application/octet-stream, а <track> ждёт text/vtt:
	// Firefox с другим типом файл не читает.
	h.Set("Content-Type", "text/vtt; charset=utf-8")
	// Номер в пределах тикета ведёт на один и тот же файл.
	h.Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxSubtitleSize))
}
