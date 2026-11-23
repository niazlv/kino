package engine

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// User-Agent локального движка (ADR-0015).
//
// Движок работает с устройства человека, и представляться чужими сайтам он
// должен тем, чем является: приложением. Браузерная подпись нужна части площадок
// (CDN режут клиентов без неё), поэтому подпись браузероподобная — но с явной
// пометкой приложения и его версии в конце, а не чужой «Chrome на Маке».
//
//	Mozilla/5.0 (Linux; Android 10; K) … Chrome/140.0.0.0 Mobile Safari/537.36 KinoPlatform-Android/0.2.14
//
// Это только про UA. TLS-отпечаток транспорта остаётся браузерным (ADR-0003):
// CDN режут и по нему, и менять его ради «честности» значило бы сломать
// воспроизведение.
//
// Менять подпись человек может сам, по каждой площадке отдельно: «мы не советуем,
// но можно, без пересборки». Что человек задал — идёт наружу как есть, на его
// совести: площадка вправе отказать или закрыть доступ.
const (
	// uaBrowserBase — браузероподобная часть: Chrome той же мажорной версии, что и
	// TLS-отпечаток транспорта, платформа — Android в сокращённой форме, как её
	// шлёт сам Chrome.
	uaBrowserBase = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) " +
		"Chrome/140.0.0.0 Mobile Safari/537.36"

	// defaultAppUA — подпись, когда приложение своей не передало (движок запущен
	// сам по себе, не из приложения).
	defaultAppUA = "KinoPlatform-Engine"

	// maxUALen — предел длины заданной человеком подписи.
	maxUALen = 512

	// EnvUAPrefix — переменная окружения подписи одной площадки:
	// ENGINE_UA_ANILIBRIA, ENGINE_UA_ANIMEGO_ME, …
	EnvUAPrefix = "ENGINE_UA_"
)

// uaPolicy решает, с каким User-Agent запрос уходит наружу. Неизменяемый после
// сборки, поэтому безопасен для параллельного чтения.
type uaPolicy struct {
	app string            // пометка приложения: «KinoPlatform-Android/0.2.14»
	all string            // подпись человека для всех площадок (пусто — по умолчанию)
	per map[string]string // подпись человека для одной площадки
}

// newUAPolicy собирает политику. Заданное человеком проверяется: подпись с
// управляющими символами или длиннее предела отброшена (с предупреждением в
// журнал) — иначе заголовок невалиден и запросы площадки падали бы все.
func newUAPolicy(app, all string, per map[string]string, log *slog.Logger) *uaPolicy {
	app = strings.TrimSpace(app)
	if !validUA(app) {
		app = defaultAppUA
	}
	p := &uaPolicy{app: app, per: map[string]string{}}
	if v := strings.TrimSpace(all); v != "" {
		if validUA(v) {
			p.all = v
		} else if log != nil {
			log.Warn("User-Agent для всех площадок не годится (управляющие символы или слишком длинный) — использую подпись по умолчанию")
		}
	}
	for id, v := range per {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !validUA(v) {
			if log != nil {
				log.Warn("User-Agent площадки не годится (управляющие символы или слишком длинный) — использую подпись по умолчанию", "provider", id)
			}
			continue
		}
		p.per[id] = v
	}
	return p
}

// validUA — подпись годится для заголовка: непустая, печатный ASCII, не длиннее предела.
func validUA(s string) bool {
	if s == "" || len(s) > maxUALen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// Default — подпись по умолчанию: браузероподобная часть и пометка приложения.
func (p *uaPolicy) Default() string {
	if p == nil {
		return uaBrowserBase + " " + defaultAppUA
	}
	return uaBrowserBase + " " + p.app
}

// For — User-Agent, с которым площадка id уходит наружу. current — то, что
// подставил сам провайдер ("" — ничего).
//
//   - подпись человека для этой площадки — как есть;
//   - иначе подпись человека «для всех» — как есть;
//   - иначе, если провайдер представился браузером (Mozilla/…, в том числе чужим
//     «Chrome на Маке»), — наша подпись по умолчанию;
//   - иначе (okhttp и подобное, нужное площадке) — его подпись плюс наша пометка.
func (p *uaPolicy) For(id, current string) string {
	if p == nil {
		return current
	}
	if v, ok := p.per[id]; ok {
		return v
	}
	if p.all != "" {
		return p.all
	}
	// Уже окончательная подпись — наша пометка или то, что задал человек. Сюда приходит
	// шлюз: подпись в тикете площадка получила на резолве, и общий слой транспорта,
	// через который шлюз тянет сегменты, не вправе её переписать.
	if p.final(current) {
		return current
	}
	if current == "" || strings.HasPrefix(current, "Mozilla/") {
		return p.Default()
	}
	return current + " " + p.app
}

// final — подпись менять не надо: в ней наша пометка или она задана человеком
// (для всех или для какой-то площадки).
func (p *uaPolicy) final(ua string) bool {
	if ua == "" {
		return false
	}
	if strings.Contains(ua, p.app) || (p.all != "" && ua == p.all) {
		return true
	}
	for _, v := range p.per {
		if ua == v {
			return true
		}
	}
	return false
}

// Custom — площадки с подписью человека (по возрастанию); "*" — подпись «для всех».
func (p *uaPolicy) Custom() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.per)+1)
	if p.all != "" {
		out = append(out, "*")
	}
	ids := make([]string, 0, len(p.per))
	for id := range p.per {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return append(out, ids...)
}

// uaKey помечает запрос, чьим User-Agent уже распорядился слой площадки: общий
// слой транспорта такой запрос не трогает, чтобы не переписать заданное человеком.
type uaKey struct{}

// transport — слой, ставящий User-Agent площадки id; id "" — общий слой для
// запросов, не привязанных к площадке (картинки, вход, шлюз без тикета).
// Исходный запрос не меняется — по контракту RoundTripper работаем с копией.
func (p *uaPolicy) transport(id string, next http.RoundTripper) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Context().Value(uaKey{}) != nil {
			return next.RoundTrip(r)
		}
		r2 := r.Clone(context.WithValue(r.Context(), uaKey{}, id))
		r2.Header.Set("User-Agent", p.For(id, r.Header.Get("User-Agent")))
		return next.RoundTrip(r2)
	})
}

// WrapDefault — для kit.Routes.WrapTransport: общий слой.
func (p *uaPolicy) WrapDefault(next http.RoundTripper) http.RoundTripper {
	return p.transport("", next)
}

// WrapClient — для providers.Config.ClientWrap: свой клиент площадки с её
// User-Agent. Копия клиента: общий клиент маршрута остальным площадкам не меняется.
func (p *uaPolicy) WrapClient(id string, hc *http.Client) *http.Client {
	if hc == nil {
		return nil
	}
	c := *hc
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = p.transport(id, base)
	return &c
}
