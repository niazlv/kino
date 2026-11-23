// Package source — площадка как ОДНО описание: что это, как её назвать
// человеку, какие ключи и маршрут ей нужны и как её собрать.
//
// Иначе площадка жила бы в пяти местах: пакет провайдера, фабрика в
// композиционном корне, списки «аниме» и «ищет по номерам», список
// источников в Android и переменные выкатки. Забыл одно — площадка
// «есть», но не подключается или не видна в настройках. Поэтому пакет
// площадки сам регистрирует свой [Spec] (обычно в spec.go рядом с
// провайдером), а всё остальное — фабрика, списки, экран настроек движка
// (GET /engine/sources) — выводится из реестра.
//
// Площадки-скрипты (ADR-0017) регистрируются тем же Spec: для платформы
// между ними и площадками на Go разницы нет.
package source

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	appprov "github.com/niazleushkin/kino/internal/app/provider"
)

// Kind — что площадка показывает. Склейка считает тайтлы аниме-площадки
// аниме, даже если у хозяина метаданных нет такого жанра.
type Kind string

const (
	KindGeneral Kind = "general" // фильмы и сериалы
	KindAnime   Kind = "anime"   // весь каталог — аниме
)

// Origin — откуда взялась площадка.
type Origin string

const (
	OriginBuiltin Origin = "builtin" // код на Go в этом репозитории
	OriginBundled Origin = "bundled" // скрипт, вшитый в сборку (каталог sources/)
	OriginLocal   Origin = "local"   // скрипт, поставленный или правленный на месте
)

// Setting — ключ или настройка площадки. Значение приходит из окружения
// (на сервере — .env, на устройстве — экран настроек движка).
type Setting struct {
	// Env — имя переменной окружения: FILMIX_TOKEN.
	Env string `json:"env"`
	// Label — как назвать поле человеку.
	Label string `json:"label"`
	// Help — подсказка под полем: где взять, что будет без него.
	Help string `json:"help,omitempty"`
	// Secret — ключ, а не адрес: экран прячет значение.
	Secret bool `json:"secret,omitempty"`
	// Required — без неё площадка не подключается вовсе.
	Required bool `json:"required,omitempty"`
	// Advanced — нужна серверу или разбору, а не человеку с телефона:
	// экран прячет её под «Ещё».
	Advanced bool `json:"advanced,omitempty"`
}

// Env — всё, что площадка получает от платформы при сборке.
type Env struct {
	// Ctx живёт, пока живёт процесс: фоновые циклы площадки (продление
	// входа) останавливаются по нему.
	Ctx context.Context
	// HTTP — клиент маршрута площадки (VPN, прокси, напрямую): маршрут
	// выбирает платформа, площадка его не знает.
	HTTP *http.Client
	Log  *slog.Logger
	// Get — значение настройки по имени переменной; пусто — не задана.
	Get func(name string) string
	// Report — доклад площадки платформе о событии, которое стоит показать
	// человеку (EventAuth). nil — молчать.
	Report func(event string, payload any)
}

// Setting — значение настройки; без Get — пусто.
func (e Env) Setting(name string) string {
	if e.Get == nil {
		return ""
	}
	return strings.TrimSpace(e.Get(name))
}

// Spec — описание площадки.
type Spec struct {
	// ID — имя в PROVIDERS и в external_refs. Меняется только вместе с
	// миграцией привязок, поэтому выбирается один раз: латиница, цифры, _.
	ID string `json:"id"`
	// Name — как назвать человеку.
	Name string `json:"name"`
	// Note — одна-две фразы для экрана настроек: что тут есть и чем
	// площадка капризна (нужен ключ, российский адрес, нестабильна).
	Note string `json:"note,omitempty"`
	Kind Kind   `json:"kind"`
	// Lookup — площадка находит произведение по номерам IMDb и Кинопоиска:
	// так она досбирается к любому открытому тайтлу.
	Lookup bool `json:"lookup,omitempty"`
	// Egress — какой маршрут ей нужен, если не маршрут по умолчанию:
	// "direct" — российский адрес без VPN. Подсказка для выкатки и экрана.
	Egress string `json:"egress,omitempty"`
	// Settings — ключи и адреса; порядок — порядок полей на экране.
	Settings []Setting `json:"settings,omitempty"`
	// Hidden — код оставлен, но площадка мертва или под замком: в списке
	// для выбора на устройстве её нет, на сервере включается явно.
	Hidden bool `json:"hidden,omitempty"`
	// Origin и Version — откуда и какой версии: у скриптов важно, чтобы
	// человек видел, что его правка и есть то, что работает.
	Origin  Origin `json:"origin"`
	Version string `json:"version,omitempty"`

	// New собирает провайдера. Ошибка — неверная настройка (нет ключа):
	// на сервере она роняет запуск, на устройстве отключает площадку.
	New func(env Env) (appprov.Provider, error) `json:"-"`

	// Роли — то, что ядро не выводит из имени площадки («kinopub —
	// источник истории»): площадка объявляет их сама (ADR-0018).
	// В /engine/sources не уходят: приложению они ни к чему.

	// Default — включена, когда PROVIDERS не задан.
	Default bool `json:"-"`
	// History — отдаёт историю просмотров своего аккаунта (перенос в
	// админке; провайдер реализует history.Source). Берётся первая по id.
	History bool `json:"-"`
	// TitleBridge — каталог без номеров MAL: с аниме-площадками его склеивает
	// мост по названиям (Shikimori). Берётся первая по id.
	TitleBridge bool `json:"-"`
	// VoiceNames — её подписи озвучек — образец написания, когда одна студия
	// пишется у площадок по-разному.
	VoiceNames bool `json:"-"`
	// Login — вход человека по коду (локальный движок); nil — входа нет.
	// env.HTTP — клиент маршрута площадки или nil, если маршрута нет.
	Login func(env Env) appprov.DeviceLogin `json:"-"`
}

var idRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// Validate — описание пригодно к регистрации.
func (s Spec) Validate() error {
	switch {
	case !idRe.MatchString(s.ID):
		return fmt.Errorf("площадка %q: id — латиница в нижнем регистре, цифры и _, от 2 до 32 знаков", s.ID)
	case strings.TrimSpace(s.Name) == "":
		return fmt.Errorf("площадка %s: пустое имя", s.ID)
	case s.Kind != KindGeneral && s.Kind != KindAnime:
		return fmt.Errorf("площадка %s: вид %q — ждём general или anime", s.ID, s.Kind)
	case s.New == nil:
		return fmt.Errorf("площадка %s: нет New", s.ID)
	}
	for _, st := range s.Settings {
		if !envRe.MatchString(st.Env) {
			return fmt.Errorf("площадка %s: настройка %q — имя переменной в верхнем регистре", s.ID, st.Env)
		}
	}
	return nil
}

var envRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)

var (
	mu    sync.RWMutex
	specs = map[string]Spec{}
)

// Register — площадка Go регистрирует себя из init. Повтор id — ошибка
// программиста, а не данных: паника при запуске, а не тихая подмена.
func Register(s Spec) {
	if s.Origin == "" {
		s.Origin = OriginBuiltin
	}
	if err := s.Validate(); err != nil {
		panic(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := specs[s.ID]; dup {
		panic(fmt.Sprintf("площадка %q зарегистрирована дважды", s.ID))
	}
	specs[s.ID] = s
}

// Put — регистрация площадки-скрипта: может заменить прежнюю с тем же id.
// Замена встроенной площадки — осознанное действие (правка, присланная
// человеком, или починка без новой сборки), поэтому она разрешена, но
// только скрипту, поставленному на месте, а не вшитому в сборку.
func Put(s Spec) error {
	if err := s.Validate(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	if prev, ok := specs[s.ID]; ok && prev.Origin == OriginBuiltin && s.Origin == OriginBundled {
		return fmt.Errorf("скрипт %s из сборки совпадает по id со встроенной площадкой: переименуйте", s.ID)
	}
	specs[s.ID] = s
	return nil
}

// Get — описание по id.
func Get(id string) (Spec, bool) {
	mu.RLock()
	defer mu.RUnlock()
	s, ok := specs[id]
	return s, ok
}

// All — все площадки по id.
func All() []Spec {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Spec, 0, len(specs))
	for _, s := range specs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// IDs — id площадок, отобранных условием, по алфавиту.
func IDs(keep func(Spec) bool) []string {
	var out []string
	for _, s := range All() {
		if keep(s) {
			out = append(out, s.ID)
		}
	}
	return out
}

// AnimeIDs — площадки, у которых весь каталог — аниме.
func AnimeIDs() []string { return IDs(func(s Spec) bool { return s.Kind == KindAnime }) }

// LookupIDs — площадки, находящие произведение по внешним номерам.
func LookupIDs() []string { return IDs(func(s Spec) bool { return s.Lookup }) }

// DefaultIDs — площадки, включённые без PROVIDERS.
func DefaultIDs() []string { return IDs(func(s Spec) bool { return s.Default }) }

// VoiceNameIDs — площадки, чьи подписи озвучек — образец написания.
func VoiceNameIDs() []string { return IDs(func(s Spec) bool { return s.VoiceNames }) }

// HistoryID — площадка, из аккаунта которой переносится история; "" — нет.
func HistoryID() string { return first(IDs(func(s Spec) bool { return s.History })) }

// TitleBridgeID — площадка без номеров MAL, которую с аниме склеивает мост
// по названиям; "" — нет.
func TitleBridgeID() string { return first(IDs(func(s Spec) bool { return s.TitleBridge })) }

func first(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// EventAuth — доклад о смене состояния входа площадки (Env.Report):
// payload — appprov.AuthStatus.
const EventAuth = "auth"
