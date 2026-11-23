package script

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"

	"github.com/niazleushkin/kino/internal/adapter/providers/sitekit"
	"github.com/niazleushkin/kino/internal/adapter/providers/source"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// host — то, что площадка-скрипт получает от платформы: общее на всех
// исполнителей одной площадки.
type host struct {
	pack *Pack
	c    *sitekit.Client
	// pc — клиент для кода платформы (плееры): ему белый список хостов
	// площадки не нужен, он сам знает, куда ходит.
	pc    *sitekit.Client
	env   source.Env
	log   *slog.Logger
	cache *memo
}

// allowedURL — адрес на одном из хостов площадки или их поддомене.
func allowedURL(hosts []string, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	return allowedHost(hosts, u.Hostname())
}

func allowedHost(hosts []string, h string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	for _, a := range hosts {
		a = strings.ToLower(a)
		if h == a || strings.HasSuffix(h, "."+a) {
			return true
		}
	}
	return false
}

// newHost: клиент маршрута площадки, но с редиректами только на её хосты —
// иначе белый список обходился бы одним 302.
func newHost(p *Pack, env source.Env) *host {
	base := env.HTTP
	if base == nil {
		base = &http.Client{Timeout: 60 * time.Second}
	}
	hc := &http.Client{}
	*hc = *base
	hosts := p.Meta.Hosts
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("больше 10 редиректов")
		}
		if !allowedHost(hosts, req.URL.Hostname()) {
			return fmt.Errorf("редирект на %s — не из hosts площадки", req.URL.Hostname())
		}
		return nil
	}
	gap := time.Duration(p.Meta.GapMS) * time.Millisecond
	if gap <= 0 {
		gap = 300 * time.Millisecond
	}
	log := env.Log
	if log == nil {
		log = slog.Default()
	}
	return &host{pack: p, c: sitekit.NewClient(hc, gap), pc: sitekit.NewClient(base, gap),
		env: env, log: log, cache: newMemo(2000)}
}

// throw — ошибка помощника как исключение JS с кодом таксономии: скрипт
// может поймать её (try/catch) и решить сам, а не пойманная дойдёт до
// платформы с тем же смыслом.
func (v *vm) throw(err error) {
	o := v.rt.NewGoError(err)
	switch {
	case errors.Is(err, kernel.ErrNotFound):
		_ = o.Set("code", "not_found")
	case errors.Is(err, kernel.ErrBusy):
		_ = o.Set("code", "busy")
	default:
		_ = o.Set("code", "unavailable")
	}
	panic(o)
}

func installHost(v *vm) {
	rt := v.rt
	obj := func(fns map[string]func(goja.FunctionCall) goja.Value) *goja.Object {
		o := rt.NewObject()
		for k, f := range fns {
			_ = o.Set(k, f)
		}
		return o
	}

	// ── http ─────────────────────────────────────────────────────────────
	v.set("http", obj(map[string]func(goja.FunctionCall) goja.Value{
		"get": func(c goja.FunctionCall) goja.Value {
			return v.response(v.fetch(http.MethodGet, c.Argument(0).String(), c.Argument(1)))
		},
		"post": func(c goja.FunctionCall) goja.Value {
			return v.response(v.fetch(http.MethodPost, c.Argument(0).String(), c.Argument(1)))
		},
		// json и text — GET с проверкой статуса: 404 — NotFound, 429 —
		// Busy, прочее не 2xx — Unavailable.
		"json": func(c goja.FunctionCall) goja.Value {
			r := v.fetch(v.method(c.Argument(1)), c.Argument(0).String(), c.Argument(1))
			v.mustOK(r)
			return v.parseJSON(r.Body)
		},
		"text": func(c goja.FunctionCall) goja.Value {
			r := v.fetch(v.method(c.Argument(1)), c.Argument(0).String(), c.Argument(1))
			v.mustOK(r)
			return rt.ToValue(string(r.Body))
		},
	}))

	// ── html ─────────────────────────────────────────────────────────────
	v.set("html", obj(map[string]func(goja.FunctionCall) goja.Value{
		"parse": func(c goja.FunctionCall) goja.Value {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(c.Argument(0).String()))
			if err != nil {
				v.throw(err)
			}
			base := ""
			if !goja.IsUndefined(c.Argument(1)) {
				base = c.Argument(1).String()
			}
			return v.node(doc.Selection, base)
		},
		// text — фрагмент HTML как текст: без тегов, с раскрытыми &amp; и
		// сжатыми пробелами.
		"text": func(c goja.FunctionCall) goja.Value {
			return rt.ToValue(sitekit.Text(c.Argument(0).String()))
		},
	}))

	// ── url ──────────────────────────────────────────────────────────────
	v.set("url", obj(map[string]func(goja.FunctionCall) goja.Value{
		"abs": func(c goja.FunctionCall) goja.Value {
			return rt.ToValue(absolute(c.Argument(0).String(), c.Argument(1).String()))
		},
		"host": func(c goja.FunctionCall) goja.Value {
			u, err := url.Parse(c.Argument(0).String())
			if err != nil {
				return rt.ToValue("")
			}
			return rt.ToValue(u.Hostname())
		},
		"param": func(c goja.FunctionCall) goja.Value {
			u, err := url.Parse(c.Argument(0).String())
			if err != nil {
				return rt.ToValue("")
			}
			return rt.ToValue(u.Query().Get(c.Argument(1).String()))
		},
		"query": func(c goja.FunctionCall) goja.Value {
			return rt.ToValue(toValues(c.Argument(0).Export()).Encode())
		},
	}))

	// ── text ─────────────────────────────────────────────────────────────
	v.set("text", obj(map[string]func(goja.FunctionCall) goja.Value{
		"int": func(c goja.FunctionCall) goja.Value {
			m := intRe.FindString(c.Argument(0).String())
			n, _ := strconv.Atoi(m)
			return rt.ToValue(n)
		},
		"year": func(c goja.FunctionCall) goja.Value {
			m := yearRe.FindString(c.Argument(0).String())
			n, _ := strconv.Atoi(m)
			return rt.ToValue(n)
		},
		"between": func(c goja.FunctionCall) goja.Value {
			s, a, b := c.Argument(0).String(), c.Argument(1).String(), c.Argument(2).String()
			i := strings.Index(s, a)
			if i < 0 {
				return rt.ToValue("")
			}
			s = s[i+len(a):]
			if j := strings.Index(s, b); j >= 0 {
				s = s[:j]
			}
			return rt.ToValue(s)
		},
	}))

	// ── base64 ───────────────────────────────────────────────────────────
	v.set("base64", obj(map[string]func(goja.FunctionCall) goja.Value{
		"encode": func(c goja.FunctionCall) goja.Value {
			return rt.ToValue(base64.StdEncoding.EncodeToString([]byte(c.Argument(0).String())))
		},
		"decode": func(c goja.FunctionCall) goja.Value {
			s := strings.TrimSpace(c.Argument(0).String())
			for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				if b, err := enc.DecodeString(s); err == nil {
					return rt.ToValue(string(b))
				}
			}
			v.throw(fmt.Errorf("base64.decode: не base64: %w", kernel.ErrInvalidInput))
			return nil
		},
	}))

	// ── log ──────────────────────────────────────────────────────────────
	logf := func(level slog.Level) func(goja.FunctionCall) goja.Value {
		return func(c goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(c.Arguments))
			for _, a := range c.Arguments {
				parts = append(parts, a.String())
			}
			if v.h != nil {
				v.h.log.Log(v.context(), level, "скрипт: "+strings.Join(parts, " "))
			}
			return goja.Undefined()
		}
	}
	v.set("log", obj(map[string]func(goja.FunctionCall) goja.Value{
		"info": logf(slog.LevelInfo), "warn": logf(slog.LevelWarn), "debug": logf(slog.LevelDebug),
	}))

	// ── settings: только объявленные в settings ──────────────────────────
	v.set("settings", obj(map[string]func(goja.FunctionCall) goja.Value{
		"get": func(c goja.FunctionCall) goja.Value {
			name := c.Argument(0).String()
			if !declared(v.pack, name) {
				// Иначе скрипт на сервере прочёл бы пароль базы из окружения.
				panic(rt.NewTypeError("settings.get(%q): настройки нет в settings площадки", name))
			}
			if v.h == nil {
				return rt.ToValue("")
			}
			return rt.ToValue(v.h.env.Setting(name))
		},
	}))

	// ── cache: общий на исполнителей площадки, в памяти ─────────────────
	v.set("cache", obj(map[string]func(goja.FunctionCall) goja.Value{
		"get": func(c goja.FunctionCall) goja.Value {
			if v.h == nil {
				return goja.Undefined()
			}
			b, ok := v.h.cache.get(c.Argument(0).String())
			if !ok {
				return goja.Undefined()
			}
			return v.parseJSON(b)
		},
		"put": func(c goja.FunctionCall) goja.Value {
			if v.h == nil {
				return goja.Undefined()
			}
			b, err := json.Marshal(c.Argument(1).Export())
			if err != nil {
				v.throw(fmt.Errorf("cache.put: %w", err))
			}
			ttl := time.Duration(c.Argument(2).ToInteger()) * time.Second
			if ttl <= 0 {
				ttl = 10 * time.Minute
			}
			v.h.cache.put(c.Argument(0).String(), b, ttl)
			return goja.Undefined()
		},
	}))

	// ── players: чужие плееры, которым нужен не iframe, а адрес файла ───
	v.set("players", obj(map[string]func(goja.FunctionCall) goja.Value{
		"sibnet": func(c goja.FunctionCall) goja.Value {
			v.needNet()
			pl, err := currentPlayers()
			if err != nil {
				v.throw(err)
			}
			st, err := pl.Sibnet(v.context(), v.h.pc, c.Argument(0).String(), argStr(c.Argument(1)))
			if err != nil {
				v.throw(err)
			}
			return rt.ToValue(streamJS(st))
		},
		"aniboom": func(c goja.FunctionCall) goja.Value {
			v.needNet()
			pl, err := currentPlayers()
			if err != nil {
				v.throw(err)
			}
			st, err := pl.AniBoom(v.context(), v.h.pc, c.Argument(0).String(), argStr(c.Argument(1)))
			if err != nil {
				v.throw(err)
			}
			return rt.ToValue(streamJS(st))
		},
	}))
}

var (
	intRe  = regexp.MustCompile(`\d+`)
	yearRe = regexp.MustCompile(`\b(19|20)\d\d\b`)
)

func declared(p *Pack, name string) bool {
	for _, s := range p.Meta.Settings {
		if s.Env == name {
			return true
		}
	}
	return false
}

func argStr(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

func (v *vm) needNet() {
	if v.h == nil {
		panic(v.rt.NewTypeError("сеть недоступна при разборе файла: запросы — только внутри методов площадки"))
	}
}

func (v *vm) method(opts goja.Value) string {
	if o, ok := opts.(*goja.Object); ok {
		if m := argStr(o.Get("method")); m != "" {
			return strings.ToUpper(m)
		}
		if f := o.Get("form"); f != nil && !goja.IsUndefined(f) {
			return http.MethodPost
		}
		if j := o.Get("json"); j != nil && !goja.IsUndefined(j) {
			return http.MethodPost
		}
	}
	return http.MethodGet
}

// fetch — запрос скрипта: только на хосты площадки, маршрутом площадки, с
// вежливой паузой и браузерным User-Agent (sitekit.Client).
func (v *vm) fetch(method, raw string, opts goja.Value) sitekit.Response {
	v.needNet()
	hdr := map[string]string{}
	var body io.Reader
	if o, ok := opts.(*goja.Object); ok {
		if h, ok := o.Get("headers").(*goja.Object); ok {
			for _, k := range h.Keys() {
				hdr[k] = h.Get(k).String()
			}
		}
		if q := o.Get("query"); q != nil && !goja.IsUndefined(q) {
			if u, err := url.Parse(raw); err == nil {
				vals := u.Query()
				for k, vv := range toValues(q.Export()) {
					vals[k] = vv
				}
				u.RawQuery = vals.Encode()
				raw = u.String()
			}
		}
		if ref := argStr(o.Get("referer")); ref != "" {
			hdr["Referer"] = ref
		}
		switch {
		case o.Get("form") != nil && !goja.IsUndefined(o.Get("form")):
			hdr["Content-Type"] = "application/x-www-form-urlencoded; charset=UTF-8"
			body = strings.NewReader(toValues(o.Get("form").Export()).Encode())
		case o.Get("json") != nil && !goja.IsUndefined(o.Get("json")):
			b, err := json.Marshal(o.Get("json").Export())
			if err != nil {
				v.throw(err)
			}
			hdr["Content-Type"] = "application/json"
			body = strings.NewReader(string(b))
		case o.Get("body") != nil && !goja.IsUndefined(o.Get("body")):
			body = strings.NewReader(o.Get("body").String())
		}
	}
	if !allowedURL(v.pack.Meta.Hosts, raw) {
		panic(v.rt.NewTypeError("http: %s — не из hosts площадки (%s)", raw, strings.Join(v.pack.Meta.Hosts, ", ")))
	}
	resp, err := v.h.c.Do(v.context(), method, raw, hdr, body)
	if err != nil {
		v.throw(fmt.Errorf("%s %s: %w", method, raw, err))
	}
	return resp
}

func (v *vm) mustOK(r sitekit.Response) {
	if r.Status >= 200 && r.Status < 300 {
		return
	}
	where := r.URL.String()
	switch r.Status {
	case http.StatusNotFound, http.StatusGone:
		v.throw(fmt.Errorf("%s: HTTP %d: %w", where, r.Status, kernel.ErrNotFound))
	case http.StatusTooManyRequests:
		v.throw(fmt.Errorf("%s: HTTP %d: %w", where, r.Status, kernel.ErrBusy))
	}
	v.throw(fmt.Errorf("%s: HTTP %d: %w", where, r.Status, kernel.ErrUnavailable))
}

func (v *vm) response(r sitekit.Response) goja.Value {
	o := v.rt.NewObject()
	_ = o.Set("status", r.Status)
	_ = o.Set("url", r.URL.String())
	_ = o.Set("text", string(r.Body))
	hdr := map[string]any{}
	for k := range r.Header {
		hdr[strings.ToLower(k)] = r.Header.Get(k)
	}
	_ = o.Set("headers", hdr)
	_ = o.Set("ok", r.Status >= 200 && r.Status < 300)
	_ = o.Set("json", func(goja.FunctionCall) goja.Value { return v.parseJSON(r.Body) })
	return o
}

func (v *vm) parseJSON(b []byte) goja.Value {
	parse, _ := goja.AssertFunction(v.rt.Get("JSON").ToObject(v.rt).Get("parse"))
	out, err := parse(goja.Undefined(), v.rt.ToValue(string(b)))
	if err != nil {
		v.throw(fmt.Errorf("ответ — не JSON: %w", kernel.ErrUnavailable))
	}
	return out
}

// node — выборка HTML для скрипта: find, first, text, attr, html, abs.
func (v *vm) node(sel *goquery.Selection, base string) goja.Value {
	rt := v.rt
	o := rt.NewObject()
	wrap := func(s *goquery.Selection) goja.Value {
		if s.Length() == 0 {
			return goja.Null()
		}
		return v.node(s, base)
	}
	_ = o.Set("find", func(c goja.FunctionCall) goja.Value {
		var out []goja.Value
		sel.Find(c.Argument(0).String()).Each(func(_ int, s *goquery.Selection) {
			out = append(out, v.node(s, base))
		})
		return rt.ToValue(out)
	})
	_ = o.Set("first", func(c goja.FunctionCall) goja.Value {
		return wrap(sel.Find(c.Argument(0).String()).First())
	})
	_ = o.Set("text", func(goja.FunctionCall) goja.Value {
		return rt.ToValue(strings.Join(strings.Fields(sel.Text()), " "))
	})
	_ = o.Set("attr", func(c goja.FunctionCall) goja.Value {
		val, _ := sel.Attr(c.Argument(0).String())
		return rt.ToValue(val)
	})
	_ = o.Set("html", func(goja.FunctionCall) goja.Value {
		h, _ := sel.Html()
		return rt.ToValue(h)
	})
	// abs — атрибут как абсолютный адрес (src, href, data-src).
	_ = o.Set("abs", func(c goja.FunctionCall) goja.Value {
		val, _ := sel.Attr(c.Argument(0).String())
		if val == "" {
			return rt.ToValue("")
		}
		return rt.ToValue(absolute(base, val))
	})
	return o
}

func absolute(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "//") {
		return "https:" + ref
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

func toValues(x any) url.Values {
	out := url.Values{}
	m, ok := x.(map[string]any)
	if !ok {
		return out
	}
	for k, val := range m {
		switch t := val.(type) {
		case []any:
			for _, e := range t {
				out.Add(k, fmt.Sprint(e))
			}
		case nil:
		default:
			out.Set(k, fmt.Sprint(t))
		}
	}
	return out
}

// streamJS — поток плеера в форме ответа resolve().
func streamJS(st sitekit.Stream) map[string]any {
	m := map[string]any{
		"protocol": st.Protocol, "url": st.URL, "headers": st.Headers,
		"origins": st.AllowedOrigins, "heights": st.Heights,
	}
	if !st.ExpiresAt.IsZero() {
		m["expires"] = st.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return m
}

// memo — кэш скрипта: значения в JSON, у каждого свой срок.
type memo struct {
	mu  sync.Mutex
	max int
	m   map[string]memoEntry
}

type memoEntry struct {
	b   []byte
	exp time.Time
}

func newMemo(max int) *memo { return &memo{max: max, m: map[string]memoEntry{}} }

func (c *memo) get(k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Now().After(e.exp) {
		delete(c.m, k)
		return nil, false
	}
	return e.b, true
}

func (c *memo) put(k string, b []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.max {
		now := time.Now()
		for key, e := range c.m {
			if now.After(e.exp) || len(c.m) >= c.max {
				delete(c.m, key)
			}
		}
	}
	c.m[k] = memoEntry{b: b, exp: time.Now().Add(ttl)}
}
