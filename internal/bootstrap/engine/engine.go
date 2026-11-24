// Package engine — композиционный корень локального движка: вся платформа
// в одном процессе, на одном файле SQLite, без Postgres, Redis и обходчика
// (ADR-0015). Клеевые типы — здесь, как в core: слой app не знает соседей.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/crypto"
	"github.com/niazleushkin/kino/internal/adapter/imgcache"
	"github.com/niazleushkin/kino/internal/adapter/memory"
	"github.com/niazleushkin/kino/internal/adapter/postgres"
	"github.com/niazleushkin/kino/internal/adapter/segcache"
	"github.com/niazleushkin/kino/internal/adapter/sqlite"
	"github.com/niazleushkin/kino/internal/app/auth"
	appcatalog "github.com/niazleushkin/kino/internal/app/catalog"
	"github.com/niazleushkin/kino/internal/app/health"
	"github.com/niazleushkin/kino/internal/app/history"
	"github.com/niazleushkin/kino/internal/app/marks"
	"github.com/niazleushkin/kino/internal/app/playback"
	"github.com/niazleushkin/kino/internal/app/progress"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/app/searchhistory"
	appshare "github.com/niazleushkin/kino/internal/app/share"
	"github.com/niazleushkin/kino/internal/app/skips"
	"github.com/niazleushkin/kino/internal/app/telemetry"
	"github.com/niazleushkin/kino/internal/bootstrap/kit"
	"github.com/niazleushkin/kino/internal/bootstrap/providers"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dprovider "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/transport/grpcservice"
	"github.com/niazleushkin/kino/internal/transport/httpuser"
	"github.com/niazleushkin/kino/internal/transport/mediahttp"
	"github.com/niazleushkin/kino/pkg/signedtoken"
)

type Config struct {
	// Addr — единственный слушатель: /api/v1 + /stream + /internal.
	Addr string
	// DBPath — файл SQLite; создаётся при первом запуске.
	DBPath string
	// SecureCookies выключены по умолчанию: движок живёт на http://localhost.
	SecureCookies bool
	// PublicURL и GatewayBase — как в core; по умолчанию пусто (тот же origin).
	PublicURL   string
	GatewayBase string
	// Token — токен владельца (ENGINE_TOKEN): его задаёт приложение, запуская
	// движок. Пусто — токен берётся из файла рядом с базой или рождается
	// новый (guard.go, ownerSession).
	Token string
	// AllowedHosts — имена хоста сверх петли (адрес эмулятора, имя в LAN).
	AllowedHosts []string
	// ExitOnStdinEOF — завершиться, когда закроется stdin (ENGINE_EXIT_ON_STDIN_EOF=1).
	// Так движок, запущенный приложением подпроцессом, не переживает его: умерло
	// приложение — закрылся его конец пайпа — движок вышел сам, а не висит
	// сиротой с занятым портом и открытой базой.
	ExitOnStdinEOF bool

	// Бережность к чужим сервисам (ENGINE_*). Движок работает с вашего устройства
	// и сам ничего не обходит: площадки спрашиваются только по явному действию —
	// поиск, открытие тайтла, просмотр — и не чаще, чем здесь сказано.
	//
	// RefreshHours — при открытии тайтла перечитывать его серии у площадки не
	// чаще, чем раз в столько часов (0 — сутки, как на сервере).
	RefreshHours int
	// SearchRepeatHours — один и тот же запрос на площадки не уходит столько
	// часов, даже если ничего не нашёл; помнится на диске, переживает перезапуск.
	SearchRepeatHours int
	// SearchEmptyRepeatHours — сколько помнится запрос, на который ничего не нашли
	// (0 — полдня): пустота помнится короче находок, чтобы случайный пустой ответ
	// площадки не закрыл запрос на неделю.
	SearchEmptyRepeatHours int
	// SearchMinHits — поиск ступенями: площадки спрашиваются по одной, лучшая
	// первой, и как только найдено столько, остальные не беспокоятся (0 — все).
	SearchMinHits int
	// SearchPerSource — сколько находок просить у одной площадки за поиск (0 —
	// сколько попросит витрина). У площадок, где за каждой карточкой нужна своя
	// страница (animego.me), это прямая цена поиска: шесть карточек — ~25 запросов.
	SearchPerSource int
	// Thumbs — запрашивать у площадок кадры серий по одному. По умолчанию нет:
	// на сериале в сотню серий это сотня лишних запросов ради картинок.
	Thumbs bool
	// SourcesMode — кого спрашивать о тайтле (ENGINE_SOURCES_MODE):
	//   - SourcesFirst (по умолчанию) — поиск ступенями до первой площадки,
	//     нашедшей достаточно; остальные площадки о тайтле спрашиваются только по
	//     «Обновить» на его странице;
	//   - SourcesAll — поиск спрашивает все площадки, а открытие тайтла — каждую
	//     включённую, которой у него нет (один раз на площадку, память на диске):
	//     больше серий и озвучек ценой запросов.
	SourcesMode string

	// User-Agent движка (useragent.go). По умолчанию — браузероподобная подпись с
	// пометкой приложения: движок не выдаёт себя за чужой браузер. Менять подпись
	// человек может сам, по каждой площадке отдельно, — на своей совести.
	//
	// AppUA — пометка приложения с версией («KinoPlatform-Android/0.2.14»);
	// UserAgent — подпись человека для всех площадок; ProviderUAs — для одной
	// (по id площадки). Что задано человеком, уходит наружу как есть.
	AppUA       string
	UserAgent   string
	ProviderUAs map[string]string
}

// Режимы опроса площадок (Config.SourcesMode).
const (
	SourcesFirst = "first"
	SourcesAll   = "all"
)

// allSources — режим «все площадки».
func (c Config) allSources() bool { return c.SourcesMode == SourcesAll }

type Engine struct {
	Cfg Config
	Log *slog.Logger
}

func New(cfg Config, log *slog.Logger) *Engine { return &Engine{Cfg: cfg, Log: log} }

// Run — сборка движка. Ручная, как в core: компилятор проверяет не хуже wire.
func (a *Engine) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if a.Cfg.ExitOnStdinEOF {
		go watchStdin(os.Stdin, func() {
			a.Log.Info("stdin закрыт — родитель ушёл, завершаюсь")
			stop()
		})
	}

	// ── хранилище: один файл ───────────────────────────────────────────────
	db, err := sqlite.Open(a.Cfg.DBPath, a.Log.With("part", "sqlite"))
	if err != nil {
		return err
	}
	defer db.Close()

	// ── адаптеры ───────────────────────────────────────────────────────────
	users := sqlite.NewUserRepo(db)
	creds := sqlite.NewCredentialRepo(db)
	invites := sqlite.NewInviteRepo(db)
	sessionsRepo := sqlite.NewSessionRepo(db)
	passkeys := sqlite.NewPasskeyRepo(db)
	progressRepo := sqlite.NewProgressRepo(db)
	marksRepo := sqlite.NewMarksRepo(db)

	hasher := crypto.NewArgon2Hasher()
	tokens := crypto.NewTokenGen()
	totp := crypto.NewTOTP("kino")
	clock := kit.Clock{}

	// ── наружу: тот же uTLS-отпечаток и именованные маршруты, что у core ──
	routes, err := kit.LoadRoutes(120 * time.Second)
	if err != nil {
		return err
	}
	// Счётчик исходящих запросов: бережность, которую нельзя измерить, — слова.
	egress := newEgressCounter(a.Log.With("part", "egress"))
	routes.WrapTransport(egress.Wrap)
	// User-Agent: общий слой — для запросов, не привязанных к площадке (картинки,
	// вход); свой слой на каждую площадку — ниже, через ClientWrap.
	ua := newUAPolicy(a.Cfg.AppUA, a.Cfg.UserAgent, a.Cfg.ProviderUAs, a.Log.With("part", "user-agent"))
	routes.WrapTransport(ua.WrapDefault)
	a.Log.Info("User-Agent", "по умолчанию", ua.Default(), "задано человеком", ua.Custom())
	pcfg, err := providers.LoadConfig()
	if err != nil {
		return err
	}
	pcfg.ClientWrap = ua.WrapClient
	providerHealth := health.New(a.Log.With("part", "health"))
	pcfg.OnAuth = func(id string, st appprov.AuthStatus) {
		if st.NeedsReauth {
			providerHealth.Failed(id, fmt.Errorf("%s: %w", st.Reason, kernel.ErrReauth))
			a.Log.Warn("площадка требует переавторизацию", "provider", id, "reason", st.Reason)
		} else {
			providerHealth.ReauthCleared(id)
		}
	}
	// Вход по коду в площадки, у которых он есть: секрет даёт человек, сессию
	// площадка хранит сама. Клиент — маршрут площадки с её подписью: вход идёт
	// через прокси человека, если он задан.
	loginLog := a.Log.With("part", "login")
	logins := siteLogins(providers.Logins(ctx, pcfg, routes, loginLog), loginLog)
	// Мягко: площадки на устройстве выбирает человек, и неверная настройка одной
	// (нет ключа, опечатка) не должна ронять весь движок — она отключает только её
	// и попадает в /engine/status.
	registry, issues := providers.BuildRegistryLenient(ctx, pcfg, routes, a.Log)
	// Тикет несёт ту же подпись, с которой шёл резолв: шлюз предъявит её площадке.
	registry.WithUserAgent(ua.For)
	a.Log.Info("площадки", "ids", pcfg.IDs, "routes", routes.Names())
	upstream := routes.Default()
	artDirect, _ := routes.Client(kit.DirectRoute)

	// ── один пользователь и его токен ──────────────────────────────
	user, err := a.seedUser(ctx, users, creds, hasher)
	if err != nil {
		return fmt.Errorf("seed user: %w", err)
	}
	sessionToken, err := ownerSession(ctx, sessionsRepo, tokens, user.ID,
		a.Cfg.Token, a.Cfg.DBPath+".token", time.Now())
	if err != nil {
		return fmt.Errorf("сессия владельца: %w", err)
	}

	// ── use cases ──────────────────────────────────────────────────────────
	authSvc := auth.NewService(users, creds, invites, sessionsRepo, passkeys,
		hasher, tokens, totp, memory.Audit{}, clock)
	// Отметки — источник «скрыт»; досмотренное ставит «Слежу», как в core.
	progressSvc := progress.NewService(progressRepo, continueAdapter{progressRepo}, marksRepo, clock).
		WithFollower(marksRepo)
	marksSvc := marks.NewService(marksRepo)
	telemetrySvc := telemetry.NewService(memory.Sessions{}, clock)
	catalogRepo := sqlite.NewCatalogRepo(db).WithPriorities(pcfg.Priority).
		WithAnimeProviders(providers.AnimeProviders()...).
		WithCartoonMatching(pcfg.MatchCartoon())
	historySvc := history.NewService(catalogRepo, progressRepo, marksRepo, clock, a.Log)
	// Ленивый каталог — тот же, что в core: поиск на площадках по запросу,
	// тайтл попадает в базу при открытии. Досбора по жанрам (Discovery) в
	// движке нет — только то, что нашёл сам человек.
	// В режиме «все площадки» поиск не останавливается на первой нашедшей.
	minHits := a.Cfg.SearchMinHits
	if a.Cfg.allSources() {
		minHits = 0
	}
	lazyCatalog := appcatalog.NewLazyCatalog(catalogRepo, catalogRepo,
		func(id string) (appcatalog.EpisodeSource, error) {
			return registry.Get(id)
		}).
		WithOfferStats(catalogRepo).
		WithLiveSearch(catalogRepo, liveSearch(registry, pcfg.Priority, 10*time.Second, minHits, a.Cfg.SearchPerSource, a.Log)).
		WithIDLookup(idLookup(registry, 20*time.Second, a.Log), pcfg.Enabled(providers.LookupProviders())...).
		// Досбор площадок тайтла: включённые площадки, которых у тайтла нет, — при
		// открытии (режим «все») или по «Обновить» (любой режим).
		WithSourceSweep(appcatalog.Sweep{
			Search:    providerSearch(registry),
			Lookup:    providerLookup(registry),
			Providers: func() ([]string, []string) { return sweepProviders(registry) },
			Asks:      catalogRepo,
			Auto:      a.Cfg.allSources(),
			Repeat:    time.Duration(a.Cfg.SearchRepeatHours) * time.Hour,
			Limit:     a.Cfg.SearchPerSource,
		}).
		WithHealth(providerHealth).
		WithStaleAfter(time.Duration(a.Cfg.RefreshHours)*time.Hour).
		WithSearchMemory(sqlite.NewSearchMemoryRepo(db), time.Duration(a.Cfg.SearchRepeatHours)*time.Hour).
		// Память запросов — по набору площадок и режиму: включил человек площадку
		// или «все площадки» — тот же запрос уйдёт и к ней, а не ждёт недели.
		WithSearchScope(searchScope(pcfg.IDs, a.Cfg.SourcesMode)).
		WithEmptySearchRepeat(time.Duration(a.Cfg.SearchEmptyRepeatHours) * time.Hour)
	if a.Cfg.Thumbs {
		lazyCatalog.WithThumbnails(registry.EpisodeThumbnail, catalogRepo)
	}
	historySource := func() (history.Source, error) {
		id := providers.HistoryProvider()
		if id == "" {
			return nil, kernel.ErrUnavailable
		}
		p, err := registry.Get(id)
		if err != nil {
			return nil, err
		}
		src, ok := p.(history.Source)
		if !ok {
			return nil, kernel.ErrUnavailable
		}
		return src, nil
	}
	tickets := memory.NewTickets()
	playbackSvc := playback.NewService(catalogRepo, tickets, registry, memory.Sessions{},
		clock, crypto.NewTicketIDGen()).
		WithHealth(providerHealth).WithVoices(registry).WithSpellings(memory.VoiceNames{})
	skipsSvc := skips.New(catalogRepo, registry.Segments, nil, a.Log.With("part", "skips"))
	shareSvc := appshare.NewService(sqlite.NewShareRepo(db), ticketAdapter{playbackSvc},
		crypto.NewTicketIDGen(), clock)
	searchHistSvc := searchhistory.NewService(sqlite.NewSearchHistoryRepo(db))

	// ── транспорт: витрина и шлюз в одном маршрутизаторе ───────────────────
	imgs, err := imgcache.New(env("ART_CACHE_DIR", "./cache/art"),
		int64(envInt("ART_CACHE_MB", 512))<<20)
	if err != nil {
		return fmt.Errorf("кеш картинок: %w", err)
	}
	imgs.StartSweeper(ctx.Done(), func(freed int64) {
		a.Log.Info("кеш картинок подчищен", "освобождено_мб", freed>>20)
	})

	userSrv := httpuser.New(authSvc, progressSvc, playbackSvc, telemetrySvc, historySvc, historySource,
		lazyCatalog, progressRepo, marksSvc, feedsAdapter{progressRepo}, browserAdapter{catalogRepo},
		shareSvc, searchHistSvc, upstream, imgs,
		a.Cfg.PublicURL, a.Log.With("space", "user"), a.Cfg.SecureCookies, a.Cfg.GatewayBase).
		WithProviderNames(registry.Name).
		WithArtFallback(artDirect).
		WithSkips(skipsSvc).
		WithTitleRefresh(lazyCatalog).
		WithLocalMode()
	if a.Cfg.Thumbs {
		userSrv.WithThumbnails(lazyCatalog, registry.CanThumb)
	}

	// Подпись сегментных URL: ключ иного процесса не нужен — тикет не
	// переживает процесс. Из окружения — чтобы перезапуск не ломал открытый
	// плеер, если ключ задан явно.
	sealer, err := signedtoken.NewSealer(env("TICKET_SIGNING_KEY", ephemeralKey()))
	if err != nil {
		return fmt.Errorf("TICKET_SIGNING_KEY: %w", err)
	}

	// Служебное пространство движка — самому себе: шлюз обновляет протухший
	// тикет и докладывает ход внешнего плеера в тот же процесс (ADR-0002
	// оговаривает Redis как компромисс ради раздельных процессов; здесь их нет).
	// Служебный токен — случайный на процесс: знают его только эти две
	// вызывающие стороны, и сохранять его некуда и незачем.
	svcToken := env("SERVICE_TOKEN", randomHex(16))
	self := selfURL(a.Cfg.Addr)
	segs := segcacheFor(a.Log)
	svcSrv := grpcservice.New(playbackSvc, progressSvc, tickets, svcToken, a.Log.With("space", "service"))
	mediaSrv := mediahttp.New(tickets, sealer, routes,
		mediahttp.NewTicketRefresher(self, svcToken), segs,
		mediahttp.NewProgressReporter(self, svcToken), a.Log)

	hp := newHostPolicy(a.Cfg.AllowedHosts)
	mux := http.NewServeMux()
	userRoutes := userSrv.Routes()
	mux.Handle("/api/", userRoutes)
	mux.Handle("/d/", userRoutes)
	mux.Handle("/s/", userRoutes)
	mux.Handle("/internal/", svcSrv.Routes())
	// Движок «готов», когда площадки зарегистрированы: они подключаются в фоне, и
	// первый же запрос после запуска — поиск, загрузка, просмотр — упирался бы в
	// пустой реестр. Пока идёт регистрация, health отвечает 503, и приложение
	// (EngineGate) держит запросы; не дождавшись, через grace всё равно открываемся —
	// площадка без сети не должна держать приложение.
	started := time.Now()
	pending := func() int {
		n := 0
		for _, st := range providerStates(pcfg.IDs, issues, func(id string) bool {
			_, err := registry.Get(id)
			return err == nil
		}) {
			if st.State == stateStarting {
				n++
			}
		}
		return n
	}
	mux.Handle("GET /api/v1/health", readyGate(userRoutes, started, 6*time.Second, pending))
	mux.Handle("GET /engine/connect", connectHandler(sessionToken, a.Cfg.SecureCookies, hp))
	loginRoutes(mux, authSvc, logins)
	mux.Handle("GET /engine/status", statusHandler(authSvc, registry, pcfg.IDs, issues, routes.Names(), time.Now(), egress, logins, ua))
	// Площадки: список, свой скрипт, проверка, файл для отправки (ADR-0017).
	sourcesRoutes(mux, sourcesDeps{auth: authSvc, registry: registry, cfg: pcfg, issues: issues,
		routes: routes, log: a.Log.With("part", "sources")})
	mux.Handle("/", mediaSrv.Routes())
	// Снаружи внутрь: свой Host/Origin → запрет выхода и пароля → маршруты.
	// Cookie сессии больше не подставляется: без токена владельца — 401.
	handler := loopbackGuard(hp)(localModeGuard(mux))

	if a.Cfg.Token == "" {
		// Не через slog: ключ — пароль к аккаунту, ему не место в структурных
		// логах, которые кто-то соберёт.
		fmt.Fprintf(os.Stderr, "\nПодключить браузер (один раз):\n  %s/engine/connect?key=%s&next=<адрес витрины>\n"+
			"Токен хранится в %s\n\n", selfURL(a.Cfg.Addr), sessionToken, a.Cfg.DBPath+".token")
	}

	public := &http.Server{Addr: a.Cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		a.Log.Info("engine запущен", "addr", a.Cfg.Addr, "db", a.Cfg.DBPath,
			"доступ", "по токену владельца (cookie kino_session), только свой Host/Origin")
		if err := public.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		a.Log.Info("shutting down")
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return public.Shutdown(shutCtx)
}

// seedUser — владелец устройства: создаётся один раз, пароль никому не
// известен (и не нужен — сессия подставляется движком).
func (a *Engine) seedUser(ctx context.Context, users *sqlite.UserRepo,
	creds *sqlite.CredentialRepo, hasher *crypto.Argon2Hasher) (account.User, error) {
	if existing, err := users.List(ctx); err == nil && len(existing) > 0 {
		return existing[0], nil
	}
	// Пароль генерируется и выбрасывается: вход по нему невозможен.
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return account.User{}, err
	}
	hash, err := hasher.Hash(hex.EncodeToString(raw))
	if err != nil {
		return account.User{}, err
	}
	return users.Create(ctx, account.User{
		Login:       "engine",
		DisplayName: "Владелец",
		Role:        account.RoleUser,
		CreatedAt:   time.Now(),
	}, account.Credential{Hash: hash})
}

// ---- клеевые типы (слой app не знает соседей — склейка живёт здесь) ----

type continueAdapter struct{ repo *sqlite.ProgressRepo }

func (c continueAdapter) Continue(ctx context.Context, u kernel.UserID, limit, offset int) ([]progress.Item, int, error) {
	rows, total, err := c.repo.Continue(ctx, u, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out := make([]progress.Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, progress.Item{
			EpisodeID: r.EpisodeID, TitleID: r.TitleID, Title: r.Title,
			Kind: r.Kind, Year: r.Year, HasArt: r.HasArt,
			SeasonNo: r.SeasonNo, EpisodeNo: r.EpisodeNo,
			PositionMS: r.PositionMS, DurationMS: r.DurationMS,
		})
	}
	return out, total, nil
}

type ticketAdapter struct{ p *playback.Service }

func (t ticketAdapter) IssueTicket(ctx context.Context, u kernel.UserID, e kernel.EpisodeID,
	cp dprovider.ClientProfile) (appshare.TicketInfo, dprovider.Source, error) {
	tk, src, err := t.p.IssueTicket(ctx, u, e, cp)
	if err != nil {
		return appshare.TicketInfo{}, dprovider.Source{}, err
	}
	return appshare.TicketInfo{ID: tk.ID, ExpiresAt: tk.ExpiresAt}, src, nil
}

// feedsAdapter и browserAdapter переводят типы sqlite-репозитория в DTO,
// которые transport/httpuser исторически ожидает от postgres-адаптера.
// Протечка типов transport в adapter — известная неровность витринных
// лент; движок не чинит её задним числом, а обходит клеем.

type feedsAdapter struct{ repo *sqlite.ProgressRepo }

func (f feedsAdapter) NewEpisodes(ctx context.Context, u kernel.UserID, limit, offset int,
	onlyUnwatched bool) ([]postgres.NewEpisodeItem, int, int, error) {
	rows, total, active, err := f.repo.NewEpisodes(ctx, u, limit, offset, onlyUnwatched)
	if err != nil {
		return nil, 0, 0, err
	}
	out := make([]postgres.NewEpisodeItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, postgres.NewEpisodeItem{
			TitleID: r.TitleID, Title: r.Title, Kind: r.Kind, Year: r.Year,
			HasArt: r.HasArt, Unwatched: r.Unwatched, Total: r.Total,
		})
	}
	return out, total, active, nil
}

func (f feedsAdapter) History(ctx context.Context, u kernel.UserID, limit, offset int,
	flt postgres.HistoryFilter) ([]postgres.HistoryItem, int, error) {
	rows, total, err := f.repo.History(ctx, u, limit, offset, sqlite.HistoryFilter(flt))
	if err != nil {
		return nil, 0, err
	}
	out := make([]postgres.HistoryItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, postgres.HistoryItem{
			EpisodeID: r.EpisodeID, TitleID: r.TitleID, Title: r.Title, Kind: r.Kind,
			SeasonNo: r.SeasonNo, EpisodeNo: r.EpisodeNo,
			PositionMS: r.PositionMS, DurationMS: r.DurationMS, Status: r.Status,
			HasArt: r.HasArt, HasShot: r.HasShot, UpdatedAt: r.UpdatedAt,
		})
	}
	return out, total, nil
}

type browserAdapter struct{ repo *sqlite.CatalogRepo }

func (b browserAdapter) Browse(ctx context.Context, q postgres.BrowseQuery) (postgres.BrowsePage, error) {
	res, err := b.repo.Browse(ctx, q.Kind, q.Genre, q.Sort, q.Page, q.Limit)
	if err != nil {
		return postgres.BrowsePage{}, err
	}
	return postgres.BrowsePage{Items: res.Items, Total: res.Total}, nil
}

func (b browserAdapter) Genres(ctx context.Context) ([]string, error) {
	return b.repo.Genres(ctx)
}

// ---- живой поиск и досбор по номерам (копия из core, без изменений) ----

// errNobodyAnswered — ни одна площадка не ответила (нет сети, все лежат) или
// спрашивать было некого (ещё регистрируются). Это не «ничего не нашли»: пустоту запомнили бы как спрошенное, и запрос на дни
// остался бы без ответа.
var errNobodyAnswered = errors.New("ни одна площадка не ответила")

// liveSearch — поиск на площадках СТУПЕНЯМИ: по одной, по убыванию приоритета, и
// как только найдено minHits, остальным не пишем. Прежде спрашивались все
// разом — каждый поиск стоил запрос на каждую включённую площадку, даже когда
// лучшая уже всё нашла. minHits 0 — как прежде, все.
func liveSearch(registry *appprov.Registry, prio map[string]int, timeout time.Duration, minHits, perSource int,
	log *slog.Logger, only ...string) appcatalog.LiveSearch {
	return func(ctx context.Context, q string, limit int) ([]dprovider.Title, error) {
		// Сколько просить у одной площадки: не больше потолка, сколько бы ни хотела витрина.
		if perSource > 0 && (limit <= 0 || limit > perSource) {
			limit = perSource
		}
		ps := registry.SearchersByPriority(prio, only...)
		searchers := make([]titleSearcher, len(ps))
		for i, p := range ps {
			searchers[i] = p
		}
		out, err := searchStepwise(ctx, searchers, q, limit, timeout, minHits, log)
		if err != nil {
			return nil, err
		}
		// Порядок — по приоритету площадок: находка главной площадки впереди.
		sort.SliceStable(out, func(i, j int) bool {
			return prio[out[i].Ref.ProviderID] > prio[out[j].Ref.ProviderID]
		})
		return out, nil
	}
}

// titleSearcher — площадка, как её видит поиск.
type titleSearcher interface {
	Search(ctx context.Context, q string, limit int) ([]dprovider.Title, error)
}

// searchStepwise спрашивает искателей по порядку и останавливается, когда
// набралось minHits находок (0 — спросить всех). Отказавшая площадка не
// останавливает поиск, а «никто не ответил» — ошибка, не пустота.
func searchStepwise(ctx context.Context, searchers []titleSearcher, q string, limit int,
	timeout time.Duration, minHits int, log *slog.Logger) ([]dprovider.Title, error) {
	var out []dprovider.Title
	answered := 0
	for _, p := range searchers {
		sctx, cancel := context.WithTimeout(ctx, timeout)
		found, err := p.Search(sctx, q, limit)
		cancel()
		if err != nil {
			log.Debug("живой поиск: площадка не ответила", "err", err)
			continue
		}
		answered++
		out = append(out, found...)
		if minHits > 0 && len(out) >= minHits {
			break
		}
	}
	// Никто не ответил — и когда все отказали, и когда спрашивать было некого: площадки
	// ещё регистрируются (холодный старт) или не подключены. Пустоту, при которой
	// не спросили никого, запомнили бы как «спросили» — запрос на дни остался бы
	// без ответа.
	if answered == 0 {
		return nil, errNobodyAnswered
	}
	return out, nil
}

// ---- досбор площадок тайтла ----

// sweepProviders — кого можно спросить о тайтле сейчас: зарегистрированные
// площадки с поиском по названию и с поиском по номерам.
func sweepProviders(registry *appprov.Registry) (search, lookup []string) {
	for _, id := range registry.IDs() {
		if d, ok := registry.Descriptor(id); ok && d.Has(dprovider.CapSearch) {
			search = append(search, id)
		}
	}
	for id := range registry.Lookups() {
		lookup = append(lookup, id)
	}
	sort.Strings(lookup)
	return search, lookup
}

func providerSearch(registry *appprov.Registry) appcatalog.ProviderSearch {
	return func(ctx context.Context, id, q string, limit int) ([]dprovider.Title, error) {
		p, err := registry.Get(id)
		if err != nil {
			return nil, err
		}
		return p.Search(ctx, q, limit)
	}
}

func providerLookup(registry *appprov.Registry) appcatalog.ProviderLookup {
	return func(ctx context.Context, id string, ids dprovider.ExternalIDs) (dprovider.Title, error) {
		l, ok := registry.Lookups()[id]
		if !ok {
			return dprovider.Title{}, fmt.Errorf("площадка %q без поиска по номерам: %w", id, kernel.ErrUnavailable)
		}
		return l.Lookup(ctx, ids)
	}
}

// searchScope — от чего зависит память запросов, кроме самого запроса: набор
// площадок (в любом порядке) и режим опроса.
func searchScope(ids []string, mode string) string {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	if mode == "" {
		mode = SourcesFirst
	}
	return mode + ":" + strings.Join(sorted, ",")
}

func idLookup(registry *appprov.Registry, timeout time.Duration, log *slog.Logger) appcatalog.IDLookup {
	return func(ctx context.Context, ids dprovider.ExternalIDs, skip map[string]bool) []dprovider.Title {
		var (
			mu  sync.Mutex
			out []dprovider.Title
			wg  sync.WaitGroup
		)
		for id, l := range registry.Lookups() {
			if skip[id] {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				lctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				t, err := l.Lookup(lctx, ids)
				if err != nil {
					if !errors.Is(err, kernel.ErrNotFound) {
						log.Debug("досбор по номерам: площадка не ответила", "provider", id, "err", err)
					}
					return
				}
				mu.Lock()
				out = append(out, t)
				mu.Unlock()
			}()
		}
		wg.Wait()
		return out
	}
}

// ---- мелочи ----

func segcacheFor(log *slog.Logger) *segcache.Cache {
	// По умолчанию выключен (как в core): Enabled() у nil безопасна, а
	// остальное mediahttp трогает только при включённом кеше.
	mb := envInt("SEGMENT_CACHE_MB", 0)
	if mb <= 0 {
		return nil
	}
	segs, err := segcache.New(env("SEGMENT_CACHE_DIR", "./cache/segments"), int64(mb)<<20)
	if err != nil {
		log.Warn("кеш сегментов недоступен", "err", err)
		return nil
	}
	log.Info("кеш сегментов включён", "потолок_мб", mb)
	return segs
}

// ephemeralKey — разовый ключ подписи сегментов: тикеты умирают вместе с
// процессом, поэтому ключ из окружения нужен только для удобной отладки.
func ephemeralKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "engine-ephemeral-signing-key"
	}
	return hex.EncodeToString(b)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
