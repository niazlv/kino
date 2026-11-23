package catalog

// Реконсилиация — лечение склейки, развалившейся при первом заходе.
//
// UpsertTitle не пересматривает однажды принятое решение: элемент площадки
// узнаётся по привязке и сразу уезжает в свой тайтл. Но данные приходят
// позже: номер MAL добирается у плеера, ключи названий пополняются
// вариантами, и то, что в момент обхода было «не на что клеить», со временем
// становится очевидным дублем. Здесь — периодический проход: найти пары с
// общим MAL или ключом, решить правилами (matching.ShouldMerge — те же
// предохранители, что у живой склейки) и слить через MergeTitles.

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/niazleushkin/kino/internal/app/matching"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// ReconcilePairs — пары тайтлов, которые могут оказаться одним
// произведением. Пара упорядочена: первая сторона — вероятный хозяин
// метаданных (площадка с большим приоритетом), слияние «в него».
type ReconcilePairs interface {
	DuplicatePairs(ctx context.Context, limit int) ([]DuplicatePair, error)
}

// DuplicatePair — как в adapter/postgres.
type DuplicatePair struct {
	A, B kernel.TitleID
}

// ReconcileCandidate — полные данные тайтла для решения.
type ReconcileCandidate interface {
	Candidate(ctx context.Context, id kernel.TitleID) (matching.Candidate, error)
}

// ReconcileMerger — слияние с следом «reconciled» в matched_by.
type ReconcileMerger interface {
	MergeTitlesReconciled(ctx context.Context, from, into kernel.TitleID) error
}

// ReconcileMAL — хранение и добор номера MAL.
type ReconcileMAL interface {
	RefsWithoutMAL(ctx context.Context, providerID string, limit int) ([]kernel.ProviderRef, error)
	SetRefMAL(ctx context.Context, ref kernel.ProviderRef, mal string) error
}

// NameCandidate — аниме из внешнего реестра (Shikimori) в терминах
// платформы: вид приведён к movie/series, названия — все, что реестр знает.
type NameCandidate struct {
	ID    string // номер MyAnimeList — у реестра он и есть id
	Names []string
	Year  int
	Kind  string // movie | series
}

// MALTitle — тайтл площадки без номера MAL, кандидат на мост по названию.
type MALTitle struct {
	TitleID  kernel.TitleID
	Title    string
	Original string
	Year     int
	Kind     string
}

// BridgeMAL — данные для моста: кому добрать номер и куда записать.
type BridgeMAL interface {
	AnimeTitlesWithoutMAL(ctx context.Context, providerID string, limit int) ([]MALTitle, error)
	SetTitleRefsMAL(ctx context.Context, titleID kernel.TitleID, providerID, mal string) error
}

// NameMAL — резолвер «название → номер MAL». Реестр не площадка: каталога
// он не пишет, а только отвечает, что за произведение.
type NameMAL interface {
	Search(ctx context.Context, q string) ([]NameCandidate, error)
}

// OrphanTitle — сериал только аниме-площадок, похожий на отдельно
// заведённый сезон другого тайтла.
type OrphanTitle struct {
	ID       kernel.TitleID
	Title    string
	Original string
	Year     int
}

// SeasonStore — данные для лечения сезонов (см. FixSeasons).
type SeasonStore interface {
	SeasonConflicts(ctx context.Context) ([]kernel.TitleID, error)
	ReseasonRefs(ctx context.Context, title kernel.TitleID, moves []matching.Move) error
	SeasonOrphans(ctx context.Context, animeProviders []string) ([]OrphanTitle, error)
	SeasonOwners(ctx context.Context, self kernel.TitleID, keys, mals []string) ([]matching.Candidate, error)
	AdoptSeason(ctx context.Context, from, into kernel.TitleID, season int) error
}

// Reconciler сводит развалившуюся склейку.
type Reconciler struct {
	pairs    ReconcilePairs
	cand     ReconcileCandidate
	merger   ReconcileMerger
	mal      ReconcileMAL
	titles   func(providerID string) (TitleSource, error)
	animeIDs []string
	log      *slog.Logger

	// Мост по названиям: площадкам без своего MAL (kinopub) номер добирает
	// внешний реестр, и тогда общий MAL склеивает их с аниме-площадками.
	bridge   BridgeMAL
	names    NameMAL
	provider string

	// seasons — лечение сезонов; nil — выключено. seasonsDry — только
	// писать в журнал, что было бы сделано: так новое правило сначала
	// смотрят на боевых данных, а потом включают.
	seasons    SeasonStore
	seasonsDry bool

	// noMAL — элемент площадки, у которого MAL не нашлось. Память, а не
	// база: отрицательный результат не стоит колонки в схеме, а после
	// перезапуска один лишний проход по ним ничего не стоит.
	noMAL map[kernel.ProviderRef]bool
	// noName — тайтл, чьё название реестр не узнал. Та же память, тот же
	// смысл: данные каталога меняются медленно, повторный поиск ничего
	// не даст, пока не приедет обновление.
	noName map[kernel.TitleID]bool
	// refused — пары, уже отвергнутые правилами. Без неё каждый проход
	// заново решал бы те же тысячи kinopub-однофамильцев — кандидат-то
	// по ним грузится из базы дважды на пару.
	refused map[DuplicatePair]bool
	// matchOpts — отдельно включаемые правила слияния уже заведённых.
	matchOpts matching.Options
}

func NewReconciler(pairs ReconcilePairs, cand ReconcileCandidate, merger ReconcileMerger,
	mal ReconcileMAL, titles func(string) (TitleSource, error), animeIDs []string,
	log *slog.Logger) *Reconciler {
	return &Reconciler{pairs: pairs, cand: cand, merger: merger, mal: mal,
		titles: titles, animeIDs: animeIDs, log: log,
		noMAL: map[kernel.ProviderRef]bool{}, refused: map[DuplicatePair]bool{},
		noName: map[kernel.TitleID]bool{}}
}

// WithBridge включает мост по названиям: providerID — чьи тайтлы мостить,
// names — резолвер (Shikimori), bridge — кому писать найденные номера.
func (r *Reconciler) WithBridge(providerID string, bridge BridgeMAL, names NameMAL) *Reconciler {
	r.provider, r.bridge, r.names = providerID, bridge, names
	return r
}

// WithCartoonMerge включает слияние аниме с «Мультфильмом» kinopub
// (matching.Options.CrossCartoon) для уже заведённых тайтлов. Отдельно от
// склейки новых элементов: первый проход сольёт сразу все накопившиеся
// пары, и это решение принимается своим переключателем.
func (r *Reconciler) WithCartoonMerge(on bool) *Reconciler {
	r.matchOpts.CrossCartoon = on
	return r
}

// WithSeasons включает лечение сезонов (FixSeasons); dry — только журнал.
func (r *Reconciler) WithSeasons(store SeasonStore, dry bool) *Reconciler {
	r.seasons, r.seasonsDry = store, dry
	return r
}

// SeasonFix — итог прохода FixSeasons.
type SeasonFix struct {
	Realigned int // привязок переведено на верный сезон
	Adopted   int // тайтлов-сезонов перенесено к своему сериалу
}

// FixSeasons лечит сезоны, склеенные не туда (см. matching.Realign и
// matching.Adopt).
//
// Сначала выравниваются сезоны внутри тайтлов: одна работа у двух площадок
// не должна стоять на двух сезонах. Потом тайтлы-сезоны переносятся к
// своим сериалам — по одному, с перечитыванием хозяев: перенесённый ранний
// сезон даёт следующему, что продолжать. Перенос мог свести в одном тайтле
// одну работу на разных сезонах — поэтому выравнивание ещё раз.
func (r *Reconciler) FixSeasons(ctx context.Context) (SeasonFix, error) {
	var fix SeasonFix
	if r.seasons == nil {
		return fix, nil
	}
	n, err := r.realign(ctx)
	fix.Realigned += n
	if err != nil {
		return fix, err
	}
	orphans, err := r.seasons.SeasonOrphans(ctx, r.animeIDs)
	if err != nil {
		return fix, err
	}
	for _, o := range orphans {
		if err := ctx.Err(); err != nil {
			return fix, err
		}
		c, err := r.cand.Candidate(ctx, o.ID)
		if err != nil {
			continue // уже перенесён в этом же проходе
		}
		names := []string{o.Title, o.Original}
		var mals []string
		for _, ref := range c.Refs {
			if ref.MAL != "" {
				mals = append(mals, ref.MAL)
			}
		}
		keys := matching.Keys(matching.BaseNames(names...)...)
		owners, err := r.seasons.SeasonOwners(ctx, o.ID, keys, mals)
		if err != nil {
			r.log.Warn("хозяева сезона не прочитаны", "title", o.ID, "err", err)
			continue
		}
		a, ok := matching.Adopt(c, names, owners)
		if !ok {
			continue
		}
		if r.seasonsDry {
			r.log.Info("сезон перенёсся бы", "from", o.ID, "title", o.Title, "into", a.Into,
				"season", a.SeasonNo, "by", a.By)
			fix.Adopted++
			continue
		}
		if err := r.seasons.AdoptSeason(ctx, o.ID, a.Into, a.SeasonNo); err != nil {
			r.log.Warn("перенос сезона не удался", "from", o.ID, "into", a.Into, "err", err)
			continue
		}
		fix.Adopted++
		r.log.Info("сезон перенесён", "from", o.ID, "title", o.Title, "into", a.Into,
			"season", a.SeasonNo, "by", a.By)
	}
	if fix.Adopted > 0 && !r.seasonsDry {
		n, err := r.realign(ctx)
		fix.Realigned += n
		if err != nil {
			return fix, err
		}
	}
	return fix, nil
}

func (r *Reconciler) realign(ctx context.Context) (int, error) {
	ids, err := r.seasons.SeasonConflicts(ctx)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return moved, err
		}
		c, err := r.cand.Candidate(ctx, id)
		if err != nil {
			continue
		}
		moves := matching.Realign(c)
		if len(moves) == 0 {
			continue
		}
		for _, m := range moves {
			r.log.Info("сезон привязки выровнен по MAL", "title", id, "provider", m.Ref.ProviderID,
				"item", m.Ref.ItemID, "from", m.Ref.SeasonNo, "to", m.To, "dry", r.seasonsDry)
		}
		if r.seasonsDry {
			moved += len(moves)
			continue
		}
		if err := r.seasons.ReseasonRefs(ctx, id, moves); err != nil {
			r.log.Warn("сезоны не выровнены", "title", id, "err", err)
			continue
		}
		moved += len(moves)
	}
	return moved, nil
}

// TitleSource — достать тайтл у площадки (включая номер MAL).
type TitleSource func(ctx context.Context, ref kernel.ProviderRef) (dp.Title, error)

// pairBatch — предохранитель объёма выдачи DuplicatePairs, а не окно:
// за проход реконсилиатор обязан увидеть все пары, иначе отказанные в
// начале навсегда закрывали бы тех, кто дальше очереди.
const pairBatch = 50000

// refusedCap — потолок памяти об отвергнутых парах: после неё память
// обнуляется и проходы начинают сначала. Данные меняются медленно, а
// безлимитная память у процесса, живущего месяцами, когда-нибудь кончится.
const refusedCap = 100000

// Run делает один проход реконсилиации и возвращает число слияний.
func (r *Reconciler) Run(ctx context.Context) (int, error) {
	pairs, err := r.pairs.DuplicatePairs(ctx, pairBatch)
	if err != nil {
		return 0, err
	}
	merged := 0
	for _, p := range pairs {
		if err := ctx.Err(); err != nil {
			return merged, err
		}
		if r.refused[p] {
			continue
		}
		a, err := r.cand.Candidate(ctx, p.A)
		if err != nil {
			continue // тайтл исчез после слияния из этой же пачки
		}
		b, err := r.cand.Candidate(ctx, p.B)
		if err != nil || a.ID == b.ID {
			continue
		}
		if !matching.ShouldMergeWith(r.matchOpts, matching.Pair{A: a, B: b}) {
			if len(r.refused) >= refusedCap {
				r.refused = map[DuplicatePair]bool{}
			}
			r.refused[p] = true
			continue
		}
		if err := r.merger.MergeTitlesReconciled(ctx, p.B, p.A); err != nil {
			// Одно неудачное слияние не должно рвать проход.
			r.log.Warn("слияние не удалось", "from", p.B, "into", p.A, "err", err)
			continue
		}
		merged++
		r.log.Info("дубли слиты", "from", p.B, "into", p.A, "by", describeMatch(a, b))
	}
	return merged, nil
}

func describeMatch(a, b matching.Candidate) string {
	if shared := sharedMALs(a, b); len(shared) > 0 {
		return "mal " + shared[0]
	}
	return "name"
}

func sharedMALs(a, b matching.Candidate) []string {
	var out []string
	for _, ra := range a.Refs {
		if ra.MAL == "" {
			continue
		}
		for _, rb := range b.Refs {
			if ra.MAL == rb.MAL {
				out = append(out, ra.MAL)
			}
		}
	}
	return out
}

// malBatch — сколько элементов площадки опрашивать за один проход добора.
const malBatch = 100

// malPause — пауза между элементами: это живые запросы к площадке, и
// добор не должен спорить с обходом за её лимиты.
const malPause = 300 * time.Millisecond

// EnrichMAL добирает номера MAL у привязок аниме-площадок, где обход его
// не сохранил (плеер площадки не ответил в тот момент).
//
// Это топливо для реконсилиации: общий MAL — самый сильный признак дубля,
// и после добора пары находятся сами. Элемент, чей MAL не нашёлся,
// запоминается и повторно не опрашивается до перезапуска.
func (r *Reconciler) EnrichMAL(ctx context.Context) (int, error) {
	found := 0
	for _, id := range r.animeIDs {
		src, err := r.titles(id)
		if err != nil {
			continue
		}
		refs, err := r.mal.RefsWithoutMAL(ctx, id, malBatch)
		if err != nil {
			r.log.Warn("добор MAL: привязки не прочитаны", "provider", id, "err", err)
			continue
		}
		for _, ref := range refs {
			if err := ctx.Err(); err != nil {
				return found, err
			}
			if r.noMAL[ref] {
				continue
			}
			t, err := src(ctx, ref)
			if err != nil {
				r.noMAL[ref] = true
				continue
			}
			if t.MAL == "" {
				r.noMAL[ref] = true
				continue
			}
			if err := r.mal.SetRefMAL(ctx, ref, t.MAL); err != nil {
				return found, err
			}
			found++
			select {
			case <-ctx.Done():
				return found, ctx.Err()
			case <-time.After(malPause):
			}
		}
	}
	return found, nil
}

// nameBatch — сколько тайтлов мостить за один проход. Каждый — запрос в
// реестр, а реестр — гость: он не должен спорить с обходом площадок.
const nameBatch = 60

// EnrichMALByNames — мост kinopub↔аниме: тайтлам площадки без своего MAL
// номер добирается по названию через реестр Shikimori.
//
// Это вторая половина топлива реконсилиации. Аниме-площадки пишут MAL сами
// (первая половина — EnrichMAL), а kinopub нет: пока у его тайтлов номера
// нет, общий MAL между ними невозможен, и половина дублей не склеивается
// никогда. Найденное пишется всем привязкам тайтла — и с этого момента
// пары kinopub↔animego находятся обычным порядком.
func (r *Reconciler) EnrichMALByNames(ctx context.Context) (int, error) {
	if r.bridge == nil || r.names == nil {
		return 0, nil
	}
	titles, err := r.bridge.AnimeTitlesWithoutMAL(ctx, r.provider, nameBatch)
	if err != nil {
		return 0, err
	}
	found := 0
	for _, t := range titles {
		if err := ctx.Err(); err != nil {
			return found, err
		}
		if r.noName[t.TitleID] {
			continue
		}
		mal := r.resolveTitle(ctx, t)
		if mal == "" {
			r.noName[t.TitleID] = true
			continue
		}
		if err := r.bridge.SetTitleRefsMAL(ctx, t.TitleID, r.provider, mal); err != nil {
			return found, err
		}
		found++
		r.log.Info("MAL по названию", "title", t.TitleID, "mal", mal)
		select {
		case <-ctx.Done():
			return found, ctx.Err()
		case <-time.After(malPause):
		}
	}
	return found, nil
}

// resolveTitle ищет произведение в реестре: сначала по оригинальному
// названию (ромадзи у реестра родное), затем по русскому. Между поисками —
// пауза: это два живых запроса на один тайтл.
func (r *Reconciler) resolveTitle(ctx context.Context, t MALTitle) string {
	for _, q := range []string{t.Original, t.Title} {
		if strings.TrimSpace(q) == "" {
			continue
		}
		cands, err := r.names.Search(ctx, q)
		if err != nil {
			r.log.Warn("реестр не ответил", "title", t.TitleID, "err", err)
			continue
		}
		if mal := pickMAL(t, cands); mal != "" {
			return mal
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(malPause):
		}
	}
	return ""
}

// pickMAL выбирает из кандидатов реестра единственного узнанного.
//
// Узнаётся тот, у кого совпал вид (фильм к фильму, сериальное к сериалу),
// год в окне ±1 (шире — опасно: у франшизы под одно название уходят
// ремейки) и есть пересечение ключей названий — той же функцией Keys, что
// пишет их в каталог, включая варианты: «Yuuki Yuuna…» реестра и «Yuki
// Yuna is a Hero» тайтла — одно произведение. Кандидатов два и больше —
// выбор уже лотерея, и правильный ответ здесь — не выбирать.
func pickMAL(t MALTitle, cands []NameCandidate) string {
	keys := matching.Keys(t.Title, t.Original)
	var hits []NameCandidate
	for _, c := range cands {
		if c.Kind != t.Kind {
			continue
		}
		if t.Year == 0 || c.Year == 0 || absInt(c.Year-t.Year) > 1 {
			continue
		}
		if !intersectsKeys(keys, matching.Keys(c.Names...)) {
			continue
		}
		hits = append(hits, c)
	}
	if len(hits) == 1 {
		return hits[0].ID
	}
	return ""
}

func intersectsKeys(a, b []string) bool {
	set := make(map[string]bool, len(b))
	for _, k := range b {
		set[k] = true
	}
	for _, k := range a {
		if set[k] {
			return true
		}
	}
	return false
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
