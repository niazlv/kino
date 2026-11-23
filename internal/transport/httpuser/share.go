package httpuser

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dplayback "github.com/niazleushkin/kino/internal/domain/playback"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	dshare "github.com/niazleushkin/kino/internal/domain/share"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

type shareReq struct {
	// Kind: watch — посмотреть без входа; download — манифест для загрузчика.
	Kind      string   `json:"kind"`
	Hours     int      `json:"hours"`
	MaxHeight int      `json:"maxHeight"`
	Audios    []string `json:"audios"`
	Subtitles []string `json:"subtitles"`
}

type shareResp struct {
	Token     string    `json:"token"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	Command   string    `json:"command,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// handleCreateShare выдаёт ссылку-разрешение.
//
// Она открывается БЕЗ входа, поэтому у неё есть срок, отзыв и учёт
// обращений. Обычная ссылка «для своих» здесь не нужна — это просто адрес
// страницы, и делать ради него запрос к серверу незачем.
func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	ep, err := episodeID(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	var in shareReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	kind := dshare.Kind(in.Kind)
	if kind == "" {
		kind = dshare.KindWatch
	}
	link, err := s.share.Create(r.Context(), actorFrom(r.Context()), ep, kind,
		dshare.Selection{
			MaxHeight: in.MaxHeight, Audios: in.Audios, Subtitles: in.Subtitles,
		},
		time.Duration(in.Hours)*time.Hour)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}

	base := s.publicBase(r)
	out := shareResp{
		Token: link.Token, Kind: string(link.Kind), ExpiresAt: link.ExpiresAt,
	}
	if kind == dshare.KindDownload {
		out.URL = base + "/d/" + link.Token
		// Готовая команда: смысл кнопки в том, чтобы не лезть в утилиту
		// руками и не вспоминать флаги.
		out.Command = "kinopub --from " + out.URL
	} else {
		out.URL = base + "/s/" + link.Token
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	links, err := s.share.ListMine(r.Context(), actorFrom(r.Context()))
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	base := s.publicBase(r)
	out := make([]shareResp, 0, len(links))
	for _, l := range links {
		path := "/s/"
		if l.Kind == dshare.KindDownload {
			path = "/d/"
		}
		out = append(out, shareResp{
			Token: l.Token, Kind: string(l.Kind),
			URL: base + path + l.Token, ExpiresAt: l.ExpiresAt,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleRevokeShare(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := s.share.Revoke(r.Context(), actorFrom(r.Context()), token); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Без входа: сам токен и есть право доступа ──────────────────────────────

type manifest struct {
	V          int          `json:"v"`
	Title      string       `json:"title"`
	Season     int          `json:"season"`
	Episode    int          `json:"episode"`
	Filename   string       `json:"filename"`
	Protocol   string       `json:"protocol"`
	URL        string       `json:"url"`
	ExpiresAt  time.Time    `json:"expiresAt"`
	Audios     []track      `json:"audios"`
	Subtitles  []track      `json:"subtitles"`
	Renditions []rendition  `json:"renditions"`
	Select     selectionDTO `json:"select"`
	// Source — площадка, выдавшая поток. Только в манифесте по сессии: там
	// её выбирают (?source=, ?avoid=), и утилите надо знать, какую обходить
	// при повторе.
	Source *sourceDTO `json:"source,omitempty"`
}

type rendition struct {
	Height int    `json:"height"`
	Codec  string `json:"codec"`
}

type selectionDTO struct {
	MaxHeight int      `json:"maxHeight,omitempty"`
	Audios    []string `json:"audios"`
	Subtitles []string `json:"subtitles"`
}

// handleManifest отдаёт загрузчику всё, что нужно для скачивания.
//
// Без входа и без авторизации площадки: платформа уже сделала резолв, и все
// адреса ведут через её шлюз. Утилите остаётся только качать.
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	res, err := s.share.Resolve(r.Context(), chi.URLParam(r, "token"), dp.ClientProfile{
		SupportsHLS: true,
	})
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if res.Link.Kind != dshare.KindDownload {
		httpx.Fail(w, r, s.log, kernel.ErrForbidden)
		return
	}

	ep, err := s.catalog.Episode(r.Context(), res.Link.EpisodeID)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	t, err := s.catalog.Title(r.Context(), ep.TitleID)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}

	httpx.JSON(w, http.StatusOK,
		s.manifestFor(r, string(res.Ticket.ID), res.Ticket.ExpiresAt, res.Source, ep, t, res.Link.Selection))
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// manifestFor собирает манифест скачивания. Он один и тот же для ссылки-
// разрешения и для запроса по сессии: загрузчику всё равно, откуда пришло
// право, ему нужны адрес потока и состав дорожек.
func (s *Server) manifestFor(r *http.Request, ticketID string, expiresAt time.Time, src dp.Source,
	ep catalog.Episode, ti catalog.Title, sel dshare.Selection) manifest {
	// Протокол — как у источника: MP4 одним файлом (Filmix, Sibnet,
	// площадки-скрипты со своими файлами) качается как файл, через шлюз с
	// докачкой по Range, а не как плейлист, которого у него нет.
	protocol, u := "HLS", s.streamURL(r, ticketID)
	if strings.EqualFold(src.Protocol, string(dplayback.ProtocolProgressive)) {
		protocol, u = string(dplayback.ProtocolProgressive), strings.TrimSuffix(u, "/master.m3u8")+"/file"
	}
	return manifest{
		V: 1, Title: ti.Title, Season: ep.SeasonNo, Episode: ep.EpisodeNo,
		Filename:   fileNameFor(ti.Title, ep.SeasonNo, ep.EpisodeNo),
		Protocol:   protocol,
		URL:        u,
		ExpiresAt:  expiresAt,
		Audios:     downloadAudios(src),
		Subtitles:  streamTextTracks(src.Subtitles),
		Renditions: toRends(src.Renditions),
		Select: selectionDTO{
			MaxHeight: sel.MaxHeight,
			// Пустые списки означают «всё»: по умолчанию человек хочет
			// полный файл, а сужение — осознанное действие.
			Audios:    orEmpty(sel.Audios),
			Subtitles: orEmpty(sel.Subtitles),
		},
	}
}

// publicBase — адрес, каким платформу видит человек снаружи.
//
// Берётся из заголовков обратного прокси: ссылка уедет в переписку или
// в терминал, и localhost в ней бесполезен. Настройка перекрывает
// заголовки, если платформа стоит за чужим доменом.
func (s *Server) publicBase(r *http.Request) string {
	if s.publicURL != "" {
		return s.publicURL
	}
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

// fileNameFor предлагает имя файла. Только предложение: загрузчик вправе
// раскладывать по своей схеме.
func fileNameFor(title string, season, episode int) string {
	base := latinize(title)
	if season > 0 {
		return fmt.Sprintf("%s.S%02dE%02d", base, season, episode)
	}
	return base
}

// latinize оставляет от названия то, что безопасно в имени файла на любой
// системе. Кириллица сохраняется — она допустима везде, где мы работаем,
// а транслитерация сделала бы имена нечитаемыми.
func latinize(s string) string {
	var b strings.Builder
	prevDot := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevDot = false
		case !prevDot && b.Len() > 0:
			b.WriteByte('.')
			prevDot = true
		}
	}
	out := strings.Trim(b.String(), ".")
	// Режем по символам, а не по байтам: срез по байтам на кириллице
	// обрубает вдвое раньше и может разрубить UTF-8 посреди символа.
	if r := []rune(out); len(r) > 80 {
		out = strings.Trim(string(r[:80]), ".")
	}
	if out == "" {
		return "episode"
	}
	return out
}

// handleSharedWatch отдаёт гостю адрес для просмотра.
//
// Гость может быть вообще не зарегистрирован, поэтому тикет выпускается от
// имени поделившегося — смотреть он будет по его подписке. Прогресс при
// этом не пишется никому: гость не должен двигать чужие позиции.
func (s *Server) handleSharedWatch(w http.ResponseWriter, r *http.Request) {
	res, err := s.share.Resolve(r.Context(), chi.URLParam(r, "token"), dp.ClientProfile{
		SupportsHLS: true,
	})
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if res.Link.Kind != dshare.KindWatch {
		httpx.Fail(w, r, s.log, kernel.ErrForbidden)
		return
	}
	ep, err := s.catalog.Episode(r.Context(), res.Link.EpisodeID)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	t, err := s.catalog.Title(r.Context(), ep.TitleID)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"title":     t.Title,
		"season":    ep.SeasonNo,
		"episode":   ep.EpisodeNo,
		"url":       s.streamURL(r, string(res.Ticket.ID)),
		"expiresAt": res.Ticket.ExpiresAt,
		"audios":    toTracks(res.Source.Audios),
		"subtitles": streamTextTracks(res.Source.Subtitles),
	})
}

// streamURL собирает АБСОЛЮТНЫЙ адрес потока.
//
// Плееру на странице мы отдаём относительный адрес намеренно: один источник
// и нативный HLS в iOS. Но манифест и ссылку на просмотр читает внешняя
// программа, у которой никакого базового адреса нет — относительный путь там
// просто не откроется. Поэтому здесь адрес всегда полный.
func (s *Server) streamURL(r *http.Request, id string) string {
	path := "/stream/" + id + "/master.m3u8"
	if strings.HasPrefix(s.gatewayBase, "http://") || strings.HasPrefix(s.gatewayBase, "https://") {
		return s.gatewayBase + path
	}
	return s.publicBase(r) + strings.TrimPrefix(s.gatewayBase, "/") + path
}
