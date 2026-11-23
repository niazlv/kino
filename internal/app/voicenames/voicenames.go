// Package voicenames — какие подписи озвучек у разных площадок значат одно.
//
// Площадки пишут одну озвучку по-разному: kinopub — «DUB», ЛордФильм —
// «Дублированный»; AnimeGO — «AniLiberty», kinopub — по-старому
// «AniLibria»; одна — «Мега-Аниме», другая — «Mega-Anime». Не зная, что это
// одно, обход тормозящей площадки не найдёт ту же озвучку на другой.
// Склеивать такие пары по одной в коде — бесконечно: формулировок много, и
// новые появляются сами.
//
// Поэтому три слоя. Вид озвучки без студии сводит разбор по словам
// (provider.parseKind) — это правило, а не список. Студии под разными
// именами сводит таблица псевдонимов в базе, и пополняют её двое:
//   - обучение — написания, которые сходятся по грубой латинице
//     (provider.LooseVoice), склеиваются сами (origin auto);
//   - человек в админке — по подсказкам похожих написаний или вручную
//     (origin admin). Ошибку обучения он разъединяет, и пара запоминается
//     как разная, чтобы обучение её не вернуло.
//
// Подсказкам нужно знать, какие написания вообще бывают, — их сервис
// копит сам: плеер сообщает о каждом списке озвучек (Observe), а в базу
// они уходят пачкой.
package voicenames

import (
	"context"
	"log/slog"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/provider"
)

// Spelling — как площадка написала озвучку.
type Spelling struct {
	// Key — ключ написания без псевдонимов (provider.BaseVoiceKey).
	Key      string
	Title    string
	Provider string
	// Episode и Title — где встретилась в последний раз: человеку в
	// админке нужен пример, чтобы проверить на слух.
	Episode  kernel.EpisodeID
	TitleID  kernel.TitleID
	Hits     int
	LastSeen time.Time
}

// Alias — написание From (ключ) значит студию To.
type Alias struct {
	From, To  string
	Origin    string // OriginAdmin | OriginAuto
	Note      string
	CreatedAt time.Time
}

const (
	OriginAdmin = "admin"
	OriginAuto  = "auto"
)

// Store — где живут написания и псевдонимы.
type Store interface {
	Aliases(ctx context.Context) ([]Alias, error)
	// PutAlias ставит псевдоним и разворачивает цепочки: кто указывал на
	// From, теперь указывает на To. Пару из «разных» снимает.
	PutAlias(ctx context.Context, a Alias) error
	DeleteAlias(ctx context.Context, from string) error
	// Rejected — пары, которые человек назвал разными; порядок в паре любой.
	Rejected(ctx context.Context) ([][2]string, error)
	Reject(ctx context.Context, a, b string) error
	RecordSpellings(ctx context.Context, s []Spelling) error
	Spellings(ctx context.Context) ([]Spelling, error)
}

// Сроки: написания копятся в памяти и уходят пачкой; таблица
// перечитывается и сама — на случай второго процесса.
const (
	flushEvery  = 30 * time.Second
	reloadEvery = 5 * time.Minute
	// pendingMax — сколько разных написаний держать до записи. Больше —
	// значит, база не отвечает, и копить бесконечно нельзя.
	pendingMax = 5000
)

type Service struct {
	store Store
	log   *slog.Logger
	now   func() time.Time

	mu       sync.Mutex
	pending  map[spellingID]*Spelling
	onChange []func()
	// current — поставленная таблица: перечитали ту же — ничего не делать.
	current map[string]string
	loaded  bool
	// learnMu — обучение и правки человека не идут одновременно.
	learnMu sync.Mutex
	// preferred — площадки, чьё написание главное в паре (WithPreferred).
	preferred map[string]bool
}

type spellingID struct{ key, title, provider string }

func New(store Store, log *slog.Logger) *Service {
	return &Service{store: store, log: log, now: time.Now, pending: map[spellingID]*Spelling{}}
}

// WithPreferred называет площадки, чьё написание студии главное, когда
// обучение склеивает два написания (у сервера — хозяин метаданных: дорожку
// его потока старые приложения ищут по ключу сами). Без них главное —
// встречавшееся чаще.
func (s *Service) WithPreferred(providers ...string) *Service {
	s.preferred = map[string]bool{}
	for _, p := range providers {
		s.preferred[p] = true
	}
	return s
}

// OnChange — кого позвать, когда таблица псевдонимов поменялась: списки
// озвучек, собранные по старой, надо забыть.
func (s *Service) OnChange(f func()) {
	s.mu.Lock()
	s.onChange = append(s.onChange, f)
	s.mu.Unlock()
}

// Start читает таблицу (не вышло — работаем с разбором без псевдонимов,
// это не повод не подниматься) и запускает запись написаний и обучение.
func (s *Service) Start(ctx context.Context) {
	if err := s.Reload(ctx); err != nil {
		s.log.Warn("псевдонимы озвучек не прочитаны — пока без них", "err", err)
	}
	go func() {
		flush := time.NewTicker(flushEvery)
		reload := time.NewTicker(reloadEvery)
		defer flush.Stop()
		defer reload.Stop()
		for {
			select {
			case <-ctx.Done():
				// Накопленное — последней пачкой: иначе выкатка теряет
				// полминуты написаний.
				fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				s.flush(fctx)
				cancel()
				return
			case <-flush.C:
				if s.flush(ctx) {
					if n, err := s.Learn(ctx); err != nil {
						s.log.Warn("обучение псевдонимов озвучек", "err", err)
					} else if n > 0 {
						s.log.Info("псевдонимы озвучек выучены по транслиту", "новых", n)
					}
				}
			case <-reload.C:
				if err := s.Reload(ctx); err != nil {
					s.log.Warn("псевдонимы озвучек не перечитаны", "err", err)
				}
			}
		}
	}()
}

// Observe запоминает написание. Не ждёт базу: зовёт его сборка списка
// озвучек, пока человек ждёт плеер.
func (s *Service) Observe(ep kernel.EpisodeID, providerID, raw string) {
	key := provider.BaseVoiceKey(raw)
	if key == "" || providerID == "" {
		return
	}
	id := spellingID{key, provider.VoiceTitle(raw), providerID}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pending[id]; ok {
		p.Hits++
		p.Episode = ep
		return
	}
	if len(s.pending) >= pendingMax {
		return
	}
	s.pending[id] = &Spelling{Key: id.key, Title: id.title, Provider: providerID, Episode: ep, Hits: 1, LastSeen: s.now()}
}

// flush пишет накопленное; true — было что писать.
func (s *Service) flush(ctx context.Context) bool {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return false
	}
	batch := make([]Spelling, 0, len(s.pending))
	for _, p := range s.pending {
		batch = append(batch, *p)
	}
	s.pending = map[spellingID]*Spelling{}
	s.mu.Unlock()
	if err := s.store.RecordSpellings(ctx, batch); err != nil {
		s.log.Warn("написания озвучек не записаны", "err", err, "штук", len(batch))
		return false
	}
	return true
}

// Reload ставит таблицу из базы. Списки озвучек забываются, только если
// она правда поменялась: плановое перечитывание раз в пять минут иначе
// гоняло бы площадки (и общий аккаунт kinopub) за списками заново.
func (s *Service) Reload(ctx context.Context) error {
	as, err := s.store.Aliases(ctx)
	if err != nil {
		return err
	}
	m := make(map[string]string, len(as))
	for _, a := range as {
		m[a.From] = a.To
	}
	s.mu.Lock()
	same := s.loaded && maps.Equal(m, s.current)
	s.current, s.loaded = m, true
	fs := append([]func(){}, s.onChange...)
	s.mu.Unlock()
	if same {
		return nil
	}
	provider.SetVoiceAliases(m)
	for _, f := range fs {
		f()
	}
	return nil
}

// Learn склеивает студии, чьи написания сходятся по грубой латинице.
// Возвращает, сколько псевдонимов добавил.
//
// Кто главный в паре: написание площадки из WithPreferred — дорожку её
// потока старые приложения ищут по ключу сами; иначе — встречавшееся чаще.
func (s *Service) Learn(ctx context.Context) (int, error) {
	s.learnMu.Lock()
	defer s.learnMu.Unlock()
	sp, err := s.store.Spellings(ctx)
	if err != nil {
		return 0, err
	}
	rej, err := s.rejected(ctx)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, g := range groupLoose(sp, s.preferred) {
		target := g.target()
		for _, c := range g.keys {
			if c.key == target || rej[pair(c.key, target)] {
				continue
			}
			note := "по транслиту: «" + c.title + "» = «" + g.titleOf(target) + "»"
			if err := s.store.PutAlias(ctx, Alias{From: c.key, To: target, Origin: OriginAuto, Note: note}); err != nil {
				return added, err
			}
			added++
		}
	}
	if added > 0 {
		return added, s.Reload(ctx)
	}
	return 0, nil
}

// Merge — человек сказал: написание from — это студия to. Принимает и
// ключи, и написания как есть.
func (s *Service) Merge(ctx context.Context, from, to, note string) error {
	s.learnMu.Lock()
	defer s.learnMu.Unlock()
	f, t := keyOf(from), keyOf(to)
	if f == "" || t == "" || f == t || provider.IsVoiceKind(f) {
		// Вид озвучки сводит разбор; псевдоним поверх него запутает всех.
		// Студию в вид — можно: «Дубляж Netflix» человек вправе назвать
		// просто дубляжом.
		return kernel.ErrInvalidInput
	}
	if r := provider.ResolveVoiceKey(t); r == f {
		// Обратная сторона уже склеенного: главным становится то, что
		// выбрал человек, — прежний псевдоним t → f снимается.
		if err := s.store.DeleteAlias(ctx, t); err != nil {
			return err
		}
	} else {
		t = r
	}
	if err := s.store.PutAlias(ctx, Alias{From: f, To: t, Origin: OriginAdmin, Note: note}); err != nil {
		return err
	}
	return s.Reload(ctx)
}

// Split — человек сказал: это разные студии. Псевдоним снимается, пара
// запоминается, чтобы обучение её не вернуло.
func (s *Service) Split(ctx context.Context, from string) error {
	s.learnMu.Lock()
	defer s.learnMu.Unlock()
	f := keyOf(from)
	as, err := s.store.Aliases(ctx)
	if err != nil {
		return err
	}
	to := ""
	for _, a := range as {
		if a.From == f {
			to = a.To
		}
	}
	if to == "" {
		return kernel.ErrNotFound
	}
	if err := s.store.DeleteAlias(ctx, f); err != nil {
		return err
	}
	if err := s.store.Reject(ctx, f, to); err != nil {
		return err
	}
	return s.Reload(ctx)
}

// Reject — пара из подсказок разная: больше не предлагать.
func (s *Service) Reject(ctx context.Context, a, b string) error {
	ka, kb := keyOf(a), keyOf(b)
	if ka == "" || kb == "" || ka == kb {
		return kernel.ErrInvalidInput
	}
	return s.store.Reject(ctx, ka, kb)
}

// keyOf — ключ по ключу или по написанию: кнопки админки шлют ключи, а в
// ручную форму вписывают написания.
func keyOf(s string) string {
	s = strings.TrimSpace(s)
	if provider.IsVoiceKeyForm(s) {
		return s
	}
	return provider.BaseVoiceKey(s)
}

func (s *Service) rejected(ctx context.Context) (map[[2]string]bool, error) {
	rs, err := s.store.Rejected(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[[2]string]bool, len(rs))
	for _, r := range rs {
		m[pair(r[0], r[1])] = true
	}
	return m, nil
}

// pair — пара без порядка.
func pair(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// ── обучение по транслиту ─────────────────────────────────────────────────

// looseGroup — студии (по ключу после псевдонимов), чьи написания
// сходятся по грубой латинице.
type looseGroup struct {
	keys []groupKey
}

type groupKey struct {
	key   string // после псевдонимов
	title string // самое частое написание
	hits  int
	// preferred — написание площадки из WithPreferred.
	preferred bool
}

func (g looseGroup) target() string {
	best := g.keys[0]
	for _, k := range g.keys[1:] {
		switch {
		case k.preferred != best.preferred:
			if k.preferred {
				best = k
			}
		case k.hits != best.hits:
			if k.hits > best.hits {
				best = k
			}
		case k.key < best.key:
			best = k
		}
	}
	return best.key
}

func (g looseGroup) titleOf(key string) string {
	for _, k := range g.keys {
		if k.key == key {
			return k.title
		}
	}
	return key
}

// minLoose — короче этого латиница слишком грубая: «Ко» и «Ko» не
// повод склеивать студии.
const minLoose = 4

func groupLoose(sp []Spelling, preferred map[string]bool) []looseGroup {
	type acc struct {
		hits      int
		preferred bool
		title     string
		titleHits int
	}
	byLoose := map[string]map[string]*acc{}
	for _, s := range sp {
		if provider.IsVoiceKind(s.Key) || provider.Subtitled(s.Title) {
			continue
		}
		l := provider.LooseVoice(s.Title)
		if len(l) < minLoose {
			continue
		}
		k := provider.ResolveVoiceKey(s.Key)
		if byLoose[l] == nil {
			byLoose[l] = map[string]*acc{}
		}
		a := byLoose[l][k]
		if a == nil {
			a = &acc{}
			byLoose[l][k] = a
		}
		a.hits += s.Hits
		a.preferred = a.preferred || preferred[s.Provider]
		if s.Hits > a.titleHits {
			a.title, a.titleHits = s.Title, s.Hits
		}
	}
	var out []looseGroup
	for _, keys := range byLoose {
		if len(keys) < 2 {
			continue
		}
		var g looseGroup
		for k, a := range keys {
			g.keys = append(g.keys, groupKey{key: k, title: a.title, hits: a.hits, preferred: a.preferred})
		}
		sort.Slice(g.keys, func(i, j int) bool { return g.keys[i].key < g.keys[j].key })
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].keys[0].key < out[j].keys[0].key })
	return out
}
