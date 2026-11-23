package segcache

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func newCache(t *testing.T) *Cache {
	t.Helper()
	c, err := New(t.TempDir(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// files — всё, что лежит в каталоге кеша, включая временные файлы.
func files(t *testing.T, c *Cache) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, d.Name())
		}
		return nil
	})
	return out
}

// readN читает ровно n байт, не дольше секунды: зависание здесь означает,
// что читатель ждёт конца закачки, а не идёт вслед за ней.
func readN(t *testing.T, r io.Reader, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(r, buf); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("чтение: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("читатель не получил уже записанное")
	}
	return buf
}

// key — ключ как у шлюза: хеш адреса.
func key(name string) string {
	k, _ := Key("https://cdn.example/hls/"+name+".ts", "")
	return k
}

func run(f *Fill, src io.Reader) <-chan error {
	done := make(chan error, 1)
	go func() { done <- f.From(src) }()
	return done
}

var seg = bytes.Repeat([]byte("0123456789abcdef"), 16<<10) // 256 КБ

// Читатели получают записанное сразу, а не после закачки; в кеш сегмент
// встаёт только целиком.
func TestFill_ReadersFollowWrites(t *testing.T) {
	c := newCache(t)
	pr, pw := io.Pipe()
	f, first, err := c.Begin(context.Background(), key("k1"), "meta", nil)
	if err != nil || f == nil {
		t.Fatalf("Begin: %v", err)
	}
	done := run(f, pr)

	half := len(seg) / 2
	go func() { _, _ = pw.Write(seg[:half]) }()
	if got := readN(t, first, half); !bytes.Equal(got, seg[:half]) {
		t.Fatal("первая половина не та")
	}
	if _, _, hit := c.Open(key("k1")); hit {
		t.Fatal("недокачанный сегмент уже в кеше")
	}

	// Второй запрос того же сегмента подхватывает закачку с начала.
	second, ok := c.Join(context.Background(), key("k1"))
	if !ok || second.Meta() != "meta" {
		t.Fatal("к идущей закачке не подключиться")
	}
	if got := readN(t, second, half); !bytes.Equal(got, seg[:half]) {
		t.Fatal("подключившийся читает не с начала")
	}

	go func() { _, _ = pw.Write(seg[half:]); _ = pw.Close() }()
	for _, rd := range []*Reader{first, second} {
		rest, err := io.ReadAll(rd)
		if err != nil || !bytes.Equal(rest, seg[half:]) {
			t.Fatalf("хвост: %v, %d байт", err, len(rest))
		}
		_ = rd.Close()
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if fh, size, hit := c.Open(key("k1")); !hit || size != int64(len(seg)) {
		t.Fatalf("в кеше: %v, %d байт", hit, size)
	} else {
		_ = fh.Close()
	}
	if got := files(t, c); len(got) != 1 {
		t.Fatalf("в каталоге лишнее: %v", got)
	}
	if _, ok := c.Join(context.Background(), key("k1")); ok {
		t.Fatal("кончившаяся закачка всё ещё числится идущей")
	}
}

// Оборванный источник не оставляет в кеше ничего, а читатель получает
// ошибку, а не EOF: иначе обрубок сошёл бы за целый сегмент.
func TestFill_BrokenSourceLeavesNothing(t *testing.T) {
	c := newCache(t)
	pr, pw := io.Pipe()
	f, rd, err := c.Begin(context.Background(), key("k2"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := run(f, pr)

	boom := errors.New("апстрим оборвался")
	go func() { _, _ = pw.Write(seg[:1000]); pw.CloseWithError(boom) }()
	if _, err := io.ReadAll(rd); !errors.Is(err, boom) {
		t.Fatalf("читатель: %v, ждали обрыв", err)
	}
	_ = rd.Close()
	if err := <-done; !errors.Is(err, boom) {
		t.Fatalf("From: %v", err)
	}
	if got := files(t, c); len(got) != 0 {
		t.Fatalf("после обрыва остались файлы: %v", got)
	}
}

// Закачку, которую никто не читает дольше отсрочки, бросают: источник
// обрывается, файла не остаётся.
func TestFill_AbandonedWithoutReaders(t *testing.T) {
	c := newCache(t)
	c.grace = 20 * time.Millisecond
	pr, pw := io.Pipe()
	var aborted atomic.Bool
	abort := func() { aborted.Store(true); pw.CloseWithError(context.Canceled) }

	f, rd, err := c.Begin(context.Background(), key("k3"), nil, abort)
	if err != nil {
		t.Fatal(err)
	}
	done := run(f, pr)
	go func() { _, _ = pw.Write(seg[:1000]) }()
	readN(t, rd, 1000)
	_ = rd.Close()

	select {
	case err := <-done:
		if !errors.Is(err, ErrAbandoned) {
			t.Fatalf("From: %v, ждали ErrAbandoned", err)
		}
	case <-time.After(time.Second):
		t.Fatal("брошенная закачка не остановилась")
	}
	if !aborted.Load() {
		t.Fatal("источник не оборван")
	}
	if got := files(t, c); len(got) != 0 {
		t.Fatalf("после брошенной закачки остались файлы: %v", got)
	}
}

// Повтор в пределах отсрочки подхватывает закачку: сегмент докачивается,
// а не начинается заново.
func TestFill_RejoinWithinGrace(t *testing.T) {
	c := newCache(t)
	c.grace = time.Hour
	pr, pw := io.Pipe()
	f, rd, err := c.Begin(context.Background(), key("k4"), nil, func() { t.Error("закачку бросили при читателе") })
	if err != nil {
		t.Fatal(err)
	}
	done := run(f, pr)
	go func() { _, _ = pw.Write(seg[:1000]) }()
	readN(t, rd, 1000)
	_ = rd.Close() // клиент ушёл

	again, ok := c.Join(context.Background(), key("k4"))
	if !ok {
		t.Fatal("повтор не подхватил закачку")
	}
	go func() { _, _ = pw.Write(seg[1000:]); _ = pw.Close() }()
	all, err := io.ReadAll(again)
	if err != nil || !bytes.Equal(all, seg) {
		t.Fatalf("повтор: %v, %d байт", err, len(all))
	}
	_ = again.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Читатель, чей клиент ушёл, не висит до следующей порции закачки.
func TestFill_ReaderStopsWithContext(t *testing.T) {
	c := newCache(t)
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	f, rd, err := c.Begin(ctx, key("k5"), nil, func() { pw.CloseWithError(context.Canceled) })
	if err != nil {
		t.Fatal(err)
	}
	done := run(f, pr)

	got := make(chan error, 1)
	go func() { _, err := rd.Read(make([]byte, 10)); got <- err }()
	cancel()
	select {
	case err := <-got:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Read: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("читатель ушедшего клиента висит")
	}
	_ = rd.Close()
	_ = pw.CloseWithError(errors.New("конец теста"))
	<-done
}

// Двое промахнулись одновременно: второй читает закачку первого, а не
// заводит свою.
func TestBegin_SecondJoinsFirst(t *testing.T) {
	c := newCache(t)
	pr, pw := io.Pipe()
	f, a, err := c.Begin(context.Background(), key("k6"), "первый", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := run(f, pr)

	g, b, err := c.Begin(context.Background(), key("k6"), "второй", nil)
	if err != nil || g != nil || b.Meta() != "первый" {
		t.Fatalf("вторая закачка того же ключа: %v %v", g, err)
	}
	go func() { _, _ = pw.Write(seg); _ = pw.Close() }()
	for _, rd := range []*Reader{a, b} {
		if all, err := io.ReadAll(rd); err != nil || !bytes.Equal(all, seg) {
			t.Fatalf("%v, %d байт", err, len(all))
		}
		_ = rd.Close()
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Временный файл старше часа — остаток оборванного процесса; уборка его
// удаляет, свежий не трогает.
func TestSweep_RemovesStaleTemp(t *testing.T) {
	c, err := New(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(c.dir, ".tmp-old")
	fresh := filepath.Join(c.dir, ".tmp-new")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * staleTemp)
	_ = os.Chtimes(stale, old, old)
	if _, err := c.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("старый временный файл не удалён")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("свежий временный файл удалён — идущая закачка сломалась бы")
	}
}
