package httpuser

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/adapter/imgcache"
	"github.com/niazleushkin/kino/internal/app/auth"
	appcatalog "github.com/niazleushkin/kino/internal/app/catalog"
	"github.com/niazleushkin/kino/internal/app/history"
	"github.com/niazleushkin/kino/internal/app/marks"
	"github.com/niazleushkin/kino/internal/app/playback"
	"github.com/niazleushkin/kino/internal/app/progress"
	"github.com/niazleushkin/kino/internal/app/searchhistory"
	appshare "github.com/niazleushkin/kino/internal/app/share"
	"github.com/niazleushkin/kino/internal/app/telemetry"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dplayback "github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

type Server struct {
	auth          *auth.Service
	progress      *progress.Service
	playback      *playback.Service
	telemetry     *telemetry.Service
	history       *history.Service
	catalog       CatalogReader
	epProgress    EpisodeProgress
	marks         *marks.Service
	searchHist    *searchhistory.Service
	feeds         Feeds
	browser       Browser
	share         *appshare.Service
	publicURL     string
	art           *http.Client
	artFallback   *http.Client
	imgs          *imgcache.Cache
	historySource func() (history.Source, error)
	log           *slog.Logger
	secureCookies bool
	// gatewayBase — публичный адрес медиа-шлюза. Клиенту отдаётся ссылка
	// на НЕГО, а не на площадку: адрес источника наружу не уходит.
	gatewayBase string
	// names — как назвать площадку человеку.
	names func(id string) string
	// thumbs/canThumb — кадры серий по запросу, см. WithThumbnails.
	thumbs   EpisodeThumbnails
	canThumb func(providerID string) bool
	// skips — разметка пропуска, см. WithSkips.
	skips Skips
	// local — локальный движок (ADR-0015): витрине это видно из /me, чтобы
	// не рисовать выход, пароль и подключение устройств.
	local bool
	// serverName — имя сервера в визитке /api/v1/server (SERVER_NAME).
	serverName string
}

// EpisodeThumbnails — кадр серии по запросу (см. catalog.LazyCatalog).
type EpisodeThumbnails interface {
	EpisodeThumbnail(ctx context.Context, id kernel.EpisodeID) (string, error)
}

// WithThumbnails включает кадры серий, которые площадка отдаёт по запросу.
// canThumb — умеет ли это площадка: витрине заранее говорится, что кадр
// стоит попросить, хотя в каталоге его ещё нет.
func (s *Server) WithThumbnails(t EpisodeThumbnails, canThumb func(providerID string) bool) *Server {
	s.thumbs, s.canThumb = t, canThumb
	return s
}

// WithArtFallback — запасной маршрут для постеров и кадров: им идут, если
// основной не ответил за artHedge или отказал.
func (s *Server) WithArtFallback(c *http.Client) *Server {
	s.artFallback = c
	return s
}

// WithLocalMode помечает сервер как локальный движок: /me сообщает об этом
// клиентам. Сами запреты (выход, пароль) стоят в композиционном корне
// движка, а не здесь — в проде это вызов не делается.
func (s *Server) WithLocalMode() *Server {
	s.local = true
	return s
}

// WithProviderNames задаёт человеческие названия площадок.
func (s *Server) WithProviderNames(fn func(id string) string) *Server {
	s.names = fn
	return s
}

func (s *Server) providerName(id string) string {
	if s.names == nil {
		return id
	}
	return s.names(id)
}

// EpisodeProgress — прогресс по сериям тайтла, для отметок на странице.
type EpisodeProgress interface {
	ForTitle(ctx context.Context, u kernel.UserID, t kernel.TitleID) (map[kernel.EpisodeID]dplayback.Progress, error)
}

// CatalogReader — чтение каталога для витрины.
type CatalogReader interface {
	Search(ctx context.Context, q string, limit, offset int) (appcatalog.SearchPage, error)
	Suggest(ctx context.Context, q string, limit int) ([]catalog.Title, error)
	Title(ctx context.Context, id kernel.TitleID) (catalog.Title, error)
	Episode(ctx context.Context, id kernel.EpisodeID) (catalog.Episode, error)
	Episodes(ctx context.Context, id kernel.TitleID) ([]catalog.Episode, error)
}

func New(a *auth.Service, p *progress.Service, pb *playback.Service, tm *telemetry.Service,
	h *history.Service, historySource func() (history.Source, error), cat CatalogReader, ep EpisodeProgress, mk *marks.Service, fd Feeds, br Browser, sh *appshare.Service,
	shr *searchhistory.Service,
	art *http.Client, imgs *imgcache.Cache, publicURL string,
	log *slog.Logger, secureCookies bool, gatewayBase string) *Server {
	return &Server{
		auth: a, progress: p, playback: pb, telemetry: tm,
		history: h, historySource: historySource, catalog: cat, epProgress: ep, marks: mk, feeds: fd, browser: br, share: sh, searchHist: shr, art: art, imgs: imgs,
		publicURL: strings.TrimRight(publicURL, "/"),
		log:       log, secureCookies: secureCookies,
		gatewayBase: strings.TrimRight(gatewayBase, "/"),
	}
}

// Routes — публичная поверхность. Версия в пути: фронт в PWA кеширует
// старый бандл, и старая версия API обязана дожить до его обновления
// (ось версионирования 4).
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.Recover(s.log), httpx.Log(s.log))

	// Без входа: сам токен и есть право доступа. На верхнем уровне, потому
	// что это ссылка для человека, а не вызов API.
	r.Get("/d/{token}", s.handleManifest)
	r.Get("/s/{token}", s.handleSharedWatch)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})

		// Без сессии.
		// Визитка: приложение проверяет введённый адрес сервера и
		// узнаёт, какую форму входа и регистрации показать.
		r.Get("/server", s.handleServerInfo)
		r.Post("/auth/register", s.handleRegister)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/logout", s.handleLogout)
		// Вход устройства (утилита-загрузчик): код, опрос, продление.
		r.Post("/auth/device", s.handleDeviceBegin)
		r.Post("/auth/device/token", s.handleDevicePoll)
		r.Post("/auth/refresh", s.handleRefresh)

		// С сессией пространства user.
		r.Group(func(r chi.Router) {
			r.Use(s.requireUser)
			r.Get("/me", s.handleMe)
			r.Put("/me/password", s.handleChangePassword)
			// «Где вы вошли»: свои сессии витрины и выход на устройстве.
			r.Get("/me/sessions", s.handleMySessions)
			r.Delete("/me/sessions", s.handleSignOutOthers)
			r.Delete("/me/sessions/{sessionID}", s.handleSignOutSession)
			// Подтверждение входа устройства — своей сессией, на сайте.
			r.Get("/auth/device/{userCode}", s.handleDeviceInfo)
			r.Post("/auth/device/{userCode}/approve", s.handleDeviceApprove)
			r.Post("/auth/device/{userCode}/deny", s.handleDeviceDeny)
			r.Get("/continue", s.handleContinue)
			r.Get("/search", s.handleSearch)
			// Подсказки под строкой, пока набирают: только наш каталог.
			r.Get("/search/suggest", s.handleSearchSuggest)
			// История поиска: подсказки над строкой, удаление — крестиком.
			r.Get("/search/history", s.handleSearchHistory)
			r.Post("/search/history", s.handleSearchHistoryAdd)
			r.Delete("/search/history", s.handleSearchHistoryDelete)
			r.Get("/browse", s.handleBrowse)
			r.Get("/genres", s.handleGenres)
			r.Get("/titles/{titleID}", s.handleTitle)
			r.Get("/titles/{titleID}/art", s.handleArt)
			r.Get("/episodes/{episodeID}/art", s.handleEpisodeArt)
			r.Get("/episodes/{episodeID}/skips", s.handleSkips)
			// Намеренные отметки: слежу, буду смотреть, оценка.
			r.Get("/titles/{titleID}/marks", s.handleGetMarks)
			r.Put("/titles/{titleID}/marks", s.handlePutMarks)
			// Выбор озвучки и субтитров: отдельно от отметок, чтобы одно
			// не затирало другое.
			r.Put("/titles/{titleID}/prefs", s.handlePutPrefs)
			r.Get("/lists/{kind}", s.handleList)
			// Производные ленты: непросмотренное по отслеживаемому и история.
			r.Get("/new-episodes", s.handleNewEpisodes)
			r.Get("/history", s.handleHistory)
			r.Get("/progress/{episodeID}", s.handleGetProgress)
			r.Put("/progress/{episodeID}", s.handlePutProgress)
			// sendBeacon при закрытии страницы всегда отправляет POST.
			r.Post("/progress/{episodeID}", s.handlePutProgress)
			// Выпуск тикета: точка, где пользователь получает право смотреть.
			r.Post("/playback/{episodeID}", s.handlePlayback)
			// Озвучки серии со всех площадок: плеер просит их, когда поток
			// уже играет, — один список вместо «площадка, потом студия».
			r.Get("/episodes/{episodeID}/voices", s.handleVoices)
			// Ключи озвучек по подписям дорожек потока: правило склейки и
			// псевдонимы живут только на сервере (voicekeys.go).
			r.Post("/voices/keys", s.handleVoiceKeys)
			// Скачать одним файлом, без утилиты: площадка отдаёт
			// прогрессивный MP4, его достаточно протолкнуть через шлюз.
			r.Get("/episodes/{episodeID}/download", s.handleDirectDownload)
			// Манифест для загрузчика по обычной ссылке со страницы: право
			// даёт сессия, ссылка-разрешение не нужна.
			r.Get("/episodes/{episodeID}/manifest", s.handleEpisodeManifest)
			// Ручная отметка: автоматика знает только позицию плеера,
			// а «посмотрел» бывает и решением человека.
			r.Put("/progress/{episodeID}/watched", s.handleSetWatched)
			r.Put("/titles/{titleID}/seasons/{season}/watched", s.handleSetSeasonWatched)
			// Ссылки-разрешения: выдать, посмотреть свои, отозвать.
			r.Post("/share/{episodeID}", s.handleCreateShare)
			r.Get("/share", s.handleListShares)
			r.Delete("/share/{token}", s.handleRevokeShare)
			// Одна запись на просмотр, а не на запрос (ADR-0005).
			r.Post("/telemetry/session", s.handleSessionTelemetry)
			// Переноса с площадки на витрине нет вовсе: его запускает только
			// администратор из карточки пользователя (Policy.CanImportFor).
		})
	})
	return r
}

// progressOf — прогресс действующего пользователя по сериям тайтла.
// Чужой прогресс сюда не попадает: пользователь берётся из сессии.
func (s *Server) progressOf(r *http.Request, t kernel.TitleID) (map[kernel.EpisodeID]dplayback.Progress, error) {
	return s.epProgress.ForTitle(r.Context(), actorFrom(r.Context()).UserID, t)
}
