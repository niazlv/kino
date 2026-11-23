// Package script — площадки-скрипты (ADR-0017): площадка как один файл на
// JavaScript, который движок на устройстве и сервер исполняют одинаково.
//
// Зачем. Площадки — расходники: домены, вёрстка и подписи меняются чаще,
// чем выходят сборки. Площадку на Go человек с телефона не добавит и не
// починит; скрипт — добавит: поставил файл, движок перезапустился, площадка
// в поиске. Тот же файл, присланный нам, проверяется набором проверок
// (cmd/sourcekit check) и ложится в sources/ — и на сервер.
//
// Файл вызывает source({...}) один раз: описание площадки и её методы.
// Два пути:
//
//   - свои потоки: episodes(id) и resolve(req) — скрипт сам отдаёт адрес
//     HLS или MP4;
//   - чужие плееры: players(id, episode) — скрипт говорит, где на странице
//     серии Kodik, CVH или Alloha, а серии, озвучки, поток и разметку
//     пропуска добывает общий код чужих плееров (Players.NewSite, ADR-0018).
//
// Песочница: сеть — только на хосты из hosts (и их поддомены), без файлов,
// без процессов; каждый вызов ограничен по времени. Запросы считаются тем же
// счётчиком, что у остальных площадок, и идут маршрутом площадки.
//
// Справочник для автора площадки — docs/script-sources.md.
package script

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/niazleushkin/kino/internal/adapter/providers/source"
)

// MaxSize — потолок файла площадки: это сценарий разбора, а не библиотека.
const MaxSize = 512 << 10

// Meta — описание площадки из source({...}).
type Meta struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Version  string           `json:"version"`
	Kind     string           `json:"kind"`
	Note     string           `json:"note"`
	Hosts    []string         `json:"hosts"`
	Home     string           `json:"home"`
	Egress   string           `json:"egress"`
	Lookup   bool             `json:"-"`
	Settings []source.Setting `json:"settings"`
	// Author — кто прислал: для журнала правок, не для доступа.
	Author string `json:"author"`
	// Thumbs, Subtitles — у пути «чужие плееры»: есть ли у площадки CVH
	// (кадры серий) и Alloha (внешние субтитры).
	Thumbs    bool `json:"thumbs"`
	Subtitles bool `json:"subtitles"`
	// GapMS — пауза между запросами к одному хосту площадки, мс (0 — 300).
	GapMS int `json:"gap"`

	// Methods — какие методы есть в файле.
	Methods []string `json:"-"`
}

// Pack — разобранный файл площадки.
type Pack struct {
	Meta Meta
	// Name — имя файла, под которым пришёл: для сообщений об ошибках.
	Name string
	// Source — текст целиком: его отправляют нам и сравнивают с вшитым.
	Source []byte
	// Hash — отпечаток текста: «правлено вами» = отпечаток не как у вшитого.
	Hash string

	prog *goja.Program
}

// Players — путь «чужие плееры».
func (p *Pack) Players() bool { return p.has("players") }

func (p *Pack) has(m string) bool {
	for _, x := range p.Meta.Methods {
		if x == m {
			return true
		}
	}
	return false
}

var methodNames = []string{"search", "title", "episodes", "resolve", "players", "voices", "lookup", "changed", "health"}

var versionRe = regexp.MustCompile(`^\d+\.\d+(\.\d+)?$`)

// Parse разбирает файл площадки: компилирует, исполняет верхний уровень в
// песочнице БЕЗ сети и проверяет описание. Ошибка — человеку: что не так и
// где.
func Parse(name string, src []byte) (*Pack, error) {
	if len(src) > MaxSize {
		return nil, fmt.Errorf("%s: файл больше %d КБ", name, MaxSize>>10)
	}
	prog, err := goja.Compile(name, string(src), false)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	p := &Pack{Name: name, Source: src, prog: prog}
	sum := sha256.Sum256(src)
	p.Hash = hex.EncodeToString(sum[:8])

	v, err := newVM(p, nil, nil)
	if err != nil {
		return nil, err
	}
	def := v.def
	raw, err := json.Marshal(exportPlain(def.Export()))
	if err != nil {
		return nil, fmt.Errorf("%s: описание: %w", name, err)
	}
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: описание: %w", name, err)
	}
	for _, mn := range methodNames {
		if _, ok := goja.AssertFunction(def.Get(mn)); ok {
			m.Methods = append(m.Methods, mn)
		}
	}
	m.Lookup = contains(m.Methods, "lookup")
	if m.Kind == "" {
		m.Kind = string(source.KindGeneral)
	}
	p.Meta = m
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return p, nil
}

func (p *Pack) validate() error {
	m := p.Meta
	var errs []error
	if m.ID == "" {
		errs = append(errs, errors.New("нет id"))
	}
	if m.Name == "" {
		errs = append(errs, errors.New("нет name"))
	}
	if !versionRe.MatchString(m.Version) {
		errs = append(errs, fmt.Errorf("version %q — ждём вида 1.2.3: по ней видно, какая правка новее", m.Version))
	}
	if len(m.Hosts) == 0 {
		errs = append(errs, errors.New("hosts пуст: скрипту некуда ходить"))
	}
	for _, h := range m.Hosts {
		if h == "" || strings.ContainsAny(h, "/:* ") {
			errs = append(errs, fmt.Errorf("hosts: %q — только имя хоста, без схемы, порта и звёздочек", h))
		}
	}
	if m.Home != "" && !allowedURL(m.Hosts, m.Home) {
		errs = append(errs, fmt.Errorf("home %q — не из hosts", m.Home))
	}
	for _, need := range []string{"search", "title"} {
		if !contains(m.Methods, need) {
			errs = append(errs, fmt.Errorf("нет метода %s", need))
		}
	}
	if !contains(m.Methods, "players") && (!contains(m.Methods, "episodes") || !contains(m.Methods, "resolve")) {
		errs = append(errs, errors.New("нужен players(id, episode) — или пара episodes(id) и resolve(req)"))
	}
	if !contains(m.Methods, "health") && m.Home == "" {
		errs = append(errs, errors.New("нужен home (адрес проверки живости) или метод health()"))
	}
	spec := source.Spec{ID: m.ID, Name: m.Name, Kind: source.Kind(m.Kind), Settings: m.Settings,
		New: func(source.Env) (prov, error) { return nil, nil }}
	if err := spec.Validate(); err != nil && m.ID != "" && m.Name != "" {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Spec — описание для реестра площадок.
func (p *Pack) Spec(origin source.Origin) source.Spec {
	m := p.Meta
	return source.Spec{
		ID: m.ID, Name: m.Name, Note: m.Note, Kind: source.Kind(m.Kind), Lookup: m.Lookup,
		Egress: m.Egress, Settings: m.Settings, Origin: origin, Version: m.Version,
		New: func(env source.Env) (prov, error) { return p.New(env) },
	}
}

// callTimeout — потолок одного вызова скрипта: поиск с десятком страниц
// укладывается, бесконечный цикл — нет. У обхода (changed) это срок
// простоя, а не всего обхода. Переменная — ради теста.
var callTimeout = 90 * time.Second

func withCallTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < callTimeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, callTimeout)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
