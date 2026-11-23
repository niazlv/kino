package httpuser

import (
	"net/http"
	"strconv"

	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/app/playback"
	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dplayback "github.com/niazleushkin/kino/internal/domain/playback"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

type playbackReq struct {
	// Профиль клиента идёт от клиента: выбор кодека и качества делает
	// провайдер, а не прибитая к одной площадке логика на сервере.
	Codecs      []string `json:"codecs"`
	MaxHeight   int      `json:"maxHeight"`
	SupportsHLS *bool    `json:"supportsHls"`
	BandwidthKB int      `json:"bandwidthKbps"`
	External    bool     `json:"external"`

	// Source — площадка: запомненная для тайтла или выбранная в плеере.
	// Strict — выбрана только что руками: не подменять другой площадкой.
	Source string `json:"source"`
	Strict bool   `json:"strict"`
	// Variant — поток той же площадки (озвучка у аниме-площадок) из
	// прошлого ответа; VariantLabel — предпочтение по подписи студии.
	Variant      string `json:"variant"`
	VariantLabel string `json:"variantLabel"`
	// Voice — студия из общего списка озвучек (ключ): играть её с любой
	// площадки, где она есть. Source тогда — лишь предпочтение.
	Voice string `json:"voice"`
	// Avoid — площадки, чей поток на этом устройстве только что тормозил:
	// их — в конец очереди (см. playback.Request.Avoid).
	Avoid []string `json:"avoid"`
}

type playbackResp struct {
	TicketID  string  `json:"ticketId"`
	URL       string  `json:"url"`
	Protocol  string  `json:"protocol"`
	ExpiresAt string  `json:"expiresAt"`
	Audios    []track `json:"audios"`
	Subtitles []track `json:"subtitles"`
	// Renditions — качества, которые предлагает источник. Тикет уже
	// описывает, что источник даёт (дорожки, субтитры); высоты — факт того
	// же рода. Отдельная ручка ради них означала бы второй поход на
	// площадку за уже полученными данными.
	Renditions []rendition `json:"renditions"`

	// Source — площадка, выдавшая поток; Sources — все, у кого есть эта
	// серия. Из них плеер строит переключатель площадок.
	Source  sourceDTO   `json:"source"`
	Sources []sourceDTO `json:"sources"`
	// Passed — площадки, стоявшие в очереди раньше выдавшей, и почему поток
	// не с них: down — лежит по свежей памяти, failed — не ответила,
	// busy — просит притормозить, missing — нет этой серии, novoice —
	// серия есть, но без запомненной студии, а у выдавшей она есть,
	// slow — её поток только что тормозил на этом устройстве (avoid),
	// reauth — отвергла наш вход и ждёт переавторизации администратором.
	Passed []passDTO `json:"passed,omitempty"`
	// Variants — другие потоки этой серии у той же площадки: у аниме-площадок
	// так устроены озвучки. Variant — выданный.
	Variants []variantDTO `json:"variants"`
	Variant  string       `json:"variant,omitempty"`
	// Segments — разметка пропуска, пришедшая вместе с потоком. Остальную
	// плеер спрашивает отдельно (/episodes/{id}/skips): её первый кадр не
	// ждёт.
	Segments []segmentDTO `json:"segments"`
}

type sourceDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type passDTO struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type variantDTO struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Player string `json:"player,omitempty"`
}

type track struct {
	ID       string `json:"id"`
	Language string `json:"language"`
	Title    string `json:"title"`
	// URL — только у субтитров отдельным файлом: адрес на шлюзе. Плеер
	// добавляет их к дорожкам потока сам; в плейлисте их нет.
	URL string `json:"url,omitempty"`
	// Default — включить сразу: вариант «с субтитрами».
	Default bool `json:"default,omitempty"`
}

// handlePlayback выпускает тикет и отдаёт ссылку НА ШЛЮЗ.
//
// Адрес площадки клиенту не показывается вообще: он живёт в тикете, который
// читает только шлюз.
func (s *Server) handlePlayback(w http.ResponseWriter, r *http.Request) {
	ep, err := episodeID(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	var in playbackReq
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			httpx.Fail(w, r, s.log, err)
			return
		}
	}
	a := actorFrom(r.Context())
	// Пользователь смотрит только от своего имени: админская сессия сюда
	// не пройдёт, чужой user_id подставить нельзя — он берётся из сессии.
	if err := (auth.Policy{}).CanIssueTicket(a, a.UserID); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}

	hls := true
	if in.SupportsHLS != nil {
		hls = *in.SupportsHLS
	}
	res, err := s.playback.Issue(r.Context(), playback.Request{
		User: a.UserID, Episode: ep,
		Client: dp.ClientProfile{
			Codecs:          in.Codecs,
			MaxHeight:       in.MaxHeight,
			SupportsHLS:     hls,
			BandwidthHintKB: in.BandwidthKB,
		},
		Choice:                    catalog.Choice{Source: in.Source, Strict: in.Strict},
		Variant:                   in.Variant,
		VariantLabel:              in.VariantLabel,
		TrackProgressFromSegments: in.External,
		Voice:                     in.Voice,
		Avoid:                     in.Avoid,
	})
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	t, src := res.Ticket, res.Source

	// Сессия открывается фактом выдачи тикета: иначе просмотр, прерванный
	// до первого отчёта плеера (закрыли страницу, упал браузер), не оставляет
	// следа вовсе, и все записи сессий получают нулевую длительность.
	// Отказ телеметрии билет не ломает — это не часть Playback.
	if err := s.telemetry.Begin(r.Context(), dplayback.Session{
		ID:        kernel.SessionID(t.ID),
		UserID:    a.UserID,
		EpisodeID: ep,
		Provider:  res.Offer.Ref.ProviderID,
	}); err != nil {
		s.log.Warn("сессия телеметрии не открылась", "ticket", string(t.ID), "err", err)
	}

	sources := make([]sourceDTO, 0, len(res.Offers))
	seen := map[string]bool{}
	for _, o := range res.Offers {
		// Две страницы одной площадки на одну серию (так бывает у сезонов,
		// склеенных вручную) — для человека это всё равно одна площадка.
		if seen[o.Ref.ProviderID] {
			continue
		}
		seen[o.Ref.ProviderID] = true
		sources = append(sources, sourceDTO{ID: o.Ref.ProviderID, Name: s.providerName(o.Ref.ProviderID)})
	}
	var passed []passDTO
	for _, p := range res.Passed {
		passed = append(passed, passDTO{ID: p.Provider, Name: s.providerName(p.Provider), Reason: string(p.Reason)})
	}
	variants := make([]variantDTO, 0, len(src.Variants))
	for _, v := range src.Variants {
		variants = append(variants, variantDTO{ID: v.ID, Title: v.Title, Player: v.Player})
	}

	httpx.JSON(w, http.StatusOK, playbackResp{
		TicketID:   string(t.ID),
		URL:        s.gatewayBase + "/stream/" + string(t.ID) + "/master.m3u8",
		Protocol:   string(t.Upstream.Protocol),
		ExpiresAt:  t.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		Audios:     toTracks(src.Audios),
		Subtitles:  s.playerTextTracks(src, string(t.ID)),
		Renditions: toRends(src.Renditions),
		Source: sourceDTO{ID: res.Offer.Ref.ProviderID,
			Name: s.providerName(res.Offer.Ref.ProviderID)},
		Sources:  sources,
		Passed:   passed,
		Variants: variants,
		Variant:  src.Variant,
		Segments: toSegments(src.Segments, res.Offer.Ref.ProviderID),
	})
}

func toTracks(in []dp.AudioTrack) []track {
	out := make([]track, 0, len(in))
	for _, a := range in {
		out = append(out, track{ID: a.ID, Language: a.Language, Title: a.Title})
	}
	return out
}

// streamTextTracks — субтитры самого потока. Утилите и внешнему плееру
// нужны только они: те берут дорожки из плейлиста, отдельных файлов там нет.
func streamTextTracks(in []dp.TextTrack) []track {
	out := make([]track, 0, len(in))
	for _, x := range in {
		if x.Sidecar {
			continue
		}
		out = append(out, track{ID: x.ID, Language: x.Language, Title: x.Title})
	}
	return out
}

// playerTextTracks — субтитры для плеера витрины: дорожки потока и файлы
// отдельно от него, эти — с адресом на шлюзе. Номер в адресе — порядок
// в Sidecars, в том же порядке ссылки лежат в тикете.
func (s *Server) playerTextTracks(src dp.Source, ticketID string) []track {
	out := streamTextTracks(src.Subtitles)
	for i, x := range src.Sidecars() {
		out = append(out, track{
			ID: x.ID, Language: x.Language, Title: x.Title, Default: x.Default,
			URL: s.gatewayBase + "/stream/" + ticketID + "/sub/" + strconv.Itoa(i) + ".vtt",
		})
	}
	return out
}

func toRends(in []dp.Rendition) []rendition {
	out := make([]rendition, 0, len(in))
	for _, r := range in {
		out = append(out, rendition{Height: r.Height, Codec: r.Codec})
	}
	return out
}

type voicesResp struct {
	Voices []voiceDTO `json:"voices"`
	// Silent — площадки серии, которые о своих озвучках не рассказали:
	// список может быть неполным.
	Silent []passDTO `json:"silent,omitempty"`
}

type voiceDTO struct {
	// Key — студия одним написанием: её присылают обратно в /playback.
	Key   string        `json:"key"`
	Title string        `json:"title"`
	Ways  []voiceWayDTO `json:"ways"`
}

// voiceWayDTO — где озвучка есть. Variant пуст — дорожка общего потока:
// её переключает сам плеер, без нового тикета.
type voiceWayDTO struct {
	Source  string `json:"source"`
	Variant string `json:"variant,omitempty"`
	Player  string `json:"player,omitempty"`
}

// handleVoices — озвучки серии со всех её площадок одним списком.
//
// Отдельно от /playback: поток не должен ждать, пока ответят все площадки.
// Плеер просит список, когда серия уже играет.
func (s *Server) handleVoices(w http.ResponseWriter, r *http.Request) {
	ep, err := episodeID(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	l, err := s.playback.Voices(r.Context(), ep)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	out := voicesResp{Voices: make([]voiceDTO, 0, len(l.Voices))}
	for _, v := range l.Voices {
		d := voiceDTO{Key: v.Key, Title: v.Title, Ways: make([]voiceWayDTO, 0, len(v.Ways))}
		for _, w := range v.Ways {
			d.Ways = append(d.Ways, voiceWayDTO{Source: w.Offer.Ref.ProviderID, Variant: w.Variant, Player: w.Player})
		}
		out.Voices = append(out.Voices, d)
	}
	for _, p := range l.Silent {
		out.Silent = append(out.Silent, passDTO{ID: p.Provider, Name: s.providerName(p.Provider), Reason: string(p.Reason)})
	}
	httpx.JSON(w, http.StatusOK, out)
}
