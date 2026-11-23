package script

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/niazleushkin/kino/internal/adapter/providers/source"
)

// Ext — расширение файла площадки.
const Ext = ".js"

// Loaded — файл площадки после разбора.
type Loaded struct {
	// File — имя файла (без каталога).
	File   string
	Origin source.Origin
	Pack   *Pack // nil — файл не разобрался, см. Err
	Err    error
	// Overrides — что эта площадка заменила при установке: вшитый скрипт
	// или встроенную площадку на Go («правлено вами»).
	Overrides source.Origin
}

// ID — id площадки; у сломанного файла — имя файла без расширения: так
// человек узнаёт, какой файл чинить.
func (l Loaded) ID() string {
	if l.Pack != nil {
		return l.Pack.Meta.ID
	}
	return strings.TrimSuffix(l.File, Ext)
}

// LoadFS разбирает все *.js в корне fsys.
func LoadFS(fsys fs.FS, origin source.Origin) []Loaded {
	if fsys == nil {
		return nil
	}
	names, err := fs.Glob(fsys, "*"+Ext)
	if err != nil {
		return nil
	}
	sort.Strings(names)
	out := make([]Loaded, 0, len(names))
	for _, n := range names {
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			out = append(out, Loaded{File: n, Origin: origin, Err: err})
			continue
		}
		p, err := Parse(n, b)
		out = append(out, Loaded{File: n, Origin: origin, Pack: p, Err: err})
	}
	return out
}

// LoadDir — то же для каталога на диске; нет каталога — пусто.
func LoadDir(dir string, origin source.Origin) []Loaded {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return LoadFS(os.DirFS(dir), origin)
}

// Install регистрирует разобранные площадки. Порядок важен: сначала вшитые,
// затем местные — местная правка заменяет вшитую с тем же id (и встроенную
// на Go тоже: так её чинят без новой сборки). Возвращает те же записи с
// пометкой, что чем заменено, и ошибкой регистрации, если была.
func Install(all ...[]Loaded) []Loaded {
	var out []Loaded
	for _, group := range all {
		for _, l := range group {
			if l.Pack != nil && l.Err == nil {
				if prev, ok := source.Get(l.Pack.Meta.ID); ok {
					l.Overrides = prev.Origin
				}
				if err := source.Put(l.Pack.Spec(l.Origin)); err != nil {
					l.Err = err
					l.Overrides = ""
				}
			}
			out = append(out, l)
		}
	}
	return out
}

// SaveFile кладёт файл площадки в каталог: сначала разбирает (сломанный
// файл не ставится), имя — по id площадки. Возвращает разобранную площадку.
func SaveFile(dir string, src []byte) (*Pack, error) {
	p, err := Parse("новый файл", src)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := path.Join(dir, p.Meta.ID+Ext)
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, src, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, name); err != nil {
		return nil, fmt.Errorf("установка %s: %w", p.Meta.ID, err)
	}
	return p, nil
}

// Newer — версия a новее b (1.2.3 против 1.2): так видно, какая правка
// свежее — присланная или та, что уже стоит.
func Newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
