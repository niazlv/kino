package imgcache

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Вытеснение по давности обращения.
//
// Кеш обязан иметь потолок, иначе это не кеш, а вечное хранилище: постеров
// и кадров десятки тысяч, и без предела диск кончится молча.
//
// Чистка идёт с ГИСТЕРЕЗИСОМ: запускается при достижении потолка и
// освобождает место до нижней отметки. Без него кеш у потолка тёр бы по
// файлу на каждую запись и вечно балансировал на грани.
const (
	// evictTo — доля потолка, до которой чистим. Запас в четверть даёт
	// заметный промежуток между уборками.
	evictTo = 0.75
	// touchAfter — как часто обновлять отметку обращения. Трогать файл на
	// каждое чтение значит превращать кеш в источник записи: страница
	// сериала это полтора десятка чтений разом.
	touchAfter = time.Hour
	// sweepEvery — как часто проверять размер, если записей мало.
	sweepEvery = 15 * time.Minute
)

// touch отмечает обращение, но не чаще, чем раз в touchAfter.
func (c *Cache) touch(path string) {
	st, err := os.Stat(path)
	if err != nil || time.Since(st.ModTime()) < touchAfter {
		return
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

type entry struct {
	path string
	size int64
	used time.Time
}

// Sweep удаляет самые давние записи, если кеш перерос потолок.
// Возвращает, сколько байт освободил.
func (c *Cache) Sweep() (int64, error) {
	if c.maxBytes <= 0 {
		return 0, nil
	}
	var total int64
	var items []entry

	err := filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".t") || strings.Contains(d.Name(), ".tmp-") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		items = append(items, entry{path: p, size: info.Size(), used: info.ModTime()})
		return nil
	})
	if err != nil {
		return 0, err
	}
	if total <= c.maxBytes {
		return 0, nil
	}

	// Самые давно не спрашиваемые уходят первыми.
	sort.Slice(items, func(i, j int) bool { return items[i].used.Before(items[j].used) })

	target := int64(float64(c.maxBytes) * evictTo)
	var freed int64
	for _, it := range items {
		if total <= target {
			break
		}
		if os.Remove(it.path) == nil {
			_ = os.Remove(it.path + ".t")
			total -= it.size
			freed += it.size
		}
	}
	return freed, nil
}

// StartSweeper запускает фоновую уборку до отмены контекста.
//
// Проверка по времени, а не только по числу записей: кеш может перерасти
// потолок и без новых записей — например, если потолок уменьшили.
func (c *Cache) StartSweeper(stop <-chan struct{}, onSweep func(freed int64)) {
	go func() {
		t := time.NewTicker(sweepEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if freed, err := c.Sweep(); err == nil && freed > 0 && onSweep != nil {
					onSweep(freed)
				}
			}
		}
	}()
}

// maybeSweep запускает уборку, когда с прошлой накопилось достаточно записей.
// Считаем записи, а не байты: точный размер требует обхода каталога.
func (c *Cache) maybeSweep() {
	const writesBetweenChecks = 200
	if atomic.AddInt64(&c.writes, 1)%writesBetweenChecks != 0 {
		return
	}
	go func() { _, _ = c.Sweep() }()
}
