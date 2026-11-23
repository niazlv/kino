package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/providers/providertest"
	"github.com/niazleushkin/kino/internal/adapter/providers/script"
	"github.com/niazleushkin/kino/internal/adapter/providers/source"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/bootstrap/kit"
	"github.com/niazleushkin/kino/internal/bootstrap/providers"
)

// Площадки на устройстве (ADR-0017): человек видит все площадки, которые
// знает движок (на Go и скрипты), ставит свою или правленую площадку-скрипт,
// проверяет её тем же набором проверок, что и мы (providertest), и выгружает
// файл, чтобы прислать нам. Установка и удаление вступают в силу после
// перезапуска движка — приложение перезапускает его само, как при любой смене
// настроек.

// sourceView — площадка для экрана настроек.
type sourceView struct {
	source.Spec
	// Enabled — включена в PROVIDERS этого запуска.
	Enabled bool `json:"enabled"`
	// State, Reason — как у /engine/status, только у включённых.
	State  string `json:"state,omitempty"`
	Reason string `json:"reason,omitempty"`
	// File — у скрипта: имя файла; Hash — отпечаток текста.
	File string `json:"file,omitempty"`
	Hash string `json:"hash,omitempty"`
	// Overrides — что заменила местная правка: builtin или bundled.
	Overrides string `json:"overrides,omitempty"`
	// Removable — есть местный файл: его можно удалить (вернуть вшитое).
	Removable bool `json:"removable,omitempty"`
	// Broken — местный файл не разобрался: почему.
	Broken string `json:"broken,omitempty"`
	// Pending — на диске уже другой файл, чем работает сейчас: нужен
	// перезапуск движка.
	Pending bool `json:"pending,omitempty"`
}

type sourcesDeps struct {
	auth     authenticator
	registry *appprov.Registry
	cfg      providers.Config
	issues   []providers.Issue
	routes   *kit.Routes
	log      *slog.Logger
}

func sourcesRoutes(mux *http.ServeMux, d sourcesDeps) {
	owner := requireOwner(d.auth)
	mux.Handle("GET /engine/sources", owner(http.HandlerFunc(d.list)))
	mux.Handle("GET /engine/sources/{id}/file", owner(http.HandlerFunc(d.file)))
	mux.Handle("POST /engine/sources", owner(http.HandlerFunc(d.install)))
	mux.Handle("DELETE /engine/sources/{id}", owner(http.HandlerFunc(d.remove)))
	mux.Handle("POST /engine/sources/check", owner(http.HandlerFunc(d.check)))
}

func (d sourcesDeps) list(w http.ResponseWriter, _ *http.Request) {
	enabled := map[string]bool{}
	for _, id := range d.cfg.IDs {
		enabled[id] = true
	}
	states := map[string]providerStatus{}
	for _, st := range providerStates(d.cfg.IDs, d.issues, func(id string) bool {
		_, err := d.registry.Get(id)
		return err == nil
	}) {
		states[st.ID] = st
	}
	running := map[string]script.Loaded{}
	for _, l := range d.cfg.Scripts {
		running[l.ID()] = l
	}
	onDisk := map[string]script.Loaded{}
	for _, l := range script.LoadDir(d.cfg.SourcesDir, source.OriginLocal) {
		onDisk[l.ID()] = l
	}

	out := []sourceView{}
	seen := map[string]bool{}
	for _, s := range source.All() {
		if s.Hidden && !enabled[s.ID] {
			continue
		}
		v := sourceView{Spec: s, Enabled: enabled[s.ID]}
		if st, ok := states[s.ID]; ok && v.Enabled {
			v.State, v.Reason = st.State, st.Reason
		}
		if l, ok := running[s.ID]; ok && l.Pack != nil && s.Origin != source.OriginBuiltin {
			v.File, v.Hash, v.Overrides = l.File, l.Pack.Hash, string(l.Overrides)
		}
		if l, ok := onDisk[s.ID]; ok {
			v.Removable = true
			if l.Err != nil {
				v.Broken = l.Err.Error()
			}
			v.Pending = l.Pack == nil || l.Pack.Hash != v.Hash
		} else if s.Origin == source.OriginLocal {
			// Файл удалён после запуска: площадка уйдёт при перезапуске.
			v.Pending = true
		}
		seen[s.ID] = true
		out = append(out, v)
	}
	// Местные файлы, ставшие площадкой только после перезапуска, и сломанные.
	for id, l := range onDisk {
		if seen[id] {
			continue
		}
		v := sourceView{Spec: source.Spec{ID: id, Name: id, Origin: source.OriginLocal}, File: l.File,
			Removable: true, Pending: true, Enabled: enabled[id]}
		if l.Pack != nil {
			sp := l.Pack.Spec(source.OriginLocal)
			v.Spec, v.Hash = sp, l.Pack.Hash
		}
		if l.Err != nil {
			v.Broken = l.Err.Error()
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": out, "dir": d.cfg.SourcesDir})
}

// file — текст площадки-скрипта: местный, иначе вшитый. ?download=1 — как
// файл для отправки.
func (d sourcesDeps) file(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := source.Get(id); !ok && !d.localExists(id) {
		writeJSONError(w, http.StatusNotFound, "not_found", "такой площадки нет")
		return
	}
	b, err := d.readScript(id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "not_found",
			"у площадки нет файла: она встроенная, на Go — править её можно, только поставив скрипт с тем же id")
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+script.Ext+`"`)
	}
	_, _ = w.Write(b)
}

func (d sourcesDeps) localPath(id string) string {
	return filepath.Join(d.cfg.SourcesDir, id+script.Ext)
}

func (d sourcesDeps) localExists(id string) bool {
	if d.cfg.SourcesDir == "" {
		return false
	}
	_, err := os.Stat(d.localPath(id))
	return err == nil
}

func (d sourcesDeps) readScript(id string) ([]byte, error) {
	if d.localExists(id) {
		return os.ReadFile(d.localPath(id))
	}
	return fs.ReadFile(script.BundleFS(), id+script.Ext)
}

// install — поставить площадку-скрипт (тело — текст .js). Сломанный файл
// не ставится: ответ — почему.
func (d sourcesDeps) install(w http.ResponseWriter, r *http.Request) {
	if d.cfg.SourcesDir == "" {
		writeJSONError(w, http.StatusConflict, "no_dir", "каталог площадок не задан (SOURCES_DIR)")
		return
	}
	src, err := io.ReadAll(io.LimitReader(r.Body, script.MaxSize+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_body", err.Error())
		return
	}
	p, err := script.SaveFile(d.cfg.SourcesDir, src)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_source", err.Error())
		return
	}
	d.log.Info("площадка-скрипт поставлена", "id", p.Meta.ID, "version", p.Meta.Version, "hash", p.Hash)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": p.Meta.ID, "name": p.Meta.Name, "version": p.Meta.Version, "hash": p.Hash, "restart": true,
	})
}

// remove — удалить местный файл: вернётся вшитая или встроенная площадка
// (если была), иначе площадка исчезнет.
func (d sourcesDeps) remove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !d.localExists(id) {
		writeJSONError(w, http.StatusNotFound, "not_found", "местного файла этой площадки нет")
		return
	}
	if err := os.Remove(d.localPath(id)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "remove_failed", err.Error())
		return
	}
	d.log.Info("площадка-скрипт удалена", "id", id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "restart": true})
}

type checkRequest struct {
	// ID — площадка, известная движку; Source — текст .js, ещё не
	// поставленный (проверить до установки).
	ID     string `json:"id"`
	Source string `json:"source"`
	Query  string `json:"query"`
	Want   string `json:"want"`
	Fetch  bool   `json:"fetch"`
}

// check — живые проверки площадки, те же, что `sourcekit check`. Это
// явное действие человека: десяток запросов к площадке по кнопке.
func (d sourcesDeps) check(w http.ResponseWriter, r *http.Request) {
	var req checkRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, script.MaxSize+4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_body", err.Error())
		return
	}
	var spec source.Spec
	switch {
	case req.Source != "":
		p, err := script.Parse("проверка.js", []byte(req.Source))
		if err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_source", err.Error())
			return
		}
		spec = p.Spec(source.OriginLocal)
	case req.ID != "":
		s, ok := source.Get(req.ID)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "not_found", "такой площадки нет")
			return
		}
		spec = s
	default:
		writeJSONError(w, http.StatusBadRequest, "bad_body", "нужен id или source")
		return
	}
	route := d.cfg.Egress[spec.ID]
	if route == "" {
		route = kit.DefaultRoute
	}
	hc, ok := d.routes.Client(route)
	if !ok {
		hc = d.routes.Default()
	}
	if d.cfg.ClientWrap != nil {
		// Та же подпись (User-Agent), с какой площадка ходит в работе.
		hc = d.cfg.ClientWrap(spec.ID, hc)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	p, err := spec.New(source.Env{Ctx: ctx, HTTP: hc, Log: d.log.With("check", spec.ID), Get: d.cfg.GetenvFunc()})
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "build_failed", err.Error())
		return
	}
	c := providertest.Case{ID: spec.ID, Query: req.Query, Want: req.Want}
	if req.Fetch {
		c.Fetch = hc
	}
	rep := providertest.Run(ctx, p, c)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		d.log.Warn("проверка площадки упёрлась в срок", "id", spec.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": rep, "failed": rep.Failed(), "text": rep.String()})
}
