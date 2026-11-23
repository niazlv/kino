package httpuser

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// Постеры проксируются, а не отдаются прямой ссылкой на площадку.
//
// Три причины: CDN площадки у домашних может быть недоступен, прямая ссылка
// раскрывает источник, и браузер иначе ходит на чужой домен с каждой карточки
// сетки. Тот же принцип, что и с видео.

const (
	artTimeout  = 15 * time.Second
	artMaxBytes = 8 << 20
	// Постер к тайтлу не меняется, поэтому кеш у клиента длинный: сетка
	// каталога иначе дёргала бы прокси на каждую прокрутку.
	artCacheControl = "private, max-age=86400"
	// artUserAgent — картинки просим как браузер. Защита от ботов у части
	// площадок (animego.fr) рвёт соединение на «Go-http-client», и постер
	// молча не приходит. Семейство — Chrome, как и TLS-отпечаток транспорта.
	artUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
)

func (s *Server) handleArt(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "titleID"), 10, 64)
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	t, err := s.catalog.Title(r.Context(), kernel.TitleID(id))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	s.proxyImage(w, r, t.Poster)
}

// handleEpisodeArt отдаёт кадр из серии тем же путём, что и постер.
func (s *Server) handleEpisodeArt(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "episodeID"), 10, 64)
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	e, err := s.catalog.Episode(r.Context(), kernel.EpisodeID(id))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	thumb := e.Thumbnail
	if thumb == "" && s.thumbs != nil {
		// Площадка отдаёт кадр отдельным запросом: спрашиваем сейчас, когда
		// его действительно показывают, и запоминаем. Не нашлось — 404, и
		// витрина рисует подпись вместо кадра.
		if u, err := s.thumbs.EpisodeThumbnail(r.Context(), kernel.EpisodeID(id)); err == nil {
			thumb = u
		}
	}
	s.proxyImage(w, r, thumb)
}

// proxyImage — общая середина: адрес пришёл от площадки, но идём по нему МЫ,
// значит проверяем схему и ограничиваем размер.
func (s *Server) proxyImage(w http.ResponseWriter, r *http.Request, raw string) {
	if raw == "" {
		httpx.Fail(w, r, s.log, kernel.ErrNotFound)
		return
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}

	// Кеш на диске: браузерный у каждого свой и не помогает ни другому
	// пользователю, ни повторному заходу после обновления страницы. А на
	// странице сериала кадров полтора десятка сразу — каждый через VPN.
	data, ct, err := s.imgs.Do(u.String(), func() ([]byte, string, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), artTimeout)
		defer cancel()
		return s.fetchArt(ctx, u.String())
	})
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrUnavailable)
		return
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", artCacheControl)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// artHedge — сколько ждать основной маршрут, прежде чем параллельно пойти
// запасным. Живой CDN отдаёт картинку за доли секунды (kinopub: 0,5 с через
// VPN, 0,1 с напрямую), а мёртвый узел VPN держит каждую по 15 с.
// Переменная — ради теста.
var artHedge = 2 * time.Second

// fetchArt берёт картинку основным маршрутом, а запасным — если основной
// отказал или молчит дольше artHedge. Побеждает первый удачный ответ.
func (s *Server) fetchArt(ctx context.Context, u string) ([]byte, string, error) {
	routes := []*http.Client{s.art}
	if s.artFallback != nil && s.artFallback != s.art {
		routes = append(routes, s.artFallback)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // проигравший маршрут бросает закачку
	type result struct {
		b   []byte
		ct  string
		err error
	}
	ch := make(chan result, len(routes))
	next, pending := 0, 0
	start := func() {
		c := routes[next]
		next++
		pending++
		go func() {
			b, ct, err := getArt(ctx, c, u)
			ch <- result{b, ct, err}
		}()
	}
	start()
	hedge := time.NewTimer(artHedge)
	defer hedge.Stop()
	var lastErr error
	for pending > 0 {
		select {
		case r := <-ch:
			pending--
			if r.err == nil {
				return r.b, r.ct, nil
			}
			lastErr = r.err
			if next < len(routes) {
				start()
			}
		case <-hedge.C:
			if next < len(routes) {
				start()
			}
		}
	}
	return nil, "", lastErr
}

func getArt(ctx context.Context, c *http.Client, u string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", artUserAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/*,*/*;q=0.8")
	resp, err := c.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", kernel.ErrNotFound
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, artMaxBytes))
	if err != nil {
		return nil, "", err
	}
	t := resp.Header.Get("Content-Type")
	if t == "" {
		t = "image/jpeg"
	}
	return b, t, nil
}
