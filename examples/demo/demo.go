// Package demo — пример площадки: четыре открытых фильма Blender Foundation
// (CC BY) с публичных тестовых HLS-потоков. Показывает протокол площадки
// целиком (ADR-0001, ADR-0017, ADR-0018) и даёт публичной сборке, где нет
// приватных площадок, хоть что-то посмотреть.
//
// Настоящая площадка устроена так же: описание (Describe), поиск, тайтл,
// серии, обход изменений (ChangedSince) и резолв серии в поток. Здесь каталог
// лежит в коде и запросов к сайту нет; у настоящей — разбор её API или
// страниц, а HTTP идёт клиентом, который даёт ядро (source.Env.HTTP):
// маршрутом площадки, с её подписью и счётчиком запросов.
//
// Как написать свою — README.md рядом.
package demo

import (
	"context"
	"strings"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/providers/sitekit"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// ProviderID — id площадки: под ним она в PROVIDERS, в ссылках тайтлов и в
// истории. Меняться не должен — привязки тайтлов к площадке хранятся по нему.
const ProviderID = "demo"

// version растёт с каждой правкой площадки: по нему видно, что работает.
const version = "1.0.0"

// film — тайтл каталога. У настоящей площадки это разобранный ответ её API.
type film struct {
	id        string // номер у площадки — то, что попадёт в ProviderRef.ItemID
	title     string
	original  string
	year      int
	minutes   int
	imdb      string
	genres    []string
	plot      string
	poster    string // CC BY, атрибуция — в README.md
	stream    string // HLS: мастер-плейлист
	countries []string
}

// catalog — открытые фильмы Blender Foundation. Потоки — тестовые копии,
// которые держат у себя разработчики видеоплееров (Mux, THEOplayer, Unified
// Streaming); постеры — Wikimedia Commons. Лицензии и авторы — README.md.
var catalog = []film{
	{
		id: "elephants-dream", title: "Сон слона", original: "Elephants Dream",
		year: 2006, minutes: 11, imdb: "tt0807840",
		genres: []string{"Мультфильм", "Фантастика", "Короткометражка"}, countries: []string{"Нидерланды"},
		plot:   "Старик Прок и юный Эмо бродят по бесконечной странной машине, и каждый видит её по-своему.",
		poster: "https://upload.wikimedia.org/wikipedia/commons/thumb/0/0c/ElephantsDreamPoster.jpg/500px-ElephantsDreamPoster.jpg",
		stream: "https://cdn.theoplayer.com/video/elephants-dream/playlist.m3u8",
	},
	{
		id: "big-buck-bunny", title: "Большой Бак Банни", original: "Big Buck Bunny",
		year: 2008, minutes: 10, imdb: "tt1254207",
		genres: []string{"Мультфильм", "Комедия", "Короткометражка"}, countries: []string{"Нидерланды"},
		plot:   "Добродушный кролик-великан отвечает трём лесным хулиганам за обиды.",
		poster: "https://upload.wikimedia.org/wikipedia/commons/thumb/c/c5/Big_buck_bunny_poster_big.jpg/500px-Big_buck_bunny_poster_big.jpg",
		stream: "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8",
	},
	{
		id: "sintel", title: "Синтел", original: "Sintel",
		year: 2010, minutes: 15, imdb: "tt1727587",
		genres: []string{"Мультфильм", "Фэнтези", "Короткометражка"}, countries: []string{"Нидерланды"},
		plot:   "Девушка Синтел ищет дракончика, которого когда-то выходила, и проходит ради него полмира.",
		poster: "https://upload.wikimedia.org/wikipedia/commons/thumb/8/8f/Sintel_poster.jpg/500px-Sintel_poster.jpg",
		stream: "https://cdn.theoplayer.com/video/sintel/index.m3u8",
	},
	{
		id: "tears-of-steel", title: "Слёзы стали", original: "Tears of Steel",
		year: 2012, minutes: 12, imdb: "tt2285752",
		genres: []string{"Фантастика", "Короткометражка"}, countries: []string{"Нидерланды"},
		plot:   "Амстердам будущего: учёные пытаются спасти мир от роботов, вернувшись к одной давней ссоре.",
		poster: "https://upload.wikimedia.org/wikipedia/commons/thumb/7/70/Tos-poster.png/500px-Tos-poster.png",
		stream: "https://demo.unified-streaming.com/k8s/features/stable/video/tears-of-steel/tears-of-steel.ism/.m3u8",
	},
}

// updated — когда каталог менялся последний раз (для ChangedSince).
var updated = time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

// Provider — площадка. Состояния у площадки нет (ADR-0001): всё, что нужно
// помнить между вызовами, хранит ядро.
type Provider struct {
	now func() time.Time
}

// New собирает площадку. Настоящей сюда приходит http-клиент её маршрута
// (source.Env.HTTP) и её настройки (source.Env.Setting).
func New() *Provider { return &Provider{now: time.Now} }

// Describe — рукопожатие: id, версия контракта и что площадка умеет. Ядро
// проверяет совместимость до того, как площадка станет доступна; у настоящей
// площадки здесь же — проверка, что сайт отвечает.
func (p *Provider) Describe(context.Context) (dp.Descriptor, error) {
	return dp.Descriptor{
		APIMajor: dp.APIMajor, ProviderID: ProviderID, ProviderVersion: version,
		Name: "Открытые фильмы (пример)",
		// Lookup заявлен — значит, метод Lookup обязан быть (providertest это
		// сверяет). Заявлять умение, которого нет, нельзя: ядро на него положится.
		Capabilities: []dp.Capability{dp.CapSearch, dp.CapSyncDelta, dp.CapHLS, dp.CapLookup},
	}, nil
}

// Search — поиск по названию. Ничего не нашлось — пустой список, не ошибка.
func (p *Provider) Search(_ context.Context, query string, limit int) ([]dp.Title, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []dp.Title
	for _, f := range catalog {
		if q != "" && (strings.Contains(strings.ToLower(f.title), q) || strings.Contains(strings.ToLower(f.original), q)) {
			out = append(out, f.toTitle())
		}
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

// Title — тайтл по номеру у площадки. Нет такого — kernel.ErrNotFound: по
// этой ошибке ядро отличает «нет тайтла» от «площадка сломалась».
func (p *Provider) Title(_ context.Context, ref kernel.ProviderRef) (dp.Title, error) {
	f, ok := find(ref.ItemID)
	if !ok {
		return dp.Title{}, kernel.ErrNotFound
	}
	return f.toTitle(), nil
}

// Episodes — серии. У фильма — одна «серия» с сезоном 0; у сериала —
// сезоны и серии в нумерации площадки (каноническую ведёт ядро).
func (p *Provider) Episodes(_ context.Context, ref kernel.ProviderRef) ([]dp.Episode, error) {
	f, ok := find(ref.ItemID)
	if !ok {
		return nil, kernel.ErrNotFound
	}
	return []dp.Episode{{Ref: f.ref(), SeasonNo: 0, EpisodeNo: 1, Title: f.title, DurationSec: f.minutes * 60,
		DurationApprox: true}}, nil
}

// ChangedSince — обход изменений: обходчик зовёт его по расписанию. Нулевое
// since — весь каталог. Обход останавливается сам, как только вышел за
// границу: у настоящей площадки полный обход ради вчерашних правок — часы.
func (p *Provider) ChangedSince(_ context.Context, since time.Time, fn func(dp.Title) error) error {
	if !updated.After(since) {
		return nil
	}
	for _, f := range catalog {
		if err := fn(f.toTitle()); err != nil {
			return err
		}
	}
	return nil
}

// Lookup — тайтл по внешним номерам (IMDb, Кинопоиск): так ядро дособирает
// площадки к тайтлу, открытому из другой площадки. Нет — kernel.ErrNotFound.
func (p *Provider) Lookup(_ context.Context, ids dp.ExternalIDs) (dp.Title, error) {
	for _, f := range catalog {
		if ids.IMDB != "" && ids.IMDB == f.imdb {
			return f.toTitle(), nil
		}
	}
	return dp.Title{}, kernel.ErrNotFound
}

// Resolve — серия в поток. Источник уходит в тикет: шлюз тянет байты сам и
// пускает только хосты из AllowedOrigins (ADR-0002), поэтому туда — домен
// потока, а ExpiresAt — когда ссылка перестанет работать (у этих — никогда,
// но тикету нужен срок).
func (p *Provider) Resolve(_ context.Context, req dp.ResolveRequest) (dp.Source, error) {
	f, ok := find(req.Ref.ItemID)
	if !ok || req.Season != 0 || req.Episode != 1 {
		return dp.Source{}, kernel.ErrNotFound
	}
	return dp.Source{
		Protocol:       "HLS",
		URL:            f.stream,
		AllowedOrigins: []string{sitekit.OriginOf(f.stream)},
		ExpiresAt:      p.now().Add(6 * time.Hour),
	}, nil
}

func find(id string) (film, bool) {
	for _, f := range catalog {
		if f.id == id {
			return f, true
		}
	}
	return film{}, false
}

func (f film) ref() kernel.ProviderRef {
	return kernel.ProviderRef{ProviderID: ProviderID, ItemID: f.id}
}

// toTitle — тайтл в канонической форме ядра: IMDb вида tt…, без HTML.
func (f film) toTitle() dp.Title {
	return dp.Title{
		Ref: f.ref(), Kind: "movie", IMDB: f.imdb,
		Title: f.title, Original: f.original, Year: f.year, Plot: f.plot,
		Genres: f.genres, Countries: f.countries, Poster: f.poster, Finished: true,
		UpdatedAt: updated, CreatedAt: updated,
	}
}
