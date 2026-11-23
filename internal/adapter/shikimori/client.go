// Package shikimori — клиент реестра Shikimori.
//
// Реестр не площадка: каталога он не пишет и в витрине не участвует, а
// отвечает на один вопрос — «какой у этого произведения номер MyAnimeList».
// Номер — топливо склейки: аниме-площадки пишут его сами, kinopub — нет,
// и мост между ними строится только через общий реестр.
package shikimori

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	appcatalog "github.com/niazleushkin/kino/internal/app/catalog"
)

// Base — публичный адрес реестра. Отдельной константой, а не магической
// строкой в запросе: зеркало меняется чаще, чем код.
const Base = "https://shikimori.one"

// Client ищет произведения в реестре.
type Client struct {
	hc   *http.Client
	base string
	// ua обязателен: реестр без подписи приложения отвечает ограничением
	// скорости, и искать причину потом пришлось бы по таймаутам.
	ua string
}

// New собирает клиент. hc ходит выбранным маршрутом (обычно прямым:
// реестр — российский сайт, VPN ему не нужен), base — адрес реестра,
// пустой значит Base.
func New(hc *http.Client, base string) *Client {
	if base == "" {
		base = Base
	}
	return &Client{hc: hc, base: base, ua: "kino-platform (+https://github.com/niazlv/my_kino_platform)"}
}

type apiAnime struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Russian string `json:"russian"`
	Kind    string `json:"kind"`
	Year    int    `json:"year"`
	AiredOn string `json:"aired_on"`
}

// Search ищет по строке и отдаёт кандидатов в терминах платформы: вид
// приведён к movie/series, номер реестра — это и есть номер MAL.
func (c *Client) Search(ctx context.Context, q string) ([]appcatalog.NameCandidate, error) {
	u := c.base + "/api/animes?limit=20&search=" + url.QueryEscape(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("shikimori: код %d", resp.StatusCode)
	}
	var raw []apiAnime
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("shikimori: %w", err)
	}
	out := make([]appcatalog.NameCandidate, 0, len(raw))
	for _, a := range raw {
		kind := kindOf(a.Kind)
		if kind == "" {
			continue // музыкальные клипы и прочее не клеится с витриной
		}
		year := a.Year
		if year == 0 {
			year = yearOf(a.AiredOn)
		}
		var names []string
		for _, n := range []string{a.Name, a.Russian} {
			if strings.TrimSpace(n) != "" {
				names = append(names, n)
			}
		}
		out = append(out, appcatalog.NameCandidate{
			ID: strconv.Itoa(a.ID), Names: names, Year: year, Kind: kind,
		})
	}
	return out, nil
}

// kindOf приводит вид реестра к виду витрины. Пусто — вид не подходит:
// у реестра их больше (музыка), у витрины — только фильм и сериал.
func kindOf(k string) string {
	switch k {
	case "movie":
		return "movie"
	case "tv", "ova", "ona", "special":
		return "series"
	}
	return ""
}

// yearOf — год из даты выхода «2015-10-03». Пустое или кривое — ноль:
// кандидат без года решающими правилами отбрасывается сам.
func yearOf(date string) int {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(date))
	if err != nil {
		return 0
	}
	return t.Year()
}
