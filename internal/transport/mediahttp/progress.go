package mediahttp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

// ProgressObserver — куда шлюз сообщает позицию, восстановленную из фактов
// отдачи сегментов.
//
// Зачем это вообще: внешний плеер (playExternal) назад ничего не присылает —
// произвольное приложение, открытое через ACTION_VIEW, о нас не знает. Но
// его байты идут через шлюз, и по ним ход просмотра виден целиком: какой
// сегмент плейлиста уже скачан, до того места досмотрено. Это восстановление,
// а не отчёт: точность до сегмента (десятки секунд), зато работает с любым
// плеером, включая тот, о котором мы никогда не слышали.
type ProgressObserver interface {
	Segments(ctx context.Context, t playback.Ticket, positionMS, durationMS int64) error
}

// reportThresholdMS — шаг отчёта: один поход в core на полминуты просмотра,
// а не на каждый сегмент (сегменты приходят каждые несколько секунд).
const reportThresholdMS = 30_000

// segSpan — отрезок медиаплейлиста: где начинается и сколько длится.
type segSpan struct {
	startMS int64
	durMS   int64
}

// streamWatch — один вариант потока (видеодорожка, озвучка). Сегменты
// каждого варианта идут по своей шкале; сводя по максимуму, получаем
// текущее место просмотра.
type streamWatch struct {
	spans  []segSpan
	idx    map[string]int // адрес сегмента -> номер в плейлисте
	maxIdx int            // самый дальний скачанный; -1 — ничего ещё
	total  int64
	urls   []string // адреса сегментов, в порядке плейлиста
}

type ticketWatch struct {
	expiry  time.Time
	byURL   map[string]*streamWatch
	streams map[string]*streamWatch // ключ — адрес самого плейлиста
	lastPos int64
}

// progressTracker копит по тикетам знание о плейлистах и сегментах.
//
// Только память процесса: тикет живёт полчаса, потерять таблицу при
// перезапуске шлюза — потерять один неточный отчёт, не данные.
type progressTracker struct {
	mu      sync.Mutex
	tickets map[kernel.TicketID]*ticketWatch
	now     func() time.Time
}

func newProgressTracker() *progressTracker {
	return &progressTracker{
		tickets: map[kernel.TicketID]*ticketWatch{},
		now:     time.Now,
	}
}

// Playlist запоминает карту сегментов медиаплейлиста. Вызывается при каждой
// отдаче плейлиста: повторный вызов по тому же адресу (обновление тикета
// переписывает подписанные ссылки) заменяет карту целиком, а не смешивает
// старые адреса с новыми.
func (tr *progressTracker) Playlist(id kernel.TicketID, playlistURL string,
	text string, base *url.URL, expiry time.Time, key func(string) string) {
	spans, urls := parseMediaSegments(text, base)
	if key != nil {
		for i := range urls {
			urls[i] = key(urls[i])
		}
	}
	if len(spans) == 0 {
		return // мастер-плейлист: варианты, но не сегменты
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	// Чистка — здесь же, при естественном доступе: ушедшие тикеты не должны
	// копиться вечно, но и ходить по карте на каждый сегмент незачем.
	if len(tr.tickets) > 4096 {
		tr.sweepLocked()
	}
	tw := tr.tickets[id]
	if tw == nil {
		tw = &ticketWatch{
			byURL:   map[string]*streamWatch{},
			streams: map[string]*streamWatch{},
		}
		tr.tickets[id] = tw
	}
	if expiry.After(tw.expiry) {
		tw.expiry = expiry
	}
	// Старый вариант того же адреса: его сегменты вымирают вместе со старыми
	// подписями, ссылки на них из карты надо снять.
	if old, ok := tw.streams[playlistURL]; ok {
		for _, u := range old.urls {
			if tw.byURL[u] == old {
				delete(tw.byURL, u)
			}
		}
	}
	idx := make(map[string]int, len(urls))
	var acc int64
	for i := range spans {
		acc += spans[i].durMS
		idx[urls[i]] = i
	}
	sw := &streamWatch{spans: spans, idx: idx, maxIdx: -1, total: acc, urls: urls}
	tw.streams[playlistURL] = sw
	for _, u := range urls {
		tw.byURL[u] = sw
	}
}

// Hit отмечает отдачу сегмента и возвращает восстановленную позицию, если
// пора докладывать core. ok=false значит «ничего нового» — и это обычный
// случай: не сегментный адрес или порог шага ещё не набран.
func (tr *progressTracker) Hit(id kernel.TicketID, rawURL string) (pos, total int64, ok bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tw := tr.tickets[id]
	if tw == nil {
		return 0, 0, false
	}
	sw := tw.byURL[rawURL]
	if sw == nil {
		return 0, 0, false // ключи, субтитры, не сегментные адреса
	}
	i, known := sw.idx[rawURL]
	if !known {
		return 0, 0, false
	}
	advanced := i > sw.maxIdx
	if advanced {
		sw.maxIdx = i
	}
	// Позиция — самое дальнее место, до которого дошла отдача. Не «сплошной
	// префикс»: перемотку вперёд внешний плеер честно отыгрывает запросами
	// с нового места, и занижать позицию до точки перемотки значило бы
	// возвращать человека назад при каждом продолжении.
	best := sw.spans[sw.maxIdx].startMS
	for _, s := range tw.streams {
		if s.maxIdx >= 0 && s.spans[s.maxIdx].startMS > best {
			best = s.spans[s.maxIdx].startMS
		}
	}
	// Тихо, пока шаг не набран — но сегмент, открывший хвост потока,
	// докладывается всегда: именно он переводит просмотр в «досмотрено».
	if best-tw.lastPos < reportThresholdMS && !(advanced && sw.maxIdx == len(sw.spans)-1) {
		return 0, 0, false
	}
	tw.lastPos = best
	return best, sw.total, true
}

func (tr *progressTracker) sweepLocked() {
	now := tr.now()
	for id, tw := range tr.tickets {
		if now.After(tw.expiry.Add(time.Hour)) {
			delete(tr.tickets, id)
		}
	}
}

// parseMediaSegments достаёт из медиаплейлиста отрезки: длительность из
// #EXTINF и абсолютный адрес следующего за ним URI. Разбирается ИСХОДНЫЙ
// текст, а не переписанный: карта нужна до запечатывания, где сегмент лежит
// на площадке. Плейлист без #EXTINF (мастер, варианты) даёт пустой список.
func parseMediaSegments(text string, base *url.URL) ([]segSpan, []string) {
	var spans []segSpan
	var urls []string
	var pending float64
	var acc int64
	hasEXTINF := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			hasEXTINF = true
			v := strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ",")
			if d, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && d >= 0 {
				pending = d
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue // прочие теги и комментарии
		}
		if pending <= 0 {
			continue // URI без EXTINF: карта инициализации, дорожки мастера
		}
		abs, err := base.Parse(line)
		if err != nil {
			continue // битый адрес: сегмента нет, пусть прогресс не врёт
		}
		durMS := int64(pending * 1000)
		spans = append(spans, segSpan{startMS: acc, durMS: durMS})
		urls = append(urls, abs.String())
		acc += durMS
		pending = 0
	}
	if !hasEXTINF {
		return nil, nil
	}
	return spans, urls
}

// noteSegment — хук горячего пути: отметка отдачи сегмента и фоновый доклад
// core, когда набран шаг. Горячий путь не ждёт ни замка дольше микросекунд,
// ни сети: поход в core всегда в отдельной горутине, и отказ телеметрии
// не рвёт просмотр.
func (s *Server) noteSegment(t playback.Ticket, rawURL string) {
	if s.progress == nil {
		return
	}
	pos, total, ok := s.watch.Hit(t.ID, relRef(t, rawURL))
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.progress.Segments(ctx, t, pos, total); err != nil {
			s.log.Warn("прогресс внешнего плеера не долетел",
				"ticket", string(t.ID), "pos_ms", pos, "err", err)
		}
	}()
}

// ProgressReporter докладывает core позицию по тикету — тот же канал
// служебного пространства, что и обновление тикета: сервис-токен и ни одного
// провайдерского понятия в контракте.
type ProgressReporter struct {
	client *http.Client
	base   string
	token  string
}

func NewProgressReporter(base, token string) *ProgressReporter {
	return &ProgressReporter{
		client: &http.Client{Timeout: 10 * time.Second},
		base:   base,
		token:  token,
	}
}

// Segments удовлетворяет ProgressObserver. Ошибка возвращается, а не глотается:
// решение «молча жить дальше» принимает зовущий — просмотр не должен ломаться
// из-за телеметрии.
func (p *ProgressReporter) Segments(ctx context.Context, t playback.Ticket,
	positionMS, durationMS int64) error {
	if p.base == "" {
		return fmt.Errorf("репорт прогресса не настроен")
	}
	body := fmt.Sprintf(`{"positionMs":%d,"durationMs":%d}`, positionMS, durationMS)
	u := fmt.Sprintf("%s/internal/tickets/%s/progress", p.base, string(t.ID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("X-Service-Token", p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("репорт прогресса %s: %w", t.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("репорт прогресса %s: код %d", t.ID, resp.StatusCode)
	}
	return nil
}
