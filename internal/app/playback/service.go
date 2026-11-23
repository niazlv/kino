// Package playback — use cases воспроизведения: выпуск тикета, обновление
// протухшего, запись прогресса. Не знает ни про HTTP, ни про SQL, ни про
// конкретных провайдеров — только про домен и свои порты.
package playback

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/niazleushkin/kino/internal/domain/catalog"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/domain/provider"
)

type Service struct {
	titles   TitleRepo
	tickets  TicketStore
	resolver Resolver
	sessions SessionSink
	clock    Clock
	ids      IDGen
	health   Health

	// Общий список озвучек серии (WithVoices) и его память.
	voices     VoiceLister
	voiceMu    sync.Mutex
	voiceCache map[kernel.EpisodeID]cachedVoices
	spellings  SpellingSink

	// Сроки ожидания площадок — полями, чтобы тест не ждал настоящие секунды.
	attempt, lastAttempt, budget, probe time.Duration
}

// Конструктор принимает интерфейсы. DI живёт в internal/bootstrap и не
// протекает сюда: сервис ничего не знает про контейнер, конфиг и порядок
// инициализации — поэтому тестируется без единого мока инфраструктуры.
func NewService(
	titles TitleRepo, tickets TicketStore,
	resolver Resolver, sessions SessionSink, clock Clock, ids IDGen,
) *Service {
	return &Service{titles: titles, tickets: tickets, resolver: resolver,
		sessions: sessions, clock: clock, ids: ids,
		attempt: attemptTimeout, lastAttempt: lastAttemptTimeout,
		budget: issueBudget, probe: probeTimeout}
}

// WithHealth включает память о лежащих площадках: без неё каждый выпуск
// тикета заново тратит попытку на площадку, которая не ответила минуту назад.
func (s *Service) WithHealth(h Health) *Service {
	s.health = h
	return s
}

// ticketTTLSlack — сколько тикет лежит в хранилище после срока ссылок.
//
// Протухший тикет шлюз обновляет сам, под тем же id, — но только пока
// тикет есть. С запасом в две минуты серия, оставленная на паузе дольше
// срока ссылок (у Alloha это три часа, у AniDub — меньше двух), после
// «играть» получала 404 на каждый сегмент: плеер доигрывал буфер и вставал.
// Шесть часов покрывают вечерний перерыв; дальше тикет просит сама витрина.
const ticketTTLSlack = 6 * time.Hour

// Сколько ждать площадку.
//
// Живая отвечает за 0,3–2 с (замер 26.09.2026: kinopub через VPN около
// 0,5 с, AnimeGO и ЛордФильм с холодного кеша до 1,8 с). Восемь секунд с
// запасом отделяют медленную площадку от лежащей, а человек за это время
// ещё не решил, что всё сломалось. Без срока площадка за мёртвым маршрутом
// держала бы плеер до таймаута HTTP-клиента — минуты, — и запасная
// включалась бы слишком поздно.
const (
	// attemptTimeout — площадке, за которой в очереди есть другие.
	attemptTimeout = 8 * time.Second
	// lastAttemptTimeout — последней или единственной: ждать больше некого,
	// но и бесконечно тоже нельзя.
	lastAttemptTimeout = 15 * time.Second
	// issueBudget — потолок на весь выбор. Шлюз ждёт обновления тикета 30 с,
	// выбор обязан уложиться раньше.
	issueBudget = 25 * time.Second
	// probeTimeout — фоновой перепроверке лежащей площадки. Её никто не
	// ждёт, но висеть вечно ей тоже незачем.
	probeTimeout = 15 * time.Second
)

// Request — что выпускать.
type Request struct {
	User    kernel.UserID
	Episode kernel.EpisodeID
	Client  provider.ClientProfile
	// Choice — площадка: выбранная только что (Strict) или запомненная.
	Choice catalog.Choice
	// Variant — поток той же площадки (озвучка у аниме-площадок); пусто —
	// по VariantLabel или по умолчанию.
	Variant                   string
	VariantLabel              string
	TrackProgressFromSegments bool
	// Voice — студия из общего списка озвучек (VoiceOption.Key): играть её,
	// откуда угодно. Площадка в Choice тогда — лишь предпочтение. Студии
	// нет нигде — отказ, а не другая озвучка: человек выбрал именно её.
	Voice string
	// Avoid — площадки, поток которых у этого человека только что
	// тормозил: идут в конец очереди, как лежащие, но не выбрасываются —
	// студия может быть только у них. Мнение одного устройства, а не
	// площадки вообще: у соседа по семье тот же kinopub может идти ровно,
	// поэтому в общую память о лежащих оно не попадает. Строгий выбор
	// руками сильнее: человек нажал именно эту площадку.
	Avoid []string
}

// Issued — выпущенный тикет и всё, что о нём нужно вызывающему.
type Issued struct {
	Ticket playback.Ticket
	// Source — ответ площадки: дорожки, качества, варианты.
	Source provider.Source
	// Offer — какая площадка выдала поток.
	Offer catalog.Offer
	// Offers — все площадки, у которых эта серия есть: переключатель в плеере.
	Offers []catalog.Offer
	// Passed — площадки, стоявшие в очереди раньше выдавшей, и почему поток
	// не с них: человеку надо объяснить, отчего играет не та площадка и не
	// та озвучка.
	Passed []Pass
}

// Pass — площадка, которую обошли.
type Pass struct {
	Provider string
	Reason   PassReason
}

// PassReason — почему площадку обошли.
type PassReason string

const (
	// PassDown — лежит по свежей памяти, даже не спрашивали.
	PassDown PassReason = "down"
	// PassFailed — спросили: не ответила или недоступна.
	PassFailed PassReason = "failed"
	// PassBusy — спросили: просит притормозить.
	PassBusy PassReason = "busy"
	// PassMissing — спросили: этой серии у неё нет.
	PassMissing PassReason = "missing"
	// PassNoVoice — серию отдала, но без запомненной студии: та же студия
	// нашлась у другой площадки.
	PassNoVoice PassReason = "novoice"
	// PassSlow — её поток только что тормозил у этого человека (Avoid),
	// даже не спрашивали.
	PassSlow PassReason = "slow"
	// PassReauth — площадка отвергла наш вход и ждёт переавторизации
	// (kernel.ErrReauth): спросили или помним. Отдельно от failed, чтобы
	// плеер сказал «нужна переавторизация», а не «не отвечает».
	PassReauth PassReason = "reauth"
)

// attempt — одна попытка выбора: площадка, номер её потока и студия.
type attempt struct {
	offer catalog.Offer
	// variant — номер потока площадки (озвучка у аниме-площадок); пусто —
	// по label или по умолчанию.
	variant string
	label   string
}

// IssueTicket резолвит источник у провайдера и выпускает тикет.
//
// Возвращает и источник: вызывающему нужен список дорожек и качеств, а
// припрятать его в сервисе значило бы завести скрытое состояние, живущее
// между запросами. Тикет уходит в хранилище, источник — вызывающему.
func (s *Service) IssueTicket(
	ctx context.Context, user kernel.UserID, ep kernel.EpisodeID, cp provider.ClientProfile,
) (playback.Ticket, provider.Source, error) {
	res, err := s.Issue(ctx, Request{User: user, Episode: ep, Client: cp})
	return res.Ticket, res.Source, err
}

// Issue выбирает площадку и выпускает тикет.
//
// Площадки перебираются по порядку: выбранная человеком, дальше по
// приоритету. Следующая пробуется, только если у предыдущей серии нет или
// площадка недоступна — площадки ДОПОЛНЯЮТ друг друга, и серия, которой нет
// у любимой, всё равно должна играть. Выбор, сделанный руками только что
// (Strict), не подменяется: человек нажал «AnimeGO» и должен узнать, что
// там не вышло, а не получить молча kinopub.
//
// Ни одна площадка не держит человека дольше своего срока, а весь выбор —
// дольше issueBudget. Лежащая по свежей памяти площадка спрашивается
// последней (не выбрасывается: у тайтла других может не быть), и пока
// играет запасная, её перепроверяют в фоне.
func (s *Service) Issue(ctx context.Context, req Request) (Issued, error) {
	episode, err := s.titles.Episode(ctx, req.Episode)
	if err != nil {
		return Issued{}, fmt.Errorf("episode %d: %w", req.Episode, err)
	}
	offers, err := s.titles.Offers(ctx, req.Episode)
	if err != nil {
		return Issued{}, fmt.Errorf("offers %d: %w", req.Episode, err)
	}
	if len(offers) == 0 {
		// Серии, заведённые до учёта площадок по сериям, — на всякий случай:
		// лучшая площадка тайтла в канонической нумерации.
		title, err := s.titles.Title(ctx, episode.TitleID)
		if err != nil {
			return Issued{}, fmt.Errorf("title %d: %w", episode.TitleID, err)
		}
		src, ok := title.BestSource()
		if !ok {
			return Issued{}, fmt.Errorf("no source for title %d: %w", title.ID, kernel.ErrNotFound)
		}
		offers = []catalog.Offer{{Ref: src.Ref, SeasonNo: episode.SeasonNo,
			EpisodeNo: episode.EpisodeNo, Priority: src.Priority}}
	}

	var ranked []attempt
	if req.Voice != "" {
		if ranked, err = s.voiceQueue(ctx, req); err != nil {
			return Issued{}, err
		}
	} else {
		for _, o := range catalog.Rank(offers, req.Choice) {
			a := attempt{offer: o, label: req.VariantLabel}
			// Номер варианта понятен только выдавшей его площадке.
			if o.Ref.ProviderID == req.Choice.Source {
				a.variant = req.Variant
			}
			ranked = append(ranked, a)
		}
	}
	if len(ranked) == 0 {
		return Issued{}, fmt.Errorf("площадка %q не отдаёт серию %d: %w",
			req.Choice.Source, req.Episode, kernel.ErrNotFound)
	}
	queue, demoted := s.healthyFirst(ranked, req.Choice, req.Avoid)

	bctx, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()
	var errs []error
	failed := map[string]PassReason{}
	for i, a := range queue {
		limit := s.lastAttempt
		if i < len(queue)-1 {
			limit = s.attempt
		}
		res, err := s.attemptResolve(bctx, limit, s.resolveRequest(req, a))
		if err == nil {
			s.recovered(a.offer.Ref.ProviderID)
			served := a
			if alt, altRes, ok := s.followVoice(bctx, req, a, res); ok {
				served, res = alt, altRes
			}
			t, err := s.store(ctx, req, episode.ID, served.offer, res)
			if err != nil {
				return Issued{}, err
			}
			s.probeDemoted(ctx, req, queue[i+1:], demoted)
			p := passed(ranked, served.offer, failed, demoted)
			if served.offer.Ref.ProviderID != a.offer.Ref.ProviderID {
				p = append(p, Pass{Provider: a.offer.Ref.ProviderID, Reason: PassNoVoice})
			}
			return Issued{Ticket: t, Source: res, Offer: served.offer, Offers: sortedForUI(offers),
				Passed: p}, nil
		}
		// Человек ушёл (закрыл страницу, переключил серию) — площадка тут ни
		// при чём, и спрашивать следующую уже некому.
		if ctx.Err() != nil {
			return Issued{}, ctx.Err()
		}
		err = fmt.Errorf("resolve %s: %w", a.offer.Ref, err)
		if providerDown(err) && s.health != nil {
			s.health.Failed(a.offer.Ref.ProviderID, err)
		}
		if !fallbackWorthy(err) {
			return Issued{}, err
		}
		errs = append(errs, err)
		if _, seen := failed[a.offer.Ref.ProviderID]; !seen {
			failed[a.offer.Ref.ProviderID] = passReason(err)
		}
		if bctx.Err() != nil {
			break // потолок на весь выбор исчерпан
		}
	}
	return Issued{}, mostTelling(errs)
}

// ErrNoVoice — студии Request.Voice нет в списке озвучек серии ни у одной
// площадки. Отдельно от прочих «не найдено»: плеер на это честно отказывает,
// а скачивание сезона берёт серию в той озвучке, что есть, — дыра посреди
// сезона хуже другой студии в одной серии.
var ErrNoVoice = errors.New("нет такой озвучки")

// voiceQueue — попытки для студии из общего списка: все площадки, где она
// есть, по приоритету, а площадка, с которой человек смотрит, — первой:
// у kinopub смена студии — та же дорожка того же потока, у аниме-площадки —
// её же плеер.
func (s *Service) voiceQueue(ctx context.Context, req Request) ([]attempt, error) {
	list, err := s.Voices(ctx, req.Episode)
	if err != nil {
		return nil, err
	}
	opt, ok := list.Find(req.Voice)
	if !ok {
		return nil, fmt.Errorf("озвучки %q у серии %d нет: %w: %w", req.Voice, req.Episode, ErrNoVoice, kernel.ErrNotFound)
	}
	ways := slices.Clone(opt.Ways)
	sort.SliceStable(ways, func(i, j int) bool {
		return ways[i].Offer.Ref.ProviderID == req.Choice.Source && ways[j].Offer.Ref.ProviderID != req.Choice.Source
	})
	out := make([]attempt, 0, len(ways))
	for _, w := range ways {
		out = append(out, attempt{offer: w.Offer, variant: w.Variant, label: opt.Title})
	}
	return out, nil
}

// followVoice — запомненная студия важнее запомненной площадки.
//
// Площадка отдала серию, но без студии, которую человек слушал в прошлой
// серии (у kinopub нет AniLibria, у AnimeGO.fr — нет этой серии в ней).
// Если та же студия есть у другой площадки, играет она: человек выбирает
// озвучку, а не площадку. Список озвучек при этом ждётся недолго —
// человек ждёт первый кадр, — а не вышло — играет то, что уже выдано.
func (s *Service) followVoice(ctx context.Context, req Request, served attempt,
	res provider.Source) (attempt, provider.Source, bool) {
	if s.voices == nil || req.Voice != "" || req.Choice.Strict || req.Variant != "" || req.VariantLabel == "" ||
		servesVoice(res, req.VariantLabel) {
		return attempt{}, provider.Source{}, false
	}
	list, err := s.voicesWithin(ctx, req.Episode, followWait)
	if err != nil {
		return attempt{}, provider.Source{}, false
	}
	opt, ok := list.Find(req.VariantLabel)
	if !ok {
		return attempt{}, provider.Source{}, false // студии нет нигде — играет выданное
	}
	id := served.offer.Ref.ProviderID
	if res.Variant == "" && slices.ContainsFunc(opt.Ways, func(w VoiceWay) bool { return w.Offer.Ref.ProviderID == id }) {
		// Дорожка общего потока (kinopub): выберет плеер, площадка не нужна другая.
		return attempt{}, provider.Source{}, false
	}
	// Поток уже есть — искать студию дальше можно недолго, и одна площадка
	// не съедает весь срок своими плеерами (у AnimeGO их по два на студию).
	fctx, cancel := context.WithTimeout(ctx, followBudget)
	defer cancel()
	// Тормозившая площадка — последней, но не выброшенной: студия важнее
	// скорости, и у неё одной эта студия может быть.
	ways := slices.Clone(opt.Ways)
	sort.SliceStable(ways, func(i, j int) bool {
		return !slices.Contains(req.Avoid, ways[i].Offer.Ref.ProviderID) &&
			slices.Contains(req.Avoid, ways[j].Offer.Ref.ProviderID)
	})
	tried := map[string]int{}
	for _, w := range ways {
		wid := w.Offer.Ref.ProviderID
		if wid == id || tried[wid] >= 2 || (s.health != nil && s.health.Down(wid)) {
			continue
		}
		tried[wid]++
		a := attempt{offer: w.Offer, variant: w.Variant, label: opt.Title}
		r, err := s.attemptResolve(fctx, s.attempt, s.resolveRequest(req, a))
		if err == nil {
			s.recovered(wid)
			return a, r, true
		}
		if fctx.Err() != nil {
			break
		}
		if providerDown(err) && s.health != nil {
			s.health.Failed(wid, err)
		}
	}
	return attempt{}, provider.Source{}, false
}

// servesVoice — играет ли выданный поток студию label. Не понять — да:
// без уверенности чужую площадку не дёргаем.
func servesVoice(res provider.Source, label string) bool {
	k := provider.VoiceKey(label)
	if res.Variant != "" {
		for _, v := range res.Variants {
			if v.ID == res.Variant {
				return provider.VoiceKey(v.Title) == k
			}
		}
		return true
	}
	if len(res.Audios) == 0 {
		return true
	}
	for _, a := range res.Audios {
		if provider.VoiceKey(a.Title) == k {
			return true
		}
	}
	return false
}

func (s *Service) resolveRequest(req Request, a attempt) provider.ResolveRequest {
	return provider.ResolveRequest{
		Ref: a.offer.Ref, Season: a.offer.SeasonNo, Episode: a.offer.EpisodeNo, Client: req.Client,
		Variant: a.variant, VariantLabel: a.label,
	}
}

// attemptResolve — одна площадка, не дольше limit. Не уложилась — это
// отказ «не отвечает», а не повод ждать дальше.
func (s *Service) attemptResolve(ctx context.Context, limit time.Duration,
	r provider.ResolveRequest) (provider.Source, error) {
	actx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	res, err := s.resolver.Resolve(actx, r)
	switch {
	case err == nil:
	case errors.Is(ctx.Err(), context.Canceled):
		// Отмена сверху — не отказ площадки, пусть едет как есть.
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		err = fmt.Errorf("выбор площадки не уложился в %v: %w: %w", s.budget, kernel.ErrUnreachable, err)
	case errors.Is(actx.Err(), context.DeadlineExceeded):
		err = fmt.Errorf("не ответила за %v: %w: %w", limit, kernel.ErrUnreachable, err)
	case errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, kernel.ErrUnavailable):
		// Собственный таймаут HTTP-клиента площадки — то же «не отвечает».
		err = fmt.Errorf("%w: %w", kernel.ErrUnreachable, err)
	}
	return res, err
}

// healthyFirst отодвигает в конец очереди лежащие площадки, а перед ними —
// тормозившие у этого человека (avoid): медленный поток лучше молчащей
// площадки. Порядок внутри каждой группы прежний. Руками выбранную только
// что не трогает: человек просил именно её. Если лежат все или отодвигать
// нечего — порядок прежний.
func (s *Service) healthyFirst(ranked []attempt, c catalog.Choice, avoid []string) ([]attempt, map[string]PassReason) {
	if c.Strict {
		return ranked, nil
	}
	var up, slow, down []attempt
	demoted := map[string]PassReason{}
	for _, a := range ranked {
		id := a.offer.Ref.ProviderID
		switch {
		case s.health != nil && s.health.Down(id):
			down = append(down, a)
			demoted[id] = s.downReason(id)
		case slices.Contains(avoid, id):
			slow = append(slow, a)
			demoted[id] = PassSlow
		default:
			up = append(up, a)
		}
	}
	if len(up)+len(slow) == 0 || len(slow)+len(down) == 0 {
		return ranked, nil
	}
	return append(append(up, slow...), down...), demoted
}

// downReason — почему лежащую площадку не спрашивали.
func (s *Service) downReason(id string) PassReason {
	if s.health != nil && s.health.NeedsReauth(id) {
		return PassReauth
	}
	return PassDown
}

func (s *Service) recovered(id string) {
	if s.health != nil {
		s.health.Recovered(id)
	}
}

// probeDemoted перепроверяет в фоне лежащие площадки, до которых очередь
// не дошла: иначе вернувшаяся площадка ждала бы конца срока, пока каждый
// получает запасную с чужой озвучкой. Спрашивается та же серия — ответ
// выбрасывается, важен лишь сам факт ответа.
func (s *Service) probeDemoted(ctx context.Context, req Request, rest []attempt, demoted map[string]PassReason) {
	if s.health == nil {
		return
	}
	for _, a := range rest {
		// Тормозившая отвечает — перепроверять её незачем.
		if r := demoted[a.offer.Ref.ProviderID]; r != PassDown && r != PassReauth {
			continue
		}
		done, ok := s.health.TryProbe(a.offer.Ref.ProviderID)
		if !ok {
			continue
		}
		r := s.resolveRequest(req, a)
		go func() {
			pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.probe)
			defer cancel()
			_, err := s.resolver.Resolve(pctx, r)
			if err != nil && !providerDown(err) && !errors.Is(pctx.Err(), context.DeadlineExceeded) {
				err = nil // ответила — пусть и «такой серии нет»: значит, жива
			}
			done(err)
		}()
	}
}

// passed — кого обошли по пути к выдавшей площадке, в исходном порядке.
func passed(ranked []attempt, served catalog.Offer,
	failed map[string]PassReason, demoted map[string]PassReason) []Pass {
	var out []Pass
	seen := map[string]bool{served.Ref.ProviderID: true}
	for _, a := range ranked {
		if a.offer.Ref == served.Ref {
			break
		}
		id := a.offer.Ref.ProviderID
		if seen[id] {
			continue
		}
		seen[id] = true
		switch {
		case failed[id] != "":
			out = append(out, Pass{Provider: id, Reason: failed[id]})
		case demoted[id] != "":
			out = append(out, Pass{Provider: id, Reason: demoted[id]})
		}
	}
	return out
}

func passReason(err error) PassReason {
	switch {
	case errors.Is(err, kernel.ErrReauth):
		return PassReauth
	case errors.Is(err, kernel.ErrBusy):
		return PassBusy
	case errors.Is(err, kernel.ErrNotFound):
		return PassMissing
	}
	return PassFailed
}

// fallbackWorthy — отказ, после которого есть смысл спросить другую площадку.
// Ошибка в самом тикете (нет allowed_origins) — поломка провайдера, и её
// лучше увидеть, чем замаскировать.
func fallbackWorthy(err error) bool {
	return errors.Is(err, kernel.ErrNotFound) || errors.Is(err, kernel.ErrUnavailable) ||
		errors.Is(err, kernel.ErrBusy) || errors.Is(err, context.DeadlineExceeded)
}

// providerDown — отказ говорит о площадке целиком, а не о серии: до неё не
// дошли, она не ответила вовремя, просит притормозить или отвергла вход.
func providerDown(err error) bool {
	return errors.Is(err, kernel.ErrUnreachable) || errors.Is(err, kernel.ErrBusy) ||
		errors.Is(err, kernel.ErrReauth) ||
		errors.Is(err, context.DeadlineExceeded)
}

// mostTelling — какую из ошибок показать, когда не отдала ни одна площадка.
//
// Последняя по счёту не годится: «kinopub лежит, у AnimeGO этой серии нет»
// превратилось бы в «не найдено», хотя серия есть и появится, как только
// kinopub оживёт. Недоступность важнее «притормозите», то — важнее
// «не найдено»; остальные ошибки едут текстом для журнала.
func mostTelling(errs []error) error {
	if len(errs) == 0 {
		return kernel.ErrNotFound
	}
	best := errs[len(errs)-1]
	for _, want := range []error{kernel.ErrUnavailable, kernel.ErrBusy} {
		if i := slices.IndexFunc(errs, func(e error) bool { return errors.Is(e, want) }); i >= 0 {
			best = errs[i]
			break
		}
	}
	if len(errs) == 1 {
		return best
	}
	var rest []string
	for _, e := range errs {
		if e != best {
			rest = append(rest, e.Error())
		}
	}
	return fmt.Errorf("%w; ещё: %s", best, strings.Join(rest, "; "))
}

func sortedForUI(offers []catalog.Offer) []catalog.Offer {
	return catalog.Rank(offers, catalog.Choice{})
}

func (s *Service) store(ctx context.Context, req Request, ep kernel.EpisodeID,
	o catalog.Offer, res provider.Source) (playback.Ticket, error) {
	mode := playback.DeliveryProxy
	if !res.Passthrough {
		// Источник не укладывается в generic-перенос — уводим в локальную
		// доставку вместо протечки его особенностей в gateway.
		mode = playback.DeliveryLocal
	}

	t := playback.Ticket{
		V:         playback.TicketSchemaVersion,
		ID:        s.ids.NewTicketID(),
		UserID:    req.User,
		EpisodeID: ep,
		Upstream: playback.Upstream{
			Protocol:           playback.Protocol(res.Protocol),
			URL:                res.URL,
			Headers:            res.Headers,
			AllowedOrigins:     res.AllowedOrigins,
			IPBound:            res.IPBound,
			CacheKeyPattern:    res.CacheKeyPattern,
			Egress:             res.Egress,
			DropOutsideOrigins: res.DropOutsideOrigins,
			DropHosts:          res.DropHosts,
			Subtitles:          sidecarURLs(res),
		},
		MaxHeight:                 req.Client.MaxHeight,
		ExpiresAt:                 res.ExpiresAt,
		Refreshable:               true,
		Mode:                      mode,
		Choice:                    playback.TicketChoice{Source: o.Ref.ProviderID, Variant: res.Variant},
		TrackProgressFromSegments: req.TrackProgressFromSegments,
	}
	if err := t.Validate(); err != nil {
		return playback.Ticket{}, fmt.Errorf("ticket from %s: %w", o.Ref, err)
	}

	ttl := res.ExpiresAt.Sub(s.clock.Now()) + ticketTTLSlack
	if err := s.tickets.Put(ctx, t, ttl); err != nil {
		return playback.Ticket{}, err
	}
	return t, nil
}

// sidecarURLs — ссылки на отдельные файлы субтитров в порядке Sidecars:
// по этому номеру клиент потом просит файл у шлюза.
func sidecarURLs(res provider.Source) []string {
	var out []string
	for _, t := range res.Sidecars() {
		out = append(out, t.URL)
	}
	return out
}

// RefreshTicket — вызывается gateway по ticket_id, без знания о провайдерах.
// Обновление идёт ПОД ТЕМ ЖЕ ID, поэтому gateway ничего не замечает.
//
// Площадка и вариант берутся из старого тикета: протухшая ссылка посреди
// серии не должна сменить озвучку или площадку.
func (s *Service) RefreshTicket(ctx context.Context, id kernel.TicketID) (playback.Ticket, error) {
	old, err := s.tickets.Get(ctx, id)
	if err != nil {
		return playback.Ticket{}, err
	}
	if !old.Refreshable {
		return playback.Ticket{}, kernel.ErrForbidden
	}
	fresh, err := s.Issue(ctx, Request{
		User: old.UserID, Episode: old.EpisodeID,
		Client:                    provider.ClientProfile{MaxHeight: old.MaxHeight},
		Choice:                    catalog.Choice{Source: old.Choice.Source, Strict: old.Choice.Source != ""},
		Variant:                   old.Choice.Variant,
		TrackProgressFromSegments: old.TrackProgressFromSegments,
	})
	if err != nil {
		return playback.Ticket{}, err
	}
	t := fresh.Ticket
	t.ID = old.ID // тот же ID — иначе клиенту пришлось бы переоткрывать поток
	ttl := t.ExpiresAt.Sub(s.clock.Now()) + ticketTTLSlack
	if err := s.tickets.Put(ctx, t, ttl); err != nil {
		return playback.Ticket{}, err
	}
	return t, nil
}
