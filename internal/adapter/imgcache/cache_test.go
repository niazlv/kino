package imgcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fill(t *testing.T, c *Cache, key string, size int, age time.Duration) {
	t.Helper()
	if err := c.Put(key, make([]byte, size), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	// Возраст задаём явно: вытеснение опирается на время обращения,
	// и без этого все записи в тесте одногодки.
	p := c.path(key)
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
}

func totalBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

func TestSweep_EvictsLeastRecentlyUsed(t *testing.T) {
	const kb = 1024
	c, err := New(t.TempDir(), 10*kb)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, c, "старый", 4*kb, 72*time.Hour)
	fill(t, c, "средний", 4*kb, 24*time.Hour)
	fill(t, c, "свежий", 4*kb, time.Minute)

	freed, err := c.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if freed == 0 {
		t.Fatal("превышение потолка не вызвало уборку")
	}
	if _, _, ok := c.Get("свежий"); !ok {
		t.Error("свежая запись удалена — вытеснение идёт не по давности")
	}
	if _, _, ok := c.Get("старый"); ok {
		t.Error("самая давняя запись осталась")
	}
}

// Гистерезис: чистим не «ровно до потолка», а с запасом, иначе кеш у
// границы тёр бы по файлу на каждую запись.
func TestSweep_LeavesHeadroom(t *testing.T) {
	const kb = 1024
	dir := t.TempDir()
	c, err := New(dir, 10*kb)
	if err != nil {
		t.Fatal(err)
	}
	for i, age := range []time.Duration{96, 72, 48, 24, 1} {
		fill(t, c, string(rune('a'+i)), 3*kb, age*time.Hour)
	}
	if _, err := c.Sweep(); err != nil {
		t.Fatal(err)
	}
	got := totalBytes(t, dir)
	want := int64(float64(10*kb) * evictTo)
	if got > want {
		t.Fatalf("после уборки %d байт, ожидалось не больше %d", got, want)
	}
}

func TestSweep_NoopBelowLimit(t *testing.T) {
	c, err := New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, c, "один", 1024, time.Hour)
	freed, err := c.Sweep()
	if err != nil || freed != 0 {
		t.Fatalf("под потолком уборки быть не должно: freed=%d err=%v", freed, err)
	}
	if _, _, ok := c.Get("один"); !ok {
		t.Fatal("запись пропала без причины")
	}
}

// Одновременные запросы одного кадра должны сходить к источнику один раз:
// на странице сериала карточек полтора десятка.
func TestDo_CollapsesConcurrentFetches(t *testing.T) {
	c, err := New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	calls := make(chan struct{}, 16)
	fetch := func() ([]byte, string, error) {
		calls <- struct{}{}
		time.Sleep(40 * time.Millisecond)
		return []byte("картинка"), "image/jpeg", nil
	}
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() { _, _, _ = c.Do("общий", fetch); done <- struct{}{} }()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if n := len(calls); n != 1 {
		t.Fatalf("походов к источнику %d, ожидался 1", n)
	}
}
