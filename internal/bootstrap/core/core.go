package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/aniskip"
	"github.com/niazleushkin/kino/internal/adapter/crypto"
	"github.com/niazleushkin/kino/internal/adapter/imgcache"
	"github.com/niazleushkin/kino/internal/adapter/postgres"
	redisadapter "github.com/niazleushkin/kino/internal/adapter/redis"
	webauthnadapter "github.com/niazleushkin/kino/internal/adapter/webauthn"
	"github.com/niazleushkin/kino/internal/app/auth"
	appcatalog "github.com/niazleushkin/kino/internal/app/catalog"
	"github.com/niazleushkin/kino/internal/app/health"
	"github.com/niazleushkin/kino/internal/app/history"
	"github.com/niazleushkin/kino/internal/app/marks"
	"github.com/niazleushkin/kino/internal/app/playback"
	"github.com/niazleushkin/kino/internal/app/progress"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/app/retention"
	"github.com/niazleushkin/kino/internal/app/searchhistory"
	appshare "github.com/niazleushkin/kino/internal/app/share"
	"github.com/niazleushkin/kino/internal/app/skips"
	"github.com/niazleushkin/kino/internal/app/telemetry"
	"github.com/niazleushkin/kino/internal/app/voicenames"
	"github.com/niazleushkin/kino/internal/bootstrap/kit"
	"github.com/niazleushkin/kino/internal/bootstrap/providers"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dprovider "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/config"
	"github.com/niazleushkin/kino/internal/platform/pg"
	"github.com/niazleushkin/kino/internal/transport/grpcservice"
	"github.com/niazleushkin/kino/internal/transport/httpadmin"
	"github.com/niazleushkin/kino/internal/transport/httpuser"
)

type CoreApp struct {
	Cfg config.Core
	Log *slog.Logger
}

func NewCore(cfg config.Core, log *slog.Logger) *CoreApp { return &CoreApp{Cfg: cfg, Log: log} }

// Run — композиционный корень. Единственное место, где конкретные адаптеры
// встречаются с интерфейсами слоя app. Сборка ручная: компилятор проверяет
// её не хуже wire/fx, а бизнес-логика остаётся не знающей про контейнер.
func (a *CoreApp) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pg.NewPool(ctx, a.Cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	// ── адаптеры ───────────────────────────────────────────────────────────
	users := postgres.NewUserRepo(pool)
	creds := postgres.NewCredentialRepo(pool)
	invites := postgres.NewInviteRepo(pool)
	sessions := postgres.NewSessionRepo(pool)
	progressRepo := postgres.NewProgressRepo(pool)
	audit := postgres.NewAuditRepo(pool)

	hasher := crypto.NewArgon2Hasher()
	tokens := crypto.NewTokenGen()
	totp := crypto.NewTOTP("kino")
	clock := kit.Clock{}

	// ── воспроизведение ────────────────────────────────────────────────────
	rdb, err := redisadapter.NewClient(ctx, a.Cfg.RedisAddr)
	if err != nil {
		return err
	}
	defer rdb.Close()

	// Выходы наружу по именам: у каждой площадки свой маршрут (VPN, прокси,
	// напрямую), и шлюз потом тянет байты тем же маршрутом.
	routes, err := kit.LoadRoutes(120 * time.Second)
	if err != nil {
		return err
	}
	pcfg, err := providers.LoadConfig()
	if err != nil {
		return err
	}
	// Память о лежащих площадках — общая для выпуска тикетов и страницы
	// тайтла: что не ответило плееру, того не ждёт и страница.
	providerHealth := health.New(a.Log.With("part", "health"))
	// Вход на площадку (её токен) — туда же: протух и не продлился —
	// площадка лежит с причиной «нужна переавторизация», и её видно в
	// админке и в плеере, даже если заметил это обходчик (общий файл).
	auths := &authBoard{m: map[string]httpadmin.AuthInfo{}}
	pcfg.OnAuth = func(id string, st appprov.AuthStatus) {
		auths.set(id, httpadmin.AuthInfo{NeedsReauth: st.NeedsReauth, Reason: st.Reason,
			ExpiresAt: st.ExpiresAt, RefreshedAt: st.RefreshedAt, CanRefresh: st.CanRefresh})
		if st.NeedsReauth {
			providerHealth.Failed(id, fmt.Errorf("%s: %w", st.Reason, kernel.ErrReauth))
		} else {
			providerHealth.ReauthCleared(id)
		}
	}
	registry, err := providers.BuildRegistry(ctx, pcfg, routes, a.Log)
	if err != nil {
		return err
	}
	a.Log.Info("площадки", "ids", pcfg.IDs, "routes", routes.Names())
	// Постеры и кадры ходят маршрутом по умолчанию: адрес картинки не
	// говорит, с какой он площадки, а маршрут по умолчанию и так нужен
	// главной площадке. Прямой — запасной: CDN картинок kinopub с сервера
	// открывается и без VPN, и мёртвый узел не должен гасить все постеры.
	upstream := routes.Default()
	artDirect, _ := routes.Client(kit.DirectRoute)

	// ── use cases ──────────────────────────────────────────────────────────
	// Регистрация: по умолчанию только по приглашению (семейный сервер);
	// REGISTRATION=open — свой сервер для многих, closed — только админка.
	registration, err := auth.ParseRegistration(env("REGISTRATION", ""))
	if err != nil {
		return err
	}
	authSvc := auth.NewService(users, creds, invites, sessions, postgres.NewPasskeyRepo(pool),
		hasher, tokens, totp, audit, clock).WithRegistration(registration)
	marksRepo := postgres.NewMarksRepo(pool)
	// Отметки — источник «скрыт», и включённая серия это скрытие снимает.
	// Досмотренная или отмеченная серия идущего сериала ставит «Слежу».
	progressSvc := progress.NewService(progressRepo, continueAdapter{progressRepo}, marksRepo, clock).
		WithFollower(marksRepo)
	sessionRepo := postgres.NewPlaybackSessionRepo(pool)
	telemetrySvc := telemetry.NewService(sessionRepo, clock)
	catalogRepo := postgres.NewCatalogRepo(pool).WithPriorities(pcfg.Priority).
		WithAnimeProviders(providers.AnimeProviders()...).
		WithCartoonMatching(pcfg.MatchCartoon())
	marksSvc := marks.NewService(marksRepo)
	historySvc := history.NewService(catalogRepo, progressRepo, marksRepo, clock, a.Log)
	// Серии догружаются при первом открытии тайтла: обход площадки собирает
	// только метаданные, потому что серии для 53 тысяч тайтлов — это столько
	// же лишних запросов ради тайтлов, которые никто не откроет.
	lazyCatalog := appcatalog.NewLazyCatalog(catalogRepo, catalogRepo,
		func(id string) (appcatalog.EpisodeSource, error) {
			p, err := registry.Get(id)
			if err != nil {
				return nil, err
			}
			return p, nil
		}).
		WithOfferStats(catalogRepo).
		// Живой поиск: каталог обходится целиком редко, а спросить площадку
		// про конкретное название стоит один запрос. Спрашиваются все
		// площадки сразу — найденное склеится в каталоге само.
		WithLiveSearch(catalogRepo, liveSearch(registry, pcfg.Priority, 10*time.Second, a.Log)).
		// Досбор площадок при открытии аниме-тайтла: только аниме-площадки,
		// лимит kinopub на это не тратится. Идёт в фоне, поэтому площадке
		// можно дать больше времени, чем живому поиску.
		WithDiscovery(liveSearch(registry, pcfg.Priority, 50*time.Second, a.Log, providers.AnimeProviders()...),
			catalogRepo, providers.AnimeProviders()...).
		// Досбор по номерам IMDb и Кинопоиска — у любого открытого тайтла:
		// балансер находит и фильмы, которых на сайте площадки нет.
		WithIDLookup(idLookup(registry, 20*time.Second, a.Log), pcfg.Enabled(providers.LookupProviders())...).
		// Кадры серий, которые площадка отдаёт по запросу, а не со списком.
		WithThumbnails(registry.EpisodeThumbnail, catalogRepo).
		WithHealth(providerHealth)
	// Источник истории берётся из реестра лениво: провайдер может быть
	// недоступен в момент старта, и это не повод не подниматься.
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
	playbackSvc := playback.NewService(
		catalogRepo,
		redisadapter.NewTicketStore(rdb),
		registry,
		sessionRepo,
		clock,
		crypto.NewTicketIDGen(),
	).WithHealth(providerHealth).WithVoices(registry)
	// Одна студия под разными именами у разных площадок: таблица
	// псевдонимов из базы, обучение по транслиту и подсказки в админке.
	// Таблица читается до приёма запросов — первый же список озвучек уже
	// слит; не прочиталась — работаем на одном разборе подписей.
	voiceNames := voicenames.New(postgres.NewVoiceNamesRepo(pool), a.Log.With("part", "voicenames")).
		WithPreferred(providers.VoiceNameProviders()...)
	playbackSvc.WithSpellings(voiceNames)
	voiceNames.OnChange(playbackSvc.ResetVoices)
	voiceNames.Start(ctx)
	// Разметка пропуска: площадки, отдающие её отдельным запросом, и
	// AniSkip. AniSkip ходит своим маршрутом (по умолчанию напрямую — с
	// сервера он открывается без VPN, сверено 03.10.2026).
	skipsRoute, ok := routes.Client(env("ANISKIP_EGRESS", kit.DirectRoute))
	if !ok {
		return fmt.Errorf("ANISKIP_EGRESS: маршрута %q нет", env("ANISKIP_EGRESS", kit.DirectRoute))
	}
	skipsSvc := skips.New(catalogRepo, registry.Segments,
		aniskip.New(skipsRoute, env("ANISKIP_URL", "")), a.Log.With("part", "skips"))
	shareSvc := appshare.NewService(
		postgres.NewShareRepo(pool),
		ticketAdapter{playbackSvc},
		crypto.NewTicketIDGen(),
		clock,
	)

	if err := a.seedAdmin(ctx, users, creds, hasher, totp); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	if err := a.resetTOTP(ctx, users, creds, audit); err != nil {
		return err
	}

	// ── транспорт: два пространства, два слушателя ─────────────────────────
	// Потолок кеша картинок. Диск дешевле памяти, но не бесконечен:
	// без предела это не кеш, а вечное хранилище.
	imgs, err := imgcache.New(env("ART_CACHE_DIR", "/var/cache/kino/art"),
		int64(envInt("ART_CACHE_MB", 2048))<<20)
	if err != nil {
		return fmt.Errorf("кеш картинок: %w", err)
	}
	imgs.StartSweeper(ctx.Done(), func(freed int64) {
		a.Log.Info("кеш картинок подчищен", "освобождено_мб", freed>>20)
	})

	userSrv := httpuser.New(authSvc, progressSvc, playbackSvc, telemetrySvc, historySvc, historySource, lazyCatalog, progressRepo, marksSvc, postgres.NewFeedsRepo(pool), catalogRepo, shareSvc,
		searchhistory.NewService(postgres.NewSearchHistoryRepo(pool)), upstream, imgs,
		env("PUBLIC_URL", ""),
		a.Log.With("space", "user"), a.Cfg.SecureCookies, a.Cfg.GatewayPublicURL).
		WithProviderNames(registry.Name).
		WithArtFallback(artDirect).
		WithThumbnails(lazyCatalog, registry.CanThumb).
		WithSkips(skipsSvc).
		WithServerName(env("SERVER_NAME", ""))
	rpID, origins := webauthnParams(env("PUBLIC_URL", ""))
	engine, err := webauthnadapter.New(rpID, "kino", origins)
	if err != nil {
		return err
	}
	adminSrv, err := httpadmin.New(authSvc, audit, postgres.NewSyncStateRepo(pool), sessionRepo,
		sessions, engine, env("PUBLIC_URL", ""), a.Log.With("space", "admin"), a.Cfg.SecureCookies)
	if err != nil {
		return err
	}
	adminSrv.WithCatalog(catalogRepo, registry.Name, pcfg.IDs).
		WithHistory(historySvc, historySource).
		WithProviderAuth(providerHealth.Reauths, auths.all).
		WithVoiceNames(voiceNames)

	// Служебное пространство: сюда ходит только шлюз — за обновлением
	// протухшего тикета и с докладом о ходе внешнего плеера. Наружу порт
	// не выставляется.
	svcSrv := grpcservice.New(playbackSvc, progressSvc, redisadapter.NewTicketStore(rdb),
		os.Getenv("SERVICE_TOKEN"), a.Log.With("space", "service"))
	service := &http.Server{
		Addr: a.Cfg.GRPCAddr, Handler: svcSrv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	public := &http.Server{
		Addr: a.Cfg.HTTPAddr, Handler: userSrv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Админка на отдельном слушателе, под префиксом /admin. Наружу её
	// выводит Caddy тем же доменом, что и витрину; свой слушатель нужен,
	// чтобы пространства не делили ни маршрутизатор, ни middleware.
	admin := &http.Server{
		Addr: a.Cfg.AdminAddr, Handler: adminSrv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 3)
	go serve(public, "public", a.Log, errCh)
	go serve(admin, "admin", a.Log, errCh)
	go serve(service, "service", a.Log, errCh)

	// Уборка просроченного: без неё телеметрия растёт бесконечно ради
	// строк, на которые никто не смотрит (ADR-0005).
	retention.NewService(postgres.NewRetentionRepo(pool), a.Log.With("job", "retention")).Start(ctx)

	a.Log.Info("core started", "public", a.Cfg.HTTPAddr, "admin", a.Cfg.AdminAddr,
		"service", a.Cfg.GRPCAddr)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		a.Log.Info("shutting down")
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = public.Shutdown(shutCtx)
	_ = admin.Shutdown(shutCtx)
	_ = service.Shutdown(shutCtx)
	return nil
}

func serve(s *http.Server, name string, log *slog.Logger, errCh chan<- error) {
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("listener failed", "name", name, "err", err)
		errCh <- err
	}
}

// liveSearch спрашивает все умеющие искать площадки параллельно.
//
// Отказ одной площадки не прячет ответы остальных: поиск — это «найди
// хоть где-нибудь», а не «найди везде».
func liveSearch(registry *appprov.Registry, prio map[string]int, timeout time.Duration,
	log *slog.Logger, only ...string) appcatalog.LiveSearch {
	return func(ctx context.Context, q string, limit int) ([]dprovider.Title, error) {
		searchers := registry.Searchers(only...)
		results := make([][]dprovider.Title, len(searchers))
		var wg sync.WaitGroup
		for i, p := range searchers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				found, err := p.Search(sctx, q, limit)
				if err != nil {
					log.Debug("живой поиск: площадка не ответила", "err", err)
				}
				results[i] = found
			}()
		}
		wg.Wait()
		// Порядок — по приоритету площадок: одно и то же произведение
		// склеится в один тайтл, и первым в выдаче окажется найденное
		// главной площадкой, а не той, что ответила быстрее.
		var out []dprovider.Title
		for _, r := range results {
			out = append(out, r...)
		}
		sort.SliceStable(out, func(i, j int) bool {
			return prio[out[i].Ref.ProviderID] > prio[out[j].Ref.ProviderID]
		})
		return out, nil
	}
}

// idLookup спрашивает площадки, умеющие искать по номерам. Площадки —
// параллельно: у каждой свой хост, и досбор ждёт самую медленную, а не
// их сумму.
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

// continueAdapter переводит строку репозитория в тип use case'а.
// Слой app не должен импортировать adapter — поэтому склейка живёт здесь.
type continueAdapter struct{ repo *postgres.ProgressRepo }

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

// seedAdmin создаёт первого администратора на пустой базе и печатает
// otpauth-URL для authenticator'а. Идемпотентно: при непустой базе no-op.
// Без этого админку невозможно открыть — инвайты выдаёт только админ.
func (a *CoreApp) seedAdmin(ctx context.Context, users *postgres.UserRepo,
	creds *postgres.CredentialRepo, hasher *crypto.Argon2Hasher, totp crypto.TOTP) error {
	login, password := os.Getenv("ADMIN_BOOTSTRAP_LOGIN"), os.Getenv("ADMIN_BOOTSTRAP_PASSWORD")
	if login == "" || password == "" {
		return nil
	}
	existing, err := users.List(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	if err := account.ValidatePassword(password); err != nil {
		return fmt.Errorf("ADMIN_BOOTSTRAP_PASSWORD: %w", err)
	}
	hash, err := hasher.Hash(password)
	if err != nil {
		return err
	}
	u, err := users.Create(ctx, account.User{
		Login: login, DisplayName: login, Role: account.RoleAdmin, CreatedAt: time.Now(),
	}, account.Credential{Hash: hash})
	if err != nil {
		return err
	}
	secret, url, err := totp.Provision(login)
	if err != nil {
		return err
	}
	if err := creds.SetTOTPSecret(ctx, u.ID, secret); err != nil {
		return err
	}
	// Единственный раз, когда это печатается. Дальше секрет только в БД.
	a.Log.Warn("АДМИН СОЗДАН — добавьте в authenticator и удалите переменные окружения",
		"login", login, "otpauth", url)
	return nil
}

// resetTOTP снимает второй фактор с администратора из ADMIN_RESET_TOTP:
// при следующем входе он настроит его заново, прямо на странице входа.
//
// Для единственного администратора это единственный путь, когда
// authenticator потерян: перевыпустить фактор из админки может лишь другой
// администратор. Переменную после этого надо убрать — иначе каждый рестарт
// будет снимать фактор снова. Уже снятый повторно не трогается.
func (a *CoreApp) resetTOTP(ctx context.Context, users *postgres.UserRepo,
	creds *postgres.CredentialRepo, audit *postgres.AuditRepo) error {
	login := os.Getenv("ADMIN_RESET_TOTP")
	if login == "" {
		return nil
	}
	u, err := users.ByLogin(ctx, login)
	if err != nil {
		return fmt.Errorf("ADMIN_RESET_TOTP=%s: %w", login, err)
	}
	if u.Role != account.RoleAdmin {
		return fmt.Errorf("ADMIN_RESET_TOTP=%s: не администратор", login)
	}
	if !u.TOTPEnabled {
		a.Log.Warn("ADMIN_RESET_TOTP: второй фактор уже снят — УБЕРИТЕ переменную из окружения", "login", login)
		return nil
	}
	if err := creds.ClearTOTPSecret(ctx, u.ID); err != nil {
		return err
	}
	_ = audit.Record(ctx, u.ID, "user.reset_totp", u.Login, "ADMIN_RESET_TOTP при старте", nil, nil)
	a.Log.Warn("ВТОРОЙ ФАКТОР СНЯТ — войдите в админку и настройте его заново, затем УБЕРИТЕ переменную",
		"login", login)
	return nil
}

// webauthnParams — RP ID и допустимые origin'ы для passkey. По умолчанию из
// PUBLIC_URL: ключ привязан к домену, и на другом домене (или через туннель
// по localhost) он не работает — там остаётся пароль + код. Без PUBLIC_URL
// это локальная разработка на localhost.
func webauthnParams(publicURL string) (string, []string) {
	if id := os.Getenv("WEBAUTHN_RP_ID"); id != "" {
		var origins []string
		for _, o := range strings.Split(os.Getenv("WEBAUTHN_ORIGINS"), ",") {
			if o = strings.TrimSpace(o); o != "" {
				origins = append(origins, o)
			}
		}
		return id, origins
	}
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		return u.Hostname(), []string{u.Scheme + "://" + u.Host}
	}
	return "localhost", []string{"http://localhost:5173", "http://localhost:8081"}
}

// env — маленький помощник для необязательных путей.
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

// ticketAdapter переводит тикет воспроизведения в то немногое, что нужно
// ссылке. Склейка живёт здесь: слой app не должен знать про соседний слой
// app, иначе use cases начнут зависеть друг от друга.
type ticketAdapter struct{ p *playback.Service }

func (t ticketAdapter) IssueTicket(ctx context.Context, u kernel.UserID, e kernel.EpisodeID,
	cp dprovider.ClientProfile) (appshare.TicketInfo, dprovider.Source, error) {
	tk, src, err := t.p.IssueTicket(ctx, u, e, cp)
	if err != nil {
		return appshare.TicketInfo{}, dprovider.Source{}, err
	}
	return appshare.TicketInfo{ID: tk.ID, ExpiresAt: tk.ExpiresAt}, src, nil
}

// authBoard — последнее известное состояние входов площадок для админки.
type authBoard struct {
	mu sync.Mutex
	m  map[string]httpadmin.AuthInfo
}

func (b *authBoard) set(id string, a httpadmin.AuthInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.m[id] = a
}

func (b *authBoard) all() map[string]httpadmin.AuthInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	return maps.Clone(b.m)
}
