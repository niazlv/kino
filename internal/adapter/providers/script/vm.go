package script

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dop251/goja"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// prelude — то, что есть у каждого скрипта до его первой строки. Отдельной
// программой, чтобы номера строк в ошибках совпадали с файлом автора.
const prelude = `
function NotFound(m) { var e = new Error(m || "не найдено"); e.code = "not_found"; return e; }
function Unavailable(m) { var e = new Error(m || "площадка недоступна"); e.code = "unavailable"; return e; }
function Busy(m) { var e = new Error(m || "площадка просит подождать"); e.code = "busy"; return e; }
`

var preludeProg = goja.MustCompile("prelude.js", prelude, false)

// vm — один исполнитель скрипта. goja.Runtime не потокобезопасен, поэтому
// вызовы площадки разбирают исполнителей из пула (provider.go), а не делят
// один.
type vm struct {
	rt   *goja.Runtime
	def  *goja.Object
	h    *host // nil — разбор файла: сети нет
	pack *Pack

	mu  sync.Mutex
	ctx context.Context // контекст текущего вызова
	cur uint64          // номер текущего вызова: позднее прерывание чужого не трогает
	seq uint64
}

func newVM(p *Pack, h *host, _ any) (*vm, error) {
	v := &vm{rt: goja.New(), h: h, pack: p}
	v.rt.SetMaxCallStackSize(1024)
	if _, err := v.rt.RunProgram(preludeProg); err != nil {
		return nil, err
	}
	var def *goja.Object
	v.set("source", func(call goja.FunctionCall) goja.Value {
		if def != nil {
			panic(v.rt.NewTypeError("source() вызван дважды: одна площадка — один файл"))
		}
		o, ok := call.Argument(0).(*goja.Object)
		if !ok {
			panic(v.rt.NewTypeError("source(): ждём объект с описанием площадки"))
		}
		def = o
		return goja.Undefined()
	})
	installHost(v)
	if _, err := v.rt.RunProgram(p.prog); err != nil {
		return nil, fmt.Errorf("%s: %w", p.Name, cleanErr(err))
	}
	if def == nil {
		return nil, fmt.Errorf("%s: файл не вызвал source({...})", p.Name)
	}
	v.def = def
	return v, nil
}

func (v *vm) set(name string, val any) {
	if err := v.rt.Set(name, val); err != nil {
		panic(err)
	}
}

// call вызывает метод площадки. Контекст обрывает исполнение: истёк срок —
// скрипт прерван там, где был, даже в бесконечном цикле.
func (v *vm) call(ctx context.Context, method string, args ...any) (goja.Value, error) {
	fn, ok := goja.AssertFunction(v.def.Get(method))
	if !ok {
		return nil, fmt.Errorf("%s: нет метода %s: %w", v.pack.Meta.ID, method, kernel.ErrNotFound)
	}
	v.mu.Lock()
	v.seq++
	id := v.seq
	v.cur, v.ctx = id, ctx
	v.mu.Unlock()
	v.rt.ClearInterrupt()
	stop := context.AfterFunc(ctx, func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		if v.cur == id {
			v.rt.Interrupt(ctx.Err())
		}
	})
	defer func() {
		v.mu.Lock()
		v.cur, v.ctx = 0, nil
		v.mu.Unlock()
		stop()
		v.rt.ClearInterrupt()
	}()
	jsArgs := make([]goja.Value, len(args))
	for i, a := range args {
		jsArgs[i] = v.rt.ToValue(a)
	}
	res, err := fn(v.def, jsArgs...)
	if err != nil {
		return nil, v.mapErr(ctx, method, err)
	}
	return res, nil
}

// context — контекст текущего вызова для помощников (http).
func (v *vm) context() context.Context {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ctx == nil {
		return context.Background()
	}
	return v.ctx
}

// mapErr переводит ошибку скрипта в таксономию платформы: throw NotFound()
// — «нет такого», Unavailable() — площадка не отвечает, прочее — поломка
// скрипта (для платформы тоже «недоступна»: играть надо с другой площадки).
func (v *vm) mapErr(ctx context.Context, method string, err error) error {
	where := v.pack.Meta.ID + "." + method
	var intr *goja.InterruptedError
	if errors.As(err, &intr) {
		cause := ctx.Err()
		if cause == nil {
			cause = errors.New("прервано")
		}
		return fmt.Errorf("%s: %w: %w", where, cause, kernel.ErrUnavailable)
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		msg := cleanErr(err).Error()
		if o, ok := ex.Value().(*goja.Object); ok {
			if goErr, ok := o.Export().(error); ok {
				// Ошибка помощника (http и т.п.), не пойманная скриптом, —
				// с её таксономией.
				return fmt.Errorf("%s: %w", where, goErr)
			}
			switch code := o.Get("code"); {
			case code == nil:
			case code.String() == "not_found":
				return fmt.Errorf("%s: %s: %w", where, msg, kernel.ErrNotFound)
			case code.String() == "busy":
				return fmt.Errorf("%s: %s: %w", where, msg, kernel.ErrBusy)
			case code.String() == "unavailable":
				return fmt.Errorf("%s: %s: %w", where, msg, kernel.ErrUnavailable)
			}
		}
		return fmt.Errorf("%s: ошибка скрипта: %s: %w", where, msg, kernel.ErrUnavailable)
	}
	return fmt.Errorf("%s: %w", where, err)
}

// cleanErr — сообщение без служебного хвоста goja: из стека остаётся
// первое место в файле автора («at search (anime.js:12:5)»), без кадров
// помощников на Go и пролога.
func cleanErr(err error) error {
	var ex *goja.Exception
	if !errors.As(err, &ex) {
		return err
	}
	s := ex.Error()
	head, stack, found := strings.Cut(s, "\n\tat ")
	// Сообщение помощника goja дописывает в ту же строку: «… at pkg.func5 (native)».
	if i := strings.Index(head, " at github.com/"); i > 0 {
		head = head[:i]
	}
	if !found {
		return errors.New(head)
	}
	for _, fr := range strings.Split(stack, "\n\tat ") {
		fr = strings.TrimSpace(fr)
		if fr == "" || strings.Contains(fr, "(native)") || strings.Contains(fr, "prelude.js") {
			continue
		}
		return errors.New(head + " (" + fr + ")")
	}
	return errors.New(head)
}

// exportPlain — значение без функций: для описания площадки, где методы
// соседствуют с данными.
func exportPlain(x any) any {
	switch t := x.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range t {
			if isFunc(v) {
				continue
			}
			out[k] = exportPlain(v)
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, v := range t {
			if !isFunc(v) {
				out = append(out, exportPlain(v))
			}
		}
		return out
	}
	return x
}

func isFunc(v any) bool {
	switch v.(type) {
	case func(goja.FunctionCall) goja.Value:
		return true
	}
	return false
}
