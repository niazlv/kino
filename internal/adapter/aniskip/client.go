// Package aniskip — разметка опенингов и эндингов аниме из AniSkip.
//
// AniSkip (api.aniskip.com) — общая база разметки, которую ведут зрители
// аниме-плееров (ani-cli, mpv, Jellyfin): «SponsorBlock для аниме». Ключ —
// номер MyAnimeList и номер серии в его нумерации. Размечают зрители
// оригинала, поэтому у каждой записи указана длина того файла: дубляж
// бывает сдвинут на секунды, и чужая разметка годится, только если длина
// совпала (решает плеер — длину потока знает он).
package aniskip

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// DefaultBase — адрес API. Открывается напрямую, без VPN (сверено
// 03.10.2026, ответ за 0,2 с).
const DefaultBase = "https://api.aniskip.com"

// Client — запросы к AniSkip.
type Client struct {
	hc   *http.Client
	base string
}

// New: base пусто — DefaultBase.
func New(hc *http.Client, base string) *Client {
	if base == "" {
		base = DefaultBase
	}
	return &Client{hc: hc, base: strings.TrimRight(base, "/")}
}

// Segments — разметка серии. Не найдено — kernel.ErrNotFound.
//
// Спрашиваются опенинг, эндинг и пересказ. «Смешанные» (mixed-op,
// mixed-ed — песня поверх сюжета) не берутся: пропустив их, человек
// пропустит и кусок серии.
func (c *Client) Segments(ctx context.Context, mal string, episode int) ([]dp.Segment, error) {
	if _, err := strconv.Atoi(mal); err != nil || episode < 1 {
		return nil, fmt.Errorf("aniskip: MAL %q серия %d: %w", mal, episode, kernel.ErrInvalidInput)
	}
	q := url.Values{"types": {"op", "ed", "recap"}, "episodeLength": {"0"}}
	u := fmt.Sprintf("%s/v2/skip-times/%s/%d?%s", c.base, mal, episode, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aniskip: %w: %w", kernel.ErrUnreachable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("aniskip: %w: %w", kernel.ErrUnreachable, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("aniskip: MAL %s серия %d: %w", mal, episode, kernel.ErrNotFound)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("aniskip: %w", kernel.ErrBusy)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("aniskip: HTTP %d: %w", resp.StatusCode, kernel.ErrUnavailable)
	}
	return Parse(body)
}

// Parse разбирает ответ /v2/skip-times.
func Parse(body []byte) ([]dp.Segment, error) {
	var raw struct {
		Found   bool `json:"found"`
		Results []struct {
			Interval struct {
				StartTime float64 `json:"startTime"`
				EndTime   float64 `json:"endTime"`
			} `json:"interval"`
			SkipType      string  `json:"skipType"`
			EpisodeLength float64 `json:"episodeLength"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("aniskip: ответ не разобран: %w", kernel.ErrUnavailable)
	}
	var out []dp.Segment
	for _, r := range raw.Results {
		kind, ok := kinds[r.SkipType]
		if !ok || r.EpisodeLength <= 0 {
			continue
		}
		s := dp.Segment{
			Kind:          kind,
			StartMS:       ms(r.Interval.StartTime),
			EndMS:         ms(r.Interval.EndTime),
			ForDurationMS: ms(r.EpisodeLength),
		}
		if s.Valid() && s.EndMS <= s.ForDurationMS+1000 {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("aniskip: разметки нет: %w", kernel.ErrNotFound)
	}
	return out, nil
}

var kinds = map[string]dp.SegmentKind{
	"op":    dp.SegIntro,
	"ed":    dp.SegOutro,
	"recap": dp.SegRecap,
}

func ms(sec float64) int64 { return int64(sec*1000 + 0.5) }

// Timeout — сколько ждать AniSkip. Разметку ждёт не первый кадр, а кнопка
// через секунды после него, но висеть дольше незачем.
const Timeout = 5 * time.Second
