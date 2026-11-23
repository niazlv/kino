package crawler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/postgres"
	"github.com/niazleushkin/kino/internal/adapter/shikimori"
	appcatalog "github.com/niazleushkin/kino/internal/app/catalog"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/bootstrap/kit"
	"github.com/niazleushkin/kino/internal/bootstrap/providers"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/config"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

type CrawlerApp struct {
	Cfg config.Crawler
	Log *slog.Logger
}

func NewCrawler(cfg config.Crawler, log *slog.Logger) *CrawlerApp {
	return &CrawlerApp{Cfg: cfg, Log: log}
}

func (a *CrawlerApp) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pg.NewPool(ctx, os.Getenv("POSTGRES_DSN"))
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	// Браузерный отпечаток обязателен: CDN и Cloudflare режут по JA3/JA4,
	// а обычный клиент Go под это попадает (ADR-0003). Маршрут наружу у
	// каждой площадки свой: kinopub нужен VPN, аниме-площадкам — не обязательно.
	routes, err := kit.LoadRoutes(60 * time.Second)
	if err != nil {
		return err
	}
	pcfg, err := providers.LoadConfig()
	if err != nil {
		return err
	}
	registry, err := providers.BuildRegistry(ctx, pcfg, routes, a.Log)
	if err != nil {
		return err
	}
	catalogRepo := func() *postgres.CatalogRepo {
		return postgres.NewCatalogRepo(pool).WithPriorities(pcfg.Priority).
			WithAnimeProviders(providers.AnimeProviders()...).
			WithCartoonMatching(pcfg.MatchCartoon())
	}

	// Ключи названий для склейки у тайтлов, заведённых до неё. Один раз и
	// в фоне: без них аниме-площадка не узнала бы уже известные тайтлы и
	// завела бы дубли.
	go func() {
		n, err := catalogRepo().BackfillNameKeys(ctx)
		if err != nil {
			a.Log.Warn("ключи названий не достроены", "err", err, "тайтлов", n)
			return
		}
		if n > 0 {
			a.Log.Info("ключи названий достроены", "тайтлов", n)
		}
		// Варианты ключей (свёрнутый ромадзи, порядок слов) — той же
		// функцией, что пишет их новым тайтлам: старые без этого прохода
		// различали бы только точную форму.
		n, err = catalogRepo().BackfillVariantKeys(ctx)
		if err != nil {
			a.Log.Warn("варианты ключей не дописаны", "err", err)
			return
		}
		if n > 0 {
			a.Log.Info("варианты ключей дописаны", "ключей", n)
		}
	}()

	every := time.Duration(envInt("CRAWLER_INTERVAL_MIN", 30)) * time.Minute

	// Реконсилиация: склейка, развалившаяся при первом заходе (площадки
	// пишут одно произведение по-разному, а MAL добирается позже),
	// чинится фоном. Сначала добор MAL — это топливо для неё.
	//
	// Реконсилиатор ОДИН на все проходы: его память (какие элементы не
	// отдали MAL, какие пары отвергнуты) переживает тики, иначе каждый
	// проход заново опрашивал бы те же сотни молчащих страниц.
	if os.Getenv("CRAWLER_RECONCILE") != "false" {
		rec := appcatalog.NewReconciler(
			catalogRepo(), catalogRepo(), catalogRepo(), catalogRepo(),
			func(id string) (appcatalog.TitleSource, error) {
				p, err := registry.Get(id)
				if err != nil {
					return nil, err
				}
				return p.Title, nil
			},
			providers.AnimeProviders(), a.Log.With("job", "reconcile")).
			WithCartoonMerge(pcfg.MergeCartoon())
		// Мост по названиям: хозяин метаданных (Spec.TitleBridge) своего MAL
		// не пишет, и без реестра половина дублей не склеивается никогда. Выключается реестром
		// (пустой SHIKIMORI_URL) — остальная реконсилиация продолжает
		// работать.
		if bridgeID := providers.TitleBridgeProvider(); bridgeID != "" && os.Getenv("SHIKIMORI_URL") != "off" {
			route := os.Getenv("SHIKIMORI_ROUTE")
			hc, ok := routes.Client(route)
			if !ok {
				return fmt.Errorf("маршрут SHIKIMORI_ROUTE=%q не настроен", route)
			}
			rec = rec.WithBridge(bridgeID, catalogRepo(),
				shikimori.New(hc, os.Getenv("SHIKIMORI_URL")))
		}
		// Лечение сезонов, склеенных не туда: «Гинтама 3» отдельным тайтлом,
		// одна работа двух площадок на разных сезонах. CRAWLER_FIX_SEASONS=dry —
		// только журнал решений, off — выключено.
		if mode := os.Getenv("CRAWLER_FIX_SEASONS"); mode != "off" {
			rec = rec.WithSeasons(catalogRepo(), mode == "dry")
		}
		go loop(ctx, every, func() bool {
			// Реконсилиации нужны только аниме-площадки: MAL добирается у
			// них, пары и решения читаются из базы. Ждать kinopub с его
			// VPN-маршрутом нечего: он бывает недоступен часами, и дубли
			// ждали бы его вместе с ним.
			// Только включённые в этой сборке: площадку, которой нет в
			// PROVIDERS, ждать бессмысленно — реконсилиация не пошла бы никогда.
			ready := true
			for _, id := range pcfg.Enabled(providers.AnimeProviders()) {
				if _, err := registry.Get(id); err != nil {
					ready = false
					break
				}
			}
			if !ready {
				return false
			}
			if n, err := rec.EnrichMAL(ctx); err != nil {
				a.Log.Warn("добор MAL прерван", "err", err)
			} else if n > 0 {
				a.Log.Info("MAL добран", "привязок", n)
			}
			if n, err := rec.EnrichMALByNames(ctx); err != nil {
				a.Log.Warn("мост MAL по названиям прерван", "err", err)
			} else if n > 0 {
				a.Log.Info("MAL по названиям добран", "тайтлов", n)
			}
			// Сезоны — до слияния дублей: ShouldMerge отказывает паре, где
			// одна работа стоит на разных сезонах, и лечит её перенос.
			if fix, err := rec.FixSeasons(ctx); err != nil {
				a.Log.Warn("лечение сезонов прервано", "err", err)
			} else if fix.Realigned > 0 || fix.Adopted > 0 {
				a.Log.Info("сезоны вылечены", "привязок выровнено", fix.Realigned, "тайтлов перенесено", fix.Adopted)
			}
			if n, err := rec.Run(ctx); err != nil {
				a.Log.Warn("реконсилиация прервана", "err", err)
			} else if n > 0 {
				a.Log.Info("реконсилиация слила дубли", "тайтлов", n)
			}
			return true
		})
	}

	// Добор серий по отслеживаемому — со всех площадок тайтла. Ленивая
	// догрузка наполняет каталог только при открытии тайтла — этого хватает
	// для просмотра, но не для счётчиков: у неоткрытых сериалов они занижены.
	if os.Getenv("CRAWLER_BACKFILL") != "false" {
		go loop(ctx, every, func() bool {
			// Сразу после старта площадки ещё регистрируются: добор по ним
			// провалился бы целиком и ждал следующего прохода полчаса.
			if len(registry.IDs()) < len(pcfg.IDs) {
				return false
			}
			bf := appcatalog.NewBackfill(
				postgres.NewMarksRepo(pool),
				catalogRepo(),
				func(id string) (appcatalog.EpisodeSource, error) { return registry.Get(id) },
				a.Log.With("job", "backfill"),
			)
			if _, err := bf.Run(ctx); err != nil {
				a.Log.Warn("добор серий прерван", "err", err)
			}
			return true
		})
	}

	// Каждая площадка обходится своим циклом: полный обход kinopub тянется
	// часами, и аниме-площадки не должны его ждать. Обходчики живут в карте,
	// чтобы перескан из админки мог будить конкретную площадку.
	walkers := map[string]*walker{}
	for _, id := range pcfg.IDs {
		w := &walker{
			id: id, registry: registry, repo: catalogRepo(),
			state: postgres.NewSyncStateRepo(pool), policy: fullWalkPolicy(a.Log),
			log: a.Log.With("provider", id),
		}
		walkers[id] = w
		go loop(ctx, every, func() bool { return w.pass(ctx) })
	}

	// «Пересканировать сейчас» из админки: заявка лежит в базе, и ждать
	// ближайшего получасового прохода незачем. Короткий цикл будит только
	// заявленные площадки; сам проход — общий с расписанием, а заявка
	// съедается в нём.
	if os.Getenv("CRAWLER_RESCAN_WATCH") != "false" {
		go loop(ctx, 20*time.Second, func() bool {
			ids, err := postgres.NewSyncStateRepo(pool).RescanRequested(ctx)
			if err != nil {
				a.Log.Warn("заявки на перескан не прочитаны", "err", err)
				return true
			}
			for _, id := range ids {
				w, ok := walkers[id]
				if !ok {
					continue
				}
				w.log.Info("перескан заявлен из админки — полный обход")
				w.pass(ctx)
			}
			return true
		})
	}
	<-ctx.Done()
	a.Log.Info("crawler остановлен")
	return nil
}

// notReadyRetry — через сколько повторить проход, если площадка ещё не
// зарегистрировалась. Ждать целый интервал нельзя: после деплоя первый
// проход откладывался бы на полчаса.
const notReadyRetry = 15 * time.Second

// loop зовёт fn сразу и потом раз в every, пока жив ctx. fn возвращает
// false, когда проходить было не по чему (площадка не готова), — тогда
// повтор скоро.
func loop(ctx context.Context, every time.Duration, fn func() bool) {
	for {
		wait := every
		if !fn() {
			wait = notReadyRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// walker — обход одной площадки.
type walker struct {
	id       string
	registry *appprov.Registry
	repo     *postgres.CatalogRepo
	state    *postgres.SyncStateRepo
	policy   appcatalog.FullWalkPolicy
	log      *slog.Logger
	// Проходы не должны идти параллельно: расписание и перескан из админки
	// могут сработать в один момент, и два полных обхода подряд лягут на
	// площадку двойной нагрузкой.
	mu sync.Mutex
}

func (w *walker) pass(ctx context.Context) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Провайдер регистрируется в фоне и может быть ещё не готов:
	// площадка периодически недоступна. Это состояние, а не повод
	// падать — берём его на каждом проходе заново.
	provider, err := w.registry.Get(w.id)
	if err != nil {
		w.log.Warn("провайдер ещё не готов", "err", err)
		return false
	}
	d, _ := w.registry.Descriptor(w.id)

	// Заявка «пересканировать сейчас» из админки: забирается атомарно,
	// и этот проход становится полным обходом.
	forced, err := w.state.ConsumeRescan(ctx, w.id)
	if err != nil {
		w.log.Warn("заявка на перескан не прочитана", "err", err)
	}

	sync := appcatalog.NewSyncService(w.repo, w.state, kit.Clock{}, w.log)
	opts := appcatalog.Options{
		// 0 — без ограничения. Ограничение полезно только для первого
		// знакомства: полный каталог это десятки тысяч тайтлов.
		MaxTitles: envInt("CRAWLER_MAX_TITLES", 0),
		// По умолчанию серии НЕ тянем: это отдельный запрос на тайтл, то есть
		// десятки тысяч обращений ради тайтлов, которые никто не откроет.
		// Серии догружаются при первом открытии тайтла.
		WithEpisodes: os.Getenv("CRAWLER_WITH_EPISODES") == "true",
	}

	// CRAWLER_FULL_RESYNC=true — идти целиком на каждом проходе. Это для
	// первого заполнения и отладки; на боевом так оставлять нельзя: полный
	// каталог каждые полчаса держит аккаунт площадки в ограничении, и оно
	// прилетает тому, кто нажал «смотреть».
	alwaysFull := os.Getenv("CRAWLER_FULL_RESYNC") == "true"
	lastFull, err := w.state.LastFull(ctx, w.id)
	if err != nil {
		w.log.Warn("не прочитать отметку полного обхода", "err", err)
	}
	full := forced || alwaysFull || w.policy.WantFull(time.Now(), lastFull)
	if !full && !d.Has(dp.CapSyncDelta) {
		// Площадка не умеет отдавать изменения — ходить по ней «по
		// курсору» значит ходить целиком каждые полчаса. Такие площадки
		// обходятся только по расписанию полного обхода.
		return true
	}
	opts.FullResync = full
	switch {
	case full && d.Has(dp.CapPagedCatalog):
		// Каталог страницами из массового API: пауза на тайтл ничего не
		// бережёт, а от прошлого размера каталога растянула бы первый
		// обход выросшего каталога на сутки (ЛордФильм: 3,9 тыс. → 90 тыс.).
		w.log.Info("полный обход каталога без растяжки", "last_full", lastFull)
	case full:
		// Растягиваем на CRAWLER_FULL_SPREAD_MIN, оценивая объём по тому,
		// сколько тайтлов этой площадки уже знаем.
		opts.Spread = time.Duration(envInt("CRAWLER_FULL_SPREAD_MIN", 150)) * time.Minute
		if n, err := w.state.RefCount(ctx, w.id); err == nil {
			opts.ExpectedTitles = n
		}
		w.log.Info("полный обход каталога", "last_full", lastFull,
			"spread", opts.Spread.String(), "expected", opts.ExpectedTitles)
	}
	n, err := sync.Run(ctx, provider, opts)
	if err != nil {
		// Площадка недоступна или токен протух — это состояние, а не
		// повод падать: следующий проход может пройти.
		w.log.Error("синк не завершён", "err", err, "titles", n)
		return true
	}
	if full && !alwaysFull {
		if err := w.state.MarkFull(ctx, w.id, time.Now()); err != nil {
			w.log.Warn("не записать отметку полного обхода", "err", err)
		}
	}
	return true
}

// fullWalkPolicy читает расписание полного обхода из окружения.
//
//	CRAWLER_FULL_EVERY_H  — не чаще, чем раз в столько часов (84 — полнедели)
//	CRAWLER_NIGHT_FROM/TO — ночное окно по часам (2..6)
//	CRAWLER_TZ            — в чьём времени ночь (Europe/Moscow)
func fullWalkPolicy(log *slog.Logger) appcatalog.FullWalkPolicy {
	tz := os.Getenv("CRAWLER_TZ")
	if tz == "" {
		tz = "Europe/Moscow"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Warn("CRAWLER_TZ не распознан, считаю по UTC", "tz", tz, "err", err)
		loc = time.UTC
	}
	return appcatalog.FullWalkPolicy{
		Every:     time.Duration(envInt("CRAWLER_FULL_EVERY_H", 84)) * time.Hour,
		NightFrom: envInt("CRAWLER_NIGHT_FROM", 2),
		NightTo:   envInt("CRAWLER_NIGHT_TO", 6),
		Loc:       loc,
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
