package httpuser

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/app/playback"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	dshare "github.com/niazleushkin/kino/internal/domain/share"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// handleDirectDownload — «просто скачать», без утилиты и без ссылки.
//
// Площадка рядом с HLS отдаёт прогрессивный файл, поэтому тикет выпускается
// с SupportsHLS=false: резолв тогда выбирает именно его. Дальше браузер
// уходит на шлюз и сохраняет файл сам — сборка из сегментов не нужна.
//
// Ограничение честное и неустранимое здесь: в таком файле одна дорожка и
// одно качество, те, что запечены площадкой. Выбор дорожек и субтитров
// требует пересборки контейнера, а это работа для утилиты, не для страницы.
func (s *Server) handleDirectDownload(w http.ResponseWriter, r *http.Request) {
	ep, err := episodeID(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	a := actorFrom(r.Context())
	if err := (auth.Policy{}).CanIssueTicket(a, a.UserID); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}

	maxH, _ := strconv.Atoi(r.URL.Query().Get("maxHeight"))
	t, _, err := s.playback.IssueTicket(r.Context(), a.UserID, ep, dp.ClientProfile{
		MaxHeight:   maxH,
		SupportsHLS: false,
	})
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}

	name := "video"
	if e, err := s.catalog.Episode(r.Context(), ep); err == nil {
		if ti, err := s.catalog.Title(r.Context(), e.TitleID); err == nil {
			name = fileNameFor(ti.Title, e.SeasonNo, e.EpisodeNo)
		}
	}

	// Уводим браузер на шлюз: качать через core значило бы гонять весь
	// файл лишним звеном, а шлюз для этого и сделан.
	http.Redirect(w, r,
		s.gatewayBase+"/stream/"+string(t.ID)+"/file?name="+url.QueryEscape(name),
		http.StatusFound)
}

// handleEpisodeManifest — манифест скачивания по сессии, без ссылки-разрешения.
//
// Путь загрузчика для обычной ссылки со страницы: человек копирует адрес из
// браузера, утилита находит серии через /titles/{id} и просит манифест на
// каждую. Право здесь — сама сессия, поэтому срок, отзыв и учёт обращений,
// как у /d/{token}, не нужны: всё это уже есть у сессии.
//
// Состав манифеста тот же, что по ссылке-разрешению; выбор пустой — что
// именно брать, человек говорит флагами утилиты, а не заранее на странице.
//
// Площадку и озвучку выбирают так же, как для плеера (?voice=, ?source=,
// ?avoid=): у kinopub все студии — дорожки одного потока, а у остальных
// площадок каждая студия — свой поток, и без выбора в файл попадала та, что
// стоит первой, а не та, что человек слушает.
func (s *Server) handleEpisodeManifest(w http.ResponseWriter, r *http.Request) {
	ep, err := episodeID(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	a := actorFrom(r.Context())
	if err := (auth.Policy{}).CanIssueTicket(a, a.UserID); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}

	// Каталог раньше тикета: за несуществующей серией незачем ходить на
	// площадку.
	e, err := s.catalog.Episode(r.Context(), ep)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	ti, err := s.catalog.Title(r.Context(), e.TitleID)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	req := manifestRequest(a.UserID, ep, r.URL.Query())
	res, err := s.playback.Issue(r.Context(), req)
	if errors.Is(err, playback.ErrNoVoice) {
		// Студии у этой серии нет ни у одной площадки (вышла позже, у неё
		// пропуск). Плеер тут отказывает, а скачивание сезона берёт серию в
		// той озвучке, что есть: дыра посреди сезона хуже другой студии в
		// одной серии. Какая досталась — манифест называет в audios.
		req.Voice = ""
		res, err = s.playback.Issue(r.Context(), req)
	}
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	m := s.manifestFor(r, string(res.Ticket.ID), res.Ticket.ExpiresAt, res.Source, e, ti, dshare.Selection{})
	m.Source = &sourceDTO{ID: res.Offer.Ref.ProviderID, Name: s.providerName(res.Offer.Ref.ProviderID)}
	httpx.JSON(w, http.StatusOK, m)
}

// manifestRequest — выбор, с которым утилита просит манифест: площадка,
// студия из общего списка озвучек (ключ из /episodes/{id}/voices) и
// площадки, которые обойти, — как в теле /playback, только строкой запроса:
// утилита ходит GET. Площадка — предпочтение, а не строгий выбор: серии,
// которой у неё нет, лучше прийти с другой, чем не скачаться.
func manifestRequest(user kernel.UserID, ep kernel.EpisodeID, q url.Values) playback.Request {
	req := playback.Request{
		User: user, Episode: ep,
		Client: dp.ClientProfile{SupportsHLS: true},
		Choice: catalog.Choice{Source: strings.TrimSpace(q.Get("source"))},
		Voice:  strings.TrimSpace(q.Get("voice")),
	}
	for _, v := range q["avoid"] {
		for _, id := range strings.Split(v, ",") {
			if id = strings.TrimSpace(id); id != "" {
				req.Avoid = append(req.Avoid, id)
			}
		}
	}
	return req
}

// downloadAudios — дорожки озвучки для утилиты.
//
// У kinopub студии — дорожки одного потока, и плейлист называет их сам. У
// площадок, где студия — отдельный поток (Variant), звук вшит в видео
// одной безымянной дорожкой, и без подписи отсюда в файле оказывалась
// «eng» без названия. Её называет выданный вариант.
func downloadAudios(src dp.Source) []track {
	if len(src.Audios) > 0 || src.Variant == "" {
		return toTracks(src.Audios)
	}
	for _, v := range src.Variants {
		if v.ID == src.Variant && strings.TrimSpace(v.Title) != "" {
			return []track{{ID: v.ID, Language: variantLanguage(v.Title), Title: dp.VoiceTitle(v.Title)}}
		}
	}
	return []track{}
}

// variantLanguage — язык вшитой дорожки по подписи варианта. Площадки с
// вариантами — русские, и студия на них — русская озвучка. Оригинал и
// вариант «с субтитрами» — звук исходника: его язык неизвестен, и лучше не
// сказать ничего, чем назвать японский русским.
func variantLanguage(title string) string {
	if dp.Subtitled(title) || dp.VoiceKey(title) == "оригинал" {
		return ""
	}
	return "rus"
}
