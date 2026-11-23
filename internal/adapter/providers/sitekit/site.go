package sitekit

import (
	"context"
	"time"
)

// Site — площадка на чужих плеерах, описанная снаружи кода плееров (скриптом,
// ADR-0017). Её дело — каталог: страница тайтла, поиск, обход и где на
// странице серии стоят плееры. Серии, озвучки, поток, разметку пропуска и
// кадры добывает общий код чужих плееров (у площадок — providers/jutsu,
// подключается к хосту скриптов портом script.Players, ADR-0018).
//
// Модель — «страница = один сезон», как у аниме-площадок: у сериала серии
// сезона 1 этой страницы, у фильма — одна «серия».
type Site interface {
	Page(ctx context.Context, id string) (SitePage, error)
	Search(ctx context.Context, query string, limit int) ([]SitePage, error)
	// Walk обходит тайтлы: все (full) или свежие. Площадке без обхода —
	// ничего не делать и вернуть nil.
	Walk(ctx context.Context, full bool, fn func(SitePage) error) error
	Players(ctx context.Context, page SitePage, episode int) (SitePlayers, error)
}

// SitePage — тайтл площадки, разобранный из её страницы или API.
type SitePage struct {
	ID, URL   string
	Title     string
	Season    int
	Original  string
	AltTitles []string
	Movie     bool
	Aired     int
	Finished  bool
	Genres    []string
	Countries []string
	Year      int
	Poster    string
	Plot      string
	Minutes   int
	MAL, KP   string
	IMDB      string
	// EpisodeNums — номера серий, если площадка их перечисляет; иначе они
	// берутся у плееров (CVH, Kodik).
	EpisodeNums []int
	// EpisodeSecs — точная длина серий по номеру, если площадка её знает.
	EpisodeSecs map[int]int
	Popularity  int64
}

// SitePlayers — плееры серии.
type SitePlayers struct {
	// MAL и CVHPub — CDNVideoHub по номеру MyAnimeList.
	MAL, CVHPub string
	// Kodik — iframe Kodik: сериала, фильма или одной серии.
	Kodik string
	// Alloha — iframe балансера Alloha для этой серии.
	Alloha string
	// Referer — страница площадки, под которую выданы подписи плееров.
	Referer string
}

// SiteOptions — как называть площадку и что она умеет.
type SiteOptions struct {
	ID, Name, Version string
	// Home — главная площадки: проверка живости, если нет Health.
	Home, Health string
	// General — каталог не аниме: жанр «Аниме» не приписывается, номер
	// Кинопоиска идёт у сериалов тоже.
	General bool
	// Thumbs — кадры серий у CVH; Subtitles — внешние субтитры у Alloha.
	Thumbs, Subtitles bool
	// Gap — вежливая пауза между запросами к одному хосту площадки.
	Gap time.Duration
}
