// Команда sourcekit — всё про площадки в одном месте (ADR-0017).
//
//	sourcekit list                         все площадки: id, откуда, вид, ключи
//	sourcekit check <id|файл.js> -q запрос живые проверки площадки (контракт)
//	sourcekit new <id> [-players]          заготовка площадки-скрипта в providers/sources/
//	sourcekit import <файл.js> [-q запрос] принять присланную площадку или
//	                                       правку: разбор, версия, проверки,
//	                                       копия в providers/sources/
//
// Ключи площадок и маршрут наружу — из окружения, как у core:
// FILMIX_TOKEN=… EGRESS_ROUTES=default=socks5://127.0.0.1:1080 sourcekit check filmix -q Дюна
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niazleushkin/kino/internal/adapter/providers/providertest"
	"github.com/niazleushkin/kino/internal/adapter/providers/script"
	"github.com/niazleushkin/kino/internal/adapter/providers/source"
	appprov "github.com/niazleushkin/kino/internal/app/provider"
	"github.com/niazleushkin/kino/internal/bootstrap/kit"
	"github.com/niazleushkin/kino/internal/bootstrap/providers"
)

type prov = appprov.Provider

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "list":
		err = list(args)
	case "check":
		err = check(args)
	case "new":
		err = scaffold(args)
	case "import":
		err = importPack(args)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sourcekit:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `sourcekit — площадки в одном месте

  sourcekit list [-json]                  все площадки
  sourcekit check <id|файл.js> -q запрос  живые проверки площадки
        [-want подстрока] [-fetch] [-json]
  sourcekit new <id> [-players] [-name Имя]  заготовка providers/sources/<id>.js
  sourcekit import <файл.js> [-q запрос]  принять присланную площадку в providers/sources/

Ключи площадок и маршрут наружу — из окружения, как у core.
`)
	os.Exit(2)
}

// load — все площадки: на Go (импорт bootstrap/providers) и скрипты из
// providers/sources/ и SOURCES_DIR.
func load() []script.Loaded {
	return providers.LoadScripts(os.Getenv("SOURCES_DIR"))
}

func list(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "вывести JSON (как /engine/sources)")
	_ = fs.Parse(args)
	scripts := load()
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(source.All())
	}
	for _, s := range source.All() {
		keys := []string{}
		for _, st := range s.Settings {
			if !st.Advanced {
				keys = append(keys, st.Env)
			}
		}
		flags := string(s.Kind)
		if s.Lookup {
			flags += ",lookup"
		}
		if s.Hidden {
			flags += ",hidden"
		}
		if s.Egress != "" {
			flags += ",egress=" + s.Egress
		}
		fmt.Printf("%-12s %-8s %-8s %-22s %-26s %s\n", s.ID, s.Origin, s.Version, s.Name, flags, strings.Join(keys, " "))
	}
	for _, l := range scripts {
		if l.Err != nil {
			fmt.Printf("%-12s СЛОМАН  %s: %v\n", l.ID(), l.File, l.Err)
		}
	}
	return nil
}

func check(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("check: какую площадку — id или файл .js")
	}
	target, rest := args[0], args[1:]
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	q := fs.String("q", "", "запрос, на который у площадки точно есть находки")
	want := fs.String("want", "", "подстрока названия, которая должна найтись")
	fetch := fs.Bool("fetch", false, "скачать начало потока (плейлист или первые байты)")
	asJSON := fs.Bool("json", false, "отчёт в JSON")
	_ = fs.Parse(rest)

	spec, err := specFor(target)
	if err != nil {
		return err
	}
	rep, err := runCheck(spec, providertest.Case{ID: spec.ID, Query: *q, Want: *want}, *fetch)
	if err != nil {
		return err
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(rep)
	} else {
		fmt.Print(rep.String())
	}
	if rep.Failed() {
		os.Exit(3)
	}
	return nil
}

// specFor — площадка по id или по файлу (файл разбирается и регистрируется
// как местный: так проверяется правка встроенной площадки).
func specFor(target string) (source.Spec, error) {
	load()
	if strings.HasSuffix(target, script.Ext) || strings.ContainsRune(target, os.PathSeparator) {
		b, err := os.ReadFile(target)
		if err != nil {
			return source.Spec{}, err
		}
		p, err := script.Parse(filepath.Base(target), b)
		if err != nil {
			return source.Spec{}, err
		}
		s := p.Spec(source.OriginLocal)
		if err := source.Put(s); err != nil {
			return source.Spec{}, err
		}
		return s, nil
	}
	s, ok := source.Get(target)
	if !ok {
		return source.Spec{}, fmt.Errorf("площадки %q нет (sourcekit list)", target)
	}
	return s, nil
}

func runCheck(spec source.Spec, c providertest.Case, fetch bool) (providertest.Report, error) {
	routes, err := kit.LoadRoutes(60 * time.Second)
	if err != nil {
		return providertest.Report{}, err
	}
	route := spec.Egress
	if route == "" {
		route = kit.DefaultRoute
	}
	hc, ok := routes.Client(route)
	if !ok {
		fmt.Fprintf(os.Stderr, "маршрута %q нет в EGRESS_ROUTES — иду маршрутом по умолчанию\n", route)
		hc = routes.Default()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	p, err := spec.New(source.Env{Ctx: ctx, HTTP: hc, Log: log, Get: os.Getenv})
	if err != nil {
		return providertest.Report{}, fmt.Errorf("сборка площадки: %w", err)
	}
	if fetch {
		c.Fetch = hc
	}
	return providertest.Run(ctx, p, c), nil
}

//go:embed template_direct.js
var tmplDirect string

//go:embed template_players.js
var tmplPlayers string

func scaffold(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("new: нужен id площадки (латиница, цифры, _)")
	}
	id, rest := args[0], args[1:]
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	players := fs.Bool("players", false, "площадка на чужих плеерах (Kodik, CVH, Alloha)")
	name := fs.String("name", id, "как назвать площадку человеку")
	dir := fs.String("dir", "providers/sources", "куда положить файл")
	_ = fs.Parse(rest)
	if err := (source.Spec{ID: id, Name: *name, Kind: source.KindGeneral,
		New: func(source.Env) (prov, error) { return nil, nil }}).Validate(); err != nil {
		return err
	}
	load()
	if _, taken := source.Get(id); taken {
		return fmt.Errorf("id %q занят: возьмите другой или правьте ту площадку", id)
	}
	tmpl := tmplDirect
	if *players {
		tmpl = tmplPlayers
	}
	out := strings.NewReplacer("__ID__", id, "__NAME__", *name).Replace(tmpl)
	path := filepath.Join(*dir, id+script.Ext)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s уже есть", path)
	}
	if _, err := script.Parse(filepath.Base(path), []byte(out)); err != nil {
		return fmt.Errorf("заготовка не разбирается (ошибка sourcekit): %w", err)
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return err
	}
	fmt.Printf("создан %s\nдальше: впишите адреса и разбор, затем\n  go run ./cmd/sourcekit check %s -q \"запрос\" -fetch\n", path, path)
	return nil
}

// importPack — принять присланную площадку: разобрать, сверить версию с
// тем, что уже в providers/sources/, прогнать проверки (если задан запрос) и положить
// файл. Коммит и выкатку делает человек — это и есть ревью.
func importPack(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("import: какой файл")
	}
	file, rest := args[0], args[1:]
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	q := fs.String("q", "", "запрос для живой проверки; пусто — без неё")
	want := fs.String("want", "", "подстрока названия, которая должна найтись")
	dir := fs.String("dir", "providers/sources", "каталог площадок репозитория")
	force := fs.Bool("force", false, "принять, даже если версия не выросла")
	_ = fs.Parse(rest)

	var src []byte
	var err error
	if file == "-" {
		src, err = io.ReadAll(os.Stdin)
	} else {
		src, err = os.ReadFile(file)
	}
	if err != nil {
		return err
	}
	p, err := script.Parse(filepath.Base(file), src)
	if err != nil {
		return err
	}
	load()
	dst := filepath.Join(*dir, p.Meta.ID+script.Ext)
	if prev, err := os.ReadFile(dst); err == nil {
		if bytes.Equal(prev, src) {
			fmt.Println("такой файл уже в", dst, "— ничего не меняю")
			return nil
		}
		old, perr := script.Parse(filepath.Base(dst), prev)
		if perr == nil && !script.Newer(p.Meta.Version, old.Meta.Version) && !*force {
			return fmt.Errorf("версия %s не новее нашей %s: попросите автора поднять version (или -force)",
				p.Meta.Version, old.Meta.Version)
		}
		if perr == nil {
			fmt.Printf("правка %s: %s → %s\n", p.Meta.ID, old.Meta.Version, p.Meta.Version)
		}
	} else if s, ok := source.Get(p.Meta.ID); ok && s.Origin == source.OriginBuiltin {
		fmt.Printf("внимание: %s заменит встроенную площадку на Go с тем же id — так задумано только для починки\n", p.Meta.ID)
	} else {
		fmt.Printf("новая площадка %s «%s» %s\n", p.Meta.ID, p.Meta.Name, p.Meta.Version)
	}
	if *q != "" {
		s := p.Spec(source.OriginLocal)
		if err := source.Put(s); err != nil && !strings.Contains(err.Error(), "совпадает по id") {
			return err
		}
		rep, err := runCheck(s, providertest.Case{ID: s.ID, Query: *q, Want: *want}, true)
		if err != nil {
			return err
		}
		fmt.Print(rep.String())
		if rep.Failed() && !*force {
			return fmt.Errorf("проверки не прошли — файл не положен (или -force)")
		}
	} else {
		fmt.Println("живая проверка пропущена (-q не задан)")
	}
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		return err
	}
	fmt.Printf("положено в %s — посмотрите git diff, закоммитьте и выкатите\n", dst)
	return nil
}
