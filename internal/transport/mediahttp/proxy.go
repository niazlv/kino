package mediahttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/adapter/segcache"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/httpx"
	"github.com/niazleushkin/kino/pkg/m3u8"
)

// handleMaster отдаёт мастер-плейлист, переписанный на наш домен.
func (s *Server) handleMaster(w http.ResponseWriter, r *http.Request) {
	t, err := s.ticket(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if t.Mode == playback.DeliveryLocal {
		// Источник не уложился в generic-перенос и был скачан заранее.
		// Отдаём с диска — провайдерского кода здесь по-прежнему нет.
		httpx.Fail(w, r, s.log, kernel.ErrUnavailable)
		return
	}
	s.serveUpstream(w, r, t, t.Upstream.URL)
}

// handleResource отдаёт всё остальное по запечатанному адресу.
func (s *Server) handleResource(w http.ResponseWriter, r *http.Request) {
	t, err := s.ticket(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	ref, err := s.sealer.Open(chi.URLParam(r, "sealed"), string(t.ID))
	if err != nil {
		// Подделка либо адрес из чужого тикета — наружу одинаково «нельзя».
		httpx.Fail(w, r, s.log, kernel.ErrForbidden)
		return
	}
	s.serveUpstream(w, r, t, ref)
}

// serveUpstream тянет адрес и решает: переписать как плейлист или
// протолкнуть как байты.
// isPlaylistPath: обновление имеет смысл повторить только для плейлиста —
// его адрес берётся из самого тикета. Запечатанный адрес сегмента указывает
// на старый путь, и повторять его бессмысленно: там будет тот же отказ.
func isPlaylistPath(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, ".m3u8")
}

func (s *Server) serveUpstream(w http.ResponseWriter, r *http.Request, t playback.Ticket, ref string) {
	s.serveUpstreamOnce(w, r, t, ref, true, true)
}

// serveUpstreamWhole отдаёт ресурс МИМО кеша сегментов.
//
// Кеш держит куски HLS: они мелкие, неизменяемые и переиспользуются между
// тикетами. Целый файл не подходит ни по одному пункту: двухсотмегабайтный
// файл вытеснил бы из кеша десятки сегментов ради одного скачивания, а
// докачка по Range пошла бы старым путём кеша — сначала весь файл, потом
// ответ (serveBytes).
func (s *Server) serveUpstreamWhole(w http.ResponseWriter, r *http.Request, t playback.Ticket, rawURL string) {
	s.serveUpstreamOnce(w, r, t, rawURL, true, false)
}

// serveUpstreamOnce тянет адрес; mayRetry разрешает ОДНУ попытку после
// обновления тикета, чтобы протухшая ссылка не рвала просмотр.
//
// ref — абсолютный адрес либо хвост от каталога потока (relRef); хвост
// разворачивается от переданного тикета.
func (s *Server) serveUpstreamOnce(w http.ResponseWriter, r *http.Request,
	t playback.Ticket, ref string, mayRetry, cacheable bool) {
	rawURL, ok := resolveRef(t, ref)
	if !ok {
		httpx.Fail(w, r, s.log, kernel.ErrForbidden)
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	// Повторная проверка после расшифровки: запечатывание уже исключает
	// подделку, но защита от SSRF не должна держаться на одном механизме.
	if !t.Upstream.OriginAllowed(u.Hostname()) {
		httpx.Fail(w, r, s.log, kernel.ErrForbidden)
		return
	}

	// Сегмент мог остаться с прошлого просмотра или качаться прямо сейчас.
	// Проверяем до того, как занимать слот и идти через VPN.
	if !isPlaylistPath(r) && s.segs.Enabled() {
		if key, ok := segcache.Key(u.String(), t.Upstream.CacheKeyPattern); ok {
			if f, _, hit := s.segs.Open(key); hit {
				defer f.Close()
				w.Header().Set("Cache-Control", "private, max-age=3600")
				w.Header().Set("X-Cache", "HIT")
				// Попадание в кеш — тоже факт просмотра: внешний плеер
				// смотрит через нас, откуда бы байты ни приехали.
				s.noteSegment(t, u.String())
				http.ServeContent(w, r, "", time.Time{}, f)
				return
			}
			// Идёт закачка того же сегмента — повтор плеера после обрыва
			// или второе устройство: читаем её, а не тянем сегмент второй
			// раз. Закачку читают только с начала, поэтому Range — мимо.
			if r.Header.Get("Range") == "" {
				if rd, ok := s.segs.Join(r.Context(), key); ok {
					s.noteSegment(t, u.String())
					s.serveFill(w, r, rd)
					return
				}
			}
		}
	}

	// Поход наружу живёт своим контекстом, а не запросом клиента: сегмент,
	// отданный на закачку в кеш, может понадобиться докачать и после ухода
	// клиента (teeBytes). До тех пор уход клиента обрывает поход, как и
	// раньше.
	//
	// Срок у сегмента — на весь поход. У файла целиком (PROGRESSIVE) — на
	// ожидание ответа и на простой: MP4 на сотню мегабайт по медленному
	// каналу идёт дольше срока, и общий срок рвал его посреди (утилита
	// докачивала, а скачивание кнопкой и плеер — нет).
	var (
		ctx    context.Context
		cancel context.CancelFunc
		idle   *idleWatch
	)
	if t.Upstream.Protocol == playback.ProtocolProgressive {
		ctx, cancel = context.WithCancel(context.WithoutCancel(r.Context()))
		idle = newIdleWatch(upstreamTimeout, cancel)
	} else {
		ctx, cancel = context.WithTimeout(context.WithoutCancel(r.Context()), upstreamTimeout)
	}
	tr := &trip{cancel: cancel, unbind: context.AfterFunc(r.Context(), cancel), idle: idle}
	defer tr.done()

	// Плейлист идёт мимо очереди: он мелкий и от него зависит старт.
	// Сегменты ждут слот, иначе предзагрузка съедает весь канал.
	release, err := s.limit.acquire(ctx, isPlaylistPath(r))
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrUnavailable)
		return
	}
	tr.release = release

	// Range прокидываем как есть: без него перемотка в плеере скачивает
	// файл с начала.
	resp, err := s.fetch(ctx, t, u, r.Header.Get("Range"))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if idle != nil {
		resp.Body = idle.wrap(resp.Body)
	}
	tr.body = resp.Body

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusGone {
		resp.Body.Close()
		s.log.Warn("апстрим отверг ссылку", "code", resp.StatusCode, "ticket", string(t.ID))

		if !mayRetry || s.refresh == nil {
			httpx.Fail(w, r, s.log, kernel.ErrExpired)
			return
		}
		// Обновляем тикет в любом случае: даже когда повторить нельзя,
		// следующий запрос плейлиста должен получить свежие ссылки.
		fresh, err := s.refreshed(r.Context(), t.ID)
		if err != nil {
			httpx.Fail(w, r, s.log, err)
			return
		}
		// Слот очереди и поход отдаём до повтора: повтор займёт свой, а
		// четыре сегмента, держащие по слоту в ожидании пятого, встали бы.
		tr.done()
		if !isPlaylistPath(r) {
			if !isRelRef(ref) {
				// Сегмент с абсолютным адресом указывает на старый путь.
				// Тикет обновлён, поэтому плееру достаточно перечитать
				// плейлист — он это делает сам на ошибке сегмента.
				httpx.Fail(w, r, s.log, kernel.ErrExpired)
				return
			}
			// Хвост разворачивается от свежего тикета — повтор осмыслен.
			s.log.Info("тикет обновлён, повторяю ресурс", "ticket", string(t.ID))
			s.serveUpstreamOnce(w, r, fresh, ref, false, cacheable)
			return
		}
		s.log.Info("тикет обновлён, повторяю", "ticket", string(t.ID))
		s.serveUpstreamOnce(w, r, fresh, fresh.Upstream.URL, false, cacheable)
		return
	}

	if isPlaylist(resp, u) {
		// Относительные адреса считаются от того места, КУДА привёл
		// редирект, а не от исходного адреса: CDN аниме-площадок отдаёт
		// плейлист через 302 на конкретный узел, и сегменты лежат рядом с
		// ним, а не на балансировщике.
		base := u
		if resp.Request != nil && resp.Request.URL != nil {
			base = resp.Request.URL
		}
		s.servePlaylist(w, r, t, base, resp)
		return
	}
	// Успешная отдача не-плейлиста — это сегмент: факт просмотра внешнего
	// плеера. Записываем до отдачи: клиент мог и оборвать, но попросил
	// именно это место.
	if resp.StatusCode == http.StatusOK {
		s.noteSegment(t, u.String())
	}
	if !cacheable {
		s.streamBytes(w, resp)
		return
	}
	s.serveBytes(w, r, resp, u, t.Upstream.CacheKeyPattern, tr)
}

// trip — поход наружу: контекст, слот очереди и тело ответа. Освобождается
// вместе с запросом клиента, если его не забрала закачка в кеш.
type trip struct {
	cancel  context.CancelFunc
	unbind  func() bool
	release func()
	body    io.Closer
	taken   bool
	idle    *idleWatch // у файла целиком: сторож простоя вместо общего срока
}

// take отвязывает поход от запроса клиента и отдаёт его освобождение
// новому владельцу. false — клиент уже ушёл и поход оборван.
func (tr *trip) take() (free func(), ok bool) {
	if !tr.unbind() {
		return nil, false
	}
	tr.taken = true
	return tr.free, true
}

func (tr *trip) done() {
	if !tr.taken {
		tr.taken = true
		tr.free()
	}
}

func (tr *trip) free() {
	if tr.body != nil {
		_ = tr.body.Close()
	}
	if tr.release != nil {
		tr.release()
	}
	if tr.idle != nil {
		tr.idle.stop()
	}
	tr.cancel()
}

// idleWatch обрывает поход, если байты не шли дольше limit: и пока ждём
// ответа, и посреди тела. Срок обновляется каждым прочитанным куском.
type idleWatch struct {
	limit time.Duration
	t     *time.Timer
}

func newIdleWatch(limit time.Duration, cancel context.CancelFunc) *idleWatch {
	return &idleWatch{limit: limit, t: time.AfterFunc(limit, cancel)}
}

func (w *idleWatch) wrap(rc io.ReadCloser) io.ReadCloser { return &idleBody{ReadCloser: rc, w: w} }

func (w *idleWatch) stop() { w.t.Stop() }

type idleBody struct {
	io.ReadCloser
	w *idleWatch
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.w.t.Reset(b.w.limit)
	}
	return n, err
}

// fetch тянет адрес так, как велит тикет: с его заголовками, его маршрутом
// наружу и с редиректами только в пределах его origin. Пустой rng — без Range.
func (s *Server) fetch(ctx context.Context, t playback.Ticket, u *url.URL, rng string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range t.Upstream.Headers {
		req.Header.Set(k, v)
	}
	if rng != "" {
		req.Header.Set("Range", rng)
	}

	client, ok := s.egress.Client(t.Upstream.Egress)
	if !ok {
		// Тикет просит маршрут, которого у шлюза нет. Молча выйти напрямую
		// нельзя: площадке, которой нужен VPN, это даст таймаут или чужую
		// страну, и поломку потом ищут по косвенным признакам.
		s.log.Error("маршрут наружу не настроен", "route", t.Upstream.Egress, "ticket", string(t.ID))
		return nil, kernel.ErrUnavailable
	}
	resp, err := withinOrigins(client, t.Upstream).Do(req)
	if err != nil {
		s.log.Error("апстрим недоступен", "host", u.Hostname(), "err", err)
		return nil, kernel.ErrUnavailable
	}
	return resp, nil
}

// servePlaylist переписывает вложенный плейлист: варианты и дорожки — это
// тоже плейлисты, и их сегменты обязаны идти через нас.
func (s *Server) servePlaylist(w http.ResponseWriter, r *http.Request, t playback.Ticket,
	base *url.URL, resp *http.Response) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlaylistSize))
	if err != nil {
		httpx.Fail(w, r, s.log, kernel.ErrUnavailable)
		return
	}
	// Карта сегментов для прогресса внешнего плеера снимается с ИСХОДНОГО
	// текста, до переписывания: трекеру нужны адреса площадки.
	// Ключи карты — те же хвосты, что в ссылках (relRef): после обновления
	// тикета сегмент приходит по свежей подписи, и абсолютный адрес из
	// старого плейлиста его бы уже не узнал.
	key := func(abs string) string { return relRef(t, abs) }
	s.watch.Playlist(t.ID, key(base.String()), string(body), base, t.ExpiresAt, key)
	out, err := m3u8.Rewriter{
		Base: base,
		Resolve: func(abs *url.URL) (string, error) {
			// Реклама, вклеенная в поток, вырезается здесь, до запечатывания:
			// правило пишет провайдер в тикет, шлюз лишь применяет его.
			if t.Upstream.SegmentDropped(abs.Hostname()) {
				return "", m3u8.ErrDrop
			}
			return s.sealURL(t, abs)
		},
	}.Rewrite(string(body))
	if err != nil {
		if errors.Is(err, kernel.ErrForbidden) || errors.Is(err, m3u8.ErrDrop) {
			// Плейлист ведёт за пределы разрешённых origin — отдавать
			// половину переписанного нельзя.
			httpx.Fail(w, r, s.log, kernel.ErrForbidden)
			return
		}
		httpx.Fail(w, r, s.log, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/vnd.apple.mpegurl")
	// Плейлист живёт ровно столько, сколько тикет, и кешировать его
	// у клиента дольше нельзя.
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(out))
}

// serveBytes проталкивает сегмент, попутно кладя его в кеш.
//
// Сегменты неизменяемы, поэтому кешируются хорошо: перемотка назад и
// повторный просмотр перестают ходить через VPN. Первый просмотр кеш не
// ускоряет, но и не задерживает: клиент получает байты по мере закачки.
func (s *Server) serveBytes(w http.ResponseWriter, r *http.Request,
	resp *http.Response, u *url.URL, pattern string, tr *trip) {
	key, ok := segcache.Key(u.String(), pattern)
	if !s.segs.Enabled() || resp.StatusCode != http.StatusOK || !ok {
		s.streamBytes(w, resp)
		return
	}
	if r.Header.Get("Range") == "" {
		s.teeBytes(w, r, resp, u, key, tr)
		return
	}
	// Клиент просил кусок, а площадка прислала сегмент целиком. Кусок из
	// идущей закачки не вырезать, поэтому старым путём: сначала в кеш, потом
	// ServeContent — он сам разбирает Range. Плеер HLS просит сегменты
	// целиком, так что путь редкий.
	if f, _, err := s.segs.Store(key, resp.Body); err == nil {
		defer f.Close()
		copyHeaders(w.Header(), resp)
		http.ServeContent(w, r, "", time.Time{}, f)
		return
	}
	s.streamBytes(w, resp)
}

// teeBytes отдаёт сегмент клиенту по мере прихода и одновременно пишет его
// в кеш.
//
// Сегмент 4K весит 10–20 МБ. Пока шлюз сначала докачивал его в кеш, а потом
// отвечал, первый байт ждали столько же, сколько всю закачку, — на медленном
// маршруте дольше, чем плеер готов ждать: он рвал запрос, закачка рвалась
// с ним, и повтор начинался с нуля.
//
// Закачку ведёт кеш, а не запрос: клиент читает её вслед за записью. Ушёл
// клиент — закачка живёт ещё немного, чтобы повтор плеера подхватил её, а не
// начал заново; не подхватили — бросается (segcache.defaultGrace).
func (s *Server) teeBytes(w http.ResponseWriter, r *http.Request,
	resp *http.Response, u *url.URL, key string, tr *trip) {
	free, ok := tr.take()
	if !ok {
		return // клиент ушёл, пока ждали ответа
	}
	h := make(http.Header)
	copyHeaders(h, resp)
	if resp.ContentLength >= 0 {
		h.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	fill, rd, err := s.segs.Begin(r.Context(), key, h, tr.cancel)
	if err != nil {
		// Неработающий кеш не повод не показать сегмент.
		s.log.Warn("кеш сегментов не принял закачку", "err", err)
		defer free()
		s.streamBytes(w, resp)
		return
	}
	if fill == nil {
		// Тот же сегмент уже качается для соседнего запроса: свой поход
		// бросаем, читаем общий.
		free()
		s.serveFill(w, r, rd)
		return
	}
	go func() {
		defer free()
		switch err := fill.From(resp.Body); {
		case err == nil:
		case errors.Is(err, segcache.ErrAbandoned):
			s.log.Debug("закачка сегмента брошена: никто не ждёт", "host", u.Hostname())
		default:
			s.log.Warn("сегмент не докачан", "host", u.Hostname(), "err", err)
		}
	}()
	s.serveFill(w, r, rd)
}

// serveFill отдаёт сегмент из идущей закачки: что уже на диске — сразу,
// остальное по мере прихода.
func (s *Server) serveFill(w http.ResponseWriter, r *http.Request, rd *segcache.Reader) {
	defer rd.Close()
	// Заголовки у закачки одни на всех читателей — каждому своя копия.
	if h, ok := rd.Meta().(http.Header); ok {
		for k, v := range h {
			w.Header()[k] = slices.Clone(v)
		}
	}
	w.WriteHeader(http.StatusOK)
	// Сбрасываем сразу, не копя буфер: плеер меряет ожидание первого байта
	// по приходу ответа, а за очередной порцией закачка может задуматься.
	rc := http.NewResponseController(w)
	flush := func() bool {
		err := rc.Flush()
		return err == nil || errors.Is(err, http.ErrNotSupported)
	}
	if !flush() {
		return
	}
	buf := make([]byte, 64<<10)
	for {
		n, err := rd.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil || !flush() {
				return // клиент ушёл
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			if r.Context().Err() != nil {
				return // клиент ушёл
			}
			// Закачка сорвалась посреди сегмента. Обычное завершение ответа
			// клиент принял бы за целый сегмент — рвём соединение.
			panic(http.ErrAbortHandler)
		}
	}
}

// streamBytes отдаёт ответ как есть, без кеша.
func (s *Server) streamBytes(w http.ResponseWriter, resp *http.Response) {
	copyHeaders(w.Header(), resp)
	w.Header().Set("Content-Length", resp.Header.Get("Content-Length"))
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func copyHeaders(dst http.Header, resp *http.Response) {
	for _, k := range []string{"Content-Type", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := resp.Header.Get(k); v != "" {
			dst.Set(k, v)
		}
	}
	// Сегменты неизменяемы — их можно держать у клиента, но не дольше,
	// чем живёт подписанная ссылка.
	dst.Set("Cache-Control", "private, max-age=3600")
}

// isPlaylist определяет плейлист по типу содержимого, а при его отсутствии
// по расширению: часть CDN отдаёт m3u8 как application/octet-stream.
func isPlaylist(resp *http.Response, u *url.URL) bool {
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "mpegurl"):
		return true
	case strings.Contains(ct, "vnd.apple"):
		return true
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".m3u8")
}

// withinOrigins — клиент, отвергающий редирект за пределы allowed_origins.
//
// Тикет приказывает серверу сходить по адресу; без этой проверки площадка
// (или тот, кто подменил её ответ) могла бы увести шлюз редиректом куда
// угодно, в том числе во внутреннюю сеть. Копия клиента дешёвая: транспорт
// и пул соединений у неё общие с исходным.
func withinOrigins(c *http.Client, up playback.Upstream) *http.Client {
	cc := *c
	cc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("слишком много редиректов")
		}
		if !up.OriginAllowed(req.URL.Hostname()) {
			return kernel.ErrForbidden
		}
		// Заголовки тикета (Referer, User-Agent) нужны и после редиректа:
		// узел CDN проверяет их так же, как балансировщик.
		for k, v := range up.Headers {
			req.Header.Set(k, v)
		}
		return nil
	}
	return &cc
}
