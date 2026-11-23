package segcache

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Закачка в кеш, которую читают, не дожидаясь её конца.
//
// Сегмент 4K весит 10–20 МБ и на медленном маршруте качается дольше, чем
// плеер ждёт первого байта. Поэтому клиент получает байты по мере прихода,
// а в кеш сегмент встаёт только целиком: читатели идут по временному файлу
// вслед за записью.
//
// Закачка на ключ одна. Второй запрос того же сегмента — соседнее
// устройство или повтор плеера после обрыва — читает её же, а не тянет
// сегмент через VPN второй раз.

// defaultGrace — сколько закачка живёт, когда её никто не читает.
//
// Клиент бросает сегмент по трём причинам: перемотка, понижение качества
// (hls.js бросает сегмент, который не успевает) и сбой связи. В первых двух
// он за этим сегментом не вернётся, и докачивать его значит отнимать канал
// у того, что нужен сейчас. В третьей возвращается быстро: hls.js повторяет
// сразу или через 1–8 с. Короткая отсрочка ловит повтор и почти ничего не
// стоит, когда его нет.
const defaultGrace = 10 * time.Second

// ErrAbandoned — закачку бросили: её никто не читал дольше отсрочки.
var ErrAbandoned = errors.New("segcache: закачку никто не ждёт")

// Fill — идущая закачка одного сегмента.
type Fill struct {
	c     *Cache
	key   string
	meta  any
	tmp   *os.File
	abort func()

	mu sync.Mutex
	// moved закрывается на каждом продвижении и заменяется новым: так
	// читатель ждёт продолжения, не опрашивая файл.
	moved   chan struct{}
	size    int64
	done    bool
	err     error
	readers int
	idle    *time.Timer
}

// Begin заводит закачку key и подключает к ней первого читателя с
// контекстом ctx. meta раздаётся читателям как есть (шлюзу — заголовки
// ответа), кеш его не разбирает. abort обрывает источник, когда закачку
// бросают: без него брошенная закачка дотянула бы сегмент до конца.
//
// Если key уже качается — двое промахнулись одновременно, — возвращает
// читателя той закачки и nil вместо своей: свой источник вызывающий бросает.
func (c *Cache) Begin(ctx context.Context, key string, meta any, abort func()) (*Fill, *Reader, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f, ok := c.inFlight[key]; ok {
		if rd := f.join(ctx); rd != nil {
			return nil, rd, nil
		}
	}

	p := c.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return nil, nil, err
	}
	f := &Fill{c: c, key: key, meta: meta, tmp: tmp, abort: abort, moved: make(chan struct{})}
	rd := f.join(ctx)
	if rd == nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, nil, errors.New("segcache: временный файл не открылся на чтение")
	}
	c.inFlight[key] = f
	return f, rd, nil
}

// Join подключает к закачке key, если она идёт.
func (c *Cache) Join(ctx context.Context, key string) (*Reader, bool) {
	c.mu.Lock()
	f, ok := c.inFlight[key]
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	rd := f.join(ctx)
	return rd, rd != nil
}

func (f *Fill) join(ctx context.Context) *Reader {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil
	}
	// Пока закачка идёт, файл лежит под временным именем, после — уже
	// в кеше. Имя меняется под этим же замком, поэтому промаха нет.
	name := f.tmp.Name()
	if f.done {
		name = f.c.path(f.key)
	}
	file, err := os.Open(name)
	if err != nil {
		return nil
	}
	f.readers++
	if f.idle != nil {
		f.idle.Stop()
		f.idle = nil
	}
	return &Reader{f: f, file: file, ctx: ctx}
}

// From переливает src во временный файл. Скачанное целиком встаёт в кеш,
// оборванное удаляется: половина сегмента в кеше — битый поток у плеера.
func (f *Fill) From(src io.Reader) error {
	buf := make([]byte, 64<<10)
	var err error
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := f.tmp.Write(buf[:n]); werr != nil {
				err = werr
				break
			}
			if !f.advance(int64(n)) {
				break // брошена: причина уже записана
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				err = rerr
			}
			break
		}
	}
	return f.finish(err)
}

func (f *Fill) advance(n int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.size += n
	close(f.moved)
	f.moved = make(chan struct{})
	return f.err == nil
}

func (f *Fill) finish(err error) error {
	if cerr := f.tmp.Close(); err == nil {
		err = cerr
	}
	f.mu.Lock()
	if f.err != nil {
		err = f.err
	}
	if err == nil {
		err = os.Rename(f.tmp.Name(), f.c.path(f.key))
	}
	if err != nil {
		_ = os.Remove(f.tmp.Name())
		f.err = err
	}
	f.done = true
	if f.idle != nil {
		f.idle.Stop()
		f.idle = nil
	}
	close(f.moved)
	size := f.size
	f.mu.Unlock()

	f.c.mu.Lock()
	// Под этим ключом уже может идти новая закачка: эта сорвалась, и
	// следующий запрос завёл свою.
	if f.c.inFlight[f.key] == f {
		delete(f.c.inFlight, f.key)
	}
	f.c.mu.Unlock()
	if err == nil {
		f.c.wrote(size)
	}
	return err
}

// abandon бросает закачку, которую так никто и не подхватил.
func (f *Fill) abandon() {
	f.mu.Lock()
	if f.readers > 0 || f.done || f.err != nil {
		f.mu.Unlock()
		return
	}
	f.err = ErrAbandoned
	f.mu.Unlock()
	if f.abort != nil {
		f.abort()
	}
}

// Reader читает закачку вслед за записью: отдаёт то, что уже на диске, и
// ждёт продолжения, пока закачка не кончится.
type Reader struct {
	f      *Fill
	file   *os.File
	off    int64
	ctx    context.Context
	closed bool
}

// Meta — то, что закачке дали при заведении.
func (r *Reader) Meta() any { return r.f.meta }

// Read отдаёт io.EOF, только когда сегмент скачан целиком. Сорвавшаяся
// закачка — ошибка, даже если всё записанное прочитано: иначе обрубок
// сошёл бы за целый сегмент. Уход читателя (его ctx) тоже прерывает
// ожидание.
func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		r.f.mu.Lock()
		size, done, err, moved := r.f.size, r.f.done, r.f.err, r.f.moved
		r.f.mu.Unlock()
		if err != nil {
			return 0, err
		}
		if r.off < size {
			if rest := size - r.off; int64(len(p)) > rest {
				p = p[:rest]
			}
			n, err := r.file.ReadAt(p, r.off)
			r.off += int64(n)
			if n > 0 {
				return n, nil
			}
			if err == nil || err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		if done {
			return 0, io.EOF
		}
		select {
		case <-moved:
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}
}

// Close отключает читателя. Ушёл последний — закачке отсрочка: не
// подхватят за неё, закачка бросается.
func (r *Reader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	err := r.file.Close()

	f := r.f
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readers--
	if f.readers == 0 && !f.done && f.err == nil {
		f.idle = time.AfterFunc(f.c.grace, f.abandon)
	}
	return err
}
