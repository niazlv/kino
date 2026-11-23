// Package segcache — дисковый кеш видеосегментов.
//
// Сегменты неизменяемы, поэтому кешируются хорошо: перемотка назад и
// повторный просмотр перестают ходить через VPN. Первый просмотр он не
// ускоряет — это надо понимать, берясь за него. Но и не замедляет: сегмент
// отдаётся клиенту по мере закачки, а не после неё (fill.go).
//
// От кеша картинок отличается двумя вещами: файлы здесь мегабайтные,
// поэтому отдаются потоком, а не через память; и ключ строится БЕЗ подписи,
// иначе попаданий не будет вовсе.
package segcache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// evictTo — до какой доли потолка чистим. Запас нужен, чтобы кеш
	// у границы не тёр по файлу на каждую запись.
	evictTo = 0.8
	// touchAfter — как часто обновлять отметку обращения. Сегмент читают
	// подряд, и трогать файл каждый раз значит писать вместо чтения.
	touchAfter = 30 * time.Minute
	sweepEvery = 10 * time.Minute
	// checksPerCap — уборка по объёму записанного, раз на десятую долю
	// потолка: точный размер требует обхода каталога. Считать записи нельзя:
	// сегмент 4K в пять раз тяжелее обычного, и полсотни записей между
	// проверками — это гигабайт сверх потолка на диске, где и так тесно.
	checksPerCap = 10
	// staleTemp — временный файл старше этого остался от оборванного
	// процесса: закачка живёт минуты, а перезапуск шлюза посреди неё
	// оставляет файл, который никто не удалит.
	staleTemp = time.Hour
)

type Cache struct {
	dir      string
	maxBytes int64
	// sinceSweep — байт записано с прошлой проверки размера.
	sinceSweep int64
	grace      time.Duration

	mu       sync.Mutex
	inFlight map[string]*Fill
}

func New(dir string, maxBytes int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Cache{dir: dir, maxBytes: maxBytes, grace: defaultGrace, inFlight: make(map[string]*Fill)}, nil
}

// Enabled сообщает, включён ли кеш. Нулевой потолок означает «выключен»:
// сегменты весят мегабайты, и включать это молча нельзя.
func (c *Cache) Enabled() bool { return c != nil && c.maxBytes > 0 }

// Key строит ключ, устойчивый к смене подписи.
//
// Подписанные ссылки на один и тот же сегмент различаются в каждом тикете,
// но ГДЕ именно сидит подпись, зависит от площадки: у одной в query,
// у другой отдельным сегментом пути. Правило приходит из тикета, поэтому
// здесь нет ни одного знания о конкретных площадках.
//
// pattern — регулярное выражение с одной группой захвата, выделяющей
// устойчивую часть. Пустое или несовпавшее означает «берём хост и путь»:
// это безопасно, просто промахов будет больше.
func Key(raw, pattern string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Path == "" {
		return "", false
	}
	base := u.Host + u.Path
	if pattern != "" {
		if re, err := compiled(pattern); err == nil {
			// Сопоставляем БЕЗ query: он у подписанных ссылок тоже
			// меняется, а жадный хвост правила утащил бы его в ключ
			// и промахи остались бы на месте.
			clean := u.Scheme + "://" + u.Host + u.Path
			if m := re.FindStringSubmatch(clean); len(m) > 1 {
				// Ключом служит ТОЛЬКО захваченный хвост, без хоста.
				// Измерено: площадка выдаёт свой uuid в поддомене на каждый
				// тикет, так что хост в ключе означал бы промах всегда.
				// Хвост содержит имя файла контента и уникален сам по себе.
				base = m[1]
			}
		}
	}
	sum := sha256.Sum256([]byte(base))
	return hex.EncodeToString(sum[:]), true
}

// compiled кеширует разобранные выражения: они приходят с каждым сегментом,
// а компиляция регулярки заметно дороже её применения.
var reCache sync.Map

func compiled(pattern string) (*regexp.Regexp, error) {
	if v, ok := reCache.Load(pattern); ok {
		if re, ok := v.(*regexp.Regexp); ok {
			return re, nil
		}
		return nil, errors.New("segcache: битое выражение")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		reCache.Store(pattern, err)
		return nil, err
	}
	reCache.Store(pattern, re)
	return re, nil
}

func (c *Cache) path(key string) string {
	return filepath.Join(c.dir, key[:2], key[2:4], key)
}

// Open отдаёт файл из кеша вместе с размером.
func (c *Cache) Open(key string) (*os.File, int64, bool) {
	p := c.path(key)
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, false
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, false
	}
	c.touch(p, st.ModTime())
	return f, st.Size(), true
}

// Store сохраняет поток на диск. Возвращает открытый файл для отдачи, чтобы
// вызывающему не пришлось читать его повторно.
//
// Запись через временный файл: оборванная загрузка не должна оставить
// в кеше половину сегмента — плеер получил бы битый поток.
func (c *Cache) Store(key string, r io.Reader) (*os.File, int64, error) {
	p := c.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return nil, 0, err
	}
	defer os.Remove(tmp.Name())

	n, err := io.Copy(tmp, r)
	if err != nil {
		_ = tmp.Close()
		return nil, 0, err
	}
	if err := tmp.Close(); err != nil {
		return nil, 0, err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return nil, 0, err
	}
	c.wrote(n)

	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	return f, n, nil
}

func (c *Cache) touch(path string, mod time.Time) {
	if time.Since(mod) < touchAfter {
		return
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

func (c *Cache) wrote(n int64) {
	step := c.maxBytes / checksPerCap
	if atomic.AddInt64(&c.sinceSweep, n) < step {
		return
	}
	// Счётчик обнуляет кто-то один: иначе две записи на границе запустили
	// бы по обходу каждая.
	if atomic.SwapInt64(&c.sinceSweep, 0) < step {
		return
	}
	go func() { _, _ = c.Sweep() }()
}

// Sweep вытесняет самые давно не спрашиваемые сегменты.
func (c *Cache) Sweep() (int64, error) {
	if c.maxBytes <= 0 {
		return 0, nil
	}
	type entry struct {
		path string
		size int64
		used time.Time
	}
	var total int64
	var items []entry

	err := filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if strings.Contains(d.Name(), ".tmp-") {
			if time.Since(info.ModTime()) > staleTemp {
				_ = os.Remove(p)
			}
			return nil
		}
		total += info.Size()
		items = append(items, entry{p, info.Size(), info.ModTime()})
		return nil
	})
	if err != nil {
		return 0, err
	}
	if total <= c.maxBytes {
		return 0, nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].used.Before(items[j].used) })

	target := int64(float64(c.maxBytes) * evictTo)
	var freed int64
	for _, it := range items {
		if total <= target {
			break
		}
		if os.Remove(it.path) == nil {
			total -= it.size
			freed += it.size
		}
	}
	return freed, nil
}

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

var errDisabled = errors.New("segcache: выключен")

// ErrDisabled возвращается, когда кеш не настроен.
func ErrDisabled() error { return errDisabled }
